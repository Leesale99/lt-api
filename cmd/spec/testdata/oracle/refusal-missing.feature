@kernel
Feature: An interface rule needs a refusal assertion

  @rule:R-KERNEL-001
  Scenario: The interface only observes a state
    Given a thing
    When the thing is written
    Then the thing reads "locked"
