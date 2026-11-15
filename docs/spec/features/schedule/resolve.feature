@schedule
Feature: Resolving a match
  A match ends when the referee says so, not when the clock does: the calendar is
  told the final score and the real end time, checks that the end has already
  happened, and writes the two facts once. From that moment the fixture is
  closed, and its result is what the rest of the product settles on.

  A result is written by a caller holding the version it read, so two people
  scoring the same match cannot both win. The calendar's phase windows — action
  until an hour before the first whistle, match until an hour after the last,
  decision afterwards — are read off these same start and end times, which is
  why an instant landing exactly on an edge already belongs to the later phase.

  Background:
    Given a season is on the calendar
    And round 1 is on the calendar
    And round 2 is on the calendar
    And the teams "Team A", "Team B" and "Team C" are on the calendar
    And the player "Ana" is registered in the season with 100 tokens

  @rule:R-SCHEDULE-020
  Rule: A result is accepted only for an end that has happened

    Scenario Outline: A result ending in the future is refused
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      When the organiser resolves match day 1 as "2–1" ending <when>
      Then the command is refused as an end that has not happened yet
      And match day 1 still reads in progress

      Examples:
        | when            |
        | in an hour      |
        | at this instant |

    Scenario: A result ending in the past is accepted
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      When the organiser resolves match day 1 as "2–1" ending twenty minutes ago
      Then match day 1 reads closed

  # A refused resolve reaches the caller as one conflict whatever stopped it, so
  # the scenarios assert the refusal and the match that kept its state wherever
  # the cause is not distinguishable.
  @rule:R-SCHEDULE-021
  Rule: A result is written once, by the caller holding the current version

    Scenario: A second result for the same match is refused
      Given round 1 is open
      And match day 1 has been played out "2–1"
      When the organiser resolves match day 1 as "3–2" ending ten minutes ago
      Then the command is refused
      And match day 1 keeps its score of "2–1"

    Scenario: A result written against an outdated reading is refused
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And match day 1 is under way
      And another edit to match day 1 has landed since the organiser read it
      When the organiser resolves match day 1 as "2–1" ending twenty minutes ago
      Then the command is refused as a stale reading
      And match day 1 still reads in progress

    # The close gate makes a closed round that still owns a running match
    # unreachable, so the round half of the guard is only observable on a fixture
    # announced after its round had already closed.
    Scenario: A result cannot be written for a fixture in a closed round
      Given round 1 is open
      And the organiser has closed round 1
      And match day 1 was announced in round 1 afterwards and is under way
      When the organiser resolves match day 1 as "2–1" ending twenty minutes ago
      Then the command is refused
      And match day 1 still reads in progress

  @rule:R-SCHEDULE-022
  Rule: A round's phase follows its match times

    Scenario: A lock one minute before the last hour lands
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And it is 61 minutes before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1
      Then the ride is locked

    Scenario: A lock landing exactly an hour before kickoff is refused
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B"
      And it is 60 minutes before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1
      Then the command is refused as the wrong phase

    Scenario: A burn landing exactly an hour after the last whistle is allowed
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B" at odds 1.80
      And a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And it is 60 minutes after round 1's last match
      When the player burns the ride
      Then the ride is burned

    Scenario: A burn a minute before the decision hour is refused
      Given round 1 is open
      And round 1 holds match day 1: "Team A" against "Team B" at odds 1.80
      And a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And it is 59 minutes after round 1's last match
      When the player burns the ride
      Then the command is refused as the wrong phase
      And the ride still waits on a decision
