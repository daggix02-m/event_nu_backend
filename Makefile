.PHONY: run worker test vet build fmt migrate-up migrate-new tidy

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