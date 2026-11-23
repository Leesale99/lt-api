---
id: R-KERNEL-001
title: A store-stamped fact is stamped by the store
statement: |-
  The row's identity is the store's business, never the caller's.
module: kernel
entity: thing
category: persistence-fact
status: implemented
authority: repository
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/thing/thing.go::Do
    layer: repository
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit: []
  e2e:
    - internal/kernel/thing/thing_test.go::TestDo
  gherkin: []
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Policy 14 fixture: a `persistence-fact` rule proved only by an e2e test. A store-stamped fact is proved by a Go unit test: the category binds the proof.
