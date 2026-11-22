package main

// policies.go owns the one ordered policy registry: every invariant from
// docs/spec/rules.schema.md §3 is one Policy in `policies`, in numeric order.
// The table is the only dispatch site — collection, report ordering, the
// feature/gherkin section and the summary table all derive from it — so adding
// a policy means adding one row, never editing a second list.
//
// A policy's Level classifies where its findings live. Presentation follows it
// (feature-level findings print in the gherkin section), and from B2.2 scoped
// reporting follows it too: rule-level findings are narrowed by
// --module/--rule while catalog-level and feature-level findings are always
// reported. Scopeable says separately whether a finding's subject is a rule id
// a scope can match, which the cross-cutting collectors do not all provide yet.

// Level is where a policy's findings live.
type Level int

const (
	// LevelCatalog policies read the whole repository (Go sources, features,
	// glossary) and report against file/scenario subjects or a rule the tree
	// names; they are not narrowed by a rule scope.
	LevelCatalog Level = iota
	// LevelRule policies verify one catalog rule at a time.
	LevelRule
	// LevelFeature policies cross-validate the catalog against the feature
	// tree; their report home is the gherkin section.
	LevelFeature
)

// Policy is one numbered invariant from docs/spec/rules.schema.md §3.
type Policy struct {
	ID    string
	Name  string
	Level Level
	// Scopeable reports whether the policy's findings name a rule id that
	// --module/--rule can narrow.
	Scopeable bool
	// Run collects the policy's findings. It is pure apart from reading the
	// repository.
	Run func(env *policyEnv) []Finding
}

// policyEnv carries the inputs every policy Run needs, plus the shared
// feature-tree parse the two feature-level policies would otherwise repeat.
type policyEnv struct {
	catalog   Catalog
	root      string
	rulesPath string

	features    *featureIndex
	gherkin     []Finding
	gherkinDone bool
}

// featureIndex returns the parsed feature tree, building it if no feature-level
// policy has run yet.
func (e *policyEnv) featureIndex() *featureIndex {
	if e.features == nil {
		e.features = (&checker{root: e.root}).indexFeatures()
	}
	return e.features
}

// gherkinFor runs the feature cross-validation once and returns one policy's
// share of it. Both feature-level policies read the same parse: the tree is
// indexed once, and its findings are filtered by the policy that owns them.
func (e *policyEnv) gherkinFor(policyID string) []Finding {
	if !e.gherkinDone {
		c := newChecker(e)
		index := c.indexFeatures()
		c.checkGherkinCatalog(e.catalog, index)
		sortFindings(c.findings)
		e.features, e.gherkin, e.gherkinDone = index, c.findings, true
	}
	return policyFindings(e.gherkin, policyID)
}

// newChecker prepares a checker with the catalog-wide fixture set policy 11
// resolves pinned_by against.
func newChecker(env *policyEnv) *checker {
	c := &checker{root: env.root, fixtures: map[string]bool{}}
	for _, rule := range env.catalog {
		if rule.Fixture != nil && *rule.Fixture != "" {
			c.fixtures[*rule.Fixture] = true
		}
	}
	return c
}

// runRulePolicy is the shape every rule-level policy shares: one checker, one
// pass over the catalog, sorted findings.
func runRulePolicy(env *policyEnv, visit func(c *checker, rule Rule)) []Finding {
	c := newChecker(env)
	for _, rule := range env.catalog {
		visit(c, rule)
	}
	sortFindings(c.findings)
	return c.findings
}

// policyFindings returns the findings one policy contributed, preserving the
// sorted order.
func policyFindings(findings []Finding, policyID string) []Finding {
	group := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Policy == policyID {
			group = append(group, f)
		}
	}
	return group
}

// policies is the registry. Order is the report order and the summary-table
// order; ids are the ones every finding carries.
var policies = []Policy{
	{
		ID: "1-id", Name: "id format, uniqueness, flat per module",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			seen := map[string]bool{}
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkID(rule, seen) })
		},
	},
	{
		ID: "2-enums", Name: "frozen enum membership",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkEnums(rule) })
		},
	},
	{
		ID: "3-implemented", Name: "implemented ⇒ sites and verification that resolve today",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkImplemented(rule) })
		},
	},
	{
		ID: "4-specified-only", Name: "specified-only ⇒ expert-ref, no sites, no verification, no refusal",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkSpecifiedOnly(rule) })
		},
	},
	{
		ID: "5-drifted", Name: "drifted ⇒ notes; stale-pointer only on implemented rules",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkDrifted(rule) })
		},
	},
	{
		ID: "6-authority", Name: "database ⇒ mechanism + scope; any other authority ⇒ no mechanism",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkAuthority(rule) })
		},
	},
	{
		ID: "7-copy-policy", Name: "non-authoritative sites tied by pinned_by or a tie note",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkCopyPolicy(rule) })
		},
	},
	{
		ID: "8-authority-default", Name: "authority ≠ category default ⇒ notes",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkAuthorityDefault(rule) })
		},
	},
	{
		ID: "9-site-ref", Name: "path::Symbol, never a bare path or a line number",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkSiteReferences(rule) })
		},
	},
	{
		ID: policyGherkinPointer, Name: "verification.gherkin resolves to a real scenario",
		Level: LevelFeature, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return env.gherkinFor(policyGherkinPointer)
		},
	},
	{
		ID: "11-fixture", Name: "pinned_by names a declared fixture",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkFixtureRefs(rule) })
		},
	},
	{
		ID: policyGherkinFeature, Name: "feature files parse; every scenario names exactly one known rule",
		Level: LevelFeature, Scopeable: false,
		Run: func(env *policyEnv) []Finding {
			return env.gherkinFor(policyGherkinFeature)
		},
	},
	{
		ID: policyVocabulary, Name: "step text: denylisted transport tokens, SQL adjacency, and the seeded glossary",
		Level: LevelCatalog, Scopeable: false,
		Run: func(env *policyEnv) []Finding {
			return collectVocabulary(env.catalog, env.root)
		},
	},
	{
		ID: policyCategory, Name: "category ⇒ verification and refusal binding",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkCategory(rule) })
		},
	},
	{
		ID: policyAnnotations, Name: "Go enforcement sites carry //rule: annotations, and every annotation resolves",
		Level: LevelCatalog, Scopeable: false,
		Run: func(env *policyEnv) []Finding {
			return collectAnnotationsAgainst(env.catalog, env.root, env.rulesPath)
		},
	},
	{
		ID: policyKnowledgeRef, Name: "knowledge-ref is a real path under the learning phases",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkKnowledgeRef(rule) })
		},
	},
	{
		ID: policyRefusal, Name: "refusal code sanity",
		Level: LevelRule, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return runRulePolicy(env, func(c *checker, rule Rule) { c.checkRefusal(rule) })
		},
	},
	{
		ID: policyOracle, Name: "scenarios assert an observable outcome, bound to their category",
		Level: LevelCatalog, Scopeable: true,
		Run: func(env *policyEnv) []Finding {
			return collectOracle(env.catalog, env.root)
		},
	},
}

// policyOrder is derived from the table in init: it is the report's ordering,
// never a second hand-maintained list. It is filled after variable
// initialization because the policies' Run closures reach sortFindings, which
// reads it.
var policyOrder map[string]int

func init() {
	policyOrder = make(map[string]int, len(policies))
	for i, p := range policies {
		policyOrder[p.ID] = i
	}
}

// featureLevelFindings collects every LevelFeature policy's findings in table
// order. Presentation selects by Level, never by policy id.
func featureLevelFindings(grouped map[string][]Finding) []Finding {
	var out []Finding
	for _, p := range policies {
		if p.Level == LevelFeature {
			out = append(out, grouped[p.ID]...)
		}
	}
	return out
}
