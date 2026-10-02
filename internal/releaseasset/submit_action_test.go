package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readSubmitAction(t *testing.T) (string, string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".github", "actions", "submit-intake", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	action := string(data)
	at := strings.Index(action, "\nruns:")
	if at < 0 {
		t.Fatal("action.yml has no runs section")
	}
	return action[:at], action[at:]
}

// stepScript returns the run: block of the named step, unindented.
func stepScript(t *testing.T, steps, name string) string {
	t.Helper()
	start := strings.Index(steps, "- name: "+name+"\n")
	if start < 0 {
		t.Fatalf("no step %q", name)
	}
	rest := steps[start+1:]
	if next := strings.Index(rest, "\n    - name:"); next >= 0 {
		rest = rest[:next]
	}
	run := strings.Index(rest, "run: |\n")
	if run < 0 {
		t.Fatalf("step %q has no run block", name)
	}
	var lines []string
	for _, line := range strings.Split(rest[run+len("run: |\n"):], "\n") {
		lines = append(lines, strings.TrimPrefix(line, "        "))
	}
	return strings.Join(lines, "\n")
}

func TestSubmitActionSecretsOnlyInFiles(t *testing.T) {
	inputs, steps := readSubmitAction(t)
	for _, forbidden := range []string{"codename:", "suite:", "target:"} {
		if strings.Contains(inputs, "\n  "+forbidden) {
			t.Fatalf("action exposes a %s input", forbidden)
		}
	}
	action := inputs + steps
	for _, forbidden := range []string{"@main", "latest", "set -x", "GITHUB_ENV"} {
		if strings.Contains(action, forbidden) {
			t.Fatalf("action contains %q", forbidden)
		}
	}
	names := regexp.MustCompile(`(?m)^    - name: (.+)$`).FindAllStringSubmatch(steps, -1)
	var order []string
	for _, name := range names {
		order = append(order, name[1])
	}
	if strings.Join(order, ",") != "Validate inputs,Install repogen,Submit,Remove secrets" {
		t.Fatalf("steps %v", order)
	}
	if !strings.Contains(stepScript(t, steps, "Validate inputs"), "scripts/validate-submit-inputs.sh") {
		t.Fatal("Validate inputs does not run validate-submit-inputs.sh")
	}
	if !strings.Contains(stepScript(t, steps, "Install repogen"), "--github-release") {
		t.Fatal("Install repogen does not install an exact GitHub release")
	}
	for _, block := range regexp.MustCompile(`(?s)run: \|\n(.*?)(?:\n    - name:|\z)`).FindAllStringSubmatch(steps, -1) {
		if strings.Contains(block[1], "${{") {
			t.Fatalf("run block interpolates an expression:\n%s", block[1])
		}
	}
	if regexp.MustCompile(`--(arg|argjson|rawfile)\b[^\n]*\$R2_`).MatchString(action) {
		t.Fatal("a secret is passed to jq as a process argument")
	}
	submit := stepScript(t, steps, "Submit")
	for _, secret := range []string{"R2_SECRET_ACCESS_KEY", "R2_ACCESS_KEY_ID"} {
		if regexp.MustCompile(`--[a-z-]+ "?\$` + secret).MatchString(submit) {
			t.Fatalf("%s is passed as a command argument", secret)
		}
	}
	for _, want := range []string{
		"umask 077", "env.R2_SECRET_ACCESS_KEY", "submit-intake",
		"--config ", "--config-sha256 ", "--policy-sha256 ", "--request ",
		"--provenance ", "--artifacts-dir ", "--submission-key ", "--credentials-file ",
	} {
		if !strings.Contains(submit, want) {
			t.Fatalf("Submit step lacks %q", want)
		}
	}
	if !regexp.MustCompile(`- name: Remove secrets\n\s+if: always\(\)`).MatchString(steps) {
		t.Fatal("Remove secrets must run if: always()")
	}
}

// TestSubmitActionSmoke runs the Submit and Remove secrets steps with a fake
// repogen and checks the credential file is private, valid JSON and removed.
func TestSubmitActionSmoke(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
	_, steps := readSubmitAction(t)
	runner := t.TempDir()
	bin := filepath.Join(runner, "repogen-bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(runner, "observed")
	fake := `#!/bin/bash
set -euo pipefail
creds=
while [ $# -gt 0 ]; do
  case "$1" in --credentials-file) creds=$2; shift ;; esac
  shift
done
{
  stat -c '%a' "$(dirname "$creds")"
  stat -c '%a' "$creds"
  jq -r .secret_access_key "$creds"
  printf '%s\n' "$*"
} > "` + record + `"
`
	if err := os.WriteFile(filepath.Join(bin, "repogen"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := "s3cr\"et\nwith newline"
	env := append(os.Environ(),
		"RUNNER_TEMP="+runner,
		"CONFIG_FILE=c", "CONFIG_SHA256=x", "POLICY_SHA256=y", "REQUEST_FILE=r",
		"PROVENANCE_FILE=p", "ARTIFACTS_DIR=a", "SUBMISSION_KEY=k",
		"R2_ACCESS_KEY_ID=AKIA-smoke", "R2_SECRET_ACCESS_KEY="+secret,
	)
	command := exec.Command("bash", "-c", stepScript(t, steps, "Submit"))
	command.Env = env
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Submit step failed: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "s3cr") || strings.Contains(string(output), "AKIA-smoke") {
		t.Fatalf("step output leaks a secret: %s", output)
	}
	observed, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(string(observed), "\n", 3)
	if lines[0] != "700" || lines[1] != "600" || !strings.HasPrefix(lines[2], secret+"\n") {
		t.Fatalf("observed %q", observed)
	}
	if strings.Contains(lines[2][len(secret):], "s3cr") {
		t.Fatal("secret reached repogen argv")
	}
	cleanup := exec.Command("bash", "-c", stepScript(t, steps, "Remove secrets"))
	cleanup.Env = env
	if output, err := cleanup.CombinedOutput(); err != nil {
		t.Fatalf("cleanup failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(runner, "repogen-submit-secrets")); !os.IsNotExist(err) {
		t.Fatalf("secrets directory survives cleanup: %v", err)
	}
}

func TestNoWorkflowCallsSubmitAction(t *testing.T) {
	root := repositoryRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, ".github", "workflows"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, ".github", "workflows", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "submit-intake") {
			t.Fatalf("%s calls submit-intake; submission is wired only in a reviewed producer change", entry.Name())
		}
	}
}
