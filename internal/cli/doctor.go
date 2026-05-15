package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"thor-golang/internal/awsauth"
	"thor-golang/internal/prompts"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose AWS credentials, Bedrock access, and embedded prompts",
		Long: `Runs three checks:

  1. AWS credentials resolve and STS GetCallerIdentity succeeds.
  2. The active region exposes the global.anthropic.claude-sonnet-4-5
     inference profile.
  3. The system prompts and CONTEXT.csv embedded in this binary load.

Exits 0 on success, 3 on expired credentials, 1 on any other failure.`,
		RunE: runDoctor,
	}
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Embedded prompts — surface their absence loudly. This indicates a
	// build problem, not a runtime issue.
	embedReport := checkEmbeddedPrompts()

	cfg, err := awsauth.LoadConfig(ctx, global.Region, global.Profile)
	if err != nil {
		// LoadConfig already classifies expired creds; bubble the error
		// through so root.Execute can map it to exit 3.
		return err
	}
	report := awsauth.Diagnose(ctx, cfg, global.Profile)

	if global.JSON {
		return emitDoctorJSON(cmd, embedReport, report)
	}

	emitDoctorText(cmd, embedReport, report)

	for _, e := range report.Errors {
		if errors.Is(e, awsauth.ErrExpiredCredentials) {
			return e
		}
	}
	if !embedReport.OK {
		return &errExitCode{code: ExitGenericError, err: errors.New("doctor: embedded prompts missing or unreadable")}
	}
	if len(report.Errors) > 0 {
		return &errExitCode{code: ExitGenericError, err: errors.New("doctor: one or more checks failed")}
	}
	return nil
}

type embedReport struct {
	OK          bool
	HasOld      bool
	HasNew      bool
	HasRevised  bool
	HasContext  bool
	ContextRows int
}

func checkEmbeddedPrompts() embedReport {
	r := embedReport{}
	if b, err := prompts.System(prompts.SystemOld); err == nil && len(b) > 0 {
		r.HasOld = true
	}
	if b, err := prompts.System(prompts.SystemNew); err == nil && len(b) > 0 {
		r.HasNew = true
	}
	if b, err := prompts.System(prompts.SystemRevised); err == nil && len(b) > 0 {
		r.HasRevised = true
	}
	if b, err := prompts.ContextCSV(); err == nil && len(b) > 0 {
		r.HasContext = true
		// Approximate row count — useful diagnostic, doesn't warrant a CSV parse.
		for _, c := range b {
			if c == '\n' {
				r.ContextRows++
			}
		}
	}
	r.OK = r.HasOld && r.HasNew && r.HasRevised && r.HasContext
	return r
}

func emitDoctorText(cmd *cobra.Command, e embedReport, r awsauth.DiagnosticReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "thor doctor")
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Embedded prompts:")
	fmt.Fprintf(out, "  %s system_old.txt\n", check(e.HasOld))
	fmt.Fprintf(out, "  %s system_new.txt\n", check(e.HasNew))
	fmt.Fprintf(out, "  %s system_revised.txt\n", check(e.HasRevised))
	fmt.Fprintf(out, "  %s CONTEXT.csv (~%d rows)\n", check(e.HasContext), e.ContextRows)
	fmt.Fprintln(out)

	fmt.Fprintln(out, "AWS configuration:")
	fmt.Fprintf(out, "  region:            %s\n", nonEmpty(r.Region))
	fmt.Fprintf(out, "  profile:           %s\n", nonEmpty(r.Profile))
	fmt.Fprintf(out, "  credential source: %s\n", nonEmpty(r.CredentialSource))
	if r.Identity != nil {
		fmt.Fprintf(out, "  account:           %s\n", r.Identity.Account)
		fmt.Fprintf(out, "  arn:               %s\n", r.Identity.ARN)
	}
	fmt.Fprintln(out)

	fmt.Fprintln(out, "Bedrock:")
	if r.Bedrock != nil {
		fmt.Fprintf(out, "  inference profiles: %d\n", r.Bedrock.InferenceProfileCount)
		fmt.Fprintf(out, "  %s model %s\n", check(r.Bedrock.HasModelMatch), awsauth.DefaultBedrockModelPrefix)
	}
	fmt.Fprintln(out)

	if len(r.Errors) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "Errors:")
		for _, e := range r.Errors {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %s\n", e)
		}
	}
}

type doctorJSONReport struct {
	Embedded embedReport              `json:"embeddedPrompts"`
	AWS      awsauth.DiagnosticReport `json:"aws"`
	Errors   []string                 `json:"errors,omitempty"`
}

func emitDoctorJSON(cmd *cobra.Command, e embedReport, r awsauth.DiagnosticReport) error {
	errs := make([]string, 0, len(r.Errors))
	for _, x := range r.Errors {
		errs = append(errs, x.Error())
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(doctorJSONReport{Embedded: e, AWS: r, Errors: errs})
}

func check(ok bool) string {
	if ok {
		return "[ok]"
	}
	return "[--]"
}

func nonEmpty(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}
