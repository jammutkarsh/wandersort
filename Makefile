APP_NAME := wandersort
IMAGE := $(APP_NAME):latest
BINARY := bin/wandersort
GO_MAIN := .
GOOS_LIST := linux darwin windows

# Absolute, not relative: go test runs each package with that package's own
# directory as cwd, so a relative test/deps would resolve differently (or
# not at all) depending which package is under test.
TEST_DEPS_DIR := $(CURDIR)/test/deps

.PHONY: help build build-all install lint test test-deps race run

help:
	@printf "Usage:\n"
	@printf "  make run               Run wandersort (builds if binary not found)\n"
	@printf "  make test              Run all tests\n"
	@printf "  make test-deps         Pre-download exiftool/location.db into test/deps\n"
	@printf "  make race         Run all tests with the race detector (CI)\n"
	@printf "  make build             Build the binary locally\n"
	@printf "  make build-all         Cross-build binary for linux/darwin/windows\n"
	@printf "  make install           Install binary via go install\n"
	@printf "  make lint              Run gofumpt -l -w .\n"

build:
	@echo "Building the binary locally"
	mkdir -p $(dir $(BINARY))
	go build -ldflags='-s -w' -o $(BINARY) $(GO_MAIN)

build-all:
	mkdir -p $(dir $(BINARY))
	@for goos in $(GOOS_LIST); do \
		echo "Building for $$goos"; \
		ext=""; [ "$$goos" = "windows" ] && ext=".exe"; \
		GOOS=$$goos go build -ldflags='-s -w' -o $(BINARY)-$$goos$$ext $(GO_MAIN) || exit 1; \
	done

test-deps:
	@mkdir -p $(TEST_DEPS_DIR)
	go run ./scripts/fetchtestdeps $(TEST_DEPS_DIR)

test: test-deps
	WANDERSORT_TEST_DEPS_DIR=$(TEST_DEPS_DIR) go test -v ./...

# -count=1 defeats the test cache so races are actually re-detected each run
race: test-deps
	go vet ./...
	WANDERSORT_TEST_DEPS_DIR=$(TEST_DEPS_DIR) go test -race -count=1 ./...

install:
	go install ./...

lint:
	@which gofumpt >/dev/null 2>&1 || (echo "gofumpt not found. Install with 'go install mvdan.cc/gofumpt@latest'"; exit 1)
	gofumpt -l -w .

run:
	@if [ ! -f $(BINARY) ]; then $(MAKE) build; fi
	@./$(BINARY)

cover: test-deps
	WANDERSORT_TEST_DEPS_DIR=$(TEST_DEPS_DIR) go test -coverprofile=coverage.out -coverpkg=./... ./...
	go tool cover -func=coverage.out | grep total | awk '{print "Total coverage: " $$3}'