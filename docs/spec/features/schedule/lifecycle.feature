@schedule
Feature: Season and round lifecycle
  A season is the fixture list, a round is one match day of it, and both walk
  the same small vocabulary forward: created, open, in progress for a season,
  closed for either. Once a match has kicked off nothing above it moves
  backwards — the calendar's history is durable from the first whistle.

  The calendar also refuses to be a step ahead of itself. A round only goes
  live under a live season and behind an unbroken sequence of closed rounds, and
  it only closes once every fixture in it has a result. Opening a round starts
  its season, and closing the thirty-eighth round is the season's own last step.

  Background:
    Given a season is on the calendar
    And round 1 is on the calendar
    And round 2 is on the calendar
    And the teams "Team A", "Team B" and "Team C" are on the calendar

  @rule:R-SCHEDULE-003
  Rule: A schedule lifecycle never moves backwards

    Scenario: A closed match cannot go back to running
      Given round 1 is open
      And match day 1 has been played out "2–1"
      When the Owner pool tries to send the closed match day 1 back to "in_progress"
      Then the write is refused by matches_freeze_gate
      And the refusal names match_status_regression

    Scenario: A closed round with nothing played cannot reopen
      Given round 1 is open
      And the organiser has closed round 1
      When the Owner pool tries to send round 1 back to "open"
      Then the write is refused by rounds_progress_gate
      And the refusal names round_status_regression

    Scenario: A live season cannot step back to open
      Given round 1 is open
      When the Owner pool tries to send the season back to "open"
      Then the write is refused by seasons_freeze_gate
      And the refusal names season_status_regression

    # The rank half runs first inside the match gate, so a closed match that
    # would also trip the started-match freeze reports the lifecycle break.
    Scenario: A closed match sent back to created reports the rank, not the freeze
      Given round 1 is open
      And match day 1 has been played out "2–1"
      When the Owner pool tries to send the closed match day 1 back to "created"
      Then the write is refused by matches_freeze_gate
      And the refusal names match_status_regression

    Scenario: A closed match keeps its status without a transition
      Given round 1 is open
      And match day 1 has been played out "2–1"
      When the Owner pool rewrites the closed match day 1 without changing its status
      Then the write is allowed

  @rule:R-SCHEDULE-004
  Rule: The status vocabularies are closed sets

    Scenario Outline: A status the league never defined cannot be stored
      Given round 1 holds match day 1: "Team A" against "Team B"
      When the Owner pool tries to set <the record> to the status "cancelled"
      Then the write is refused by <constraint>

      Examples:
        | the record  | constraint           |
        | the season  | seasons_status_check |
        | round 2     | rounds_status_check  |
        | match day 1 | matches_status_check |

  @rule:R-SCHEDULE-005
  Rule: A first whistle makes the calendar's history durable

    Scenario: A started match cannot be postponed
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      When the organiser postpones match day 1
      Then the command is refused as a started match
      And match day 1 still reads created

    Scenario: The database refuses to postpone a started match
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      When the Owner pool tries to postpone match day 1
      Then the write is refused by matches_freeze_gate
      And the refusal names started_match_blocks_status_regression

    # For a round the started gate and the rank gate are two triggers, and the
    # started one fires first — which is the refusal a bypassing writer hears.
    Scenario: The database refuses to send a round with a started match back
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 has kicked off
      When the Owner pool tries to send round 1 back to "created"
      Then the write is refused by rounds_freeze_gate
      And the refusal names started_match_blocks_round_regression

  # The calendar answers every refused lifecycle write with one shared conflict
  # (misalignment-report §6.4), so the scenarios below assert the refusal and the
  # row that did not move rather than a name the caller never hears.
  @rule:R-SCHEDULE-015
  Rule: A round goes live only under a live season and in order

    Scenario: The database refuses a round opened behind an unclosed predecessor
      Given the organiser has declared the season open
      When the Owner pool tries to open round 2
      Then the write is refused by rounds_progress_gate
      And the refusal names round_sequence_incomplete

    Scenario: The database refuses a round opened under a season that has not started
      When the Owner pool tries to open round 1
      Then the write is refused by rounds_progress_gate
      And the refusal names round_progress_season_not_live

    Scenario: The organiser cannot open a round ahead of the sequence
      Given the organiser has declared the season open
      When the organiser opens round 2
      Then the command is refused
      And round 2 still reads created
      And the season still reads open

  @rule:R-SCHEDULE-016
  Rule: A round closes only over fixtures with results

    Scenario: A round with a fixture still to play cannot close
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      When the organiser closes round 1
      Then the command is refused
      And round 1 still reads open

    Scenario: The database refuses a close over a match still running
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      When the Owner pool tries to close round 1 with match day 1 still running
      Then the write is refused by rounds_close_gate
      And the refusal names round_close_has_unclosed_matches

  # Opening a round is dispatched to the calendar's own open transaction, which
  # flips the season before the round; a writer that bypasses that dispatch is
  # refused by nothing (a round open under a merely open season satisfies every
  # gate), so this rule has no sabotage scenario.
  @rule:R-SCHEDULE-017
  Rule: Opening a round starts its season

    Scenario: Opening round 1 starts the season
      Given the organiser has declared the season open
      When the organiser opens round 1
      Then round 1 reads open
      And the season reads in progress

    Scenario: Opening round 2 keeps the season running
      Given round 1 is open
      And the organiser has closed round 1
      When the organiser opens round 2
      Then round 2 reads open
      And the season still reads in progress

  # The last-round predicate and the season's flip sit in one repository
  # statement; a writer that bypasses it closes the round and leaves the season
  # running, and no database gate refuses that — so this rule has no sabotage
  # scenario.
  @rule:R-SCHEDULE-018
  Rule: Closing the last round closes the season

    Scenario: Closing round thirty-eight closes the season
      Given the season's fixture list runs to thirty-eight rounds
      And rounds 1 to 37 have opened and closed in order
      And round 38 is open
      When the organiser closes round 38
      Then round 38 reads closed
      And the season reads closed

    Scenario: Closing a round before the last leaves the season running
      Given the season's fixture list runs to thirty-eight rounds
      And rounds 1 to 36 have opened and closed in order
      And round 37 is open
      When the organiser closes round 37
      Then round 37 reads closed
      And the season reads in progress
