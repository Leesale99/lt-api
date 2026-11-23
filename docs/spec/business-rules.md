<!-- GENERATED — do not edit. -->
# Business rules — dashboard

> **GENERATED — do not edit.** This dashboard is derived from the per-rule files
> under `docs/spec/rules`: one authored Markdown file per rule. Those files are the
> canonical catalog; the dashboard is only a generated summary over them.
>
> Never hand-edit the dashboard. Change the rule file, then regenerate with
> `make spec/gen` (or the command below):

```sh
go run ./cmd/spec gen --rules docs/spec/rules --out docs/spec/business-rules.md
```

The catalog's schema, enums and conventions live in `rules.schema.md`;
the catalog's own invariants are checked by `go run ./cmd/spec check`.

## Dashboard

**126 rules** across 6 modules — 4 drifted, 103 implemented, 19 specified-only.

### By status

| Status | Rules |
| --- | ---: |
| `drifted` | 4 |
| `implemented` | 103 |
| `specified-only` | 19 |
| **total** | **126** |

### By module

| Module | Rules |
| --- | ---: |
| `game` | 33 |
| `identity` | 22 |
| `kernel` | 17 |
| `ledger` | 7 |
| `platform` | 17 |
| `schedule` | 30 |
| **total** | **126** |

### Rule files

Every rule is authored in its own file, `<module>/<ID>.md`, under `docs/spec/rules`.

| Module | Rules | Files |
| --- | ---: | --- |
| `game` | 33 | [game](rules/game/) |
| `identity` | 22 | [identity](rules/identity/) |
| `kernel` | 17 | [kernel](rules/kernel/) |
| `ledger` | 7 | [ledger](rules/ledger/) |
| `platform` | 17 | [platform](rules/platform/) |
| `schedule` | 30 | [schedule](rules/schedule/) |
| **total** | **126** | |
