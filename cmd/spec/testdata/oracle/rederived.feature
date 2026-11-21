@kernel
Feature: A scenario may not copy the implementation's literal

  @rule:R-KERNEL-001
  Scenario: The expected message is copied from the code
    Given a thing
    When the thing is written
    Then the refusal says "boom"
