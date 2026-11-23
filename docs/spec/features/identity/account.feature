@identity
Feature: Account registration and activation
  An account is born unactivated: registering creates it, mails her an
  activation token, and answers with an account that cannot act yet.
  Activation is the member's own step — she takes the activation token from
  her mail and spends it once — and it is the gate between holding a password
  and belonging.

  An email is one account whatever its case, a newly created account always
  holds the standard user role, and an activation token that no longer
  resolves is simply "no such activation token": spent, expired and minted for
  another purpose are one answer, because telling them apart would tell a
  stranger something.

  # The mail transport is a capturing one: what the service mails is held
  # rather than delivered, so a scenario reads the activation token the way the
  # member reads it from her mail.
  Background:
    Given the mail the service sends is held

  @rule:R-IDENTITY-019
  Rule: An account is born unactivated

    Scenario: A newly registered account arrives unactivated
      When the visitor registers as "Ana" with the email "ana@example.com"
      Then the answer names "Ana"
      And the answer names the email "ana@example.com"
      And the answer says the account is not yet activated
      And the answer carries the stamp of when the account was made

  # The other half of the rule — a client cannot ask for a role, hint at one or
  # grant itself one — is a refusal of the shape of the registration, not of the
  # account: the registration carries no room for a role at all, so nothing this
  # feature could observe distinguishes "cannot choose" from "chose and was
  # ignored". It stays prose here (and in the catalog) rather than a scenario.
  @rule:R-IDENTITY-012
  Rule: Registration gives the account the standard user role

    Scenario: A registration arrives holding the standard user role
      When the visitor registers as "Ana" with the email "ana@example.com"
      Then the answer names the standard user role

  @rule:R-IDENTITY-011
  Rule: An email address identifies exactly one account, whatever its case

    Scenario: An email already taken in another case is refused as a duplicate
      Given a member is registered as "Ana" with the email "ana@example.com"
      When the visitor registers as "Ana B" with the email "ANA@EXAMPLE.COM"
      Then the registration is refused as a duplicate
      And the refusal names the email as the field to change

    # The same comparison on the way in: an address typed in any case reaches
    # the one account that holds it, so the casing of an email is never a
    # second identity.
    Scenario: A differently cased email signs the same member in
      Given a member is activated as "Ana" with the email "ana@example.com"
      When the member "Ana" signs in as "ANA@EXAMPLE.COM" with her password
      Then she is signed in

  @rule:R-IDENTITY-007
  Rule: Activation tokens live three days, sign-in tokens one day

    Scenario: Her activation token is good for three days
      When the visitor registers as "Ana" with the email "ana@example.com"
      Then her activation token is good for three days

    Scenario: A sign-in token is good for one day
      Given a member is activated as "Ana" with the email "ana@example.com"
      When the member "Ana" signs in with her password
      Then her sign-in token is good for one day

  @rule:R-IDENTITY-008
  Rule: A credential only works for what it was minted for, while it is fresh

    # An expired, spent or wrong-purpose credential resolves to nothing at all:
    # the lookup answers "no such credential", so the refusal of every one of
    # them is the same refusal — and it is the one a never-minted credential
    # receives.
    Scenario: An activation token does not sign anyone in
      Given a member is registered as "Ana" with the email "ana@example.com"
      When the member "Ana" opens the list of seasons with the activation token from her mail
      Then the page is refused as unauthenticated

    Scenario: A sign-in token past its day is refused exactly as one that never existed
      Given a member is activated as "Ana" with the email "ana@example.com"
      And her earlier sign-in token has passed its one day
      When the member "Ana" opens the list of seasons with that sign-in token
      Then the page is refused as unauthenticated

  @rule:R-IDENTITY-009
  Rule: Activating an account spends every activation token it holds

    Scenario: The same activation token cannot be spent twice
      Given a member is registered as "Ana" with the email "ana@example.com"
      And the member "Ana" activated her account with the activation token from her mail
      When the member "Ana" presents the same activation token again
      Then her activation is refused as an expired or spent activation token

    # Two mails in flight is the deep-link case: the account holds both
    # activation tokens, and the first successful activation spends the second
    # one too — so an old mail cannot be opened after the account is already in.
    Scenario: A second activation mail dies with the first activation
      Given a member is registered as "Ana" with the email "ana@example.com"
      And the member "Ana" asked for a second activation mail
      And the member "Ana" activated her account with the activation token from her first mail
      When the member "Ana" presents the activation token from her second mail
      Then her activation is refused as an expired or spent activation token

  @rule:R-IDENTITY-022
  Rule: Activation happens once and asking discloses nothing

    # The request endpoint's answer carries no existence bit: an address no
    # account holds and an account that is already active are answered exactly
    # as a genuine request is — same status, same body — and neither is mailed
    # anything.
    Scenario: An activated account collects no second activation mail
      Given a member is activated as "Ana" with the email "ana@example.com"
      And a member is registered as "Bea" with the email "bea@example.com"
      When the member "Ana" asks for a fresh activation mail
      Then no activation mail arrives for "ana@example.com"
      And the answer is the one the unactivated member "Bea" receives

    Scenario: An address no account holds is answered like a registered one
      Given a member is registered as "Bea" with the email "bea@example.com"
      When a visitor asks for a fresh activation mail for "nobody@example.com"
      Then no activation mail arrives for "nobody@example.com"
      And the answer is the one the unactivated member "Bea" receives

    Scenario: An account that never activated may collect a fresh mail
      Given a member is registered as "Ana" with the email "ana@example.com"
      When the member "Ana" asks for a fresh activation mail
      Then a fresh activation token arrives in her mail

  @rule:R-IDENTITY-006
  Rule: Only a token's fingerprint is ever kept

    # The table has no column a readable token could sit in, and the fingerprint
    # is the key: the same token can never be kept twice, whoever writes.
    Scenario: The database refuses the same token a second time
      Given a member is registered as "Ana" with the email "ana@example.com"
      When the Owner pool tries to store the same token a second time
      Then the write is refused by user_tokens_pkey

  @rule:R-IDENTITY-020
  Rule: An account's identity facts are stamped by the store

    # The id is the one identity fact the database refuses to take from a
    # caller: it is generated always, not a default a supplied value falls back
    # from, so the account is the store's to name.
    Scenario: The database refuses an account that names its own id
      Given a member is registered as "Ana" with the email "ana@example.com"
      When the Owner pool tries to record an account that names its id
      Then the write is refused for the id the store stamps
