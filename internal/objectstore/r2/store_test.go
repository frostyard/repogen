package r2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/frostyard/repogen/internal/generator/deb"
)

func TestPublicationStoreUsesConditionalWritesAndExactETagReplacement(t *testing.T) {
	api := newMemoryAPI()
	locker, err := NewLocker(api, "locks", "coord")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewPublicationStore(Target{
		Client:   api,
		Bucket:   "repo",
		Prefix:   "repository",
		Codename: "trixie",
		AllowedPoolObject: map[string]deb.PoolDigest{
			"pool/main/f/foo/foo_1.0_amd64.deb": {
				SHA256: digestForTest([]byte("package")),
				Size:   7,
			},
		},
		Locker: locker,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Create(
		context.Background(),
		"pool/main/f/foo/foo_1.0_amd64.deb",
		bytes.NewReader([]byte("package")),
		7,
	); err != nil {
		t.Fatal(err)
	}
	if got := aws.ToString(api.puts[0].IfNoneMatch); got != "*" {
		t.Fatalf("conditional create If-None-Match = %q, want *", got)
	}
	api.objects["repository/dists/trixie/Release"] = []byte("prior")
	api.etags["repository/dists/trixie/Release"] = `"prior-etag"`
	if err := store.Replace(
		context.Background(),
		"dists/trixie/Release",
		bytes.NewReader([]byte("candidate")),
		9,
		digestForTest([]byte("prior")),
	); err != nil {
		t.Fatal(err)
	}
	replacement := api.puts[len(api.puts)-1]
	if got := aws.ToString(replacement.IfMatch); got != `"prior-etag"` {
		t.Fatalf("replacement If-Match = %q, want prior ETag", got)
	}
	if replacement.IfNoneMatch != nil {
		t.Fatal("replacement unexpectedly used If-None-Match")
	}

	writes := len(api.puts)
	api.objects["repository/dists/trixie/InRelease"] = []byte("third-state")
	api.etags["repository/dists/trixie/InRelease"] = `"third"`
	err = store.Replace(
		context.Background(),
		"dists/trixie/InRelease",
		bytes.NewReader([]byte("candidate")),
		9,
		digestForTest([]byte("prior")),
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("third-state Replace() error = %v, want ErrConflict", err)
	}
	if len(api.puts) != writes {
		t.Fatal("third-state replacement performed a write")
	}
}

func TestPublicationStoreRejectsEveryOutOfScopePathBeforeIO(t *testing.T) {
	api := newMemoryAPI()
	locker, _ := NewLocker(api, "locks", "")
	store, err := NewPublicationStore(Target{
		Client:   api,
		Bucket:   "repo",
		Codename: "trixie",
		AllowedPoolObject: map[string]deb.PoolDigest{
			"pool/main/f/foo/foo_1.0_amd64.deb": {
				SHA256: digestForTest([]byte("package")),
				Size:   7,
			},
		},
		Locker: locker,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "stable create",
			call: func() error {
				return store.Create(context.Background(), "dists/stable/by-hash/SHA256/"+strings.Repeat("a", 64), bytes.NewReader(nil), 0)
			},
		},
		{
			name: "other codename replace",
			call: func() error {
				return store.Replace(context.Background(), "dists/forky/Release", bytes.NewReader(nil), 0, strings.Repeat("a", 64))
			},
		},
		{
			name: "sysext read",
			call: func() error {
				_, err := store.Open(context.Background(), "ext/index")
				return err
			},
		},
		{
			name: "public key root create",
			call: func() error {
				return store.Create(context.Background(), "public.key", bytes.NewReader(nil), 0)
			},
		},
		{
			name: "unapproved pool create",
			call: func() error {
				return store.Create(context.Background(), "pool/main/b/bar/bar_1.0_amd64.deb", bytes.NewReader(nil), 0)
			},
		},
		{
			name: "stable list",
			call: func() error {
				_, err := store.ListPrefix(context.Background(), "dists/stable/")
				return err
			},
		},
		{
			name: "other target lock",
			call: func() error {
				_, err := store.Acquire(context.Background(), "forky")
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := api.calls
			if err := test.call(); !errors.Is(err, ErrScope) {
				t.Fatalf("error = %v, want ErrScope", err)
			}
			if api.calls != before {
				t.Fatal("scope rejection contacted the provider")
			}
		})
	}
}

func TestListRequiresCompleteProgressingUniquePagination(t *testing.T) {
	tests := []struct {
		name    string
		pages   []*s3.ListObjectsV2Output
		want    []string
		wantErr bool
	}{
		{
			name: "complete",
			pages: []*s3.ListObjectsV2Output{
				{
					Contents:              []types.Object{{Key: aws.String("p/b")}},
					IsTruncated:           aws.Bool(true),
					NextContinuationToken: aws.String("next"),
				},
				{
					Contents:    []types.Object{{Key: aws.String("p/a")}},
					IsTruncated: aws.Bool(false),
				},
			},
			want: []string{"p/a", "p/b"},
		},
		{
			name: "missing continuation token",
			pages: []*s3.ListObjectsV2Output{{
				IsTruncated: aws.Bool(true),
			}},
			wantErr: true,
		},
		{
			name: "duplicate key",
			pages: []*s3.ListObjectsV2Output{
				{
					Contents:              []types.Object{{Key: aws.String("p/a")}},
					IsTruncated:           aws.Bool(true),
					NextContinuationToken: aws.String("next"),
				},
				{
					Contents:    []types.Object{{Key: aws.String("p/a")}},
					IsTruncated: aws.Bool(false),
				},
			},
			wantErr: true,
		},
		{
			name: "nonprogress token",
			pages: []*s3.ListObjectsV2Output{
				{
					IsTruncated:           aws.Bool(true),
					NextContinuationToken: aws.String("next"),
				},
				{
					IsTruncated:           aws.Bool(true),
					NextContinuationToken: aws.String("next"),
				},
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := newMemoryAPI()
			api.pages = test.pages
			got, err := list(context.Background(), api, "bucket", "p/")
			if test.wantErr {
				if !errors.Is(err, ErrIntegrity) {
					t.Fatalf("list() error = %v, want ErrIntegrity", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("list() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestProviderErrorMappingFailsClosed(t *testing.T) {
	tests := []struct {
		code string
		want error
	}{
		{code: "NoSuchKey", want: errNotFound},
		{code: "AccessDenied", want: ErrAccess},
		{code: "ConditionalRequestConflict", want: ErrConflict},
		{code: "PreconditionFailed", want: ErrConflict},
		{code: "InternalError", want: ErrUnavailable},
		{code: "ServiceUnavailable", want: ErrUnavailable},
		{code: "Unknown", want: ErrProvider},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			err := mapProviderError("test", &smithy.GenericAPIError{
				Code:    test.code,
				Message: "provider detail",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("mapped error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), "provider detail") {
				t.Fatal("provider error detail leaked through the production boundary")
			}
		})
	}
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := mapProviderError("test", err); !errors.Is(got, err) {
			t.Fatalf("context error = %v, want %v", got, err)
		}
	}
}

func TestReplaceRejectsTruncatedReadAndPreconditionFailure(t *testing.T) {
	api := newMemoryAPI()
	locker, _ := NewLocker(api, "locks", "")
	store, _ := NewPublicationStore(Target{
		Client:   api,
		Bucket:   "repo",
		Codename: "trixie",
		Locker:   locker,
	})
	api.objects["dists/trixie/Release"] = []byte("short")
	api.etags["dists/trixie/Release"] = `"etag"`
	err := store.Replace(
		context.Background(),
		"dists/trixie/Release",
		bytes.NewReader([]byte("candidate")),
		9,
		digestForTest([]byte("complete prior")),
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("truncated prior error = %v, want ErrConflict", err)
	}

	api.objects["dists/trixie/Release"] = []byte("prior")
	api.putError = &smithy.GenericAPIError{Code: "PreconditionFailed"}
	err = store.Replace(
		context.Background(),
		"dists/trixie/Release",
		bytes.NewReader([]byte("candidate")),
		9,
		digestForTest([]byte("prior")),
	)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("412 replacement error = %v, want ErrConflict", err)
	}
}

func TestLockerNeverStealsHeldStateAndUsesCASForRelease(t *testing.T) {
	api := newMemoryAPI()
	locker, err := NewLocker(api, "locks", "coord")
	if err != nil {
		t.Fatal(err)
	}
	var next byte
	locker.Random = func(body []byte) (int, error) {
		next++
		for index := range body {
			body[index] = next
		}
		return len(body), nil
	}
	first, err := locker.Acquire(context.Background(), "publication-trixie")
	if err != nil {
		t.Fatal(err)
	}
	writes := len(api.puts)
	if _, err := locker.Acquire(context.Background(), "publication-trixie"); !errors.Is(err, ErrLockHeld) {
		t.Fatalf("second Acquire() error = %v, want ErrLockHeld", err)
	}
	if len(api.puts) != writes+1 {
		t.Fatal("held-lock probe performed an overwrite after failed conditional create")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	release := api.puts[len(api.puts)-1]
	if release.IfMatch == nil || release.IfNoneMatch != nil {
		t.Fatal("lock release did not use exact If-Match CAS")
	}
	second, err := locker.Acquire(context.Background(), "publication-trixie")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}

	third, err := locker.Acquire(context.Background(), "publication-trixie")
	if err != nil {
		t.Fatal(err)
	}
	api.putError = &smithy.GenericAPIError{Code: "PreconditionFailed"}
	if err := third.Release(); !errors.Is(err, ErrConflict) {
		t.Fatalf("release failure = %v, want ErrConflict", err)
	}
}

func TestCredentialsErrorsNeverExposeSecretMaterial(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "credentials.json")
	secret := "super-secret-material"
	if err := os.WriteFile(
		filename,
		[]byte(`{"access_key_id":"id","secret_access_key":"`+secret+`","unexpected":true}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCredentialsFile(filename)
	if err == nil {
		t.Fatal("LoadCredentialsFile() accepted an unknown field")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("credentials error exposed secret material")
	}
	if err := os.Chmod(filename, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentialsFile(filename); !errors.Is(err, ErrAccess) {
		t.Fatalf("mode error = %v, want ErrAccess", err)
	}
}

type memoryAPI struct {
	objects  map[string][]byte
	etags    map[string]string
	puts     []*s3.PutObjectInput
	pages    []*s3.ListObjectsV2Output
	putError error
	getError error
	calls    int
}

func newMemoryAPI() *memoryAPI {
	return &memoryAPI{
		objects: make(map[string][]byte),
		etags:   make(map[string]string),
	}
}

func (m *memoryAPI) GetObject(
	_ context.Context,
	input *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	m.calls++
	if m.getError != nil {
		return nil, m.getError
	}
	key := aws.ToString(input.Key)
	body, ok := m.objects[key]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey"}
	}
	etag := m.etags[key]
	if etag == "" {
		etag = `"` + digestForTest(body)[:16] + `"`
	}
	return &s3.GetObjectOutput{
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: aws.Int64(int64(len(body))),
		ETag:          aws.String(etag),
	}, nil
}

func (m *memoryAPI) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	m.calls++
	m.puts = append(m.puts, input)
	if m.putError != nil {
		return nil, m.putError
	}
	key := aws.ToString(input.Key)
	current, exists := m.objects[key]
	if aws.ToString(input.IfNoneMatch) == "*" && exists {
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
	}
	if input.IfMatch != nil {
		currentETag := m.etags[key]
		if !exists || currentETag != aws.ToString(input.IfMatch) {
			return nil, &smithy.GenericAPIError{Code: "PreconditionFailed"}
		}
	}
	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != aws.ToInt64(input.ContentLength) {
		return nil, errors.New("body length mismatch")
	}
	_ = current
	m.objects[key] = body
	m.etags[key] = `"` + digestForTest(body)[:16] + `"`
	return &s3.PutObjectOutput{ETag: aws.String(m.etags[key])}, nil
}

func (m *memoryAPI) ListObjectsV2(
	_ context.Context,
	input *s3.ListObjectsV2Input,
	_ ...func(*s3.Options),
) (*s3.ListObjectsV2Output, error) {
	m.calls++
	if len(m.pages) > 0 {
		index := 0
		if input.ContinuationToken != nil {
			index = 1
		}
		if index >= len(m.pages) {
			index = len(m.pages) - 1
		}
		return m.pages[index], nil
	}
	prefix := aws.ToString(input.Prefix)
	var contents []types.Object
	for key, body := range m.objects {
		if strings.HasPrefix(key, prefix) {
			contents = append(contents, types.Object{
				Key:  aws.String(key),
				Size: aws.Int64(int64(len(body))),
			})
		}
	}
	return &s3.ListObjectsV2Output{
		Contents:    contents,
		IsTruncated: aws.Bool(false),
	}, nil
}

func digestForTest(body []byte) string {
	value := sha256.Sum256(body)
	return fmt.Sprintf("%x", value)
}
