@game
Feature: A facade step that slips into SQL

  # gherkin-style.md §6 scopes the SQL ban to facade steps: the §8 Owner-pool
  # write may say `insert`, a player's step may not say `update`. Only the exact
  # keyword is a keyword — `selects` is business language.

  @rule:R-KERNEL-001
  Scenario: A facade step borrows a keyword
    When the player sends an update for the ride
    Then the ride is locked

  @rule:R-KERNEL-002
  Scenario: A conjugated verb is business language, not a keyword
    When the player selects the ride
    Then the ride is locked
