package awsauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/smithy-go"
)

// expiredCredsHelp is the operator-facing help message produced when the
// SDK reports expired credentials. Audit decision B33: one combined
// message — SSO/static-keys for external partners, Isengard/Ada for
// Amazon Cloud Desktop users. No build tags.
const expiredCredsHelp = `AWS credentials expired. To refresh, run one of:
  aws sso login --profile <profile>     # for SSO users
  aws configure                         # for static keys
  isengardcli assume <account>          # if you're on an Amazon Cloud Desktop
  ada credentials update                # alternative for Amazon Cloud Desktop users`

// ErrExpiredCredentials is the sentinel returned (wrapped) by LoadConfig
// and Diagnose when the SDK reports the active credentials are expired.
// The wrapping error always carries the multi-line refresh hint above.
var ErrExpiredCredentials = errors.New("aws: credentials expired")

// LoadConfig builds an aws.Config from the default credential chain
// (env -> SSO -> credential_process -> shared profile -> IMDS).
//
// region is the desired Bedrock region. If empty, resolution falls back to
// AWS_REGION (Go default), then AWS_DEFAULT_REGION (Python/boto3 default —
// honored for parity with users coming from the Python implementation).
//
// profile, when non-empty, overrides AWS_PROFILE.
func LoadConfig(ctx context.Context, region, profile string) (aws.Config, error) {
	opts := []func(*awsconfig.LoadOptions) error{}

	if region == "" {
		region = firstNonEmpty(os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("aws: load config: %w", classifyAuthErr(err))
	}
	return cfg, nil
}

// classifyAuthErr returns ErrExpiredCredentials wrapped over the original
// error if the failure looks like a token-expiry condition. Otherwise
// returns err unchanged.
//
// The SDK does not export a single typed error for "expired credentials":
// remote APIs surface a smithy.APIError with code "ExpiredToken" /
// "ExpiredTokenException", and local SSO providers return wrapped errors
// whose message contains "expired". Both are handled.
func classifyAuthErr(err error) error {
	if err == nil {
		return nil
	}
	if isExpiredCredentials(err) {
		return fmt.Errorf("%w: %v\n\n%s", ErrExpiredCredentials, err, expiredCredsHelp)
	}
	return err
}

// isExpiredCredentials reports whether err is a credential-expiry signal.
// Exported for testing; callers should prefer errors.Is(err, ErrExpiredCredentials).
func isExpiredCredentials(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "ExpiredToken", "ExpiredTokenException", "TokenRefreshRequired":
			return true
		}
	}
	// SSO and credential_process providers wrap a string error; match a
	// conservative substring to catch them.
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "sso session has expired"),
		strings.Contains(msg, "cached sso token is expired"),
		strings.Contains(msg, "the security token included in the request is expired"):
		return true
	}
	return false
}

// firstNonEmpty returns the first non-empty string in s, or "" if all are empty.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
