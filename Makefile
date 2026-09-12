## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed 's/^/ /'

.PHONY: confirm
confirm: 
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

## run/api: run the cmd/api application
.PHONY: run/server
run/server: 
	go run ./cmd/server -db-dsn=${LT_API_DSN}

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

