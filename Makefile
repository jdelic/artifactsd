# artifactsd developer Makefile (design/13 §1).
# `make check` runs the full local quality gate; CI runs the same target.

GO            ?= go
GOBIN         := $(shell $(GO) env GOPATH)/bin
GOLANGCI_LINT ?= $(GOBIN)/golangci-lint
GOFUMPT       ?= $(GOBIN)/gofumpt
GOVULNCHECK   ?= $(GOBIN)/govulncheck

PKGS          := ./...
VERSION       ?= dev
COMMIT        ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE          ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS       := -X github.com/jdelic/artifactsd/internal/buildinfo.Version=$(VERSION) \
                 -X github.com/jdelic/artifactsd/internal/buildinfo.Commit=$(COMMIT) \
                 -X github.com/jdelic/artifactsd/internal/buildinfo.Date=$(DATE)

.DEFAULT_GOAL := check

.PHONY: check
check: tidy-check fmt-check vet lint test vulncheck ## Run all quality gates

.PHONY: build
build: ## Build both binaries into ./dist
	$(GO) build -ldflags '$(LDFLAGS)' -o dist/artifactsd ./cmd/artifactsd
	$(GO) build -ldflags '$(LDFLAGS)' -o dist/artifactctl ./cmd/artifactctl

.PHONY: fmt
fmt: ## Format code with gofumpt
	$(GOFUMPT) -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not gofumpt-formatted
	@out=$$($(GOFUMPT) -l .); \
	if [ -n "$$out" ]; then \
		echo "gofumpt: files need formatting:"; echo "$$out"; exit 1; \
	fi

.PHONY: vet
vet: ## go vet
	$(GO) vet $(PKGS)

.PHONY: lint
lint: ## golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## Run tests with the race detector
	$(GO) test $(PKGS) -race

.PHONY: cover
cover: ## Run tests with coverage report
	$(GO) test $(PKGS) -race -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vulncheck
vulncheck: ## Scan dependencies for known vulnerabilities
	$(GOVULNCHECK) $(PKGS)

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	$(GO) mod tidy -diff

.PHONY: tools
tools: ## Install the developer tools used by `make check`
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.62.2
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf dist coverage.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
