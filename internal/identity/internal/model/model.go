// Package model holds the identity context's entities, validators and
// vocabulary: users, their tokens, roles and permissions. It is the anemic,
// transport-free half of the layered module — no json tags (the HTTP shape is
// the handler's DTO, which replicates the frozen wire format verbatim), no
// pgx, no SQL.
//
// The context's sentinel errors live in errors.go. The repository layer
// translates Postgres facts into them, so identity-internal control flow
// (unknown user, lost version race, duplicate email) speaks this package's
// vocabulary rather than the shared persistence one.
package model
