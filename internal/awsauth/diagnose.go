package awsauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// DefaultBedrockModelPrefix is the inference profile prefix the Python
// validator and the Go validator both target. Phase 1D.4 checks that at
// least one inference profile in the active region matches this prefix.
const DefaultBedrockModelPrefix = "global.anthropic.claude-sonnet-4-5"

// DiagnosticReport is the structured result of `thor doctor`. Fields are
// optional — the report is rendered for humans, so missing pieces just
// surface as blanks. Errors collected per-check rather than aborted on
// the first failure so the operator gets the full picture.
type DiagnosticReport struct {
	Region           string
	Profile          string
	CredentialSource string

	Identity *CallerIdentity
	Bedrock  *BedrockReport

	Errors []error
}

// CallerIdentity mirrors sts:GetCallerIdentity.
type CallerIdentity struct {
	Account string
	ARN     string
	UserID  string
}

// BedrockReport summarises the model-access check.
type BedrockReport struct {
	InferenceProfileCount int
	HasModelMatch         bool
	MatchedModelArn       string
}

// stsClient and bedrockClient are the minimal interfaces consumed by
// Diagnose. They make the function trivially mockable from tests.
type stsClient interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput,
		opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}
type bedrockClient interface {
	ListInferenceProfiles(ctx context.Context, in *bedrock.ListInferenceProfilesInput,
		opts ...func(*bedrock.Options)) (*bedrock.ListInferenceProfilesOutput, error)
}

// Diagnose runs the `thor doctor` checks against the live AWS APIs:
//
//  1. sts:GetCallerIdentity — confirms the credential chain produces a
//     usable caller and returns the account/ARN.
//  2. bedrock:ListInferenceProfiles — confirms Bedrock is reachable in the
//     region and surfaces whether DefaultBedrockModelPrefix is available.
//
// Any check that errors is appended to the returned report.Errors slice;
// other checks still run. Expired-credential errors are classified through
// ErrExpiredCredentials so callers can detect them with errors.Is.
func Diagnose(ctx context.Context, cfg aws.Config, profile string) DiagnosticReport {
	return diagnoseWith(ctx, cfg, profile,
		sts.NewFromConfig(cfg),
		bedrock.NewFromConfig(cfg),
	)
}

func diagnoseWith(ctx context.Context, cfg aws.Config, profile string,
	stsCli stsClient, bedrockCli bedrockClient,
) DiagnosticReport {
	rep := DiagnosticReport{
		Region:  cfg.Region,
		Profile: firstNonEmpty(profile, os.Getenv("AWS_PROFILE")),
	}

	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, fmt.Errorf("retrieve credentials: %w", classifyAuthErr(err)))
	} else {
		rep.CredentialSource = creds.Source
	}

	if id, err := stsCli.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		rep.Errors = append(rep.Errors, fmt.Errorf("sts:GetCallerIdentity: %w", classifyAuthErr(err)))
	} else {
		rep.Identity = &CallerIdentity{
			Account: aws.ToString(id.Account),
			ARN:     aws.ToString(id.Arn),
			UserID:  aws.ToString(id.UserId),
		}
	}

	if br, err := checkBedrock(ctx, bedrockCli, DefaultBedrockModelPrefix); err != nil {
		rep.Errors = append(rep.Errors, fmt.Errorf("bedrock:ListInferenceProfiles: %w", classifyAuthErr(err)))
	} else {
		rep.Bedrock = br
	}
	return rep
}

func checkBedrock(ctx context.Context, cli bedrockClient, prefix string) (*BedrockReport, error) {
	out, err := cli.ListInferenceProfiles(ctx, &bedrock.ListInferenceProfilesInput{})
	if err != nil {
		return nil, err
	}
	rep := &BedrockReport{InferenceProfileCount: len(out.InferenceProfileSummaries)}
	for _, p := range out.InferenceProfileSummaries {
		id := aws.ToString(p.InferenceProfileId)
		if strings.HasPrefix(id, prefix) {
			rep.HasModelMatch = true
			rep.MatchedModelArn = aws.ToString(p.InferenceProfileArn)
			break
		}
	}
	return rep, nil
}

// HasExpiredCredentialsError returns true if any of report.Errors is an
// ErrExpiredCredentials. Convenience for callers (CLI doctor command,
// MCP handler) that need a one-line check.
func HasExpiredCredentialsError(report DiagnosticReport) bool {
	for _, err := range report.Errors {
		if errors.Is(err, ErrExpiredCredentials) {
			return true
		}
	}
	return false
}
