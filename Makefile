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
	rm -rf bin/ .build/
	$(GOCMD) clean

# Build the Lambda bootstrap (pure Go, arm64, provided.al2023).
# Output goes to .build/lambda/bootstrap, which the CDK stack packages via
# Code.fromAsset("../.build/lambda"). Run this before `cdk synth`/`cdk deploy`.
.PHONY: lambda
lambda:
	mkdir -p .build/lambda
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -tags lambda.norpc \
		-o .build/lambda/bootstrap ./cmd/cainban-lambda

# Build the Cognito pre-token-generation trigger bootstrap (pure Go, arm64,
# provided.al2023). Output goes to .build/pretoken/bootstrap, packaged by the
# CDK stack via Code.fromAsset("../.build/pretoken"). This trigger maps a user's
# custom:repos / custom:default_repo attributes into the top-level repos /
# default_repo claims the auth validator authorizes against.
.PHONY: pretoken
pretoken:
	mkdir -p .build/pretoken
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -tags lambda.norpc \
		-o .build/pretoken/bootstrap ./cmd/cainban-pretoken

# Build the API Gateway WebSocket API Lambda bootstrap (pure Go, arm64,
# provided.al2023). Output goes to .build/ws/bootstrap, packaged by the CDK
# stack via Code.fromAsset("../.build/ws"). This is the $connect/$disconnect/
# $default handler that manages the WebSocket connection registry in DynamoDB
# for the serverless real-time push path (the MCP Lambda does the actual push).
.PHONY: ws
ws:
	mkdir -p .build/ws
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -tags lambda.norpc \
		-o .build/ws/bootstrap ./cmd/cainban-ws-lambda

# Build every Lambda bundle the CDK stack packages. Run before `cdk synth`/`cdk deploy`.
# NOTE: connect Lambda was removed in Phase 5 (GitHub-native permissions).
.PHONY: bundles
bundles: lambda pretoken ws

# Verify the LIVE API edge after a deploy: asserts the CORS preflight succeeds
# (unauthenticated OPTIONS -> 2xx with CORS headers) AND that the data path is
# still auth-gated (unauthenticated request -> 401). Endpoints are read from the
# deployed CloudFormation stack outputs, so no URLs are hardcoded. Requires AWS
# creds that can read the stack (AWS_PROFILE / AWS_REGION) + curl. Run this after
# every `cdk deploy` of the MCP/Connect APIs.
#   make verify-deploy                   # defaults to CainbanPhase2Stack
#   STACK=... ORIGIN=... make verify-deploy
.PHONY: verify-deploy
verify-deploy:
	./infra/scripts/verify-deploy.sh

# Self-service reset of the stale cainban OAuth grant kiro-cli caches under
# ~/.aws/sso/cache/. Use when cainban tools 401 / show "Not verified" and no
# consent banner fires. Removes ONLY cainban's grant (matched by issuer host),
# then re-authorize from a fresh chat. Override env with CAINBAN_ISSUER_HOST=.
.PHONY: reauth-cainban
reauth-cainban:
	./infra/scripts/reauth.sh

# One canonical deploy flow: build every Lambda bundle, cdk deploy the stack,
# then AUTOMATICALLY run verify-deploy as the final gate so a broken live edge
# (e.g. a CORS preflight the authorizer 401s) can never ship unnoticed. Use this
# instead of a bare `cdk deploy` — it guarantees the bundles are fresh and the
# edge is smoke-checked. The CDK CLI and stack are overridable:
#   make deploy
#   CDK="npx -y aws-cdk@2.1143.0" STACK=CainbanPhase2Stack make deploy
# Requires AWS creds (AWS_PROFILE / AWS_REGION) that can deploy + read the stack.
# NOTE: review `cdk diff` yourself BEFORE this — deploy does not gate on the diff.
CDK ?= npx -y aws-cdk@2.1143.0
STACK ?= CainbanPhase2Stack
.PHONY: deploy
deploy: bundles
	cd infra && $(CDK) deploy $(STACK) --require-approval never
	STACK=$(STACK) $(MAKE) verify-deploy

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
	@echo "  verify-deploy Smoke-check the live API edge (CORS preflight + auth gate)"
	@echo "  reauth-cainban Reset the stale cainban OAuth grant (re-authorize from a fresh chat)"
	@echo "  deploy       Build bundles, cdk deploy, then auto-run verify-deploy"
	@echo "  help         Show this help message"