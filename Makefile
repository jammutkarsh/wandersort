APP_NAME := wandersort
IMAGE := $(APP_NAME):latest
BINARY := bin/wandersort
GO_MAIN := .

.PHONY: help build swagger lint test run

help:
	@printf "Usage:\n"
	@printf "  make run               Run the server (builds if binary not found)\n"
	@printf "  make test              Run all tests\n"
	@printf "  make build             Build the binary locally\n"
	@printf "  make lint              Run gofumpt -l -w .\n"
	@printf "  make swagger		 Generate Swagger docs (swag required)\n"

build:
	@echo "Building the binary locally"
	mkdir -p $(dir $(BINARY))
	go build -ldflags='-s -w' -o $(BINARY) $(GO_MAIN)

test:
	go test -v ./...

swagger:
	@which swag >/dev/null 2>&1 || (echo "Swag CLI not found. Install with 'go install github.com/swaggo/swag/cmd/swag@latest'"; exit 1)
	swag init -g internal/cli/serve.go -o swagger

lint:
	@which gofumpt >/dev/null 2>&1 || (echo "gofumpt not found. Install with 'go install mvdan.cc/gofumpt@latest'"; exit 1)
	gofumpt -l -w .

run:
	@if [ ! -f $(BINARY) ]; then $(MAKE) build; fi
	@./$(BINARY)

