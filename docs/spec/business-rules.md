<!-- GENERATED — do not edit. -->
# Business rules — dashboard

> **GENERATED — do not edit.** This file is the dashboard and index over the
> per-module views in `views/`. Both are readable views of the canonical,
> hand-edited catalog `docs/spec/rules.yaml`.
>
> Change the YAML, never these files, then regenerate:

```sh
go run ./cmd/spec gen --rules docs/spec/rules.yaml --out docs/spec/business-rules.md
```

The catalog's schema, enums and conventions live in `rules.schema.md`;
the catalog's own invariants are checked by `go run ./cmd/spec check`.

## Dashboard

**126 rules** across 6 modules — 107 implemented, 19 specified-only.

### By status

| Status | Rules |
| --- | ---: |
| `implemented` | 107 |
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

### Module views

| Module | Rules | View |
| --- | ---: | --- |
| `game` | 33 | [`views/game.md`](views/game.md) |
| `identity` | 22 | [`views/identity.md`](views/identity.md) |
| `kernel` | 17 | [`views/kernel.md`](views/kernel.md) |
| `ledger` | 7 | [`views/ledger.md`](views/ledger.md) |
| `platform` | 17 | [`views/platform.md`](views/platform.md) |
| `schedule` | 30 | [`views/schedule.md`](views/schedule.md) |
| **total** | **126** | |
