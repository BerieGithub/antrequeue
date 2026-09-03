.PHONY: run build test tidy fmt vet docker-up docker-down

run:
	go run ./cmd/api

build:
	go build -o bin/antrequeue-api ./cmd/api

test:
	go test ./... -race -cover

tidy:
	go mod tidy

fmt:
	go fmt ./...

vet:
	go vet ./...

docker-up:
	docker compose up -d

docker-down:
	docker compose down
