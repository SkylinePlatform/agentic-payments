GO             ?= go
GIT            ?= git
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

# Generated test doubles. mockery is pinned in tools/mockery/go.mod — its own
# module, for the reason contracts/tools is one: a generator is not a
# dependency of the thing it generates, and backend/ is what gets imported. It
# is a second tools module rather than an entry in the first because the two
# generators share cobra and pflag, and one build list would let a mockery
# upgrade move the versions the schema generator compiles against.
#
# The binary is built rather than run through `go tool`, because mockery
# resolves the packages it mocks from its working directory: it has to run
# inside backend/, and `go -C tools/mockery tool` would run it inside the tools
# module, where none of those packages exist.
#
# Output is mocks_test.go beside each interface, gitignored and regenerated —
# the same rule generated types follow. `generate` and `check` both produce it,
# which is what keeps a fresh checkout able to run the tests.
MOCKERY_MODULE := tools/mockery
MOCKERY        := $(abspath $(BACKEND))/bin/mockery

$(MOCKERY): $(MOCKERY_MODULE)/go.mod $(MOCKERY_MODULE)/go.sum
	@$(GO) -C $(MOCKERY_MODULE) build -o $(MOCKERY) github.com/vektra/mockery/v3

.PHONY: generate-mocks
generate-mocks: $(MOCKERY) ## Regenerate the mocks configured in backend/.mockery.yml
	@cd $(BACKEND) && $(MOCKERY) --log-level warn
	@echo "generate-mocks: $(BACKEND)/.mockery.yml -> mocks_test.go"

# Mocks are not the canonical model, so their target lives here rather than in
# contracts/codegen.mk — but they are generated code that the tests need, so
# `generate` produces them too.
generate: generate-mocks

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
#
# core/ joined them when the constraint renderer got a second implementation.
# AGENTS.md set the criterion for widening this list — something in core/ that a
# second implementation has to reproduce — and named the renderer's output as
# the case that would qualify. frontend/src/constraint/render.ts is that second
# implementation, and contracts/testdata/render_vectors.json is the artefact the
# two agree on. See the paragraph in AGENTS.md beside this list for why the one
# core rule that stayed out is still out.
.PHONY: vectors
vectors: ## Conformance suite against the golden vectors
	cd $(BACKEND) && $(GO) test ./internal/adapters/... ./internal/core/... ./pkg/... -run 'TestGolden' -v

.PHONY: lint
lint: ## golangci-lint, including the depguard architecture rules
	cd $(BACKEND) && $(GOLANGCI_LINT) run

.PHONY: fmt
fmt: ## Apply formatters
	cd $(BACKEND) && $(GOLANGCI_LINT) fmt

.PHONY: tidy
tidy: ## go mod tidy
	cd $(BACKEND) && $(GO) mod tidy

# The frontend targets need Node, which is why they are not in `check`. The
# same reasoning as generate-ts: work that never touches the frontend should
# not need npm installed to pass the local gate.
#
# All three depend on generate-ts because src/protocol/generated is not
# committed — a fresh checkout has no canonical types, and the app imports
# them. That is the same shape as the Go half, where `check` regenerates
# before it lints, and it applies to the tests as much as to the build: a test
# that renders a surface imports whatever that surface imports.
#
# They depend on generate-disclosure for the same reason and it is easy to
# miss, because the two generators write into the same gitignored directory:
# src/protocol/index.ts re-exports DISCLOSABLE from generated/disclosure.ts,
# which generate-ts does not produce. Without this a fresh clone running
# `make frontend-test` fails on an unresolved import rather than on anything to
# do with the frontend. It costs no Node — generate-disclosure is a `go run`
# that happens to emit a .ts file.

# The whole stack, one command. Needs Node, because the frontend is part of it.
#
# The binaries are built first rather than `go run` per process: nine `go run`s
# would recompile nine times and the demo would start in its own good time.
# deploy/demo.json is the topology; adding a process is an entry there.
#
# Reproducible on every machine, deliberately: no process this starts reads a
# key or reaches a network, so the golden numbers every screenshot in this
# repository shows are the scripted five, always. `make demo-live`, right
# below, is the one command that trades that guarantee for the agent reading
# free text — read its own comment before reaching for it.
.PHONY: demo
demo: generate-go generate-disclosure generate-ts ## Bring up every role, the collector and the frontend (needs Node)
	cd $(BACKEND) && $(GO) build -o bin/ ./cmd/...
	$(BACKEND)/bin/demo -manifest deploy/demo.json -root .

# The opt-in `make demo` deliberately does not carry. Two other shapes were
# tried first and rejected, both recorded in cmd/agent's own package doc:
# `auto` as this flag's default would make the outcome depend on whichever
# shell happened to run `make demo`, and naming `-interpreter auto` in
# deploy/demo.json made that same opt-in visible in a reviewed file without
# making the *outcome* any more deterministic — GEMINI_API_KEY still decides,
# silently, which demonstration a machine gets. A command a person actually
# types is the one thing that cannot happen by accident, so that is what this
# target is.
#
# Same eight processes as `make demo`, unmodified — cmd/demo's own `-append`
# flag hands agent-watch `-interpreter auto` at the runner, rather than a
# second manifest that could drift from deploy/demo.json the moment either one
# changes without the other. See deploy/demo.json's own $comment for why
# agent-watch is the one process this reaches for.
#
# Reproducible only on a machine with no GEMINI_API_KEY exported — on one that
# has, agent-watch reads free text and the demonstration is no longer the
# scripted five `make demo` always shows. That is the point of running this
# target rather than the other one, and it is safe because the console states
# which mode it is in before the box is touched, so a screenshot stays
# attributable to whichever this run actually was.
.PHONY: demo-live
demo-live: generate-go generate-disclosure generate-ts ## Same stack as `make demo`, but the agent reads free text when GEMINI_API_KEY is set (needs Node)
	cd $(BACKEND) && $(GO) build -o bin/ ./cmd/...
	$(BACKEND)/bin/demo -manifest deploy/demo.json -root . -append agent-watch=-interpreter,auto

.PHONY: frontend
frontend: generate-disclosure generate-ts ## Run the frontend dev server (needs Node)
	cd $(FRONTEND) && npm run dev

# Vitest in jsdom, reading the same vite.config.ts the app builds with. Its
# other home is the Contracts job in CI, which already installs Node for the
# frontend build — a fourth job would install it a fourth time to run a suite
# that takes a second.
.PHONY: frontend-test
frontend-test: generate-disclosure generate-ts ## Run the frontend test suite (needs Node)
	cd $(FRONTEND) && npm test

.PHONY: frontend-check
frontend-check: generate-disclosure generate-ts frontend-test ## Type-check, build and test the frontend (needs Node)
	cd $(FRONTEND) && npm run build

# core.hooksPath is repository-local rather than per-worktree, so setting it once
# covers every worktree of this checkout — which is the case that produced the
# unsigned commit the hook exists for.
.PHONY: hooks
hooks: ## Point git at the tracked hooks in .githooks
	@$(GIT) config core.hooksPath .githooks
	@chmod +x .githooks/*
	@echo "hooks: core.hooksPath -> .githooks"

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
	@for doc in $$(find docs -name '*.md' -not -path 'docs/diagrams/*' -not -path 'docs/specs/*' -not -path 'docs/plans/*'); do \
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
check: generate-go generate-disclosure generate-mocks lint test ## Lint and test against freshly generated Go types

# mocks_test.go is mockery's filename and nothing hand-written may take it —
# .gitignore covers it, so a hand-written one would be invisible to git anyway.
.PHONY: clean
clean: ## Remove build output and generated types
	cd $(BACKEND) && $(GO) clean -cache -testcache
	rm -rf $(BACKEND)/bin $(GENERATED_GO) $(GENERATED_TS)
	find $(BACKEND) -name mocks_test.go -delete
