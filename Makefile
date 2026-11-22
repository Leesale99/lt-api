# `-` = optional: .envrc is host-side only (not committed); CI sets the
# variables it needs at job level instead.
-include .envrc

# ============================================================================================================================ #
# HELPERS
# ============================================================================================================================ #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed 's/^/ /'

.PHONY: confirm
confirm: 
	@echo 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]


# ============================================================================================================================ #
# DEVELOPMENT
# ============================================================================================================================ #

## run/api: run the cmd/api application
.PHONY: run/api
run/api: 
	go run ./cmd/api -db-dsn=${LT_API_DSN} -smtp-username=${SMTP_USERNAME} -smtp-password=${SMTP_PASSWORD}

## test: run the full suite with -count=1 (never a cached ok); the spec suite FAILS without LT_API_TEST_DSN (bring it up with db/up)
.PHONY: test
test:
	go test -count=1 ./...

## test/unit: run the pure-logic tests with -count=1 (no database needed)
.PHONY: test/unit
test/unit:
	go test -count=1 ./internal/schedule/... -run 'TestValidateNewMatch|TestValidateMatchUpdate|TestWinner|TestPhase' -v

## test/db: run the full suite against the test database (LT_API_TEST_DSN from .envrc; fails loudly if unreachable)
.PHONY: test/db
test/db:
	go test -count=1 ./... -v

## test/spec: run the godog suite against real Postgres with -count=1 (never a cached ok; needs LT_API_TEST_DSN, `make db/up` provides it)
.PHONY: test/spec
test/spec:
	go test -count=1 ./internal/spec/...

## db/up: start the compose test database (CI's postgres 18 service container) and wait until it is healthy
.PHONY: db/up
db/up:
	docker compose up -d
	@echo 'waiting for the compose test database to report healthy ...'
	@for i in $$(seq 1 60); do \
		status=$$(docker inspect --format '{{.State.Health.Status}}' $$(docker compose ps -q postgres) 2>/dev/null || echo starting); \
		if [ "$$status" = healthy ]; then echo 'test database is healthy'; exit 0; fi; \
		sleep 2; \
	done; \
	echo 'test database did not become healthy; see: docker compose logs postgres' >&2; exit 1

## db/down: stop the compose test database (the named volume survives; compose down -v drops it too)
.PHONY: db/down
db/down:
	docker compose down
	@echo 'named volume kept: `docker compose down -v` also drops the base database'

## db/seed: reset and seed the dev database with the canonical dummy data (run db/migrations/up first)
.PHONY: db/seed
db/seed: confirm
	go run ./cmd/seed -db-dsn=${LT_API_DSN} -reset

## db/psql: connect to the database using psql
.PHONY: db/psql
db/psql: 
	psql ${LT_API_DSN}

## db/migrations/new name=$1: create a new database migration
.PHONY: db/migrations/new
db/migrations/new:
	migrate create -seq -ext=.sql -dir=./migrations/ ${name}

## db/migrations/up n=$1: apply N up migrations (or all if n is omitted)
.PHONY: db/migrations/up
db/migrations/up: confirm
	migrate -path ./migrations/ -database ${LT_API_DSN} up $(n)

## db/migrations/down n=$1: apply N down database migrations (explicit count required; n=all drops the whole schema)
.PHONY: db/migrations/down
db/migrations/down: confirm
ifeq ($(n),)
	$(error usage: make db/migrations/down n=1  (or n=all to drop the whole schema))
endif
	migrate -path ./migrations/ -database ${LT_API_DSN} down $(n)

## db/migrations/version: get database migrations version
.PHONY: db/migrations/version
db/migrations/version: 
	migrate -path ./migrations/ -database ${LT_API_DSN} version

## db/migrations/goto version=$1: migrate up or down to specific version
.PHONY: db/migrations/goto
db/migrations/goto: confirm
	migrate -path ./migrations/ -database ${LT_API_DSN} goto ${version}

## db/migrations/force version=$1: force specific version
.PHONY: db/migrations/force
db/migrations/force: confirm
	migrate -path ./migrations/ -database ${LT_API_DSN} force ${version}

# ============================================================================================================================ #
# QUALITY CONTROL
# ============================================================================================================================ #

## tidy: tidy module dependencies, and format and modernize all .go files
.PHONY: tidy
tidy:
	go mod tidy
	go mod verify
	go mod vendor
	go fix ./...
	go fmt ./...

# GOFMT: use the one shipped with the active toolchain, not PATH's — a
# system gofmt older than the toolchain cannot parse newer language
# features and misreports them as errors (e.g. generic methods in go1.27).
GOFMT := $(shell go env GOROOT)/bin/gofmt

## audit/static: run the static half of the merge gate (fails on any unformatted
## file, stale spec or spec-check error): go mod tidy -diff → go mod verify →
## gofmt → vet → staticcheck → spec freshness → spec check. It needs no database.
.PHONY: audit/static
audit/static:
	go mod tidy -diff
	go mod verify
	@test -z "$$( $(GOFMT) -l $$(git ls-files '*.go' | grep -v '^vendor/') )" || ( $(GOFMT) -l $$(git ls-files '*.go' | grep -v '^vendor/'); exit 1)
	go vet ./...
	go tool staticcheck ./...
	$(MAKE) spec/diff
	$(MAKE) spec/check

## audit: run quality control checks — the pipeline's single merge gate. It is
## the static half above plus the whole test tree (unit, spec, db), so gofmt →
## vet → staticcheck → spec freshness → spec check → go test. The test step runs
## with -count=1 and never trusts the go test cache.
.PHONY: audit
audit: audit/static
	go test -race -vet=off -count=1 ./...

# The two suites below are plain `node` harnesses (node's built-in .ts type
# stripping): the gate/receipt/policy suite plus the sandbox-seam suite (sbx
# argv + exit mapping, host integrity check, merge detector, host-only receipts).
## smoke: run the delegate extension's headless smoke suites — gate/receipt/policy plus the sandbox seams (sbx argv, integrity check, merge detector, host-only receipts); no database, no network, no real sandboxes
.PHONY: smoke
smoke:
	node .pi/extensions/delegate/smoke-gates.mjs
	node .pi/extensions/delegate/smoke-sbx.mjs
	node .pi/extensions/delegate/smoke-ui.mjs

# ============================================================================================================================ #
# SPEC CATALOG
# ============================================================================================================================ #

## spec/gen: regenerate the catalog's Markdown — the thin dashboard (totals by module/status + links
## into rules/<module>/); the rule files under docs/spec/rules/ are authored, never generated
.PHONY: spec/gen
spec/gen:
	go run ./cmd/spec gen

## spec/check: validate the catalog against rules.schema.md §3 (--strict: any policy error fails the audit)
.PHONY: spec/check
spec/check:
	go run ./cmd/spec check --strict

## spec/work: print the work queue in priority order (drifted → specified-only → broken pointer).
## args='R-XXX-NNN' prints one rule's work order instead (the overlay: resolved sites and
## verification pointers, refusal map, linked scenarios, gate); args='--module game' scopes it.
## It is a reading aid, never a verdict: the rules and their scenarios are the source of truth.
.PHONY: spec/work
spec/work:
	go run ./cmd/spec work $(args)

## spec/diff: fail if the generated dashboard is stale (wired into `make audit`)
.PHONY: spec/diff
spec/diff:
	go run ./cmd/spec gen --diff

## prove: run the DB-backed mutant registry — the slow verification gate. With no
## args it proves the seeded registry (--all); args override it, e.g. `make prove
## args='--base origin/main'` or `args='--rule R-SCHEDULE-003'`. NOT part of
## `make audit`; needs LT_API_TEST_DSN (see `make db/up`).
.PHONY: prove
prove:
	go run ./cmd/spec prove $(if $(args),$(args),--all)

# ============================================================================================================================ #
# BUILD
# ============================================================================================================================ #

.PHONY: build/api
build/api:
	go build -ldflags='-s' -o=./bin/api ./cmd/api
	GOOS=linux GOARCH=amd64 go build -ldflags='-s' -o=./bin/linux_amd64/api ./cmd/api/
