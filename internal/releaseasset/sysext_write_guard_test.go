package releaseasset

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeAWSObjects is a stand-in for the `aws` commands the sysext write guard
// uses (ADR-0019): `s3 sync` in both directions with --exclude/--include
// filters (AWS semantics: in order, last match wins, `*` matches `/`), and
// `s3api list-objects-v2`, `get-object`, `put-object` (with --if-match and
// --if-none-match, evaluated like R2: a mismatch is a 412 PreconditionFailed)
// and `delete-object`. The bucket is the directory FAKE_BUCKET/<bucket>; an
// object's ETag is the quoted MD5 of its bytes. Every call is logged to
// FAKE_AWS_LOG.
//
// Knobs: FAKE_AWS_NO_CONDITIONAL=1 behaves like an AWS CLI without the
// conditional flags (it refuses them as unknown options).
// FAKE_LOSE_RESPONSE_KEY writes that key once and then reports a connection
// error, like a response lost after a successful write.
// FAKE_DELETE_FAIL=1 makes every delete-object fail.
const fakeAWSObjects = `#!/usr/bin/env bash
set -uo pipefail

log() {
  printf '%s\n' "$*" >> "$FAKE_AWS_LOG"
}

etag_of() {
  printf '"%s"' "$(md5sum < "$1" | cut -d' ' -f1)"
}

matches_filters() {
  local rel="$1" include=1 filter kind pattern
  for filter in ${filters[@]+"${filters[@]}"}; do
    kind="${filter%% *}"
    pattern="${filter#* }"
    if [[ $rel == $pattern ]]; then
      if [[ $kind == "+" ]]; then include=1; else include=0; fi
    fi
  done
  (( include ))
}

if [[ ${1:-} == "s3" && ${2:-} == "sync" ]]; then
  shift 2
  dryrun=0
  sizeonly=0
  filters=()
  positional=()
  while (( $# > 0 )); do
    case "$1" in
      --endpoint-url) shift 2 ;;
      --dryrun) dryrun=1; shift ;;
      --size-only) sizeonly=1; shift ;;
      --exclude) filters+=("- $2"); shift 2 ;;
      --include) filters+=("+ $2"); shift 2 ;;
      -*) echo "fake aws: unsupported flag: $1" >&2; exit 99 ;;
      *) positional+=("$1"); shift ;;
    esac
  done
  [[ ${#positional[@]} -eq 2 ]] || { echo "fake aws: expected SRC and DEST" >&2; exit 99; }
  src="${positional[0]}"
  dst="${positional[1]}"
  if [[ $src == s3://* ]]; then
    log "sync-down ${filters[*]:-}"
    root="$FAKE_BUCKET/${src#s3://}"
    [[ -d $root ]] || exit 0
    while IFS= read -r file; do
      rel="${file#"$root"/}"
      matches_filters "$rel" || continue
      if [[ -f $dst/$rel ]] && cmp -s "$file" "$dst/$rel"; then continue; fi
      mkdir -p "$(dirname "$dst/$rel")"
      cp "$file" "$dst/$rel"
      log "download $rel"
    done < <(find "$root" -type f | LC_ALL=C sort)
    exit 0
  fi
  [[ $dst == s3://* ]] || { echo "fake aws: DEST is not an s3:// path: $dst" >&2; exit 99; }
  log "sync ${filters[*]:-}"
  root="$FAKE_BUCKET/${dst#s3://}"
  while IFS= read -r file; do
    rel="${file#"$src"/}"
    matches_filters "$rel" || continue
    target="$root/$rel"
    if [[ -f $target ]]; then
      if (( sizeonly )); then
        [[ $(stat -c%s "$file") == "$(stat -c%s "$target")" ]] && continue
      elif cmp -s "$file" "$target"; then
        continue
      fi
    fi
    if (( dryrun )); then
      echo "(dryrun) upload: $src/$rel to $dst/$rel"
      continue
    fi
    mkdir -p "$(dirname "$target")"
    cp "$file" "$target"
    log "upload $rel"
  done < <(find "$src" -type f | LC_ALL=C sort)
  exit 0
fi

if [[ ${1:-} != "s3api" ]]; then
  echo "fake aws: unsupported command: ${*:-}" >&2
  exit 99
fi
operation="${2:-}"
shift 2

bucket=""
key=""
prefix=""
body=""
query=""
output=""
if_match=""
if_none_match=""
has_condition=0
positional=()
while (( $# > 0 )); do
  case "$1" in
    --bucket) bucket="$2"; shift 2 ;;
    --key) key="$2"; shift 2 ;;
    --prefix) prefix="$2"; shift 2 ;;
    --body) body="$2"; shift 2 ;;
    --endpoint-url) shift 2 ;;
    --query) query="$2"; shift 2 ;;
    --output) output="$2"; shift 2 ;;
    --if-match) if_match="$2"; has_condition=1; shift 2 ;;
    --if-none-match) if_none_match="$2"; has_condition=1; shift 2 ;;
    -*) echo "fake aws: unsupported flag: $1" >&2; exit 99 ;;
    *) positional+=("$1"); shift ;;
  esac
done
[[ -n $bucket ]] || { echo "fake aws: --bucket is required" >&2; exit 99; }
root="$FAKE_BUCKET/$bucket"

case "$operation" in
  list-objects-v2)
    [[ $query == 'Contents[].[Key]' && $output == text ]] || { echo "fake aws: unexpected list query" >&2; exit 99; }
    log "list-objects-v2 $prefix"
    found=0
    if [[ -d $root ]]; then
      while IFS= read -r file; do
        k="${file#"$root"/}"
        [[ $k == "$prefix"* ]] || continue
        printf '%s\n' "$k"
        found=1
      done < <(find "$root" -type f | LC_ALL=C sort)
    fi
    (( found )) || echo "None"
    ;;
  get-object)
    [[ ${#positional[@]} -eq 1 ]] || { echo "fake aws: get-object needs OUTFILE" >&2; exit 99; }
    log "get-object $key"
    if [[ ! -f $root/$key ]]; then
      echo "An error occurred (NoSuchKey) when calling the GetObject operation: The specified key does not exist." >&2
      exit 254
    fi
    cp "$root/$key" "${positional[0]}"
    if [[ $query == ETag ]]; then
      [[ $output == text ]] || { echo "fake aws: ETag query needs --output text" >&2; exit 99; }
      etag_of "$root/$key"
      echo
    else
      printf '{"ETag": "%s"}\n' "$(etag_of "$root/$key" | sed 's/"/\\"/g')"
    fi
    ;;
  put-object)
    if (( has_condition )) && [[ -n ${FAKE_AWS_NO_CONDITIONAL:-} ]]; then
      echo "" >&2
      echo "usage: aws [options] <command> <subcommand> [<subcommand> ...] [parameters]" >&2
      echo "aws: error: Unknown options: --if-match, --if-none-match" >&2
      exit 252
    fi
    [[ -f $body ]] || { echo "fake aws: --body must be a file" >&2; exit 99; }
    if [[ -n $if_match ]]; then
      log "put-object $key if-match $if_match"
    elif [[ -n $if_none_match ]]; then
      log "put-object $key if-none-match $if_none_match"
    else
      log "put-object $key unconditional"
    fi
    target="$root/$key"
    if [[ -n $if_match ]]; then
      if [[ ! -f $target || "$(etag_of "$target")" != "$if_match" ]]; then
        echo "An error occurred (PreconditionFailed) when calling the PutObject operation: At least one of the pre-conditions you specified did not hold" >&2
        exit 254
      fi
    fi
    if [[ -n $if_none_match ]]; then
      [[ $if_none_match == '*' ]] || { echo "fake aws: --if-none-match takes only *" >&2; exit 99; }
      if [[ -f $target ]]; then
        echo "An error occurred (PreconditionFailed) when calling the PutObject operation: At least one of the pre-conditions you specified did not hold" >&2
        exit 254
      fi
    fi
    mkdir -p "$(dirname "$target")"
    cp "$body" "$target"
    if [[ -n ${FAKE_LOSE_RESPONSE_KEY:-} && $key == "$FAKE_LOSE_RESPONSE_KEY" && ! -f $FAKE_AWS_LOG.lost ]]; then
      touch "$FAKE_AWS_LOG.lost"
      echo "Connection was closed before we received a valid response from endpoint URL" >&2
      exit 255
    fi
    printf '{"ETag": "%s"}\n' "$(etag_of "$target" | sed 's/"/\\"/g')"
    ;;
  delete-object)
    log "delete-object $key"
    if [[ -n ${FAKE_DELETE_FAIL:-} ]]; then
      echo "An error occurred (AccessDenied) when calling the DeleteObject operation" >&2
      exit 254
    fi
    rm -f "$root/$key"
    ;;
  *)
    echo "fake aws: unsupported s3api operation: $operation" >&2
    exit 99
    ;;
esac
`

const (
	guardBucket = "repo-bucket"
	guardPrefix = "prefix"
)

// guardEnv is one fake bucket, one local repo tree, the fake aws on PATH and
// the environment the three sysext scripts read.
type guardEnv struct {
	t          *testing.T
	repoDir    string
	bucketDir  string
	logPath    string
	etagsPath  string
	prunedPath string
	env        []string
}

func newGuardEnv(t *testing.T) *guardEnv {
	t.Helper()
	requireBash(t)

	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "aws"), []byte(fakeAWSObjects), 0o755); err != nil {
		t.Fatal(err)
	}
	env := &guardEnv{
		t:          t,
		repoDir:    filepath.Join(root, "repo"),
		bucketDir:  filepath.Join(root, "bucket"),
		logPath:    filepath.Join(root, "aws.log"),
		etagsPath:  filepath.Join(root, "etags.tsv"),
		prunedPath: filepath.Join(root, "pruned.txt"),
	}
	env.env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"S3_PATH=s3://"+guardBucket+"/"+guardPrefix,
		"R2_ENDPOINT=https://example.r2.cloudflarestorage.com",
		"REPO_DIR="+env.repoDir,
		"UPLOAD_ATTEMPTS=3",
		"UPLOAD_RETRY_DELAY=0",
		"FAKE_BUCKET="+env.bucketDir,
		"FAKE_AWS_LOG="+env.logPath,
	)
	return env
}

func (e *guardEnv) setEnv(key, value string) {
	e.env = append(e.env, key+"="+value)
}

func (e *guardEnv) bucketPath(rel string) string {
	return filepath.Join(e.bucketDir, guardBucket, guardPrefix, rel)
}

func (e *guardEnv) writeBucket(files map[string]string) {
	e.t.Helper()
	for rel, content := range files {
		writeTestFile(e.t, e.bucketPath(rel), content)
	}
}

func (e *guardEnv) writeRepo(files map[string]string) {
	e.t.Helper()
	for rel, content := range files {
		writeTestFile(e.t, filepath.Join(e.repoDir, rel), content)
	}
}

func (e *guardEnv) bucketFile(rel string) (string, bool) {
	e.t.Helper()
	return readOptional(e.t, e.bucketPath(rel))
}

func (e *guardEnv) repoFile(rel string) (string, bool) {
	e.t.Helper()
	return readOptional(e.t, filepath.Join(e.repoDir, rel))
}

func (e *guardEnv) runScript(name string, extraEnv ...string) (string, error) {
	e.t.Helper()
	command := exec.Command("bash", filepath.Join(repositoryRoot(e.t), "scripts", name))
	command.Env = append(append([]string(nil), e.env...), extraEnv...)
	output, err := command.CombinedOutput()
	return string(output), err
}

// sync restores the bucket's sysext metadata as the action does.
func (e *guardEnv) sync() {
	e.t.Helper()
	if output, err := e.runScript("sync-sysext-metadata.sh", "METADATA_ETAGS="+e.etagsPath); err != nil {
		e.t.Fatalf("sync-sysext-metadata.sh failed: %v\n%s", err, output)
	}
}

// upload runs the ordered upload with the sysext write guard on.
func (e *guardEnv) upload() (string, error) {
	e.t.Helper()
	return e.runScript("upload-to-r2.sh", "METADATA_ETAGS="+e.etagsPath)
}

func (e *guardEnv) deletePruned(report string) (string, error) {
	e.t.Helper()
	if err := os.WriteFile(e.prunedPath, []byte(report), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return e.runScript("delete-pruned-payloads.sh", "PRUNED_REPORT="+e.prunedPath)
}

func (e *guardEnv) logLines() []string {
	e.t.Helper()
	data, ok := readOptional(e.t, e.logPath)
	if !ok {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(data, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// indexOf returns the position of the first log line equal to want, or -1.
func (e *guardEnv) indexOf(want string) int {
	return slices.Index(e.logLines(), want)
}

func (e *guardEnv) clearLog() {
	e.t.Helper()
	if err := os.Remove(e.logPath); err != nil && !os.IsNotExist(err) {
		e.t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOptional(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatal(err)
	}
	return string(data), true
}

func quotedMD5(content string) string {
	sum := md5.Sum([]byte(content))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

const (
	publishedManifest  = "aaaa  foo_1_13_x86-64.raw\n"
	publishedSignature = "signature of manifest 1"
)

// publishedBucket is a bucket with one signed sysext.
func publishedBucket() map[string]string {
	return map[string]string{
		"ext/foo/foo_1_13_x86-64.raw": "payload 1",
		"ext/foo/SHA256SUMS":          publishedManifest,
		"ext/foo/SHA256SUMS.gpg":      publishedSignature,
		"ext/foo/foo.transfer":        "transfer",
		"ext/foo/index.html":          "foo index",
		"ext/index":                   "foo\n",
	}
}

// generateNextVersion changes the local tree the way repogen does when it
// adds foo 2 for OS version 14.
func (e *guardEnv) generateNextVersion() (manifest, signature string) {
	e.t.Helper()
	manifest = publishedManifest + "bbbb  foo_2_14_x86-64.raw\n"
	signature = "signature of manifest 2"
	e.writeRepo(map[string]string{
		"ext/foo/foo_2_14_x86-64.raw": "payload 2",
		"ext/foo/SHA256SUMS":          manifest,
		"ext/foo/SHA256SUMS.gpg":      signature,
		"ext/foo/index.html":          "foo index 2",
	})
	return manifest, signature
}

func TestSyncSysextMetadataRecordsETagOfDownloadedBytes(t *testing.T) {
	env := newGuardEnv(t)
	bucket := publishedBucket()
	bucket["ext/bar/SHA256SUMS"] = "cccc  bar_1_14_x86-64.raw\n"
	bucket["ext/bar/bar_1_14_x86-64.raw.zst"] = "bar payload"
	env.writeBucket(bucket)

	env.sync()

	for _, rel := range []string{"ext/foo/SHA256SUMS", "ext/foo/SHA256SUMS.gpg", "ext/foo/foo.transfer", "ext/index", "ext/bar/SHA256SUMS"} {
		got, ok := env.repoFile(rel)
		if !ok || got != bucket[rel] {
			t.Fatalf("restored %s = %q (present %v), want %q", rel, got, ok, bucket[rel])
		}
	}
	for _, payload := range []string{"ext/foo/foo_1_13_x86-64.raw", "ext/bar/bar_1_14_x86-64.raw.zst"} {
		if _, ok := env.repoFile(payload); ok {
			t.Fatalf("restore downloaded payload %s", payload)
		}
	}

	state, ok := readOptional(t, env.etagsPath)
	if !ok {
		t.Fatal("restore wrote no ETag state file")
	}
	want := ""
	for _, rel := range []string{"ext/bar/SHA256SUMS", "ext/foo/SHA256SUMS", "ext/foo/SHA256SUMS.gpg"} {
		want += rel + "\t" + quotedMD5(bucket[rel]) + "\t" + sha256Hex(bucket[rel]) + "\n"
	}
	if state != want {
		t.Fatalf("ETag state =\n%s\nwant\n%s", state, want)
	}

	// The guarded files come from get-object, which returns the ETag of the
	// bytes it wrote; the sync never downloads them.
	for _, line := range env.logLines() {
		if strings.HasPrefix(line, "download ") && strings.Contains(line, "SHA256SUMS") {
			t.Fatalf("aws s3 sync downloaded a guarded file: %s", line)
		}
	}
	for _, rel := range []string{"ext/foo/SHA256SUMS", "ext/foo/SHA256SUMS.gpg", "ext/bar/SHA256SUMS"} {
		if env.indexOf("get-object "+guardPrefix+"/"+rel) < 0 {
			t.Fatalf("restore did not get-object %s: %v", rel, env.logLines())
		}
	}
}

func TestSyncSysextMetadataFirstPublishWritesEmptyState(t *testing.T) {
	env := newGuardEnv(t)
	writeTestFile(t, env.etagsPath, "stale\tline\tfrom an earlier call\n")

	env.sync()

	state, ok := readOptional(t, env.etagsPath)
	if !ok || state != "" {
		t.Fatalf("first-publish ETag state = %q (present %v), want an empty file", state, ok)
	}
}

func TestUploadGuardFirstPublishUsesIfNoneMatch(t *testing.T) {
	env := newGuardEnv(t)
	env.sync()
	env.writeRepo(map[string]string{
		"ext/foo/foo_1_13_x86-64.raw": "payload 1",
		"ext/foo/SHA256SUMS":          publishedManifest,
		"ext/foo/SHA256SUMS.gpg":      publishedSignature,
		"ext/foo/foo.transfer":        "transfer",
		"ext/index":                   "foo\n",
	})
	env.clearLog()

	if output, err := env.upload(); err != nil {
		t.Fatalf("first guarded publish failed: %v\n%s", err, output)
	}

	payload := env.indexOf("upload ext/foo/foo_1_13_x86-64.raw")
	manifest := env.indexOf("put-object " + guardPrefix + "/ext/foo/SHA256SUMS if-none-match *")
	signature := env.indexOf("put-object " + guardPrefix + "/ext/foo/SHA256SUMS.gpg if-none-match *")
	index := env.indexOf("upload ext/index")
	if payload < 0 || manifest < 0 || signature < 0 || index < 0 {
		t.Fatalf("missing calls (payload %d, manifest %d, signature %d, index %d): %v", payload, manifest, signature, index, env.logLines())
	}
	if payload >= manifest || manifest >= signature || signature >= index {
		t.Fatalf("order is not payload < SHA256SUMS < SHA256SUMS.gpg < index: %v", env.logLines())
	}
	for _, line := range env.logLines() {
		if line == "upload ext/foo/SHA256SUMS" || line == "upload ext/foo/SHA256SUMS.gpg" {
			t.Fatalf("phase 3 synced a guarded file without its condition: %v", env.logLines())
		}
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != publishedManifest {
		t.Fatalf("bucket SHA256SUMS = %q", got)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != publishedSignature {
		t.Fatalf("bucket SHA256SUMS.gpg = %q", got)
	}
}

func TestUploadGuardWritesWithRecordedETag(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	manifest, signature := env.generateNextVersion()
	env.clearLog()

	if output, err := env.upload(); err != nil {
		t.Fatalf("guarded upload failed: %v\n%s", err, output)
	}

	if env.indexOf("put-object "+guardPrefix+"/ext/foo/SHA256SUMS if-match "+quotedMD5(publishedManifest)) < 0 {
		t.Fatalf("SHA256SUMS was not written with If-Match of the restored ETag: %v", env.logLines())
	}
	if env.indexOf("put-object "+guardPrefix+"/ext/foo/SHA256SUMS.gpg if-match "+quotedMD5(publishedSignature)) < 0 {
		t.Fatalf("SHA256SUMS.gpg was not written with If-Match of the restored ETag: %v", env.logLines())
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != manifest {
		t.Fatalf("bucket SHA256SUMS = %q, want %q", got, manifest)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != signature {
		t.Fatalf("bucket SHA256SUMS.gpg = %q, want %q", got, signature)
	}
}

func TestUploadGuardConflictFailsAndWritesNoSignature(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	env.generateNextVersion()

	// Another publisher (the other suite) wrote its pair after this run's
	// restore.
	otherManifest := publishedManifest + "dddd  foo_3_13_x86-64.raw\n"
	otherSignature := "signature of the other publisher"
	env.writeBucket(map[string]string{
		"ext/foo/foo_3_13_x86-64.raw": "payload 3",
		"ext/foo/SHA256SUMS":          otherManifest,
		"ext/foo/SHA256SUMS.gpg":      otherSignature,
	})
	env.clearLog()

	output, err := env.upload()
	if err == nil {
		t.Fatalf("upload over a concurrent change succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::another publisher changed "+guardPrefix+"/ext/foo/SHA256SUMS since this run restored it") {
		t.Fatalf("412 output lacks the conflict error:\n%s", output)
	}
	if !strings.Contains(output, "Rerun the job") {
		t.Fatalf("412 output does not say to rerun:\n%s", output)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != otherManifest {
		t.Fatalf("the other publisher's SHA256SUMS was replaced: %q", got)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != otherSignature {
		t.Fatalf("the other publisher's SHA256SUMS.gpg was replaced: %q", got)
	}
	for _, line := range env.logLines() {
		if strings.HasPrefix(line, "put-object "+guardPrefix+"/ext/foo/SHA256SUMS.gpg") {
			t.Fatalf("a signature write followed the refused manifest: %v", env.logLines())
		}
		if line == "upload ext/index" || line == "upload ext/foo/index.html" {
			t.Fatalf("an index was uploaded after the refused manifest: %v", env.logLines())
		}
	}
}

func TestUploadGuardSignatureConflictReportsMismatch(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	manifest, _ := env.generateNextVersion()
	env.writeBucket(map[string]string{"ext/foo/SHA256SUMS.gpg": "signature of an unguarded writer"})

	output, err := env.upload()
	if err == nil {
		t.Fatalf("upload over a changed signature succeeded: %s", output)
	}
	if !strings.Contains(output, "can mismatch") {
		t.Fatalf("signature conflict does not name the mismatched pair:\n%s", output)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != manifest {
		t.Fatalf("bucket SHA256SUMS = %q, want this run's", got)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != "signature of an unguarded writer" {
		t.Fatalf("the changed signature was replaced: %q", got)
	}
}

func TestUploadGuardSkipsUnchangedManifest(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	// repogen signs every manifest again, with a new signature over the same
	// bytes.
	env.writeRepo(map[string]string{"ext/foo/SHA256SUMS.gpg": "fresh signature of manifest 1"})
	env.clearLog()

	if output, err := env.upload(); err != nil {
		t.Fatalf("guarded upload failed: %v\n%s", err, output)
	}
	for _, line := range env.logLines() {
		if strings.HasPrefix(line, "put-object ") {
			t.Fatalf("an unchanged manifest was written: %v", env.logLines())
		}
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != publishedSignature {
		t.Fatalf("the published signature of an unchanged manifest changed to %q", got)
	}
}

func TestUploadGuardRecognizesOwnWriteAfterLostResponse(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	manifest, signature := env.generateNextVersion()
	env.setEnv("FAKE_LOSE_RESPONSE_KEY", guardPrefix+"/ext/foo/SHA256SUMS")
	env.clearLog()

	if output, err := env.upload(); err != nil {
		t.Fatalf("a lost response after a landed write failed the run: %v\n%s", err, output)
	}
	attempts := 0
	for _, line := range env.logLines() {
		if strings.HasPrefix(line, "put-object "+guardPrefix+"/ext/foo/SHA256SUMS if-match") {
			attempts++
		}
	}
	if attempts != 2 {
		t.Fatalf("SHA256SUMS put attempts = %d, want 2 (the lost response, then the 412): %v", attempts, env.logLines())
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != manifest {
		t.Fatalf("bucket SHA256SUMS = %q", got)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS.gpg"); got != signature {
		t.Fatalf("bucket SHA256SUMS.gpg = %q", got)
	}
}

func TestUploadGuardFailsLoudOnCLIWithoutConditionalWrites(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	env.generateNextVersion()
	env.setEnv("FAKE_AWS_NO_CONDITIONAL", "1")
	env.clearLog()

	output, err := env.upload()
	if err == nil {
		t.Fatalf("an aws CLI without --if-match succeeded: %s", output)
	}
	if !strings.Contains(output, "AWS CLI v2.22 or newer is required") {
		t.Fatalf("output does not name the CLI requirement:\n%s", output)
	}
	if got, _ := env.bucketFile("ext/foo/SHA256SUMS"); got != publishedManifest {
		t.Fatalf("SHA256SUMS changed without a condition: %q", got)
	}
	for _, line := range env.logLines() {
		if strings.HasSuffix(line, " unconditional") || line == "upload ext/foo/SHA256SUMS" {
			t.Fatalf("a guarded file was written without its condition: %v", env.logLines())
		}
	}
}

func TestUploadGuardRefusesStaleSignatureBeforeUploading(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	env.generateNextVersion()
	if err := os.Remove(filepath.Join(env.repoDir, "ext", "foo", "SHA256SUMS.gpg")); err != nil {
		t.Fatal(err)
	}
	env.clearLog()

	output, err := env.upload()
	if err == nil {
		t.Fatalf("a new manifest without its signature succeeded: %s", output)
	}
	if !strings.Contains(output, "would leave a stale signature") {
		t.Fatalf("output does not explain the refusal:\n%s", output)
	}
	if lines := env.logLines(); len(lines) != 0 {
		t.Fatalf("the refused tree reached aws: %v", lines)
	}
}

// uploadFilterCalls returns the filter sets of the upload syncs in the log.
func uploadFilterCalls(lines []string) []string {
	var calls []string
	for _, line := range lines {
		if call, ok := strings.CutPrefix(line, "sync "); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func TestUploadWithoutGuardKeepsOtherPackageTypesUnchanged(t *testing.T) {
	files := map[string]string{
		"x86_64/foo-1-1-x86_64.pkg.tar.zst":           "pacman payload",
		"x86_64/myrepo.db":                            "pacman db",
		"aarch64/foo-1.0-r0.apk":                      "apk payload",
		"aarch64/APKINDEX.tar.gz":                     "apk index",
		"bottles/foo--1.0.arm64_sonoma.bottle.tar.gz": "bottle",
		"Formula/foo.rb":                              "formula",
		"index.html":                                  "root index",
	}

	unguarded := newGuardEnv(t)
	unguarded.writeRepo(files)
	if output, err := unguarded.runScript("upload-to-r2.sh"); err != nil {
		t.Fatalf("upload without the guard failed: %v\n%s", err, output)
	}

	// Without METADATA_ETAGS no s3api call is made at all.
	for _, line := range unguarded.logLines() {
		if !strings.HasPrefix(line, "sync ") && !strings.HasPrefix(line, "upload ") {
			t.Fatalf("upload without the guard made a call other than s3 sync: %s", line)
		}
	}
	calls := uploadFilterCalls(unguarded.logLines())
	if len(calls) != 4 {
		t.Fatalf("upload without the guard made %d syncs, want 4 (payloads, verify, metadata, indexes): %v", len(calls), calls)
	}
	if strings.Contains(calls[2], "SHA256SUMS") {
		t.Fatalf("the metadata phase without the guard excludes guarded files: %s", calls[2])
	}
	for rel, content := range files {
		got, ok := unguarded.bucketFile(rel)
		if !ok || got != content {
			t.Fatalf("bucket %s = %q (present %v), want %q", rel, got, ok, content)
		}
	}

	// A sysext tree without METADATA_ETAGS also keeps the old upload.
	sysext := newGuardEnv(t)
	sysext.writeRepo(map[string]string{
		"ext/foo/foo_1_13_x86-64.raw": "payload",
		"ext/foo/SHA256SUMS":          publishedManifest,
	})
	if output, err := sysext.runScript("upload-to-r2.sh"); err != nil {
		t.Fatalf("sysext upload without the guard failed: %v\n%s", err, output)
	}
	if sysext.indexOf("upload ext/foo/SHA256SUMS") < 0 {
		t.Fatalf("without METADATA_ETAGS the manifest no longer goes up in phase 3: %v", sysext.logLines())
	}
}

func TestDeletePrunedPayloadsOnlyAfterMetadataIsLive(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()

	// Retention keeps foo 2 and prunes foo 1.
	newManifest := "bbbb  foo_2_13_x86-64.raw\n"
	env.writeRepo(map[string]string{
		"ext/foo/foo_2_13_x86-64.raw": "payload 2",
		"ext/foo/SHA256SUMS":          newManifest,
		"ext/foo/SHA256SUMS.gpg":      "signature of the pruned manifest",
	})
	report := "ext/foo/foo_1_13_x86-64.raw\n"

	// Before the upload the live manifest still lists foo 1: nothing goes.
	output, err := env.deletePruned(report)
	if err == nil {
		t.Fatalf("delete before the metadata upload succeeded: %s", output)
	}
	if !strings.Contains(output, "is still listed") {
		t.Fatalf("output does not explain the refusal:\n%s", output)
	}
	if _, ok := env.bucketFile("ext/foo/foo_1_13_x86-64.raw"); !ok {
		t.Fatal("a payload the live manifest lists was deleted")
	}

	if output, err := env.upload(); err != nil {
		t.Fatalf("guarded upload failed: %v\n%s", err, output)
	}
	if output, err := env.deletePruned(report); err != nil {
		t.Fatalf("delete after the metadata upload failed: %v\n%s", err, output)
	}
	if _, ok := env.bucketFile("ext/foo/foo_1_13_x86-64.raw"); ok {
		t.Fatal("the pruned payload is still in the bucket")
	}
	if _, ok := env.bucketFile("ext/foo/foo_2_13_x86-64.raw"); !ok {
		t.Fatal("the kept payload was deleted")
	}
}

func TestDeletePrunedPayloadsNeverAfterFailedMetadataWrite(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(publishedBucket())
	env.sync()
	env.writeRepo(map[string]string{
		"ext/foo/foo_2_13_x86-64.raw": "payload 2",
		"ext/foo/SHA256SUMS":          "bbbb  foo_2_13_x86-64.raw\n",
		"ext/foo/SHA256SUMS.gpg":      "signature of the pruned manifest",
	})
	// Another publisher keeps foo 1 listed after this run's restore.
	env.writeBucket(map[string]string{
		"ext/foo/SHA256SUMS":     publishedManifest + "eeee  foo_1_14_x86-64.raw\n",
		"ext/foo/SHA256SUMS.gpg": "signature of the other publisher",
	})

	// The action runs the delete step only after a successful upload; the
	// shell's `&&` stands for that ordering here.
	command := exec.Command("bash", "-c",
		`bash "$1/upload-to-r2.sh" && bash "$1/delete-pruned-payloads.sh"`,
		"pipeline", filepath.Join(repositoryRoot(t), "scripts"))
	if err := os.WriteFile(env.prunedPath, []byte("ext/foo/foo_1_13_x86-64.raw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command.Env = append(append([]string(nil), env.env...),
		"METADATA_ETAGS="+env.etagsPath, "PRUNED_REPORT="+env.prunedPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("pipeline over a concurrent change succeeded: %s", output)
	}
	for _, line := range env.logLines() {
		if strings.HasPrefix(line, "delete-object ") {
			t.Fatalf("a payload was deleted after the metadata write failed: %v", env.logLines())
		}
	}

	// Even when run by hand, the script refuses: the live manifest lists it.
	if output, err := env.deletePruned("ext/foo/foo_1_13_x86-64.raw\n"); err == nil {
		t.Fatalf("delete with a live manifest that lists the payload succeeded: %s", output)
	}
	if _, ok := env.bucketFile("ext/foo/foo_1_13_x86-64.raw"); !ok {
		t.Fatal("a payload the live manifest lists was deleted")
	}
}

func TestDeletePrunedPayloadsValidatesReport(t *testing.T) {
	for _, report := range []string{
		"ext/foo/../SHA256SUMS\n",
		"ext/foo/SHA256SUMS\n",
		"ext/../foo_1_13_x86-64.raw\n",
		"/ext/foo/foo_1_13_x86-64.raw\n",
		"ext/foo/bar/foo_1_13_x86-64.raw\n",
	} {
		env := newGuardEnv(t)
		env.writeBucket(publishedBucket())
		output, err := env.deletePruned(report)
		if err == nil {
			t.Fatalf("report %q was accepted: %s", report, output)
		}
		for _, line := range env.logLines() {
			if strings.HasPrefix(line, "delete-object ") {
				t.Fatalf("report %q deleted an object: %v", report, env.logLines())
			}
		}
	}

	env := newGuardEnv(t)
	if output, err := env.deletePruned(""); err != nil {
		t.Fatalf("an empty report failed: %v\n%s", err, output)
	}
	if lines := env.logLines(); len(lines) != 0 {
		t.Fatalf("an empty report called aws: %v", lines)
	}
}

func TestDeletePrunedPayloadsReportsDeleteFailure(t *testing.T) {
	env := newGuardEnv(t)
	env.writeBucket(map[string]string{
		"ext/foo/foo_1_13_x86-64.raw": "payload 1",
		"ext/foo/foo_2_13_x86-64.raw": "payload 2",
		"ext/foo/SHA256SUMS":          "bbbb  foo_2_13_x86-64.raw\n",
	})
	env.writeRepo(map[string]string{"ext/foo/SHA256SUMS": "bbbb  foo_2_13_x86-64.raw\n"})
	env.setEnv("FAKE_DELETE_FAIL", "1")

	output, err := env.deletePruned("ext/foo/foo_1_13_x86-64.raw\n")
	if err == nil {
		t.Fatalf("a failed delete succeeded: %s", output)
	}
	if !strings.Contains(output, "::error::could not delete "+guardPrefix+"/ext/foo/foo_1_13_x86-64.raw") {
		t.Fatalf("output does not name the object:\n%s", output)
	}
}

func TestPublishActionGuardsSysextMetadataAndPrunesLast(t *testing.T) {
	root := repositoryRoot(t)
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))

	input := contractInput(t, action, "sysext-keep-versions")
	if !strings.Contains(input, "default: '0'") {
		t.Fatalf("sysext-keep-versions must default to '0' (nothing is pruned):\n%s", input)
	}
	validate := contractStep(t, action, "Validate inputs")
	for _, required := range []string{
		"KEEP_VERSIONS: ${{ inputs.sysext-keep-versions }}",
		"sysext-keep-versions supports only package-type sysext",
	} {
		if !strings.Contains(validate, required) {
			t.Fatalf("validate step lacks %q", required)
		}
	}

	sync := contractStep(t, action, "Sync existing repository from R2")
	if !strings.Contains(sync, "scripts/sync-sysext-metadata.sh") {
		t.Fatal("the sysext restore does not record ETags through sync-sysext-metadata.sh")
	}

	run := contractStep(t, action, "Run repogen")
	for _, required := range []string{`rm -f "$PRUNED_REPORT_PATH"`, `"--sysext-keep-versions"`, `"--pruned-report"`} {
		if !strings.Contains(run, required) {
			t.Fatalf("run step lacks %q", required)
		}
	}

	upload := contractStep(t, action, "Upload repository to R2")
	for _, required := range []string{
		`if [[ "$PACKAGE_TYPE" == "sysext" ]]; then`,
		`export METADATA_ETAGS="$METADATA_ETAGS_PATH"`,
		"unset METADATA_ETAGS",
	} {
		if !strings.Contains(upload, required) {
			t.Fatalf("upload step lacks %q:\n%s", required, upload)
		}
	}

	deleteStep := contractStep(t, action, "Delete pruned sysext payloads")
	if !strings.Contains(deleteStep, "scripts/delete-pruned-payloads.sh") {
		t.Fatal("the delete step does not run delete-pruned-payloads.sh")
	}
	for _, forbidden := range []string{"always()", "failure()", "cancelled()", "continue-on-error"} {
		if strings.Contains(deleteStep, forbidden) {
			t.Fatalf("the delete step can run after a failed upload (%s)", forbidden)
		}
	}
	uploadAt := strings.Index(action, "- name: Upload repository to R2")
	purgeAt := strings.Index(action, "- name: Purge Cloudflare cache")
	deleteAt := strings.Index(action, "- name: Delete pruned sysext payloads")
	if uploadAt >= deleteAt || purgeAt >= deleteAt {
		t.Fatalf("the delete step must follow the upload (%d) and the cache purge (%d), at %d", uploadAt, purgeAt, deleteAt)
	}
}
