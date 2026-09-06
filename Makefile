.PHONY: run worker test vet build fmt migrate-up migrate-new tidy ci

run:
	go run ./cmd/api

worker:
	go run ./cmd/worker

build:
	go build ./...

test:
	# -p 1: packages run serially because integration tests share one dev DB.
	go test -race -p 1 ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

migrate-up:
	cd migrations && goose postgres "$$DATABASE_ADMIN_URL" up

migrate-new:
	@test -n "$(name)" || (echo "usage: make migrate-new name=<snake_case>" && exit 1)
	cd migrations && goose create "$(name)" sql

ci:
	# Local equivalent of the CI gate (phase-01): same commands the workflow
	# runs, against a local Postgres instead of the CI service container.
	# Bootstrapping app_user + running migrations is documented in README.md.
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is required (point at a local/CI Postgres)" && exit 1)
	@test -n "$$DATABASE_ADMIN_URL" || (echo "DATABASE_ADMIN_URL is required (migration/owner connection)" && exit 1)
	@out="$$(gofmt -l .)"; test -z "$$out" || (echo "Files needing gofmt:"; echo "$$out"; exit 1)
	go vet ./...
	go build ./...
	go test -race -p 1 -count=1 ./...