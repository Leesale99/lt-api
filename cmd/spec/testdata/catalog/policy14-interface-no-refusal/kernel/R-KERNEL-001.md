---
id: R-KERNEL-001
title: A thing always is a thing
statement: |-
  A thing always is. It is never almost.
module: kernel
entity: thing
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/thing/thing.go::Do
    layer: handler
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
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

Policy 14 fixture: an `interface` rule that names no refusal. It frames a request, so the category says it must name the status the framed-away request is refused with; there is nothing else wrong with the entry.
