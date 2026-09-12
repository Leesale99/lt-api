include .envrc

run/server:
	go run ./cmd/server -db-dsn=${LT_API_DSN}

psql: 
	psql ${LT_API_DSN}

up: 
	migrate -path ./migrations -database ${LT_API_DSN} up
