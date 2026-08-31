.PHONY: run build test lint deploy

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
