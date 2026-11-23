package main

// finding.go owns the one shape every policy reports through. A Finding is one
// policy violation (or WARN line) attributed to a subject: the rule id the
// finding is about, or the empty string for a catalog-level fact that belongs to
// no rule (a broken glossary, a feature file outside a module, an annotation
// that resolves to nothing). The subject is what --module/--rule scoping filters
// on (scope.go): a rule-attributed finding is narrowed to its rule, while a
// catalog-level finding is always reported — at WARN while scoped, so global rot
// is visible without failing a worker's --strict gate.

// severity orders the two report levels. A WARN never fails --strict.
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

// Finding is one policy violation (or WARN line) for one subject.
type Finding struct {
	Policy string
	// Subject is the rule id the finding is about, or "" when the finding is
	// catalog-level (it names no rule, so no scope can implicate it).
	Subject string
	// Pointer is the human-addressed location the finding is read at
	// (`file::scenario` for a step, a file path for a broken file), when the
	// policy has one that is not the subject. It is presentation only: scoping
	// reads Subject.
	Pointer  string
	Severity severity
	Message  string
}
