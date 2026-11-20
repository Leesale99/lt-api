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

// quantityUnits are the business nouns a three-digit figure legitimately
// quantifies in this domain. `100 tokens` is a stake, not a status; a figure
// with no unit ("refused as 409") is the transport fact §6 forbids. The seed is
// the examples the schema states; `isQuantityFollower` generalises it to any
// plain business word ("200 entries").
var quantityUnits = enum(
	"tokens", "rounds", "matches", "minutes", "hours", "days", "seconds",
	"players", "teams", "members", "points",
)

// statusNouns are the transport nouns a three-digit figure names when it is a
// status rather than a quantity: `409 status`, `409 code`, `409 error`,
// `409 response`, `409 request` all speak the wire, not the business.
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
	// place of digits (`2oo` for `200`, `4O4` for `404`): a token that is not
	// all digits but begins with a digit and mixes letters in. It is a
	// transport figure, never a word a feature may declare.
	disguisedStatusPattern = regexp.MustCompile(`^[0-9]+[0-9a-z]*[a-z][0-9a-z]*$`)
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
// `check`), which the §8 exemption covers because those names appear only in
// sabotage steps (see the file comment).
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
// figure a QUANTITY rather than a status. The rule is deliberately narrow: a
// plain alphabetic word that is not a transport noun. `100 tokens` and `200
// entries` quantify; the seeded business nouns are the fast path, and the rule
// generalises the seed to any business word.
//
// The edges stay conservative so the denied cases never loosen: a figure at the
// END of a stream is a status ("refused as 409"), and so is a figure followed
// by a transport noun ("409 status", "409 code") or by another number / a
// mixed token ("409 2", "409_2"), which quantifies nothing a feature may say.
func isQuantityFollower(token string) bool {
	if statusNouns[token] {
		return false
	}
	if quantityUnits[token] {
		return true
	}
	if token == "" {
		return false
	}
	for _, r := range token {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
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
		if windowContains(split, phrase) {
			return fmt.Sprintf("it spells the denylisted phrase %q", strings.Join(phrase, " "))
		}
	}
	for _, token := range split {
		if denylistedTransportTokens[token] {
			return fmt.Sprintf("it carries the transport word %q", token)
		}
	}
	if disguisedStatusPattern.MatchString(term) {
		return "it is a numeric status written with letters in place of digits"
	}
	return ""
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

// collectVocabulary runs policy 13 over the feature tree.
func collectVocabulary(root string) []Finding {
	c := &checker{root: root}
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
				continue // `100 tokens` / `200 entries` quantify; not a status
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
