package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeAWS is a minimal stand-in for `aws s3 sync` used by the ordered-upload
// tests. It accepts only the flags scripts/upload-to-r2.sh passes, maps the
// s3:// destination to a local directory, applies --exclude/--include filters
// with AWS semantics (in order, last match wins, default include, `*` matches
// `/`), and can inject a transient or permanent payload failure.
const fakeAWS = `#!/usr/bin/env bash
set -uo pipefail

log() {
  printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
}

if [[ ${1:-} != "s3" || ${2:-} != "sync" ]]; then
  echo "fake aws: unsupported command: ${*:-}" >&2
  exit 99
fi
shift 2

dryrun=0
sizeonly=0
filters=()
positional=()
while (( $# > 0 )); do
  case "$1" in
    --endpoint-url)
      [[ $# -ge 2 ]] || { echo "fake aws: --endpoint-url needs a value" >&2; exit 99; }
      shift 2
      ;;
    --dryrun)
      dryrun=1
      shift
      ;;
    --size-only)
      sizeonly=1
      shift
      ;;
    --exclude)
      [[ $# -ge 2 ]] || { echo "fake aws: --exclude needs a value" >&2; exit 99; }
      filters+=("- $2")
      shift 2
      ;;
    --include)
      [[ $# -ge 2 ]] || { echo "fake aws: --include needs a value" >&2; exit 99; }
      filters+=("+ $2")
      shift 2
      ;;
    -*)
      echo "fake aws: unsupported flag: $1" >&2
      exit 99
      ;;
    *)
      positional+=("$1")
      shift
      ;;
  esac
done

if [[ ${#positional[@]} -ne 2 ]]; then
  echo "fake aws: expected SRC and DEST" >&2
  exit 99
fi
src="${positional[0]}"
dst="${positional[1]}"
if [[ $dst != s3://* ]]; then
  echo "fake aws: DEST is not an s3:// path: $dst" >&2
  exit 99
fi

log "sync ${filters[*]:-}"

root="$FAKE_BUCKET/${dst#s3://}"

failed=0
while IFS= read -r file; do
  rel="${file#"$src"/}"

  include=1
  for filter in ${filters[@]+"${filters[@]}"}; do
    kind="${filter%% *}"
    pattern="${filter#* }"
    if [[ $rel == $pattern ]]; then
      if [[ $kind == "+" ]]; then
        include=1
      else
        include=0
      fi
    fi
  done
  (( include )) || continue

  target="$root/$rel"
  if [[ -f $target ]]; then
    if (( sizeonly )); then
      if [[ $(stat -c%s "$file") == "$(stat -c%s "$target")" ]]; then
        continue
      fi
    elif cmp -s "$file" "$target"; then
      continue
    fi
  fi

  if (( dryrun )); then
    echo "(dryrun) upload: $src/$rel to $dst/$rel"
    continue
  fi

  if [[ -n ${FAKE_FAIL_PATTERN:-} && $rel == $FAKE_FAIL_PATTERN ]]; then
    remaining=$(cat "$FAKE_FAIL_STATE" 2>/dev/null || echo 0)
    if (( remaining > 0 )); then
      printf '%s\n' "$((remaining - 1))" > "$FAKE_FAIL_STATE"
      echo "upload failed: $file to $target: InvalidPart: One or more of the specified parts could not be found" >&2
      failed=1
      continue
    fi
  fi

  if [[ -n ${FAKE_SKIP_PATTERN:-} && $rel == $FAKE_SKIP_PATTERN ]]; then
    continue
  fi

  mkdir -p "$(dirname "$target")"
  cp "$file" "$target"
  log "upload $rel"
done < <(find "$src" -type f | LC_ALL=C sort)

exit "$failed"
`

const (
	uploadBucket    = "repo-bucket"
	uploadPrefix    = "prefix"
	payloadPhase    = "payload"
	metadataPhase   = "metadata"
	indexPhase      = "index"
	uploadAttempts  = 3
	uploadFailTries = 3
)

type uploadEnv struct {
	t             *testing.T
	repoDir       string
	bucketDir     string
	logPath       string
	failStatePath string
	env           []string
}

func newUploadEnv(t *testing.T, files map[string]string) *uploadEnv {
	t.Helper()

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakePath := filepath.Join(binDir, "aws")
	if err := os.WriteFile(fakePath, []byte(fakeAWS), 0o755); err != nil {
		t.Fatal(err)
	}

	env := &uploadEnv{
		t:             t,
		repoDir:       filepath.Join(root, "repo"),
		bucketDir:     filepath.Join(root, "bucket"),
		logPath:       filepath.Join(root, "aws.log"),
		failStatePath: filepath.Join(root, "fail-state"),
	}
	env.writeFiles(files)
	env.env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"S3_PATH=s3://"+uploadBucket+"/"+uploadPrefix,
		"R2_ENDPOINT=https://example.r2.cloudflarestorage.com",
		"REPO_DIR="+env.repoDir,
		"UPLOAD_ATTEMPTS="+strconv.Itoa(uploadAttempts),
		"UPLOAD_RETRY_DELAY=0",
		"FAKE_BUCKET="+env.bucketDir,
		"FAKE_AWS_LOG="+env.logPath,
		"FAKE_FAIL_STATE="+env.failStatePath,
	)

	return env
}

func (e *uploadEnv) writeFiles(files map[string]string) {
	e.t.Helper()
	for rel, content := range files {
		path := filepath.Join(e.repoDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *uploadEnv) setEnv(key, value string) {
	e.env = append(e.env, key+"="+value)
}

func (e *uploadEnv) setFailCounter(count int) {
	e.t.Helper()
	if err := os.WriteFile(e.failStatePath, []byte(strconv.Itoa(count)+"\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *uploadEnv) run() (string, error) {
	e.t.Helper()
	script := filepath.Join(repositoryRoot(e.t), "scripts", "upload-to-r2.sh")
	command := exec.Command("bash", script)
	command.Env = e.env
	output, err := command.CombinedOutput()

	return string(output), err
}

// logLines returns every line the fake aws appended, in order. A run that
// never called the fake returns nothing.
func (e *uploadEnv) logLines() []string {
	e.t.Helper()
	data, err := os.ReadFile(e.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		e.t.Fatal(err)
	}

	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

// uploads returns the relative paths the fake aws copied, in order.
func (e *uploadEnv) uploads() []string {
	e.t.Helper()
	var rels []string
	for _, line := range e.logLines() {
		if rel, ok := strings.CutPrefix(line, "upload "); ok {
			rels = append(rels, rel)
		}
	}

	return rels
}

// syncCalls returns every filter set the script asked the fake aws to sync.
func (e *uploadEnv) syncCalls() []string {
	e.t.Helper()
	var calls []string
	for _, line := range e.logLines() {
		if call, ok := strings.CutPrefix(line, "sync "); ok {
			calls = append(calls, call)
		}
	}

	return calls
}

func (e *uploadEnv) bucketFile(rel string) (string, bool) {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.bucketDir, uploadBucket, uploadPrefix, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		e.t.Fatal(err)
	}

	return string(data), true
}

func (e *uploadEnv) writeBucketFile(rel, content string) {
	e.t.Helper()
	path := filepath.Join(e.bucketDir, uploadBucket, uploadPrefix, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not available")
	}
}

// assertPhaseOrder fails when an upload of a later phase appears before an
// upload of an earlier one. Phases are, in order: payload, metadata, index.
func assertPhaseOrder(t *testing.T, uploads []string, phases map[string]string) {
	t.Helper()

	order := map[string]int{payloadPhase: 0, metadataPhase: 1, indexPhase: 2}
	highest := -1
	for _, rel := range uploads {
		phase, ok := phases[rel]
		if !ok {
			t.Fatalf("uploaded %q has no expected phase", rel)
		}
		if order[phase] < highest {
			t.Fatalf("upload %q (%s) came after a later phase in %v", rel, phase, uploads)
		}
		highest = order[phase]
	}
}

func TestUploadToR2OrdersPayloadsBeforeMetadataBeforeIndexes(t *testing.T) {
	requireBash(t)

	files := map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": "payload",
		"ext/foo/SHA256SUMS":           "manifest",
		"ext/foo/SHA256SUMS.gpg":       "signature",
		"ext/foo/foo.transfer":         "transfer",
		"ext/foo/index.html":           "index",
		"ext/index":                    "index index",
		"ext/index.html":               "index html",
		"index.html":                   "root index",
	}
	env := newUploadEnv(t, files)

	output, err := env.run()
	if err != nil {
		t.Fatalf("ordered upload failed: %v\n%s", err, output)
	}

	uploads := env.uploads()
	if len(uploads) != len(files) {
		t.Fatalf("uploaded %d files, want %d: %v", len(uploads), len(files), uploads)
	}
	phases := map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": payloadPhase,
		"ext/foo/SHA256SUMS":           metadataPhase,
		"ext/foo/SHA256SUMS.gpg":       metadataPhase,
		"ext/foo/foo.transfer":         metadataPhase,
		"ext/foo/index.html":           indexPhase,
		"ext/index":                    indexPhase,
		"ext/index.html":               indexPhase,
		"index.html":                   indexPhase,
	}
	assertPhaseOrder(t, uploads, phases)

	for rel, content := range files {
		got, ok := env.bucketFile(rel)
		if !ok {
			t.Fatalf("bucket lacks %q after a successful upload", rel)
		}
		if got != content {
			t.Fatalf("bucket %q = %q, want %q", rel, got, content)
		}
	}
}

func TestUploadToR2ClassifiesEveryFormatPayload(t *testing.T) {
	requireBash(t)

	table := []struct {
		rel   string
		phase string
	}{
		{"x86_64/foo-1-1-x86_64.pkg.tar.zst", payloadPhase},
		{"x86_64/foo-1-1-x86_64.pkg.tar.zst.sig", payloadPhase},
		{"x86_64/foo-1-1-x86_64.pkg.tar.xz", payloadPhase},
		{"x86_64/foo-1-1-x86_64.pkg.tar.xz.sig", payloadPhase},
		{"x86_64/myrepo.db", metadataPhase},
		{"myrepo.db.tar.zst", metadataPhase},
		{"myrepo.db.sig", metadataPhase},
		{"aarch64/foo-1.0-r0.apk", payloadPhase},
		{"aarch64/APKINDEX.tar.gz", metadataPhase},
		{"bottles/foo--1.0.arm64_sonoma.bottle.tar.gz", payloadPhase},
		{"Formula/foo.rb", metadataPhase},
	}
	phases := make(map[string]string, len(table))
	files := make(map[string]string, len(table))
	for _, row := range table {
		phases[row.rel] = row.phase
		files[row.rel] = "content of " + row.rel
	}
	env := newUploadEnv(t, files)

	output, err := env.run()
	if err != nil {
		t.Fatalf("ordered upload failed: %v\n%s", err, output)
	}

	uploads := env.uploads()
	if len(uploads) != len(files) {
		t.Fatalf("uploaded %d files, want %d: %v", len(uploads), len(files), uploads)
	}
	for rel := range phases {
		if _, ok := env.bucketFile(rel); !ok {
			t.Fatalf("bucket lacks %q", rel)
		}
	}
	assertPhaseOrder(t, uploads, phases)
}

func TestUploadToR2StopsBeforeMetadataOnPermanentPayloadFailure(t *testing.T) {
	requireBash(t)

	env := newUploadEnv(t, map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": "payload",
		"ext/foo/SHA256SUMS":           "new manifest",
		"index.html":                   "root index",
	})
	env.writeBucketFile("ext/foo/SHA256SUMS", "old manifest")
	env.setEnv("FAKE_FAIL_PATTERN", "ext/foo/*.raw.zst")
	env.setFailCounter(100)

	output, err := env.run()
	if err == nil {
		t.Fatalf("permanent payload failure unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::") {
		t.Fatalf("failure output lacks a ::error:: annotation: %s", output)
	}
	if got := strings.Count(output, "InvalidPart"); got != uploadFailTries {
		t.Fatalf("payload was tried %d times, want %d:\n%s", got, uploadFailTries, output)
	}

	uploads := env.uploads()
	if len(uploads) != 0 {
		t.Fatalf("failed payload phase uploaded %v, want nothing", uploads)
	}
	manifest, ok := env.bucketFile("ext/foo/SHA256SUMS")
	if !ok {
		t.Fatal("pre-existing published manifest disappeared")
	}
	if manifest != "old manifest" {
		t.Fatalf("pre-existing published manifest changed to %q", manifest)
	}
	if _, ok := env.bucketFile("index.html"); ok {
		t.Fatal("index was uploaded although the payload phase failed")
	}
}

func TestUploadToR2RetriesTransientPayloadFailure(t *testing.T) {
	requireBash(t)

	env := newUploadEnv(t, map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": "payload",
		"ext/foo/SHA256SUMS":           "manifest",
		"index.html":                   "root index",
	})
	env.setEnv("FAKE_FAIL_PATTERN", "ext/foo/*.raw.zst")
	env.setFailCounter(1)

	output, err := env.run()
	if err != nil {
		t.Fatalf("transient payload failure was not retried: %v\n%s", err, output)
	}

	uploads := env.uploads()
	want := []string{"ext/foo/foo_1_x86-64.raw.zst", "ext/foo/SHA256SUMS", "index.html"}
	if len(uploads) != len(want) {
		t.Fatalf("uploads = %v, want %v", uploads, want)
	}
	for rel, content := range map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": "payload",
		"ext/foo/SHA256SUMS":           "manifest",
		"index.html":                   "root index",
	} {
		got, ok := env.bucketFile(rel)
		if !ok {
			t.Fatalf("bucket lacks %q after a retried upload", rel)
		}
		if got != content {
			t.Fatalf("bucket %q = %q, want %q", rel, got, content)
		}
	}

	payloadIndex, metadataIndex := -1, -1
	for index, rel := range uploads {
		switch rel {
		case "ext/foo/foo_1_x86-64.raw.zst":
			payloadIndex = index
		case "ext/foo/SHA256SUMS":
			metadataIndex = index
		}
	}
	if payloadIndex < 0 || metadataIndex < 0 || payloadIndex > metadataIndex {
		t.Fatalf("payload upload (index %d) did not precede metadata (index %d) in %v", payloadIndex, metadataIndex, uploads)
	}
}

func TestUploadToR2VerifyPhaseStopsSilentlySkippedPayload(t *testing.T) {
	requireBash(t)

	env := newUploadEnv(t, map[string]string{
		"ext/foo/foo_1_x86-64.raw.zst": "payload",
		"ext/foo/SHA256SUMS":           "manifest",
		"index.html":                   "root index",
	})
	env.setEnv("FAKE_SKIP_PATTERN", "ext/foo/*.raw.zst")

	output, err := env.run()
	if err == nil {
		t.Fatalf("silently skipped payload unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::") {
		t.Fatalf("failure output lacks a ::error:: annotation: %s", output)
	}
	if !strings.Contains(output, "Phase 2 (verify payloads)") {
		t.Fatalf("failure did not come from the verify phase: %s", output)
	}
	if _, ok := env.bucketFile("ext/foo/SHA256SUMS"); ok {
		t.Fatal("metadata was uploaded although the payload was missing from the bucket")
	}
	if _, ok := env.bucketFile("index.html"); ok {
		t.Fatal("index was uploaded although the payload was missing from the bucket")
	}
}

func TestUploadToR2KeepsPayloadLikeDirectoryManifestOutOfPayloadPhase(t *testing.T) {
	requireBash(t)

	// A sysext identity may itself contain ".pkg.tar", so its directory name
	// looks like a pacman payload. A trailing-wildcard payload pattern such as
	// "*.pkg.tar.*" matches this directory's SHA256SUMS; phase 1 then uploaded
	// that manifest beside its payload. The patterns are end-anchored, and only
	// the base name counts, so the manifest stays in phase 3.
	files := map[string]string{
		"ext/foo.pkg.tar.x/foo.pkg.tar.x_1_13_x86-64.raw": "payload",
		"ext/foo.pkg.tar.x/SHA256SUMS":                    "manifest",
		"ext/foo.pkg.tar.x/foo.pkg.tar.x.transfer":        "transfer",
		"ext/foo.pkg.tar.x/index.html":                    "index",
	}
	env := newUploadEnv(t, files)

	output, err := env.run()
	if err != nil {
		t.Fatalf("ordered upload failed: %v\n%s", err, output)
	}

	want := []string{
		"ext/foo.pkg.tar.x/foo.pkg.tar.x_1_13_x86-64.raw",
		"ext/foo.pkg.tar.x/SHA256SUMS",
		"ext/foo.pkg.tar.x/foo.pkg.tar.x.transfer",
		"ext/foo.pkg.tar.x/index.html",
	}
	uploads := env.uploads()
	if len(uploads) != len(want) {
		t.Fatalf("uploads = %v, want %v", uploads, want)
	}
	for index, rel := range want {
		if uploads[index] != rel {
			t.Fatalf("upload %d = %q, want %q (all: %v)", index, uploads[index], rel, uploads)
		}
	}
	for rel, content := range files {
		got, ok := env.bucketFile(rel)
		if !ok {
			t.Fatalf("bucket lacks %q after a successful upload", rel)
		}
		if got != content {
			t.Fatalf("bucket %q = %q, want %q", rel, got, content)
		}
	}
}

func TestUploadToR2FailedPayloadKeepsPayloadLikeDirectoryManifest(t *testing.T) {
	requireBash(t)

	env := newUploadEnv(t, map[string]string{
		"ext/foo.pkg.tar.x/foo.pkg.tar.x_1_13_x86-64.raw": "payload",
		"ext/foo.pkg.tar.x/SHA256SUMS":                    "new manifest",
		"ext/foo.pkg.tar.x/foo.pkg.tar.x.transfer":        "transfer",
		"ext/foo.pkg.tar.x/index.html":                    "index",
	})
	env.writeBucketFile("ext/foo.pkg.tar.x/SHA256SUMS", "old manifest")
	env.setEnv("FAKE_FAIL_PATTERN", "ext/foo.pkg.tar.x/*.raw")
	env.setFailCounter(100)

	output, err := env.run()
	if err == nil {
		t.Fatalf("permanent payload failure unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::") {
		t.Fatalf("failure output lacks a ::error:: annotation: %s", output)
	}
	// The failure must come from the payload phase, not from phase 0
	// refusing this tree: the tree is valid.
	if !strings.Contains(output, "Phase 1 (payloads)") {
		t.Fatalf("failure did not reach the payload phase: %s", output)
	}
	if calls := env.syncCalls(); len(calls) == 0 {
		t.Fatal("no sync call was made: phase 0 refused a valid tree")
	}

	manifest, ok := env.bucketFile("ext/foo.pkg.tar.x/SHA256SUMS")
	if !ok {
		t.Fatal("pre-existing published manifest disappeared")
	}
	if manifest != "old manifest" {
		t.Fatalf("pre-existing published manifest changed to %q", manifest)
	}
	if _, ok := env.bucketFile("ext/foo.pkg.tar.x/index.html"); ok {
		t.Fatal("index was uploaded although the payload phase failed")
	}
	if uploads := env.uploads(); len(uploads) != 0 {
		t.Fatalf("failed payload phase uploaded %v, want nothing", uploads)
	}
}

func TestUploadToR2KeepsRawNamedDirectoryMetadataOutOfPayloadPhase(t *testing.T) {
	requireBash(t)

	// An extension may itself be named "foo.raw", so its directory name and its
	// transfer file both carry ".raw". The scanner detects a sysext only by an
	// end-anchored suffix, so the classify guard does not look at ".raw" at
	// all, and these metadata files stay in phase 3.
	files := map[string]string{
		"ext/foo.raw/foo.raw_1_13_x86-64.raw.zst": "payload",
		"ext/foo.raw/SHA256SUMS":                  "manifest",
		"ext/foo.raw/foo.raw.transfer":            "transfer",
	}
	env := newUploadEnv(t, files)

	output, err := env.run()
	if err != nil {
		t.Fatalf("ordered upload failed: %v\n%s", err, output)
	}

	want := []string{
		"ext/foo.raw/foo.raw_1_13_x86-64.raw.zst",
		"ext/foo.raw/SHA256SUMS",
		"ext/foo.raw/foo.raw.transfer",
	}
	uploads := env.uploads()
	if len(uploads) != len(want) {
		t.Fatalf("uploads = %v, want %v", uploads, want)
	}
	for index, rel := range want {
		if uploads[index] != rel {
			t.Fatalf("upload %d = %q, want %q (all: %v)", index, uploads[index], rel, uploads)
		}
	}
	for rel, content := range files {
		got, ok := env.bucketFile(rel)
		if !ok {
			t.Fatalf("bucket lacks %q after a successful upload", rel)
		}
		if got != content {
			t.Fatalf("bucket %q = %q, want %q", rel, got, content)
		}
	}
}

func TestUploadToR2ClassifyGuardRefusesUnmatchedPayloadLikeName(t *testing.T) {
	requireBash(t)

	env := newUploadEnv(t, map[string]string{
		"x86_64/foo-1-1-x86_64.pkg.tar.weird": "payload",
		"x86_64/myrepo.db":                    "database",
	})

	output, err := env.run()
	if err == nil {
		t.Fatalf("unmatched payload-like file name unexpectedly succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::") {
		t.Fatalf("failure output lacks a ::error:: annotation: %s", output)
	}
	if !strings.Contains(output, "x86_64/foo-1-1-x86_64.pkg.tar.weird") {
		t.Fatalf("failure output does not name the file: %s", output)
	}
	if calls := env.syncCalls(); len(calls) != 0 {
		t.Fatalf("guard let %d sync calls through, want none: %v", len(calls), calls)
	}
	if uploads := env.uploads(); len(uploads) != 0 {
		t.Fatalf("guard uploaded %v, want nothing", uploads)
	}
}

func TestPublishActionUsesOrderedUploadScript(t *testing.T) {
	root := repositoryRoot(t)
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))
	if strings.Contains(action, `aws s3 sync "./repo"`) {
		t.Fatal("publish action still runs a single parallel upload of ./repo")
	}
	if _, err := os.Stat(filepath.Join(root, "scripts", "upload-to-r2.sh")); err != nil {
		t.Fatalf("ordered upload script is missing: %v", err)
	}

	step := contractStep(t, action, "Upload repository to R2")
	for _, required := range []string{
		"scripts/upload-to-r2.sh",
		"REPO_DIR=./repo",
		"BUCKET: ${{ inputs.r2-bucket }}",
		"PREFIX: ${{ inputs.repo-prefix }}",
	} {
		if !strings.Contains(step, required) {
			t.Fatalf("upload step lacks %q:\n%s", required, step)
		}
	}
	run := step[strings.Index(step, "run: |"):]
	for _, forbidden := range []string{"${{ inputs.r2-bucket }}", "${{ inputs.repo-prefix }}"} {
		if strings.Contains(run, forbidden) {
			t.Fatalf("upload step interpolates %s inside its script:\n%s", forbidden, run)
		}
	}
}

// contractStep slices one composite-action step, from its `- name:` line to
// the next step.
func contractStep(t *testing.T, action, name string) string {
	t.Helper()
	marker := "- name: " + name + "\n"
	start := strings.Index(action, marker)
	if start < 0 {
		t.Fatalf("action has no step named %q", name)
	}
	rest := action[start+len(marker):]
	if next := strings.Index(rest, "\n    - name: "); next >= 0 {
		rest = rest[:next]
	}

	return rest
}
