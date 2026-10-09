.PHONY: run build test lint deploy docker-up docker-down

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

lint:
	golangci-lint run

deploy:
	./scripts/deploy.sh

docker-up:
	docker compose up --build -d --wait --wait-timeout 1800

docker-down:
	docker compose down
