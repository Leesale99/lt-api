@schedule
Feature: Removing calendar records
  The calendar can be tidied: a fixture announced by mistake, a round that never
  happened, a club that never played. History cannot be tidied — a record whose
  matches have started is durable — and nothing the game still leans on
  disappears under it: a club a player favours, a match someone rode, a season
  whose registrations still carry rides.

  A delete that is allowed takes the record's children with it, so the calendar
  never keeps a fixture whose round is gone. A season's delete is one act: it
  takes its registrations with it or it takes nothing.

  Background:
    Given a season is on the calendar
    And round 1 is on the calendar
    And round 2 is on the calendar
    And the teams "Team A", "Team B" and "Team C" are on the calendar

  # The calendar answers every refused delete with the same in-use conflict
  # (misalignment-report §6.4), so the scenarios assert the refusal and the
  # record that is still there rather than a name the caller never hears.
  @rule:R-SCHEDULE-023
  Rule: A record whose matches have started is durable

    Scenario: A round whose match has kicked off cannot be deleted
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      When the organiser deletes round 1
      Then the command is refused
      And round 1 is still on the calendar

    Scenario: A closed round cannot be deleted
      Given round 1 is open
      And the organiser has closed round 1
      When the organiser deletes round 1
      Then the command is refused
      And round 1 is still on the calendar

    Scenario: A live season cannot be deleted
      Given round 1 is open
      When the organiser deletes the season
      Then the command is refused
      And the season is still on the calendar

    Scenario: The database refuses to delete a round with a started match
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      When the Owner pool tries to delete round 1
      Then the write is refused by rounds_delete_gate
      And the refusal names round_lifecycle_blocks_delete

    Scenario: The database refuses to delete a live season
      Given round 1 is open
      When the Owner pool tries to delete the season
      Then the write is refused by seasons_delete_gate
      And the refusal names season_lifecycle_blocks_delete

  # A cascade cannot fail, so a direct writer is refused by nothing; the rule's
  # face is the children that are gone.
  @rule:R-SCHEDULE-024
  Rule: Deleting a season or a round takes its fixtures with it

    Scenario: Deleting a round takes its match day with it
      Given round 1 holds match day 1: "Team A" against "Team B"
      When the organiser deletes round 1
      Then round 1 is no longer on the calendar
      And match day 1 is no longer on the calendar

    Scenario: Deleting a season takes its rounds and fixtures with it
      Given round 1 holds match day 1: "Team A" against "Team B"
      And round 2 holds match day 2: "Team B" against "Team C"
      When the organiser deletes the season
      Then the season is no longer on the calendar
      And round 1 is no longer on the calendar
      And match day 2 is no longer on the calendar

  # A club's references sit on both sides of the context boundary: the fixtures
  # that name it are the calendar's own, while the players who favour it and the
  # rides on it are the game's, asked through the delete policy. The rule is one
  # decision at the calendar's delete, so it is proved there.
  @rule:R-SCHEDULE-025
  Rule: A team its fixtures keep cannot be deleted

    Scenario: A team a fixture names cannot be deleted
      Given round 1 holds match day 1: "Team A" against "Team B"
      When the organiser deletes the team "Team A"
      Then the command is refused
      And the team "Team A" is still on the calendar

    Scenario: A team nothing refers to can be deleted
      When the organiser deletes the team "Team C"
      Then the team "Team C" is no longer on the calendar

  # The references this rule asks about belong to the game context and are
  # answered through a port the calendar declares; the cross-context foreign
  # keys are gone, so a writer that bypasses the use case is refused by nothing
  # and there is no sabotage scenario.
  @rule:R-SCHEDULE-026
  Rule: A record the game still leans on is not deleted

    Scenario: A team a player still favours cannot be deleted
      Given the player "Ana" is registered in the season favouring "Team C"
      When the organiser deletes the team "Team C"
      Then the command is refused
      And the team "Team C" is still on the calendar

    Scenario: A match a ride still sits on cannot be deleted
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And the player "Ana" is registered in the season with 100 tokens
      And a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the organiser deletes match day 1
      Then the command is refused
      And match day 1 is still on the calendar

    Scenario: A season whose registration still carries a ride cannot be deleted
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And the player "Ana" is registered in the season with 100 tokens
      And a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the organiser deletes the season
      Then the command is refused
      And the player "Ana" is still registered in the season

    Scenario: A refused season delete leaves its registrations untouched
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      And the player "Ana" is registered in the season with 100 tokens
      When the organiser deletes the season
      Then the command is refused
      And the season is still on the calendar
      And the player "Ana" is still registered in the season

  @rule:R-SCHEDULE-027
  Rule: Deleting a season also deletes its players

    Scenario: A deleted season takes its registrations with it
      Given the player "Ana" is registered in the season with 100 tokens
      When the organiser deletes the season
      Then the season is no longer on the calendar
      And the player "Ana" is no longer registered in the season

  @rule:R-SCHEDULE-028
  Rule: An edit lands only on the version the editor was shown

    Scenario: An edit written against an outdated reading is refused
      Given the organiser has read the team "Team A"
      And another editor has since changed the description of the team "Team A"
      When the organiser saves a new description for the team "Team A"
      Then the command is refused as a stale reading
      And the team "Team A" keeps the other editor's description

    Scenario: An edit from the reading the editor now holds lands
      Given the organiser has read the team "Team A"
      And another editor has since changed the description of the team "Team A"
      And the organiser re-reads the team "Team A"
      When the organiser saves a new description for the team "Team A"
      Then the team "Team A" carries the organiser's description
