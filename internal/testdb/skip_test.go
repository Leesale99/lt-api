package testdb

// Unit tests for the missing-DSN policy. They never connect to a database —
// that is the point of DecideSkip living here: internal/testdb's own suite runs
// on a machine with no Postgres at all, so the rule that decides whether the
// DB-backed suites may be green is itself provable there.
//
// The semantics pinned below are the audit's honesty contract:
//
//   - no DSN and no opt-out → FAIL with actionable instructions (not a skip);
//   - no DSN but AUDIT_SKIP_DB=1 → SKIP, loudly, and the run may still pass;
//   - DSN set (set-but-unreachable) → run: Setup owns that failure.

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// The compose database is published on host port 5433 (5432 belongs to a
// host-native PostgreSQL on developer machines), so the instructions must not
// send an operator to 5432 — that server knows nothing about lt_test.
func TestDefaultTestDSNPinsTheComposeHostPort(t *testing.T) {
	if !strings.Contains(DefaultTestDSN, "@localhost:5433/") {
		t.Errorf("DefaultTestDSN = %q, want the compose database on host port 5433", DefaultTestDSN)
	}
}

func TestDecideSkipRunsWhenDSNIsSet(t *testing.T) {
	decision := DecideSkip(DefaultTestDSN, "")
	if decision.Action != ActionRun {
		t.Fatalf("Action = %v, want %v (a set DSN always runs; Setup fails hard if unreachable)", decision.Action, ActionRun)
	}
	if decision.Message != "" {
		t.Errorf("Message = %q, want empty: the runner prints nothing when it runs", decision.Message)
	}
}

// The DSN being set must never be a skip, whatever the opt-out says: the opt-out
// is for DB-less machines, not for making a real run quiet.
func TestDecideSkipDSNWinsOverOptOut(t *testing.T) {
	decision := DecideSkip(DefaultTestDSN, "1")
	if decision.Action != ActionRun {
		t.Fatalf("Action = %v with both DSN and AUDIT_SKIP_DB=1, want %v", decision.Action, ActionRun)
	}
}

func TestDecideSkipFailsWithoutDSNAndOptionOut(t *testing.T) {
	for _, optOut := range []string{"", "0", "true", "yes", " 1"} {
		t.Run("AUDIT_SKIP_DB="+optOut, func(t *testing.T) {
			decision := DecideSkip("", optOut)
			if decision.Action != ActionFail {
				t.Fatalf("Action = %v for AUDIT_SKIP_DB=%q, want %v (only the literal 1 is an opt-out)", decision.Action, optOut, ActionFail)
			}
			for _, want := range []string{
				"SPEC SUITE DID NOT RUN",
				"docker compose up -d",
				"make db/up",
				"export LT_API_TEST_DSN='" + DefaultTestDSN + "'",
				"AUDIT_SKIP_DB=1",
			} {
				if !strings.Contains(decision.Message, want) {
					t.Errorf("failure message does not mention %q:\n%s", want, decision.Message)
				}
			}
		})
	}
}

// Whitespace is not a DSN: a blank value is unset, and unsets fail loudly.
func TestDecideSkipTreatsBlankDSNAsUnset(t *testing.T) {
	decision := DecideSkip("   ", "")
	if decision.Action != ActionFail {
		t.Fatalf("Action = %v for a blank DSN, want %v", decision.Action, ActionFail)
	}
}

func TestDecideSkipSkipsLoudlyWithExplicitOptOut(t *testing.T) {
	decision := DecideSkip("", "1")
	if decision.Action != ActionSkip {
		t.Fatalf("Action = %v with AUDIT_SKIP_DB=1, want %v", decision.Action, ActionSkip)
	}
	if want := "SPEC SUITE SKIPPED: no LT_API_TEST_DSN and AUDIT_SKIP_DB=1 — the database-backed scenarios did NOT run"; !strings.Contains(decision.Message, want) {
		t.Errorf("skip banner = %q, want it to contain %q", decision.Message, want)
	}
	// A skip must not offer the failure's instructions: it is a decision, not a
	// mistake.
	if strings.Contains(decision.Message, "SPEC SUITE DID NOT RUN") {
		t.Errorf("skip banner carries the failure text:\n%s", decision.Message)
	}
}

// The banner has to land in every stream the caller captures; whether anyone
// sees it is `go test`'s buffering (Announce's doc comment) and the terminal's
// job. Both captured streams are pinned because a non-tty non-verbose run keeps
// only one of them.
func TestAnnounceWritesTheMessageToEveryStream(t *testing.T) {
	var stdout, stderr bytes.Buffer
	announce(SkipBanner, &stdout, &stderr)

	for name, got := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String()} {
		if n := strings.Count(got, "SPEC SUITE SKIPPED:"); n != 1 {
			t.Errorf("%s carries the banner %d times, want 1:\n%s", name, n, got)
		}
	}
}

// Announce itself must reach both of the package's own captured streams, not
// just the one it is printed on first.
func TestAnnounceWritesStdoutAndStderr(t *testing.T) {
	stdout, stderr := captureStdio(t, func() { Announce(SkipBanner) })

	for name, got := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if !strings.Contains(got, "SPEC SUITE SKIPPED:") {
			t.Errorf("Announce did not write the banner to %s:\n%s", name, got)
		}
	}
}

// captureStdio redirects os.Stdout/os.Stderr for the duration of fn and returns
// what each captured.
func captureStdio(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stderr pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	_ = outW.Close()
	_ = errW.Close()
	outBytes, err := io.ReadAll(outR)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	errBytes, err := io.ReadAll(errR)
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	return string(outBytes), string(errBytes)
}

func TestActionString(t *testing.T) {
	for action, want := range map[Action]string{
		ActionRun:  "run",
		ActionSkip: "skip",
		ActionFail: "fail",
	} {
		if got := action.String(); got != want {
			t.Errorf("Action(%d).String() = %q, want %q", int(action), got, want)
		}
	}
}
