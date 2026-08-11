package constraint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
)

// TestSelectiveAnswersForBothHalvesOfTheVocabulary is issue #132's second step.
//
// Whether a field says *what to go looking for*, rather than stating a term the
// purchase has to meet, is a property of the field. It has lived as a column on
// Field since #132's first step; what it did not have was a way to be asked by
// name. The one caller that needs it — internal/agent, building a merchant
// search — may not import this package, so it kept the same fact as two string
// prefixes and a test held the two in step. This function is what lets that
// second copy be deleted, so the answers below are now the whole of the fact
// rather than one of two statements of it.
//
// Nothing here is about selective *disclosure*, which is SD-JWT's and is
// decided per mandate rather than per field. See internal/adapters/ap2/disclose.go.
//
// # Both halves, and the open one is why this is a function rather than a column
//
// Vocabulary already published the bit for the closed registry, and a caller
// walking it could read it there. What no walk can answer for is
// item.attr.<name>: it is minted per name by lookupField rather than held in
// the table, so it appears in no listing, and before this the only way to
// classify one was to notice that the family's prefix begins with "item." — a
// coincidence between two literals in two packages that no compiler relates,
// which #169 had to assert rather than derive. Asking by name routes both
// halves through lookupField, so the open half is answered by the code that
// mints it.
//
// # An unknown name is not selective, and that is the safe direction
//
// A field this verifier cannot read is one it will refuse as
// constraint_type_unknown at the moment of purchase. Answering true would put
// that name into a merchant's catalogue search, where it would be refused as a
// malformed query or, worse, quietly match nothing. Answering false withholds
// it from the search and leaves it in the mandate, where the verifier rejects
// it with a code that names the problem.
func TestSelectiveAnswersForBothHalvesOfTheVocabulary(t *testing.T) {
	t.Parallel()

	// closed is every field the registry holds. It is keyed by name so that the
	// coverage check below can be a set comparison rather than a second list.
	closed := map[string]struct {
		want bool
		why  string
	}{
		"amount": {false,
			"the price is the term a watch exists to wait for; a search carrying it returns " +
				"nothing at all while the price is still too high"},
		"at": {false,
			"when a purchase may happen describes the purchase, not the thing being bought"},
		"quantity": {false,
			"how many is a term of the sale; the catalogue sells the same offer either way"},
		"item.id": {true,
			"naming the object is the plainest thing a search can be built on"},
		"item.category": {true,
			"a category narrows what to go looking for rather than gating the sale"},
		"merchant.id": {true,
			"\"buy it from this shop\" describes what to look for as much as \"buy this bicycle\" does"},
		"merchant.category": {true,
			"a trade narrows the search the same way a category does"},
	}

	for name, tc := range closed {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, constraint.Selective(name), tc.why)
		})
	}

	// The open half and the names that are in neither half. These cannot be
	// covered by a walk, which is the whole reason the question is asked by name.
	for _, tc := range []struct {
		field string
		want  bool
		why   string
	}{
		{"item.attr.route.origin", true,
			"the built scenario's flight is discovered by its route and by nothing else, so an " +
				"answer of false here would leave that search matching every flight the merchant sells"},
		{"item.attr.fare", true,
			"every name in the open half is selective, whatever it is called — the family is a " +
				"rule rather than a list, so this must not depend on the name having been seen before"},
		{"item.attr.", false,
			"the bare prefix addresses no attribute at all, so it is a name no verifier can read"},
		{"channel.id", false,
			"a field the verifier does not know is refused at the moment of purchase; putting it " +
				"in a search would ask a merchant a question in a vocabulary neither party shares"},
		{"item", false,
			"the stem of two selective names is not itself a name, and a prefix test is exactly " +
				"what said otherwise"},
		{"", false,
			"the empty name is no name; a group node carries no field and must never reach here " +
				"as one"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, constraint.Selective(tc.field), tc.why)
		})
	}

	// Coverage, not a second copy of the answers. A field added to the registry
	// with no row above is one this table says nothing about, and a table of
	// names is only as good as its coverage of the registry that grows.
	//
	// It is not what stops a new field being *misclassified*: fields is built
	// from term and selector, so an entry declaring neither does not compile.
	for _, name := range constraint.FieldNames() {
		_, covered := closed[name]
		assert.True(t, covered,
			"%q is registered and classified by nothing above; add a row saying which of the "+
				"two it is and why, or the case this test exists to state is one nobody stated", name)
	}
}
