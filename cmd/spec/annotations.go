package main

// annotations.go implements checker policy 15 (`15-annotations`,
// docs/spec/rules.schema.md §3): the catalog and the Go tree name each other at
// the enforcement site, in both directions.
//
//   - CATALOG → TREE. Every Go enforcement site named by an implemented rule
//     carries an annotation for that rule at the declaration, so a reader of the
//     code can see which business rules the function enforces.
//   - TREE → CATALOG. Every `//rule:` annotation anywhere in the tree resolves
//     to a catalog rule id. An annotation whose id does not even match
//     `^R-[A-Z]+-\d{3}$` is a typo and an ERROR of its own, so a mistyped id
//     cannot masquerade as "a rule that does not exist yet".
//
// # The convention (pinned)
//
// An annotation line is exactly `//rule:R-XXX-NNN` — the prefix, the id, and
// nothing else (no space after the colon, no trailing prose; one id per line).
// It sits either on the declaration line or in the contiguous comment block
// directly above it. A blank line or a non-comment line breaks contiguity, so a
// comment block separated from the declaration by a blank line documents
// something else. A site named by several rules carries one `//rule:` line per
// id in that same block (the sweep writes them stacked, column 0, under the doc
// comment).
//
// gofmt normalizes a doc comment's `//rule:` to `// rule:` (the Go 1.19
// comment reflow), so the space-inserted spelling is the same annotation and is
// recognized too; the id after the colon must still be exact, which keeps
// `// rule: R-X-001` (a space after the colon) a typo.
//
// SQL sites (paths starting `migrations/`) and test files are exempt from
// carrying annotations: a migration's own text is the record, and a test names
// its rule through `verification`, not through an annotation. Test files are
// still scanned by the reverse direction — an annotation in a test must resolve.
//
// Sources are parsed with go/parser, not searched line by line: a `//rule:`
// inside a string literal or a doc sentence is not an annotation.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// policyAnnotations is the policy id every annotation finding is reported
	// under.
	policyAnnotations = "15-annotations"

	// ruleAnnotationPrefix opens an annotation. The id follows immediately;
	// gofmtRuleAnnotationPrefix is the same annotation after gofmt's doc-comment
	// reflow has inserted the space.
	ruleAnnotationPrefix      = "//rule:"
	gofmtRuleAnnotationPrefix = "// rule:"
)

// skippedGoDirs are never scanned: `vendor` is third-party code the catalog
// never names, and `testdata` is fixture data the Go toolchain does not compile
// either (the checker's own red-first fixtures live there, deliberately
// unresolvable).
var skippedGoDirs = enum("vendor", "testdata")

// ruleAnnotationPattern is the id an annotation must carry, exactly.
var ruleAnnotationPattern = regexp.MustCompile(`^R-[A-Z]+-\d{3}$`)

// siteRequirement is one Go enforcement site that must carry an annotation.
type siteRequirement struct {
	ruleID string
	site   string
	path   string
	symbol string
}

// collectAnnotations runs policy 15 over the tree. It is the only policy that
// reads Go sources rather than catalog pointers, so it is called next to
// collectVocabulary rather than from collectFindings.
func collectAnnotations(catalog Catalog, root string) []Finding {
	c := &checker{root: root}

	ruleIDs := make(map[string]bool, len(catalog))
	naming := map[string][]siteRequirement{} // site path → requirements
	for _, rule := range catalog {
		ruleIDs[rule.ID] = true
		if rule.Status != statusImplemented {
			continue
		}
		// A malformed id cannot be annotated: the annotation would have to be
		// malformed too, and policy 1 already fails the rule. Skipping it keeps
		// one catalog mistake from cascading into a second finding per site.
		if !idPattern.MatchString(rule.ID) {
			continue
		}
		for _, site := range rule.Enforcement {
			req, ok := annotationRequirement(rule.ID, site.Site)
			if !ok {
				continue // policy 3 / 9 report the malformed site
			}
			naming[req.path] = append(naming[req.path], req)
		}
	}

	files := map[string]goSource{}
	for _, source := range scanGoFiles(c, root) {
		files[source.path] = source
	}

	c.checkAnnotationsResolve(files, ruleIDs)
	c.checkSitesAnnotated(files, naming)
	sortFindings(c.findings)
	return c.findings
}

// goSource is one parsed Go file: the tree it hangs from, its comment groups
// and the declaration each doc comment belongs to.
type goSource struct {
	path  string
	fset  *token.FileSet
	file  *ast.File
	decls map[string]declaration
}

// declaration is one annotated-able declaration: its position (for narrowing a
// trailing comment) and the comment group directly above it (go/ast leaves Doc
// nil when a blank line separates the comment from the declaration, which is
// exactly the pinned contiguity rule).
type declaration struct {
	doc     *ast.CommentGroup
	start   token.Pos
	end     token.Pos
	endLine int
}

// annotationRequirement turns a catalog site into what the forward check needs:
// the site's path and the symbol whose declaration must be annotated. Sites
// outside Go (SQL migrations) and test files are exempt; sites that are not
// `path::Symbol` are policy 9's finding.
func annotationRequirement(ruleID, site string) (siteRequirement, bool) {
	if strings.HasPrefix(site, "migrations/") {
		return siteRequirement{}, false
	}
	path, symbol, ok := splitPointer(site)
	if !ok || isLineNumber(symbol) {
		return siteRequirement{}, false
	}
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return siteRequirement{}, false
	}
	return siteRequirement{ruleID: ruleID, site: site, path: path, symbol: symbol}, true
}

// scanGoFiles walks the repository, skipping vendor/testdata and hidden
// directories, and parses every Go file it finds.
func scanGoFiles(c *checker, root string) []goSource {
	var sources []goSource
	walkErr := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner of the tree is not a catalog finding
		}
		name := entry.Name()
		if entry.IsDir() {
			if full == root {
				return nil
			}
			if skippedGoDirs[name] || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, full)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		data, readErr := os.ReadFile(full)
		if readErr != nil {
			c.errorf(policyAnnotations, rel, "Go source %q cannot be read: %v", rel, readErr)
			return nil
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, rel, data, parser.ParseComments|parser.SkipObjectResolution)
		if parseErr != nil {
			// Unparseable code fails the build long before this policy; the
			// checker has nothing to say about the annotations inside it.
			return nil
		}
		sources = append(sources, goSource{path: rel, fset: fset, file: file, decls: declarationsOf(file, fset)})
		return nil
	})
	if walkErr != nil {
		c.errorf(policyAnnotations, "", "cannot walk the tree for Go sources: %v", walkErr)
	}
	return sources
}

// declarationsOf indexes the top-level declarations a catalog site can name:
// functions, methods (under both `Type.Name` and `Name`), types, constants and
// variables.
func declarationsOf(file *ast.File, fset *token.FileSet) map[string]declaration {
	decls := map[string]declaration{}
	record := func(name string, doc *ast.CommentGroup, start, end token.Pos) {
		if name == "" || name == "_" {
			return
		}
		decls[name] = declaration{doc: doc, start: start, end: end, endLine: fset.Position(end).Line}
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil && len(d.Recv.List) == 1 {
				record(receiverName(d.Recv.List[0].Type)+"."+d.Name.Name, d.Doc, d.Pos(), d.End())
			}
			record(d.Name.Name, d.Doc, d.Pos(), d.End())
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					record(s.Name.Name, d.Doc, d.Pos(), d.End())
				case *ast.ValueSpec:
					for _, name := range s.Names {
						record(name.Name, d.Doc, d.Pos(), d.End())
					}
				}
			}
		}
	}
	return decls
}

// receiverName returns the bare type name of a method receiver (`*Handler` →
// `Handler`, `pkg.T` → `T`).
func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	case *ast.IndexListExpr:
		return receiverName(t.X)
	}
	return ""
}

// annotationIDsOf returns the ids a comment group's annotation lines carry.
func annotationIDsOf(group *ast.CommentGroup) []string {
	if group == nil {
		return nil
	}
	var ids []string
	for _, comment := range group.List {
		ids = append(ids, commentAnnotationIDs(comment.Text)...)
	}
	return ids
}

// commentAnnotationIDs returns the verbatim ids a comment annotates. The pinned
// form is `//rule:R-X-001`; gofmt's normalized `// rule:R-X-001` is the same
// annotation. The text must start with the prefix, so a `//rule:` mentioned
// inside prose (or inside a string literal, which the parser would not present
// as a comment) is not an annotation. The id is taken exactly as written, so a
// space after the colon or trailing prose is the reverse check's typo finding.
func commentAnnotationIDs(text string) []string {
	for _, prefix := range []string{ruleAnnotationPrefix, gofmtRuleAnnotationPrefix} {
		if strings.HasPrefix(text, prefix) {
			return []string{strings.TrimRight(text[len(prefix):], " \t\r")}
		}
	}
	return nil
}

// checkAnnotationsResolve is the tree → catalog direction: every annotation
// names a catalog rule, and every annotation's id is well-formed.
func (c *checker) checkAnnotationsResolve(files map[string]goSource, ruleIDs map[string]bool) {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		source := files[path]
		for _, group := range source.file.Comments {
			for _, id := range annotationIDsOf(group) {
				line := source.fset.Position(group.Pos()).Line
				if !ruleAnnotationPattern.MatchString(id) {
					c.errorf(policyAnnotations, fmt.Sprintf("%s:%d", path, line),
						"%s:%d carries //rule:%s, which is not an id; an annotation is exactly //rule:R-<MODULE>-<NNN>",
						path, line, id)
					continue
				}
				if !ruleIDs[id] {
					c.errorf(policyAnnotations, id,
						"%s:%d carries //rule:%s, which names no rule in the catalog", path, line, id)
				}
			}
		}
	}
}

// checkSitesAnnotated is the catalog → tree direction: every implemented rule's
// Go site carries that rule's annotation at the declaration.
func (c *checker) checkSitesAnnotated(files map[string]goSource, naming map[string][]siteRequirement) {
	paths := make([]string, 0, len(naming))
	for path := range naming {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		source, ok := files[path]
		if !ok {
			continue // policy 3 reports a site whose file does not exist
		}

		requirements := naming[path]
		sort.SliceStable(requirements, func(i, j int) bool {
			if requirements[i].symbol != requirements[j].symbol {
				return requirements[i].symbol < requirements[j].symbol
			}
			return requirements[i].ruleID < requirements[j].ruleID
		})

		for _, req := range requirements {
			decl, ok := source.decls[req.symbol]
			if !ok {
				// symbolExists already accepted the symbol (policy 3), so a
				// declaration this index cannot attribute is not worth a second
				// error.
				continue
			}
			present := map[string]bool{}
			for _, id := range annotationIDsOf(decl.doc) {
				present[id] = true
			}
			// A trailing comment on the declaration line is the other accepted
			// placement (`func Do() {} //rule:R-X-001`).
			for _, group := range source.file.Comments {
				line := source.fset.Position(group.Pos()).Line
				if line != source.fset.Position(decl.start).Line && line != decl.endLine {
					continue
				}
				for _, id := range annotationIDsOf(group) {
					present[id] = true
				}
			}
			if present[req.ruleID] {
				continue
			}
			c.errorf(policyAnnotations, req.ruleID,
				"enforcement site %q carries no //rule:%s annotation on or above the declaration (%s:%d)",
				req.site, req.ruleID, path, source.fset.Position(decl.start).Line)
		}
	}
}
