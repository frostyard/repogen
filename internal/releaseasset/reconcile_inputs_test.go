package releaseasset

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	reconcileAccessKey = "AKIA-test-access-key-value"
	reconcileSecretKey = "test-secret-access-key-value"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func reconcileConfig(target, request, account, endpoint string) string {
	return `{"account_id":"` + account + `","coordination_bucket":"c","coordination_prefix":"",` +
		`"endpoint":"` + endpoint + `","intake_bucket":"i","intake_prefix":"",` +
		`"publication_bucket":"p","publication_prefix":"","region":"auto",` +
		`"request_sha256":"` + request + `","schema":"org.frostyard.repogen.production-config.v1",` +
		`"target":"` + target + `"}`
}

func reconcilePolicy(target, request string) string {
	return reconcilePolicyFull(target, request, "0.6.0", strings.Repeat("d", 40))
}

func reconcilePolicyFull(target, request, version, commit string) string {
	return `{"action_commit":"` + commit + `","repogen_version":"` + version + `","request_sha256":"` + request +
		`","schema":"org.frostyard.repogen.authorization-policy.v1","target":"` + target + `"}`
}

func TestReconcileInputsFailClosed(t *testing.T) {
	script := filepath.Join(repositoryRoot(t), "scripts", "validate-reconcile-inputs.sh")
	request := strings.Repeat("a", 64)
	provenance := strings.Repeat("b", 64)
	endpoint := "https://acct0.r2.cloudflarestorage.com"
	validConfig := reconcileConfig("trixie", request, "acct0", endpoint)
	validPolicy := reconcilePolicy("trixie", request)

	type fixture struct {
		config, policy string
		env            map[string]string
		symlinkConfig  bool
		configPin      func(string) string
		policyPin      func(string) string
	}
	cases := []struct {
		name    string
		fixture fixture
		message string
	}{
		{name: "valid"},
		{name: "config pin wrong", fixture: fixture{configPin: func(string) string { return strings.Repeat("0", 64) }}, message: "config digest"},
		{name: "policy pin wrong", fixture: fixture{policyPin: func(string) string { return strings.Repeat("0", 64) }}, message: "policy digest"},
		{name: "config pin uppercase", fixture: fixture{configPin: strings.ToUpper}, message: "not a lowercase SHA-256"},
		{name: "config pin 63 chars", fixture: fixture{configPin: func(s string) string { return s[:63] }}, message: "not a lowercase SHA-256"},
		{name: "config pin trailing space", fixture: fixture{configPin: func(s string) string { return s + " " }}, message: "not a lowercase SHA-256"},
		{name: "policy pin uppercase", fixture: fixture{policyPin: strings.ToUpper}, message: "not a lowercase SHA-256"},
		{name: "request pin malformed", fixture: fixture{env: map[string]string{"REQUEST_SHA256": strings.Repeat("A", 64)}}, message: "not a lowercase SHA-256"},
		{name: "provenance pin malformed", fixture: fixture{env: map[string]string{"PROVENANCE_SHA256": "xyz"}}, message: "not a lowercase SHA-256"},
		{name: "config target stable", fixture: fixture{config: reconcileConfig("stable", request, "acct0", endpoint)}, message: "stable"},
		{name: "config target Stable", fixture: fixture{config: reconcileConfig("Stable", request, "acct0", endpoint)}, message: "stable"},
		{name: "config target padded stable", fixture: fixture{config: reconcileConfig(" stable", request, "acct0", endpoint)}, message: "stable"},
		{name: "config target forky", fixture: fixture{config: reconcileConfig("forky", request, "acct0", endpoint)}, message: "trixie"},
		{name: "policy target stable", fixture: fixture{policy: reconcilePolicy("stable", request)}, message: "stable"},
		{name: "policy target forky", fixture: fixture{policy: reconcilePolicy("forky", request)}, message: "trixie"},
		{name: "policy request differs", fixture: fixture{policy: reconcilePolicy("trixie", strings.Repeat("c", 64))}, message: "request"},
		{name: "config request differs", fixture: fixture{config: reconcileConfig("trixie", strings.Repeat("c", 64), "acct0", endpoint)}, message: "request"},
		{name: "policy version with v", fixture: fixture{policy: reconcilePolicyFull("trixie", request, "v0.6.0", strings.Repeat("d", 40))}, message: "without its v"},
		{name: "policy version differs", fixture: fixture{policy: reconcilePolicyFull("trixie", request, "0.5.2", strings.Repeat("d", 40))}, message: "without its v"},
		{name: "policy commit differs", fixture: fixture{policy: reconcilePolicyFull("trixie", request, "0.6.0", strings.Repeat("e", 40))}, message: "action_commit"},
		{name: "account differs", fixture: fixture{env: map[string]string{"R2_ACCOUNT_ID": "acct1"}}, message: "account"},
		{name: "endpoint differs", fixture: fixture{config: reconcileConfig("trixie", request, "acct0", "https://evil.example")}, message: "endpoint"},
		{name: "config symlink", fixture: fixture{symlinkConfig: true}, message: "regular file"},
		{name: "version latest", fixture: fixture{env: map[string]string{"REPOGEN_VERSION": "latest"}}, message: "exact release tag"},
		{name: "version main", fixture: fixture{env: map[string]string{"REPOGEN_VERSION": "main"}}, message: "exact release tag"},
		{name: "commit short", fixture: fixture{env: map[string]string{"REPOGEN_COMMIT": "749b142"}}, message: "commit"},
		{name: "commit uppercase", fixture: fixture{env: map[string]string{"REPOGEN_COMMIT": strings.Repeat("A", 40)}}, message: "commit"},
	}
	for _, name := range []string{
		"CONFIG_FILE", "CONFIG_SHA256", "POLICY_FILE", "POLICY_SHA256",
		"REQUEST_SHA256", "PROVENANCE_SHA256", "R2_ACCOUNT_ID",
		"R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "SIGNING_KEY",
		"REPOGEN_VERSION", "REPOGEN_COMMIT",
	} {
		cases = append(cases,
			struct {
				name    string
				fixture fixture
				message string
			}{name: name + " missing", fixture: fixture{env: map[string]string{name: ""}}, message: "is required"},
			struct {
				name    string
				fixture fixture
				message string
			}{name: name + " blank", fixture: fixture{env: map[string]string{name: "  "}}, message: "is required"},
		)
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			config, policy := validConfig, validPolicy
			if test.fixture.config != "" {
				config = test.fixture.config
			}
			if test.fixture.policy != "" {
				policy = test.fixture.policy
			}
			configPath := filepath.Join(dir, "config.json")
			policyPath := filepath.Join(dir, "policy.json")
			if err := os.WriteFile(policyPath, []byte(policy), 0o644); err != nil {
				t.Fatal(err)
			}
			if test.fixture.symlinkConfig {
				real := filepath.Join(dir, "real-config.json")
				if err := os.WriteFile(real, []byte(config), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, configPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			configPin, policyPin := sha256Hex([]byte(config)), sha256Hex([]byte(policy))
			if test.fixture.configPin != nil {
				configPin = test.fixture.configPin(configPin)
			}
			if test.fixture.policyPin != nil {
				policyPin = test.fixture.policyPin(policyPin)
			}
			env := map[string]string{
				"CONFIG_FILE":          configPath,
				"CONFIG_SHA256":        configPin,
				"POLICY_FILE":          policyPath,
				"POLICY_SHA256":        policyPin,
				"REQUEST_SHA256":       request,
				"PROVENANCE_SHA256":    provenance,
				"R2_ACCOUNT_ID":        "acct0",
				"R2_ACCESS_KEY_ID":     reconcileAccessKey,
				"R2_SECRET_ACCESS_KEY": reconcileSecretKey,
				"SIGNING_KEY":          "-----BEGIN PGP PRIVATE KEY BLOCK-----",
				"REPOGEN_VERSION":      "v0.6.0",
				"REPOGEN_COMMIT":       strings.Repeat("d", 40),
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
