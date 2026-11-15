@game
Feature: A registration (a season entry)
  A registration is one person's entry in one season: the team they follow
  and the balance they play with start here. It belongs to the member who
  made it — nobody registers on someone else's behalf, and nobody edits or
  withdraws a season entry that is not theirs, unless they act as an
  administrator. A person holds at most one registration per season, so one
  person can never play with two balances.

  A registration also reaches outside the game: its season, its team and the
  member behind it live in other contexts, and those references are checked
  when the registration is written rather than by the database. An edit may
  claim the version it read, so a change made from a stale reading is
  refused instead of quietly overwriting the newer one.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the teams "Team A", "Team B" and "Team C" play in "Spring 2026"
    And the player "Ana" is registered by her member in "Spring 2026" with 100 tokens
    And round 1 holds match day 1: "Team A" against "Team B"

  @rule:R-GAME-012
  Rule: A registration belongs to the member who makes it, in the season they name

    Scenario: A registration made in a season belongs to the member who made it
      When the second member registers in "Spring 2026" favouring "Team B"
      Then the registration belongs to the second member
      And the registration sits in "Spring 2026"

  @rule:R-GAME-011
  Rule: A registration is managed by its owner or an administrator

    Scenario: The owner changes her own registration
      When the owner changes her registration to favour "Team C"
      Then the registration has "Team C" as its favourite team

    Scenario: A change to someone else's registration is refused
      When the second member changes "Ana"'s registration to favour "Team C"
      Then the command is refused as someone else's registration
      And the registration still has "Team A" as its favourite team

    Scenario: The administrator changes any registration
      When the administrator changes "Ana"'s registration to favour "Team C"
      Then the registration has "Team C" as its favourite team

    Scenario: The owner withdraws her own registration
      When the owner withdraws her registration
      Then no registration exists for "Ana" in "Spring 2026"

    Scenario: Withdrawing someone else's registration is refused
      When the second member withdraws "Ana"'s registration
      Then the command is refused as someone else's registration
      And the registration still exists for "Ana" in "Spring 2026"

    Scenario: The administrator withdraws any registration
      When the administrator withdraws "Ana"'s registration
      Then no registration exists for "Ana" in "Spring 2026"

    # Defect 2 (misalignment-report §6): a removal the database itself refuses is
    # not translated into a named refusal — the caller learns only that something
    # failed, never that the player still carries rides.
    @known-defect:2
    Scenario: A player still carrying rides is refused without a named reason
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the owner withdraws her registration
      Then the refusal is unnamed for the caller
      And the registration still exists for "Ana" in "Spring 2026"

  @rule:R-GAME-013
  Rule: Another context's records are checked when the game writes a reference to them

    Scenario: A registration naming a team the calendar does not carry is refused
      When the second member registers in "Spring 2026" favouring a team the calendar does not carry
      Then the registration is refused on its favourite team
      And no registration exists for the second member in "Spring 2026"

    Scenario: A registration in a season the calendar does not carry is refused
      When the second member registers in a season the calendar does not carry
      Then the registration is refused as an unknown season
      And the second member holds no registration

    Scenario: A ride on a team the calendar does not carry is refused
      Given it is 3 hours before round 1's first match
      When the player locks 10 tokens on a team the calendar does not carry for match day 1
      Then the command is refused as an unknown team
      And no ride exists for "Ana" on match day 1

  @rule:R-GAME-014
  Rule: One registration per member per season

    Scenario: A second registration in the same season is refused as a duplicate
      When the owner registers again in "Spring 2026" favouring "Team B"
      Then the registration is refused as a duplicate
      And "Ana" still holds one registration in "Spring 2026"

  @rule:R-GAME-017
  Rule: A registration edit may claim the version it read

    Scenario: An edit claiming the version it read lands
      When the owner changes her registration to favour "Team C", claiming the version as it read when she last looked
      Then the registration has "Team C" as its favourite team

    Scenario: An edit claiming a version the registration has moved past is refused as a conflict
      Given the owner's registration has moved to "Team B" since she last looked at it
      When the owner changes her registration to favour "Team C", claiming the version as it read when she last looked
      Then the command is refused as a conflict
      And the registration still has "Team B" as its favourite team
