package awsauth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	smithy "github.com/aws/smithy-go"
)

// ------------------------------------------------------------------
// Region resolution: AWS_REGION wins; AWS_DEFAULT_REGION (boto3) is the
// fallback for parity with users coming from the Python implementation.
// ------------------------------------------------------------------

func TestLoadConfig_PrefersExplicitRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_DEFAULT_REGION", "eu-central-1")

	cfg, err := LoadConfig(context.Background(), "us-east-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("region = %q, want explicit override us-east-1", cfg.Region)
	}
}

func TestLoadConfig_FallsBackToAWSRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("AWS_DEFAULT_REGION", "eu-central-1")

	cfg, err := LoadConfig(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-west-2" {
		t.Errorf("region = %q, want AWS_REGION us-west-2", cfg.Region)
	}
}

func TestLoadConfig_FallsBackToAWSDefaultRegion(t *testing.T) {
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "eu-central-1")

	cfg, err := LoadConfig(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "eu-central-1" {
		t.Errorf("region = %q, want AWS_DEFAULT_REGION eu-central-1", cfg.Region)
	}
}

// ------------------------------------------------------------------
// Expired-credentials detection
// ------------------------------------------------------------------

type fakeAPIError struct {
	code    string
	message string
}

func (e *fakeAPIError) Error() string                 { return e.code + ": " + e.message }
func (e *fakeAPIError) ErrorCode() string             { return e.code }
func (e *fakeAPIError) ErrorMessage() string          { return e.message }
func (e *fakeAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

var _ smithy.APIError = (*fakeAPIError)(nil)

func TestIsExpiredCredentials_APIErrorCodes(t *testing.T) {
	for _, code := range []string{"ExpiredToken", "ExpiredTokenException", "TokenRefreshRequired"} {
		err := &fakeAPIError{code: code, message: "stale"}
		if !isExpiredCredentials(err) {
			t.Errorf("code %q: expected expired-credentials match", code)
		}
	}
}

func TestIsExpiredCredentials_LocalSSOMessages(t *testing.T) {
	for _, msg := range []string{
		"sso session has expired or is invalid",
		"cached SSO token is expired, or not present",
		"the security token included in the request is expired",
	} {
		if !isExpiredCredentials(errors.New(msg)) {
			t.Errorf("substring %q: expected expired-credentials match", msg)
		}
	}
}

func TestIsExpiredCredentials_DoesNotFalsePositive(t *testing.T) {
	for _, msg := range []string{
		"some unrelated error",
		"connection refused",
		"InvalidClientTokenId: The security token included in the request is invalid", // not expired, just wrong
	} {
		if isExpiredCredentials(errors.New(msg)) {
			t.Errorf("substring %q: should NOT match expired-credentials", msg)
		}
	}
}

func TestClassifyAuthErr_WrapsExpiredWithHelpText(t *testing.T) {
	in := &fakeAPIError{code: "ExpiredTokenException", message: "stale"}
	out := classifyAuthErr(in)
	if !errors.Is(out, ErrExpiredCredentials) {
		t.Fatal("expected wrapped ErrExpiredCredentials")
	}
	msg := out.Error()
	for _, want := range []string{
		"aws sso login",
		"aws configure",
		"isengardcli assume",
		"ada credentials update",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("help text missing %q in: %s", want, msg)
		}
	}
}

func TestClassifyAuthErr_PassesThroughOtherErrors(t *testing.T) {
	in := errors.New("network unreachable")
	out := classifyAuthErr(in)
	if errors.Is(out, ErrExpiredCredentials) {
		t.Error("non-expired error should not be classified as expired")
	}
	if out != in {
		t.Errorf("got %v, want exact passthrough %v", out, in)
	}
}

func TestClassifyAuthErr_NilPassesThrough(t *testing.T) {
	if classifyAuthErr(nil) != nil {
		t.Error("nil input must produce nil output")
	}
}

// ------------------------------------------------------------------
// Diagnose with stub clients
// ------------------------------------------------------------------

type stubSTS struct {
	out *sts.GetCallerIdentityOutput
	err error
}

func (s *stubSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput,
	_ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return s.out, s.err
}

type stubBedrock struct {
	out *bedrock.ListInferenceProfilesOutput
	err error
}

func (b *stubBedrock) ListInferenceProfiles(_ context.Context, _ *bedrock.ListInferenceProfilesInput,
	_ ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error) {
	return b.out, b.err
}

func TestDiagnose_Success(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			return aws.Credentials{
				AccessKeyID:     "AKIA",
				SecretAccessKey: "SECRET",
				Source:          "test-static",
			}, nil
		}),
	}
	stubS := &stubSTS{out: &sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"),
		Arn:     aws.String("arn:aws:iam::123456789012:user/test"),
		UserId:  aws.String("AIDA-TEST"),
	}}
	stubB := &stubBedrock{out: &bedrock.ListInferenceProfilesOutput{
		InferenceProfileSummaries: []bedrocktypes.InferenceProfileSummary{
			{
				InferenceProfileId:  aws.String("global.anthropic.claude-sonnet-4-5-20250929-v1:0"),
				InferenceProfileArn: aws.String("arn:aws:bedrock:us-east-1::inference-profile/sonnet-4-5"),
			},
			{
				InferenceProfileId: aws.String("us.amazon.nova-pro-v1:0"),
			},
		},
	}}

	got := diagnoseWith(context.Background(), cfg, "myprofile", stubS, stubB)
	if len(got.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", got.Errors)
	}
	if got.Region != "us-east-1" {
		t.Errorf("region = %q", got.Region)
	}
	if got.Profile != "myprofile" {
		t.Errorf("profile = %q", got.Profile)
	}
	if got.CredentialSource != "test-static" {
		t.Errorf("credential source = %q", got.CredentialSource)
	}
	if got.Identity == nil || got.Identity.Account != "123456789012" {
		t.Errorf("identity = %+v", got.Identity)
	}
	if got.Bedrock == nil || !got.Bedrock.HasModelMatch {
		t.Errorf("bedrock report missing or no model match: %+v", got.Bedrock)
	}
	if got.Bedrock.InferenceProfileCount != 2 {
		t.Errorf("inference profile count = %d, want 2", got.Bedrock.InferenceProfileCount)
	}
}

func TestDiagnose_ExpiredCredentialsClassified(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			return aws.Credentials{}, &fakeAPIError{code: "ExpiredToken", message: "stale"}
		}),
	}
	stubS := &stubSTS{err: &fakeAPIError{code: "ExpiredToken", message: "stale"}}
	stubB := &stubBedrock{err: &fakeAPIError{code: "ExpiredToken", message: "stale"}}

	got := diagnoseWith(context.Background(), cfg, "", stubS, stubB)
	if !HasExpiredCredentialsError(got) {
		t.Fatalf("expected HasExpiredCredentialsError; got errors %v", got.Errors)
	}
	if len(got.Errors) < 3 {
		// Verifies "all checks ran even though they all failed" — a single
		// expired-creds failure mustn't short-circuit the report.
		t.Errorf("expected >=3 errors (creds + sts + bedrock), got %d: %v", len(got.Errors), got.Errors)
	}
}

func TestDiagnose_PartialFailure_BedrockOnly(t *testing.T) {
	cfg := aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			return aws.Credentials{Source: "test"}, nil
		}),
	}
	stubS := &stubSTS{out: &sts.GetCallerIdentityOutput{Account: aws.String("000")}}
	stubB := &stubBedrock{err: errors.New("AccessDeniedException")}

	got := diagnoseWith(context.Background(), cfg, "", stubS, stubB)
	if got.Identity == nil {
		t.Error("identity should still resolve when only Bedrock fails")
	}
	if len(got.Errors) != 1 {
		t.Errorf("expected exactly 1 error (Bedrock), got %d: %v", len(got.Errors), got.Errors)
	}
}

func TestCheckBedrock_NoMatchingModel(t *testing.T) {
	stubB := &stubBedrock{out: &bedrock.ListInferenceProfilesOutput{
		InferenceProfileSummaries: []bedrocktypes.InferenceProfileSummary{
			{InferenceProfileId: aws.String("us.amazon.nova-pro-v1:0")},
		},
	}}
	rep, err := checkBedrock(context.Background(), stubB, DefaultBedrockModelPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if rep.HasModelMatch {
		t.Error("expected no model match when prefix is absent")
	}
	if rep.InferenceProfileCount != 1 {
		t.Errorf("count = %d, want 1", rep.InferenceProfileCount)
	}
}

// ------------------------------------------------------------------
// firstNonEmpty
// ------------------------------------------------------------------

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "b", "c"}, "b"},
		{[]string{"a"}, "a"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("firstNonEmpty(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
