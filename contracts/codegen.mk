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

$(FRONTEND)/node_modules: $(FRONTEND)/package.json $(FRONTEND)/package-lock.json
	cd $(FRONTEND) && $(NPM) ci
	@touch $@

# Generated output is gitignored, so a stale copy is not something a diff
# against the repository can find. What can still go stale is generation
# itself: a schema the tools cannot process, output that changes on a second
# run, or a tracked file the generators quietly rewrite. This checks all three.
SHA_GENERATED = find $(GENERATED_GO) $(GENERATED_TS) -type f | sort | xargs sha256sum

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
