package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/frostyard/repogen/internal/intake"
	"github.com/frostyard/repogen/internal/objectstore/r2"
	"github.com/frostyard/repogen/internal/production"
)

type submitFakeAPI struct {
	mu      sync.Mutex
	objects map[string][]byte // bucket + "\x00" + key
	puts    []*s3.PutObjectInput
	calls   int
}

func newSubmitFakeAPI() *submitFakeAPI {
	return &submitFakeAPI{objects: map[string][]byte{}}
}

func (f *submitFakeAPI) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	body, ok := f.objects[aws.ToString(in.Bucket)+"\x00"+aws.ToString(in.Key)]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	sum := sha256.Sum256(body)
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: aws.Int64(int64(len(body))),
		ETag:          aws.String(`"` + hex.EncodeToString(sum[:8]) + `"`),
	}, nil
}

func (f *submitFakeAPI) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.puts = append(f.puts, in)
	id := aws.ToString(in.Bucket) + "\x00" + aws.ToString(in.Key)
	current, exists := f.objects[id]
	if aws.ToString(in.IfNoneMatch) == "*" && exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if in.IfMatch != nil {
		sum := sha256.Sum256(current)
		if !exists || aws.ToString(in.IfMatch) != `"`+hex.EncodeToString(sum[:8])+`"` {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
		}
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[id] = body
	sum := sha256.Sum256(body)
	return &s3.PutObjectOutput{ETag: aws.String(`"` + hex.EncodeToString(sum[:8]) + `"`)}, nil
}

func (f *submitFakeAPI) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	var contents []types.Object
	for id, body := range f.objects {
		bucket, key, _ := strings.Cut(id, "\x00")
		if bucket == aws.ToString(in.Bucket) && strings.HasPrefix(key, aws.ToString(in.Prefix)) {
			contents = append(contents, types.Object{Key: aws.String(key), Size: aws.Int64(int64(len(body)))})
		}
	}
	return &s3.ListObjectsV2Output{Contents: contents, IsTruncated: aws.Bool(false)}, nil
}

type submitFixture struct {
	dir, config, configSHA, request, provenance, artifacts, credentials string
}

func digestHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newSubmitFixture(t *testing.T, mutate func(*production.SubmitConfig)) submitFixture {
	t.Helper()
	dir := t.TempDir()
	config := production.SubmitConfig{
		Schema: production.SubmitConfigSchema, AccountID: "acct0",
		Endpoint: "https://acct0.r2.cloudflarestorage.com", Region: "auto",
		IntakeBucket: "frostyard-intake", CoordinationBucket: "frostyard-coordination",
		CoordinationPrefix: "locks", PublicationBucket: "frostyardrepo",
		Kind: "debian", Target: "trixie", Producer: "frostyard/gchlog",
	}
	if mutate != nil {
		mutate(&config)
	}
	configData, err := intake.CanonicalJSON(config)
	if err != nil {
		t.Fatal(err)
	}
	provenance := []byte(`{"builder":"fixture"}`)
	artifact := []byte("fixture trixie package\n")
	requestData, err := intake.CanonicalJSON(intake.Request{
		Schema: intake.RequestSchema, Kind: "debian", Operation: "initialize",
		Target: "trixie", Producer: "frostyard/gchlog",
		ProvenanceDigest: digestHex(provenance), ArtifactDigests: []string{digestHex(artifact)},
		Codename: "trixie", Suite: "trixie", Origin: "Frostyard", Label: "Frostyard",
		Component: "main", Architectures: []string{"amd64"}, ValidUntilPolicy: "omit",
		ProductionEligible: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	f := submitFixture{
		dir:         dir,
		config:      filepath.Join(dir, "submit-config.json"),
		configSHA:   digestHex(configData),
		request:     filepath.Join(dir, "request.json"),
		provenance:  filepath.Join(dir, "provenance.json"),
		artifacts:   filepath.Join(dir, "artifacts"),
		credentials: filepath.Join(dir, "r2-credentials.json"),
	}
	for name, data := range map[string][]byte{f.config: configData, f.request: requestData, f.provenance: provenance} {
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(f.artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.artifacts, "gchlog_1.2.3_amd64.deb"), artifact, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.credentials, []byte(`{"access_key_id":"AKIA-cli-test","secret_access_key":"cli-test-secret-value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func runSubmit(t *testing.T, f submitFixture, api *submitFakeAPI) (string, error) {
	t.Helper()
	previous := newSubmitIntakeAPI
	newSubmitIntakeAPI = func(production.SubmitConfig, r2.Credentials) (r2.API, error) { return api, nil }
	t.Cleanup(func() { newSubmitIntakeAPI = previous })
	command := NewSubmitIntakeCmd()
	command.SilenceUsage = true
	command.SilenceErrors = true
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&out)
	command.SetArgs([]string{
		"--config", f.config, "--config-sha256", f.configSHA,
		"--policy-sha256", strings.Repeat("c", 64),
		"--request", f.request, "--provenance", f.provenance,
		"--artifacts-dir", f.artifacts, "--submission-key", "gchlog-run-1",
		"--credentials-file", f.credentials,
	})
	err := command.ExecuteContext(context.Background())
	text := out.String()
	if err != nil {
		text += err.Error()
	}
	if strings.Contains(text, "cli-test-secret-value") || strings.Contains(text, "AKIA-cli-test") {
		t.Fatalf("output leaks a credential: %s", text)
	}
	return out.String(), err
}

func TestSubmitIntakeRequiresEveryFlag(t *testing.T) {
	required := []string{"config", "config-sha256", "policy-sha256", "request", "provenance", "artifacts-dir", "submission-key", "credentials-file"}
	for _, omitted := range required {
		var arguments []string
		for _, name := range required {
			if name != omitted {
				arguments = append(arguments, "--"+name, "fixture")
			}
		}
		command := NewSubmitIntakeCmd()
		command.SilenceUsage, command.SilenceErrors = true, true
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "--"+omitted) {
			t.Fatalf("omitting --%s returned %v", omitted, err)
		}
	}
}

func TestSubmitIntakeHappyPathUsesOnlyConditionalIntakeWrites(t *testing.T) {
	f := newSubmitFixture(t, nil)
	api := newSubmitFakeAPI()
	out, err := runSubmit(t, f, api)
	if err != nil {
		t.Fatal(err)
	}
	var receipt intake.Receipt
	if err := intake.DecodeCanonical([]byte(strings.TrimSuffix(out, "\n")), &receipt); err != nil {
		t.Fatalf("stdout %q: %v", out, err)
	}
	if receipt.Target != "trixie" || receipt.Kind != "debian" || receipt.PolicySHA256 != strings.Repeat("c", 64) {
		t.Fatalf("receipt %+v", receipt)
	}
	intakeWrites := 0
	for _, put := range api.puts {
		bucket, key := aws.ToString(put.Bucket), aws.ToString(put.Key)
		switch bucket {
		case "frostyard-intake":
			intakeWrites++
			if aws.ToString(put.IfNoneMatch) != "*" || put.IfMatch != nil {
				t.Fatalf("intake PUT %s is not create-if-absent", key)
			}
		case "frostyard-coordination":
			if !strings.HasPrefix(key, "locks/") {
				t.Fatalf("lock write outside the coordination prefix: %s", key)
			}
		default:
			t.Fatalf("PUT to bucket %q (%s)", bucket, key)
		}
	}
	if intakeWrites != 5 {
		t.Fatalf("intake writes = %d, want blob, provenance, request, submission, receipt", intakeWrites)
	}
	// Replay is idempotent and returns the same receipt.
	again, err := runSubmit(t, f, api)
	if err != nil || again != out {
		t.Fatalf("replay returned %q, %v; want %q", again, err, out)
	}
}

func assertSubmitRefusedBeforeIO(t *testing.T, f submitFixture, message string) {
	t.Helper()
	api := newSubmitFakeAPI()
	_, err := runSubmit(t, f, api)
	if err == nil || !strings.Contains(err.Error(), message) {
		t.Fatalf("returned %v, want %q", err, message)
	}
	if api.calls != 0 {
		t.Fatalf("made %d provider calls before refusing", api.calls)
	}
}

func TestSubmitIntakeRefusesBeforeIO(t *testing.T) {
	t.Run("config digest mismatch", func(t *testing.T) {
		f := newSubmitFixture(t, nil)
		f.configSHA = strings.Repeat("0", 64)
		assertSubmitRefusedBeforeIO(t, f, "digest")
	})
	t.Run("config target stable", func(t *testing.T) {
		f := newSubmitFixture(t, func(c *production.SubmitConfig) { c.Target = "stable" })
		assertSubmitRefusedBeforeIO(t, f, "stable")
	})
	t.Run("config publication bucket", func(t *testing.T) {
		f := newSubmitFixture(t, func(c *production.SubmitConfig) { c.IntakeBucket = c.PublicationBucket })
		assertSubmitRefusedBeforeIO(t, f, "publication")
	})
	t.Run("producer mismatch", func(t *testing.T) {
		f := newSubmitFixture(t, func(c *production.SubmitConfig) { c.Producer = "frostyard/other" })
		assertSubmitRefusedBeforeIO(t, f, "producer")
	})
	t.Run("credentials mode 0644", func(t *testing.T) {
		f := newSubmitFixture(t, nil)
		if err := os.Chmod(f.credentials, 0o644); err != nil {
			t.Fatal(err)
		}
		assertSubmitRefusedBeforeIO(t, f, "mode-0600")
	})
	t.Run("credentials symlink", func(t *testing.T) {
		f := newSubmitFixture(t, nil)
		link := filepath.Join(f.dir, "link.json")
		if err := os.Symlink(f.credentials, link); err != nil {
			t.Fatal(err)
		}
		f.credentials = link
		assertSubmitRefusedBeforeIO(t, f, "mode-0600")
	})
	t.Run("extra artifact", func(t *testing.T) {
		f := newSubmitFixture(t, nil)
		if err := os.WriteFile(filepath.Join(f.artifacts, "extra.deb"), []byte("extra"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertSubmitRefusedBeforeIO(t, f, "do not match")
	})
	t.Run("empty artifacts dir", func(t *testing.T) {
		f := newSubmitFixture(t, nil)
		if err := os.Remove(filepath.Join(f.artifacts, "gchlog_1.2.3_amd64.deb")); err != nil {
			t.Fatal(err)
		}
		assertSubmitRefusedBeforeIO(t, f, "do not match")
	})
}

func TestSubmitIntakeRejectsSymlinkArtifact(t *testing.T) {
	f := newSubmitFixture(t, nil)
	name := filepath.Join(f.artifacts, "gchlog_1.2.3_amd64.deb")
	real := filepath.Join(f.dir, "real.deb")
	if err := os.Rename(name, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, name); err != nil {
		t.Fatal(err)
	}
	assertSubmitRefusedBeforeIO(t, f, "symlink")
}
