@game
Feature: A step that borrows the wrong words

  # The glossary fixture is seeded for a good feature; these steps deliberately
  # step outside it, one per finding policy 13 must produce.

  @rule:R-KERNEL-001
  Scenario: A step borrows the wire's vocabulary
    When the player sends a JSON request for "Team A"
    Then the ride is locked
    And the answer says 409
    And the scheduler ponders

  @rule:R-KERNEL-002
  Scenario: A step names the transport's status
    Then the answer carries the status code
