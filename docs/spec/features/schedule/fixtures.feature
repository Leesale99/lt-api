@schedule
Feature: The fixture calendar
  A fixture is an announcement: these two teams meet, at this hour, in this
  round, at these odds. The calendar is strict about what an announcement may
  say — the kickoff must still be ahead, the two teams must differ, the round
  must belong to the fixture's own season, the price must be above evens —
  because the rest of the product reads these fixtures as facts: the phase
  windows, the bonus arithmetic, the settlement.

  A match carries a score exactly while it is being played or finished, and only
  a finished match has an end. The database holds those shapes so that no
  writer, however it arrives, leaves a half-scored or half-closed fixture
  behind, and it stamps the end once, keeping the one the resolve flow supplies.

  Background:
    Given a season is on the calendar
    And round 1 is on the calendar
    And round 2 is on the calendar
    And the teams "Team A", "Team B" and "Team C" are on the calendar

  @rule:R-SCHEDULE-002
  Rule: A fixture is announced for a kickoff that is still ahead

    Scenario Outline: A fixture cannot be announced for a kickoff that has arrived
      When the organiser schedules match day 1 in round 1: "Team A" against "Team B" kicking off <when>
      Then the command is refused as a kickoff that has already arrived
      And the calendar holds no match day 1

      Examples:
        | when            |
        | an hour ago     |
        | at this instant |

    Scenario: A fixture announced for later on is on the calendar
      When the organiser schedules match day 1 in round 1: "Team A" against "Team B" kicking off in two hours
      Then match day 1 is on the calendar
      And match day 1 reads created

    # The fixture is in play, not merely past its kickoff: the started-freeze
    # gate refuses every write to a created match whose kickoff has passed
    # (R-SCHEDULE-005), so the editable fixture is the one already under way.
    Scenario: A fixture keeps being editable after its kickoff has passed
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      When the organiser corrects match day 1's kickoff to three hours ago
      Then match day 1 is announced to kick off three hours ago

  @rule:R-SCHEDULE-006
  Rule: A score exists exactly while a match is playing or finished

    Scenario Outline: The database refuses a score that does not fit the match
      When the Owner pool tries to store a match day that is <the shape>
      Then the write is refused by <database object>

      Examples:
        | the shape                                      | database object              |
        | created with a full-time score of 2–1          | matches_status_score_check   |
        | postponed with a full-time score of 2–1        | matches_status_score_check   |
        | in progress with no score at all               | matches_status_score_check   |
        | in progress with only the home half of a score | matches_score_complete_check |
        | in progress with a home score of minus one     | matches_home_score_check     |
        | in progress with an away score of minus two    | matches_away_score_check     |

  @rule:R-SCHEDULE-007
  Rule: A finished match records when it ended

    Scenario Outline: The database refuses an end that does not fit the match
      When the Owner pool tries to store a match day that is <the shape>
      Then the write is refused by matches_ended_at_check

      Examples:
        | the shape                                        |
        | closed with no end at all                        |
        | in progress with an end time                     |
        | closed with an end that falls before its kickoff |

  # The end is a write the database performs on the way into closed, not a
  # predicate: nothing is refused, so there is no refusal to name and the
  # scenarios read the end back instead.
  @rule:R-SCHEDULE-008
  Rule: A match's end is written once, and the resolve flow's end is kept

    Scenario: The end the organiser resolved with is the end the calendar keeps
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      When the organiser resolves match day 1 as "2–1" ending twenty minutes ago
      Then match day 1 reads closed
      And match day 1 keeps the end it was resolved with

    Scenario: A finished match keeps its end when the result is corrected
      Given round 1 is open
      And match day 1 has been played out "2–1"
      When the organiser corrects match day 1's score to "2–2"
      Then match day 1 reads "2–2"
      And match day 1 keeps the end it was resolved with

    Scenario: A writer that supplies no end gets the calendar's own clock
      Given round 1 is open
      And match day 1 is under way
      When the Owner pool tries to close match day 1 without an end time
      Then the write is allowed
      And match day 1 records the calendar's own end

  # Only the bound is a refusal. The two places are a stored width, not a
  # predicate — an odds of 1.234 arrives rounded to 1.23 — so these scenarios
  # prove the price above evens.
  @rule:R-SCHEDULE-009
  Rule: Odds are a price above evens

    Scenario Outline: The database refuses a fixture priced at evens or below
      Given round 1 holds match day 1: "Team A" against "Team B" at odds 1.80
      When the Owner pool tries to price match day 1 at <the price> for the <the side> side
      Then the write is refused by <database object>

      Examples:
        | the price | the side | database object         |
        | 1.00      | home     | matches_home_odds_check |
        | 1.00      | away     | matches_away_odds_check |
        | 0.90      | home     | matches_home_odds_check |

    Scenario: The organiser cannot price a fixture at evens
      Given round 1 holds match day 1: "Team A" against "Team B" at odds 1.80
      When the organiser prices match day 1 at 1.00 for the home side
      Then the command is refused as a price that is not above evens
      And match day 1 keeps a home price of 1.80

  @rule:R-SCHEDULE-010
  Rule: A team never plays itself

    Scenario: The organiser cannot announce a team against itself
      When the organiser schedules match day 1 in round 1: "Team A" against "Team A"
      Then the command is refused as the same team on both sides
      And the calendar holds no match day 1

    Scenario: The database refuses a fixture with one team on both sides
      When the Owner pool tries to store a match day with "Team A" on both sides
      Then the write is refused by matches_teams_differ_check

  @rule:R-SCHEDULE-011
  Rule: A fixture belongs to a round of its own season

    Scenario: The database refuses a fixture moved into another season
      Given round 1 holds match day 1: "Team A" against "Team B"
      And another season is on the calendar
      When the Owner pool tries to move match day 1 into another season
      Then the write is refused by matches_season_id_round_id_fkey

  @rule:R-SCHEDULE-013
  Rule: A round's number lies between one and thirty-eight

    Scenario Outline: The database refuses a round numbered outside the fixture list
      When the Owner pool tries to create a round numbered <number>
      Then the write is refused by rounds_number_check

      Examples:
        | number |
        | 0      |
        | 39     |

    Scenario: The last number in the fixture list is storable
      When the Owner pool tries to create a round numbered 38
      Then the write is allowed

  @rule:R-SCHEDULE-014
  Rule: A season has one round per number

    Scenario: The organiser cannot add a second round 1
      When the organiser adds round 1 to the season
      Then the command is refused as a round number already taken
      And the season holds one round 1

    Scenario: The same number in another season is a different round
      Given another season is on the calendar
      When the organiser adds round 1 to the other season
      Then the other season holds a round 1

    Scenario: The database refuses a duplicate round number
      When the Owner pool tries to duplicate round 1 in the season
      Then the write is refused by rounds_season_id_number_key

  # A club is the third kind of record the calendar carries, and its shape is
  # the same kind of promise as a fixture's: a described club or no club, never
  # a half-described one — for the organiser's screen and for a writer that
  # skips it alike.
  @rule:R-SCHEDULE-029
  Rule: A team carries a name, a badge and a description

    Scenario Outline: The database refuses a team with a part of its record missing
      When the Owner pool tries to record a team with <the gap>
      Then the write is refused by <database object>

      Examples:
        | the gap        | database object         |
        | no name        | teams_name_check        |
        | an empty badge | teams_logo_check        |
        | no description | teams_description_check |
