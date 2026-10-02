package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/frostyard/repogen/internal/intake"
	"github.com/frostyard/repogen/internal/objectstore/r2"
	"github.com/frostyard/repogen/internal/production"
	"github.com/spf13/cobra"
)

// newSubmitIntakeAPI builds the R2 client for submit-intake; tests replace it.
var newSubmitIntakeAPI = func(config production.SubmitConfig, credentials r2.Credentials) (r2.API, error) {
	return r2.NewClient(config.Endpoint, config.Region, credentials, nil)
}

// NewSubmitIntakeCmd returns the submit-only producer command that records one
// trixie request, its provenance and its artifacts in the durable intake.
func NewSubmitIntakeCmd() *cobra.Command {
	var configFile, configSHA256, policySHA256, requestFile, provenanceFile string
	var artifactsDir, submissionKey, credentialsFile string

	command := &cobra.Command{
		Use:   "submit-intake",
		Short: "Submit one Trixie request, provenance and artifacts to the R2 intake",
		Long: `Submits one producer request to the durable R2 intake: artifacts and
provenance as digest-addressed blobs, then the request, submission pointer and
a sequenced receipt binding --policy-sha256. Every write is create-if-absent
under the intake producer prefixes; nothing is replaced or deleted and nothing
is written to the publication bucket. The configuration is digest-pinned and
fixes kind debian, target trixie and the one allowed producer. Credentials come
only from an explicit mode-0600 file. The receipt is printed as canonical JSON.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			for _, flag := range []string{
				"config", "config-sha256", "policy-sha256", "request",
				"provenance", "artifacts-dir", "submission-key", "credentials-file",
			} {
				if !command.Flags().Changed(flag) {
					return invalidProductionConfig("--%s must be provided explicitly", flag)
				}
			}
			var config production.SubmitConfig
			if _, err := production.LoadCanonicalFile(configFile, configSHA256, &config); err != nil {
				return invalidProductionConfig("load exact intake-submit config: %v", err)
			}
			if err := config.Validate(); err != nil {
				return invalidProductionConfig("%v", err)
			}
			requestData, err := readRegularFile(requestFile, 4<<20)
			if err != nil {
				return invalidProductionConfig("read request: %v", err)
			}
			var request intake.Request
			if err := intake.DecodeCanonical(requestData, &request); err != nil {
				return invalidProductionConfig("request is not canonical: %v", err)
			}
			if request.Producer != config.Producer || request.Kind != config.Kind {
				return invalidProductionConfig("request producer or kind does not match the pinned config")
			}
			provenance, err := readRegularFile(provenanceFile, 4<<20)
			if err != nil {
				return invalidProductionConfig("read provenance: %v", err)
			}
			artifacts, err := collectArtifacts(artifactsDir)
			if err != nil {
				return invalidProductionConfig("%v", err)
			}

			credentials, err := r2.LoadCredentialsFile(credentialsFile)
			if err != nil {
				return productionStateError("%v", err)
			}
			client, err := newSubmitIntakeAPI(config, credentials)
			if err != nil {
				return productionStateError("%v", err)
			}
			locker, err := r2.NewLocker(client, config.CoordinationBucket, config.CoordinationPrefix)
			if err != nil {
				return productionStateError("%v", err)
			}
			store, err := r2.NewIntakeStore(client, config.IntakeBucket, config.IntakePrefix, locker)
			if err != nil {
				return productionStateError("%v", err)
			}
			receipt, err := intake.Submit(command.Context(), store, intake.Submission{
				Principal:     config.Producer,
				SubmissionKey: submissionKey,
				PolicySHA256:  policySHA256,
				Request:       request,
				Provenance:    provenance,
				Artifacts:     artifacts,
			})
			if err != nil {
				return productionStateError("submit: %v", err)
			}
			encoded, err := intake.CanonicalJSON(receipt)
			if err != nil {
				return productionStateError("%v", err)
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), string(encoded))
			return err
		},
	}
	flags := command.Flags()
	flags.StringVar(&configFile, "config", "", "Canonical intake-submit configuration")
	flags.StringVar(&configSHA256, "config-sha256", "", "Exact intake-submit configuration SHA-256")
	flags.StringVar(&policySHA256, "policy-sha256", "", "Authorization policy SHA-256 bound into the receipt")
	flags.StringVar(&requestFile, "request", "", "Canonical request JSON")
	flags.StringVar(&provenanceFile, "provenance", "", "Canonical provenance JSON")
	flags.StringVar(&artifactsDir, "artifacts-dir", "", "Directory holding exactly the request's artifacts")
	flags.StringVar(&submissionKey, "submission-key", "", "Idempotency key for this submission")
	flags.StringVar(&credentialsFile, "credentials-file", "", "Explicit mode-0600 R2 credentials JSON")
	return command
}

func readRegularFile(filename string, limit int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular non-symlink file no larger than %d bytes", filename, limit)
	}
	return os.ReadFile(filename)
}

// collectArtifacts lists a flat directory of regular non-symlink files. The
// intake hashes each one and requires the set to equal the request digests.
func collectArtifacts(directory string) ([]intake.Artifact, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, fmt.Errorf("inspect artifacts directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("artifacts directory must be a real directory, not a symlink")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read artifacts directory: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	artifacts := make([]intake.Artifact, 0, len(entries))
	for _, entry := range entries {
		filename := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("artifact %s must be a regular file, not a symlink or directory", entry.Name())
		}
		digest, err := fileSHA256(filename)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, intake.Artifact{
			SHA256: digest,
			Size:   info.Size(),
			Open:   openNoFollow(filename),
		})
	}
	return artifacts, nil
}

func fileSHA256(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	return digestReader(file)
}

func openNoFollow(filename string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("artifact %s changed into a non-regular file", filepath.Base(filename))
		}
		return os.Open(filename)
	}
}

func digestReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
