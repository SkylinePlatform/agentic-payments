package console_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// schemas is where the canonical model is defined, from this package.
//
// Four directories up: console, agent, internal, backend. A relative path is
// fragile against a move and that is the trade — it fails loudly, here, with a
// message naming what it could not find, rather than quietly walking nothing and
// passing forever. TestTheWalkFindsTheSchemas is the other half of the same
// worry.
const schemas = "../../../../contracts"

// TestNoContractCarriesAMandateState is the first of the three arrangements that
// keep a mandate state out of the protocol.
//
// The state is the agent's own bookkeeping. It may be served by its owner — this
// package is what serves it — and it must not become a protocol artefact, because
// the moment a schema carries one there is a generated Go type and a generated
// TypeScript type for it, and from there it is one request field away from being
// something an agent can be *told*. A verifier that took the buyer's word for
// where a mandate stood would be reading an opinion where AP2 gives it a signed
// receipt.
//
// It checks values rather than substrings, and the reason is worth stating: a
// naive search for "ready" matches the word "already" in checkout_mandate.json's
// own description, which would fail on prose that has nothing to do with this.
// What matters is a *value* — an enum entry, a const, a default, an example — or
// a property named for one, and those are what this walks.
func TestNoContractCarriesAMandateState(t *testing.T) {
	t.Parallel()

	spellings := []string{
		authz.StateReady.String(),
		authz.StateAwaitingReceipt.String(),
		authz.StateSpent.String(),
	}
	// From String rather than written out, so a rename in the machine cannot
	// leave this test guarding words nothing produces any more.
	require.Equal(t, []string{"ready", "awaiting_receipt", "spent"}, spellings,
		"the three states, as the machine spells them")

	for _, path := range schemaFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(path)
			require.NoError(t, err, "reading %s", path)

			var doc any
			require.NoError(t, json.Unmarshal(raw, &doc), "%s has to be JSON to be a schema", path)

			for _, found := range texts(doc) {
				assert.NotContains(t, spellings, found,
					"%s carries %q as a value or a property name, which makes a mandate state part "+
						"of the canonical model — it is the agent's own bookkeeping, served by its "+
						"owner and never sent to anybody", path, found)
			}
		})
	}
}

// TestTheWalkFindsTheSchemas proves the test above can fail.
//
// A walk that found nothing would pass forever and protect nothing, which is
// exactly the shape of failure this repository has been bitten by. So the same
// walk is asked for a string that is definitely in there.
func TestTheWalkFindsTheSchemas(t *testing.T) {
	t.Parallel()

	found := 0
	for _, path := range schemaFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var doc any
		require.NoError(t, json.Unmarshal(raw, &doc))
		if slices.Contains(texts(doc), "constraint_violated") {
			found++
		}
	}
	assert.Equal(t, 1, found,
		"error_code.json lists constraint_violated as an enum value, so a walk that cannot see "+
			"it here would see nothing anywhere")
}

// schemaFiles lists the schemas the canonical model is generated from.
func schemaFiles(t *testing.T) []string {
	t.Helper()

	found, err := filepath.Glob(filepath.Join(schemas, "*", "*.json"))
	require.NoError(t, err, "walking %s", schemas)
	require.NotEmpty(t, found,
		"no schema found under %s — this test is relative to the package that owns it, so a "+
			"directory move has to fail here rather than pass by walking nothing", schemas)
	return found
}

// texts returns every string in a decoded JSON document: property names and
// values alike.
//
// Both, because either one would be the thing this is looking for. A property
// called "mandate_state" is as much a mandate state in the model as an enum
// entry spelling one.
func texts(node any) []string {
	switch v := node.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, texts(item)...)
		}
		return out
	case map[string]any:
		var out []string
		for name, item := range v {
			out = append(out, name)
			out = append(out, texts(item)...)
		}
		return out
	default:
		return nil
	}
}
