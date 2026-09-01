.PHONY: run worker test vet build fmt migrate-up migrate-new tidy

run:
	go run ./cmd/api

worker:
	go run ./cmd/worker

build:
	go build ./...

test:
	go test -race ./...

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