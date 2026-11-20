package main

// vocabulary.go implements checker policy 13 (`13-vocabulary`,
// docs/spec/rules.schema.md §3): the step text's vocabulary is bounded from
// both sides.
//
//   - A DENYLIST (gherkin-style.md §6): transport vocabulary — the words that
//     describe how a message is shaped rather than what the business decides —
//     digit-form status-like numbers, all absolute, and SQL vocabulary, which is
//     partitioned: a homograph errors only on adjacency, a hard keyword alone
//     (sqlHomographTokens).
//   - A seeded ALLOWLIST, docs/spec/glossary.yaml: every remaining token in a
//     step must be declared in that module's section or in `common`. The seed
//     is today's tree, so the glossaries start as a description of the
//     vocabulary the features already use; a new word is a deliberate edit to
//     the glossary, not an accident that slips through review.
//
// Attribution walks the parsed feature tree (gherkin.go), so every finding
// names `file::scenario` exactly as a `verification.gherkin` pointer does.
//
// # Sabotage steps and the SQL half of the denylist (pinned V1 clarification)
//
// gherkin-style.md §6 scopes SQL vocabulary to FACADE steps. The sabotage step
// family (§8 — `the Owner pool tries to …`, `the write is refused by …`, `the
// refusal names …`, `the write is allowed`) exists to bypass the application
// with raw writes, so its sentences legitimately carry SQL verbs ("the Owner
// pool tries to insert a ride"). Sabotage steps are therefore EXEMPT from the
// SQL-keyword half of the denylist. Everything else still applies to them: the
// transport words, the numeric status codes, and the glossary binding (whose
// seed declares the harness's own words like `insert` or `rides_state_gate`).
//
// The one phrase-level exception is a denylisted phrase glued inside a database
// object name (`rides_status_code_present_check`), and it is CONDITIONAL on the
// name being REAL (realObjectNames): a name is real when it is a value of some
// rule's `refusal.constraint` or `refusal.sentinel` in docs/spec/rules.yaml, or
// when it appears literally in a migrations/*.sql file. A fabricated name names
// no object the database has, so its fragments are the words the step said and
// the phrase fires, in a sabotage step as anywhere else. Written as words, the
// phrase always errors.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
	"gopkg.in/yaml.v3"
)

const (
	// policyVocabulary is the policy id every step-text finding is reported
	// under.
	policyVocabulary = "13-vocabulary"

	// glossaryFile is the seeded allowlist, relative to the repository root.
	glossaryFile = "docs/spec/glossary.yaml"

	// glossaryCommonSection is the shared section every module may use.
	glossaryCommonSection = "common"

	// catalogFile is the canonical catalog, relative to the repository root: the
	// §8 reality gate reads every rule's refusal names from it.
	catalogFile = "docs/spec/rules.yaml"

	// migrationsGlob matches the SQL migrations, relative to the repository
	// root: the second half of the §8 reality gate.
	migrationsGlob = "migrations/*.sql"
)

// denylistedTransportTokens are gherkin-style.md §6's transport words: they
// describe how a message is shaped, not what the business decides.
var denylistedTransportTokens = enum(
	"http", "https", "json", "request", "response", "header", "headers",
	"endpoint", "route", "handler", "dto", "payload",
)

// denylistedSQLTokens are the SQL words §6 forbids in facade steps: the
// DML/DDL surface a facade step would have to borrow to describe a query. The
// set is partitioned, because not every SQL word is only SQL (see
// sqlHomographTokens).
//
// Two keywords every SQL list carries are deliberately ABSENT: `from` and
// `where`. Both are ordinary English function words that business prose needs
// ("the activation token from her mail", "the field to change"), so a bare
// token match on them flags well-written facade steps rather than SQL. SQL
// shape in a step is still caught by every verb and object noun below; the
// denylist's job is to spot a step that reads like a query, not to ban English.
var denylistedSQLTokens = enum(
	"select", "insert", "update", "delete", "join", "create", "drop", "alter",
	"table", "index", "column", "check", "constraint", "trigger", "unique",
	"references", "grant", "revoke", "cascade", "upsert", "truncate",
)

// sqlHomographTokens is the HOMOGRAPH half of denylistedSQLTokens: words that
// are SQL vocabulary and ordinary English at once. `the check passes` and `each
// pair of teams is unique` are business prose, so a homograph alone may not
// error; it fires only on adjacency (isSQLAdjacency), which is what separates
// `the check passes` from `check constraint` and a lone `table` from `create
// table`. The complement of this set inside denylistedSQLTokens is the HARD
// keywords (`select`, `insert`, `update`, `delete`, `join`, `drop`, `alter`,
// `constraint`, `trigger`, `grant`, `revoke`, `cascade`, `upsert`, `truncate`),
// which have no English reading in this domain and still error alone.
var sqlHomographTokens = enum(
	"check", "unique", "index", "references", "create", "table", "column",
)

// sqlAdjacencyNouns are the database object nouns that make a neighbouring
// homograph SQL-shaped: `unique key`, `table row`, `column schema`. They are
// not SQL words themselves (a `key` is business data), so they never error;
// they only license the homograph beside them.
var sqlAdjacencyNouns = enum("key", "database", "schema", "row")

// denylistedPhrases are multi-word §6 bans, matched over the token stream so a
// quoted literal ("...") stays exempt. `code` alone is business vocabulary ("a
// permission code"); `status code` is the transport fact.
var denylistedPhrases = [][]string{
	{"status", "code"},
}

// sabotageStepPrefixes are gherkin-style.md §8's step shapes. A step matching
// one of them is a direct database write by the Owner pool and is exempt from
// the SQL half of the denylist (see the file comment).
var sabotageStepPrefixes = []string{
	"the owner pool",
	"the write is refused",
	"the write is allowed",
	"the refusal names",
}

// httpStatusLikePattern is §6's "numeric statuses (`200`, `401`, `409`, `422`,
// `500`)": a bare three-digit number in the HTTP range.
var httpStatusLikePattern = regexp.MustCompile(`^[1-5][0-9]{2}$`)

// quantityUnits are the units a three-digit figure legitimately quantifies in
// this domain, and the seed of the closed quantity-follower set (see
// isQuantityFollower). `100 tokens` is a stake, not a status; a figure with no
// unit ("refused as 409") is the transport fact §6 forbids.
var quantityUnits = enum(
	"tokens", "rounds", "matches", "minutes", "hours", "days", "seconds",
	"players", "teams", "members", "points",
)

// quantityNouns are the curated business nouns that complete the closed
// quantity-follower set. They are not units (`a token`, not `a token of`), but
// they are the countable things this domain's features quantify (`200 entries`,
// `200 rows`) — the real tree's follower audit, not a licence for any word.
var quantityNouns = enum("entries", "rows")

// statusNouns are the transport nouns a three-digit figure names when it is a
// status rather than a quantity: `409 status`, `409 code`, `409 error`,
// `409 response`, `409 request` all speak the wire, not the business. They are
// denied explicitly (and are outside the closed follower set anyway), so adding
// a noun to the set can never re-license them.
var statusNouns = enum("status", "code", "error", "response", "request")

var (
	// possessivePattern removes the English possessive suffix before
	// punctuation splitting, so `round 1's first match` reads `round 1 first
	// match` and `the ride's team` reads `the ride team`.
	possessivePattern = regexp.MustCompile(`['’]s\b`)
	// quotedLiteralPattern removes quoted literals: `"Spring 2026"`, `"2–1"`,
	// `"in_progress"` and `"ana@example.com"` are data, not vocabulary.
	quotedLiteralPattern = regexp.MustCompile(`"[^"]*"`)
	// tokenSplitPattern replaces everything that is not a letter, a digit or
	// an underscore with whitespace. Underscore is preserved because database
	// object names (`rides_state_gate`, `matches_ended_at_check`) are single
	// tokens: splitting them on `_` would turn every gate name into SQL
	// vocabulary. En/em dashes and typographic apostrophes are covered because
	// they are not ASCII letters.
	tokenSplitPattern = regexp.MustCompile(`[^a-z0-9_]+`)
	// pureNumberPattern is the exemption: figures carry no vocabulary.
	pureNumberPattern = regexp.MustCompile(`^[0-9]+$`)
	// glossaryTermPattern is what a glossary entry may look like: one token,
	// already lowercase.
	glossaryTermPattern = regexp.MustCompile(`^[a-z0-9_]+$`)
	// disguisedStatusPattern is §6's numeric status written with letters in
	// place of digits (`2oo` for `200`, `4O4` for `404`): EXACTLY three
	// characters, an HTTP-range leading digit (1–5) and then digits or the
	// letter `o` standing in for a zero. The shape is narrow on purpose. An
	// earlier version was "begins with a digit and mixes letters in", which
	// made every digit-leading word a transport figure: `1st`, `30min` and
	// `3d` were refused by disguiseReason and could never be declared, in a
	// glossary entry or in a step. Only a lookalike of the three-digit figure
	// is the disguise §6 forbids.
	disguisedStatusPattern = regexp.MustCompile(`(?i)^[1-5][0-9o]{2}$`)
)

// tokenizeStep is the pinned tokenizer, in order: lowercase; drop English
// possessives; drop quoted literals; replace every remaining non-word run with
// a space; split. Numbers are returned — callers that bind vocabulary skip them
// (they are exempt), and the status-code rule is the one place that looks at
// them.
func tokenizeStep(text string) []string {
	return tokenize(text, true)
}

// tokenizeDocString tokenizes a docstring body WITHOUT the quoted-literal
// exemption. A docstring is prose, not a list of data values: the quotes in it
// are characters, so a docstring that says `"json"` still says the transport
// word §6 forbids.
func tokenizeDocString(text string) []string {
	return tokenize(text, false)
}

// tokenize is the pinned tokenizer; dropQuoted switches the quoted-literal
// exemption (on for step lines and data-table cells, off for docstring bodies).
func tokenize(text string, dropQuoted bool) []string {
	lowered := strings.ToLower(text)
	lowered = possessivePattern.ReplaceAllString(lowered, "")
	if dropQuoted {
		lowered = quotedLiteralPattern.ReplaceAllString(lowered, " ")
	}
	fields := tokenSplitPattern.Split(lowered, -1)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

// splitTokenStream splits the tokenizer's tokens on `_`, dropping empty
// fields.
//
// The underscore-preserving stream is the one the glossary binding uses: a
// database object name (`rides_state_gate`) is a single declared word. The
// DENYLIST reads this split stream instead, because leaving `_` opaque would be
// a bypass hatch: `status_code` would escape the `status code` phrase, and
// `http_status` would escape the transport-word list. Splitting the names the
// sabotage family spells surfaces their fragments (`matches_ended_at_check` →
// `check`), which the §8 exemption covers only for REAL object names (see
// realObjectNames and the file comment).
func splitTokenStream(tokens []string) []string {
	split := make([]string, 0, len(tokens))
	for _, token := range tokens {
		for _, field := range strings.Split(token, "_") {
			if field != "" {
				split = append(split, field)
			}
		}
	}
	return split
}

// isQuantityFollower reports whether a token makes the preceding three-digit
// figure a QUANTITY rather than a status. The set is CLOSED: quantityUnits (the
// schema's units) plus quantityNouns (the curated business nouns, `entries`,
// `rows`). Any other follower leaves the figure a numeric status, which is what
// keeps §6's status ban shut: `100 tokens` and `200 entries` quantify, while
// `409 conflict`, `200 ok` and `404 not` are statuses again.
//
// The closure is the point. An earlier fix widened the follower to "any plain
// alphabetic word", so a curated-looking quantity check became a bypass hatch
// for any figure followed by prose, and bare three-digit figures passed again.
// A follower is either on the list or it is not.
//
// The edges stay conservative so the denied cases never loosen: a figure at the
// END of a stream is a status ("refused as 409"), and so is a figure followed
// by a transport noun ("409 status", "409 code") or by another number / a
// mixed token ("409 2", "409_2"), which quantifies nothing a feature may say.
func isQuantityFollower(token string) bool {
	if statusNouns[token] {
		return false
	}
	return quantityUnits[token] || quantityNouns[token]
}

// disguiseReason explains why a glossary term is disguised denylisted
// vocabulary rather than a word the features may say, or "" when the term is
// legitimate. The glossary is where the remedy message sends the author, so a
// term that merely re-spells a denylisted phrase or a transport word would
// launder the disguise permanently: `status_code` and `http_status` must never
// be addable.
//
// The SQL half of the denylist is deliberately NOT applied to glossary terms.
// The seed declares the §8 sabotage family's own SQL words (`insert`, `delete`)
// and the database object names those assertions spell
// (`rides_tokens_locked_check`, `round_lifecycle_blocks_delete`), which carry
// `check`/`delete` fragments by construction. Those names are exactly what the
// glossary is for (the pinned V1 exemption); a facade step that says one of
// them is still caught per step, because the SQL half is scoped to facade steps
// when the step is scanned.
func disguiseReason(term string) string {
	split := splitTokenStream([]string{term})
	for _, phrase := range denylistedPhrases {
		if !windowContains(split, phrase) {
			continue
		}
		// A term that IS the phrase re-spelled (`status_code`) is the disguise
		// this check exists to refuse. A term that merely CARRIES the fragments
		// among fragments of its own is an underscore-joined database object
		// name (`rides_status_code_present_check`), which gherkin-style.md §8
		// makes the glossary's legitimate cargo: the phrase is part of the name
		// the identifier spells, and where that name may appear is decided per
		// step by the sabotage exemption and its reality gate (realObjectNames),
		// not here.
		if len(split) > len(phrase) {
			continue
		}
		return fmt.Sprintf("it spells the denylisted phrase %q", strings.Join(phrase, " "))
	}
	for _, token := range split {
		if denylistedTransportTokens[token] {
			return fmt.Sprintf("it carries the transport word %q", token)
		}
	}
	if first, second, ok := concatenatedTransport(term); ok {
		return fmt.Sprintf("it concatenates the denylisted transport words %q + %q", first, second)
	}
	if disguisedStatusPattern.MatchString(term) {
		return "it is a numeric status written with letters in place of digits"
	}
	return ""
}

// concatenatedTransport reports whether the term is two denylisted TRANSPORT
// words written without the `_` that would let the split stream see them
// (`httpstatus` = `http` + `status`, `statuscode` = `status` + `code`), and
// returns the halves. Splitting on `_` closed the `status_code` hatch, but a
// concatenation carries no separator at all, so it has to be cut by hand.
//
// The halves vocabulary is the transport half only: denylistedTransportTokens ∪
// statusNouns. The SQL half is deliberately absent for the same reason it is
// absent from disguiseReason's other checks — the glossary legitimately declares
// the §8 sabotage family's database object names (`rides_tokens_locked_check`,
// `round_lifecycle_blocks_delete`), and a SQL word glued to another word is how
// those names are spelled.
func concatenatedTransport(term string) (first, second string, ok bool) {
	transportWord := func(word string) bool {
		return denylistedTransportTokens[word] || statusNouns[word]
	}
	for i := 1; i < len(term); i++ {
		if transportWord(term[:i]) && transportWord(term[i:]) {
			return term[:i], term[i:], true
		}
	}
	return "", "", false
}

// isSabotageStep reports whether the step is one of gherkin-style.md §8's
// Owner-pool shapes (see the file comment for why that matters).
func isSabotageStep(text string) bool {
	lowered := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range sabotageStepPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			return true
		}
	}
	return false
}

// glossary is the seeded allowlist: one section per module enum plus `common`.
type glossary struct {
	sections map[string]map[string]bool
}

// allows reports whether the token is declared for the module or shared.
func (g *glossary) allows(module, token string) bool {
	if g == nil {
		return false
	}
	return g.sections[module][token] || g.sections[glossaryCommonSection][token]
}

// section is the glossary section a module's step tokens are expected in.
func section(module string) string {
	if module == "" {
		return glossaryCommonSection
	}
	return module
}

// loadGlossary reads docs/spec/glossary.yaml and validates its shape: `common`
// plus one section per module enum value, each term a single lowercase token.
// A missing or malformed file is an ERROR and yields an empty glossary, so the
// vocabulary binding then names every undeclared token instead of quietly
// passing.
func (c *checker) loadGlossary() *glossary {
	g := &glossary{sections: map[string]map[string]bool{}}

	rel := glossaryFile
	data, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(rel)))
	if err != nil {
		c.errorf(policyVocabulary, rel,
			"glossary %q cannot be read: %v — the step vocabulary has no allowlist", rel, err)
		return g
	}
	var raw map[string][]string
	if err := yaml.Unmarshal(data, &raw); err != nil {
		c.errorf(policyVocabulary, rel, "glossary %q does not parse: %v", rel, err)
		return g
	}

	sections := make([]string, 0, len(raw))
	for name := range raw {
		sections = append(sections, name)
	}
	sort.Strings(sections)

	for _, name := range sections {
		switch {
		case name == glossaryCommonSection:
		case moduleEnum[name]:
		default:
			c.errorf(policyVocabulary, rel,
				"glossary section %q is neither %q nor a module enum value", name, glossaryCommonSection)
			continue
		}
		terms := map[string]bool{}
		for _, term := range raw[name] {
			if !glossaryTermPattern.MatchString(term) {
				c.errorf(policyVocabulary, rel,
					"glossary term %q in section %q is not a single lowercase token", term, name)
				continue
			}
			if reason := disguiseReason(term); reason != "" {
				c.errorf(policyVocabulary, rel,
					"glossary term %q in section %q is not a legitimate step word: %s", term, name, reason)
				continue
			}
			terms[term] = true
		}
		g.sections[name] = terms
	}

	// Every module owns a section, even where no feature file exists yet: the
	// glossary is the six-module map, and an empty section is the declaration
	// that nothing has been standardized there.
	for _, module := range sortedEnumValues(moduleEnum) {
		if _, ok := raw[module]; !ok {
			c.errorf(policyVocabulary, rel,
				"glossary has no %q section (one section per module enum value)", module)
		}
	}
	if _, ok := raw[glossaryCommonSection]; !ok {
		c.errorf(policyVocabulary, rel, "glossary has no %q section", glossaryCommonSection)
	}
	return g
}

// # The §8 reality set
//
// gherkin-style.md §8 promises that a sabotage assertion names a REAL database
// object, and rules.schema.md policy 13 exempts those names from the denylisted
// PHRASES. The set is built once per run (realObjectNames): a name is REAL when
// it is a value of some rule's `refusal.constraint` or `refusal.sentinel` in
// docs/spec/rules.yaml, or when it appears literally in a migrations/*.sql
// file. The loader is deliberately cheap (one catalog read, one glob) and
// silent on catalog-shape problems: those are other policies' findings, and
// reporting them here would double-report them.

// realObjectNames is the §8 reality gate's set of database object names.
type realObjectNames struct {
	// named holds the catalog half: every refusal.constraint and
	// refusal.sentinel value of every rule.
	named map[string]bool
	// migrations holds the migrations half: the text of every migrations/*.sql
	// file, in sorted path order. A name is real when it is a literal substring
	// of one of them.
	migrations []string
}

// loadRealObjectNames builds the §8 reality set for one checker run. A missing
// or unparseable catalog, and a missing migrations directory, yield the empty
// half: the policies that own the catalog's shape report those problems, and
// their absence here must not turn into a second finding.
func loadRealObjectNames(root string) *realObjectNames {
	real := &realObjectNames{named: map[string]bool{}}

	if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(catalogFile))); err == nil {
		var catalog Catalog
		if err := yaml.Unmarshal(data, &catalog); err == nil {
			for _, rule := range catalog {
				if rule.Refusal == nil {
					continue
				}
				for _, name := range rule.Refusal.Constraint {
					if name != "" {
						real.named[name] = true
					}
				}
				if sentinel := rule.Refusal.Sentinel; sentinel != nil && *sentinel != "" {
					real.named[*sentinel] = true
				}
			}
		}
	}

	paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(migrationsGlob)))
	if err != nil {
		return real
	}
	sort.Strings(paths)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		real.migrations = append(real.migrations, string(data))
	}
	return real
}

// has reports whether the name is a REAL database object: a catalog refusal
// name, or a literal substring of a migration.
func (r *realObjectNames) has(name string) bool {
	if r == nil || name == "" {
		return false
	}
	if r.named[name] {
		return true
	}
	for _, migrations := range r.migrations {
		if strings.Contains(migrations, name) {
			return true
		}
	}
	return false
}

// realNameCarriesPhrase reports whether every underscore-joined token whose
// fragments spell the phrase in the denylist's split stream is a REAL database
// object name. It is the reality condition on the §8 phrase exemption: a
// fabricated name falls back to the normal phrase pass.
func (c *checker) realNameCarriesPhrase(tokens, phrase []string) bool {
	carriers := phraseCarriers(tokens, phrase)
	if len(carriers) == 0 {
		return false
	}
	for _, carrier := range carriers {
		if !c.realObjects.has(carrier) {
			return false
		}
	}
	return true
}

// phraseCarriers returns the underscore-preserving tokens whose `_`-split
// fragments spell the phrase, one entry per fragment of a matching window. A
// window written as words has no glued carrier and contributes nothing (the
// caller's windowHasUnglued already reports it); a window cut out of one name
// contributes that name, so the reality gate judges the whole identifier, not
// one of its fragments.
func phraseCarriers(tokens, phrase []string) []string {
	if len(phrase) == 0 {
		return nil
	}
	scrutiny := splitTokenStream(tokens)
	owners := fragmentOwners(tokens)
	var carriers []string
	for i := 0; i+len(phrase) <= len(scrutiny); i++ {
		match := true
		for j, want := range phrase {
			if scrutiny[i+j] != want {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		glued := true
		for j := range phrase {
			if len(splitTokenStream([]string{tokens[owners[i+j]]})) < 2 {
				glued = false
				break
			}
		}
		if !glued {
			continue
		}
		for j := range phrase {
			carriers = append(carriers, tokens[owners[i+j]])
		}
	}
	return carriers
}

// fragmentOwners maps each index of the `_`-split stream back to the index of
// the underscore-preserving token it was cut out of — the carrier of the
// fragment. gluedFragments says WHETHER a fragment is glued; this says which
// name it belongs to, which is what the reality gate has to look up.
func fragmentOwners(tokens []string) []int {
	owners := make([]int, 0, len(tokens))
	for i, token := range tokens {
		for range splitTokenStream([]string{token}) {
			owners = append(owners, i)
		}
	}
	return owners
}

// collectVocabulary runs policy 13 over the feature tree.
func collectVocabulary(root string) []Finding {
	c := &checker{root: root, realObjects: loadRealObjectNames(root)}
	g := c.loadGlossary()
	c.checkStepVocabulary(g)
	sortFindings(c.findings)
	return c.findings
}

// checkStepVocabulary walks the executable scenarios and applies the denylist
// and the glossary binding to every step. Findings are deduplicated per
// `file::scenario` + message, so a Scenario Outline's rows report one finding
// for one undeclared word.
func (c *checker) checkStepVocabulary(g *glossary) {
	root := filepath.Join(c.root, filepath.FromSlash(featuresRoot))
	seen := map[string]bool{}
	report := func(pointer, msg string) {
		key := pointer + "\x00" + msg
		if seen[key] {
			return
		}
		seen[key] = true
		// The scenario pointer is the finding's subject: it is the same
		// `file::scenario` address a verification.gherkin pointer uses, so the
		// author can jump straight to the step.
		c.errorf(policyVocabulary, pointer, "%s", msg)
	}

	for _, full := range walkingFeatureFiles(root) {
		rel, err := filepath.Rel(c.root, full)
		if err != nil {
			rel = full
		}
		rel = filepath.ToSlash(rel)

		// An unreadable or unparseable feature is policy 12's finding; policy
		// 13 has no steps to read.
		file, err := os.Open(full)
		if err != nil {
			continue
		}
		ids := &messages.Incrementing{}
		doc, parseErr := gherkin.ParseGherkinDocument(file, ids.NewId)
		_ = file.Close()
		if parseErr != nil || doc.Feature == nil {
			continue
		}

		module, moduleOK := moduleOfFeature(rel)
		if !moduleOK {
			report(rel, fmt.Sprintf("feature file %q is not under %s/<module>/",
				rel, featuresRoot))
			continue
		}

		for _, pickle := range gherkin.Pickles(*doc, rel, ids.NewId) {
			pointer := rel + "::" + pickle.Name
			for _, step := range pickle.Steps {
				c.checkStep(report, g, module, pointer, step.Text, step.Argument)
			}
		}
	}
}

// checkStep applies both halves of policy 13 to one step: its text and its step
// argument (a docstring body or data-table cells — they are part of the step,
// so they carry the same rules and the same `file::scenario` attribution).
//
// The sabotage exemption is decided once, by the step's own text, and covers
// the whole step including its argument (the §8 family's docstring is a raw
// write too).
func (c *checker) checkStep(report func(pointer, msg string), g *glossary, module, pointer, text string, argument *messages.PickleStepArgument) {
	sabotage := isSabotageStep(text)
	c.scanVocabulary(report, g, module, pointer, tokenizeStep(text), sabotage)

	if argument == nil {
		return
	}
	if docString := argument.DocString; docString != nil {
		c.scanVocabulary(report, g, module, pointer, tokenizeDocString(docString.Content), sabotage)
	}
	if table := argument.DataTable; table != nil {
		for _, row := range table.Rows {
			for _, cell := range row.Cells {
				// A cell is a value: the quoted-literal exemption still applies
				// (`"Team A"` in a cell is data, as on the step line).
				c.scanVocabulary(report, g, module, pointer, tokenizeStep(cell.Value), sabotage)
			}
		}
	}
}

// scanVocabulary applies both halves of policy 13 to one token stream (a step
// line, a docstring body or a data-table cell): the denylist first, over the
// `_`-split stream (absolute for transport words and figures, adjacency-scoped
// for SQL homographs), then the glossary binding for the underscore-preserving
// tokens the denylist did not already name (one finding per problem step token
// is enough to act on).
func (c *checker) scanVocabulary(report func(pointer, msg string), g *glossary, module, pointer string, tokens []string, sabotage bool) {
	scrutiny := splitTokenStream(tokens)
	denied := map[string]bool{}
	deny := func(token, msg string) {
		denied[token] = true
		report(pointer, msg)
	}

	glued := gluedFragments(tokens)

	for i, token := range scrutiny {
		switch {
		case pureNumberPattern.MatchString(token):
			if !httpStatusLikePattern.MatchString(token) {
				continue
			}
			if i+1 < len(scrutiny) && isQuantityFollower(scrutiny[i+1]) {
				// The follower set is closed (quantityUnits ∪ quantityNouns):
				// `100 tokens` / `200 entries` quantify, and any other follower
				// leaves the figure the numeric status §6 forbids.
				continue
			}
			deny(token, fmt.Sprintf(
				"step carries the bare number %q: numeric statuses are transport vocabulary; say what the business decided instead (gherkin-style.md §6)",
				token))
		case disguisedStatusPattern.MatchString(token):
			// `2oo` is `200` wearing letters: the numeric-status rule must not
			// be escapable by respelling the figure, and the remedy is not "add
			// it to the glossary" — there is no word to add.
			deny(token, fmt.Sprintf(
				"step token %q is a numeric status written with letters in place of digits: numeric statuses are transport vocabulary; say what the business decided instead (gherkin-style.md §6)",
				token))
		case denylistedTransportTokens[token]:
			deny(token, fmt.Sprintf(
				"step uses %q, which describes the wire, not the decision (gherkin-style.md §6 transport vocabulary)",
				token))
		case denylistedSQLTokens[token] && !sabotage:
			// A homograph is English until a neighbour makes it SQL; a hard
			// keyword has no such reading and always fires. A fragment cut out
			// of an underscore-joined database name (`matches_ended_at_check`)
			// keeps firing: an identifier is SQL-shaped by its form, so `_` must
			// not become the bypass hatch fix A closed.
			if sqlHomographTokens[token] && !glued[i] && !isSQLAdjacency(scrutiny, i) {
				continue
			}
			deny(token, fmt.Sprintf(
				"step uses the SQL word %q (gherkin-style.md §6 forbids SQL vocabulary outside the sabotage family)",
				token))
		}
	}

	for _, phrase := range denylistedPhrases {
		if !windowContains(scrutiny, phrase) {
			continue
		}
		// gherkin-style.md §8: a REAL database object name
		// (`rides_status_code_present_check` style) may spell a denylisted
		// phrase with its `_`-glued fragments. The exemption is conditional on
		// the name being REAL (realObjectNames): a fabricated name names no
		// object the database has, so its fragments are the words the step
		// said and the phrase fires, in a sabotage step as anywhere else. A
		// phrase written as words always fires, sabotage step or not.
		if sabotage && !windowHasUnglued(scrutiny, glued, phrase) &&
			c.realNameCarriesPhrase(tokens, phrase) {
			continue
		}
		for _, token := range phrase {
			denied[token] = true
		}
		report(pointer, fmt.Sprintf(
			"step uses %q: numeric statuses and transport vocabulary are forbidden (gherkin-style.md §6)",
			strings.Join(phrase, " ")))
	}

	// A whole token whose split form is denied is already explained by the
	// denylist finding; offering the glossary remedy for it would invite the
	// author to launder the disguise (fix B rejects it anyway).
	for _, token := range tokens {
		for _, field := range splitTokenStream([]string{token}) {
			if denied[field] {
				denied[token] = true
				break
			}
		}
	}

	// The allowlist half: every token that is not a pure number and not already
	// denied must be declared for the module or in `common`.
	for _, token := range tokens {
		if pureNumberPattern.MatchString(token) || denied[token] || g.allows(module, token) {
			continue
		}
		report(pointer, fmt.Sprintf(
			"step token %q is not declared for module %q; add it to the %q section of %s (or to %q)",
			token, module, section(module), glossaryFile, glossaryCommonSection))
	}
}

// gluedFragments reports, per token of the `_`-split stream, whether it was cut
// out of an underscore-joined name (a database object name) rather than written
// as a word of its own. Such a fragment is SQL-shaped by the FORM of the name it
// belongs to, so the homograph adjacency rule does not apply to it: the split
// stream the denylist reads must not turn `matches_ended_at_check` back into an
// innocent `check`.
func gluedFragments(tokens []string) []bool {
	glued := make([]bool, 0, len(tokens))
	for _, token := range tokens {
		fields := splitTokenStream([]string{token})
		for range fields {
			glued = append(glued, len(fields) > 1)
		}
	}
	return glued
}

// isSQLAdjacency reports whether a token at index i has a neighbour (i±1 in the
// same stream) that makes a homograph SQL-shaped: SQL vocabulary of either kind,
// or a database object noun.
func isSQLAdjacency(tokens []string, i int) bool {
	for _, j := range []int{i - 1, i + 1} {
		if j < 0 || j >= len(tokens) {
			continue
		}
		if denylistedSQLTokens[tokens[j]] || sqlAdjacencyNouns[tokens[j]] {
			return true
		}
	}
	return false
}

// windowHasUnglued reports whether the phrase occurs in the split stream at a
// position that was written as a word rather than cut out of an
// underscore-joined name. It is what keeps the §8 object-name exemption narrow:
// `rides_status_code_present_check` is a name, while `the status code` is the
// phrase gherkin-style.md §6 forbids in every other step. The exemption is also
// conditional on the name being REAL — a fully glued window still reports the
// phrase when its carrier is fabricated (see scanVocabulary).
func windowHasUnglued(tokens []string, glued []bool, phrase []string) bool {
	if len(phrase) == 0 || len(tokens) < len(phrase) {
		return false
	}
	for i := 0; i+len(phrase) <= len(tokens); i++ {
		match := true
		unglued := false
		for j, want := range phrase {
			if tokens[i+j] != want {
				match = false
				break
			}
			if i+j >= len(glued) || !glued[i+j] {
				unglued = true
			}
		}
		if match && unglued {
			return true
		}
	}
	return false
}

// windowContains reports whether the token stream carries the phrase.
func windowContains(tokens, phrase []string) bool {
	if len(phrase) == 0 || len(tokens) < len(phrase) {
		return false
	}
	for i := 0; i+len(phrase) <= len(tokens); i++ {
		match := true
		for j, want := range phrase {
			if tokens[i+j] != want {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// sortedEnumValues returns an enum's values in ascending order.
func sortedEnumValues(m map[string]bool) []string {
	values := make([]string, 0, len(m))
	for v := range m {
		values = append(values, v)
	}
	sort.Strings(values)
	return values
}

// moduleOfFeature returns the module a feature file belongs to: the directory
// under docs/spec/features/. The module decides which glossary section its step
// vocabulary is bound to.
func moduleOfFeature(rel string) (string, bool) {
	trimmed := strings.TrimPrefix(rel, featuresRoot+"/")
	if trimmed == rel {
		return "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 || !moduleEnum[parts[0]] {
		return "", false
	}
	return parts[0], true
}
