package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAWSListObjects is a minimal stand-in for `aws s3api list-objects-v2` as
// scripts/check-published-payloads.sh calls it. It accepts only that one
// command with the flags the script passes, prints the keys of
// FAKE_AWS_KEYS_FILE that start with --prefix (or `None` when none match), and
// fails on demand through FAKE_AWS_FAIL.
const fakeAWSListObjects = `#!/usr/bin/env bash
set -uo pipefail

log() {
  printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
}

if [[ ${1:-} != "s3api" || ${2:-} != "list-objects-v2" ]]; then
  echo "fake aws: unsupported command: ${*:-}" >&2
  exit 99
fi
shift 2

bucket=""
prefix=""
endpoint=""
query=""
output=""
while (( $# > 0 )); do
  case "$1" in
    --bucket|--prefix|--endpoint-url|--query|--output)
      [[ $# -ge 2 ]] || { echo "fake aws: $1 needs a value" >&2; exit 99; }
      case "$1" in
        --bucket) bucket="$2" ;;
        --prefix) prefix="$2" ;;
        --endpoint-url) endpoint="$2" ;;
        --query) query="$2" ;;
        --output) output="$2" ;;
      esac
      shift 2
      ;;
    *)
      echo "fake aws: unsupported argument: $1" >&2
      exit 99
      ;;
  esac
done

if [[ -z "$bucket" || -z "$prefix" || -z "$endpoint" ]]; then
  echo "fake aws: --bucket, --prefix and --endpoint-url are required" >&2
  exit 99
fi
if [[ "$query" != 'Contents[].[Key]' ]]; then
  echo "fake aws: unsupported query: $query" >&2
  exit 99
fi
if [[ "$output" != "text" ]]; then
  echo "fake aws: unsupported output: $output" >&2
  exit 99
fi

log "list-objects-v2 $bucket $prefix"

if [[ -n ${FAKE_AWS_FAIL:-} ]]; then
  echo "An error occurred (AccessDenied) when calling the ListObjectsV2 operation" >&2
  exit 1
fi

found=0
while IFS= read -r key; do
  [[ -n "$key" ]] || continue
  if [[ "$key" == "$prefix"* ]]; then
    printf '%s\n' "$key"
    found=1
  fi
done < "${FAKE_AWS_KEYS_FILE:-/dev/null}"

if (( found == 0 )); then
  printf 'None\n'
fi
`

type publishedPayloadsEnv struct {
	t         *testing.T
	reportDir string
	logPath   string
	keysPath  string
	env       []string
}

// newPublishedPayloadsEnv builds the fake aws on PATH and the environment the
// check script reads. report is written as the skipped report; keys is what the
// fake bucket lists.
func newPublishedPayloadsEnv(t *testing.T, report, keys string) *publishedPayloadsEnv {
	t.Helper()
	requireBash(t)

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "aws"), []byte(fakeAWSListObjects), 0o755); err != nil {
		t.Fatal(err)
	}

	env := &publishedPayloadsEnv{
		t:         t,
		reportDir: root,
		logPath:   filepath.Join(root, "aws.log"),
		keysPath:  filepath.Join(root, "keys.txt"),
	}
	if err := os.WriteFile(env.keysPath, []byte(keys), 0o644); err != nil {
		t.Fatal(err)
	}
	reportPath := filepath.Join(root, "skipped.txt")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}

	env.env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SKIPPED_REPORT="+reportPath,
		"BUCKET=repository-bucket",
		"R2_ENDPOINT=https://example.r2.cloudflarestorage.com",
		"FAKE_AWS_LOG="+env.logPath,
		"FAKE_AWS_KEYS_FILE="+env.keysPath,
	)

	return env
}

func (e *publishedPayloadsEnv) dropEnv(name string) {
	e.t.Helper()
	var kept []string
	for _, entry := range e.env {
		if strings.HasPrefix(entry, name+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	e.env = kept
}

func (e *publishedPayloadsEnv) run() (string, error) {
	e.t.Helper()
	script := filepath.Join(repositoryRoot(e.t), "scripts", "check-published-payloads.sh")
	command := exec.Command("bash", script)
	command.Env = e.env
	output, err := command.CombinedOutput()

	return string(output), err
}

// listCalls returns the listing calls the fake aws served.
func (e *publishedPayloadsEnv) listCalls() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		e.t.Fatal(err)
	}

	var calls []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			calls = append(calls, line)
		}
	}

	return calls
}

func TestCheckPublishedPayloadsAcceptsPresentPayloads(t *testing.T) {
	report := "ext/incus/incus_7.3_13_x86-64.raw\next/chatgpt/chatgpt_1.2_13_x86-64.raw.zst\n"
	keys := "ext/incus/incus_7.3_13_x86-64.raw\next/incus/SHA256SUMS\next/chatgpt/chatgpt_1.2_13_x86-64.raw.zst\n"
	env := newPublishedPayloadsEnv(t, report, keys)

	output, err := env.run()
	if err != nil {
		t.Fatalf("check of present payloads failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "2 skipped identities have their payloads") {
		t.Fatalf("check did not report both identities: %s", output)
	}
	if calls := env.listCalls(); len(calls) != 1 {
		t.Fatalf("made %d listing calls, want exactly 1: %v", len(calls), calls)
	}
}

func TestCheckPublishedPayloadsRejectsMissingPayload(t *testing.T) {
	report := "ext/incus/incus_7.3_13_x86-64.raw\next/incus/incus_7.4_13_x86-64.raw\n"
	keys := "ext/incus/incus_7.3_13_x86-64.raw\n"
	env := newPublishedPayloadsEnv(t, report, keys)

	output, err := env.run()
	if err == nil {
		t.Fatalf("missing payload unexpectedly passed: %s", output)
	}
	if !strings.Contains(output, "::error::") {
		t.Fatalf("failure output lacks a ::error:: annotation: %s", output)
	}
	if !strings.Contains(output, "ext/incus/incus_7.4_13_x86-64.raw") {
		t.Fatalf("failure output does not name the missing key: %s", output)
	}
	if !strings.Contains(output, "signature-verified") {
		t.Fatalf("failure output does not explain the source of the identity: %s", output)
	}
}

func TestCheckPublishedPayloadsSkipsListingForEmptyReport(t *testing.T) {
	env := newPublishedPayloadsEnv(t, "", "")

	output, err := env.run()
	if err != nil {
		t.Fatalf("empty report failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "no skipped identities") {
		t.Fatalf("empty report output = %q", output)
	}
	if calls := env.listCalls(); len(calls) != 0 {
		t.Fatalf("empty report made listing calls: %v", calls)
	}
}

func TestCheckPublishedPayloadsReadsPlusAsSpace(t *testing.T) {
	// The AWS CLI decodes keys with unquote_plus, so "1+7.3" comes back as
	// "1 7.3". The report keeps the real object key.
	report := "ext/incus/incus_1+7.3_13_x86-64.raw\n"
	keys := "ext/incus/incus_1 7.3_13_x86-64.raw\n"
	env := newPublishedPayloadsEnv(t, report, keys)

	output, err := env.run()
	if err != nil {
		t.Fatalf("a key with a plus sign was not matched: %v\n%s", err, output)
	}
	if !strings.Contains(output, "1 skipped identities have their payloads") {
		t.Fatalf("check did not report the identity: %s", output)
	}
}

func TestCheckPublishedPayloadsFailsOnListingError(t *testing.T) {
	env := newPublishedPayloadsEnv(t, "ext/incus/incus_7.3_13_x86-64.raw\n", "ext/incus/incus_7.3_13_x86-64.raw\n")
	env.env = append(env.env, "FAKE_AWS_FAIL=1")

	output, err := env.run()
	if err == nil {
		t.Fatalf("listing failure unexpectedly passed: %s", output)
	}
	if !strings.Contains(output, "could not list") {
		t.Fatalf("listing failure output = %q", output)
	}
}

func TestCheckPublishedPayloadsAppliesPrefix(t *testing.T) {
	report := "ext/incus/incus_7.3_13_x86-64.raw\n"
	keys := "prefix/ext/incus/incus_7.3_13_x86-64.raw\n"
	env := newPublishedPayloadsEnv(t, report, keys)
	env.env = append(env.env, "PREFIX=prefix")

	output, err := env.run()
	if err != nil {
		t.Fatalf("prefixed payload was not matched: %v\n%s", err, output)
	}
	calls := env.listCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], " prefix/ext/") {
		t.Fatalf("listing was not scoped to the prefixed keys: %v", calls)
	}

	missing := newPublishedPayloadsEnv(t, report, "prefix/ext/incus/incus_7.4_13_x86-64.raw\n")
	missing.env = append(missing.env, "PREFIX=prefix")
	output, err = missing.run()
	if err == nil {
		t.Fatalf("missing prefixed payload unexpectedly passed: %s", output)
	}
	if !strings.Contains(output, "::error::prefix/ext/incus/incus_7.3_13_x86-64.raw") {
		t.Fatalf("failure output does not name the prefixed key: %s", output)
	}
}

func TestCheckPublishedPayloadsRequiresEnvironment(t *testing.T) {
	for _, name := range []string{"SKIPPED_REPORT", "BUCKET", "R2_ENDPOINT"} {
		t.Run(name, func(t *testing.T) {
			env := newPublishedPayloadsEnv(t, "ext/incus/incus_7.3_13_x86-64.raw\n", "")
			env.dropEnv(name)

			output, err := env.run()
			if err == nil {
				t.Fatalf("%s unset unexpectedly passed: %s", name, output)
			}
			if !strings.Contains(output, "::error::") || !strings.Contains(output, name) {
				t.Fatalf("missing %s output = %q", name, output)
			}
			if calls := env.listCalls(); len(calls) != 0 {
				t.Fatalf("missing %s still made listing calls: %v", name, calls)
			}
		})
	}
}

func TestPublishActionOffersSkipPublishedIdentities(t *testing.T) {
	root := repositoryRoot(t)
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))
	if _, err := os.Stat(filepath.Join(root, "scripts", "check-published-payloads.sh")); err != nil {
		t.Fatalf("payload check script is missing: %v", err)
	}

	input := contractInput(t, action, "skip-published-identities")
	if !strings.Contains(input, "default: 'false'") {
		t.Fatalf("skip-published-identities does not default to 'false':\n%s", input)
	}
	if !strings.Contains(input, "Known gap") {
		t.Fatalf("skip-published-identities does not state the known gap:\n%s", input)
	}
	if !strings.Contains(action, "  published:") {
		t.Fatal("action has no published output")
	}

	validate := contractStep(t, action, "Validate inputs")
	if !strings.Contains(validate, "SKIP_PUBLISHED: ${{ inputs.skip-published-identities }}") {
		t.Fatalf("validate step does not pass the new input through env:\n%s", validate)
	}
	if !strings.Contains(validate, `"$PACKAGE_TYPE" != "sysext"`) {
		t.Fatalf("validate step does not refuse the new input for non-sysext:\n%s", validate)
	}

	run := contractStep(t, action, "Run repogen")
	if !strings.Contains(run, "SKIP_PUBLISHED: ${{ inputs.skip-published-identities }}") {
		t.Fatalf("run-repogen step does not pass the new input through env:\n%s", run)
	}
	for _, required := range []string{"--skip-published-identities", "--skipped-report"} {
		if !strings.Contains(run, required) {
			t.Fatalf("run-repogen step lacks %q:\n%s", required, run)
		}
	}
}

func TestPublishActionChecksSkippedPayloadsBeforeUploading(t *testing.T) {
	root := repositoryRoot(t)
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))

	check := contractStep(t, action, "Check skipped identities have their payloads")
	if !strings.Contains(check, "scripts/check-published-payloads.sh") {
		t.Fatalf("check step does not run the payload script:\n%s", check)
	}
	if !strings.Contains(check, "if: inputs.skip-published-identities == 'true'") {
		t.Fatalf("check step is not gated on the new input:\n%s", check)
	}
	for _, required := range []string{"BUCKET: ${{ inputs.r2-bucket }}", "PREFIX: ${{ inputs.repo-prefix }}"} {
		if !strings.Contains(check, required) {
			t.Fatalf("check step lacks %q:\n%s", required, check)
		}
	}
	run := check[strings.Index(check, "run: |"):]
	if strings.Contains(run, "${{ inputs.") {
		t.Fatalf("check step interpolates an input inside its script:\n%s", run)
	}

	checkAt := strings.Index(action, "- name: Check skipped identities have their payloads")
	uploadAt := strings.Index(action, "- name: Upload repository to R2")
	if checkAt < 0 || uploadAt < 0 || checkAt > uploadAt {
		t.Fatalf("the payload check does not come before the upload (check %d, upload %d)", checkAt, uploadAt)
	}
}

func TestPublishActionSkipsUploadWhenNothingIsPublished(t *testing.T) {
	root := repositoryRoot(t)
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))

	upload := contractStep(t, action, "Upload repository to R2")
	if !strings.Contains(upload, "if: steps.run-repogen.outputs.published == 'true'") {
		t.Fatalf("upload step is not gated on published:\n%s", upload)
	}

	purge := contractStep(t, action, "Purge Cloudflare cache")
	if !strings.Contains(purge, "if: inputs.purge-cache == 'true' && steps.run-repogen.outputs.published == 'true'") {
		t.Fatalf("purge step is not gated on published:\n%s", purge)
	}
}

// contractInput slices one composite-action input definition, from its
// `  <name>:` line to the next input.
func contractInput(t *testing.T, action, name string) string {
	t.Helper()
	marker := "\n  " + name + ":\n"
	start := strings.Index(action, marker)
	if start < 0 {
		t.Fatalf("action has no input named %q", name)
	}

	var block []string
	for _, line := range strings.Split(action[start+len(marker):], "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && line != "" {
			break
		}
		block = append(block, line)
	}

	return strings.Join(block, "\n")
}
