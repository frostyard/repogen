package cli

import (
	"fmt"
	"os"

	"github.com/frostyard/repogen/internal/buildinfo"
	"github.com/frostyard/repogen/internal/objectstore/r2"
	"github.com/frostyard/repogen/internal/production"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/spf13/cobra"
)

func NewReconcileProductionCmd() *cobra.Command {
	var configFile string
	var configSHA256 string
	var policyFile string
	var policySHA256 string
	var requestSHA256 string
	var provenanceSHA256 string
	var credentialsFile string
	var signingKeyFile string
	var signingPassphraseFile string

	command := &cobra.Command{
		Use:   "reconcile-production",
		Short: "Reconcile one exact authorized Trixie receipt through R2",
		Long: `Reconciles one exact retained Trixie receipt through the durable intake
and recovery graph. Every nonsecret configuration and authorization byte is
digest-pinned. Credentials and signing material come only from explicit local
mode-0600 files. The command exposes no delete, sync, copy, multipart, cache,
stable, other-codename, or sysext operation.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (retErr error) {
			for _, flag := range []string{
				"config", "config-sha256", "policy", "policy-sha256",
				"request-sha256", "provenance-sha256", "credentials-file",
				"signing-key",
			} {
				if !command.Flags().Changed(flag) {
					return invalidProductionConfig("--%s must be provided explicitly", flag)
				}
			}

			var config production.Config
			if _, err := production.LoadCanonicalFile(
				configFile,
				configSHA256,
				&config,
			); err != nil {
				return invalidProductionConfig("load exact production config: %v", err)
			}
			if err := config.Validate(); err != nil {
				return invalidProductionConfig("%v", err)
			}
			var policy production.Policy
			if _, err := production.LoadCanonicalFile(
				policyFile,
				policySHA256,
				&policy,
			); err != nil {
				return invalidProductionConfig("load exact authorization policy: %v", err)
			}
			if err := policy.Validate(config); err != nil {
				return invalidProductionConfig("%v", err)
			}

			credentials, err := r2.LoadCredentialsFile(credentialsFile)
			if err != nil {
				return productionStateError("%v", err)
			}
			client, err := r2.NewClient(config.Endpoint, config.Region, credentials, nil)
			if err != nil {
				return productionStateError("%v", err)
			}
			locker, err := r2.NewLocker(
				client,
				config.CoordinationBucket,
				config.CoordinationPrefix,
			)
			if err != nil {
				return productionStateError("%v", err)
			}
			intakeStore, err := r2.NewIntakeStore(
				client,
				config.IntakeBucket,
				config.IntakePrefix,
				locker,
			)
			if err != nil {
				return productionStateError("%v", err)
			}
			publicationStore, err := r2.NewPublicationStore(r2.Target{
				Client:            client,
				Bucket:            config.PublicationBucket,
				Prefix:            config.PublicationPrefix,
				Codename:          config.Target,
				AllowedPoolObject: policy.PoolDigests(),
				Locker:            locker,
			})
			if err != nil {
				return productionStateError("%v", err)
			}

			passphrase, err := loadSecretFile(signingPassphraseFile)
			if err != nil {
				return productionStateError("load signing passphrase: %v", err)
			}
			gpgSigner, err := signer.NewGPGSigner(signingKeyFile, string(passphrase))
			for index := range passphrase {
				passphrase[index] = 0
			}
			if err != nil {
				return productionStateError("initialize explicit signer: %v", err)
			}
			defer func() {
				if err := gpgSigner.Close(); retErr == nil && err != nil {
					retErr = productionStateError("close signer: %v", err)
				}
			}()

			workDir, err := os.MkdirTemp("", "repogen-production-*")
			if err != nil {
				return productionStateError("create private production workspace: %v", err)
			}
			if err := os.Chmod(workDir, 0o700); err != nil {
				_ = os.RemoveAll(workDir)
				return productionStateError("protect production workspace: %v", err)
			}
			defer func() {
				if err := os.RemoveAll(workDir); retErr == nil && err != nil {
					retErr = productionStateError("remove private production workspace: %v", err)
				}
			}()
			executable, err := os.Executable()
			if err != nil {
				return productionStateError("resolve running executable: %v", err)
			}
			return (production.Execution{
				Config:         config,
				ConfigSHA256:   configSHA256,
				Policy:         policy,
				PolicySHA256:   policySHA256,
				RequestSHA256:  requestSHA256,
				ProvenanceHash: provenanceSHA256,
				Intake:         intakeStore,
				Publication:    publicationStore,
				Signer:         gpgSigner,
				WorkDir:        workDir,
				ExecutablePath: executable,
				Version:        buildinfo.Version,
				Commit:         buildinfo.Commit,
			}).Run(command.Context())
		},
	}
	command.Flags().StringVar(&configFile, "config", "", "Canonical nonsecret production configuration")
	command.Flags().StringVar(&configSHA256, "config-sha256", "", "Exact production configuration SHA-256")
	command.Flags().StringVar(&policyFile, "policy", "", "Canonical exact authorization policy")
	command.Flags().StringVar(&policySHA256, "policy-sha256", "", "Exact authorization policy SHA-256")
	command.Flags().StringVar(&requestSHA256, "request-sha256", "", "Exact retained request SHA-256")
	command.Flags().StringVar(&provenanceSHA256, "provenance-sha256", "", "Exact retained provenance SHA-256")
	command.Flags().StringVar(&credentialsFile, "credentials-file", "", "Explicit mode-0600 R2 credentials JSON")
	command.Flags().StringVar(&signingKeyFile, "signing-key", "", "Explicit private signing key file")
	command.Flags().StringVar(
		&signingPassphraseFile,
		"signing-passphrase-file",
		"",
		"Optional explicit mode-0600 signing passphrase file",
	)
	return command
}

func loadSecretFile(filename string) ([]byte, error) {
	if filename == "" {
		return nil, nil
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm() != 0o600 || info.Size() > 64<<10 {
		return nil, fmt.Errorf("secret file must be a regular mode-0600 file no larger than 64 KiB")
	}
	value, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	for len(value) > 0 && (value[len(value)-1] == '\n' || value[len(value)-1] == '\r') {
		value = value[:len(value)-1]
	}
	return value, nil
}
