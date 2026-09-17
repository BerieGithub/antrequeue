.PHONY: run build test test-race tidy fmt vet docker-up docker-down

run:
	go run ./cmd/api

build:
	go build -o bin/antrequeue-api ./cmd/api

# Portable: runs anywhere Go does.
test:
	go test ./... -cover

# What CI gates on. Needs cgo and a C toolchain (gcc or clang), so it will not
# run on a bare Windows checkout; use `make test` there and let CI catch races.
test-race:
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
