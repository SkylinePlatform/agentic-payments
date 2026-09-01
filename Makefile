GO             ?= go
GIT            ?= git
GOLANGCI_LINT  ?= golangci-lint
BACKEND        := backend
CI_WORKFLOW    := .github/workflows/ci.yml

# The golangci-lint version the local gate has to be, taken from the workflow
# that pins it rather than copied here — the same rule the Node floor follows in
# contracts/codegen.mk, and for the same reason: a second copy of a version
# number is free to drift, and the drift is invisible until somebody's gate
# disagrees with CI.
#
# Recursive rather than `:=`, so the awk runs when lint-version asks and not on
# every `make help`.
GOLANGCI_PIN    = $(shell awk '/golangci-lint-action/ { found = 1 } \
                              found && $$1 == "version:" { print $$2; exit }' $(CI_WORKFLOW))

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
build: generated ## Build every binary under backend/cmd
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

# mockery loads the packages it mocks, and they do not compile until the
# canonical model exists — internal/agent/console and internal/platform/obs both
# import it from non-test files. On a tree nothing has generated into, this
# target alone fails with go/packages naming the import rather than the missing
# generator, which reads like a broken .mockery.yml. `check` and `generate`
# happened to list generate-go first, so the dependency was real and satisfied
# by the order prerequisites were written in; ordering is not a dependency, and
# `make -j` may run them either way round.
generate-mocks: generate-go generate-disclosure

# Mocks are not the canonical model, so their target lives here rather than in
# contracts/codegen.mk — but they are generated code that the tests need, so
# `generate` produces them too.
generate: generate-mocks

# Everything a Go toolchain alone can generate, under one name because three
# callers have to agree on it: `check`, which is the gate; the hooks in
# .githooks/, which run it after any git operation that moves the working tree;
# and a person on a fresh clone typing `make setup`. A hook naming the three
# targets itself would be a fourth copy of a list, and the copy that drifts is
# always the one nobody runs.
#
# generate-ts is deliberately out. AGENTS.md promises the gate needs only Go,
# and a git hook that reached for npm would put a Node toolchain in front of a
# checkout. generate-disclosure stays in even though it writes a .ts file — it
# is a `go run`, and what it costs is what decides this, not what it emits.
.PHONY: generated
generated: generate-go generate-disclosure generate-mocks ## Regenerate everything a Go toolchain alone can produce

# deploy/catalogue.json and the picture beside every row of it, derived from the
# CC0 snapshot in tools/catalogue/data. A third tool-only module, on the rule
# the two above follow: a generator is not a dependency of the thing it
# generates.
#
# **It is deliberately not a prerequisite of anything.** Not `generate`, not
# `check`, not `demo`. Its output is committed, its input is committed, and a
# catalogue that filled differently between runs would make every scenario block
# in it a claim that happened to hold when it was written — issue #158 and issue
# #160 both. What keeps the committed file honest is a test rather than a
# rebuild: TestTheCommittedCatalogueIsWhatThisProgramProduces re-derives it under
# `make test` and compares, so a hand edit fails the gate without the gate ever
# running the generator.
CATALOGUE_TOOL := tools/catalogue

# The suite over .githooks/ and over the tracked sentinel in the generated
# package. A fourth tool-only module, and the one that generates nothing: its
# subject is the repository rather than the model, and it must stay out of
# backend/go.mod for the reason the other three do — backend/ is what gets
# imported. It holds no non-test Go source, exactly as internal/suite holds
# none.
BOOTSTRAP_TOOL := tools/bootstrap

.PHONY: catalogue
catalogue: ## Re-derive deploy/catalogue.json and its images from tools/catalogue/data
	$(GO) -C $(CATALOGUE_TOOL) run .

.PHONY: test
test: generated ## Unit tests
	cd $(BACKEND) && $(GO) test -race ./...
	$(GO) -C $(CONTRACT_TOOLS) test ./...
	$(GO) -C $(CATALOGUE_TOOL) test ./...
# -count=1 is not belt-and-braces. The subject of this suite is four shell
# scripts and one Go file outside the module, none of which the build cache
# tracks: `go test` reported (cached) after post-merge had been deleted from the
# tree. A suite that cannot see its own subject change is the thing it exists to
# prevent. The comment sits at column 0 rather than inside the recipe, as
# `clean`'s does, so that make does not echo five lines of prose on every run.
	$(GO) -C $(BOOTSTRAP_TOOL) test -count=1 ./...

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
vectors: generated ## Conformance suite against the golden vectors
	cd $(BACKEND) && $(GO) test ./internal/adapters/... ./internal/core/... ./pkg/... -run 'TestGolden' -v

.PHONY: lint
lint: generated lint-version ## golangci-lint, including the depguard architecture rules
	cd $(BACKEND) && $(GOLANGCI_LINT) run

# Refuse a golangci-lint that is not the one CI runs — issue #272.
#
# AGENTS.md says `make check` passing locally is necessary and not sufficient,
# and the whole value of that sentence is that the local gate is the *weaker*
# of the two. #272 is what it looks like when it is not: `make lint` failed on
# main with two staticcheck findings the Lint job never reported, so the tree
# was green in CI and red on the machine the rule says has to see it pass.
#
# Which version is stricter is not the point and is not knowable in advance.
# A linter is a moving set of checks; two versions disagree in both directions,
# and either disagreement costs the same thing — a gate that answers a different
# question from the one the pull request has to pass.
#
# **CI does not run this target at all** — its *Lint* job uses
# golangci-lint-action, which is why the version lives in the workflow and is
# read from there rather than declared here and duplicated into it. Nothing
# below therefore runs on a runner, and no CI job needs a golangci-lint on PATH.
#
# `make fmt` is deliberately not held to this. CI never runs it either, and
# unlike `run` there is no answer for it to agree with — a formatter whose output
# differs is caught by the pinned `run` on the next line anyway.
.PHONY: lint-version
lint-version: ## Refuse a golangci-lint that is not the version CI pins
	@pin='$(GOLANGCI_PIN)'; pin="$${pin#v}"; \
	if [ -z "$$pin" ]; then \
		echo "make: no golangci-lint version found in $(CI_WORKFLOW)." >&2; \
		echo "make: that file is where this rule reads the pin from, so a renamed action or a" >&2; \
		echo "make: reshaped step leaves nothing holding \`make lint\` to what CI runs." >&2; \
		exit 1; \
	fi; \
	have=$$($(GOLANGCI_LINT) version 2>/dev/null | awk '{ print $$4 }'); have="$${have#v}"; \
	if [ -z "$$have" ]; then \
		echo "make: no golangci-lint that reports a version — CI pins v$$pin." >&2; \
		echo "make:   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$$pin" >&2; \
		exit 1; \
	fi; \
	if [ "$$have" != "$$pin" ]; then \
		echo "make: golangci-lint here is v$$have and CI pins v$$pin, so this gate answers a" >&2; \
		echo "make: different question from the one the pull request has to pass — in either" >&2; \
		echo "make: direction. Issue #272 is the instance: two staticcheck findings locally," >&2; \
		echo "make: none in CI, on a tree with nothing wrong in it." >&2; \
		echo "make:   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$$pin" >&2; \
		echo "make: or point GOLANGCI_LINT= at a build of that version." >&2; \
		exit 1; \
	fi

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
# Same nine processes as `make demo`, unmodified — cmd/demo's own `-append`
# flag hands the agent `-interpreter auto` at the runner, rather than a
# second manifest that could drift from deploy/demo.json the moment either one
# changes without the other. See deploy/demo.json's own $comment for why
# the agent is the one process this reaches for.
#
# Reproducible only on a machine with no GEMINI_API_KEY exported — on one that
# has, the agent reads free text and the demonstration is no longer the
# scripted sentences `make demo` always offers. That is the point of running this
# target rather than the other one, and it is safe because the console states
# which mode it is in before the box is touched, so a screenshot stays
# attributable to whichever this run actually was.
#
# **Since issue #243 it appends a second flag, and the two are one argument.**
# `-interpreter auto` lets the agent read a sentence nobody scripted;
# `-catalogue-live dummyjson` gives that sentence a shelf nobody wrote down.
# Either alone is half the point — free text against sixty-four committed offers
# proves nothing a lookup table could not, and a wider shop nobody can address in
# their own words is a longer table. The merchant states at start-up how many
# offers it fetched and from where, and which sentences a fetched offer can
# answer: it holds one price, so a condition ("when it drops below $200") can
# only ever resolve against the committed offers, and an instruction is what a
# live offer answers.
#
# This half is *not* conditional on an environment variable, which is the one
# way it differs from the interpreter's. A shop that will not answer stops the
# merchant rather than falling back to the file — an unset key is an answer, and
# a shop asked for and not delivered is not — so this target needs a network
# where `make demo` needs none.
#
# **It reads `.env`, and that is the one place in this tree that does.** The
# target exists for the model, `.env` is where this repository tells you to put
# the key, and until issue #296 nothing joined the two — so a shell that had not
# run `set -a; source .env; set +a` got the scripted sentences from the target
# whose whole point is that they are not needed. Nothing was red and nothing was
# broken; the demonstration silently became the other one.
#
# Three rules, each of them one sentence, and the block below is deliberately one
# block: the loading and the reporting cannot disagree if the same `if` decides
# both, and a message that names a source it did not read is precisely the defect
# this repository keeps finding.
#
#   * An already-exported GEMINI_API_KEY wins and `.env` is not read at all, so
#     `GEMINI_API_KEY=… make demo-live` still works for a one-off and nobody's
#     deliberate export is replaced by a file behind their back.
#   * The name is printed and the value never is.
#   * With no key anywhere it says so **and names the consequence**, because the
#     console header saying the same thing scrolls past in a stack that prints
#     several hundred lines — which is how #296 was found.
#
# `make demo` does not do this and must not: a run that needs no network, no key
# and no ambient state is the whole of what that target is for. The binaries are
# unchanged either way — they still read only the environment they are handed,
# which is what the recorded decision in `.env.example` was actually protecting.
.PHONY: demo-live
demo-live: generate-go generate-disclosure generate-ts ## Same stack as `make demo`, with free text when GEMINI_API_KEY is set — read from .env if the shell has none — and a shop fetched at start-up (needs Node and a network)
	cd $(BACKEND) && $(GO) build -o bin/ ./cmd/...
	@src=the\ environment; \
	if [ -z "$${GEMINI_API_KEY:-}" ] && [ -f .env ]; then set -a; . ./.env; set +a; src=.env; fi; \
	if [ -n "$${GEMINI_API_KEY:-}" ]; then \
		echo "demo-live: GEMINI_API_KEY read from $$src — the agent reads free text"; \
	else \
		echo "demo-live: no GEMINI_API_KEY in the environment or in .env — the agent will answer only its scripted sentences"; \
	fi; \
	$(BACKEND)/bin/demo -manifest deploy/demo.json -root . \
		-append agent=-interpreter,auto \
		-append merchant=-catalogue-live,dummyjson

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
#
# tools/catalogue is listed and tools/mockery is not, on the one criterion that
# separates them: an editor needs a module in the workspace to resolve the Go
# source in it, and mockery holds none. What made listing contracts/tools a
# trade-off does not apply to the catalogue generator either — it requires
# nothing but testify, which backend/ already builds against, so unifying its
# build list moves no version.
#
# tools/bootstrap is listed on that same criterion — a _test.go is Go source
# somebody opens — and, like the catalogue generator, it requires nothing but
# testify.
.PHONY: workspace
workspace: ## Write the untracked go.work an editor opened at the root needs
	@if [ -e go.work ]; then \
		echo "go.work already exists — leaving it alone"; \
	else \
		$(GO) work init ./$(BACKEND) ./$(CONTRACT_TOOLS) ./$(CATALOGUE_TOOL) ./$(BOOTSTRAP_TOOL) && \
		echo "wrote go.work — untracked on purpose, see AGENTS.md"; \
	fi

# The one command a fresh clone needs, and the only place its three steps are
# written down in an order. git does not clone config, so core.hooksPath is
# unset in a new checkout and nothing in .githooks/ can run until a person has
# installed them once — which is why the first command is the one thing a hook
# cannot be. `generated` comes first so that an editor opened straight after is
# already right.
.PHONY: setup
setup: generated hooks workspace ## Prepare a fresh clone: generate, install the git hooks, write go.work
	@if [ ! -f $(GENERATED_TS)/index.ts ]; then \
		echo "setup: the frontend's generated types are not here — \`make generate\` writes those, and needs Node"; \
	fi

# `make setup` is a claim about a tree with nothing generated in it, and the
# only honest way to check it is to run it in one.
#
# The copy is `git ls-files` through tar, which takes the tracked files as they
# are on disk right now. `git worktree add --detach HEAD` was the obvious way to
# get a pristine tree and is wrong in the one direction that matters: it
# verifies the committed Makefile, so editing `setup` and running this passes
# green against the version you are not testing.
#
# Two properties, each a mutation away from failing. NPM points at a command
# that does not exist, so a Node step added to `setup` fails here rather than
# passing on a machine that happens to have npm — every GitHub runner has one,
# so absence can never be the check. And `go vet ./...` is the toolchain's own
# package loader, the one gopls drives: it type-checks test files, so a missing
# mock is a failure rather than a build that is clean without it.
#
# GIT is neutralised for the opposite reason NPM is: the copy has no .git of its
# own, so `make hooks` inside it would reach up and configure *this* repository.
# What is under test is what setup generates, not that `git config` can be
# called.
#
# A failing run leaves the copy on disk to be looked at. The next run removes it.
SETUP_VERIFY := $(abspath .setup-verify)

.PHONY: setup-verify
setup-verify: ## Prove `make setup` leaves a tree the toolchain can load, from nothing and with no npm
	@rm -rf $(SETUP_VERIFY)
	@mkdir -p $(SETUP_VERIFY)
	@$(GIT) ls-files -z | tar --null -T - -cf - | tar -xf - -C $(SETUP_VERIFY)
	@$(MAKE) --no-print-directory -C $(SETUP_VERIFY) setup NPM=npm-must-not-run GIT=true
	@cd $(SETUP_VERIFY)/$(BACKEND) && GOWORK=off $(GO) vet ./...
	@rm -rf $(SETUP_VERIFY)
	@echo "setup-verify: a tree with nothing generated in it loads after \`make setup\`, with no npm on the path"

# mermaid-cli pulls a headless Chromium, which is why this is not wired into
# `check`: AGENTS.md promises that the gate needs only Go. Diagrams are
# exported on demand, by whoever needs a picture for an article.
MERMAID_CLI ?= @mermaid-js/mermaid-cli@11.4.2

# The export name is the document's path with docs/ stripped and the separators
# flattened, not its basename: two documents named README.md in different
# directories share a basename, so the second export would overwrite the first
# and nothing would say so. A name seen twice fails the target for that reason —
# the alternative is a reader finding out instead of the build.
#
# The root README is spelled root-README rather than README, and that is the one
# name the rule does not derive. Stripping a leading docs/ leaves docs/README.md
# — the documentation index — as README too, so the two would collide the day
# either of them gained a mermaid block. The guard below would catch it, and
# catching it is worse than not being able to write it: the failure would arrive
# for whoever next edited an unrelated document, naming two files neither of
# which they had touched.
.PHONY: diagrams
diagrams: ## Export inline mermaid from docs/ and the README to SVG for the article series
	@mkdir -p docs/diagrams
	@names=""; \
	for doc in README.md $$(find docs -name '*.md' -not -path 'docs/diagrams/*' -not -path 'docs/specs/*' -not -path 'docs/plans/*' | sort); do \
		grep -q '```mermaid' $$doc || continue; \
		name=$$(echo $$doc | sed -e 's|^README\.md$$|root/README.md|' -e 's|^docs/||' -e 's|\.md$$||' -e 's|/|-|g'); \
		case " $$names " in \
			*" $$name "*) \
				echo "diagrams: two documents export as $$name — the second would overwrite the first" >&2; \
				exit 1;; \
		esac; \
		names="$$names $$name"; \
		echo "diagrams: $$doc -> $$name"; \
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
check: generated lint test ## Lint and test against freshly generated Go types

# mocks_test.go is mockery's filename and nothing hand-written may take it —
# .gitignore covers it, so a hand-written one would be invisible to git anyway.
.PHONY: clean
clean: ## Remove build output and generated types
	cd $(BACKEND) && $(GO) clean -cache -testcache
	rm -rf $(BACKEND)/bin $(GENERATED_TS)
# doc.go is tracked and is the only file under $(GENERATED_GO) that is not
# generated. `rm -rf` on the directory would delete it and leave `make clean`
# with a dirty tree behind it.
	find $(GENERATED_GO) -type f ! -name doc.go -delete
	find $(BACKEND) -name mocks_test.go -delete
