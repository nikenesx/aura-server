.PHONY: run build test lint deploy up down

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

lint:
	golangci-lint run

up:
	docker compose up --build

down:
	docker compose down

deploy:
	./scripts/deploy.sh
