SHELL := /bin/bash

# Where `go install` puts binaries. Used by `make install` and `make uninstall`.
GOBIN := $(shell go env GOPATH)/bin
BIN   := $(GOBIN)/pulse

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: dev
dev: ## Compile and run from source (no install). Use during iteration.
	@go run ./cmd/pulse

.PHONY: doctor
doctor: ## Run the setup health check
	@go run ./cmd/pulse doctor

.PHONY: usage
usage: ## One-shot fetch of /api/oauth/usage (Claude debug)
	@go run ./cmd/pulse usage

.PHONY: build
build: ## Build a release binary into ./bin/pulse
	@mkdir -p bin
	@go build -ldflags="-s -w" -o bin/pulse ./cmd/pulse
	@echo "Built ./bin/pulse"

.PHONY: install
install: ## Install pulse to $GOBIN (so you can run `pulse` from anywhere)
	@go install ./cmd/pulse
	@echo "Installed $(BIN)"
	@if ! echo "$$PATH" | tr ':' '\n' | grep -q "$(GOBIN)"; then \
		echo ""; \
		echo "⚠  $(GOBIN) is not on your PATH. Add it once:"; \
		echo "    echo 'export PATH=\"$(GOBIN):\$$PATH\"' >> ~/.zshrc && source ~/.zshrc"; \
	fi

.PHONY: uninstall
uninstall: ## Remove the installed binary
	@rm -f $(BIN) && echo "Removed $(BIN)"

.PHONY: tidy
tidy: ## Sync go.mod / go.sum
	@go mod tidy

.PHONY: vet
vet: ## Static analysis (go vet)
	@go vet ./...

.PHONY: test
test: ## Run unit tests
	@go test ./...

.PHONY: check
check: vet test ## Run vet + tests

.PHONY: clean
clean: ## Remove build artefacts
	@rm -rf bin
