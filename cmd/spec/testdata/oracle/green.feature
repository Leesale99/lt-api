@kernel
Feature: The oracle gate accepts observable outcomes

  @rule:R-KERNEL-001
  Scenario: A refused write is observable
    Given a thing
    When the thing is written
    Then the write is refused by things_check

  @rule:R-KERNEL-001
  Scenario: A state is observable
    Given a thing
    When the thing is written
    Then the thing reads "locked"
