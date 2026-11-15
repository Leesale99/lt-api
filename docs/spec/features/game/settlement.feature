@game
Feature: Settlement
  A round of matches ends, its results become committed facts, and every ride
  on that round's matches arrives somewhere. Settlement is the moment the game
  decides: a won ride waits on a decision with the bonus it accrued, a lost
  ride forfeits what it had accrued, and a completing round resolves whatever
  is still waiting.

  Money follows the ride and never the other way round. The tokens locked are
  the stake, the bonus is the accrual, and the state a ride reaches decides
  what happens to both.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the teams "Team A", "Team B" and "Team C" play in "Spring 2026"
    And the player "Ana" is registered in "Spring 2026" with 100 tokens
    And the player "Bruno" is registered in "Spring 2026" with 100 tokens
    And round 1 holds match day 1: "Team A" against "Team B" at odds 1.80
    And round 1 holds match day 2: "Team B" against "Team C"
    And round 2 holds match day 3: "Team A" against "Team C"

  @rule:R-GAME-004
  Rule: Ride money stays inside its domain

    Scenario Outline: A stake of nothing is refused and no ride is born
      Given it is 3 hours before round 1's first match
      When the player locks <the stake> on "Team A" for match day 1
      Then the command is refused
      And no ride exists for "Ana" on match day 1

      Examples:
        | the stake |
        | 0 tokens  |
        | -5 tokens |

    Scenario Outline: The database refuses a ride carrying money outside its domain
      When the Owner pool tries to insert a locked ride with <an amount outside the domain>
      Then the write is refused by <database object>

      Examples:
        | an amount outside the domain  | database object           |
        | a stake of zero tokens        | rides_tokens_locked_check |
        | a stake of minus five tokens  | rides_tokens_locked_check |
        | a base price of zero          | rides_base_at_lock_check  |
        | an accrued bonus of minus one | rides_bonus_acc_check     |
        | a streak of minus one         | rides_streak_check        |

  @rule:R-GAME-005
  Rule: An unlocked or lost ride carries no accrued bonus

    Scenario: Unlocking a won ride forfeits the bonus it accrued
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens at odds 1.80
      And match day 1 ends "2–1" to "Team A"
      And it is 2 hours after round 1's last match
      When the player unlocks the ride
      Then the ride is unlocked
      And the ride carries no bonus

    Scenario: A lost ride ends with no bonus
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens at odds 1.80
      When match day 1 ends "1–2" to "Team B"
      Then the ride is lost
      And the ride carries no bonus

    # A burn fulfils the stake and the bonus as TB, so the ride keeps its
    # accrual: the coupling that zeroes the bonus covers unlocked and lost only.
    Scenario: A burned ride keeps the bonus it accrued
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens at odds 1.80
      And match day 1 ends "2–1" to "Team A"
      And it is 2 hours after round 1's last match
      When the player burns the ride
      Then the ride is burned
      And the ride keeps its bonus of 8.00

    Scenario: The database refuses an unlock that keeps the bonus
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And the ride has accrued a bonus of 8.00
      When the Owner pool tries to unlock the ride without releasing its bonus
      Then the write is refused by rides_money_state_coupling

  @rule:R-GAME-006
  Rule: A win accrues a bonus from the stake, the odds and the banked streak

    Scenario Outline: A win accrues the stake times the odds minus one times the streak multiplier
      Given a ride locked by "Ana" on "Team A" for match day 1 with <the stake>
      And the ride's team is priced at <the odds>
      And the ride has banked <banked>
      When its match ends "2–1" to "Team A"
      Then the ride waits on a decision with a bonus of <the bonus>

      Examples:
        | the stake | the odds | banked          | the bonus |
        | 10 tokens | 1.80     | no earlier win  | 8.00      |
        | 10 tokens | 1.80     | one earlier win | 9.60      |
        | 20 tokens | 2.50     | no earlier win  | 30.00     |

  @rule:R-GAME-008
  Rule: A ride write needs an open round at the write instant

    Scenario: A command on an open round is written
      Given it is 3 hours before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1
      Then the ride is locked

    # The round closed without its completion reaching settlement — the accepted
    # gap the sweep closes — so the ride is still waiting here.
    Scenario: A command after the round has closed is refused and writes nothing
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And round 1 closed without settling its rides
      When the player burns the ride
      Then the command is refused as the round not being open
      And the ride still reads "won_pending"

  @rule:R-GAME-009
  Rule: A completing round force-resolves the rides still waiting

    Scenario: A completing round unlocks the ride still waiting on a decision
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And the ride has accrued a bonus of 8.00
      When round 1 completes
      Then the ride is unlocked
      And the ride carries no bonus

    # The season's last round is reachable only after rounds 1-37 have closed (the
    # calendar refuses an unbroken sequence), so the fixture builds the last round
    # instead of the scenario walking thirty-eight rounds by hand.
    Scenario: The season's final round burns the ride still waiting
      Given the season's final round holds a match day for "Team A"
      And a ride waiting on a decision for "Team A" in that match day with 10 tokens
      And the ride has accrued a bonus of 8.00
      When the season's final round completes
      Then the ride is burned
      And the ride keeps its bonus of 8.00

    # The result of match day 1 never reached settlement, so the ride is still
    # locked when the round completes: the completion settles it from the
    # committed result first, then sweeps what is left waiting.
    Scenario: A completing round also settles the ride whose result never arrived
      Given a ride locked by "Bruno" on "Team B" for match day 1 with 10 tokens
      And match day 1 ended "0–2" to "Team B" without reaching settlement
      When round 1 completes
      Then the ride is unlocked
      And the ride carries no bonus

  @rule:R-GAME-010
  Rule: A settled ride agrees with the committed match result

    Scenario: Each ride arrives where the committed result puts it
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      And a ride locked by "Bruno" on "Team B" for match day 1 with 10 tokens
      When match day 1 ends "2–1" to "Team A"
      Then the ride on "Team A" waits on a decision
      And the ride on "Team B" is lost

    # The result-agreement check lives in the settlement use case, not in the
    # database, so a writer that bypasses the application is not refused here.
    # This rule is proved at the facade and has no sabotage scenario: claiming
    # one would assert a gate the tree does not have.
    Scenario: A settlement that claims the wrong winner is refused and pays nothing
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      And match day 1 ended "2–1" to "Team A"
      When the settlement reports match day 1 as a win for "Team B"
      Then the settlement is refused as an illegal transition
      And the ride is still locked
      And the ride carries no bonus

    # The destination here agrees with the committed result; only the rate the
    # bonus would be paid at is misreported. The rate is a claim like the
    # destination, so it is verified against the committed odds and refused
    # when it disagrees — nothing is paid at a rate the match does not carry.
    Scenario: A settlement that misreports the odds is refused and pays nothing
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      And match day 1 ended "2–1" to "Team A"
      When the settlement reports match day 1's odds for "Team A" as 9.99
      Then the settlement is refused as an illegal transition
      And the ride is still locked
      And the ride carries no bonus

    Scenario: A draw crowns nobody
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens
      When match day 1 ends "1–1"
      Then the ride is lost
      And the ride carries no bonus

  @rule:R-GAME-016
  Rule: A settlement arrives at each ride once

    Scenario: The same result arriving twice pays the bonus once
      Given a ride locked by "Ana" on "Team A" for match day 1 with 10 tokens at odds 1.80
      And match day 1 ended "2–1" to "Team A"
      When the result of match day 1 arrives a second time
      Then the ride still waits on a decision
      And the ride's bonus is still 8.00
