GO             ?= go
GOLANGCI_LINT  ?= golangci-lint
BACKEND        := backend

.DEFAULT_GOAL := help

include contracts/codegen.mk

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build every binary under backend/cmd
	cd $(BACKEND) && $(GO) build ./...

.PHONY: test
test: ## Unit tests
	cd $(BACKEND) && $(GO) test -race ./...

.PHONY: vectors
vectors: ## Conformance suite against the golden vectors
	cd $(BACKEND) && $(GO) test ./internal/adapters/... -run 'TestGolden' -v

.PHONY: lint
lint: ## golangci-lint, including the depguard architecture rules
	cd $(BACKEND) && $(GOLANGCI_LINT) run

.PHONY: fmt
fmt: ## Apply formatters
	cd $(BACKEND) && $(GOLANGCI_LINT) fmt

.PHONY: tidy
tidy: ## go mod tidy
	cd $(BACKEND) && $(GO) mod tidy

.PHONY: check
check: lint test ## What CI runs

.PHONY: clean
clean: ## Remove build output and generated types
	cd $(BACKEND) && $(GO) clean -cache -testcache
	rm -rf $(BACKEND)/bin $(GENERATED_GO) $(GENERATED_TS)
