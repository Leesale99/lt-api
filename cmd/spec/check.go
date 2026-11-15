package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	statusImplemented   = "implemented"
	statusSpecifiedOnly = "specified-only"
	statusDrifted       = "drifted"

	stalePointerPrefix = "stale-pointer:"
)

// policy is one numbered invariant from docs/spec/rules.schema.md §3. The
// refusal policy carries WARN lines (the SQLSTATE set is illustrative).
type policy struct {
	ID    string
	Title string
}

var policies = []policy{
	{"1-id", "id format, uniqueness, flat per module"},
	{"2-enums", "frozen enum membership"},
	{"3-implemented", "implemented ⇒ sites and verification that resolve today"},
	{"4-specified-only", "specified-only ⇒ expert-ref, no sites, no verification, no refusal"},
	{"5-drifted", "drifted ⇒ notes; stale-pointer only on implemented rules"},
	{"6-authority", "database ⇒ mechanism + scope; any other authority ⇒ no mechanism"},
	{"7-copy-policy", "non-authoritative sites tied by pinned_by or a Tie: note"},
	{"8-authority-default", "authority ≠ category default ⇒ notes"},
	{"9-site-ref", "path::Symbol, never a bare path or a line number"},
	{"10-gherkin", "no verification.gherkin before Phase C"},
	{"11-fixture", "pinned_by names a declared fixture"},
	{"refusal", "refusal code sanity"},
}

var policyIndex = func() map[string]int {
	m := make(map[string]int, len(policies))
	for i, p := range policies {
		m[p.ID] = i
	}
	return m
}()

// Frozen enums, docs/spec/rules.schema.md §1.
var (
	moduleEnum   = enum("schedule", "game", "identity", "ledger", "kernel", "platform")
	categoryEnum = enum("invariant", "policy", "orchestration", "interface",
		"persistence-fact", "refusal-mapping", "boundary")
	statusEnum    = enum(statusImplemented, statusSpecifiedOnly, statusDrifted)
	authorityEnum = enum("database", "domain", "service", "repository",
		"composition-root", "handler")
	mechanismEnum = enum("constraint", "trigger", "composite-fk")
	scopeEnum     = enum("row-local", "cross-row", "cross-table")
	layerEnum     = enum("handler", "validator", "service", "domain", "repository",
		"db-constraint", "db-trigger")
	roleEnum = enum("authoritative", "defense-in-depth", "advisory-copy")
)

// categoryDefaultAuthority is the schema's default authority table. The
// refusal-mapping category has no default ("per rule"), so it is absent.
var categoryDefaultAuthority = map[string]string{
	"invariant":        "database",
	"policy":           "domain",
	"orchestration":    "service",
	"interface":        "handler",
	"persistence-fact": "repository",
	"boundary":         "composition-root",
}

// illustrativeSQLSTATEs are the codes the schema's refusal table shows. Codes
// outside the set are a WARN, not a failure (23505 and 42501 are real and
// intentionally catalogued).
var illustrativeSQLSTATEs = map[string]bool{
	"P0001": true,
	"23514": true,
	"23503": true,
	"23001": true,
}

var (
	idPattern         = regexp.MustCompile(`^R-[A-Z]+-\d{3}$`)
	sqlstatePattern   = regexp.MustCompile(`^[0-9A-Z]{5}$`)
	lineNumberPattern = regexp.MustCompile(`\.(?:go|sql):[0-9]+`)
	syntheticCode     = "synthetic"
)

func enum(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

type severity int

const (
	severityError severity = iota
	severityWarn
)

func (s severity) String() string {
	if s == severityWarn {
		return "WARN"
	}
	return "ERROR"
}

// Finding is one policy violation (or WARN line) for one rule.
type Finding struct {
	Policy   string
	RuleID   string
	Severity severity
	Message  string
}

type checker struct {
	root     string
	fixtures map[string]bool
	findings []Finding
}

func (c *checker) add(policyID, ruleID string, sev severity, format string, args ...any) {
	c.findings = append(c.findings, Finding{
		Policy:   policyID,
		RuleID:   ruleID,
		Severity: sev,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (c *checker) errorf(policyID, ruleID, format string, args ...any) {
	c.add(policyID, ruleID, severityError, format, args...)
}

func (c *checker) warnf(policyID, ruleID, format string, args ...any) {
	c.add(policyID, ruleID, severityWarn, format, args...)
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("spec check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rulesPath := fs.String("rules", "docs/spec/rules.yaml", "path to the canonical YAML catalog")
	strict := fs.Bool("strict", false, "exit 1 when any ERROR finding exists (warnings never fail)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "spec check: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	catalog, err := loadCatalog(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec check: %v\n", err)
		return 1
	}
	root, err := repoRoot(*rulesPath)
	if err != nil {
		fmt.Fprintf(stderr, "spec check: %v\n", err)
		return 1
	}

	findings := collectFindings(catalog, root)
	printFindings(stdout, *rulesPath, catalog, findings)

	errorsFound, _ := countFindings(findings)
	if *strict && errorsFound > 0 {
		return 1
	}
	return 0
}

// collectFindings runs every policy over the catalog. It is pure apart from
// reading the repo (files referenced by sites, tests and expert refs).
func collectFindings(catalog Catalog, root string) []Finding {
	c := &checker{root: root, fixtures: map[string]bool{}}
	for _, rule := range catalog {
		if rule.Fixture != nil && *rule.Fixture != "" {
			c.fixtures[*rule.Fixture] = true
		}
	}

	seenIDs := map[string]bool{}
	for _, rule := range catalog {
		c.checkID(rule, seenIDs)
		c.checkEnums(rule)
		c.checkImplemented(rule)
		c.checkSpecifiedOnly(rule)
		c.checkDrifted(rule)
		c.checkAuthority(rule)
		c.checkCopyPolicy(rule)
		c.checkAuthorityDefault(rule)
		c.checkSiteReferences(rule)
		c.checkGherkin(rule)
		c.checkFixtureRefs(rule)
		c.checkRefusal(rule)
	}

	sortFindings(c.findings)
	return c.findings
}

// policy 1: id matches ^R-[A-Z]+-\d{3}$ and is unique (flat per module).
func (c *checker) checkID(rule Rule, seen map[string]bool) {
	if !idPattern.MatchString(rule.ID) {
		c.errorf("1-id", rule.ID, "id %q does not match ^R-[A-Z]+-\\d{3}$", rule.ID)
	}
	if seen[rule.ID] {
		c.errorf("1-id", rule.ID, "duplicate id %q (ids are flat per module and never reused)", rule.ID)
	}
	seen[rule.ID] = true
}

// policy 2: frozen enum membership for every enumerated field, including each
// enforcement entry's layer and role.
func (c *checker) checkEnums(rule Rule) {
	check := func(field, value string, allowed map[string]bool, allowNull bool) {
		if value == "" && allowNull {
			return
		}
		if !allowed[value] {
			c.errorf("2-enums", rule.ID, "%s %q is not a frozen enum value", field, value)
		}
	}
	check("module", rule.Module, moduleEnum, false)
	check("category", rule.Category, categoryEnum, false)
	check("status", rule.Status, statusEnum, false)
	check("authority", rule.Authority, authorityEnum, false)
	if rule.Mechanism != nil {
		check("mechanism", *rule.Mechanism, mechanismEnum, false)
	}
	if rule.Scope != nil {
		check("scope", *rule.Scope, scopeEnum, false)
	}
	for _, site := range rule.Enforcement {
		check("enforcement.layer", site.Layer, layerEnum, false)
		check("enforcement.role", site.Role, roleEnum, false)
	}
}

// policy 3: implemented needs at least one site and one verification pointer,
// and every named path/symbol exists today. Sites are checked for every status
// (non-implemented rules must not carry any, which policy 4 reports).
func (c *checker) checkImplemented(rule Rule) {
	if rule.Status == statusImplemented {
		if len(rule.Enforcement) == 0 {
			c.errorf("3-implemented", rule.ID, "implemented rule has no enforcement site")
		}
		if len(verificationPointers(rule.Verification)) == 0 {
			c.errorf("3-implemented", rule.ID, "implemented rule has no verification pointer (unit or e2e)")
		}
	}

	for _, site := range rule.Enforcement {
		c.checkEnforcementSite(rule.ID, site.Site)
	}

	if rule.Status != statusImplemented {
		return
	}
	for _, pointer := range rule.Verification.Unit {
		c.checkVerificationPointer(rule.ID, "unit", pointer)
	}
	for _, pointer := range rule.Verification.E2E {
		c.checkVerificationPointer(rule.ID, "e2e", pointer)
	}
}

func (c *checker) checkEnforcementSite(ruleID, site string) {
	if site == "" {
		c.errorf("9-site-ref", ruleID, "enforcement site is empty")
		return
	}
	if lineNumberPattern.MatchString(site) {
		c.errorf("9-site-ref", ruleID, "enforcement site %q uses a line number; use path::Symbol", site)
		return
	}
	if strings.HasPrefix(site, "migrations/") {
		if !c.fileExists(site) {
			c.errorf("3-implemented", ruleID, "enforcement migration %q does not exist", site)
		}
		return
	}
	path, symbol, ok := splitPointer(site)
	if !ok {
		c.errorf("9-site-ref", ruleID, "enforcement site %q is neither a migrations/ id nor path::Symbol", site)
		return
	}
	if isLineNumber(symbol) {
		c.errorf("9-site-ref", ruleID, "enforcement site %q uses a line number; use path::Symbol", site)
		return
	}
	c.checkSymbolPath(ruleID, "enforcement", path, symbol, symbolExists)
}

// checkVerificationPointer resolves a pointer that policy 9 already accepted
// (checkSiteReferences reports format problems for every rule).
func (c *checker) checkVerificationPointer(ruleID, kind, pointer string) {
	path, symbol, ok := splitPointer(pointer)
	if !ok || lineNumberPattern.MatchString(pointer) || isLineNumber(symbol) {
		return
	}
	c.checkSymbolPath(ruleID, "verification", path, symbol, testSymbolExists)
}

func (c *checker) checkSymbolPath(ruleID, kind, path, symbol string, symbolOK func(src, symbol string) bool) {
	full := filepath.Join(c.root, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		c.errorf("3-implemented", ruleID, "%s path %q does not exist", kind, path)
		return
	}
	if !symbolOK(string(data), symbol) {
		c.errorf("3-implemented", ruleID, "%s symbol %q not found in %q", kind, symbol, path)
	}
}

// policy 4: specified-only means designed, not built — expert-ref exists, and
// there are no enforcement sites, verification pointers or refusal surface.
func (c *checker) checkSpecifiedOnly(rule Rule) {
	if rule.Status != statusSpecifiedOnly {
		return
	}
	if rule.ExpertRef == nil || *rule.ExpertRef == "" {
		c.errorf("4-specified-only", rule.ID, "specified-only rule has no expert-ref")
	} else if !c.fileExists(*rule.ExpertRef) {
		c.errorf("4-specified-only", rule.ID, "expert-ref %q does not exist", *rule.ExpertRef)
	}
	if len(rule.Enforcement) > 0 {
		c.errorf("4-specified-only", rule.ID, "specified-only rule has %d enforcement site(s)", len(rule.Enforcement))
	}
	if len(verificationPointers(rule.Verification)) > 0 || len(rule.Verification.Gherkin) > 0 {
		c.errorf("4-specified-only", rule.ID, "specified-only rule has verification pointers")
	}
	if rule.Refusal != nil {
		c.errorf("4-specified-only", rule.ID, "specified-only rule has a refusal")
	}
}

// policy 5: drifted needs notes saying what disagrees; stale-pointer notes are
// rot and only allowed on implemented rules.
func (c *checker) checkDrifted(rule Rule) {
	if rule.Status == statusDrifted && strings.TrimSpace(rule.Notes) == "" {
		c.errorf("5-drifted", rule.ID, "drifted rule has no notes naming the disagreement")
	}
	if rule.Status != statusImplemented && strings.Contains(rule.Notes, stalePointerPrefix) {
		c.errorf("5-drifted", rule.ID,
			"stale-pointer note on a %s rule; rot notes are only allowed on implemented rules", rule.Status)
	}
}

// policy 6: database authority must name mechanism + scope; every other
// authority must leave mechanism null.
func (c *checker) checkAuthority(rule Rule) {
	if rule.Authority == "database" {
		if rule.Mechanism == nil {
			c.errorf("6-authority", rule.ID, "database authority requires a mechanism")
		}
		if rule.Scope == nil {
			c.errorf("6-authority", rule.ID, "database authority requires a scope")
		}
		return
	}
	if rule.Mechanism != nil {
		c.errorf("6-authority", rule.ID, "authority %q must not set a mechanism (got %q)",
			rule.Authority, *rule.Mechanism)
	}
}

// policy 7: a data-carrying copy must be tied to its authority by a pinned_by
// fixture or a `Tie:` sentence. advisory-copy is exempt; authoritative sites
// are the authority itself.
func (c *checker) checkCopyPolicy(rule Rule) {
	for _, site := range rule.Enforcement {
		if site.Role == "authoritative" || site.Role == "advisory-copy" {
			continue
		}
		if !roleEnum[site.Role] {
			continue // policy 2 already reports the bad role
		}
		if site.PinnedBy != nil && *site.PinnedBy != "" {
			continue
		}
		if strings.Contains(rule.Notes, "Tie:") {
			continue
		}
		c.errorf("7-copy-policy", rule.ID,
			"non-authoritative site %q has no pinned_by and no `Tie:` note", site.Site)
	}
}

// policy 8: crossing a category boundary requires an explanation.
func (c *checker) checkAuthorityDefault(rule Rule) {
	defaultAuthority, ok := categoryDefaultAuthority[rule.Category]
	if !ok {
		return // refusal-mapping has no default ("per rule")
	}
	if rule.Authority != defaultAuthority && strings.TrimSpace(rule.Notes) == "" {
		c.errorf("8-authority-default", rule.ID,
			"authority %q deviates from category %q default %q without notes",
			rule.Authority, rule.Category, defaultAuthority)
	}
}

// policy 9: every verification pointer is path::Symbol — never a bare path,
// never a line number. Enforcement sites are validated in checkEnforcementSite.
func (c *checker) checkSiteReferences(rule Rule) {
	for _, pointer := range verificationPointers(rule.Verification) {
		if lineNumberPattern.MatchString(pointer) {
			c.errorf("9-site-ref", rule.ID, "verification pointer %q uses a line number; use path::Symbol", pointer)
			continue
		}
		_, symbol, ok := splitPointer(pointer)
		if !ok {
			c.errorf("9-site-ref", rule.ID, "verification pointer %q is not path::Symbol", pointer)
			continue
		}
		if isLineNumber(symbol) {
			c.errorf("9-site-ref", rule.ID, "verification pointer %q uses a line number; use path::Symbol", pointer)
		}
	}
}

// policy 10: feature files do not exist yet (Phase C).
func (c *checker) checkGherkin(rule Rule) {
	for _, entry := range rule.Verification.Gherkin {
		c.errorf("10-gherkin", rule.ID, "verification.gherkin entry %q is reserved for Phase C", entry)
	}
}

// policy 11: every pinned_by names a fixture some rule declares.
func (c *checker) checkFixtureRefs(rule Rule) {
	for _, site := range rule.Enforcement {
		if site.PinnedBy == nil || *site.PinnedBy == "" {
			continue
		}
		if !c.fixtures[*site.PinnedBy] {
			c.errorf("11-fixture", rule.ID,
				"pinned_by %q is not a fixture declared by any rule", *site.PinnedBy)
		}
	}
}

// Refusal sanity: `synthetic` or a 5-character SQLSTATE; codes outside the
// illustrative set are a WARN, not a failure.
func (c *checker) checkRefusal(rule Rule) {
	if rule.Refusal == nil {
		return
	}
	code := rule.Refusal.Code
	if code == "" {
		c.errorf("refusal", rule.ID, "refusal has no code")
		return
	}
	if code == syntheticCode {
		return
	}
	if !sqlstatePattern.MatchString(code) {
		c.errorf("refusal", rule.ID, "refusal code %q is neither 'synthetic' nor a 5-character SQLSTATE", code)
		return
	}
	if !illustrativeSQLSTATEs[code] {
		c.warnf("refusal", rule.ID,
			"refusal code %q is outside the illustrative set {P0001, 23514, 23503, 23001}", code)
	}
}

func verificationPointers(v Verification) []string {
	pointers := make([]string, 0, len(v.Unit)+len(v.E2E))
	pointers = append(pointers, v.Unit...)
	pointers = append(pointers, v.E2E...)
	return pointers
}

func isLineNumber(symbol string) bool {
	if symbol == "" {
		return false
	}
	for _, r := range symbol {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (c *checker) fileExists(rel string) bool {
	info, err := os.Stat(filepath.Join(c.root, filepath.FromSlash(rel)))
	return err == nil && !info.IsDir()
}

// testSymbolExists is the strict check policy 3 asks for on verification
// pointers: the named Go test is a top-level function (`func TestName(`).
func testSymbolExists(src, symbol string) bool {
	name := symbol
	if i := strings.LastIndex(symbol, "."); i >= 0 {
		name = symbol[i+1:]
	}
	return name != "" && strings.Contains(src, "func "+name+"(")
}

// symbolExists reports whether a Go symbol is declared in src. It accepts a
// top-level func (`func Name(`), a method (`func (r *T) Name(` — the catalog
// writes these as `T.Name`), a type, and const/var declarations. Symbols are
// identifiers, never line numbers.
func symbolExists(src, symbol string) bool {
	name := symbol
	if i := strings.LastIndex(symbol, "."); i >= 0 {
		name = symbol[i+1:]
	}
	if name == "" {
		return false
	}
	if strings.Contains(src, "func "+symbol+"(") || strings.Contains(src, "func "+symbol+"[") {
		return true
	}
	quoted := regexp.QuoteMeta(name)
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`func\s*\([^)]*\)\s*` + quoted + `\s*[\[(]`),
		regexp.MustCompile(`(?m)^\s*type\s+` + quoted + `\b`),
		regexp.MustCompile(`(?m)^\s*(?:const|var)\s+` + quoted + `\b`),
		regexp.MustCompile(`(?m)^\s*` + quoted + `\s*=`),
	}
	for _, re := range patterns {
		if re.MatchString(src) {
			return true
		}
	}
	return false
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		pi, pj := policyIndex[findings[i].Policy], policyIndex[findings[j].Policy]
		if pi != pj {
			return pi < pj
		}
		if findings[i].RuleID != findings[j].RuleID {
			return findings[i].RuleID < findings[j].RuleID
		}
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity < findings[j].Severity
		}
		return findings[i].Message < findings[j].Message
	})
}

func countFindings(findings []Finding) (errorsFound, warnings int) {
	for _, f := range findings {
		if f.Severity == severityWarn {
			warnings++
		} else {
			errorsFound++
		}
	}
	return errorsFound, warnings
}

func printFindings(w io.Writer, rulesPath string, catalog Catalog, findings []Finding) {
	statusCounts := map[string]int{}
	for _, rule := range catalog {
		statusCounts[rule.Status]++
	}
	statusParts := make([]string, 0, len(statusCounts))
	for _, status := range sortedKeys(statusCounts) {
		statusParts = append(statusParts, fmt.Sprintf("%d %s", statusCounts[status], status))
	}
	fmt.Fprintf(w, "%s — %d rules (%s)\n\n", displayPath(rulesPath), len(catalog), strings.Join(statusParts, ", "))

	grouped := map[string][]Finding{}
	for _, f := range findings {
		grouped[f.Policy] = append(grouped[f.Policy], f)
	}

	for _, p := range policies {
		group := grouped[p.ID]
		if len(group) == 0 {
			continue
		}
		errorsFound, warnings := countFindings(group)
		fmt.Fprintf(w, "## %s — %s (%d errors, %d warnings)\n\n", p.ID, p.Title, errorsFound, warnings)
		for _, f := range group {
			prefix := ""
			if f.Severity == severityWarn {
				prefix = "WARN "
			}
			ruleID := f.RuleID
			if ruleID == "" {
				ruleID = "(catalog)"
			}
			fmt.Fprintf(w, "- %s**%s** %s\n", prefix, ruleID, f.Message)
		}
		fmt.Fprint(w, "\n")
	}

	if len(findings) == 0 {
		fmt.Fprint(w, "No findings — the catalog satisfies docs/spec/rules.schema.md §3.\n\n")
	}

	errorsFound, warnings := countFindings(findings)
	fmt.Fprint(w, "## Summary\n\n| Policy | Errors | Warnings |\n| --- | ---: | ---: |\n")
	for _, p := range policies {
		e, wn := countFindings(grouped[p.ID])
		fmt.Fprintf(w, "| `%s` | %d | %d |\n", p.ID, e, wn)
	}
	fmt.Fprintf(w, "| **total** | **%d** | **%d** |\n\n", errorsFound, warnings)

	switch {
	case errorsFound == 0 && warnings == 0:
		fmt.Fprint(w, "spec check: no findings — report-only; pass --strict to exit 1 on errors\n")
	case errorsFound == 0:
		fmt.Fprintf(w, "spec check: 0 errors, %d warnings — report-only; pass --strict to exit 1 on errors\n", warnings)
	default:
		fmt.Fprintf(w, "spec check: %d errors, %d warnings — report-only; pass --strict to exit 1 on errors\n",
			errorsFound, warnings)
	}
}
