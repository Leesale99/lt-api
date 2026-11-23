# League Tokens — Game Design Reference

> **Document purpose:** A shared, implementation-independent reference for developers, designers, and AI
> agents.
>
> **Core idea:** Players acquire team Tokens, commit them to real basketball matches, and decide when to turn
> successful rides into **TB**. TB is the only championship score. The game asks players to balance personal
> ranking against their favourite team's ranking.

---

## 1. Game at a Glance

This section summarizes what a player does in League Tokens, the strategic tension that drives those choices,
and the concepts the rest of the document defines. League Tokens is a fantasy-economy game built around a real
basketball league and its match results, schedules, and bookmaker odds.

Players:

- Receive a fixed Currency grant when they register.
- Buy Tokens from a finite reserve for a specific team.
- Lock Tokens into a match to start a **ride**.
- After a win, choose whether to continue the ride, burn it for TB, or unlock it.
- Compete in two season-long championships:
  - **Team Championship:** TB credited to each team's basket.
  - **Player Championship:** TB earned by each player.

### The central strategic tension

**Burning** scores TB for both the player's total and the team's basket. A player may therefore burn a ride
for personal advantage even when that burn is not the best contribution to their team's championship, or
continue riding to pursue a larger future reward at greater risk. The player cannot fully optimize both goals
at all times.

### What matters

| Concept | Role in the game |
|---|---|
| **TB** | The only score used by either championship |
| **Currency** | Spendable game money used to acquire Tokens; it is not score |
| **Tokens** | Team-specific assets used in rides and burns |
| **Ride** | A chain of match commitments made with locked Tokens |
| **acc** | A ride's accumulated virtual TB, realized only when burned |
| **Team basket** | The total TB credited to a team |
| **Reserve** | The finite supply of a team's Tokens available for primary purchase |

---

## 2. Scope and Reading Guide

This section states what this document governs, which companion document answers engine questions, and which
delivery labels mark scope. This document defines **what the game is, the player-facing rules, and why the
major systems exist**; it is the product and domain reference, not an implementation specification.

| Need | Source of truth |
|---|---|
| Game rules, player decisions, economy intent, championships, and phasing | This document |
| Engine calculations, state transitions, timing rules, and edge cases | `domain-knowledge-game-engine.md` |
| System architecture and technical design | `system-design.md` (to be developed using the Domain-Driven Design book as a guide) |

The game design and engine specification define the problem and its rules. The system design will describe how
the software realizes them, using the concepts and methods from the DDD book we are studying.

### Delivery labels

- **[Launch]** — Required for the MVP soft launch.
- **[Market]** — End-of-MVP delivery; priority 1.
- **[Post-launch]** — Deferred; no committed delivery date.
- **Simulation-owned** — A starting value or hypothesis that must be validated by simulation, not treated as a
  permanent product rule.

---

## 3. Design Principles

This section lists the five principles that govern every system in the game; each system must support at least
one of them.

- **One score, many paths.** TB determines both championships. Currency is fuel, never victory.
- **A meaningful decision for every team.** Choosing which team to back and how many Tokens to commit matters
  from the start of the season.
- **Underdogs are viable.** High variance and rare, large rewards should create a legitimate underdog
  strategy—not charity.
- **Risk is visible and honest.** Players risk accumulated acc and a portion of the Tokens they committed.
- **Use the least complexity that preserves the game.** Every system must support one or more of these
  principles.

---

## 4. Season and Round Structure

This section defines how a season is configured, how it moves through its lifecycle states, how team phases
and match windows are timed, and which match statuses the engine recognizes.

### League configuration

- Each season is configured with **N teams** and **R rounds**.
- The simulation corpus uses EuroLeague history: 18 teams and 34 rounds. A later season may use 20 teams.
- The application build targets a **20-team, 38-round** season — a double round-robin in which every team
  plays every other team once at home and once away. That shape is a recorded product decision (dated
  2026-11-23), restated with the simulation parameters in §12 Simulation-Owned Parameters.
- The game uses the real league schedule, match results, and bookmaker odds.

### Season lifecycle

The engine's authoritative Season states are:

```text
Created → Open → InProgress → Closed
```

- **Created:** Season configuration is loaded, including teams, match schedule, reserves, and engine settings.
- **Open:** Players can register and choose a favourite team.
- **InProgress:** The competition is active. Each team's Action and Match phases follow that team's match
  schedule.
- **Closed:** The final regular-season result has been committed, the season-end automatic-burn operation has
  completed, and championship standings are final and read-only.

`FinalAutoBurn` is an engine operation performed before the Season enters `Closed`; it is not a Season state.
At Launch, seasons are independent, with no cross-season carryover.

### Team-specific phases and match windows

Rounds remain part of the season schedule and group the competition. However, **Action and Match phases are
tracked independently for each team**. A team's phase is determined by that team's match schedule, not by a
shared round boundary. Teams can therefore be in different phases at the same time.

Each team alternates between two phases:

- **Action Phase** — Players can create a Ride by locking Tokens for that team's next scheduled match. Players
  can also manage eligible winning Rides, provided the match's official result has been committed and the
  post-result buffer has elapsed.
- **Match Phase** — The team's lock window is closed. Players cannot create a new Ride or continue a Ride by
  locking its Tokens for that team.

The two phases are separated by configurable time buffers:

- **Pre-match buffer (`pre_match_buffer`)** — The time before the team's next scheduled match when the Match
  Phase begins. The current setting is **1 hour**.
- **Post-result buffer (`post_result_buffer`)** — The time after the match's official result is committed when
  the team's Action Phase opens. The current setting is **1 hour**.

Both settings are configurable. With the current settings, the team's Action Phase opens exactly one hour
after the official result is committed and remains open until one hour before the team's next scheduled match,
when the Match Phase begins.

The official result commitment is required before the Action Phase can reopen. If the result is delayed, the
team remains in its Match Phase even if the match itself has ended; the post-result buffer starts only when
the official result is committed.

After a win is officially committed and the post-result buffer has elapsed, the Ride becomes eligible for the
player's decision during that team's Action Phase:

- **Ride** — Continue the existing Ride by committing the same Tokens to the team's next scheduled match.
- **Unlock** — Return the locked Tokens to the player's wallet and forfeit accumulated `acc`.
- **Burn** — Burn the locked Tokens and convert the Tokens and accumulated `acc` into TB.

If the player does not make a decision before the Action Phase closes, the Ride is automatically burned. There
is no separate Decision Phase; Ride, Unlock, and Burn are operations available during the team's Action Phase.

### Match statuses

The engine recognizes four Match status values, listed below with the behavior each one implies for Ride
commitment and phase scheduling.

```go
const (
    MatchCreated    MatchStatus = "created"
    MatchInProgress MatchStatus = "in_progress"
    MatchPostponed  MatchStatus = "postponed"
    MatchClosed     MatchStatus = "closed"
)
```

- `created`: The match is scheduled and can be selected as the team's next match.
- `in_progress`: The match has started and its official result is pending.
- `postponed`: The fixture is postponed and is skipped when calculating the team's next Match Phase.
- `closed`: The official result has been committed and Ride resolution has been processed.

At Launch, a committed result is final. Cancelled, voided, or corrected-result handling is not defined.

### MVP season arc

The MVP covers the regular season:

`Regular season → season-end auto-burn → final standings`

Playoffs are deferred to post-launch.

---

## 5. Assets and Economy

This section defines the game's assets — Tokens, Currency, TB, acc, base price and the reserve — and the
economy rules that govern supply, price, and destruction.

### Core assets

| Asset | Meaning |
|---|---|
| **Token** | A team-specific asset. Players buy Tokens from a finite team reserve, lock them into matches, and burn them to produce TB. |
| **Currency** | In-game money used to acquire Tokens and participate in the economy. Currency has no leaderboard value. Its total supply is fixed by registration grants. |
| **TB** | The game's score unit. TB is created only by burning. Each Token burned contributes 1 TB, with the ride's acc added on top. |
| **acc (virtual TB)** | A ride-level accumulator that increases after wins. It becomes TB when the ride is burned; it is forfeited on a loss or unlock. |
| **Base price** | A team's current engine price. It starts at 1.0 and changes after wins and losses. It informs reserve pricing, the market sell cap, and burn Currency returns. |
| **base_at_lock** | The team's base price captured when a ride begins. It determines the Currency returned when that ride is burned. |
| **Reserve** | The finite primary supply of each team's Tokens. Once depleted, additional Tokens for that team must come from circulation. |
| **Common pool** | The house pool used to fund Currency returned by burns. Its funding and deficit-handling mechanics are specified by the engine/economy design. |

### Economy rules

#### 5.1 Finite supply of Tokens

- Each team has a finite Token reserve.
- Tokens enter circulation through purchases from that reserve.
- When a team's reserve is empty, players can acquire that team's Tokens only from other players through the
  market, once the market is available.
- Reserve scarcity is intended to make team assets meaningfully limited.

#### 5.2 No guaranteed price floor

- A reserve purchase is priced at the team's current `base`.
- A burn returns Currency using the ride's frozen `base_at_lock` multiplied by the Tokens burned.
- Neither value is guaranteed to remain at or above 1.0.
- A team's price can fall substantially after losses. The design intentionally allows losing teams' Tokens to
  become very cheap.

#### 5.3 Burn and re-entry are not equivalent

- A burn returns Currency using the price captured when the ride was locked.
- Re-entering the same team later costs the current price.
- Because the current price may have changed, burning and buying again can reduce a player's Token position.
- This **burn-buy-back gap** is intended to support scarcity and limit runaway accumulation by large holders.

#### 5.4 Closed Currency economy

The intended economy is a closed loop:

- The only planned Currency injection is the one-time registration grant.
- Burns return Currency from the common pool.
- Reserve purchases transfer Currency into the economy's pool.
- Token destruction on a loss destroys Tokens, not Currency.
- There is no recurring faucet, yield, or player reward paid in Currency.

The common-pool deficit mechanism is described as mechanical safety rather than a player-facing faucet. Its
exact accounting must remain consistent with the closed-loop principle and should be made explicit in the
engine/economy specification.

---

## 6. The Player Loop

This section lists the operations a player performs during a team's Action Phase and the trade-offs behind the
three choices available after a win.

### 6.1 Actions during the Action Phase

Players can:

- **Lock** Tokens for a team's next scheduled match during that team's Action Phase. This creates a Ride.
- **Ride, burn, or unlock** eligible winning Rides during the relevant team's Action Phase. The Action Phase
  opens one configured post-result buffer after the official result is committed. If the player makes no
  decision before the phase closes, the Ride is automatically burned.
- **Trade** Tokens on the player market **[Market]**.

Each team has its own Action and Match phases. During a team's Match Phase, players cannot create a new Ride
or continue an existing Ride for that team. The market remains open **[Market]**.

### 6.2 The decision after a win

After a ride wins, the player chooses what to do next:

| Choice | Outcome | Trade-off |
|---|---|---|
| **Ride** | Continue the chain into another match. The existing acc and stake remain committed. | More upside, but a later loss forfeits all acc and destroys a portion of the locked Tokens. |
| **Burn** | Convert the locked Tokens and acc into TB. Return Currency using `base_at_lock × tokens`. | Secure the score now, but give up all future upside from the ride. |
| **Unlock** | Return the remaining Tokens to the player's wallet. | Preserve the Tokens, but forfeit all acc. Tokens may be sold at a premium once the market exists. |

**Market dependency:** Before the market ships, unlock has limited strategic value because players cannot sell
the returned Tokens. The complete ride/burn/unlock decision becomes meaningful only with the market, and the
UI should make this limitation clear.

---

## 7. Ride Rules

This section defines what a ride is and how it behaves on a win, on a loss, and when the player takes no
action. A ride is one commitment of Tokens to a match, potentially continued across multiple wins; the locked
Token amount remains fixed for the life of the ride.

### 7.1 On a win

A win increases the ride's acc using:

```text
acc_delta = tokens_locked × (odds_settled − 1) × (1 + streak × s)
```

Where:

- `tokens_locked` — Number of Tokens committed to the ride; fixed across the chain.
- `odds_settled` — Settled bookmaker odds for the winning match.
- `streak` — Zero-indexed position of this win in the chain. The first win uses `0`.
- `s` — Streak coefficient. Initial value: `0.20`; simulation-owned.

The streak multiplier increases linearly with each consecutive win. The first win earns the base contribution;
later wins earn progressively larger contributions.

The design hypothesis is that:

- Favourite-team rides can continue for several wins, creating a lower-variance strategy.
- Underdog rides are more likely to be burned after the first win, realizing a rarer, larger payout.
- Long streaks create meaningful upside but remain exposed to the loss rule.

Any stated typical chain lengths or burn thresholds are simulation outcomes, not guarantees.

### 7.2 On a loss

When a match in a ride is lost:

- **25% of locked Tokens are destroyed** by default (`X = 25%`; simulation-owned).
- All accumulated acc is forfeited.
- The remaining Tokens return to the player's wallet.
- The ride ends.

The loss rule makes locking a real commitment from the first round.

### 7.3 If the player takes no action: auto-burn

If a player does not act before the applicable deadline, the winning ride is automatically burned:

- Locked Tokens and acc become TB.
- Currency is returned using `base_at_lock × tokens`.

Auto-burn is intended to protect inactive players from losing an already-earned winning ride merely because
they missed a decision deadline. It also caps their opportunity to continue a streak.

### 7.4 Intended risk/reward profile

The game aims to support two broad approaches:

- **Favourite strategy:** lower variance, longer rides, and more modest acc per ride.
- **Underdog strategy:** higher variance, shorter rides, and occasional large first-win rewards.

The design rationale is that odds influence both expected acc and expected Token loss, while the variance of
outcomes differs, and the streak factor adds further upside to longer winning chains.

Illustrative simulation assumptions in the current design include approximately six-win favourite chains and
an underdog example of roughly 25 acc per 10 Tokens at odds of 3.5 on a first win. These are examples to
validate, not guaranteed outcomes or player-facing promises.

---

## 8. Championships and Leaderboards

This section defines the two season-long championships, the leaderboards that rank them, and the tie-breakers
the MVP uses.

### 8.1 Team Championship

A team's basket is the sum of all TB credited to that team.

- Every Token burned contributes 1 TB to its team's basket.
- The ride's acc is added to the same team basket.
- At Launch, only Ride burns contribute TB. Burning un-ridden Tokens directly from a wallet (wallet-burn) is
  not an available Launch operation.
- There is **no basket cap** in the current design.

The design expects two balancing effects:

- **Defensive trading [Market]:** Rivals may buy and sell a team's Tokens, increasing the cost for that team's
  supporters to re-arm.
- **Cheap underdog entry:** Losing teams' Tokens can become cheap, and a successful high-odds Ride can produce
  a large TB reward.

Wallet-burn, as a possible additional basket-filling mechanic, is deferred to playoff design and is not part
of Launch. These effects are hypotheses to test in simulation; defensive trading must not be treated as a
proven balancing mechanism until market depth and championship outcomes have been validated.

### 8.2 Player Championship

A player's score is their total TB earned from burns.

- Currency does not count toward the Player Championship.
- The same burn contributes to the player's total and the relevant team's basket.

### 8.3 Leaderboards

| Board | What it ranks | Filters / notes |
|---|---|---|
| **Team** | Teams by basket TB | One entry per team |
| **Player** | Players by total TB | Includes a rookie filter based on join date |
| **Team Contributors** | Players who burned TB for a selected team | Shows each player's favourite-team allegiance; filter for supporters or rivals |
| **Net Worth [Market]** | Players by wallet Currency plus the value of their holdings | Ships with the market |

The MVP includes the first three boards. Net Worth ships with the market.

### 8.4 MVP tie-breakers

- Player leaderboard: higher TB first, then oldest account.
- Team leaderboard: higher TB first, then team ID.
- More complete tie-break rules are deferred to post-launch.

---

## 9. Player Market [Market]

This section describes the player-to-player order book that ships at end-of-MVP priority 1, its pricing and
fee rules, and the market-depth question that simulation must answer.

The market is the next major system after the MVP ride loop and is **end-of-MVP priority 1**. It is a full
player order book for each team.

The architecture should allow the market to be added without redesigning the core ride and economy concepts.

### Market rules

- **Order book:** player-to-player, organized by team.
- **Maximum sell price:** **2× current base**.
- **Buy-price cap:** none for players.
- **Reserve fallback:** the reserve provides a fallback ask at the current base price; it does not place bids.
- **Fees:** **1% taker / 0% maker**.
- **Intended play styles:**
  - Player market-making is an intended play style.
  - Defensive trading is intended player-versus-player strategy, not abuse.
- **Leaderboard:** the Net Worth board ships alongside the market.

### Market depth is an open simulation question

Before the market exists, players are expected to hold mostly their favourite team's Tokens. Cross-team
holdings may therefore be too limited to create a sufficiently deep seller side.

The intended defensive-trading effect depends on speculative accumulation and resale of rival-team Tokens.
Whether that activity is strong enough to slow dominant teams is a simulation question—not an established
design fact.

If market depth is insufficient, possible post-launch levers include:

- Rival-team starter Tokens.
- A market-activation grant.
- Staking that naturally creates additional sellers.

---

## 10. Season End and Playoffs

This section defines what happens to rides when the regular season ends and which playoff concepts are
deferred.

### 10.1 Season-end auto-burn

After the official result of the last regular-season match has been committed:

- All pending winning rides are automatically burned.
- Locked Tokens and accumulated acc become TB.
- Currency is returned using `base_at_lock × tokens`.

There is no post-season harvest window in the current design.

### 10.2 Playoffs [Post-launch]

Playoff rules are not part of the MVP. The concepts under consideration are:

- A cascading burn competition.
- Freezing Tokens associated with eliminated teams.
- Wallet-burns by fans of eliminated teams.
- Continued rides using surviving teams.

These concepts carry unresolved questions — for example whether wallet-burns may raise a team's basket target,
and whether riding is restricted to a team's own supporters — recorded in §15.2 Playoffs and wallet-burns.

---

## 11. Registration, Participation, and Anti-Abuse

This section defines how a player joins the game, how late joiners are treated, which engagement features are
deferred, and which constraints guard the economy against abuse.

### Registration

- Each player selects a **favourite team** at registration.
- The favourite team is locked for the season.
- The team's logo represents the player's allegiance throughout the experience.
- Each player receives **50 Currency** as a one-time registration grant.

### Late joiners

The current design accepts that late joiners may be priced out of popular teams and steered toward cheaper
underdogs. Two responses exist in the current design:

- The rookie leaderboard filter gives new players a separate competitive view.
- A median-scaled registration grant is a possible future lever if retention data shows that late joiners are
  disadvantaged enough to churn.

### Notifications and session features [Post-launch]

Planned engagement features include:

- Ride resolved: e.g. “Won vs Madrid — ride or burn?”
- Action deadline reminder.
- Favourite-team match result.
- Live match centre.

### Anti-abuse principles

- One account per person, using email or SSO.
- API rate limits.
- No trading API at launch.
- Market sell cap, reserve fallback, and price movement are intended to limit manipulation.
- No special whale rules are planned. The intended structural constraints are:
  - Burn-buy-back gap.
  - Token destruction on losses.
  - Finite team reserves.
- The closed Currency economy is intended to prevent repeatable Currency creation through gameplay.

---

## 12. Simulation-Owned Parameters

This section lists the starting parameter values that simulation must validate, the interactions simulations
must examine, and the season-shape decision already made for the application build. Historical EuroLeague data
is the pre-launch simulation corpus; the values below are starting assumptions, not immutable design
decisions.

> **Application configuration — product decision (2026-11-23).** The application build uses **20 teams and 38
> rounds**: a double round-robin in which every team plays every other team once at home and once away. The
> EuroLeague corpus description and the parameters below stay as historical context and simulation starting
> points; the 20-team, 38-round shape is the configuration the application is built for.

| Parameter | Starting value | Simulation must validate |
|---|---:|---|
| Loss destruction (`X`) | 25% | Reserves survive the season; favourite-team play remains painful but viable |
| Streak coefficient (`s`) | 0.20 | Chain lengths, jackpot sizes, and championship balance |
| Base win multiplier | ×1.05 | Price divergence becomes meaningful around rounds 5–8 without runaway growth |
| Base loss multiplier | ×0.95 | Price divergence is meaningful without runaway decline |
| Token reserve coefficient (`K`) | 200 | Popular-team reserve depletion timing relative to season length |
| Registration grant (`G`) | 50 Currency | A player can participate throughout the season on the initial grant |
| Market sell cap | 2× base | Players cannot corner low-base teams; the cap supports a meaningful seller side |
| Taker fee | 1% | Market-making is viable without suppressing trading volume |

### Interactions simulations must examine

- **Loss destruction and reserve pacing:** losses accelerate Token destruction and affect how quickly reserves
  deplete.
- **Streak payouts and score distribution:** long chains increase acc and may concentrate championship scores.
- **No price floor:** losing teams can approach near-zero base prices. The UI must communicate falling prices,
  and simulations must test for exploitable loops.
- **Currency liquidity:** total supply is fixed after onboarding. Simulations must verify that liquidity does
  not dry up before the market ships.
- **Underdog sustainability:** cheap re-entry should allow underdog supporters to remain active late in the
  season.

Simulation results should be recorded separately from design intent. If a starting value changes, update the
engine specification and the relevant simulation configuration as well as this reference where appropriate.

---

## 13. Delivery Phases

This section maps the game's features onto the three delivery phases: Launch, Market, and Post-launch.

| Phase | Scope |
|---|---|
| **[Launch]** | Full ride loop (lock, ride, burn, unlock); streak-based acc; loss destruction; auto-burn; finite reserves; closed Currency economy; Team and Player Championships; Team, Player, and Team Contributors boards; season-end auto-burn |
| **[Market]** | Player order book; Net Worth board; defensive trading. End-of-MVP priority 1 |
| **[Post-launch]** | Staking; configurable idle policy; notifications; live match centre; full tie-breakers; registration-grant and basket-cap levers; additional sinks/fees; season rollover; playoffs |

---

## 14. Deferred and Rejected Design

This section records which mechanics were deferred and which were rejected, with the reason for each decision,
so future contributors do not reintroduce previously considered mechanics without understanding why they were
set aside.

### 14.1 Deferred

#### Staking [Post-launch]

Passive Currency yield on unlocked Tokens is deferred because:

- A non-TB income stream may conflict with the principle that TB is the only meaningful reward.
- Its funding source is unresolved in a closed Currency economy.
- Passive yield could compete with active riding and reduce jackpot-seeking.

Open staking questions — funding source, yield formula, magnitude, unstake friction, and market seeding — are
recorded in §15.1 Staking.

#### Playoffs [Post-launch]

Playoff mechanics require a dedicated design session. Current concepts are:

- Cascading burns.
- Eliminated-team freezes.
- Wallet-burns.
- Rides on surviving teams.

#### Other deferred levers and features

- Idle-policy setting (auto-ride instead of auto-burn).
- Live match centre.
- Full tie-break rules.
- Median-scaled registration grant.
- Team basket cap, if dominant teams pull too far ahead.
- Additional sinks or fees beyond the taker fee.
- Season-two rollover rules.

### 14.2 Rejected

| Proposal | Reason for rejection |
|---|---|
| **Pre-season team-specific pricing** | Adds calibration risk and another data dependency. All teams start at 1.0 and diverge through play. |
| **Wound mechanic** | The loss-destruction rule provides one uniform risk mechanic. |
| **Separate “Who Burned For Us” board** | Its purpose is covered by the Team Contributors board and its allegiance filter. |
| **Losses credit the opponent's basket** | Breaks clear attribution of which team's supporters generated its basket. |
| **1.0 burn-price floor** | Creates a positive-EV exploit when a team's base falls below 1.0. The design instead allows the burn return to follow `base_at_lock`, avoiding an asymmetric guaranteed mint. |
| **Dividend** | Adds a second income stream, competes with TB, and risks making Currency the primary reward. It also adds a rule and simulation lever. |
| **Post-season harvest window** | Lets players hold rides and concentrate high streak-bonus burns in a short final window. Season-end auto-burn better preserves the season-long competition. |

---

## 15. Open Questions

This section lists the unresolved design problems — staking, playoff and wallet-burn behavior, an alternative
registration model, and match reliability. They are not MVP requirements and should not be treated as settled
behavior.

### 15.1 Staking

- **Funding source:** Mint from the common pool (breaks the closed loop), skim from active riders (taxes
  active play), or draw down reserves (self-capping but potentially confusing).
- **Yield formula:** A simple input such as `base × p(win)` or a multi-input formula involving base, streak,
  and standings.
- **Magnitude:** Yield must remain below active jackpot-hunt EV on the TB axis, or staking may dominate. On
  the Currency axis, it must be competitive enough to have a purpose without overwhelming active play.
- **Unstake friction:** Cooldown rounds versus immediate sale through the market.
- **Market seeding:** Stakers could become sellers when market demand peaks. This only helps market depth if
  the yield is attractive enough to bring casual holders into staking.

### 15.2 Playoffs and wallet-burns

- **Cascading burn competition:** Can fans of eliminated teams burn frozen Tokens to increase their team's
  basket target and raise the bar for remaining teams?
- **Currency asymmetry:** Higher-base teams return more Currency on wallet-burns; underdog teams return less
  Currency but may offer more TB per Token.
- **Buy-to-burn behavior:** Can players buy their own team's Tokens specifically to burn them into the basket?
  Should this be unrestricted, capped, or limited to existing holdings?
- **Open riding:** Can fans of eliminated teams ride Tokens belonging to surviving teams, or is riding
  restricted to supporters of those teams?
- **Championship decision condition:** What mathematical condition determines whether wallet-burns or
  ride-burns decide the Team Championship? Simulation must validate it.

### 15.3 Alternative registration model

An alternative model would grant every player **10 Currency plus 5 Tokens for every team**, with the market
available at launch.

This changes the player identity from a fan-bettor to a portfolio manager and requires the market in the MVP.
It is deferred until retention data from the fan-bettor model is available.

### 15.4 Match reliability

The MVP assumes the schedule and result feed is authoritative once a result is committed. Postponed fixtures
have a defined effect on team phases:

- They do not start a Match Phase.
- They are skipped when finding the next match window.

The handling of a Ride already committed to a postponed fixture is unresolved, and cancelled, voided, and
corrected-result handling is not defined.

Questions requiring a dedicated design decision include:

- What happens to a Ride already committed to a postponed match: does it remain pending, move to the
  rescheduled fixture, or resolve another way?
- How should a voided or corrected result affect an already-committed Ride?
- How do makeup fixtures affect round grouping and action deadlines?

The engine specification treats committed results as final at Launch. A post-launch administrative
reconciliation operation may handle rare corrected-result cases. This assumption must be revisited before
using a league or feed with known reliability gaps.

---

## 16. Domain Glossary

This table defines the domain terms that the design and engine documents use, so each term can be read without
relying on another section.

| Term | Definition |
|---|---|
| **Action Phase** | Team-specific period when players can create Rides and manage eligible winning Rides. It opens after the configured post-result buffer and closes at the pre-match buffer. |
| **Base price (`base`)** | Current price value for a team's Token economy. |
| **`base_at_lock`** | Base price captured when a ride is created; used to calculate its Currency return on burn. |
| **Burn** | Finalize a ride or wallet holding: convert Tokens and any ride acc into TB. |
| **Common pool** | Shared house pool used to fund Currency returns. |
| **Currency** | Spendable in-game money; not a score. |
| **`acc`** | Virtual TB accumulated by a ride and realized only on burn. |
| **Match Phase** | Team-specific lock-closed period from the pre-match buffer until the configured post-result buffer after the official result is committed. |
| **Reserve** | Finite primary supply of a team's Tokens. |
| **Ride** | A commitment of a fixed Token amount to a match, potentially continued across consecutive wins. |
| **Streak** | Number of consecutive wins in a ride; used by the acc formula. |
| **TB** | The only score unit in the game. |
| **Team basket** | Total TB credited to a team. |
| **Token** | Team-specific asset used for rides, trading, and burns. |
| **Unlock** | End a winning ride, return its remaining Tokens to the wallet, and forfeit its acc. |
| **Wallet-burn** | A proposed post-launch/playoff operation to burn Tokens held in a wallet without first using them in a Ride. Not available at Launch. |

---

## 17. Design Invariants

The following statements summarize the rules that should remain true across product, engine, and
implementation discussions:

1. **TB is the only championship score.** Currency and Net Worth never determine either championship.
2. **TB is created by burning.** A burn contributes the number of Tokens burned plus any accumulated ride acc.
3. **A burn scores for both sides.** The same TB contributes to the player's total and the relevant team's
   basket.
4. **A ride's Token commitment is fixed.** The locked Token amount does not change as the ride continues.
5. **A loss ends the ride.** It destroys the configured share of locked Tokens and forfeits all acc.
6. **Unlock forfeits acc.** The Tokens return to the wallet; acc does not.
7. **Burn returns Currency at the locked base.** It uses `base_at_lock`, not the team's current base.
8. **There is no guaranteed 1.0 price floor.** Team base prices may fall below 1.0.
9. **Team Token reserves are finite.** Once depleted, primary reserve supply is unavailable for that team.
10. **The MVP ends with automatic realization.** Pending winning rides are auto-burned after the final
    regular-season match's official result has been committed.
11. **Market behavior is phase-gated.** Player-to-player trading and Net Worth are [Market], not [Launch].
12. **Simulation assumptions are not promises.** Starting coefficients, expected chain lengths, and balance
    outcomes must be validated empirically.
13. **Wallet-burn is not a Launch operation.** At Launch, TB comes from Ride burns; wallet-burn remains
    deferred to playoff design.

---

## 18. Editorial Notes for the Specification Set

The following points should be reconciled across the design and engine documents before implementation is
considered complete:

- **Common-pool deficit handling vs. “no mint”:** The economy section says the common pool may mint on
  deficit, while the closed-loop principle says there is no mint. Specify whether this is an accounting
  backstop, a bounded liability, or actual Currency creation, and use consistent language.
- **Wallet-burn:** Not available at Launch. Its rules, eligibility, and championship effects remain part of
  the deferred playoff design.
- **“Stake” terminology:** The source uses “stake” for the locked Token commitment and for the Currency
  returned on burn. Prefer “locked Tokens” for the former and “Currency return” for the latter to avoid
  ambiguity.
- **Pre-market unlock value:** The source says a loss returns remaining Tokens anyway, making unlock after a
  win nearly dead before the market. Confirm the exact unlock availability and player rationale in the engine
  rules and UX.
- **Board count:** The MVP has three boards; Net Worth is added with [Market]. Keep roadmap tables and UI
  scope aligned.
- **Simulation examples:** Values such as six-win chains and 25 acc per 10 Tokens at 3.5 odds should be
  labelled as observed or target simulation outputs, never as deterministic rules.

These are editorial and specification-alignment checks; they do not change the intended game rules by
themselves.
