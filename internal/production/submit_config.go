package production

import (
	"fmt"
	"strings"
)

// SubmitConfigSchema identifies the canonical intake-submit configuration.
const SubmitConfigSchema = "org.frostyard.repogen.intake-submit-config.v1"

// PublicationBucket is the Frostyard package-repository bucket. It is fixed
// here, not trusted from a producer-supplied configuration, so that a
// configuration naming a decoy publication bucket cannot direct intake writes
// at the real one.
const PublicationBucket = "frostyardrepo"

// SubmitConfig is the digest-pinned, nonsecret configuration of
// `repogen submit-intake`. It fixes the R2 account, the intake and
// coordination buckets, the kind and target, and the one producer allowed to
// submit. PublicationBucket is named only so that it can be refused: the
// submit path never writes to it.
type SubmitConfig struct {
	Schema             string `json:"schema"`
	AccountID          string `json:"account_id"`
	Endpoint           string `json:"endpoint"`
	Region             string `json:"region"`
	IntakeBucket       string `json:"intake_bucket"`
	IntakePrefix       string `json:"intake_prefix"`
	CoordinationBucket string `json:"coordination_bucket"`
	CoordinationPrefix string `json:"coordination_prefix"`
	PublicationBucket  string `json:"publication_bucket"`
	Kind               string `json:"kind"`
	Target             string `json:"target"`
	Producer           string `json:"producer"`
}

// Validate refuses any submit configuration that is not exactly a Debian
// trixie intake in buckets distinct from the publication bucket.
func (c SubmitConfig) Validate() error {
	if strings.EqualFold(strings.TrimSpace(c.Target), "stable") {
		return fmt.Errorf("submit configuration target 'stable' is refused: the signed stable suite is frozen")
	}
	if c.Schema != SubmitConfigSchema || !safeName(c.AccountID) ||
		c.Endpoint != "https://"+c.AccountID+".r2.cloudflarestorage.com" ||
		c.Region != "auto" || !safeName(c.IntakeBucket) ||
		!safeName(c.CoordinationBucket) || !safeName(c.PublicationBucket) ||
		!safePrefix(c.IntakePrefix) || !safePrefix(c.CoordinationPrefix) ||
		c.Kind != "debian" || c.Target != "trixie" || !repositoryIdentity(c.Producer) {
		return fmt.Errorf("invalid exact intake-submit configuration")
	}
	if c.PublicationBucket != PublicationBucket {
		return fmt.Errorf("submit configuration publication_bucket must be %s", PublicationBucket)
	}
	for _, bucket := range []string{c.IntakeBucket, c.CoordinationBucket} {
		if strings.EqualFold(bucket, PublicationBucket) {
			return fmt.Errorf("intake and coordination buckets must differ from the publication bucket")
		}
	}
	if strings.EqualFold(c.IntakeBucket, c.CoordinationBucket) {
		return fmt.Errorf("intake and coordination buckets must differ: locks are replaced in the coordination bucket")
	}
	return nil
}

// repositoryIdentity accepts exactly one GitHub-style owner/name pair.
func repositoryIdentity(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !safeIdentity(value) {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
