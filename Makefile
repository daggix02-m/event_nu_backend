# Defaults point at the CI-equivalent local Postgres from docker-compose.yml;
# anything already exported in the environment wins (the `?=` only applies
# when unset), matching the secrets CI injects.
DATABASE_ADMIN_URL ?= postgres://postgres:postgres@127.0.0.1:55432/event_nu_test?sslmode=disable
DATABASE_URL ?= postgres://app_user:app_user_pass@127.0.0.1:55432/event_nu_test?sslmode=disable
JWT_SECRET ?= ci-only-test-secret-never-used-in-production
EMAIL_PROVIDER ?= noop

export DATABASE_ADMIN_URL DATABASE_URL JWT_SECRET EMAIL_PROVIDER

.PHONY: run worker test vet build fmt migrate-up migrate-new db-up db-down tidy ci

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

db-up:
	# CI-equivalent Postgres 16: up, wait for readiness, create the RLS
	# app_user role + default privileges, then apply migrations.
	docker compose -f ../docker-compose.yml up -d
	@echo "waiting for Postgres..."
	@until docker exec event_nu_ci_pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done
	psql "$$DATABASE_ADMIN_URL" -f scripts/bootstrap_app_user.sql
	cd migrations && goose postgres "$$DATABASE_ADMIN_URL" up

db-down:
	docker compose -f ../docker-compose.yml down

ci:
	# Local equivalent of the CI gate (phase-01): same commands the workflow
	# runs, against a local Postgres instead of the CI service container.
	# Bootstrapping app_user + running migrations is documented in README.md.
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL is required (point at a local/CI Postgres)" && exit 1)
	@test -n "$$DATABASE_ADMIN_URL" || (echo "DATABASE_ADMIN_URL is required (migration/owner connection)" && exit 1)
	@out="$$(gofmt -l .)"; test -z "$$out" || (echo "Files needing gofmt:"; echo "$$out"; exit 1)
	go vet ./...
	go build ./...
	# -timeout 20m: the DB-backed handlers suite runs against a shared remote
	# dev DB whose published-event count grows on every run, pushing the page
	# walk past go test's 10m default; the same suite is fast against the local
	# CI Postgres.
	go test -race -p 1 -count=1 -timeout 20m ./...