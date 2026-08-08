package ap2

import (
	"fmt"
	"slices"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Selective disclosure minimisation: which of an open mandate's constraints a
// given verifier is shown, and what a verifier does about the ones it was not.
//
// AP2 secures mandates with SD-JWT so that a holder can show some claims and
// withhold others, and the specification turns that capability into an
// obligation: *"To ensure user privacy, Shopping Agents MUST present only the
// disclosures from the open Mandates needed in the evaluation of the closed
// Mandates."* The agent-authorization framework says the same from the holder's
// side — *"If it contains selective disclosures, the Agent MUST choose which
// disclosures to include so as to maximize user privacy."*
//
// # Relevant to what
//
// Both, and they are one question rather than two. The specification says the
// agent *"needs to determine the applicable Mandates and Disclosures ad-hoc
// based on the Checkout"*, and it assigns evaluation to the verifier of a
// particular closed mandate: *"Extract each Constraint from each open Mandate
// Content and evaluate them against the closed Mandate Content based on the
// Constraint Type."* So which closed mandate is under evaluation decides who
// the verifier is, and who the verifier is decides which facts about the
// purchase it can state. A constraint is needed in that evaluation exactly when
// the verifier can state every fact it reads.
//
// The two readings coincide because each open mandate has one audience: an open
// Checkout Mandate is only ever presented as the root of a Checkout Mandate
// chain, an open Payment Mandate as the root of a Payment Mandate one — chain.go's
// requireVCT refuses either in the other's place. What the audience then decides
// is the field set, which is where the two mandates genuinely differ: a Merchant
// holds the checkout it issued and can state everything in it, and a Credential
// Provider is sent the closed Payment Mandate and nothing else.
//
// # Why a verifier that cannot state a fact should not be shown the constraint
//
// Not to spare it the reading. constraint.Evaluate treats a fact the purchase
// does not state as *unsatisfied* — see evaluateLeaf, which is right, because
// treating unstated as permitted is how a limit stops limiting. A Credential
// Provider shown "the origin is BEG", with no item in anything it holds,
// therefore refuses every payment under that mandate. That is not the limit
// being enforced; it is a refusal made in ignorance, delivered on every
// transaction, and it makes the mandate unusable rather than safe. The
// constraint is enforced where it can be — by the Merchant, out of the open
// Checkout Mandate — and withheld where it cannot.
//
// **That is also the whole of what makes withholding safe, so it is worth
// saying rather than implying.** A constraint withheld here is enforced by
// another verifier only if another open mandate carried it to one. Nothing in
// this file can check that, because it sees one mandate. What a verifier can do
// is name the facts it will not proceed without having seen constrained — see
// ChainOptions.RequireConstrained — which is the half of this that does not
// depend on the agent's good faith.
//
// # This is not the agent evaluating constraints
//
// The hard rule is that constraints are typed and evaluated by the verifier,
// never the agent, and choosing what to disclose sits close enough to that line
// to be worth pinning. Minimise parses each constraint and reads the field
// names out of it; it never compares one against a purchase. There is no
// constraint.Subject in this file and none in Minimise's signature, so the
// distinction is structural rather than a promise — an agent here cannot
// discover which constraint would have refused the purchase, because it has
// nothing to evaluate against.

// Evaluation names the closed mandate a presentation of an open mandate is
// being narrowed for, which is what settles who the verifier is.
//
// It is not ChainOptions.Audience, which is the `aud` claim of one delegation —
// one verifier's own identifier, checked for equality. This names a *kind* of
// verifier, and the two would be a poor pair of synonyms: an audience string
// changes per merchant, and the facts a merchant can state do not.
type Evaluation string

const (
	// ForCheckout is a Merchant deciding a closed Checkout Mandate.
	ForCheckout Evaluation = "checkout"

	// ForPayment is a Credential Provider or a Network deciding a closed
	// Payment Mandate.
	ForPayment Evaluation = "payment"
)

// evaluation is one audience: who it is, and which facts about a purchase it
// can state a value for.
type evaluation struct {
	// who names the verifier in the sentence a refusal produces.
	who string

	// states is the canonical constraint fields this verifier can supply.
	//
	// Enumerated rather than derived from constraint.FieldNames, and that is
	// the point of the table: a fact added to the core registry that lands in
	// neither list is a fact nobody decided about, and TestEveryFactIsPlacedWithAVerifier
	// is what fails on it. Derived lists agree with the registry by
	// construction, including agreeing that a gap is not a gap.
	states map[string]bool

	// attributes says whether this verifier can state item attributes —
	// item.attr.<name>, the one part of the vocabulary that is open by
	// construction and therefore cannot be enumerated above.
	attributes bool
}

// evaluations is which facts each verifier of a closed mandate can state.
//
// **The Merchant's list is every fact the registry holds, and that is a finding
// rather than a placeholder.** It issued the checkout, so it knows the item,
// the quantity, the category, the price and its own identity and trade; there
// is nothing in constraint.Subject it cannot fill in. Minimising an open
// Checkout Mandate therefore withholds nothing today, and saying so plainly is
// better than a reader inferring a bug. The row still earns its place: it is
// what makes the day a fact arrives that a merchant cannot state — an
// instrument's type, say — a change to this table rather than a silent
// widening of what merchants are shown.
//
// The Credential Provider's list is short for a reason written into the
// protocol: AP2 sends it the Payment Mandate and nothing else, so the only
// facts it holds are the ones that mandate carries. payment_amount is the
// amount, payee is the merchant — by id only, because contracts/identity/merchant.json
// has no category — and `at` comes from its own clock, which every verifier
// has. Item, quantity, category and every item attribute are absent, and a
// Credential Provider cannot acquire them without being sent a document AP2
// does not send it.
// knownFields is the closed half of the constraint vocabulary, read once
// because it does not change at runtime. What is not in it, for a field name
// that came out of a parsed Expression, is an item attribute — see canState.
var knownFields = constraint.FieldNames()

var evaluations = map[Evaluation]evaluation{
	ForCheckout: {
		who: "the Merchant, which holds the checkout it issued",
		states: map[string]bool{
			"amount":            true,
			"at":                true,
			"quantity":          true,
			"item.id":           true,
			"item.category":     true,
			"merchant.id":       true,
			"merchant.category": true,
		},
		attributes: true,
	},
	ForPayment: {
		who: "a Credential Provider or Network, which is sent the Payment Mandate and nothing else",
		states: map[string]bool{
			"amount":      true,
			"at":          true,
			"merchant.id": true,
		},
		attributes: false,
	},
}

// canState reports whether this verifier can supply a value for every fact the
// constraint reads, and can therefore reach a verdict on it that means
// something.
//
// A name knownFields does not hold is an item attribute. That is not an
// assumption: constraint.Parse admits a leaf's field only if the registry holds
// it or it carries the item-attribute prefix, so for a field name that came out
// of a parsed Expression there is no third case — which is why this needs no
// copy of the prefix string, and cannot drift from one.
func (e evaluation) canState(fields []string) bool {
	for _, name := range fields {
		if !slices.Contains(knownFields, name) {
			if !e.attributes {
				return false
			}
			continue
		}
		if !e.states[name] {
			return false
		}
	}
	return true
}

// Minimise narrows a presentation of an open mandate to the disclosures the
// verifier named by to actually needs.
//
// The returned SD-JWT carries the same Issuer-signed JWT and a subset of its
// Disclosures, so it verifies exactly as the full one does — the digests of the
// withheld constraints stay in the signed payload and simply match nothing.
// constraints is an array, so the step that governs is RFC 9901 §7.1 step 3.d,
// which removes an element whose digest matched nothing rather than leaving a
// hole; the withheld-or-decoy rule a reader is more likely to have in mind is
// step 3.c.i, and that one is about an object's _sd array.
//
// Call it **before** delegating. sdjwt.Delegate binds sd_hash over the root as
// presented, so narrowing afterwards produces a chain whose delegation no
// longer covers its own root.
//
// # What this does not hide
//
// It hides the *content* of the withheld constraints and nothing else, and the
// difference matters enough to be stated rather than left to a reader who
// assumes selective disclosure hides more than it does. The signed payload
// carries one `{"...": digest}` element per constraint whether or not that
// constraint is disclosed, in issuance order. So a verifier holding this
// presentation can see how many constraints the mandate carries, how many were
// withheld from it, and which positions they occupied. What it cannot do is
// learn what they said, or tell a withheld constraint from an Issuer's decoy —
// that indistinguishability is the property the salt buys, and it is the whole
// of what is bought.
//
// # What it refuses
//
// A Disclosure it cannot classify, rather than guessing in either direction.
// Named Disclosures are kept, because on an open mandate they are claims rather
// than constraints and every one of those is read by a verifier: cnf and the
// timestamps by authz.Endorsement, and a Payment Mandate's pinned payee, amount,
// instrument and execution date by authz.checkPinned. This package blinds none
// of them — blindPaths gets "constraints[]" and nothing else from
// generated.Disclosable for either open mandate — but a mandate from another
// implementation may, and dropping one would hand a verifier an open mandate
// that endorses nobody.
// An array element that does not decode as a constraint of this adapter's own
// type is refused through decodeConstraints, the same refusal a verifier makes
// on the way in, so a constraint type nobody here can read is never silently
// dropped from a presentation either.
func Minimise(sd *sdjwt.SDJWT, to Evaluation) (*sdjwt.SDJWT, error) {
	if sd == nil {
		return nil, fmt.Errorf("%w: no SD-JWT to narrow", ErrMisconfigured)
	}
	audience, ok := evaluations[to]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a verifier this adapter knows the reach of",
			ErrMisconfigured, to)
	}

	// Decided up front rather than inside the predicate, because Present takes
	// a predicate that cannot fail and a constraint this package cannot read is
	// a failure rather than a false.
	disclosures := sd.Disclosures()
	keep := make(map[string]bool, len(disclosures))
	for _, d := range disclosures {
		if _, named := d.Name(); named {
			keep[d.String()] = true
			continue
		}

		cs, err := decodeConstraints([]any{d.Value()})
		if err != nil {
			return nil, err
		}
		parsed, err := constraint.Parse(cs[0])
		if err != nil {
			return nil, err
		}
		keep[d.String()] = audience.canState(parsed.Fields())
	}

	return sd.Present(func(d sdjwt.Disclosure) bool { return keep[d.String()] })
}

// requireConstrained refuses a presentation whose disclosed constraints say
// nothing about a fact this verifier will not proceed without.
//
// # Why this is the shape the check has to take
//
// Because the obvious shape does not exist. A verifier cannot detect that a
// constraint was withheld from it: RFC 9901 makes an undisclosed digest and an
// Issuer's decoy indistinguishable by design — pkg/sdjwt/verify.go says so at
// the line that ignores one — and the processed payload drops the element
// entirely rather than leaving a hole. Counting would not help even if the
// count were reachable: "one of six was withheld" tells a verifier nothing
// about whether the one mattered, so a verifier refusing on any withholding
// forbids minimisation outright and one accepting learns nothing.
//
// So the verifier states its own requirement instead, and the requirement is
// about *facts* rather than about constraints it has never seen. "I will not
// authorise a purchase against a mandate that does not limit the amount" is
// something a verifier knows without being told, and it is checkable against
// what it was shown. That is the reading of disclosure_insufficient this file
// implements: *a claim this verifier needs was withheld*, where the verifier is
// the one that says what it needs.
//
// **What it does not close.** A constraint on a fact nobody required is
// withheld undetectably, and that is a property of selective disclosure rather
// than of this function. The remedy is elsewhere and is worth naming: the agent
// signs sd_hash over the root exactly as presented, so the narrowing it chose
// is attributable to it afterwards, in the evidence a dispute reads.
//
// An empty required list is a verifier with no such policy, not a verifier that
// checks nothing by accident. Every caller that existed before this check did
// leaves it empty, which is why no chain test written for #12 changed.
func requireConstrained(cs []generated.Constraint, required []string) error {
	if len(required) == 0 {
		return nil
	}

	stated := make(map[string]bool)
	for i, c := range cs {
		parsed, err := constraint.Parse(c)
		if err != nil {
			return fmt.Errorf("constraint %d: %w", i, err)
		}
		for _, name := range parsed.Fields() {
			stated[name] = true
		}
	}

	for _, name := range required {
		if !stated[name] {
			return fmt.Errorf(
				"%w: this verifier does not authorise without a limit on %s, and none of the %d constraints disclosed to it names one",
				ErrDisclosureInsufficient, name, len(cs))
		}
	}
	return nil
}
