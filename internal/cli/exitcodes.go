// Package cli builds the cobra command tree for `thor`. The cmd/thor
// entrypoint is a 5-line wrapper that calls Execute().
//
// Tests live alongside the command files; an entire run can be exercised
// by setting the cobra args and reading the writer attached to the
// command (cmd.SetOut/cmd.SetArgs).
package cli

import "errors"

// Exit codes are documented in PLAN.md 2.9.
//
//	0 — success
//	1 — generic error
//	2 — invalid usage (cobra parses arg/flag errors)
//	3 — expired AWS credentials
//	4 — partial failure (some controls ERRORED while others succeeded;
//	    distinct from "validation produced FAIL verdicts" which is exit 0)
const (
	ExitOK             = 0
	ExitGenericError   = 1
	ExitUsageError     = 2
	ExitExpiredCreds   = 3
	ExitPartialFailure = 4
)

// errExitCode wraps an exit-code-only error so RunE handlers can return
// `errExitCode{code: 3}` and the root command translates it to os.Exit.
type errExitCode struct {
	code int
	err  error
}

func (e *errExitCode) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return "thor: exiting with code " + itoa(e.code)
}

func (e *errExitCode) Unwrap() error { return e.err }

// ExitCodeForError returns the exit code that best classifies err. Used by
// the cmd/thor entrypoint to translate a RunE error into os.Exit(N).
//
//   - errExitCode  — uses its embedded code
//   - awsauth.ErrExpiredCredentials — 3
//   - cobra parsing/usage errors    — handled in main.go via FlagError
//   - anything else                 — 1
func ExitCodeForError(err error) int {
	if err == nil {
		return ExitOK
	}
	var ec *errExitCode
	if errors.As(err, &ec) {
		return ec.code
	}
	return ExitGenericError
}

// itoa avoids pulling strconv into this small helper file.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
