package merchant

import (
	"net/http"
	"slices"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// ShelvesPath is where this merchant publishes the categories it sells.
//
// Exported for the reason ItemParam, QuantityParam and SearchParam are: whoever
// builds the URL is outside this package, and internal/agent holds its own copy
// rather than importing the seller to read one string —
// TestTheAgentSpellsTheMerchantsQueryParameters is what keeps the two in step.
//
// **That test matters more for this path than for the three parameters.** The
// agent treats an unanswered fetch as "this merchant publishes nothing" and
// carries on, because a merchant that does not publish its shelves is a merchant
// the model has to guess at, which is where this was before issue #254 — see
// Client.shelves. So a rename on this side would not fail anything at run time:
// it would quietly restore the defect, with the interpreter guessing again and
// no error anywhere naming the cause.
const ShelvesPath = "/shelves"

// Shelves is what GET /shelves answers: the categories this catalogue sells.
//
// # Why the categories and not the attribute values
//
// This is the *closed* half of what a constraint can say about an object, and
// the halves are closed and open for a structural reason rather than by choice.
// A category is a shelf: the set of them is bounded by how many shelves the shop
// has, so publishing it costs 24 to 30 short strings against a catalogue of 257
// offers and does not grow when the shop puts more stock on the same shelves.
// The values under item.attr.<name> are bounded by nothing — every route, brand,
// venue and colour across every offer — and a published set that grew with the
// catalogue would end up in a model's instruction, where a prompt that grows
// with the shop stops being a prompt.
//
// So the open half is not published at all, and the interpreter is told to narrow
// by an attribute value only where the sentence names one the shop would hold
// verbatim. Issue #254 records the measurement that forced the split: a model
// reading "buy a flight to Palma when it drops below $200" answered
// `item.category eq "flight"` where the shop says `flights`, and
// `item.attr.route.destination eq "Palma"` where it says `PMI` — two reasonable
// human readings, ANDed with everything else, each matching nothing.
//
// # It is not in contracts/, and that is the same line Offer draws
//
// What a shop calls its own shelves is the shop's, not a fact every purchase
// has. The canonical model knows that an item has a category; which categories
// exist is this merchant's answer about this merchant's stock, exactly as Title
// and ImageURL are, and a schema in contracts/ would mean core knew what a flight
// is.
type Shelves struct {
	// Categories is every category this catalogue sells, sorted, each in the
	// shop's own spelling.
	//
	// Never nil: NewCatalogue refuses a catalogue with no offers and every offer
	// has a category, so an empty array here would be a shop with nothing on any
	// shelf.
	Categories []string `json:"categories"`
}

// Categories lists the categories this catalogue sells, sorted, each in the
// shop's own spelling.
//
// # Distinct as the verifier counts distinct, published as the shop writes it
//
// Two offers filed under "Flights" and "flights" are one shelf, because
// item.category is compared folded — constraint.FoldText is that comparison, and
// asking for it rather than lower-casing here is what stops this list drawing a
// distinction no verifier makes. What is published is the first spelling the
// catalogue's own order carries, because a constraint written against this list
// is the constraint a verifier will enforce and it should read the way the shop
// writes it.
//
// The copy is the copy Offers hands out, one field narrower: nothing a caller
// holds is what a later search matches against.
func (c *Catalogue) Categories() []string {
	seen := make(map[string]struct{}, len(c.offers))
	out := make([]string, 0, len(c.offers))
	for _, o := range c.offers {
		key := constraint.FoldText(o.Category)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, o.Category)
	}
	slices.Sort(out)
	return out
}

// shelves answers GET /shelves.
//
// A safe method, so no idempotency key reaches it and nothing is remembered —
// roles.Middleware skips safe methods by RFC 9110, which is the same reason
// GET /search may be polled. It has no failure of its own to report: the route
// is registered only when there is a Catalogue to ask, and a catalogue that
// exists always sells something.
func (s *Service) shelves(w http.ResponseWriter, _ *http.Request) {
	roles.OK(w, http.StatusOK, Shelves{Categories: s.Catalogue.Categories()})
}
