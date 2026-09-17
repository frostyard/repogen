package production

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/intake"
)

const (
	ConfigSchema = "org.frostyard.repogen.production-config.v1"
	PolicySchema = "org.frostyard.repogen.authorization-policy.v1"
)

type Config struct {
	Schema             string `json:"schema"`
	AccountID          string `json:"account_id"`
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	PublicationBucket  string `json:"publication_bucket"`
	PublicationPrefix  string `json:"publication_prefix"`
	IntakeBucket       string `json:"intake_bucket"`
	IntakePrefix       string `json:"intake_prefix"`
	CoordinationBucket string `json:"coordination_bucket"`
	CoordinationPrefix string `json:"coordination_prefix"`
	Target             string `json:"target"`
	RequestSHA256      string `json:"request_sha256"`
}

type ApprovedObject struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Policy struct {
	Schema                     string           `json:"schema"`
	Operator                   string           `json:"operator"`
	Target                     string           `json:"target"`
	Operation                  string           `json:"operation"`
	RequestSHA256              string           `json:"request_sha256"`
	ProvenanceSHA256           string           `json:"provenance_sha256"`
	Producer                   string           `json:"producer"`
	ProducerCommit             string           `json:"producer_commit"`
	ProducerTree               string           `json:"producer_tree"`
	ActionCommit               string           `json:"action_commit"`
	RepogenVersion             string           `json:"repogen_version"`
	RepogenSHA256              string           `json:"repogen_sha256"`
	SigningKeyFingerprint      string           `json:"signing_key_fingerprint"`
	SigningPublicKeySHA256     string           `json:"signing_public_key_sha256"`
	ReleaseTime                string           `json:"release_time"`
	AllowedPoolObjects         []ApprovedObject `json:"allowed_pool_objects"`
	AuthoritativeTargetAbsent  bool             `json:"authoritative_target_absent"`
	ProviderPermissionEvidence string           `json:"provider_permission_evidence"`
}

func LoadCanonicalFile(filename, expectedSHA256 string, destination any) ([]byte, error) {
	if !validSHA256(expectedSHA256) {
		return nil, fmt.Errorf("expected canonical file digest is invalid")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, fmt.Errorf("inspect canonical file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4<<20 {
		return nil, fmt.Errorf("canonical file must be a regular file no larger than 4 MiB")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("read canonical file: %w", err)
	}
	if digest(data) != expectedSHA256 {
		return nil, fmt.Errorf("canonical file digest does not match the explicit pin")
	}
	if err := intake.DecodeCanonical(data, destination); err != nil {
		return nil, err
	}
	return data, nil
}

func (c Config) Validate() error {
	expectedEndpoint := "https://" + c.AccountID + ".r2.cloudflarestorage.com"
	parsed, err := url.Parse(c.Endpoint)
	if c.Schema != ConfigSchema || !safeName(c.AccountID) ||
		c.Endpoint != expectedEndpoint || err != nil || parsed.Host == "" ||
		c.Region != "auto" || !safeName(c.PublicationBucket) ||
		!safeName(c.IntakeBucket) || !safeName(c.CoordinationBucket) ||
		!safePrefix(c.PublicationPrefix) || !safePrefix(c.IntakePrefix) ||
		!safePrefix(c.CoordinationPrefix) || c.Target != "trixie" ||
		!validSHA256(c.RequestSHA256) {
		return fmt.Errorf("invalid exact production configuration")
	}
	return nil
}

func (p Policy) Validate(config Config) error {
	if p.Schema != PolicySchema || !safeIdentity(p.Operator) ||
		p.Target != config.Target || p.Operation != "initialize" ||
		p.RequestSHA256 != config.RequestSHA256 ||
		!validSHA256(p.ProvenanceSHA256) || !safeIdentity(p.Producer) ||
		!validCommit(p.ProducerCommit) || !validCommit(p.ProducerTree) ||
		!validCommit(p.ActionCommit) || !safeIdentity(p.RepogenVersion) ||
		!validSHA256(p.RepogenSHA256) || !validFingerprint(p.SigningKeyFingerprint) ||
		!validSHA256(p.SigningPublicKeySHA256) ||
		p.ProviderPermissionEvidence == "" || !p.AuthoritativeTargetAbsent {
		return fmt.Errorf("invalid exact production authorization policy")
	}
	releaseTime, err := time.Parse(time.RFC3339, p.ReleaseTime)
	if err != nil || releaseTime.Location() != time.UTC {
		return fmt.Errorf("authorization policy release_time must be RFC3339 UTC")
	}
	if len(p.AllowedPoolObjects) == 0 {
		return fmt.Errorf("authorization policy has no approved pool objects")
	}
	seen := make(map[string]struct{}, len(p.AllowedPoolObjects))
	for _, object := range p.AllowedPoolObjects {
		if !strings.HasPrefix(object.Path, "pool/main/") ||
			path.Clean(object.Path) != object.Path || !validSHA256(object.SHA256) ||
			object.Size < 0 {
			return fmt.Errorf("authorization policy contains an invalid pool object")
		}
		if _, duplicate := seen[object.Path]; duplicate {
			return fmt.Errorf("authorization policy repeats a pool object")
		}
		seen[object.Path] = struct{}{}
	}
	return nil
}

func (p Policy) PoolDigests() map[string]deb.PoolDigest {
	result := make(map[string]deb.PoolDigest, len(p.AllowedPoolObjects))
	for _, object := range p.AllowedPoolObjects {
		result[object.Path] = deb.PoolDigest{
			SHA256: object.SHA256,
			Size:   object.Size,
		}
	}
	return result
}

func digest(data []byte) string {
	value := sha256.Sum256(data)
	return hex.EncodeToString(value[:])
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validCommit(value string) bool {
	return len(value) == 40 && strings.ToLower(value) == value && validHex(value)
}

func validFingerprint(value string) bool {
	return len(value) == 40 && strings.ToUpper(value) == value && validHex(value)
}

func validHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func safeName(value string) bool {
	return value != "" && value != "." && value != ".." &&
		len(value) <= 256 && !strings.ContainsAny(value, "/\\\x00\r\n\t ")
}

func safePrefix(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !safeName(segment) {
			return false
		}
	}
	return true
}

func safeIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._-/+:@", character) {
			continue
		}
		return false
	}
	return true
}
