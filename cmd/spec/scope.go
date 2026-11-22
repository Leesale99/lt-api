package main

// scope.go owns the one Go-side reading of the SPEC-SCOPE packet grammar and of
// the --module/--rule flag pair. Every command that narrows a report asks the
// same three questions — which rules are in scope, what label the scope prints,
// and whether a finding's subject belongs to it — and answers them here.
//
// # Packet grammar (SPEC-SCOPE)
//
// A task packet may carry one scope line:
//
//	SPEC-SCOPE: module=<module> rules=<id>[,<id>…]
//
//   - Only lines whose trimmed form starts with `SPEC-SCOPE:` count; the prefix
//     is matched case-insensitively, and the first line with usable content
//     wins.
//   - A line whose body names neither a module nor a rule is NOT a scope: it
//     must not silence the full check.
//   - `module=` takes [A-Za-z0-9_./-]+ with any trailing `.`/`/` stripped.
//   - `rules=` takes [A-Za-z0-9_.,/-]+ split on `,`; each trimmed id must match
//     ^[A-Za-z0-9_-]+$ (an empty fragment is not an id).
//
// The normalized single-line form (`module=… rules=…`, an empty half dropped) is
// what the delegate extension forwards as PI_DELEGATE_SPEC_SCOPE, and ParseEnv
// reads that value back with the same body grammar. package main is the Go-side
// owner of the grammar; .pi/extensions/delegate/gates.ts keeps its own parser
// until B3 moves it onto these rules.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// scopeLinePrefix opens a task-packet scope line.
const scopeLinePrefix = "SPEC-SCOPE:"

// scopeBodyPatterns are the two field patterns of a scope body; both are
// searched, not anchored, so the field order does not change the parse.
// scopeRuleIDPattern validates one rule id from the `rules=` list.
var (
	scopeModulePattern = regexp.MustCompile(`module=([A-Za-z0-9_./-]+)`)
	scopeRulesPattern  = regexp.MustCompile(`rules=([A-Za-z0-9_.,/-]+)`)
	scopeRuleIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Scope is a narrowing of the catalog: one module (whose rules are resolved
// against the catalog), or a set of rule ids. Rules is the resolved id set; for
// a module scope it stays empty until Resolve has seen the catalog.
type Scope struct {
	Module string
	Rules  []string
}

// parseScopeBody reads one scope body. ok is false when the body names neither a
// module nor a rule, which is the "this line is not a scope" case.
func parseScopeBody(body string) (Scope, bool) {
	scope := Scope{}
	if match := scopeModulePattern.FindStringSubmatch(body); match != nil {
		scope.Module = strings.TrimRight(match[1], "./")
	}
	if match := scopeRulesPattern.FindStringSubmatch(body); match != nil {
		for _, part := range strings.Split(match[1], ",") {
			id := strings.TrimSpace(part)
			if id != "" && scopeRuleIDPattern.MatchString(id) {
				scope.Rules = append(scope.Rules, id)
			}
		}
	}
	if scope.Module == "" && len(scope.Rules) == 0 {
		return Scope{}, false
	}
	return scope, true
}

// ParseTaskLine extracts the scope from a task packet: the first SPEC-SCOPE line
// with usable content. ok is false when the packet names no scope at all.
func ParseTaskLine(text string) (Scope, bool) {
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if len(line) < len(scopeLinePrefix) ||
			!strings.EqualFold(line[:len(scopeLinePrefix)], scopeLinePrefix) {
			continue
		}
		if scope, ok := parseScopeBody(line[len(scopeLinePrefix):]); ok {
			return scope, true
		}
	}
	return Scope{}, false
}

// ParseEnv parses the PI_DELEGATE_SPEC_SCOPE value: the already-normalized
// `module=… rules=…` form, or a hand-set body. An empty or unusable value is
// ok == false, which means the full check.
func ParseEnv(value string) (Scope, bool) {
	if strings.TrimSpace(value) == "" {
		return Scope{}, false
	}
	return parseScopeBody(value)
}

// ParseCLI parses the --module/--rule flag pair every scoped command accepts.
// The error text is the message after the command's "spec <cmd>: " prefix; each
// command keeps its own exit status for a bad flag (check and queue use 2, brief
// 1). Nothing catalog-dependent is resolved here: see Resolve.
func ParseCLI(module, ruleID string) (Scope, error) {
	switch {
	case module != "" && ruleID != "":
		return Scope{}, fmt.Errorf("--module and --rule are mutually exclusive")
	case module != "":
		if !moduleEnum[module] {
			return Scope{}, fmt.Errorf("unknown module %q; one of %s",
				module, strings.Join(catalogModules(), ", "))
		}
		return Scope{Module: module}, nil
	case ruleID != "":
		return Scope{Rules: []string{ruleID}}, nil
	}
	return Scope{}, nil
}

// Resolve binds a scope to a loaded catalog: a rule scope must name a catalog
// rule (catalogLabel is the catalog path the error names), and a module scope
// expands to the ids that module owns. The expansion is what lets a module scope
// filter findings, not merely label the report.
func (s *Scope) Resolve(catalog Catalog, catalogLabel string) error {
	if s.Module != "" {
		s.Rules = RuleIDs(catalog, s.Module)
		return nil
	}
	if len(s.Rules) == 0 {
		return nil
	}
	known := make(map[string]bool, len(catalog))
	for _, rule := range catalog {
		known[rule.ID] = true
	}
	for _, id := range s.Rules {
		if !known[id] {
			return fmt.Errorf("unknown rule id %q; %s is the catalog", id, catalogLabel)
		}
	}
	return nil
}

// RuleIDs returns the ids of the rules a module owns, in catalog order.
func RuleIDs(catalog Catalog, module string) []string {
	ids := make([]string, 0, len(catalog))
	for _, rule := range catalog {
		if rule.Module == module {
			ids = append(ids, rule.ID)
		}
	}
	return ids
}

// IDs returns the scope's rule ids in ascending order.
func (s Scope) IDs() []string {
	ids := make([]string, len(s.Rules))
	copy(ids, s.Rules)
	sort.Strings(ids)
	return ids
}

// Contains reports whether a finding subject (a rule id, a feature pointer or an
// annotation site) is in scope. A catalog-level finding carries no subject, so it
// is never in scope: those are reported only unscoped.
func (s Scope) Contains(subject string) bool {
	if subject == "" {
		return false
	}
	for _, id := range s.Rules {
		if id == subject {
			return true
		}
	}
	return false
}

// Empty reports whether the scope narrows nothing.
func (s Scope) Empty() bool {
	return s.Module == "" && len(s.Rules) == 0
}

// Select returns the rules the scope contains, preserving the input order — the
// callers hand it sortedRules, so a module brief or queue stays id-ordered.
func (s Scope) Select(rules []Rule) []Rule {
	selected := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if s.Contains(rule.ID) {
			selected = append(selected, rule)
		}
	}
	return selected
}

// String is the report label: "module game" or "rule R-GAME-001".
func (s Scope) String() string {
	switch {
	case s.Module != "":
		return "module " + s.Module
	case len(s.Rules) == 1:
		return "rule " + s.Rules[0]
	case len(s.Rules) > 1:
		return "rules " + strings.Join(s.Rules, ", ")
	}
	return ""
}
