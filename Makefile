GO             ?= go
GOLANGCI_LINT  ?= golangci-lint
BACKEND        := backend

# A developer may keep an untracked go.work at the repository root so that an
# editor opened here — rather than on backend/ — resolves both modules. CI has
# no such file and builds backend/ standalone, so make turns the workspace off:
# whether the two modules' build lists get unified must not depend on whether
# the person running `make check` happens to use an IDE.
export GOWORK := off

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

# pkg/ is in scope alongside adapters/ because that is where implementations of
# public standards live, and those are the ones with vectors published by
# somebody other than us. RFC 9901 prints its own Disclosures, digests, sd_hash
# and Processed SD-JWT Payload; a suite that called itself the conformance
# suite and skipped them would be checking only the half of the system whose
# vectors we wrote ourselves.
.PHONY: vectors
vectors: ## Conformance suite against the golden vectors
	cd $(BACKEND) && $(GO) test ./internal/adapters/... ./pkg/... -run 'TestGolden' -v

.PHONY: lint
lint: ## golangci-lint, including the depguard architecture rules
	cd $(BACKEND) && $(GOLANGCI_LINT) run

.PHONY: fmt
fmt: ## Apply formatters
	cd $(BACKEND) && $(GOLANGCI_LINT) fmt

.PHONY: tidy
tidy: ## go mod tidy
	cd $(BACKEND) && $(GO) mod tidy

# go.mod sits in backend/, so an editor opened at the repository root finds no
# module, falls back to a GOPATH view, and reports every intra-module import as
# unresolvable. A go.work listing both modules fixes that, and it stays
# untracked: a committed one would unify the two modules' build lists, and
# contracts/tools pins the code generator outside backend/go.mod on purpose —
# with a workspace active, backend/ compiles against eleven modules it does not
# declare and CI does not provide. That is the drift the separate module
# exists to prevent, so the file must not be checked in.
#
# What that costs is a contributor typing the file out of AGENTS.md, and this
# target is here to remove that cost without giving the property back.
#
# go work init rather than writing the contents by hand: it takes the go
# directive from the toolchain actually installed, so a generated workspace can
# never demand a newer Go than its owner has — the failure mode a committed
# go.work would hand anyone on an older toolchain. It also refuses to overwrite,
# so a replace directive added locally for debugging survives.
.PHONY: workspace
workspace: ## Write the untracked go.work an editor opened at the root needs
	@if [ -e go.work ]; then \
		echo "go.work already exists — leaving it alone"; \
	else \
		$(GO) work init ./$(BACKEND) ./$(CONTRACT_TOOLS) && \
		echo "wrote go.work — untracked on purpose, see AGENTS.md"; \
	fi

# mermaid-cli pulls a headless Chromium, which is why this is not wired into
# `check`: AGENTS.md promises that the gate needs only Go. Diagrams are
# exported on demand, by whoever needs a picture for an article.
MERMAID_CLI ?= @mermaid-js/mermaid-cli@11.4.2

.PHONY: diagrams
diagrams: ## Export inline mermaid from docs/ to SVG for the article series
	@mkdir -p docs/diagrams
	@for doc in $$(find docs -name '*.md' -not -path 'docs/diagrams/*' -not -path 'docs/superpowers/*'); do \
		grep -q '```mermaid' $$doc || continue; \
		name=$$(basename $$doc .md); \
		echo "diagrams: $$doc"; \
		npx -y $(MERMAID_CLI) -i $$doc -o docs/diagrams/$$name.md --outputFormat svg >/dev/null; \
	done
	@echo "diagrams: exported to docs/diagrams/"

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
