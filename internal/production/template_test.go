package production

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/intake"
)

func templatePath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "production", "templates", name)
}

func readTemplate(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(templatePath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadTemplates(t *testing.T) (Config, Policy) {
	t.Helper()
	var config Config
	if err := intake.DecodeCanonical(readTemplate(t, "trixie-config.template.json"), &config); err != nil {
		t.Fatalf("config template is not canonical: %v", err)
	}
	var policy Policy
	if err := intake.DecodeCanonical(readTemplate(t, "authorization-policy.template.json"), &policy); err != nil {
		t.Fatalf("policy template is not canonical: %v", err)
	}
	return config, policy
}

func TestTemplatesAreCanonicalAndUnfilledFailValidation(t *testing.T) {
	config, policy := loadTemplates(t)
	for name, value := range map[string]any{
		"trixie-config.template.json":        config,
		"authorization-policy.template.json": policy,
	} {
		encoded, err := intake.CanonicalJSON(value)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != string(readTemplate(t, name)) {
			t.Fatalf("%s is not byte-canonical:\n got %s\nwant %s", name, readTemplate(t, name), encoded)
		}
	}
	if config.Target != "trixie" || policy.Target != "trixie" {
		t.Fatalf("templates must fix target trixie: %q %q", config.Target, policy.Target)
	}
	if err := config.Validate(); err == nil {
		t.Fatal("unfilled config template validated")
	}
	if err := policy.Validate(filledConfig(config)); err == nil {
		t.Fatal("unfilled policy template validated")
	}
}

func filledConfig(config Config) Config {
	config.AccountID = "0123456789abcdef"
	config.Endpoint = "https://0123456789abcdef.r2.cloudflarestorage.com"
	config.IntakeBucket = "frostyard-intake"
	config.CoordinationBucket = "frostyard-coordination"
	config.RequestSHA256 = strings.Repeat("a", 64)
	return config
}

func filledPolicy(policy Policy, config Config) Policy {
	policy.Operator = "github:bketelsen"
	policy.RequestSHA256 = config.RequestSHA256
	policy.ProvenanceSHA256 = strings.Repeat("b", 64)
	policy.Producer = "github.com/frostyard/gchlog"
	policy.ProducerCommit = strings.Repeat("c", 40)
	policy.ProducerTree = strings.Repeat("d", 40)
	policy.ActionCommit = strings.Repeat("e", 40)
	policy.RepogenVersion = "0.6.0"
	policy.RepogenSHA256 = strings.Repeat("f", 64)
	policy.SigningKeyFingerprint = strings.Repeat("A", 40)
	policy.SigningPublicKeySHA256 = strings.Repeat("1", 64)
	policy.ReleaseTime = "2026-10-02T00:00:00Z"
	policy.ProviderPermissionEvidence = "https://example.invalid/evidence"
	policy.AuthoritativeTargetAbsent = true
	policy.AllowedPoolObjects = []ApprovedObject{{
		Path:   "pool/main/g/gchlog/gchlog_1.0.0_amd64.deb",
		SHA256: strings.Repeat("2", 64),
		Size:   1,
	}}
	return policy
}

func TestTemplatesFilledValidate(t *testing.T) {
	templateConfig, templatePolicy := loadTemplates(t)
	config := filledConfig(templateConfig)
	if err := config.Validate(); err != nil {
		t.Fatalf("filled config: %v", err)
	}
	policy := filledPolicy(templatePolicy, config)
	if err := policy.Validate(config); err != nil {
		t.Fatalf("filled policy: %v", err)
	}
	stable := config
	stable.Target = "stable"
	if err := stable.Validate(); err == nil {
		t.Fatal("stable config validated")
	}
	stablePolicy := policy
	stablePolicy.Target = "stable"
	if err := stablePolicy.Validate(config); err == nil {
		t.Fatal("stable policy validated")
	}
}

func TestTemplatesCoverEveryField(t *testing.T) {
	for name, value := range map[string]any{
		"trixie-config.template.json":        Config{},
		"authorization-policy.template.json": Policy{},
	} {
		var decoded map[string]any
		if err := intake.DecodeCanonical(readTemplate(t, name), &decoded); err != nil {
			t.Fatal(err)
		}
		var got, want []string
		for key := range decoded {
			got = append(got, key)
		}
		kind := reflect.TypeOf(value)
		for index := 0; index < kind.NumField(); index++ {
			want = append(want, strings.Split(kind.Field(index).Tag.Get("json"), ",")[0])
		}
		sort.Strings(got)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s keys %v, want %v", name, got, want)
		}
	}
}

// TestTemplateFilledPolicyMatchesExecutableIdentity follows the template
// README: a release binary built from tag v0.6.0 embeds version "0.6.0" and
// its full commit, which must equal repogen_version and action_commit.
func TestTemplateFilledPolicyMatchesExecutableIdentity(t *testing.T) {
	templateConfig, templatePolicy := loadTemplates(t)
	binary := filepath.Join(t.TempDir(), "repogen")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	policy := filledPolicy(templatePolicy, filledConfig(templateConfig))
	policy.RepogenSHA256 = digest([]byte("binary"))
	if err := VerifyExecutableIdentity(binary, "0.6.0", policy.ActionCommit, policy); err != nil {
		t.Fatalf("policy filled as documented does not match the binary: %v", err)
	}
	policy.RepogenVersion = "v0.6.0"
	if err := VerifyExecutableIdentity(binary, "0.6.0", policy.ActionCommit, policy); err == nil {
		t.Fatal("v-prefixed repogen_version unexpectedly matched")
	}
}

func TestSubmitConfigTemplateIsCanonicalAndUnfilledFailsValidation(t *testing.T) {
	data := readTemplate(t, "submit-config.template.json")
	var config SubmitConfig
	if err := intake.DecodeCanonical(data, &config); err != nil {
		t.Fatalf("submit config template is not canonical: %v", err)
	}
	encoded, err := intake.CanonicalJSON(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != string(data) {
		t.Fatalf("submit config template is not byte-canonical:\n got %s\nwant %s", data, encoded)
	}
	if config.Schema != SubmitConfigSchema || config.Target != "trixie" ||
		config.Kind != "debian" || config.PublicationBucket != "frostyardrepo" {
		t.Fatalf("template fixed values wrong: %+v", config)
	}
	if err := config.Validate(); err == nil {
		t.Fatal("unfilled submit config template validated")
	}
	config.AccountID = "0123456789abcdef"
	config.Endpoint = "https://0123456789abcdef.r2.cloudflarestorage.com"
	config.IntakeBucket = "frostyard-intake"
	config.CoordinationBucket = "frostyard-coordination"
	config.Producer = "frostyard/gchlog"
	if err := config.Validate(); err != nil {
		t.Fatalf("filled submit config template rejected: %v", err)
	}
}
