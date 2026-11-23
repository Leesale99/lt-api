# League Tokens — Progressive Backend Learning Path

This directory contains the learning-oriented implementation plan for League Tokens. Its
two tracked domain documents are:

- `domain-knowledge.md` — the design reference: the player-facing Game Design document, the source of truth for gameplay behavior.
- `domain-knowledge-game-engine.md` — the game engine specification: season and match lifecycles, Ride behavior, and the engine state definitions the system must protect.

The phase guides are deliberately simpler than the production-oriented system-design document —
`system-design.md`, the "System architecture and technical design" entry in
`domain-knowledge.md`'s source-of-truth table. That document is planned and not yet written;
until it exists, the two domain references above (`domain-knowledge.md` and
`domain-knowledge-game-engine.md`) are the current source of truth. The phase guides
introduce one backend concept at a time and state where prior knowledge helps.

## Learning sequence

```text
Phase 0  HTTP/API foundation   (prior backend experience helpful)
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

- microservices and Kubernetes
- Kafka/NATS and Redis
- gRPC and SSE infrastructure
- advanced identity/refresh-token architecture
- a dormant market module
- a generic dependency-injection framework
- a large Clean Architecture / Hexagonal folder hierarchy

Those can be explored later once the monolith has real pressures that justify them.

## What this path does not cover

The guides teach backend concepts in this repo's order; Go syntax, deployment and
operations stay out, and the two domain documents above remain the gameplay source of
truth rather than something a phase guide restates.
