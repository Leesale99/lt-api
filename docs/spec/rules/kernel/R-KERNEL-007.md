---
id: R-KERNEL-007
title: A list request is bounded, and its sort never becomes client SQL
statement: |
  A list request selects a bounded window of results — at least one row, at most one
  hundred per page, and a page number small enough to be addressable — and it may sort
  only by a column the resource publishes. A sort name outside that list is refused as
  a malformed request rather than handed to the database.
module: kernel
entity: list_query
category: interface
status: implemented
authority: handler
mechanism: null
write_scope: null
enforcement:
  - site: internal/kernel/persistence/filters.go::ValidateFilters
    layer: validator
    role: authoritative
    pinned_by: null
  - site: internal/kernel/persistence/filters.go::Filters.SortColumn
    layer: repository
    role: defense-in-depth
    pinned_by: null
refusal:
  code: synthetic
  constraint: null
  sentinel: null
  http: 422
fixture: null
verification:
  unit: null
  e2e:
    - internal/e2e/teams_test.go::TestListTeamsHandler
    - internal/e2e/rides_test.go::TestListRidesHandler
  gherkin: null
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: |-
  the panicking SortColumn is the loud half — if a new query path built its ORDER BY from an unvalidated sort it panics instead of interpolating, so the copy cannot drift silently in the dangerous direction; the safelist arrays (SortSafelistNameID / SortSafelistID) are shared, not copied per handler.
---

## Notes

Two halves of one framing rule (the handler applies the defaults DefaultPage/DefaultPageSize = 1/20 from the kernel's tuning file, the bounds are page > 0 and ≤ 10,000,000, page_size 1..100, sort from the per-resource safelist). VERIFY GAP: neither ValidateFilters, CalculateMetadata nor ReadIDParam has a unit test; the bounds are pinned only through two list handler e2e suites, and the panic path is untested.
