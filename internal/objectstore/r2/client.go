package r2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Credentials struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
}

func LoadCredentialsFile(filename string) (Credentials, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return Credentials{}, fmt.Errorf("open explicit R2 credentials: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return Credentials{}, errorsWithoutSecret("explicit R2 credentials must be a regular mode-0600 file")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return Credentials{}, fmt.Errorf("read explicit R2 credentials: %w", err)
	}
	var value Credentials
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Credentials{}, errorsWithoutSecret("decode explicit R2 credentials")
	}
	if value.AccessKeyID == "" || value.SecretAccessKey == "" {
		return Credentials{}, errorsWithoutSecret("explicit R2 credentials are incomplete")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Credentials{}, errorsWithoutSecret("explicit R2 credentials contain trailing data")
	}
	return value, nil
}

func NewClient(endpoint, region string, value Credentials, transport http.RoundTripper) (API, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("%w: R2 endpoint must be an origin HTTPS URL", ErrScope)
	}
	if region == "" || value.AccessKeyID == "" || value.SecretAccessKey == "" {
		return nil, fmt.Errorf("%w: explicit R2 region and credentials are required", ErrScope)
	}
	httpClient := &http.Client{
		Timeout: 2 * time.Minute,
	}
	if transport != nil {
		httpClient.Transport = transport
	}
	config := aws.Config{
		Region: region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			value.AccessKeyID,
			value.SecretAccessKey,
			value.SessionToken,
		)),
		HTTPClient: httpClient,
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(options *retry.StandardOptions) {
				options.MaxAttempts = 3
			})
		},
	}
	return s3.NewFromConfig(config, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	}), nil
}

func errorsWithoutSecret(message string) error {
	return fmt.Errorf("%w: %s", ErrAccess, message)
}
