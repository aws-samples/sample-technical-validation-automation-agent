package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"thor-golang/internal/awsauth"
	"thor-golang/internal/version"
)

// globalFlags holds the values surfaced on every subcommand. Mirrors the
// `--region`, `--profile`, `--verbose`, `--json` set documented in
// PLAN.md 2.8 plus the `THOR_*` / `AWS_*` env-var fallbacks.
type globalFlags struct {
	Region  string
	Profile string
	Verbose bool
	JSON    bool
}

// global is package-scoped so subcommand RunE handlers can read it
// without threading a context object through every call. Cobra
// already serialises Execute() through a single goroutine.
var global globalFlags

// NewRootCommand returns a fully-wired `thor` command tree. Tests build
// a tree per invocation so they can inject custom Bedrock/HTTP clients.
func NewRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "thor",
		Short: "AWS PSA Validator (Go port)",
		Long: `Thor validates partner Self-Assessment evidence against AWS controls
using Anthropic Claude via Amazon Bedrock.

The Python implementation at server/thor/ remains the reference until
parity ships. See PLAN.md for the migration roadmap.`,
		Version:           version.String(),
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}

	// --version short-circuits the help/usage output.
	cmd.SetVersionTemplate(version.String() + "\n")

	pf := cmd.PersistentFlags()
	pf.StringVar(&global.Region, "region", envOr("AWS_REGION", "AWS_DEFAULT_REGION"),
		"AWS region for Bedrock (env: AWS_REGION, AWS_DEFAULT_REGION)")
	pf.StringVar(&global.Profile, "profile", os.Getenv("AWS_PROFILE"),
		"AWS named profile (env: AWS_PROFILE)")
	pf.BoolVarP(&global.Verbose, "verbose", "v", boolEnv("THOR_VERBOSE"),
		"verbose log output (env: THOR_VERBOSE)")
	pf.BoolVar(&global.JSON, "json", boolEnv("THOR_JSON"),
		"emit machine-readable JSON instead of human-formatted text where applicable (env: THOR_JSON)")

	cmd.AddCommand(
		newDoctorCommand(),
		newConvertCommand(),
		newValidateCommand(),
		newDiffCommand(),
		newPrepCommand(),
		newMapCommand(),
		newListControlsCommand(),
		newExportCommand(),
	)

	return cmd
}

// Execute parses os.Args, runs the matched subcommand, and returns the
// numeric exit code. The caller (cmd/thor/main.go) is responsible for
// the actual os.Exit.
func Execute(args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(os.Stdin)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}

	// Cobra wraps unknown-flag / wrong-arg-count failures in a
	// FlagError-shaped error; classify those as usage errors.
	if isUsageError(err) {
		fmt.Fprintln(stderr, err)
		return ExitUsageError
	}

	if errors.Is(err, awsauth.ErrExpiredCredentials) {
		fmt.Fprintln(stderr, err)
		return ExitExpiredCreds
	}

	fmt.Fprintln(stderr, err)
	return ExitCodeForError(err)
}

// isUsageError detects cobra's flag/arg parsing failures so the caller
// can map them to ExitUsageError (2). Cobra doesn't expose a typed error
// for these; we match on the canonical message prefixes.
func isUsageError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, prefix := range []string{
		"unknown command",
		"unknown flag",
		"unknown shorthand flag",
		"flag needs an argument",
		"invalid argument",
		"requires at least",
		"accepts ",
	} {
		if strings.Contains(msg, prefix) {
			return true
		}
	}
	return false
}

// envOr returns the first non-empty value among the given env-var names.
func envOr(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

// boolEnv parses a THOR_* boolean env var. Empty/0/false → false; anything
// else (including "1", "true", "yes") → true.
func boolEnv(name string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	switch v {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
