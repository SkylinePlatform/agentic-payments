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
// It cannot cost a purchase the user did not authorise — see the second point
// above. What it can cost is buying a different authorised offer than the person
// would have picked, which is a cost a *reader* can catch: the preference travels
// to the consent screen beside every candidate the search found, so "this one,
// because it is the cheapest of four" is checkable by the person about to sign.
// interpret.Rank's "Why a rank need not be signed" section is that argument in
// full.

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
// # An unnamed currency is refused too, and nothing on the wire can reach it
//
// A price carrying no currency code passes a uniformity check against another one
// carrying none — "" equals "" — and tells us nothing about whether the two
// integers beside them mean the same thing. That is the same trap with the label
// missing rather than differing, so it fails here as well.
//
// **It is unreachable from candidates and that was worth checking rather than
// assuming.** generated.Amount's own unmarshaller requires the field, so a merchant
// answering `"price":{"amount":100}` fails the decode with "field currency in
// Amount: required" and never gets this far; the only way in is a caller building a
// candidate in Go, which is TestRankedRefusesOffersItCannotCompare.
//
// It stays for the reason a duplicated check is normally *wrong* here and this one
// is not. What would be wrong is a second list of field names or a second parser —
// something that could come to disagree with the original and would drift in the
// direction of accepting more. This is a precondition of a comparison, stated where
// the comparison is, and its failure mode if the schema ever made currency optional
// is that the comparison silently becomes meaningless. A check that costs one branch
// and whose absence is undetectable is the shape to keep.
//
// # Two currencies is unreachable through `make demo` too, and that is not an
// # argument against it either
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
