// Package e2e is the composition-root acceptance suite: compose_test.go wires
// the real modules over the real Postgres test pools exactly like
// cmd/api/main.go does (moved verbatim from internal/platform/server).
//
// It runs only when LT_API_TEST_DSN is set (the internal/testdb contract) and
// is a test-only package — every file here is a _test.go apart from this doc
// comment, and it holds no production code.
package e2e
