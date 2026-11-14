// Package arch pins the bounded-context dependency map (Phase 05, as built)
// in code: it walks every Go file under internal/ and cmd/, extracts the
// package-level import edges, and fails with a readable diff when an edge
// outside the matrix appears.
//
// The matrix mirrors docs/designs/bounded-contexts-dependency-map.mmd with the
// Phase 05 refinements:
//
//   - schedule is the ACL: it owns teams/seasons/rounds/matches, mints their
//     ids, and may import only the shared kernel (shared/persistence,
//     validator) and the event bus it publishes on. It must NEVER import
//     game, ledger, identity or api — the downstream side of the seam.
//   - game owns rides/players and consumes schedule via the SchedulePort and
//     ledger via the LedgerPort interfaces; it may also touch the shared
//     kernel (shared/persistence, validator) and idempotency (its claim
//     table). It must never import identity or api.
//   - ledger and eventbus are thin: no domain imports (shared/persistence
//     only for ledger).
//   - identity is a generic supporting context: shared/persistence,
//     validator only.
//   - api is the gateway, not a context: it may import any context.
//   - cmd/* is the composition root: wires everything, imports anything.
//
// Test-file policy: _test.go files are checked against the same matrix — a
// test importing across a forbidden edge is a real coupling, not a fiction.
// One documented exemption: internal/testdb (the DB test harness, providing
// pools/roles/seeds) may be imported from any _test.go file; it is to Go
// tests what the testing package is — infrastructure, not a context.
package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// modulePath is read from go.mod at the repo root so the walk can classify
// intra-module imports.
var modulePath = mustModulePath()

// allowedImports maps an internal package (relative to internal/) to the set
// of internal packages it may import. Anything not listed is forbidden.
// Packages absent from the map default to: no internal imports.
var allowedImports = map[string]map[string]bool{
	"schedule": {"eventbus": true, "shared/persistence": true, "validator": true},
	"game": {
		"schedule":           true, // SchedulePort provider (resolve, ids, events)
		"ledger":             true, // LedgerPort provider (token intents)
		"idempotency":        true, // its own claim table
		"eventbus":           true,
		"shared/persistence": true,
		"validator":          true,
	},
	"identity":           {"shared/persistence": true, "validator": true},
	"ledger":             {"shared/persistence": true},
	"eventbus":           {}, // pure in-proc pub/sub: stdlib only
	"idempotency":        {"validator": true},
	"shared/persistence": {"validator": true}, // shared kernel: never a domain context
	// shared/httpx is the transport shared by every HTTP surface: it reaches
	// the shared kernel (constants, persistence, validator) and nothing else —
	// no domain context may ever be reachable from it (identity arrives as a
	// consumer-side interface, adapted at the composition boundary).
	"shared/httpx": {"constants": true, "shared/persistence": true, "validator": true},
	"validator":    {}, // leaf
	"constants":    {},
	"config":       {"vcs": true, "shared/persistence": true, "validator": true, "logging": true},
	"logging":      {},
	"mailer":       {},
	"testdb":       {},
	"vcs":          {},
	"arch":         {}, // this package: walks the tree, imports nothing internal
}

// apiGateway lists what api (the HTTP gateway) may import: any context plus
// the support packages, mirroring the gateway node in the map.
var apiGateway = map[string]bool{
	"constants":          true,
	"eventbus":           true,
	"game":               true,
	"idempotency":        true,
	"identity":           true,
	"ledger":             true,
	"logging":            true,
	"mailer":             true,
	"schedule":           true,
	"shared/httpx":       true,
	"shared/persistence": true,
	"validator":          true,
}

// cmdRoot lists what the composition root may import: everything internal.
func cmdRoot(importee string) bool { return true }

type edge struct {
	from string // internal package name, or "cmd/<name>"
	to   string // internal package name
	file string // repo-relative file path where the edge appears
	line int
}

func TestImportBoundaries(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	var edges []edge
	fset := token.NewFileSet()
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		isTest := strings.HasSuffix(path, "_test.go")
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		fromPkg, inCmd, ok := classify(path, root)
		if !ok { // not part of the module's internal/cmd tree
			return nil
		}
		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			toPkg, ok := internalPkg(impPath)
			if !ok {
				continue
			}
			// cmd/* is the composition root: any edge is fine.
			if inCmd {
				continue
			}
			// The test harness is infra, not a context: exempt in test files.
			if isTest && toPkg == "testdb" {
				continue
			}
			edges = append(edges, edge{
				from: fromPkg, to: toPkg,
				file: relRepo(root, path, fset, imp.Pos()),
			})
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if len(edges) == 0 {
		t.Fatal("no internal import edges found — the walk is broken, fix before trusting green")
	}

	var violations []string
	for _, e := range edges {
		var ok bool
		switch e.from {
		case "api":
			ok = apiGateway[e.to]
		default:
			ok = allowedImports[e.from][e.to]
		}
		if !ok {
			violations = append(violations, e.violation())
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		want := renderMatrix()
		t.Errorf("import-boundary violations (%d):\n\n%s\n\nallowed edge matrix:\n%s",
			len(violations), strings.Join(violations, "\n"), want)
	}
}

func (e edge) violation() string {
	allowed := "nothing internal"
	if a, exists := allowedImports[e.from]; exists && len(a) > 0 {
		var names []string
		for n := range a {
			names = append(names, n)
		}
		sort.Strings(names)
		allowed = strings.Join(names, ", ")
	}
	if e.from == "api" {
		allowed = "any context (gateway): constants, eventbus, game, idempotency, identity, ledger, logging, mailer, schedule, shared/persistence, validator"
	}
	return "  X  " + e.from + " -> " + e.to + "\n     at " + e.file + "\n     " + e.from + " may import: " + allowed
}

func renderMatrix() string {
	pkgs := make([]string, 0, len(allowedImports)+1)
	for p := range allowedImports {
		pkgs = append(pkgs, p)
	}
	pkgs = append(pkgs, "api", "cmd/*")
	sort.Strings(pkgs)
	var b strings.Builder
	for _, p := range pkgs {
		if p == "cmd/*" {
			b.WriteString("  cmd/* -> any internal package (composition root)\n")
			continue
		}
		var names []string
		if p == "api" {
			for n := range apiGateway {
				names = append(names, n)
			}
		} else {
			for n := range allowedImports[p] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			names = []string{"—"}
		}
		b.WriteString("  " + p + " -> " + strings.Join(names, ", ") + "\n")
	}
	b.WriteString("  (+) any _test.go file may additionally import testdb (test harness)\n")
	return b.String()
}

// classify maps a .go file path to its internal package name and whether it
// lives under cmd/ (composition root).
func classify(path, root string) (pkg string, inCmd bool, ok bool) {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Dir(r)), "/")
	if len(parts) == 0 {
		return "", false, false
	}
	if parts[0] == "cmd" {
		return "cmd/" + parts[len(parts)-1], true, true
	}
	if parts[0] != "internal" {
		return "", false, false
	}
	// Nested packages (internal/shared/persistence) are named by their path
	// below internal/, so the matrix can hold shared kernel entries.
	return strings.Join(parts[1:], "/"), false, true
}

// internalPkg reports whether an import path belongs to this module's
// internal/ tree, returning the package name (e.g. "game").
func internalPkg(impPath string) (string, bool) {
	prefix := modulePath + "/internal/"
	if !strings.HasPrefix(impPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(impPath, prefix)
	if rest == "" {
		return "", false
	}
	return rest, true
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func relRepo(root, path string, fset *token.FileSet, pos token.Pos) string {
	f := fset.File(pos)
	line := 0
	if f != nil {
		line = f.Line(pos)
	}
	if r, err := filepath.Rel(root, path); err == nil {
		return r + ":" + itoa(line)
	}
	return path + ":" + itoa(line)
}

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

func mustModulePath() string {
	b, err := os.ReadFile(filepath.Join(mustRepoRoot(), "go.mod"))
	if err != nil {
		panic(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	panic("module directive not found in go.mod")
}

func mustRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("go.mod not found above test directory")
		}
		dir = parent
	}
}
