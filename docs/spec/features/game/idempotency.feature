@game
Feature: A ride command runs once
  Every ride command travels with a key its caller chooses. The key is the
  caller's promise that the command happens once: a retry under the same key
  never spends the player's tokens twice — it comes back with the answer the
  first run recorded. A command that was refused records nothing, so the same
  key is still free and the caller's retry runs for real.

  The key names one command and only one. The same key sent with a different
  command is a mistake the game refuses, never a replay of the other command.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the teams "Team A", "Team B" and "Team C" play in "Spring 2026"
    And the player "Ana" is registered in "Spring 2026" with 100 tokens
    And round 1 holds match day 1: "Team A" against "Team B"
    And round 1 holds match day 2: "Team B" against "Team C"
    And round 2 holds match day 3: "Team A" against "Team C"

  @rule:R-GAME-018
  Rule: A ride command claims its key before it runs

    # The second lock is the caller's retry: the same command, the same key,
    # sent again because the first answer never reached them.
    Scenario: A retry under the same key comes back with the recorded answer
      Given it is 3 hours before round 1's first match
      When the player locks 10 tokens on "Team A" for match day 1 under her key "ana-locks"
      Then the ride is locked
      When the player locks 10 tokens on "Team A" for match day 1 under her key "ana-locks"
      Then the recorded answer comes back
      And no second ride is born for "Ana" on match day 1

    Scenario: A refused command records nothing, so its retry runs for real
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And the last match of round 1 has not ended
      When the player burns the ride under her key "ana-burns"
      Then the command is refused as the wrong phase
      When match day 2 ends "1–0" to "Team B"
      And the player burns the ride under her key "ana-burns"
      Then the ride is burned

  # R-KERNEL-003 belongs to the kernel; its observable face is the ride command.
  @rule:R-KERNEL-003
  Rule: A key names exactly one logical command

    Scenario: The same key on a different command is refused, never replayed
      Given a ride waiting on a decision for "Team A" on match day 1 with 10 tokens
      And it is 2 hours after round 1's last match
      When the player burns the ride under her key "ana-decides"
      Then the ride is burned
      When the player unlocks the ride under her key "ana-decides"
      Then the command is refused as a key that already names another command
      And the ride still reads "burned"
