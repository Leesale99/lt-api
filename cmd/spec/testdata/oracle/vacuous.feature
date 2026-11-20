@kernel
Feature: The oracle gate refuses a vacuous Then

  @rule:R-KERNEL-001
  Scenario: The call only did not explode
    Given a thing
    When the thing is written
    Then the call succeeds
