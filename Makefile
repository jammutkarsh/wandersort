APP_NAME := wandersort
IMAGE := $(APP_NAME):latest
BINARY := bin/server
GO_MAIN := ./cmd

.PHONY: help build docker-up docker-down docker-up-detach docker-up-test docker-down-test swagger lint migrate-create test

help:
	@printf "Usage:\n"
	@printf "  make build             Build the binary locally\n"
	@printf "  make image       Build the docker image ($(IMAGE))\n"
	@printf "  make docker-up         Run docker-compose up\n"
	@printf "  make docker-up-detach  Run docker-compose up -d\n"
	@printf "  make docker-down       Run docker-compose down\n"
	@printf "  make docker-up-test    Run tests via docker-compose.test.yml\n"
	@printf "  make docker-down-test  Tear down test compose\n"
	@printf "  make swagger              Generate Swagger docs (swag required)\n"
	@printf "  make lint              Run gofmt -s -d -e -w .\n"
	@printf "  make migrate-create NAME=<name>  Create DB migration SQL files\n"

build:
	@echo "Building the binary locally (no Docker image will be produced)"
	mkdir -p $(dir $(BINARY))
	go build -ldflags='-s -w' -o $(BINARY) $(GO_MAIN)

image:
	@echo "Building the Docker image $(IMAGE)"
	docker build -t $(IMAGE) .

test:
	@echo "Running test compose (brings up postgres + test runner)"
	@docker-compose -f docker-compose.test.yml up --abort-on-container-exit --build; rc=$$?; \
		docker-compose -f docker-compose.test.yml down --volumes --remove-orphans; \
		exit $$rc

swagger:
	@which swag >/dev/null 2>&1 || (echo "Swag CLI not found. Install with 'go install github.com/swag/cmd/swag@latest'"; exit 1)
	swag init -g cmd/main.go -o docs

lint:
	gofmt -s -d -e -w .

migrate-create:
	@if [ -z "$(NAME)" ]; then \
		echo "ERROR: NAME is required. Usage: make migrate-create NAME=descriptive_name"; exit 1; \
	fi
	migrate create -dir pkg/db/migrations -ext sql $(NAME)
