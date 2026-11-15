@game
Feature: Outline names

  @rule:R-KERNEL-003
  Scenario Outline: The <case> claim
    When the owner tries "<case>"
    Then the write is refused

    Examples:
      | case  |
      | alpha |
      | beta  |
