package releaseasset

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestReconcileActionContract(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "actions", "reconcile-production", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	action := string(data)
	stepsAt := strings.Index(action, "\nruns:")
	if stepsAt < 0 {
		t.Fatal("action.yml has no runs section")
	}
	inputs, steps := action[:stepsAt], action[stepsAt:]

	for _, forbidden := range []string{"codename:", "suite:", "target:"} {
		if strings.Contains(inputs, "\n  "+forbidden) {
			t.Fatalf("action exposes a %s input; the target is fixed by the pinned config", forbidden)
		}
	}
	for _, forbidden := range []string{"@main", "latest", "set -x"} {
		if strings.Contains(action, forbidden) {
			t.Fatalf("action contains %q", forbidden)
		}
	}

	names := regexp.MustCompile(`(?m)^    - name: (.+)$`).FindAllStringSubmatch(steps, -1)
	if len(names) < 4 || names[0][1] != "Validate inputs" || names[1][1] != "Install repogen" {
		t.Fatalf("steps must start with Validate inputs then Install repogen: %v", names)
	}
	validate := strings.Index(steps, "- name: Validate inputs")
	install := strings.Index(steps, "- name: Install repogen")
	reconcile := strings.Index(steps, "- name: Reconcile")
	cleanup := strings.Index(steps, "- name: Remove secrets")
	if validate >= install || install >= reconcile || reconcile >= cleanup {
		t.Fatal("steps out of order")
	}
	if !strings.Contains(steps[validate:install], "scripts/validate-reconcile-inputs.sh") {
		t.Fatal("Validate inputs does not run validate-reconcile-inputs.sh")
	}
	if !strings.Contains(steps[install:reconcile], "--github-release") {
		t.Fatal("Install repogen does not install an exact GitHub release")
	}

	// Inputs reach shell only through env, never interpolated into run:.
	for _, block := range regexp.MustCompile(`(?s)run: \|\n(.*?)(?:\n    - name:|\z)`).FindAllStringSubmatch(steps, -1) {
		if strings.Contains(block[1], "${{") {
			t.Fatalf("run block interpolates an expression; pass it through env:\n%s", block[1])
		}
	}

	body := steps[reconcile:cleanup]
	umask := strings.Index(body, "umask 077")
	write := strings.Index(body, "r2-credentials.json")
	if umask < 0 || write < 0 || umask > write {
		t.Fatal("umask 077 must precede secret file writes")
	}
	for _, want := range []string{
		"reconcile-production",
		"--config ", "--config-sha256 ", "--policy ", "--policy-sha256 ",
		"--request-sha256 ", "--provenance-sha256 ", "--credentials-file ",
		"--signing-key ", "--signing-passphrase-file",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("Reconcile step lacks %q", want)
		}
	}
	for _, secret := range []string{"R2_SECRET_ACCESS_KEY", "R2_ACCESS_KEY_ID", "GPG_PRIVATE_KEY", "GPG_PASSPHRASE"} {
		if regexp.MustCompile(`--[a-z-]+ "?\$` + secret).MatchString(body) {
			t.Fatalf("%s is passed as a command argument", secret)
		}
	}
	if regexp.MustCompile(`--(arg|argjson|rawfile)\b[^\n]*\$(R2_|GPG_)`).MatchString(action) {
		t.Fatal("a secret is passed to jq as a process argument; read it with env. inside jq")
	}
	if !strings.Contains(body, "env.R2_SECRET_ACCESS_KEY") {
		t.Fatal("credentials JSON must read the secret via jq env., not argv")
	}
	if strings.Contains(action, "GITHUB_ENV") {
		t.Fatal("action writes to GITHUB_ENV")
	}
	if !regexp.MustCompile(`- name: Remove secrets\n\s+if: always\(\)`).MatchString(steps[cleanup:]) {
		t.Fatal("Remove secrets must run if: always()")
	}
}

func TestNoWorkflowCallsReconcileAction(t *testing.T) {
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
		if strings.Contains(string(data), "reconcile-production") {
			t.Fatalf("%s calls reconcile-production; production runs are wired only in a reviewed producer change", entry.Name())
		}
	}
}
