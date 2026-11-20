package persistence

import "time"

// DBTimeout bounds one DATABASE-BUDGET window: the clock a request may spend
// on storage work (the middleware's auth/permission lookups and the module
// handlers' store calls both draw it from here, so the value cannot drift
// between modules).
//
// It is a database budget, not a request budget. CPU-bound crypto — the
// bcrypt hash on registration and the bcrypt compare on login — must run
// OUTSIDE this window: a cost-12 hash can eat the whole budget on a contended
// machine and turn a healthy INSERT into a spurious "context deadline
// exceeded". Callers therefore time only the DB section, never the crypto in
// between.
// rule:R-KERNEL-005
const DBTimeout = 3 * time.Second
