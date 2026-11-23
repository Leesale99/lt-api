# League Tokens — Game Engine Specification

## Purpose

This document defines how the game engine applies the rules in the Game Design document. It describes the
season and match lifecycles, Ride behavior, player operations, calculations, and rules the engine must always
protect.

The Game Design document is the source of truth for player-facing behavior. This specification is
authoritative for engine state definitions, including the four-state Season lifecycle. If a player-facing rule
and an engine rule conflict, resolve the conflict explicitly rather than silently inferring behavior.

This document does not describe APIs, database schemas, authentication, deployment, or user-interface
implementation. Those belong in the system-design document, which is not yet written.

## 1. Core concepts

This table defines the domain concepts the engine manipulates, so later sections can use the terms without
redefining them.

| Concept | Meaning |
|---|---|
| **Season** | A competition with a configured set of teams, matches, and championship standings. |
| **Round** | A way to group matches in the season schedule. Rounds do not control when a team enters its Action or Match Phase. |
| **Team phase** | The current period in which players may or may not perform Ride operations for a particular team. Each team has its own phase. |
| **Match** | A scheduled real-world game, with settled odds and an official result supplied by an external source. |
| **Token** | A team-specific asset held in a player's wallet, committed to a Ride, or destroyed by a burn or loss. |
| **Ride** | A player's commitment of a fixed number of a team's Tokens to that team's matches. A Ride can continue across consecutive wins. |
| **`acc`** | Virtual TB accumulated by a Ride after wins. It is credited as TB only when the Ride is burned. |
| **TB** | The game's score unit. TB is created by burning a Ride. |
| **Base** | The current price value for a team's Tokens. |
| **`base_at_lock`** | The team's Base captured when a Ride is first created. It remains fixed for the life of that Ride. |
| **Reserve** | The finite supply of a team's Tokens available for purchase from the game. |
| **Common pool** | The pool that receives Currency from reserve purchases and funds Currency returns from burns. |

## 2. Season lifecycle

This section defines the four states a season moves through and the conditions that characterize each one.

The four states, in order, are:

```text
Created → Open → InProgress → Closed
```

- **Created:** Season configuration is loaded, including teams, match schedule, reserves, and engine settings.
- **Open:** Players can register and choose a favourite team.
- **InProgress:** The competition is active. Each team's Action and Match phases follow that team's match
  schedule.
- **Closed:** The final regular-season result has been committed, the season-end automatic-burn operation has
  completed, and championship standings are final and read-only.

At Launch, seasons are independent. There is no cross-season carryover.

## 3. Team phases and match lifecycle

This section defines how each team's phases are derived from that team's match schedule, the buffers that time
them, how postponed fixtures and match statuses affect them, and the decision window that closes each Action
Phase.

### 3.1 Phases belong to teams, not rounds

Rounds group the competition schedule, but they do not define player action windows. Each team has its own
Action and Match phases, calculated from that team's match schedule. Different teams can be in different
phases at the same time.

A team alternates between:

- **Action Phase:** Players may create Rides for the team's next scheduled match and make decisions about
  eligible winning Rides.
- **Match Phase:** Players may not create a new Ride or continue an existing Ride for that team.

The market, when introduced, has its own rules and is not automatically closed by a team's Match Phase.

### 3.2 Configurable time buffers

Two per-season settings control the team windows:

| Setting | Meaning | Current setting |
|---|---|---:|
| `pre_match_buffer` | How long before the next scheduled match the team's Match Phase begins | 1 hour |
| `post_result_buffer` | How long after the official result is committed the team's Action Phase opens | 1 hour |

For a team's match:

1. The Match Phase begins at `scheduled_match_start - pre_match_buffer`.
2. The match is played.
3. The external result is officially committed by the engine.
4. The post-result buffer begins at the moment of official result commitment.
5. The Action Phase opens at `result_committed_at + post_result_buffer`.
6. The next Match Phase begins at `next_match_start - pre_match_buffer`.

With the current settings, the Action Phase opens one hour after the official result is committed and closes
one hour before the team's next match.

**A delayed result delays the next Action Phase.** The team remains in its Match Phase until the official
result is committed and the post-result buffer has elapsed. The end of the match itself, or the passage of the
scheduled post-match time, does not open the Action Phase.

The engine must use the actual configured match schedule and the timestamp at which the official result is
committed. It must not infer a result from the clock.

### 3.3 Postponed matches

A postponed match does not put the team into a Match Phase; the team remains in its Action Phase while that
fixture is postponed. Two scheduling consequences follow:

- The next Match Phase begins one `pre_match_buffer` before the next match whose status is `created`.
- A team enters Match Phase only when its next match is in the `created` state; postponed fixtures are skipped
  when determining the next match window.

### 3.4 Match statuses

The engine recognizes these four Match status values:

```go
const (
    MatchCreated    MatchStatus = "created"
    MatchInProgress MatchStatus = "in_progress"
    MatchPostponed  MatchStatus = "postponed"
    MatchClosed     MatchStatus = "closed"
)
```

- `created`: The match is scheduled and is eligible to be the team's next match for Ride commitment and Match
  Phase scheduling.
- `in_progress`: The match has started; its official result is still pending.
- `postponed`: The fixture is postponed. It does not put the team into Match Phase and is skipped when
  determining the next match window.
- `closed`: The official result has been committed and the match's Ride resolution has been processed.

The schedule, settled odds, and result are supplied by an external source. The engine does not calculate or
predict them. At Launch, a committed result is final; corrections and administrative reconciliation are
deferred.

### 3.5 Ride decision window and automatic burn

When a match result is committed:

- A Ride on a winning team becomes `WonPending`.
- A Ride on a losing team is resolved as a loss immediately.
- A winning Ride can be continued, unlocked, or burned only during that team's Action Phase, after the
  post-result buffer has elapsed.
- If the player makes no decision before that Action Phase closes, the engine automatically burns the Ride.

The decision deadline is the close of the team's Action Phase: the start of its next Match Phase, one
`pre_match_buffer` before the next scheduled match.

A delayed official result delays the Action Phase and the decision window with it. A Ride must never be
auto-burned before the player has had the Action Phase that follows the official result and its configured
post-result buffer.

At season end, the FinalAutoBurn operation burns all remaining `WonPending` Rides after the official result of
the final regular-season match has been committed.

## 4. Ride lifecycle

This section defines the five Ride states, the transitions between them, and the data a Ride must retain. A
Ride moves through these states:

```text
Locked
  ├─ official win result ─→ WonPending
  │                          ├─ Ride ─→ Locked
  │                          ├─ Burn ─→ Burned
  │                          ├─ Unlock ─→ Unlocked
  │                          └─ Action Phase closes ─→ Burned
  └─ official loss result ─→ Lost
```

The five Ride states are:

- `Locked`
- `WonPending`
- `Burned`
- `Unlocked`
- `Lost`

`Burned`, `Unlocked`, and `Lost` are terminal states. A terminal Ride cannot be changed.

Only `WonPending` allows a player to choose Ride, Burn, or Unlock.

### 4.1 Ride data

A Ride must retain at least:

```go
type Ride struct {
    ID           string
    PlayerID     int64
    TeamID       string
    MatchID      string
    State        RideState
    TokenLockID  string
    BaseAtLock   Decimal
    Acc          Decimal
    Streak       int
}
```

These Go types illustrate the domain data; exact implementation types are a decision deferred to the
system-design document, which is not yet written. The Ride references a wallet Token lock through
`TokenLockID`; it does not own the Tokens.

- `MatchID` identifies the match to which the Ride is currently committed.
- `TokenLockID` identifies the locked Token amount in the player's wallet. The Tokens remain in the wallet;
  the Ride refers to the wallet lock rather than holding or owning the Tokens.
- The locked amount and `BaseAtLock` are fixed from the Ride's creation and do not change as it continues.
- `Acc` increases when the Ride wins.
- `Streak` belongs to the Ride, not to the team's real-world winning streak.
- Multiple independent Rides for the same player and team are allowed.

## 5. Player operations

This section defines each player and system operation in turn, with the validations it requires and the state
changes it applies. All operations must validate the relevant phase, Ride state, and balance rules before
changing game state.

### 5.1 Register

Registration:

- Creates the player.
- Records the player's favourite team, which cannot be changed during that season.
- Grants 50 Currency.

Players do not receive Tokens automatically at Launch.

### 5.2 Buy Tokens from the reserve

A player can buy Tokens from a team's reserve during either that team's Action Phase or Match Phase.

```text
price = (team.base × amount)

player.currency -= price
common_pool     += price
team.reserve    -= amount
player.tokens   += amount
```

The purchase is allowed only if the player has enough Currency and the team has enough reserve. There is no
additional purchase cap at Launch.

### 5.3 Lock Tokens and create a Ride

A player can create a Ride only during the team's Action Phase.

Requirements:

- The team is in its Action Phase.
- The player has enough Tokens for that team.
- The team has a next scheduled match to which the Ride can be committed.

When the Ride is created:

```text
base_at_lock = team.base
streak       = 0
acc          = 0
state        = Locked
match        = team's next scheduled match

wallet.tokens[team].locked += amount
ride.token_lock_id = wallet token lock reference
```

The Tokens remain in the player's wallet, but their wallet status changes from available to locked. The Ride
stores a reference to that locked wallet amount; it does not hold the Tokens itself. Multiple Rides for the
same team are allowed, each linked to its own wallet lock.

### 5.4 Commit a match result

Result commitment is a system operation. It is idempotent by `match_id`: committing the same result again must
not apply the result or its economic effects twice.

For each team in the match, update its Base once:

```text
win:  team.base = (team.base × 1.05)
loss: team.base = (team.base × 0.95)
```

Base must remain strictly positive.

Then resolve every Ride committed to that match.

**If the Ride's team won:**

```text
acc  += acc_delta
state = WonPending
```

The player may decide what to do with the Ride during the team's next Action Phase, after the configured
post-result buffer.

**If the Ride's team lost:**

```text
destroyed = tokens_locked × X
returned  = tokens_locked - destroyed

wallet.tokens[team].locked -= tokens_locked
wallet.tokens[team].available += returned
team.tokens_destroyed += destroyed
acc = 0
state = Lost
```

The destroyed Tokens leave circulation. The remaining Tokens return to the player's wallet. A loss ends the
Ride; there is no player decision after a loss.

### 5.5 Continue a Ride

A player can continue a Ride only when:

- the Ride is `WonPending`;
- the team's Action Phase is open;
- the official result that produced `WonPending` has been committed;
- the team has a next scheduled match.

Continuing a Ride commits the same Tokens to the team's next scheduled match:

```text
streak += 1
match  = team's next scheduled match
state  = Locked
```

The wallet's locked Token amount, `BaseAtLock`, and accumulated `Acc` remain unchanged. The Ride continues to
reference the same wallet lock.

### 5.6 Burn a Ride

A player can burn a Ride only when it is `WonPending` and the team's Action Phase is open.

```text
tb_credit       = tokens_locked + acc
currency_credit = base_at_lock × tokens_locked

player.TB       += tb_credit
team.TB         += tb_credit
player.currency += currency_credit

wallet.tokens[team].locked -= tokens_locked
team.tokens_destroyed += tokens_locked
state = Burned
```

The CommonPool funds the Currency return. The same burn rules apply to a player-requested burn, an automatic
burn at the end of the Action Phase, and a season-end burn.

### 5.7 Unlock a Ride

A player can unlock a Ride only when it is `WonPending` and the team's Action Phase is open.

```text
wallet.tokens[team].locked -= tokens_locked
wallet.tokens[team].available += tokens_locked

acc is forfeited
state = Unlocked
```

Unlocking changes the status of the Tokens in the player's wallet from locked to available. The Tokens never
leave the wallet. The player receives no TB or Currency from the Ride, and the Ride ends.

### 5.8 Automatic burn

At the close of a team's Action Phase, the engine burns every `WonPending` Ride for that team that has not
already been acted on.

Automatic burn uses exactly the same TB and Currency calculations as a player-requested burn.

At season end, the `FinalAutoBurn` operation burns every remaining `WonPending` Ride across all teams and
players after the official result of the final regular-season match has been committed. `FinalAutoBurn` is an
engine operation, not a separate season state.

## 6. Economy calculations

This section states every calculation the engine performs: the `acc` earned on a win, loss destruction, base
price updates, reserve purchase price, and Currency flow.

### 6.1 `acc` earned on a win

```text
acc_delta =
    (
        tokens_locked
        × (odds_settled - 1)
        × (1 + streak × s)
    )

acc += acc_delta
```

Defaults:

```text
s = 0.20
```

- `odds_settled` is the settled decimal odds supplied by the external result feed.
- `streak` is zero-based. A newly created Ride starts at zero.
- The formula uses the Ride's current streak when the match result is committed. The streak increases when the
  player continues the Ride.
- The team's real-world winning streak does not affect this calculation.

### 6.2 Loss destruction

```text
destroyed = (tokens_locked × X)
returned  = tokens_locked - destroyed
```

Default:

```text
X = 0.25
```

Only one loss can end a Ride. Accumulated `acc` is forfeited.

### 6.3 Base price

Base is updated once per team for each committed match result:

```text
win:  base = (base × 1.05)
loss: base = (base × 0.95)
```

Base must remain greater than zero. There is no 1.0 floor and no upper cap. Existing Rides are unaffected by
later Base changes because `BaseAtLock` is fixed when the Ride is created.

### 6.4 Reserve purchase

```text
price = (team.base × amount)
```

The current Base is used for a reserve purchase. The reserve decreases by the purchased amount and does not
refill at Launch.

### 6.5 Currency flow

At Launch:

- Registration grants are the planned Currency injection.
- Reserve purchases move Currency from player wallets into the CommonPool.
- Burns return Currency from the CommonPool to player wallets.
- Token destruction destroys Tokens, not Currency.

The Game Design document describes the CommonPool deficit mechanism as mechanical safety, but its exact
accounting treatment must remain consistent with the intended closed Currency economy. Whether a burn may
create Currency when the pool is insufficient, or must be backed by available pool funds, is listed as
decision 1 in §11 Decisions to confirm.

## 7. Championships and standings

This section defines how player and team TB totals are produced, which tiebreaks Launch uses, and which
leaderboards ship at Launch.

TB is the only championship score. Currency and wallet value do not count toward either championship.

**Player Championship**

```text
player_TB = sum of TB credits from that player's Ride burns
```

**Team Championship**

```text
team_TB = sum of TB credits from burns of Rides belonging to that team
```

At Launch, the tiebreaks are:

- Player standings: TB descending, then oldest account first.
- Team standings: TB descending, then team ID.

Launch leaderboards:

- Team Championship.
- Player Championship.
- Team Contributors.

The Net Worth board is part of the later Market delivery.

## 8. Engine invariants

The engine must preserve these rules on every operation:

1. **No negative balances:** player Currency, available and locked player Token balances, team reserves, and
   Ride `acc` cannot be negative.
2. **Positive Base:** a team's Base must remain greater than zero.
3. **Phase-gated Ride operations:** Lock, Ride, Burn, and Unlock are allowed only during the relevant team's
   Action Phase.
4. **Reserve purchase:** requires sufficient player Currency and sufficient team reserve. At Launch, it is
   allowed during Action and Match phases.
5. **Correct match target:** a new Ride or continued Ride must target that team's next scheduled match.
6. **Result before resolution:** a Ride cannot become `WonPending` or `Lost` until the match's official result
   is committed.
7. **Decision window:** only a `WonPending` Ride can be continued, burned, or unlocked, and only during its
   team's Action Phase.
8. **Automatic burn:** unresolved player decisions are automatically burned when the team's Action Phase
   closes. Players cannot cancel or reverse an automatic burn.
9. **Terminal states:** `Lost`, `Burned`, and `Unlocked` are final.
10. **Favourite team:** a player's favourite team is set once at registration and cannot change during the
    season.
11. **Idempotent result commitment:** a match result and its economic effects are applied at most once per
    `match_id`.
12. **Ride lock remains fixed:** the wallet Token lock reference, locked amount, and `BaseAtLock` do not
    change during a Ride chain.

## 9. Configuration

This section lists the per-season engine settings that tune risk, price movement, supply, and phase timing.
Values marked as current defaults may be changed through simulation or product decisions.

| Setting | Current value | Purpose |
|---|---:|---|
| Loss destruction `X` | `0.25` | Share of locked Tokens destroyed after a loss |
| Streak coefficient `s` | `0.20` | Bonus applied to `acc_delta` for a Ride's streak |
| Base win multiplier | `1.05` | Base update after a win |
| Base loss multiplier | `0.95` | Base update after a loss |
| Initial Token reserve `K` | `200` per team | Initial team reserve |
| Registration grant `G` | `50 Currency` | Currency granted at registration |
| `pre_match_buffer` | 1 hour | Opens the team's Match Phase before its next match |
| `post_result_buffer` | 1 hour | Opens the team's Action Phase after official result commitment |

## 10. Scope and open questions

This section lists what the Launch build includes, what is deferred, and which decisions must be confirmed
before implementation relies on them.

### Included at Launch

- Season registration and favourite-team selection.
- Team-specific Action and Match phases.
- Token reserve purchases.
- Ride creation, continuation, loss resolution, player burn, unlock, and automatic burn.
- Base and `acc` calculations.
- Player and Team Championships and Launch leaderboards.
- Season-end automatic burn.

### Deferred

- Player-to-player market and Net Worth board.
- Staking and idle policies.
- Wallet-burn.
- Playoffs and season rollover.
- Live match centre and richer notifications.
- Cancelled, voided, or corrected match handling beyond the Launch assumption that a committed result is
  final. Postponed fixtures do not put a team into a Match Phase and are skipped when the engine determines
  the next match window (§3.3 Postponed matches).

### Decisions to confirm

1. **CommonPool deficit handling:** The Game Design document calls this a mechanical safety mechanism but does
   not fully define its accounting. Confirm whether a burn may create Currency if the pool is insufficient, or
   whether burns must be backed by available pool funds.
2. **Ride already committed to a postponed match:** A postponed fixture does not put the team into a Match
   Phase and is skipped when the engine determines the next match window (§3.3 Postponed matches). The
   handling of a Ride already committed to that fixture remains unresolved and must be decided before relying
   on postponed fixtures in production.
3. **Result-feed failure:** A delayed official result keeps the team in its Match Phase and delays its next
   Action Phase. Operational retry and escalation behavior belongs in the system design.

Until these decisions are made, do not invent additional gameplay behavior.
