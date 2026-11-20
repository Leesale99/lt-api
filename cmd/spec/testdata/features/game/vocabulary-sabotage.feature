@game
Feature: The sabotage family's vocabulary

  # gherkin-style.md §8: raw writes by the Owner pool legitimately carry SQL
  # verbs. The transport words stay forbidden there.

  @rule:R-KERNEL-001
  Scenario: The Owner pool writes a ride the application would refuse
    When the Owner pool tries to insert a ride
    Then the write is refused by rides_state_gate
    And the refusal names ride_state_transition_illegal

  @rule:R-KERNEL-002
  Scenario: A sabotage step still may not describe the wire
    When the Owner pool tries to insert a ride with a JSON payload
    Then the write is refused by rides_state_gate
