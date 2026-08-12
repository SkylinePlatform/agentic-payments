package agent

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
)

// Applying the preference the sentence stated, which is issue #262's half of the
// word "cheapest".
//
// interpret.Rank is where the argument for a rank existing at all lives, and
// docs/specs/2026-08-12-ranking-among-authorised-offers.md is the long form. This
// file is only the applying, and it is in its own file because the one thing worth
// being sure of about it is the boundary in the next section.
//
// # Ordering candidates is not evaluating a constraint
//
// AGENTS.md gives constraint evaluation to the verifier and to nobody else, and
// this is the code in internal/agent that sits closest to that line. It does not
// cross it, and the reason is not a promise:
//
//   - **There is no limit on either side of any comparison here.** Evaluation asks
//     whether a purchase satisfies something the user signed, and answers with a
//     verdict that decides whether it may proceed. This asks which of two offers a
//     sentence preferred, and answers with an order. No signed value is read, no
//     subject is built, and nothing here can say that a purchase is or is not
//     allowed.
//   - **It cannot add a candidate.** Every element of the slice it is handed is an
//     offer the merchant already said matches the narrowing query built from the
//     signed constraints. Reordering a set cannot enlarge it, so every offer this
//     can choose still has to pass a verifier at the moment of purchase, and
//     beat 5 of the built scenario is that happening.
//   - **The import graph still holds.** internal/agent does not import
//     internal/core/authz/constraint, so no file in this package can name
//     constraint.Parse, constraint.Subject or Expression.Evaluate, and
//     TestTheAgentCannotReachAConstraintEvaluator is what keeps that true rather
//     than this comment.
//
// The same distinction one party along is why agent.Watch is structurally rather
// than merely dutifully clean: it reads a step index and a final flag and compares
// no money to anything. Ranking compares money to money, never money to a limit,
// and those are different acts.
//
// # What a wrong rank costs, and what it cannot cost
//
// It cannot cost a purchase the user did not authorise — see the second point above,
// and interpret.Rank's "Why a rank need not be signed" for the argument in full.
//
// **What it can cost is more than "a different authorised offer", and the review of
// this branch is what found the stronger case.** candidates deliberately omits the
// amount term from the query — a search carrying the user's cap can only ever find
// something the agent could already buy, which is the one case a watch is not for —
// so candidates *above* the cap are in the set this function is handed. Nothing here
// compares any of them to it. A descending preference therefore selects the dearest
// candidate, which for a shelf straddling the bound is one the verifier will refuse:
// the run ends in a refusal it need not have collected, where a rankless run would
// have bought the merchant's first offer.
//
// That is the same shape as a wrong Trigger's cost — "a run that ends sooner than the
// person hoped", collecting a visible refusal at a price the merchant was openly
// asking — rather than a new kind of harm. **And the obvious fix is forbidden**, which
// is why this is documented rather than closed: skipping candidates above the cap
// would be the agent comparing an offer's price to the user's signed limit, which
// AGENTS.md gives to the verifier and to nobody else. A refusal a reader can see beats
// a filter nobody can audit, so the failure direction is the right one.

// ErrCannotRank means the sentence stated a preference that cannot be applied to
// the offers this merchant answered with.
//
// **It wraps ErrNothingToBuy as well**, on ErrMerchantAnsweredDifferently's
// reasoning: a caller checking only for that one still finds this is a case of it,
// because there is nothing here this proposal can honestly use. console.Service
// therefore answers 422 without a new arm, which is the right status — the shop is
// not misbehaving and the agent is not broken; the request cannot be turned into a
// purchase this system is able to justify.
//
// The alternative was to fall back to catalogue order and carry on, and it is the
// one thing that must not happen. A run that was asked for the cheapest and
// silently bought the first is exactly the defect issue #262 exists to close,
// except that this time the agent would have had the preference in its hand.
var ErrCannotRank = errors.New(
	"agent: the offers this merchant answered with cannot be ordered by the preference the sentence stated")

// applies is the preference that will actually be applied, given what the caller
// already chose: the sentence's own, or none at all when the caller named an item.
//
// **One function rather than a condition at each site, because two sites read it and
// they must not be able to disagree.** settle uses it to decide what to sort;
// Propose uses it to decide what to *report* on Proposal.Rank, which is what the
// consent screen turns into "the cheapest of the offers that matched". A screen
// stating a reason the agent did not act on is worse than one stating none — it is a
// false account of how the offer in front of the person was chosen, and the caller
// named that offer by hand.
//
// # Why a named item wins
//
// A rank chooses among candidates. A caller who named one has already chosen — see
// Intent.Item, where the console's product table is the thing doing the choosing —
// so a preference read out of a sentence must not overrule a choice made by a
// person.
//
// It is also what keeps settle's refusal honest. A search on item.id has exactly one
// answer, and settle compares the merchant's *own first* answer against the
// identifier that was asked for. Sorting first would let a merchant answering
// `[something cheaper, the offer asked for]` sort its way past that check, so the
// preference would have laundered a refusal into a purchase of an offer the caller
// never picked. TestARankCannotLaunderAMerchantsWrongAnswer drives both price
// orderings, because only one of the two can be got wrong silently.
func applies(preference interpret.Rank, chosen string) interpret.Rank {
	if chosen != "" {
		return interpret.Rank{}
	}
	return preference
}

// ranked orders the candidates a search came back with by the preference the
// sentence stated, and hands them back untouched when it stated none.
//
// A pure function of its arguments, and it clones rather than sorting in place so
// that it cannot quietly reorder a slice a caller still holds.
//
// # Silence returns the merchant's own order, unsorted
//
// Not sorted-by-nothing: returned as it arrived. That is what keeps `make demo`
// byte-for-byte the demonstration it was — the flight and the bicycle rank
// nothing, so nothing in this file touches them, and every golden number in this
// repository's documentation still comes from the order #255 made defensible
// (committed offers ahead of fetched ones, then by identifier).
//
// # A stated preference sorts stably
//
// slices.SortStableFunc rather than SortFunc, so that two offers at the same price
// keep the merchant's order between them. An unstable sort would make a screenshot
// of a shelf with two equal prices differ between runs for no reason anybody could
// point at, which is the property this repository's numbers depend on and the
// cheapest one to lose by accident.
func ranked(found []candidate, preference interpret.Rank) ([]candidate, error) {
	if !preference.Stated() {
		return found, nil
	}

	// Both halves are closed sets and each gets its own refusal, because
	// interpret.Validate has already refused an incomplete rank on the Propose
	// path and a guard standing behind another guard still has to give the right
	// answer — the same argument itemIDField's comment makes about the two
	// prefixes that used to live beside it. Falling through to a default order
	// would be the silent skip AGENTS.md's "Open for extension is not open at
	// runtime" paragraph forbids, one vocabulary along.
	direction, known := ordering(preference.Direction)
	if !known {
		return nil, fmt.Errorf("%w: %w: %q is not an order this agent can put offers in",
			ErrCannotRank, ErrNothingToBuy, preference.Direction)
	}

	switch preference.By {
	case interpret.RankByPrice:
		if err := onePriceCurrency(found); err != nil {
			return nil, err
		}
		out := slices.Clone(found)
		slices.SortStableFunc(out, func(a, b candidate) int {
			return direction * cmp.Compare(a.Price.Amount, b.Price.Amount)
		})
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %w: %q is not a fact this agent can order offers on",
			ErrCannotRank, ErrNothingToBuy, preference.By)
	}
}

// ordering turns a direction into the sign to multiply a comparison by: +1 for
// ascending, -1 for descending, and false for a direction this package cannot put
// offers in.
//
// A sign rather than two comparison functions, so that a second orderable fact
// arriving in interpret.RankField writes one comparison and gets both directions.
func ordering(d interpret.RankDirection) (int, bool) {
	switch d {
	case interpret.RankAscending:
		return 1, true
	case interpret.RankDescending:
		return -1, true
	default:
		return 0, false
	}
}

// onePriceCurrency refuses a candidate set whose prices cannot be compared.
//
// # Why this is a refusal and not a comparison of minor units
//
// An Amount is an integer in minor units and an ISO 4217 code, and
// contracts/instrument/amount.json is explicit that the model carries no rate
// between two codes and no way to acquire one. So 100 JPY and 100 USD are two
// amounts whose minor units are equal and whose values are not, and a sort over
// the integers alone would put the yen first roughly a hundred and fifty times out
// of a hundred while reporting that it had found the cheapest.
//
// The alternatives are worse in the way that matters. Converting means inventing an
// exchange rate in the agent, which is a far larger product decision than the sort
// order this whole change exists to stop the agent making by accident, and it would
// have to be inventing one *at the moment of purchase* with nobody watching.
// Ranking within one currency and ignoring the rest means the offers a person could
// have bought silently stop being candidates. Falling back to catalogue order is
// issue #262 again. A refusal names the thing this system cannot do, in the run
// that asked it to, which is the only outcome a reader can act on.
//
// # An unnamed currency is refused too, and it is reachable
//
// A price carrying no currency code passes a uniformity check against another one
// carrying none — "" equals "" — and tells us nothing about whether the two
// integers beside them mean the same thing. That is the same trap with the label
// missing rather than differing, so it fails here as well.
//
// **Two ways in, and the first draft of this comment claimed there were none.**
// `"price":{"amount":100}` does fail earlier: generated.Amount's own unmarshaller
// requires the field, so candidates reports "field currency in Amount: required" and
// never gets here. But an offer with the `price` key **omitted entirely** never
// invokes that unmarshaller at all — encoding/json leaves the field at its zero value
// — so `{"offers":[{"id":"gtin:0001","title":"no price"}]}` decodes clean and arrives
// here priced at nothing in nothing. TestRefusesToOrderOffersItCannotCompare drives
// that one over the wire, and TestRankedRefusesOffersItCannotCompare drives the
// in-Go one.
//
// **An adjacent gap that is not this issue's to close**, named because finding it is
// what corrected the paragraph above: for a sentence that ranks *nothing*, an offer
// with no price at all is not refused anywhere. It becomes a candidate reading
// `0.00` with no currency and can be found[0]. That is a defect in what candidates
// accepts from a merchant rather than in what this function compares, and closing it
// belongs with the identifier check beside it.
//
// # Refused over the whole set, not over the comparisons actually made
//
// One offer priced in another currency refuses the proposal even when the winner
// would have been unambiguous without it. That is the intended unit, and the reason is
// the alternative: ranking the comparable ones and ignoring the rest is the
// "offers a person could have bought silently stop being candidates" line from the
// table below, arriving through the back door. A preference is over the candidate
// set, so a preference that cannot be applied to the candidate set cannot be applied.
//
// The cost is worth naming: a merchant publishing one EUR offer in a category can
// suppress every ranked sentence in it. That is a loud refusal naming both currencies
// rather than a quiet wrong answer, which is the trade this whole function is built
// on.
//
// # Two currencies is unreachable through `make demo`, and that is not an
// # argument against any of it
//
// deploy/catalogue.json is USD throughout and so is every offer merchant/shop
// derives from a fetched one, so no committed path can provoke this. The check is
// against the shape of the model rather than against the shape of today's data —
// #250's own record of a rule that was "safe by data, not by rule" is the
// precedent, and the currency column is the one field in an Amount that a second
// catalogue would move first.
func onePriceCurrency(found []candidate) error {
	var first string
	for i, c := range found {
		switch {
		case c.Price.Currency == "":
			return fmt.Errorf("%w: %w: %s is priced with no currency, so there is nothing to compare its amount against",
				ErrCannotRank, ErrNothingToBuy, c.ID)
		case i == 0:
			first = c.Price.Currency
		case c.Price.Currency != first:
			return fmt.Errorf(
				"%w: %w: the offers are priced in both %s and %s, and this model holds no rate between them",
				ErrCannotRank, ErrNothingToBuy, first, c.Price.Currency)
		}
	}
	return nil
}
