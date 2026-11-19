@game
Feature: A registration (a season entry)
  A registration is one member's entry in one season: the team she follows
  and the balance she plays with start here. It belongs to the member who
  made it — nobody registers on someone else's behalf, and nobody edits or
  withdraws a season entry that is not hers, unless she acts as an
  administrator. A member holds at most one registration per season, so she
  can never play with two balances.

  A registration also reaches outside the game: its season, its team and the
  member behind it live in other parts of the product, and those references are
  checked when the registration is written rather than by the database. An edit
  may claim the version it read, so a change made from a stale reading is
  refused instead of quietly overwriting the newer one.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the teams "Team A", "Team B" and "Team C" play in "Spring 2026"
    And the player "Ana" is registered by her member in "Spring 2026" with 100 tokens
    And round 1 holds match day 1: "Team A" against "Team B"

  @rule:R-GAME-012
  Rule: A registration belongs to the member who makes it, in the season she names

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

  # A withdrawal the database refuses is still a named refusal: the registration
  # is referenced by its rides, so the caller is told that rather than only that
  # the server failed (misalignment-report §6 item 2, fixed).
  @rule:R-GAME-035
  Rule: A registration its rides still name cannot be withdrawn

    Scenario: A player still carrying rides is refused as a registration in use
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the owner withdraws her registration
      Then the withdrawal is refused as a registration still in use
      And the registration still exists for "Ana" in "Spring 2026"

    # The refusal the caller reads is the adapter's copy; the rule itself is the
    # rides' reference, so a writer that skips the adapter is refused the same
    # way.
    Scenario: The database refuses to withdraw a registration its rides name
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the Owner pool tries to delete "Ana"'s registration
      Then the write is refused by rides_player_id_fkey

  @rule:R-GAME-013
  Rule: Another context's records are checked when the game writes a reference to them

    Scenario: A registration naming a team the calendar does not carry is refused
      When the second member registers in "Spring 2026" favouring a team the calendar does not carry
      Then the refusal names the favourite team as the field to change
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

    # One entry per member per season is the key's word, not the adapter's: a
    # writer that skips the duplicate check is refused by the same key.
    Scenario: The database refuses a second registration for the same member
      When the Owner pool tries to register "Ana" a second time in the season
      Then the write is refused by players_season_id_user_id_key

  @rule:R-GAME-017
  Rule: A registration edit may claim the version it read

    Scenario: An edit claiming the version it read lands
      When the owner changes her registration to favour "Team C", claiming the version she last read
      Then the registration has "Team C" as its favourite team

    Scenario: An edit claiming a version the registration has moved past is refused as a stale reading
      Given the owner's registration has moved to "Team B" since she last read it
      When the owner changes her registration to favour "Team C", claiming the version she last read
      Then the command is refused as a stale reading
      And the registration still has "Team B" as its favourite team
