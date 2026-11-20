// Command spec generates the business-rule catalog's Markdown view and checks
// the catalog against its own invariants (docs/spec/rules.schema.md §3).
//
// It is deliberately thin (the standard library, yaml.v3 for the catalog and the
// gherkin parser for the feature files): the catalog is a documentation
// artifact, not a service, and the tool must run on any checkout.
package main

import (
	"fmt"
	"os"
)

const usage = `usage: spec <command> [flags]

Commands:
  gen    generate the Markdown view of docs/spec/rules.yaml
  check  validate the catalog against docs/spec/rules.schema.md §3
  brief  print a Markdown work order for one rule (R-XXX-NNN) or one module
  prove  run the DB-backed mutant registry for the rules a change touches

Run "spec <command> -h" for the command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "gen":
		os.Exit(runGen(os.Args[2:], os.Stdout, os.Stderr))
	case "check":
		os.Exit(runCheck(os.Args[2:], os.Stdout, os.Stderr))
	case "brief":
		os.Exit(runBrief(os.Args[2:], os.Stdout, os.Stderr))
	case "prove":
		os.Exit(runProve(os.Args[2:], os.Stdout, os.Stderr))
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usage)
	default:
		fmt.Fprintf(os.Stderr, "spec: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
}
