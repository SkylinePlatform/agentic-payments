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
// requireVCT refuses either in the other's place. Minimise reads that audience
// off the mandate's own vct rather than taking it from its caller, for the
// reason its doc comment gives: a caller that could name the other one could
// narrow away the constraint that would have refused the purchase.
//
// **The specification's own granularity is the checkout, and ours is the role.**
// *"Ad-hoc based on the Checkout"* is per transaction; the table below is per
// verifier, fixed at compile time. The two agree whenever a verifier of a given
// kind holds the facts the table says it holds, and diverge the moment one
// holds fewer — a merchant whose checkout omits a category is shown a
// constraint on `item.category` it will refuse in ignorance. Closing that would
// mean deriving the reach from the actual document, per transaction, which is a
// real design and not this one. See the spec for why a proof of concept stops
// here.
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
// # The cost of that rule, which is real and is not a rounding error
//
// **Disclosure granularity is the top-level constraint, so "every fact it
// reads" makes one group all-or-nothing.** A user's intent written as four
// top-level constraints gives a Credential Provider the two it can apply. The
// identical intent written as a single `all` group — which is a legal
// constraint, and how internal/core/authz/constraint's own tests write the same
// scenario — reads facts that verifier cannot state, so the whole group is
// withheld and the price cap inside it is enforced by nobody at that verifier.
//
// This is the lesser of two losses rather than the correct answer, and it is
// worth being plain that the other branch is also unsafe: disclosing the group
// makes the Credential Provider refuse every transaction in ignorance, which is
// not enforcement either. What closes the floor under it is
// requireSomeConstraintDisclosed below — a mandate whose *only* constraint is
// such a group narrows to nothing, and a presentation of nothing is refused
// outright rather than read as a mandate with no limits. What stays open is the
// mixed case: a group withheld alongside another constraint that survives. A
// verifier's own RequireConstrained is the answer there, and it is a policy
// rather than a guarantee.
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

// Evaluation names the closed mandate an open one will be evaluated against,
// which is what settles who the verifier is.
//
// It is not a parameter anywhere. Minimise derives it from the mandate's own
// vct, because the audience is a property of the credential and not a choice
// its holder makes — see that function.
//
// It is also not ChainOptions.Audience, which is the `aud` claim of one
// delegation: one verifier's own identifier, checked for equality. This names a
// *kind* of verifier, and the two would be a poor pair of synonyms — an
// audience string changes per merchant, and the facts a merchant can state do
// not.
type Evaluation string

const (
	// ForCheckout is a Merchant deciding a closed Checkout Mandate.
	ForCheckout Evaluation = "checkout"

	// ForPayment is a Credential Provider, a Network or a Merchant Payment
	// Processor deciding a closed Payment Mandate.
	//
	// Three verifiers, one row, and the third is the one to be careful about.
	// AP2 names all three — contracts/authz/payment_mandate.json says so — and
	// this row credits them with what a *Credential Provider* holds, which is
	// the narrowest of the three. An MPP sits merchant-side and may well hold
	// the checkout, in which case this row withholds constraints it could have
	// enforced. That is the safe direction and it is not the right one; the
	// spec records it as a gap rather than a decision, because nothing in this
	// package routes an MPP through a chain today — MPPRules.VerifyCredential
	// answers a different question entirely, about a credential rather than a
	// mandate.
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
	//
	// **Nothing ties this to the constraint.Subject a role actually builds.**
	// It is this package's statement of what each verifier *can* hold, and a
	// role populating less refuses in ignorance while one populating more has
	// constraints withheld that it could have enforced. Keeping the two in step
	// is a caller's obligation today; internal/adapters/ap2's own
	// credentialProviderSubject honours it by hand and says so.
	states map[string]bool

	// attributes says whether this verifier can state item attributes —
	// item.attr.<name>, the one part of the vocabulary that is open by
	// construction and therefore cannot be enumerated above.
	attributes bool
}

// knownFields is the closed half of the constraint vocabulary, read once
// because it does not change at runtime. What is not in it, for a field name
// that came out of a parsed Expression, is an item attribute — see canState.
var knownFields = constraint.FieldNames()

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
		who: "a Credential Provider, Network or Merchant Payment Processor, credited here with what the first of those holds: the Payment Mandate and nothing else",
		states: map[string]bool{
			"amount":      true,
			"at":          true,
			"merchant.id": true,
		},
		attributes: false,
	},
}

// audienceOf is the one open mandate type each verifier reads, keyed by the vct
// that names it.
//
// The two closed types are deliberately absent rather than mapped to anything.
// A closed mandate is already bound to a transaction and carries no constraints
// array, so there is nothing in one to minimise and no audience to minimise it
// for; Minimise refuses it here rather than narrowing a credential whose shape
// it has not understood.
var audienceOf = map[string]Evaluation{
	VCTCheckoutOpen: ForCheckout,
	VCTPaymentOpen:  ForPayment,
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
// verifier that will evaluate it actually needs.
//
// # It takes no audience, and that is the point
//
// The audience is read from the mandate's own vct. An earlier shape took it as
// a parameter, and that parameter was a hole big enough to drive the whole
// issue through: Minimise(openCheckoutMandate, ForPayment) narrowed a Merchant's
// mandate for a Credential Provider's reach, dropped the route pins the user
// set, and the Merchant then authorised a flight to the wrong city with
// err == nil. One wrong enum let the holder evaluate away the constraint that
// would have refused the purchase — the very line this file's header claims is
// structural. A caller cannot name the wrong audience if there is no audience
// to name.
//
// A vct that is not one of the two open mandates is refused rather than
// narrowed: a closed mandate has no constraints array and no audience of its
// own, and a credential type this adapter has never heard of is one whose shape
// it cannot make a disclosure decision about.
//
// The vct is read from the Issuer-signed payload without verifying the
// signature, which is sound here in a way it would not be in a verifier. This
// is the Holder narrowing its own credential; a mandate whose vct has been
// tampered with narrows wrongly and is then refused by the verifier's own
// requireVCT, which reads the *verified* claims. Nothing downstream trusts this
// read.
//
// # What it produces
//
// The returned SD-JWT carries the same Issuer-signed JWT and a subset of its
// Disclosures, so it verifies exactly as the full one does — the digests of the
// withheld constraints stay in the signed payload and simply match nothing.
// constraints is an array, so the step that governs is RFC 9901 §7.1 step 3.d,
// which removes an element whose digest matched nothing rather than leaving a
// hole. The withheld-or-decoy rule a reader is more likely to have in mind is
// step 3.c.i, and that one is about an object's _sd array — pkg/sdjwt's
// processObject carries it, and processArray, which is the branch a constraint
// actually travels through, carries 3.d.
//
// Call it **before** delegating. sdjwt.Delegate binds sd_hash over the root as
// presented, so narrowing afterwards produces a chain whose delegation no
// longer covers its own root.
//
// # What this does not hide
//
// It hides the *content* of the withheld constraints, and not the fact that
// there were any. The signed payload carries one `{"...": digest}` element per
// constraint whether or not that constraint is disclosed, in issuance order, so
// a verifier holding this presentation can count how many the mandate carries,
// how many were withheld from it, and which positions they occupied —
// sdjwt.SDJWT.SignedClaims is that computation and requireSomeConstraintDisclosed
// depends on it. What it cannot learn is what they said, or which of two
// withheld positions held what. Decoys do not close the gap either: pkg/sdjwt
// adds them to _sd arrays only and never to arrays of values, on the deliberate
// grounds that padding an array makes an application counting its elements read
// a number the Issuer invented.
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
func Minimise(sd *sdjwt.SDJWT) (*sdjwt.SDJWT, error) {
	if sd == nil {
		return nil, fmt.Errorf("%w: no SD-JWT to narrow", ErrMisconfigured)
	}

	signed, err := sd.SignedClaims()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMandateMalformed, err)
	}
	audience, err := audienceFor(signed)
	if err != nil {
		return nil, err
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

// audienceFor resolves the one verifier a mandate carrying these claims will be
// evaluated by.
//
// It reports the mandate types this adapter *can* narrow when it refuses, since
// the caller that gets this wrong is holding a credential and wondering which
// of four it has.
func audienceFor(signed map[string]any) (evaluation, error) {
	raw, ok := signed[vctClaim]
	if !ok {
		return evaluation{}, fmt.Errorf("%w: no %s claim, so there is no audience to narrow for",
			ErrMandateMalformed, vctClaim)
	}
	vct, ok := raw.(string)
	if !ok {
		return evaluation{}, fmt.Errorf("%w: %s must be a string, got %T",
			ErrMandateMalformed, vctClaim, raw)
	}
	name, ok := audienceOf[vct]
	if !ok {
		return evaluation{}, fmt.Errorf(
			"%w: %s is %q, and only an open mandate (%q, %q) has constraints to narrow and one audience to narrow them for",
			ErrWrongMandateType, vctClaim, vct, VCTCheckoutOpen, VCTPaymentOpen)
	}
	return evaluations[name], nil
}

// requireSomeConstraintDisclosed refuses a presentation that withheld every
// constraint the open mandate committed to.
//
// # Why this one needs no configuration
//
// It is the floor under RequireConstrained, and it is the only part of this
// that is verifier-independent. An open mandate whose constraints all reached a
// verifier as undisclosed digests arrives as constraint.Report{} — and an empty
// report is *satisfied*, correctly, because a mandate carrying no constraints is
// one where the user placed no limits. That correctness is what makes the
// narrowed-to-nothing case dangerous: the two states are identical in the
// processed payload, and the second authorises a purchase against limits nobody
// read.
//
// They are not identical in the *signed* payload. It commits to one digest per
// constraint whether disclosed or not, so "the user set no limits" is zero
// committed and "you were shown none of them" is more than zero committed and
// none disclosed. sdjwt.Verified.RootSigned is where that count comes from, and
// it exists because of this check.
//
// # When the count is unreadable, which is a refusal and not a zero
//
// An Issuer may make the whole constraints claim selectively disclosable rather
// than only its elements — RFC 9901 §4.2.6, which sdjwt.Blinder implements by
// applying paths deepest first, so Blind(claims, "constraints[]",
// "constraints") produces exactly it. The signed payload then carries no
// top-level constraints key at all: there is a digest in _sd, and the array
// with its own element digests lives inside a Disclosure. A Holder that
// discloses that claim and withholds every element inside it reaches the state
// above — nothing to evaluate — while the signed payload commits to nothing
// this can see.
//
// **An earlier version answered zero here and let that through**, on the stated
// reasoning that the decoder beside it refuses a missing claim anyway. The
// reasoning was wrong in a way worth recording rather than quietly fixing: the
// decoder reads the *processed* payload and this reads the *signed* one, and in
// this exact case they disagree — the processed payload has the disclosed array
// and the signed one has nothing. A Credential Provider funded $5,000 against a
// $200 cap through that gap, with err == nil.
//
// So a missing commitment is unanswerable rather than absent, and unanswerable
// refuses. The cost is availability against an Issuer that hides the whole
// claim, which is not a shape this package mints — blindPaths asks for
// "constraints[]" and nothing else — and it is the safe direction.
//
// # What it still does not do
//
// It is a floor and not a ceiling: a mandate narrowed from six constraints to
// one passes here, and only a verifier saying what it needs catches that.
//
// The count is also exact only for an Issuer that does not pad. RFC 9901 §4.2.5
// permits decoy digests in an array of values, and sdjwt.Blinder deliberately
// declines to put them there — but a foreign Issuer may, and its padding
// inflates this number. A mandate that set no limits and carries two decoys
// reads as committing to two and disclosing none, which is a refusal rather
// than a pass: padding costs availability, never safety.
func requireSomeConstraintDisclosed(who string, signed map[string]any, disclosed []generated.Constraint) error {
	raw, present := signed[claimConstraints]
	if !present {
		return fmt.Errorf(
			"%w: %s was shown no signed %s array, so how many constraints this mandate placed cannot be established, and a presentation of none of them cannot be told from a mandate that set none",
			ErrDisclosureInsufficient, who, claimConstraints)
	}
	elements, ok := raw.([]any)
	if !ok {
		// Shape rather than disclosure, so a different sentinel.
		//
		// **This arm cannot fire through either caller, and that is recorded
		// rather than left for somebody to find while hunting untested
		// branches** — the same courtesy Dispute.Verify's own unreachable arms
		// get. To reach it the signed payload would have to hold a non-array
		// under this claim while the processed payload held a usable one, and
		// the two cannot disagree that way: a claim published in the clear is
		// in both, and RFC 9901 §7.1 step 3.c.ii.3 makes a Disclosure that
		// would overwrite one a rejection rather than a precedence rule. So a
		// non-array here is a non-array there, and decodeConstraints has
		// already refused it.
		//
		// It stays because handling it is correct for any caller whose
		// preconditions are weaker, and because the alternative — letting the
		// type assertion answer zero elements — is the fail-open this whole
		// function was rewritten to remove.
		return fmt.Errorf("%w: %s must be an array, got %T",
			ErrMandateMalformed, claimConstraints, raw)
	}

	committed := len(elements)
	if committed == 0 || len(disclosed) > 0 {
		return nil
	}
	return fmt.Errorf(
		"%w: this mandate's %s commits to %d constraints and the presentation shown to %s disclosed none of them, which is not the same thing as a mandate that set no limits",
		ErrDisclosureInsufficient, claimConstraints, committed, who)
}

// requireConstrained refuses a presentation whose disclosed constraints say
// nothing about a fact this verifier will not proceed without.
//
// # Why this is the shape the check has to take
//
// Because the obvious shape does not exist. A verifier cannot detect *which*
// constraint was withheld from it, or what it said: RFC 9901 makes an
// undisclosed digest opaque by design, and the processed payload drops the
// element rather than leaving a hole. Nor would knowing *how many* went missing
// help — "one of six was withheld" says nothing about whether the one mattered,
// so a verifier refusing on any withholding forbids minimisation outright and
// one accepting learns nothing.
//
// (How many is nonetheless computable, and requireSomeConstraintDisclosed above
// uses it. The one thing a count settles is the extreme: none of them.)
//
// So the verifier states its own requirement instead, and the requirement is
// about *facts* rather than about constraints it has never seen. "I will not
// authorise a purchase against a mandate that says nothing about the amount" is
// something a verifier knows without being told, and it is checkable against
// what it was shown. That is the reading of disclosure_insufficient this file
// implements: *a claim this verifier needs was withheld*, where the verifier is
// the one that says what it needs.
//
// # What it does not close
//
// A constraint on a fact nobody required is withheld undetectably, and that is
// a property of selective disclosure rather than of this function. So is the
// weaker point that this checks a fact is *mentioned* and not that it is
// *bounded*: `any[amount lte 200 USD, merchant.id eq "x"]` satisfies a
// requirement for "amount" while placing no effective cap whenever the payee
// matches. Requiring an effective bound would mean deciding what "effective"
// means across every operator and every group shape, which is the verifier's
// policy question and not this table's.
//
// The remedy for both is elsewhere and is worth naming: the agent signs sd_hash
// over the root exactly as presented, so the narrowing it chose is attributable
// to it afterwards, in the evidence a dispute reads.
//
// Every name must be stated, not any of them. A Credential Provider requiring
// both an amount and a payee is not served by a presentation that constrains
// only the amount — that is the case where an any-of reading funds a payment to
// a payee the mandate never pinned.
//
// An empty required list is a verifier that has no such policy. Every caller
// that existed before this check leaves it empty, which is why no chain test
// written for #12 changed.
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
				"%w: this verifier does not authorise unless a constraint names %s, and none of the %d disclosed to it does",
				ErrDisclosureInsufficient, name, len(cs))
		}
	}
	return nil
}

// decodeOpenPresented is the standalone-presentation counterpart of the floor
// the two chain entry points apply, shared by VerifyOpenCheckout and
// VerifyOpenPayment because the question does not differ between them.
//
// The signed claims are read off the presentation rather than taken from the
// caller, and that is sound here for the reason it is sound in VerifyChain and
// not in Minimise: sdjwt.Verify has already checked the signature over them by
// the time this runs. A chain gets the same payload from
// sdjwt.Verified.RootSigned, which is that same read done one layer down.
//
// It is generic over the mandate type so that neither open mandate's decoder
// has to be wrapped in a near-copy of the other's, and so that a third one
// cannot land with the floor silently missing from it.
func decodeOpenPresented[M any](
	sd *sdjwt.SDJWT,
	decode func(map[string]any) (M, error),
	claims map[string]any,
) (M, error) {
	m, err := decode(claims)
	if err != nil {
		return m, err
	}

	signed, err := sd.SignedClaims()
	if err != nil {
		var zero M
		return zero, fmt.Errorf("%w: %w", ErrMandateMalformed, err)
	}
	audience, err := audienceFor(signed)
	if err != nil {
		return m, err
	}
	return m, requireSomeConstraintDisclosed(audience.who, signed, constraintsOf(m))
}

// constraintsOf reads the constraints off either open mandate.
//
// A type switch rather than an interface the generated types would have to
// satisfy: internal/core/generated is regenerated from contracts/ on every
// build, so a method declared on one of its types by hand is deleted rather
// than reviewed.
//
// **A case missing here fails closed, not open**, and that is worth stating
// because the opposite is the natural guess — this comment claimed it until a
// mutation said otherwise. The default returns nil, which reads as "nothing was
// disclosed", so the floor refuses a presentation of that mandate type wherever
// its mandate committed to any constraint. Not *every* presentation: a mandate
// that set no limits commits to none, and the committed == 0 arm short-circuits
// ahead of the disclosed one. So a third open mandate landing without a case
// here stops working loudly for every mandate anybody actually uses, and does
// not quietly lose its floor.
//
// The test that catches it is TestTheFloorCoversBothOpenMandates' positive
// control — the subtest that presents a mandate with its constraints *disclosed*
// and requires it to verify. The refusal subtests do not catch it, and that is
// worth knowing rather than assuming: they present a mandate narrowed to
// nothing and assert a refusal, so a mutant that refuses for the wrong reason
// still satisfies them. This comment named those subtests until a mutation
// showed the payment case could be deleted with both of them green.
func constraintsOf(m any) []generated.Constraint {
	switch v := m.(type) {
	case generated.OpenCheckoutMandate:
		return v.Constraints
	case generated.OpenPaymentMandate:
		return v.Constraints
	default:
		return nil
	}
}
