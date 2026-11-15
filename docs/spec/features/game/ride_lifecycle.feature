@game
Feature: Ride lifecycle
  A ride is a player's tokens locked on one team for one match. It is born
  locked, waits on a decision once its match is won, and ends as burned,
  unlocked or lost. Those five states, and the moves between them, are the
  whole life of a ride.

  Nobody invents a state. The game, the settlement and a repair script all
  walk the same graph, and the database is what refuses a move the graph does
  not allow — so a ride can never be transplanted, revived or fast-forwarded
  by a writer that skips the game.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the teams "Team A", "Team B" and "Team C" play in "Spring 2026"
    And the player "Ana" is registered in "Spring 2026" with 100 tokens
    And round 1 holds match day 1: "Team A" against "Team B"
    And round 1 holds match day 2: "Team B" against "Team C"
    And round 2 holds match day 3: "Team A" against "Team C"

  @rule:R-GAME-001
  Rule: Rides are born locked

    Scenario: A new ride is born locked
      Given it is 3 hours before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1
      Then the ride is locked
      And the ride carries a stake of 10 tokens
      And the ride carries a base price captured when it was locked
      And the ride has banked no streak and accrued no bonus

    Scenario Outline: A ride cannot be born in any other state
      When the Owner pool tries to insert a ride for "Ana" on match day 1 in the state "<state>"
      Then the write is refused by rides_state_insert_gate

      Examples:
        | state       |
        | won_pending |
        | burned      |
        | unlocked    |
        | lost        |

  @rule:R-GAME-002
  Rule: A ride moves only along the five-state graph

    Scenario: A won match leaves the ride waiting on a decision
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When match day 1 ends "2–1" to "Team A"
      Then the ride waits on a decision
      And the ride has accrued a bonus

    Scenario: A lost match ends the ride
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When match day 1 ends "0–1" to "Team B"
      Then the ride is lost

    Scenario Outline: A move the graph does not allow is refused through the facade
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When the player <command>
      Then the command is refused as an illegal transition
      And the ride still reads "locked"

      Examples:
        | command            |
        | burns the ride     |
        | unlocks the ride   |
        | continues the ride |

    Scenario Outline: The database refuses the same moves for a direct writer
      Given the ride on match day 1 is in the state "<from state>"
      When the Owner pool tries to move the ride straight to "<to state>"
      Then the write is refused by rides_state_gate
      And the refusal names ride_state_transition_illegal

      Examples:
        | from state  | to state |
        | locked      | burned   |
        | locked      | unlocked |
        | won_pending | lost     |

  @rule:R-GAME-003
  Rule: Terminal rides are frozen

    Scenario: A burned ride accepts no further command
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      And match day 1 ends "2–1" to "Team A"
      And it is 2 hours after round 1's last match
      And the player burns the ride
      When the player unlocks the ride
      Then the command is refused as an illegal transition
      And the ride still reads "burned"

    Scenario Outline: The database refuses to revive a terminal ride
      Given the ride on match day 1 is in the state "<state>"
      When the Owner pool tries to move the ride straight to "<to state>"
      Then the write is refused by rides_state_gate
      And the refusal names ride_terminal_state_frozen

      Examples:
        | state    | to state    |
        | burned   | locked      |
        | lost     | won_pending |
        | unlocked | locked      |

    Scenario: A terminal ride keeps the match it ended on
      Given a ride for "Team A" on match day 1 that was burned
      When the Owner pool tries to move the frozen ride onto match day 3
      Then the write is refused by rides_state_gate
      And the refusal names ride_terminal_state_frozen

    Scenario: Rewriting a terminal ride without moving it is not a transition
      Given a ride for "Team A" on match day 1 that was burned
      When the Owner pool rewrites the frozen ride without changing its state or its match
      Then the write is allowed
      And the ride still reads "burned"

  @rule:R-GAME-007
  Rule: Each ride command opens only in its phase window

    Scenario: A lock attempt inside the last hour before kickoff is refused
      Given it is 30 minutes before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1
      Then the command is refused as the wrong phase
      And no ride exists for "Ana" on match day 1

    Scenario: A lock attempt after the round's matches have ended is refused
      Given it is 2 hours after round 1's last match
      And round 1 is still open
      When the player locks 10 tokens on "Team A" for match day 1
      Then the command is refused as the wrong phase
      And no ride exists for "Ana" on match day 1

    Scenario Outline: A decision command before the round's last match ends is refused
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      And match day 1 ends "2–1" to "Team A"
      And the last match of round 1 has not ended
      When the player <command>
      Then the command is refused as the wrong phase
      And the ride still reads "won_pending"

      Examples:
        | command            |
        | burns the ride     |
        | unlocks the ride   |
        | continues the ride |

  @rule:R-GAME-025
  Rule: A ride changes the match it is on only by continuing

    Scenario: Continuing moves the won ride onto its next match
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And it is 2 hours after round 1's last match
      When the player continues the ride
      Then the ride is locked on match day 3
      And the ride still carries a stake of 10 tokens
      And the ride has banked one win

    Scenario Outline: No writer moves the ride to another match except by continuing it
      Given the ride on match day 1 is in the state "<state>"
      When the Owner pool tries to move the ride onto match day 3
      Then the write is refused by rides_state_gate
      And the refusal names ride_match_id_immutable_outside_continuation

      Examples:
        | state       |
        | locked      |
        | won_pending |
