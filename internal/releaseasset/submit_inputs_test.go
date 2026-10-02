package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func submitConfig(target, producer, account, endpoint, intakeBucket, coordinationBucket string) string {
	return `{"account_id":"` + account + `","coordination_bucket":"` + coordinationBucket + `","coordination_prefix":"",` +
		`"endpoint":"` + endpoint + `","intake_bucket":"` + intakeBucket + `","intake_prefix":"","kind":"debian",` +
		`"producer":"` + producer + `","publication_bucket":"frostyardrepo","region":"auto",` +
		`"schema":"org.frostyard.repogen.intake-submit-config.v1","target":"` + target + `"}`
}

func TestSubmitInputsFailClosed(t *testing.T) {
	script := filepath.Join(repositoryRoot(t), "scripts", "validate-submit-inputs.sh")
	endpoint := "https://acct0.r2.cloudflarestorage.com"
	valid := submitConfig("trixie", "frostyard/gchlog", "acct0", endpoint, "intake", "coord")

	type fixture struct {
		config        string
		env           map[string]string
		symlinkConfig bool
		configPin     func(string) string
	}
	cases := []struct {
		name    string
		fixture fixture
		message string
	}{
		{name: "valid"},
		{name: "config pin wrong", fixture: fixture{configPin: func(string) string { return strings.Repeat("0", 64) }}, message: "config digest"},
		{name: "config pin uppercase", fixture: fixture{configPin: strings.ToUpper}, message: "not a lowercase SHA-256"},
		{name: "policy pin malformed", fixture: fixture{env: map[string]string{"POLICY_SHA256": "xyz"}}, message: "not a lowercase SHA-256"},
		{name: "target stable", fixture: fixture{config: submitConfig("stable", "frostyard/gchlog", "acct0", endpoint, "intake", "coord")}, message: "stable"},
		{name: "target Stable", fixture: fixture{config: submitConfig("Stable", "frostyard/gchlog", "acct0", endpoint, "intake", "coord")}, message: "stable"},
		{name: "target padded stable", fixture: fixture{config: submitConfig(" stable ", "frostyard/gchlog", "acct0", endpoint, "intake", "coord")}, message: "stable"},
		{name: "target forky", fixture: fixture{config: submitConfig("forky", "frostyard/gchlog", "acct0", endpoint, "intake", "coord")}, message: "trixie"},
		{name: "producer differs", fixture: fixture{env: map[string]string{"GITHUB_REPOSITORY": "frostyard/other"}}, message: "GITHUB_REPOSITORY"},
		{name: "account differs", fixture: fixture{env: map[string]string{"R2_ACCOUNT_ID": "acct1"}}, message: "account"},
		{name: "endpoint differs", fixture: fixture{config: submitConfig("trixie", "frostyard/gchlog", "acct0", "https://evil.example", "intake", "coord")}, message: "endpoint"},
		{name: "intake is publication", fixture: fixture{config: submitConfig("trixie", "frostyard/gchlog", "acct0", endpoint, "frostyardrepo", "coord")}, message: "intake_bucket"},
		{name: "coordination is publication", fixture: fixture{config: submitConfig("trixie", "frostyard/gchlog", "acct0", endpoint, "intake", "frostyardrepo")}, message: "coordination_bucket"},
		{name: "intake is coordination", fixture: fixture{config: submitConfig("trixie", "frostyard/gchlog", "acct0", endpoint, "same", "same")}, message: "must differ"},
		{name: "decoy publication bucket", fixture: fixture{config: strings.Replace(submitConfig("trixie", "frostyard/gchlog", "acct0", endpoint, "frostyardrepo", "coord"), `"publication_bucket":"frostyardrepo"`, `"publication_bucket":"decoy"`, 1)}, message: "publication_bucket must be"},
		{name: "config symlink", fixture: fixture{symlinkConfig: true}, message: "regular file"},
		{name: "version latest", fixture: fixture{env: map[string]string{"REPOGEN_VERSION": "latest"}}, message: "exact release tag"},
		{name: "commit short", fixture: fixture{env: map[string]string{"REPOGEN_COMMIT": "749b142"}}, message: "commit"},
		{name: "submission key slash", fixture: fixture{env: map[string]string{"SUBMISSION_KEY": "a/b"}}, message: "submission-key"},
		{name: "artifacts not a dir", fixture: fixture{env: map[string]string{"ARTIFACTS_DIR": "/nonexistent-artifacts"}}, message: "artifacts-dir"},
	}
	for _, name := range []string{
		"CONFIG_FILE", "CONFIG_SHA256", "POLICY_SHA256", "REQUEST_FILE",
		"PROVENANCE_FILE", "ARTIFACTS_DIR", "SUBMISSION_KEY", "R2_ACCOUNT_ID",
		"R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "REPOGEN_VERSION",
		"REPOGEN_COMMIT", "GITHUB_REPOSITORY",
	} {
		cases = append(cases, struct {
			name    string
			fixture fixture
			message string
		}{name: name + " missing", fixture: fixture{env: map[string]string{name: ""}}, message: "is required"})
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			config := valid
			if test.fixture.config != "" {
				config = test.fixture.config
			}
			configPath := filepath.Join(dir, "config.json")
			if test.fixture.symlinkConfig {
				real := filepath.Join(dir, "real.json")
				if err := os.WriteFile(real, []byte(config), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, configPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"request.json", "provenance.json"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0o755); err != nil {
				t.Fatal(err)
			}
			pin := sha256Hex([]byte(config))
			if test.fixture.configPin != nil {
				pin = test.fixture.configPin(pin)
			}
			env := map[string]string{
				"CONFIG_FILE":          configPath,
				"CONFIG_SHA256":        pin,
				"POLICY_SHA256":        strings.Repeat("c", 64),
				"REQUEST_FILE":         filepath.Join(dir, "request.json"),
				"PROVENANCE_FILE":      filepath.Join(dir, "provenance.json"),
				"ARTIFACTS_DIR":        filepath.Join(dir, "artifacts"),
				"SUBMISSION_KEY":       "gchlog-run-1",
				"R2_ACCOUNT_ID":        "acct0",
				"R2_ACCESS_KEY_ID":     reconcileAccessKey,
				"R2_SECRET_ACCESS_KEY": reconcileSecretKey,
				"REPOGEN_VERSION":      "v0.6.0",
				"REPOGEN_COMMIT":       strings.Repeat("d", 40),
				"GITHUB_REPOSITORY":    "frostyard/gchlog",
			}
			for key, value := range test.fixture.env {
				env[key] = value
			}
			command := exec.Command("sh", script)
			command.Env = []string{"PATH=" + os.Getenv("PATH")}
			for key, value := range env {
				command.Env = append(command.Env, key+"="+value)
			}
			raw, err := command.CombinedOutput()
			output := string(raw)
			if strings.Contains(output, reconcileAccessKey) || strings.Contains(output, reconcileSecretKey) {
				t.Fatalf("output leaks a secret value:\n%s", output)
			}
			if test.message == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v\n%s", err, output)
				}
				return
			}
			if err == nil {
				t.Fatalf("unexpectedly accepted:\n%s", output)
			}
			if !strings.Contains(output, "::error::") || !strings.Contains(output, test.message) {
				t.Fatalf("output %q lacks ::error:: and %q", output, test.message)
			}
		})
	}
}
