package production

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/intake"
)

func validSubmitConfig() SubmitConfig {
	return SubmitConfig{
		Schema:             SubmitConfigSchema,
		AccountID:          "acct0",
		Endpoint:           "https://acct0.r2.cloudflarestorage.com",
		Region:             "auto",
		IntakeBucket:       "frostyard-intake",
		IntakePrefix:       "",
		CoordinationBucket: "frostyard-coordination",
		CoordinationPrefix: "locks",
		PublicationBucket:  "frostyardrepo",
		Kind:               "debian",
		Target:             "trixie",
		Producer:           "frostyard/gchlog",
	}
}

func TestSubmitConfigValidate(t *testing.T) {
	if err := validSubmitConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]func(*SubmitConfig){
		"schema":                func(c *SubmitConfig) { c.Schema = ConfigSchema },
		"endpoint other host":   func(c *SubmitConfig) { c.Endpoint = "https://evil.example" },
		"endpoint path":         func(c *SubmitConfig) { c.Endpoint += "/x" },
		"region":                func(c *SubmitConfig) { c.Region = "us-east-1" },
		"account unsafe":        func(c *SubmitConfig) { c.AccountID = "a/b"; c.Endpoint = "https://a/b.r2.cloudflarestorage.com" },
		"intake bucket unsafe":  func(c *SubmitConfig) { c.IntakeBucket = "a/b" },
		"intake bucket empty":   func(c *SubmitConfig) { c.IntakeBucket = "" },
		"coord bucket unsafe":   func(c *SubmitConfig) { c.CoordinationBucket = ".." },
		"publication empty":     func(c *SubmitConfig) { c.PublicationBucket = "" },
		"intake prefix unsafe":  func(c *SubmitConfig) { c.IntakePrefix = "../x" },
		"coord prefix absolute": func(c *SubmitConfig) { c.CoordinationPrefix = "/x" },
		"kind sysext":           func(c *SubmitConfig) { c.Kind = "sysext" },
		"target stable":         func(c *SubmitConfig) { c.Target = "stable" },
		"target Stable":         func(c *SubmitConfig) { c.Target = "Stable" },
		"target padded":         func(c *SubmitConfig) { c.Target = " trixie" },
		"target forky":          func(c *SubmitConfig) { c.Target = "forky" },
		"producer no owner":     func(c *SubmitConfig) { c.Producer = "gchlog" },
		"producer three parts":  func(c *SubmitConfig) { c.Producer = "a/b/c" },
		"producer empty part":   func(c *SubmitConfig) { c.Producer = "frostyard/" },
		"producer space":        func(c *SubmitConfig) { c.Producer = "frostyard/gch log" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			config := validSubmitConfig()
			mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSubmitConfigRejectsPublicationBucket(t *testing.T) {
	for name, mutate := range map[string]func(*SubmitConfig){
		"intake":       func(c *SubmitConfig) { c.IntakeBucket = c.PublicationBucket },
		"coordination": func(c *SubmitConfig) { c.CoordinationBucket = c.PublicationBucket },
	} {
		t.Run(name, func(t *testing.T) {
			config := validSubmitConfig()
			mutate(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), "publication") {
				t.Fatalf("returned %v", err)
			}
		})
	}
}

func TestSubmitConfigRejectsSpoofedPublicationBucket(t *testing.T) {
	config := validSubmitConfig()
	config.PublicationBucket = "decoy"
	config.IntakeBucket = PublicationBucket
	if err := config.Validate(); err == nil {
		t.Fatal("decoy publication bucket let intake target the real one")
	}
	config = validSubmitConfig()
	config.PublicationBucket = "decoy"
	if err := config.Validate(); err == nil {
		t.Fatal("publication bucket other than the fixed one accepted")
	}
	config = validSubmitConfig()
	config.IntakeBucket = "FrostyardRepo"
	if err := config.Validate(); err == nil {
		t.Fatal("case variant of the publication bucket accepted")
	}
}

func TestSubmitConfigRejectsSharedIntakeAndCoordinationBucket(t *testing.T) {
	config := validSubmitConfig()
	config.CoordinationBucket = config.IntakeBucket
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("returned %v", err)
	}
}

func TestSubmitConfigLoadRejectsUnknownField(t *testing.T) {
	data, err := intake.CanonicalJSON(validSubmitConfig())
	if err != nil {
		t.Fatal(err)
	}
	extended := strings.Replace(string(data), `{"account_id"`, `{"a_extra":"x","account_id"`, 1)
	file := filepath.Join(t.TempDir(), "submit.json")
	if err := os.WriteFile(file, []byte(extended), 0o644); err != nil {
		t.Fatal(err)
	}
	var config SubmitConfig
	if _, err := LoadCanonicalFile(file, digest([]byte(extended)), &config); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCanonicalFile(file, digest(data), &config); err != nil {
		t.Fatalf("canonical config rejected: %v", err)
	}
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
}
