GO             ?= go
GOLANGCI_LINT  ?= golangci-lint
BACKEND        := backend

.DEFAULT_GOAL := help

include contracts/codegen.mk

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-17s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build every binary under backend/cmd
	cd $(BACKEND) && $(GO) build ./...

.PHONY: test
test: ## Unit tests
	cd $(BACKEND) && $(GO) test -race ./...
	$(GO) -C $(CONTRACT_TOOLS) test ./...

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

# Generation comes first on purpose. contracts/ is the source of truth for the
# canonical model, so linting and testing a tree whose generated half was built
# from an older schema checks the wrong thing.
#
# The Go half only. AGENTS.md makes this target the gate before any task may be
# reported finished, and generate-ts needs npm — which would put a Node
# toolchain in the way of work that never touches the frontend. Both targets
# here are pure Go; generate-disclosure is a `go run` that happens to emit a
# .ts file, which takes no Node to produce. The TypeScript half runs in the
# Contracts CI job, which does full `generate` and `generate-verify` on every
# pull request, so cross-language drift is still caught — just not on the path
# of a Go-only contributor.
.PHONY: check
check: generate-go generate-disclosure lint test ## Lint and test against freshly generated Go types

.PHONY: clean
clean: ## Remove build output and generated types
	cd $(BACKEND) && $(GO) clean -cache -testcache
	rm -rf $(BACKEND)/bin $(GENERATED_GO) $(GENERATED_TS)
