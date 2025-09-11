.PHONY: build test lint clean install dev setup-hooks

# Build variables
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

# Go variables
GOCMD := go
GOBUILD := $(GOCMD) build
GOTEST := $(GOCMD) test
GOLINT := $(shell go env GOPATH)/bin/golangci-lint run
GOMOD := $(GOCMD) mod

# Build the application
build:
	$(GOBUILD) -ldflags="$(LDFLAGS)" -o bin/cainban ./cmd/cainban

# Install the application
install:
	$(GOCMD) install -ldflags="$(LDFLAGS)" ./cmd/cainban

# Run tests
test:
	$(GOTEST) -race -cover ./...

# Run tests with verbose output
test-verbose:
	$(GOTEST) -race -cover -v ./...

# Run linting
lint:
	$(GOLINT)

# Run linting with fixes
lint-fix:
	$(GOLINT) --fix

# Clean build artifacts
clean:
	rm -rf bin/
	$(GOCMD) clean

# Development setup
dev: setup-hooks
	$(GOMOD) download
	$(GOMOD) tidy

# Setup pre-commit hooks
setup-hooks:
	@which pre-commit >/dev/null 2>&1 || (echo "Installing pre-commit..." && pip install pre-commit)
	pre-commit install

# Run all quality checks
quality: lint test

# Run the application in development mode
run:
	$(GOBUILD) -ldflags="$(LDFLAGS)" -o bin/cainban ./cmd/cainban && ./bin/cainban

# Build Docker image
docker-build:
	docker build -t cainban:$(VERSION) .

# Run Docker container
docker-run:
	docker run --rm -it -v $(HOME)/.cainban:/root/.cainban cainban:$(VERSION)

# Generate coverage report
coverage:
	$(GOTEST) -race -coverprofile=coverage.out ./...
	$(GOCMD) tool cover -html=coverage.out -o coverage.html

# Help
help:
	@echo "Available targets:"
	@echo "  build        Build the application"
	@echo "  install      Install the application"
	@echo "  test         Run tests"
	@echo "  test-verbose Run tests with verbose output"
	@echo "  lint         Run linting"
	@echo "  lint-fix     Run linting with fixes"
	@echo "  clean        Clean build artifacts"
	@echo "  dev          Setup development environment"
	@echo "  setup-hooks  Setup pre-commit hooks"
	@echo "  quality      Run all quality checks (lint + test)"
	@echo "  run          Build and run the application"
	@echo "  docker-build Build Docker image"
	@echo "  docker-run   Run Docker container"
	@echo "  coverage     Generate test coverage report"
	@echo "  help         Show this help message"