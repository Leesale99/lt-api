@game
Feature: Ride lifecycle
  The pilot-shaped happy path: two rules tagged at the scenario, one rule
  tagged at the group.

  Background:
    Given the season "Spring 2026" has 2 rounds

  @rule:R-KERNEL-001
  Rule: A rule with several scenarios
    Scenario: A new ride is born locked
      When the player locks 10 tokens
      Then the ride is locked

    Scenario: A ride cannot be born in any other state
      When the Owner pool tries to insert a ride
      Then the write is refused

  @rule:R-KERNEL-002
  Scenario: A lost match ends the ride
    When match day 1 ends
    Then the ride is lost
