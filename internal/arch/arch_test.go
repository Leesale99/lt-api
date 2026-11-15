// Package arch pins the bounded-context dependency map (Phase 05, as built;
// final shape after phase 6) in code: it walks every Go file under internal/
// and cmd/, extracts the package-level import edges, and fails with a
// readable diff when an edge outside the matrix appears.
//
// The matrix mirrors docs/designs/bounded-contexts-dependency-map.mmd.
//
// Classes are uniform (path-aware, see classify):
//
//   - "<module>" — a bounded context (schedule, game, ledger, identity), at
//     ANY depth of its directory tree.
//   - "kernel:<name>" — the kernel machinery under internal/kernel/
//     (kernel/persistence → kernel:persistence): httpx, persistence,
//     eventbus, validator, idempotency.
//   - "platform:<name>" — the technical platform under internal/platform/
//     (platform/config → platform:config): config, logging, mailer, server.
//   - "shared:<name>" — the test-only remainder that is neither kernel nor
//     platform: internal/testdb (the DB harness) and internal/arch (this
//     package).
//   - "cmd" — the composition root. Phase 6 deleted the api gateway class
//     with the package: cmd/api now owns the router, the middleware chain and
//     the healthcheck, so there is no API-layer row to special-case.
//   - "e2e" — the acceptance suite (internal/e2e): flat, test-only, and the
//     composition root in test form, so it gets its own cmd-like row instead
//     of collapsing into "shared:e2e".
//   - "spec" — the executable-specification suite (internal/spec): flat,
//     test-only, and the second composition root in test form (godog over the
//     same modules, no server), so it gets its own row for the same reason.
//
// The kernel is a set of context-agnostic technical capabilities: each owns
// no business rules, never imports a module, and serves any context that
// needs it — at least one today ("more than one context" is a signal, not a
// requirement). kernel:idempotency is the first STATEFUL member: it owns the
// idempotency_keys table, while still holding no business rule of its own.
//
// Because nesting never changes a class, "modules import other module ROOTS
// only" and "the kernel imports the kernel only" are enforced by the class
// scheme itself: internal/<module>/internal/<x> is still "<module>", and no
// kernel:* or platform:* row may list a module (pinned by
// TestKernelImportsOnlyKernel and TestNonModulesNeverImportModules). No
// module may reach the platform either (pinned by
// TestModulesNeverImportPlatform).
//
// The Phase 05 refinements the matrix encodes:
//
//   - schedule is the ACL: it owns teams/seasons/rounds/matches, mints their
//     ids, and may import only the kernel (kernel:persistence,
//     kernel:validator, kernel:httpx) and the event bus it publishes on. It
//     must NEVER import game, ledger or identity — the downstream side of the
//     seam.
//   - schedule is also the LAYERED module (phase 3): its facade (the root
//     package) reaches its own internal layers and the kernel, and each layer
//     has its own row — model → kernel:validator; repository →
//     model/kernel:persistence; service → model/repository/kernel:eventbus;
//     handler → model/service/kernel:httpx. The layers cannot skip past each
//     other, and no layer can reach the facade's callers.
//   - game is the HEXAGONAL module (phase 4): its facade (the root package)
//     reaches its own core and adapters and nothing else; each nested package
//     has its own row — core/domain → kernel:validator; core/port →
//     domain/kernel:persistence; core/service → domain/port; the inbound HTTP
//     adapter → core/service/domain/port + kernel:httpx + the idempotency
//     KERNEL and the identity ROOT (wire-level translation only); the outbound ACL package
//     is the ONLY game package importing schedule (the anti-corruption layer
//     and the event translation); the Postgres adapter → core + the
//     persistence kernel. The facade wires the ledger and idempotency ports
//     the core declares.
//   - ledger and eventbus are thin: no domain imports (kernel:persistence
//     only for ledger).
//   - identity is also a LAYERED module (phase 5): its facade (the root
//     package) reaches its own internal layers and the kernel, and
//     each layer has its own row — model → kernel:validator; repository →
//     model/kernel:persistence; service → model/repository/kernel:persistence
//     (it owns the DB budget its store calls are wrapped in);
//     handler → model/service/kernel:httpx/kernel:persistence/kernel:validator.
//     The facade is what httpx's consumer-side lookups are satisfied by, and
//     it is the only identity package any other class may import.
//   - cmd/api is the composition root: it wires every module and the kernel,
//     owns the router and the chain, and imports anything. There is
//     no gateway class between it and the modules (phase 6 deleted the api
//     package); the only HTTP surface left above the modules is httpx.
//
// Test-file policy: _test.go files are checked against the same matrix — a
// test importing across a forbidden edge is a real coupling, not a fiction.
// Two documented exemptions:
//
//   - shared:testdb (the DB test harness, providing pools/roles/seeds) may be
//     imported from any _test.go file; it is to Go tests what the testing
//     package is — infrastructure, not a context.
//   - the E2E suite is the composition root in test form: every converted api
//     test lives in internal/e2e and composes the real modules exactly like
//     cmd/api/main.go (the ONE compose_test.go helper). It has its own "e2e"
//     class, whose row is cmd-like in width but enumerated; nothing may import
//     it. platform:server's production code still imports nothing internal.
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

// moduleClasses are the package classes that are NOT kernel or platform: the
// bounded contexts. Every other internal package classifies as
// "kernel:<name>", "platform:<name>" or "shared:<name>" (see classOf) — a new
// module must be registered here to get its own matrix row, so an
// unregistered package can only ever be treated as kernel/platform/shared
// (which forbids it from importing any module).
var moduleClasses = map[string]bool{
	"game":     true,
	"identity": true,
	"ledger":   true,
	"schedule": true,
}

// moduleLayers declares the intra-module layer packages a MODULE exposes to
// its own import matrix. A layered module nests its implementation under
// internal/<module>/internal/<layer>; with the collapse rule those packages
// would all be "<module>" and the matrix could not tell a handler from a
// model. Each declared layer becomes its own class ("schedule:model",
// "schedule:repository", ...), and the facade keeps the module class, so the
// layer edges are pinned explicitly (model → validator; repository →
// persistence; service → model/repository/eventbus; handler →
// httpx/service/model; facade → its own layers + the kernel).
//
// Only schedule and identity are layered so far (the phase-3 pilot and the
// phase-5 conversion); every other module keeps the uniform collapse — an
// undeclared layer directory still classifies as its module, so nothing
// slips past the matrix.
var moduleLayers = map[string]map[string]bool{
	"schedule": {"model": true, "repository": true, "service": true, "handler": true},
	"identity": {"model": true, "repository": true, "service": true, "handler": true},
}

// hexLayers names a HEXAGONAL module's nested packages: their path below
// internal/<module>/internal/ maps to their own class, so core/domain,
// core/service and the two adapters cannot share a row. Lookup is by exact
// path with a longest-prefix fallback (a new sub-package inherits its nearest
// declared ancestor's row instead of silently collapsing into the module
// class). An undeclared package still classifies as its module, so nothing
// slips past the matrix.
var hexLayers = map[string]map[string]string{
	"game": {
		"core/domain":               "game:core:domain",
		"core/port":                 "game:core:port",
		"core/service":              "game:core:service",
		"adapter/inbound/http":      "game:adapter:inbound:http",
		"adapter/outbound":          "game:adapter:outbound",
		"adapter/outbound/postgres": "game:adapter:outbound:postgres",
	},
}

// allowedImports maps a class to the classes it may import. Anything not
// listed is forbidden; a class absent from the map defaults to: no internal
// imports. Rows below the modules (kernel:*, platform:*, shared:*) may never
// list a module; platform:* rows reach platform:* and kernel:* only, and
// kernel:* rows reach kernel:* only (pinned by
// TestNonModulesNeverImportModules, TestModulesNeverImportPlatform and
// TestKernelImportsOnlyKernel). The kernel never reaches a module.
var allowedImports = map[string]map[string]bool{
	// schedule (layered): the facade reaches its own layers and nothing else
	// internal; each layer's row is its legal edge set.
	"schedule": {
		"schedule:handler":    true,
		"schedule:model":      true,
		"schedule:repository": true,
		"schedule:service":    true,
		"kernel:eventbus":     true,
	},
	// The domain model: entities, validators, events. No storage, no
	// transport, no bus.
	"schedule:model": {
		"kernel:validator": true,
	},
	// Storage: SQL and its error classification. It maps rows to model
	// values; it never publishes (the service owns the event seam).
	"schedule:repository": {
		"schedule:model":     true,
		"kernel:persistence": true,
	},
	// Use cases: they compose repositories, publish post-commit, and speak
	// the model's vocabulary.
	"schedule:service": {
		"schedule:model":      true,
		"schedule:repository": true,
		"kernel:eventbus":     true,
		"kernel:persistence":  true,
	},
	// The HTTP layer: request shape and wire DTOs over the use cases.
	"schedule:handler": {
		"schedule:model":     true,
		"schedule:service":   true,
		"kernel:httpx":       true,
		"kernel:persistence": true,
		"kernel:validator":   true,
	},
	// game (hexagonal): the facade wires its own layers and the ports'
	// providers; it reaches no other context's internals (ledger and the
	// idempotency claim engine arrive as the core's ports, structurally
	// satisfied).
	"game": {
		"game:adapter:inbound:http":      true,
		"game:adapter:outbound":          true,
		"game:adapter:outbound:postgres": true,
		"game:core:domain":               true,
		"game:core:port":                 true,
		"game:core:service":              true,
		"kernel:eventbus":                true,
		"kernel:idempotency":             true, // the facade wraps the claim engine
	},
	// The core domain: entities, validators, the state machine, sentinels. No
	// storage, no transport, no calendar, no event bus.
	"game:core:domain": {
		"kernel:validator": true,
	},
	// The consumer-side interfaces. They name the pgx tx type (the documented
	// pragmatic exception) and the pagination primitives every list crosses
	// with; nothing else.
	"game:core:port": {
		"game:core:domain":   true,
		"kernel:persistence": true,
	},
	// The use cases: ports and domain, plus the kernel's pagination shape the
	// list use cases pass through and the validator the merged player row goes
	// through. No storage, no transport, no calendar.
	"game:core:service": {
		"game:core:domain":   true,
		"game:core:port":     true,
		"kernel:persistence": true,
		"kernel:validator":   true,
	},
	// The inbound HTTP adapter: the frozen wire DTOs over the use cases, plus
	// the idempotency kernel's token types/validation and the identity ROOT's
	// user existence read for player create. It draws its per-request
	// database budget from the persistence kernel.
	"game:adapter:inbound:http": {
		"game:core:domain":   true,
		"game:core:port":     true,
		"game:core:service":  true,
		"identity":           true,
		"kernel:httpx":       true,
		"kernel:idempotency": true,
		"kernel:persistence": true,
		"kernel:validator":   true,
	},
	// The outbound anti-corruption layers: THE only game package allowed to
	// import the calendar. Its types and errors stop here.
	"game:adapter:outbound": {
		"game:core:domain":   true,
		"game:core:port":     true,
		"game:core:service":  true,
		"kernel:eventbus":    true,
		"kernel:idempotency": true,
		"schedule":           true,
	},
	// The Postgres adapter: the SQL behind the ports and its error
	// translation. No calendar, no transport, no use cases.
	"game:adapter:outbound:postgres": {
		"game:core:domain":   true,
		"game:core:port":     true,
		"kernel:persistence": true,
	},
	"identity": {
		"identity:handler":    true,
		"identity:model":      true,
		"identity:repository": true,
		"identity:service":    true,
		"kernel:httpx":        true,
		"kernel:validator":    true,
	},
	// The domain model: entities, validators and the identity sentinels. No
	// storage, no transport.
	"identity:model": {
		"kernel:validator": true,
	},
	// Storage: SQL and its error classification (Postgres facts → the model's
	// sentinels). It maps rows to model values; it never speaks HTTP.
	"identity:repository": {
		"identity:model":     true,
		"kernel:persistence": true,
	},
	// Use cases: the activation transaction and the registration/token
	// flows (which deliver mail through the Mailer port declared here). The
	// DB budget they wrap their store calls in is the kernel's
	// persistence.DBTimeout — the crypto between those calls runs untimed.
	"identity:service": {
		"identity:model":      true,
		"identity:repository": true,
		"kernel:persistence":  true,
	},
	// The HTTP layer: the frozen wire DTOs over the use cases. It draws the
	// DB budget (persistence.DBTimeout) for the paths whose use case does no
	// crypto; the crypto paths leave the timing to the service.
	"identity:handler": {
		"identity:model":     true,
		"identity:service":   true,
		"kernel:httpx":       true,
		"kernel:persistence": true,
		"kernel:validator":   true,
	},
	"ledger": {
		"kernel:persistence": true,
	},
	// kernel:idempotency is the first STATEFUL kernel package: it owns the
	// idempotency_keys table but holds no business rule of its own, so it
	// reaches only the validator.
	"kernel:idempotency": {
		"kernel:validator": true,
	},

	// The kernel machinery (internal/kernel/).
	"kernel:eventbus":    {}, // pure in-proc pub/sub: stdlib only
	"kernel:persistence": {"kernel:validator": true},
	// kernel:httpx is the transport shared by every HTTP surface: it reaches
	// the kernel (persistence, validator) and nothing else — no module
	// may ever be reachable from it (identity arrives as a consumer-side
	// interface, adapted at the composition boundary). Its per-request list
	// defaults and sweep interval are local consts; the database budget it
	// spends on auth/permission lookups comes from the persistence kernel.
	"kernel:httpx": {
		"kernel:persistence": true,
		"kernel:validator":   true,
	},
	// e2e is the acceptance suite (the composition root in test form): it
	// composes the real modules over the real Postgres test pools exactly as
	// cmd/api/main.go does, so it reaches module ROOTS and the kernel the way
	// cmd does. Every file in the package is a _test.go — doc.go holds only
	// the package comment — so this is not a production surface, and nothing
	// may import e2e: it is a leaf.
	"e2e": {
		"game":               true,
		"identity":           true,
		"ledger":             true,
		"schedule":           true,
		"kernel:eventbus":    true,
		"kernel:httpx":       true,
		"kernel:idempotency": true,
		"kernel:persistence": true,
		"platform:server":    true, // the suite drives the process lifecycle
	},

	// The specification suite: the modules composed over real Postgres, in
	// test form (internal/spec). It drives the modules' own route tables and
	// the settlement event seam in-process — no server, no middleware chain —
	// so it reaches the modules and the kernel edge they publish/listen on,
	// and nothing else.
	"spec": {
		"game":               true,
		"identity":           true,
		"ledger":             true,
		"schedule":           true,
		"kernel:eventbus":    true,
		"kernel:httpx":       true,
		"kernel:idempotency": true,
	},

	// The platform (internal/platform/): process-level technical packages.
	// None reaches a module; today none reaches even the kernel (config's vcs
	// helper is inlined, and the server/logging/mailer packages use stdlib and
	// external libraries only). Only cmd/api (implicitly) and e2e import them.
	"platform:config":  {}, // flag/env precedence + semantic validation
	"platform:logging": {}, // the process-wide slog logger and the pgx bridge
	"platform:mailer":  {}, // SMTP delivery with embedded templates
	// platform:server owns the process lifecycle: it serves a fully-built
	// handler and knows no module, so it reaches nothing internal — its
	// shutdown budgets are local consts.
	"platform:server":  {},
	"kernel:validator": {},
	"shared:testdb":    {},
	"shared:arch":      {}, // this package: walks the tree, imports nothing internal
}

// testOnlyImports widens a class's edge set for _test.go files only. The
// end-to-end DB suites wire the real adapters over the real calendar — the
// composition root's job — so the use-case package's tests reach the game
// adapters, the ledger and the calendar facade, and the Postgres adapter's
// tests reach the calendar ACL for the round guards. Production code in the
// same package keeps the narrower row above (pinned by the same check).
var testOnlyImports = map[string]map[string]bool{
	"game:core:service": {
		"game:adapter:outbound":          true,
		"game:adapter:outbound:postgres": true,
		"game:core:service":              true, // external test package import
		"kernel:eventbus":                true,
		"kernel:idempotency":             true,
		"ledger":                         true,
		"schedule":                       true,
	},
	"game:adapter:outbound:postgres": {
		"game:adapter:outbound": true,
		"schedule":              true,
	},
}

type edge struct {
	from     string // class of the importing package ("cmd", "e2e", "<module>", "kernel:<name>", "platform:<name>" or "shared:<name>")
	to       string // class of the imported package, same scheme
	file     string // repo-relative file path where the edge appears
	testOnly bool   // the importing file is a _test.go
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
		fromClass, ok := classify(path, root)
		if !ok { // not part of the module's internal/cmd tree
			return nil
		}
		// cmd is the composition root: any edge is fine.
		if fromClass == "cmd" {
			return nil
		}
		for _, imp := range f.Imports {
			impPath := strings.Trim(imp.Path.Value, `"`)
			toClass, ok := internalPkg(impPath)
			if !ok {
				continue
			}
			// The test harness is infra, not a context: exempt in test files.
			if isTest && toClass == "shared:testdb" {
				continue
			}
			edges = append(edges, edge{
				from: fromClass, to: toClass,
				file:     relRepo(root, path, fset, imp.Pos()),
				testOnly: isTest,
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
		ok := allowedImports[e.from][e.to]
		if !ok {
			ok = e.testOnly && testOnlyImports[e.from][e.to]
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

// TestNonModulesNeverImportModules pins the direction for everything below
// the modules: no platform:*, kernel:* or shared:* row may list a module
// class, so the technical layers can never grow a dependency on a bounded
// context (the edge check would already reject the import; this names the
// rule when someone edits the matrix instead). Only the module rows
// themselves (their own layers, the game ACL's calendar) and the e2e
// composition root may reach a module.
func TestNonModulesNeverImportModules(t *testing.T) {
	for from, targets := range allowedImports {
		if from == "e2e" || from == "spec" || isModuleClass(from) {
			continue
		}
		for to := range targets {
			if isModuleClass(to) {
				t.Errorf("matrix: %s may not import %s — only a module or e2e reaches a module", from, to)
			}
		}
	}
}

// TestModulesNeverImportPlatform pins the platform direction: a platform:*
// row reaches platform:* and kernel:* only, and no module row may reach a
// platform:* class. Only cmd/api (the composition root, implicit) and e2e
// import the platform.
func TestModulesNeverImportPlatform(t *testing.T) {
	for from, targets := range allowedImports {
		isPlatform := strings.HasPrefix(from, "platform:")
		for to := range targets {
			if strings.HasPrefix(to, "platform:") {
				if !isPlatform && from != "e2e" {
					t.Errorf("matrix: %s may not import %s — only cmd and e2e import the platform", from, to)
				}
				continue
			}
			if isPlatform && !strings.HasPrefix(to, "kernel:") {
				t.Errorf("matrix: %s may not import %s — a platform package imports platform:*/kernel:* only", from, to)
			}
		}
	}
}

// isModuleClass reports whether a class names a bounded context: the module
// root itself ("game") or any of its layers/facade rows ("game:core:domain").
func isModuleClass(class string) bool {
	for module := range moduleClasses {
		if class == module || strings.HasPrefix(class, module+":") {
			return true
		}
	}
	return false
}

// TestKernelImportsOnlyKernel pins the kernel direction: no kernel:* row may
// list a module, a platform:* or a shared:* class, so the kernel imports the
// kernel only (httpx → persistence → validator).
func TestKernelImportsOnlyKernel(t *testing.T) {
	for from, targets := range allowedImports {
		if !strings.HasPrefix(from, "kernel:") {
			continue
		}
		for to := range targets {
			if !strings.HasPrefix(to, "kernel:") {
				t.Errorf("matrix: %s may not import %s — the kernel imports kernel:* only", from, to)
			}
		}
	}
}

func (e edge) violation() string {
	allowed := "nothing internal"
	if a, exists := allowedImports[e.from]; exists && len(a) > 0 {
		allowed = joinClasses(a)
	}
	return "  X  " + e.from + " -> " + e.to + "\n     at " + e.file + "\n     " + e.from + " may import: " + allowed
}

// joinClasses renders a matrix row (a class set) as sorted, comma-separated
// class names — violations and the rendered matrix speak the same vocabulary
// the class scheme uses.
func joinClasses(set map[string]bool) string {
	names := make([]string, 0, len(set))
	for n := range set {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func renderMatrix() string {
	pkgs := make([]string, 0, len(allowedImports)+1)
	for p := range allowedImports {
		pkgs = append(pkgs, p)
	}
	pkgs = append(pkgs, "cmd")
	sort.Strings(pkgs)
	var b strings.Builder
	for _, p := range pkgs {
		if p == "cmd" {
			b.WriteString("  cmd -> any internal package (composition root)\n")
			continue
		}
		names := joinClasses(allowedImports[p])
		if names == "" {
			names = "—"
		}
		b.WriteString("  " + p + " -> " + names + "\n")
	}
	for from, targets := range testOnlyImports {
		b.WriteString("  (+) _test.go only: " + from + " -> " + joinClasses(targets) + "\n")
	}
	b.WriteString("  (+) any _test.go file may additionally import shared:testdb (test harness)\n")
	return b.String()
}

// classify maps a .go file path to its import class: "cmd" under cmd/, and
// otherwise the class of its directory below internal/ (see classOf).
func classify(path, root string) (class string, ok bool) {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	parts := strings.Split(filepath.ToSlash(filepath.Dir(r)), "/")
	if len(parts) == 0 || parts[0] == "." {
		return "", false
	}
	switch parts[0] {
	case "cmd":
		return "cmd", true
	case "internal":
	default:
		return "", false
	}
	if len(parts) < 2 {
		return "", false // a .go file directly in internal/: no package class
	}
	return classOf(parts[1:])
}

// classOf maps the path segments below internal/ to a class name. Uniform at
// any depth: the first segment decides — a module keeps its own class however
// deeply it nests (internal/schedule/internal/foo is still "schedule"), the
// acceptance suite is its own class (internal/e2e → "e2e"), the platform is
// named by its first two segments (internal/platform/config →
// platform:config), and everything else is kernel, named the same way
// (internal/kernel/httpx → kernel:httpx, internal/testdb → shared:testdb).
// The collapse of nested packages into their module class
// IS the "modules import module roots only" rule.
func classOf(below []string) (string, bool) {
	if len(below) == 0 || below[0] == "" {
		return "", false
	}
	if below[0] == "e2e" {
		// The acceptance suite: a flat, non-module directory that is the
		// composition root in test form. It gets its own class rather than
		// defaulting to "shared:e2e", so its (cmd-like) edges are pinned in
		// their own row and nothing can reach it by accident.
		return "e2e", true
	}
	if below[0] == "spec" {
		// The executable-specification suite: flat, non-module, test-only,
		// and the second composition root in test form (no server). Its own
		// class keeps its module edges pinned in their own row.
		return "spec", true
	}
	if below[0] == "kernel" {
		if len(below) < 2 || below[1] == "" {
			return "", false // internal/kernel itself holds no packages
		}
		return "kernel:" + below[1], true
	}
	if below[0] == "platform" {
		if len(below) < 2 || below[1] == "" {
			return "", false // internal/platform itself holds no packages
		}
		return "platform:" + below[1], true
	}
	if moduleClasses[below[0]] {
		if len(below) >= 3 && below[1] == "internal" {
			// A layered module names its implementation packages: the facade is
			// the module class, each declared layer its own ("schedule:model").
			// Undeclared directories keep the module class (still inside it).
			if layers, ok := moduleLayers[below[0]]; ok && layers[below[2]] {
				return below[0] + ":" + below[2], true
			}
			// A hexagonal module names nested packages by their full path below
			// internal/: core/domain, core/service, adapter/outbound/postgres.
			if layers, ok := hexLayers[below[0]]; ok {
				nested := strings.Join(below[2:], "/")
				for {
					if class, found := layers[nested]; found {
						return class, true
					}
					cut := strings.LastIndex(nested, "/")
					if cut < 0 {
						break
					}
					nested = nested[:cut]
				}
			}
		}
		return below[0], true
	}
	// Everything else is the test-only rest (internal/testdb →
	// shared:testdb, internal/arch → shared:arch): the DB harness and this
	// matrix package, neither a module nor a technical capability.
	return "shared:" + below[0], true
}

// internalPkg reports whether an import path belongs to this module's
// internal/ tree, returning its class — the same class classify assigns to a
// file in the imported package, so edges compare like for like.
func internalPkg(impPath string) (string, bool) {
	prefix := modulePath + "/internal/"
	if !strings.HasPrefix(impPath, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(impPath, prefix)
	if rest == "" {
		return "", false
	}
	return classOf(strings.Split(rest, "/"))
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
