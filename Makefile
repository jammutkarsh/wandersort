APP_NAME := wandersort
IMAGE := $(APP_NAME):latest
BINARY := bin/server
GO_MAIN := ./cmd

.PHONY: help build swagger lint test run

help:
	@printf "Usage:\n"
	@printf "  make run               Run the server (builds if binary not found)\n"
	@printf "  make test              Run all tests\n"
	@printf "  make build             Build the binary locally\n"
	@printf "  make lint              Run gofmt -s -d -e -w .\n"
	@printf "  make swagger		 Generate Swagger docs (swag required)\n"

build:
	@echo "Building the binary locally"
	mkdir -p $(dir $(BINARY))
	go build -ldflags='-s -w' -o $(BINARY) $(GO_MAIN)

test:
	go test -v ./...

swagger:
	@which swag >/dev/null 2>&1 || (echo "Swag CLI not found. Install with 'go install github.com/swaggo/swag/cmd/swag@latest'"; exit 1)
	swag init -g cmd/main.go -o docs

lint:
	gofmt -s -d -e -w .

run:
	@if [ ! -f $(BINARY) ]; then $(MAKE) build; fi
	@./$(BINARY)

