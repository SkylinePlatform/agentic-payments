# Code generation from the JSON Schema contracts.
#
# contracts/ is the single source of truth for the canonical model. Go types
# and TypeScript types are both generated from it — that cross-language
# generation is the actual reason the schemas exist. Hand-editing either side
# is how the two languages drift apart.
#
# Not implemented yet — see issue #2.

CONTRACTS      := contracts
SCHEMA_DIRS    := $(CONTRACTS)/authz $(CONTRACTS)/identity $(CONTRACTS)/instrument $(CONTRACTS)/evidence
SCHEMAS        := $(wildcard $(addsuffix /*.json,$(SCHEMA_DIRS)))
GENERATED_GO   := backend/internal/core/generated
GENERATED_TS   := frontend/src/protocol/generated

.PHONY: generate
generate: generate-go generate-ts ## Regenerate Go and TypeScript types from contracts/

.PHONY: generate-go
generate-go:
	@echo "generate-go: not implemented — see issue #2" >&2
	@exit 1

.PHONY: generate-ts
generate-ts:
	@echo "generate-ts: not implemented — see issue #2" >&2
	@exit 1

.PHONY: schemas
schemas: ## List the schemas currently under contracts/
	@for s in $(SCHEMAS); do echo "  $$s"; done
