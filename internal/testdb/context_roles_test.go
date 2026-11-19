package testdb

// Context-role probe: R-PLATFORM-015 — the pool a context is given runs every
// statement as that context's role, bound together with the search path that
// resolves its own unqualified table names.
//
// Placement: here, not internal/e2e. ContextPool is the deliberate structural
// copy of cmd/api's openContextPool hook (SET ROLE + SET search_path as an
// AfterConnect pair), and this package's TestMain already applies every
// migration and holds the pools, so the probe pins the harness contract the
// store suites rely on — the same contract a sabotage of the AfterConnect
// hook would break — with no extra database and no extra boot cost.
//
// It asserts, per context pool:
//   - current_user IS the context role (every statement runs as it);
//   - session_user is NOT that role but the DSN role (the switch happened at
//     session level, the login is the owner's — the pool never runs as the
//     DSN role);
//   - the search path is exactly the context's own schema followed by public,
//     so unqualified SQL resolves to its own tables and never to another
//     context's;
// and, once, that the Owner pool is the DSN role — unrestricted, never
// role-switched (fixtures and constraint probes need that).
//
// Same skip-guard as boundary_test.go: the TestMain above leaves boundaryDB nil
// only under the explicit AUDIT_SKIP_DB=1 opt-out; a set-but-unusable DSN has
// already failed there.

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dsnRole returns the role the test DSN logs in as: the session_user of every
// pool, and the current_user of the Owner pool.
func dsnRole(t *testing.T) string {
	t.Helper()

	u, err := url.Parse(os.Getenv("LT_API_TEST_DSN"))
	if err != nil {
		t.Fatalf("parse LT_API_TEST_DSN: %v", err)
	}
	if u.User == nil || u.User.Username() == "" {
		t.Fatalf("LT_API_TEST_DSN carries no role: %q", os.Getenv("LT_API_TEST_DSN"))
	}
	return u.User.Username()
}

// searchPathList reads a pool's search_path and returns it as a trimmed list.
func searchPathList(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()

	var raw string
	if err := pool.QueryRow(context.Background(), "SHOW search_path").Scan(&raw); err != nil {
		t.Fatalf("SHOW search_path: %v", err)
	}

	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = strings.Trim(strings.TrimSpace(parts[i]), `"`)
	}
	return parts
}

// TestContextPoolsRunAsContextRole: each context pool speaks as its own role
// with its own schema on the search path, while the Owner pool speaks as the
// DSN role. Fails if the SET ROLE half of the hook is dropped (current_user
// becomes the DSN role), if SET search_path is dropped or reordered (another
// context's tables would become resolvable unqualified), or if the pools are
// mixed up at Setup time (a context handed another context's pool).
func TestContextPoolsRunAsContextRole(t *testing.T) {
	requireDB(t)
	owner := dsnRole(t)

	pools := []struct {
		name   string
		pool   *pgxpool.Pool
		role   string
		schema string
	}{
		{"schedule", boundaryDB.Schedule, RoleSchedule, "schedule"},
		{"game", boundaryDB.Game, RoleGame, "game"},
		{"identity", boundaryDB.Identity, RoleIdentity, "identity"},
	}

	for _, tc := range pools {
		t.Run(tc.name, func(t *testing.T) {
			var currentUser, sessionUser string
			err := tc.pool.QueryRow(context.Background(), "SELECT current_user, session_user").
				Scan(&currentUser, &sessionUser)
			if err != nil {
				t.Fatalf("read current_user/session_user: %v", err)
			}

			if currentUser != tc.role {
				t.Fatalf("pool runs as current_user %q, want the context role %q", currentUser, tc.role)
			}
			if sessionUser == tc.role {
				t.Fatalf("session_user is %q — the pool logged in as the context role; the role must be assumed by SET ROLE on a %q session", sessionUser, owner)
			}
			if sessionUser != owner {
				t.Fatalf("session_user is %q, want the DSN role %q", sessionUser, owner)
			}

			got := searchPathList(t, tc.pool)
			want := []string{tc.schema, "public"}
			if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("search_path is %v, want %v — unqualified names must resolve to the context's own schema", got, want)
			}
		})
	}

	t.Run("owner", func(t *testing.T) {
		var currentUser, sessionUser string
		err := boundaryDB.Owner.QueryRow(context.Background(), "SELECT current_user, session_user").
			Scan(&currentUser, &sessionUser)
		if err != nil {
			t.Fatalf("read current_user/session_user: %v", err)
		}

		for _, user := range []struct{ label, got string }{
			{"current_user", currentUser},
			{"session_user", sessionUser},
		} {
			if user.got != owner {
				t.Fatalf("Owner pool %s is %q, want the DSN role %q (never role-switched)", user.label, user.got, owner)
			}
		}
	})
}
