# lt-api

**League Tokens API** — a backend service for a game token system, built as a progressive learning project. The goal is not to ship fast, but to build deep understanding of backend fundamentals one phase at a time.

## Tech stack

- **Go** (1.27)
- `net/http` standard library (routing/mux)
- `log/slog` for structured logging
- [`julienschmidt/httprouter`](https://github.com/julienschmidt/httprouter)

## Learning path

Implementation follows the phased plan in [`docs/league-tokens-learning-phases/`](docs/league-tokens-learning-phases/README.md):

0. HTTP/API foundation
1. PostgreSQL persistence
2. Domain model & state machines
3. Transactions & atomic operations
4. Concurrency & idempotency
5. Modular monolith boundaries
6. Ledger & accounting authority
7. Background workers
8. Transactional outbox & events

Each phase introduces one major backend concept only when the system has a concrete reason to need it. Things deliberately deferred: microservices, Kubernetes, message brokers, Redis, gRPC, and heavy architectural frameworks.

## Project layout

```
cmd/server/        Entry point: config flags, logger, server bootstrap
internal/api/      HTTP layer: application struct, routes, handlers
docs/              Learning-oriented implementation plan (phases 0–8)
```
