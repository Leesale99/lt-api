package main

// finding.go owns the one shape every policy reports through. A Finding is one
// policy violation (or WARN line) attributed to a subject: the rule id it names,
// a feature pointer (`path::scenario`) when the feature tree itself is the
// problem, or an annotation site. The subject is what --module/--rule scoping
// filters on (scope.go): a catalog-level finding carries no subject and is
// reported only unscoped.

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
	Policy   string
	Subject  string
	Severity severity
	Message  string
}
