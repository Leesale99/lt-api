@identity
Feature: Who may act
  Holding a password is not the same as being allowed to act. An account that
  has not finished activation may sign in and do nothing else; an activated
  account acts only as far as the codes its role carries; and a page that
  names a code is served to nobody else — not to a stranger, not to a member
  whose role half carries what the page asks for.

  What a caller may do is fixed reference data: two roles and eleven codes
  seeded with the schema, so every account holding the standard user role is
  allowed exactly the same things, always. A standard user reads the calendar
  and manages her own player; she does not touch the teams and she does not
  touch anyone else's player. The administrator does everything.

  Background:
    Given the season "Spring 2026" has 2 rounds
    And the member "Ana" is an activated account
    And the member "Carla" is an activated account
    And the member "Carla" is registered as the player "Duda" in "Spring 2026" with 100 tokens
    And the administrator is an activated account

  @rule:R-IDENTITY-018
  Rule: A failed login is one refusal, whichever half failed

    # Both refusals are the same one — same answer, same sentence — so a caller
    # cannot use signing in to learn whether an address is registered. They also
    # cost the same work: the unknown-email path burns a dummy password
    # comparison before it refuses, so the timing is no longer an oracle
    # (misalignment-report §6.8 is resolved). No scenario reads a clock — the
    # work half is pinned by the service test TestAuthenticate instead.
    Scenario: An unknown email is refused as an invalid credential
      When the visitor signs in as "nobody@example.com" with the password "guessed-once"
      Then the sign-in is refused as an invalid credential

    Scenario: A wrong password is refused as an invalid credential
      When the member "Ana" signs in with a password that does not match hers
      Then the sign-in is refused as an invalid credential

  @rule:R-IDENTITY-016
  Rule: An unactivated account is refused at every gated page

    Scenario: An unactivated account may still sign in
      Given the member "Nina" is registered and has not activated
      When the member "Nina" signs in with her password
      Then she is signed in

    Scenario Outline: An unactivated account is refused at every gated page
      # Nina's role carries the read codes, so the players page is refused for
      # the activation and not for the code: activation is checked first.
      Given the member "Nina" is registered and has not activated
      And the member "Nina" signed in with her password
      When the member "Nina" <page>
      Then she is refused as an unactivated account

      Examples:
        | page                      |
        | opens the list of seasons |
        | opens the list of players |
        | records a team            |
        | renames the player "Duda" |

  @rule:R-IDENTITY-017
  Rule: A gated page requires the exact permission code it names

    Scenario: A page is served to the role that carries the code it names
      When the member "Ana" opens the list of seasons
      Then the page lists "Spring 2026"

    Scenario: A code the standard user's role does not carry is refused
      When the member "Ana" records a team
      Then she is refused as a permission she does not hold
      And no team was recorded

    # The player surface asks for players:write at the page and players:write:any
    # again for another member's player. The standard user carries the first and
    # not the second, so a half-carried code is still no code.
    Scenario: A code the standard user's role half carries is refused
      When the member "Ana" renames the player "Duda"
      Then she is refused as a permission she does not hold
      And the player "Duda" still reads "Duda"

    Scenario: A visitor with no credential at all is refused as unauthenticated
      When the visitor opens the list of seasons
      Then the page is refused as unauthenticated

  @rule:R-IDENTITY-014
  Rule: Two roles and eleven permission codes are seeded reference data

    # Nothing in the running service adds, removes or re-grants a permission: the
    # matrix is data an applied migration seeds, read-only to the application. A
    # wrong matrix is fixed by a new migration, so this is asserted as the data
    # it is, not through a page.
    Scenario: The standard user may read the calendar and manage her own player
      Then the standard user role carries:
        | code          |
        | seasons:read  |
        | rounds:read   |
        | matches:read  |
        | players:read  |
        | players:write |
        | teams:read    |
      And the standard user role carries no other code

    Scenario: The administrator may do everything
      Then the administrator role carries all eleven codes
