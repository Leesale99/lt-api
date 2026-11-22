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
knowledge-ref: docs/spec/README.md
stale_pointer: null
tie: null
---

## Notes

Policy 16 fixture: knowledge-ref points outside the learning-phase tree, so the note behind the rule is not a source-of-truth document.
