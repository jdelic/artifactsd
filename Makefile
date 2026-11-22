# artifactsd developer Makefile (design/13 §1).
# `make check` runs the full local quality gate; CI runs the same target.

GO            ?= go
# Developer tools are pinned as `tool` dependencies in separate modules (kept
# out of the main go.mod) and updated by Dependabot. `go tool` builds them with
# the project's toolchain on first use and caches them; no install step.
GOLANGCI_LINT ?= $(GO) tool -modfile=tools/go.mod golangci-lint
GOVULNCHECK   ?= $(GO) tool -modfile=tools/go.mod govulncheck
GORELEASER    ?= $(GO) tool -modfile=tools/goreleaser/go.mod goreleaser
TOOL_MODULES  := tools tools/goreleaser

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
fmt: ## Format code (gofumpt, via golangci-lint's formatters)
	$(GOLANGCI_LINT) fmt

.PHONY: fmt-check
fmt-check: ## Fail if any file is not formatted; prints the diff
	$(GOLANGCI_LINT) fmt --diff

.PHONY: vet
vet: ## go vet
	$(GO) vet $(PKGS)

.PHONY: lint
lint: ## golangci-lint
	$(GOLANGCI_LINT) run

.PHONY: test
test: ## Run tests with the race detector
	$(GO) test $(PKGS) -race

.PHONY: smoke
smoke: build ## Spawn a real artifactsd and drive it with artifactctl + curl
	./scripts/smoke-test.sh

.PHONY: release-snapshot
release-snapshot: ## Build release archives for all platforms into ./dist without publishing
	$(GORELEASER) release --snapshot --clean

.PHONY: docker-build
docker-build: ## Build the artifactsd Docker image for the local platform
	docker build -t artifactsd:dev --build-arg VERSION=$(VERSION) .

.PHONY: cover
cover: ## Run tests with coverage report
	$(GO) test $(PKGS) -race -coverprofile=coverage.out
	$(GO) tool cover -func=coverage.out | tail -1

.PHONY: vulncheck
vulncheck: ## Scan dependencies for known vulnerabilities
	$(GOVULNCHECK) $(PKGS)

.PHONY: tidy
tidy: ## go mod tidy (main and tool modules)
	$(GO) mod tidy
	for m in $(TOOL_MODULES); do $(GO) -C $$m mod tidy || exit 1; done

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum (main and tool modules) are not tidy
	$(GO) mod tidy -diff
	for m in $(TOOL_MODULES); do $(GO) -C $$m mod tidy -diff || exit 1; done

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf dist coverage.out

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
