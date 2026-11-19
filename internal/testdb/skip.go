package testdb

// The missing-DSN policy for the database-backed suites, as a pure decision.
//
// Every suite needs to answer one question before it touches Postgres: the DSN
// is not set — is that a skip or a failure? Answering it inline (in a TestMain,
// behind an os.Getenv) makes the answer untestable on the DB-less machine where
// it matters most, and it is the rule the audit gate rests on: a suite that
// quietly tests nothing is not evidence. DecideSkip is that rule, DB-free.
//
// The contract a suite implements with it:
//
//   - ActionRun → LT_API_TEST_DSN is set; call Setup. An unreachable server is
//     Setup's hard failure, unchanged (a DSN typo must never read as a skip).
//   - ActionSkip → the operator opted out with AUDIT_SKIP_DB=1. Print the banner
//     and run whatever needs no database; the run may be green, but it must say
//     out loud that the database-backed scenarios did NOT run.
//   - ActionFail → neither: print the instructions and exit non-zero. A green
//     `go test ./...` that skipped the enforcement half is the failure mode this
//     policy exists to remove.

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// SkipOptOutEnv is the environment variable that turns the missing-DSN failure
// into an explicit, loud skip. Only the exact value "1" opts out: a typo must
// fail loudly rather than silently disable the suite.
const SkipOptOutEnv = "AUDIT_SKIP_DB"

// DSNEnv is the environment variable every database-backed suite reads for the
// throwaway test database.
const DSNEnv = "LT_API_TEST_DSN"

// DefaultTestDSN is the DSN of the compose database (compose.yaml), which
// mirrors CI's Postgres service container. It is what the instructions below
// tell an operator to export.
const DefaultTestDSN = "postgres://lt_test:test@localhost:5432/lt_api_test?sslmode=disable"

// Action is what a database-backed suite should do about the environment it
// was started in.
type Action int

const (
	// ActionRun means the DSN is set: build the throwaway database and run.
	ActionRun Action = iota
	// ActionSkip means the DSN is unset and the operator opted out explicitly.
	ActionSkip
	// ActionFail means the DSN is unset and nothing opted out: refuse to be
	// green while testing nothing.
	ActionFail
)

// String names the action for test failures and log lines.
func (a Action) String() string {
	switch a {
	case ActionRun:
		return "run"
	case ActionSkip:
		return "skip"
	case ActionFail:
		return "fail"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// Decision is the answer plus the banner the caller must print: Message is
// non-empty for ActionSkip (print it on stdout) and ActionFail (stderr).
type Decision struct {
	Action  Action
	Message string
}

// DecideSkip reports what a database-backed suite should do, given the values
// of LT_API_TEST_DSN and AUDIT_SKIP_DB. It reads nothing and connects to
// nothing, so the policy is testable without PostgreSQL.
//
//	dsn            auditSkipDB   Action   outcome
//	""             ""            Fail     exit 1, with the instructions above
//	""             "1"           Skip     exit 0, loud banner: nothing ran
//	non-empty      anything      Run      Setup decides (unreachable is fatal)
func DecideSkip(dsn, auditSkipDB string) Decision {
	switch {
	case strings.TrimSpace(dsn) != "":
		return Decision{Action: ActionRun}
	case auditSkipDB == "1":
		return Decision{Action: ActionSkip, Message: SkipBanner}
	default:
		return Decision{Action: ActionFail, Message: MissingDSNHelp}
	}
}

// SkipBanner is the loud notice an AUDIT_SKIP_DB=1 run prints. It is not a
// warning about a flaky dependency: it says the enforcement half did not happen.
const SkipBanner = "SPEC SUITE SKIPPED: no LT_API_TEST_DSN and AUDIT_SKIP_DB=1 — the database-backed scenarios did NOT run.\n" +
	"This run says nothing about the scenario suite; bring the database up with `make db/up` for the real gate."

// MissingDSNHelp is the failure banner: what went wrong, why it is fatal, and
// the two ways out (run the database, or opt out on purpose).
const MissingDSNHelp = "SPEC SUITE DID NOT RUN: LT_API_TEST_DSN is unset and AUDIT_SKIP_DB is not 1.\n" +
	"The database-backed scenarios are the audit's enforcement half, so a green run without them is not evidence.\n" +
	"\n" +
	"Bring the throwaway test database up (it mirrors CI's Postgres service container):\n" +
	"\n" +
	"    docker compose up -d          # or: make db/up  (waits until it is healthy)\n" +
	"    export LT_API_TEST_DSN='" + DefaultTestDSN + "'\n" +
	"\n" +
	"To opt out on a deliberately DB-less machine, say so out loud:\n" +
	"\n" +
	"    AUDIT_SKIP_DB=1 go test ./...\n"

// Announce prints a decision's message where a *passing* run can still be seen.
//
// Printing is not enough: `go test ./...` captures a package's output and
// discards all of it when the package passes (only -v or a failure shows it),
// so a banner written to stdout or stderr inside a green run is invisible —
// exactly the silent skip this policy exists to remove. Announce therefore
// writes to both the captured stream (tests, -v runs, failure dumps) and the
// controlling terminal, when there is one (a developer's `make audit`); with no
// terminal (CI, pipes) the captured copy is all that is possible.
func Announce(message string) {
	announce(os.Stdout, message)
}

// announce is Announce's testable core: the given stream plus /dev/tty.
func announce(primary io.Writer, message string) {
	fmt.Fprintln(primary, message)
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return // no terminal (CI, nohup): the captured stream is all there is
	}
	defer func() { _ = tty.Close() }()
	fmt.Fprintln(tty, message)
}
