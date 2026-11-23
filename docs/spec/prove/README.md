# docs/spec/prove — the prove registry, one file per module

Each `<module>.yaml` here is the prove registry for that module — one file per module that
has provable rules today (game, identity, schedule; modules enter as their rules become
executable). It maps a rule id to an entry naming the executable scenarios that attempt
the rule's bypass, or a git-attributed signoff when no executable scenario exists.
Entries are added on demand — a rule enters prove only when a change touches it.

One entry:

    R-SCHEDULE-003:
      scenarios:                 # one or more `<feature-file>::<scenario name>`
        - docs/spec/features/schedule/lifecycle.feature::A closed match cannot go back to running
      signoff: "Name <email> <commit-sha>"   # optional; only for UNVALIDATABLE
      notes: "..."               # optional, free text

The file stem is the module: `game.yaml` holds `R-GAME-*` ids only, and a rule id
declared in two files is an error. `spec prove` reads the whole directory by
default (`--registry` still accepts a legacy single file). Entry schema, verdicts
and scope rules: [`docs/spec/README.md` §7](../README.md#7-prove).
