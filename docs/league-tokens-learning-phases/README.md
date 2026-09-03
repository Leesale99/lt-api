# League Tokens — Progressive Backend Learning Path

This directory contains the learning-oriented implementation plan for League Tokens.

The phase guides are deliberately simpler than the production-oriented `backend_system_design.md`. They are designed to build gently on the knowledge from *Let's Go Further* and introduce one major backend concept at a time.

## Learning sequence

```text
Let's Go Further
      |
      v
Phase 0  HTTP/API foundation
      |
      v
Phase 1  PostgreSQL persistence
      |
      v
Phase 2  Domain model + state machines
      |
      v
Phase 3  Transactions + atomic operations
      |
      v
Phase 4  Concurrency + idempotency
      |
      v
Phase 5  Modular monolith boundaries
      |
      v
Phase 6  Ledger + accounting authority
      |
      v
Phase 7  Background workers
      |
      v
Phase 8  Transactional outbox + events
```

## Scope philosophy

The game engine specification remains the source of truth for gameplay behavior. The large backend design document is treated as a future destination, not a checklist to implement immediately.

At every phase, resist introducing a concept merely because it appears in a more advanced architecture. Add it when the current system gives you a concrete reason to need it.

## What is intentionally deferred

The learning path does not initially require:

- microservices
- Kubernetes
- Kafka/NATS
- Redis
- gRPC
- SSE infrastructure
- advanced identity/refresh-token architecture
- a dormant market module
- a generic dependency-injection framework
- a large Clean Architecture / Hexagonal folder hierarchy

Those can be explored later once the monolith has real pressures that justify them.
