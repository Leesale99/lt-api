include .envrc

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed 's/^/ /'

.PHONY: confirm
confirm: 
	@echo 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

## run/api: run the cmd/api application
.PHONY: run/server
run/server: 
	go run ./cmd/server -db-dsn=${LT_API_DSN}

## test: run the full suite; DB-backed tests skip when LT_API_TEST_DSN is unset
.PHONY: test
test:
	go test ./...

## test/unit: run the pure-logic tests (no database needed)
.PHONY: test/unit
test/unit:
	go test ./internal/game -run 'TestValidateMatch|TestWinner' -v

## test/db: run the full suite against the test database (LT_API_TEST_DSN from .envrc; fails loudly if unreachable)
.PHONY: test/db
test/db:
	go test ./... -v

## db/psql: connect to the database using psql
.PHONY: db/psql
db/psql: 
	psql ${LT_API_DSN}

## db/migrations/new name=$1: create a new database migration
.PHONY: db/migrations/new
db/migrations/new:
	migrate create -seq -ext=.sql -dir=./migrations/ ${name}

## db/migrations/up: apply all up database migrations
.PHONY: db/migrations/up
db/migrations/up: confirm
	migrate -path ./migrations/ -database ${LT_API_DSN} up

## db/migrations/down: apply all down database migrations
.PHONY: db/migrations/down
db/migrations/down: confirm
	migrate -path ./migrations/ -database ${LT_API_DSN} down

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

