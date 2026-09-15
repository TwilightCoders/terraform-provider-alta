SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

BINARY   := terraform-provider-alta-labs
GOOS     := $(shell go env GOOS)
GOARCH   := $(shell go env GOARCH)
VERSION  ?= $(or $(patsubst v%,%,$(shell git describe --tags --abbrev=0 2>/dev/null)),0.0.0)
LDFLAGS  := -s -w -X main.version=$(VERSION)
TOOL     := go tool -modfile=tools/go.mod

PLUGIN_DIR := $(HOME)/.terraform.d/plugins/registry.terraform.io/twilightcoders/alta-labs/$(VERSION)/$(GOOS)_$(GOARCH)

TIMEOUT ?= 30m
RUN     ?= .

##@ Build

.PHONY: build
build: ## Build the provider binary into bin/
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) .

.PHONY: install
install: build ## Install into the local Terraform plugin mirror
	mkdir -p '$(PLUGIN_DIR)'
	cp bin/$(BINARY) '$(PLUGIN_DIR)/$(BINARY)_v$(VERSION)'
	@echo "Installed $(VERSION) to $(PLUGIN_DIR)"

.PHONY: dev-override
dev-override: build ## Print a ~/.terraformrc dev_overrides block for this checkout
	@printf 'provider_installation {\n  dev_overrides {\n    "%s" = "%s"\n  }\n  direct {}\n}\n' \
		'registry.terraform.io/twilightcoders/alta-labs' '$(CURDIR)/bin'
	@echo "# Add the block above to ~/.terraformrc yourself; this target never writes it." >&2

.PHONY: clean
clean: ## Remove build and coverage output
	rm -rf bin dist coverage.out coverage.html

##@ Quality

.PHONY: test
test: ## Run unit tests
	go test -race -cover ./...

.PHONY: testacc
testacc: ## Run acceptance tests against a live site (RUN=regex TIMEOUT=30m)
	TF_ACC=1 go test ./... -run '$(RUN)' -v -timeout '$(TIMEOUT)'

.PHONY: coverage
coverage: ## Write coverage.out and coverage.html
	go test -race -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

.PHONY: lint
lint: ## Run golangci-lint
	$(TOOL) golangci-lint run ./...

.PHONY: fmt
fmt: ## Format code
	$(TOOL) golangci-lint fmt ./...

##@ Documentation

.PHONY: docs
docs: ## Generate registry documentation into docs/
	$(TOOL) tfplugindocs generate --provider-name alta

##@ Release

.PHONY: release
release: ## Update CHANGELOG, commit and tag (VERSION=x.y.z); does not push
	@[[ "$(VERSION)" =~ ^[0-9]+\.[0-9]+\.[0-9]+$$ ]] || { echo "VERSION must be x.y.z" >&2; exit 1; }
	@command -v git-cliff >/dev/null || { echo "git-cliff is required: brew install git-cliff" >&2; exit 1; }
	@! git rev-parse 'v$(VERSION)' >/dev/null 2>&1 || { echo "tag v$(VERSION) already exists" >&2; exit 1; }
	@git diff --quiet && git diff --cached --quiet || { echo "working tree is not clean" >&2; exit 1; }
	git-cliff --tag 'v$(VERSION)' -o CHANGELOG.md
	git add CHANGELOG.md
	git commit -m 'chore(release): v$(VERSION)'
	git tag -a 'v$(VERSION)' -m 'v$(VERSION)'
	@echo "Tagged v$(VERSION). Push with: git push origin main --tags"

##@ Help

.PHONY: help
help: ## List targets
	@awk 'BEGIN {FS = ":.*##"} /^##@/ {printf "\n%s\n", substr($$0, 5)} /^[a-z-]+:.*##/ {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
