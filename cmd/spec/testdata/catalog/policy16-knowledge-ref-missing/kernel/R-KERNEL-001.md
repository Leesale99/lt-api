---
id: R-KERNEL-001
title: A thing always is a thing
statement: |-
  A thing always is. It is never almost.
module: kernel
entity: thing
category: orchestration
status: implemented
authority: service
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/thing/thing.go::Do
    layer: service
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/kernel/thing/thing_test.go::TestDo
  e2e: []
  gherkin: []
expert-ref: null
knowledge-ref: docs/league-tokens-learning-phases/phase-99.md
stale_pointer: null
tie: null
---

## Notes

Policy 16 fixture: knowledge-ref sits under the learning-phase tree but names a note that does not exist.
