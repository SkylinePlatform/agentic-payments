# Code generation from the JSON Schema contracts.
#
# contracts/ is the single source of truth for the canonical model. Go types
# and TypeScript types are both generated from it — that cross-language
# generation is the actual reason the schemas exist. Hand-editing either side
# is how the two languages drift apart.
#
# SCHEMAS below is the one enumeration of the model. make, the Go generator,
# the disclosure extractor and the TypeScript generator all work from it, so
# adding a schema file is the whole of adding a type.

CONTRACTS      := contracts
SCHEMA_DIRS    := $(CONTRACTS)/authz $(CONTRACTS)/identity $(CONTRACTS)/instrument $(CONTRACTS)/evidence
SCHEMAS        := $(wildcard $(addsuffix /*.json,$(SCHEMA_DIRS)))
GENERATED_GO   := backend/internal/core/generated
GENERATED_TS   := frontend/src/protocol/generated

CONTRACT_TOOLS := $(CONTRACTS)/tools
FRONTEND       := frontend
NPM            ?= npm

.PHONY: generate
generate: generate-go generate-disclosure generate-ts ## Regenerate Go and TypeScript types from contracts/, and the mocks

# go-jsonschema is pinned in contracts/tools/go.mod, not in backend/go.mod. A
# code generator is not a dependency of the thing it generates, and backend/ is
# what gets imported — keeping the tool out of it is what lets core/ compile
# against the standard library alone.
.PHONY: generate-go
generate-go:
	@mkdir -p $(GENERATED_GO)
	@$(GO) -C $(CONTRACT_TOOLS) tool go-jsonschema \
		--package generated \
		--struct-name-from-title \
		--capitalization ID \
		--tags json \
		--output $(abspath $(GENERATED_GO))/model.go \
		$(abspath $(SCHEMAS))
	@echo "generate-go: $(words $(SCHEMAS)) schemas -> $(GENERATED_GO)/model.go"

# Which fields may be withheld is stated in the schemas and extracted for both
# languages in one walk. A hand-written copy of that list drifts, and the
# failure mode of the drift is silent: disclose everything, pass every test,
# defeat the point of using SD-JWT.
.PHONY: generate-disclosure
generate-disclosure:
	@mkdir -p $(GENERATED_GO) $(GENERATED_TS)
	@$(GO) -C $(CONTRACT_TOOLS) run ./disclosure \
		-go-out $(abspath $(GENERATED_GO))/disclosure.go \
		-ts-out $(abspath $(GENERATED_TS))/disclosure.ts \
		$(abspath $(SCHEMAS))

.PHONY: generate-ts
generate-ts: $(FRONTEND)/node_modules
	@mkdir -p $(GENERATED_TS)
	@cd $(FRONTEND) && $(NPM) run --silent generate -- $(abspath $(SCHEMAS))

# Eight targets route through this rule — demo, demo-live, frontend,
# frontend-test, frontend-check, generate-ts, generate and generate-verify — so
# the Node a contributor has is checked here once rather than in each of them.
#
# What it replaces is npm's own refusal. `engine-strict` in .npmrc makes `npm ci`
# stop on a Node below `engines`, and what it prints is two version strings, the
# word EBADENGINE and no fix: the first theory that produced in practice was a
# CPU architecture mismatch, which is a fair reading of "Unsupported engine" and
# is wrong — `engines` compares versions and never platforms. Issue #295, and it
# happened twice to two people who did not know the first time had happened.
#
# frontend/vite.config.ts already says the right thing for the narrower case, and
# cannot reach this one: it runs when vite or vitest starts, which is long after
# `npm ci` declined to install the tree that would have run it. So the message
# below is that one moved one step earlier, and the two are meant to read as one
# voice.
#
# # Where the floor comes from, which is the decision worth writing down
#
# `engines` in frontend/package.json, read with sed. Three candidates; the other
# two are worse in ways that matter:
#
#   * **.nvmrc holds `22`** — the release line, not a version — so it cannot be
#     the source of a [major, minor] floor at all. It is the obvious answer and
#     it is wrong in the direction that hurts: a check derived from it would
#     accept 22.0 through 22.12 and hand them to the failure it exists to
#     replace. That is the trap #288 hit and #269 paid for.
#   * **OLDEST_NODE in frontend/vite.config.ts** is the pair, and reading a
#     TypeScript constant with sed buys nothing: that constant is itself a
#     transcription of `engines`, and `engines` is the string npm acts on.
#     Deriving from the transcription would leave this check free to disagree
#     with the very refusal it is here to pre-empt.
#
# So it reads the original, and by construction cannot name a floor npm does not
# enforce. What construction does not tie down is the transcription — vite.config
# could still drift from package.json — so that pair is held by a test instead:
# TestTheFloorViteConfigRefusesBelowIsTheOneEnginesDeclares in tools/bootstrap
# fails when the two disagree, and the arms beside it drive this whole rule with
# a stub node on PATH.
#
# **The pattern refuses rather than guesses, and that is the part to keep.** It
# reads a `"node"` key at the start of a line, which is how npm writes this file,
# and what follows is an arity check: zero matches or two are both errors, never
# a pick. `"@types/node": "^22.20.1"` sits four lines above `engines` and is
# *not* what that is for — the quoted key there is `"@types/node"`, so `"node"`
# with both its quotes never appears in it, and an unanchored pattern misses it
# too. This comment claimed otherwise until the mutation was run; what the arity
# check actually catches is a *second* `"node"` key, which is a package anyone can
# depend on, and a reformat that puts `engines` on one line, which is a formatter
# away. Both are pinned in tools/bootstrap, both stop, and neither is allowed to
# resolve into a floor that came from somewhere other than `engines`.
#
# And nothing here runs node, npx or jq. A check that needed Node to run could
# not report a missing one, which is the case a fresh machine actually hits;
# `node --version` is the single call and `command -v` is what guards it.
#
# A version string this cannot parse is let through rather than refused, which is
# the direction refuseUnsupportedNode picks for the same case and for the same
# reason: the failure mode of a version guard should be letting an unknown
# through to npm, not stopping a working install on a string it did not
# recognise. A floor it cannot parse is the opposite — that one fails, because a
# guard silently switching itself off is exactly the state this rule exists to
# leave nobody in.
#
# **There is one version it parses and still hands to npm, and it is deliberate.**
# `engines` is a union with a hole in it — `^22.13.0 || >=24.0.0` — and this is a
# floor, so 23.x reads as newer than 22.13 and reaches the EBADENGINE above. The
# hole is jsdom's range rather than a rounding, and frontend/README.md says so.
# Reading the second alternative means a second pattern that can fail to match,
# which the arity check exists to refuse rather than guess at, and
# refuseUnsupportedNode in frontend/vite.config.ts is a floor in the same shape —
# the two are meant to read as one voice, so widening one alone would make them
# disagree about what is supported. Node 23 reached end of life on 2025-06-01 and
# .nvmrc names 22, so nobody arrives here by default. An `engines` that widens the
# gap is the change to re-read this on.
$(FRONTEND)/node_modules: $(FRONTEND)/package.json $(FRONTEND)/package-lock.json
	@floor=$$(sed -n 's/^[[:space:]]*"node"[[:space:]]*:[[:space:]]*"^\([0-9][0-9]*\)\.\([0-9][0-9]*\)\..*/\1 \2/p' $(FRONTEND)/package.json); \
	set -- $$floor; \
	if [ $$# -ne 2 ]; then \
		echo '$(FRONTEND)/node_modules: cannot read the Node floor out of `engines` in $(FRONTEND)/package.json, so the check that would name it is not running.' >&2; \
		echo '  It reads a line of the form "node": "^22.13.0 || >=24.0.0" — see the comment on this rule in contracts/codegen.mk.' >&2; \
		exit 1; \
	fi; \
	major=$$1; minor=$$2; \
	if ! command -v node >/dev/null 2>&1; then \
		echo "$(FRONTEND)/node_modules: this package needs Node $$major.$$minor or newer and there is no node on PATH at all." >&2; \
		echo '  Run `nvm use`, which reads .nvmrc, or install a Node that `engines` in $(FRONTEND)/package.json accepts.' >&2; \
		exit 1; \
	fi; \
	running=$$(node --version 2>/dev/null); v=$${running#v}; \
	rmajor=$${v%%.*}; rest=$${v#*.}; rminor=$${rest%%.*}; unreadable=""; \
	case "$$rmajor" in ''|*[!0-9]*) unreadable=1;; esac; \
	case "$$rminor" in ''|*[!0-9]*) unreadable=1;; esac; \
	if [ -z "$$unreadable" ] && { [ "$$rmajor" -lt "$$major" ] || { [ "$$rmajor" -eq "$$major" ] && [ "$$rminor" -lt "$$minor" ]; }; }; then \
		echo "$(FRONTEND)/node_modules: this package needs Node $$major.$$minor or newer and the node on PATH is $$running." >&2; \
		echo '  Run `nvm use`, which reads .nvmrc, or install a Node that `engines` in $(FRONTEND)/package.json accepts.' >&2; \
		echo '  Stopping here rather than in `npm ci`, whose EBADENGINE names both versions and no fix.' >&2; \
		exit 1; \
	fi
	cd $(FRONTEND) && $(NPM) ci
	@touch $@

# Generated output is gitignored, so a stale copy is not something a diff
# against the repository can find. What can still go stale is generation
# itself: a schema the tools cannot process, output that changes on a second
# run, or a tracked file the generators quietly rewrite. This checks all three.
#
# The mocks are found by name rather than by directory, because they are
# generated beside the interface they mock rather than into one place. Listing
# the two directories that hold them today would be a list that goes out of date
# the first time .mockery.yml names a third package — and it would go out of
# date silently, since a path this misses is not checked rather than reported.
#
# They belong here for the reason the schema output does: mockery's output is
# deterministic for the two single-method interfaces configured now, but nothing
# makes that a property of mockery rather than of this configuration. A package
# with several interfaces could order them by however go/packages happened to
# walk the filesystem, and a check that covered only some generated code would
# pass while saying it had proved generation reproducible.
SHA_GENERATED = { \
	find $(GENERATED_GO) $(GENERATED_TS) -type f; \
	find $(BACKEND) -name 'mocks_test.go' -type f; \
	} | sort | xargs sha256sum

# The tracked-file check compares the working tree against how it looked before
# generation, not against clean. The question this target asks is whether
# generation touched anything tracked — not whether the developer happens to
# have uncommitted work. Requiring a clean tree answers a different question and
# makes the target unrunnable exactly when someone is editing a schema and wants
# to check it, which is the one moment it earns its keep.
.PHONY: generate-verify
generate-verify: ## Regenerate twice and prove the result is reproducible and self-contained
	@git status --porcelain > .generate-verify.tree-before
	@$(MAKE) --no-print-directory generate
	@$(SHA_GENERATED) > .generate-verify.first
	@$(MAKE) --no-print-directory generate
	@$(SHA_GENERATED) > .generate-verify.second
	@if ! diff -u .generate-verify.first .generate-verify.second; then \
		rm -f .generate-verify.first .generate-verify.second; \
		echo "generate-verify: generation is not idempotent — a second run changed its own output" >&2; \
		exit 1; \
	fi
	@rm -f .generate-verify.first .generate-verify.second
	@git status --porcelain > .generate-verify.tree-after
	@if ! diff -u .generate-verify.tree-before .generate-verify.tree-after >&2; then \
		rm -f .generate-verify.tree-before .generate-verify.tree-after; \
		echo "generate-verify: generation modified a tracked file, or generated output escaped .gitignore" >&2; \
		exit 1; \
	fi
	@rm -f .generate-verify.tree-before .generate-verify.tree-after
	@echo "generate-verify: reproducible, and no tracked file was touched"

.PHONY: schemas
schemas: ## List the schemas currently under contracts/
	@for s in $(SCHEMAS); do echo "  $$s"; done
