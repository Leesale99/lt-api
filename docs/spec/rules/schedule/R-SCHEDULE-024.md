---
id: R-SCHEDULE-024
title: Deleting a season or a round takes its matches with it
statement: |
  Removing a season removes its rounds and their matches; removing a round
  removes its matches. Calendar children never outlive their parent, so no
  match can point at a round or season that is gone.
module: schedule
entity: match
category: invariant
status: implemented
authority: database
mechanism: constraint
write_scope: cross-table
enforcement:
  - site: migrations/000001_create_initial_game_models.up.sql
    layer: db-constraint
    role: authoritative
    pinned_by: null
refusal: null
fixture: null
verification:
  unit:
    - internal/schedule/internal/repository/constraints_test.go::TestMatchConstraints
  e2e:
    - internal/e2e/rounds_test.go::TestDeleteOpenRoundCascadesMatches
    - internal/e2e/seasons_test.go::TestDeleteCreatedSeasonHandler
  gherkin:
    - docs/spec/features/schedule/deletion.feature::Deleting a season takes its rounds and fixtures with it
    - docs/spec/features/schedule/deletion.feature::A direct delete of the season takes its fixtures with it
expert-ref: null
knowledge-ref: null
stale_pointer: null
tie: null
---

## Notes

The cascade is three FK behaviours doing the work (rounds_season_id_fkey and matches_season_id_fkey / matches_round_id_fkey, all ON DELETE CASCADE), so it is a declarative constraint, not a decision anyone makes: the delete gates (R-SCHEDULE-023) decide *whether* the parent may go, and once it may, the children follow with no code. The composite FK (season_id, round_id) -> rounds (season_id, id) is what makes the two paths agree: a season delete cannot leave a match whose round id survived, because the composite key ties the match's season copy to the round's own. No refusal of its own — a cascade cannot fail; the only e2e-visible risk is a partial delete, which the two tests rule out by counting rows afterwards. The unit pointer is the closest existing probe (TestMatchConstraints's "round from another season rejected" case proves the composite key exists); no test deletes a *season* in the repository suite.
