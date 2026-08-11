package ap2

import (
	"errors"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// is is errors.Is under a shorter name, used only inside this package's own
// mapping tables where the repetition would otherwise be the loudest thing on
// the page.
func is(err, target error) bool { return errors.Is(err, target) }

// The failures this adapter can report. Every one of them maps to a canonical
// error code — see codeFor — because AP2 requires a rejection to be answered
// with a receipt naming why, and a failure with no code cannot be put in one.
var (
	// ErrMisconfigured means this package was not given something it needs to do
	// its job: a nil signer or blinder at issuance, a nil issuer key or clock at
	// verification. It says nothing about the mandate.
	//
	// It is separate from ErrMandateMalformed because the two blame different
	// parties, and the receipt is where that distinction becomes irreversible. A
	// verifier whose operator forgot to wire up a clock has not been shown a bad
	// mandate — it has failed for its own reasons, which is why this maps to
	// verifier_unavailable and not to any code in the "Securing format" block.
	// Answering such a caller with mandate_malformed would send the one party
	// who did nothing wrong away to debug their own request.
	//
	// pkg/sdjwt drew this same line one layer down, in the self-review on #40:
	// a nil Issuer or Clock used to report ErrUnsupportedAlgorithm, dressing a
	// configuration bug as a protocol failure. This is that lesson applied at
	// the adapter boundary, where the error becomes an error code.
	ErrMisconfigured = errors.New("ap2: verifier misconfigured")

	// ErrMandateMalformed means the payload verified but is not shaped like the
	// mandate it claims to be: a required claim missing, or of the wrong JSON
	// type. Distinct from a signature failure, which pkg/sdjwt reports.
	ErrMandateMalformed = errors.New("ap2: mandate malformed")

	// ErrUnsupportedVersion means the vct is not a credential type this
	// verifier implements. The version suffix exists to make this refusable
	// rather than guessable, so it is refused rather than guessed.
	ErrUnsupportedVersion = errors.New("ap2: mandate version unsupported")

	// ErrWrongMandateType means the vct names one of AP2's mandates, but not
	// the one being verified — an open Checkout Mandate presented where a
	// closed one belongs, most usefully. Separated from ErrUnsupportedVersion
	// so the message can say which of the four arrived.
	ErrWrongMandateType = errors.New("ap2: wrong mandate type")

	// ErrCheckoutHashMismatch means the checkout this mandate was verified
	// against does not hash to the checkout_hash claim. The mandate authorises
	// a different purchase from the one being presented.
	ErrCheckoutHashMismatch = errors.New("ap2: checkout hash mismatch")

	// ErrPaymentBindingMismatch means a Checkout Mandate and a Payment Mandate
	// name different checkouts. Authorisation to buy and authorisation to pay
	// were given for two different purchases, which is the pairing the shared
	// checkout_hash exists to make detectable.
	//
	// Separate from ErrCheckoutHashMismatch, which is what a single mandate
	// gets when it disagrees with the document it was checked against. This one
	// says nothing about either mandate on its own — both may be perfectly
	// valid — only that they do not belong together.
	ErrPaymentBindingMismatch = errors.New("ap2: payment and checkout mandates name different checkouts")

	// ErrPaymentAmountMismatch means a Payment Mandate authorises an amount the
	// checkout it is bound to does not cost.
	//
	// This is the one sentinel in this file that no AP2 rule produces. The
	// specification defines transaction_id as the hash of the Checkout JWT and
	// stops there, so the binding proves the two documents name one purchase and
	// proves nothing about the number — see AmountMatches, which is where the
	// divergence is argued, and docs/protocols/ap2.md, which is where it is
	// recorded for a reader who is not in this package.
	//
	// Separate from ErrPaymentBindingMismatch for the same reason that one is
	// separate from ErrCheckoutHashMismatch: the binding may be perfect and the
	// price still wrong, and a receipt that reported the two as one failure
	// would send a reader looking for a substituted purchase when what happened
	// was a substituted price.
	ErrPaymentAmountMismatch = errors.New("ap2: payment amount is not what the checkout costs")

	// ErrCredentialScopeMismatch means the payment credential is good for a
	// different purchase from the one being paid for.
	//
	// Distinct from the two mandate-level mismatches, and the distinction is
	// what a reader of the receipt needs: the mandates may agree with each other
	// perfectly and the money still be wrong. A credential naming no checkout at
	// all lands here too, because a credential scoped to nothing is scoped to
	// everything, which is the failure this claim exists to prevent.
	ErrCredentialScopeMismatch = errors.New("ap2: credential is scoped to another checkout")

	// ErrCredentialExpired means the credential was good for this purchase and
	// is no longer good for anything. Separate from the scope failure so a
	// retryable condition is not reported as a permanent one.
	ErrCredentialExpired = errors.New("ap2: credential has expired")

	// ErrReceiptMismatch means a receipt's reference is not the digest of the
	// mandate it is being checked against. The receipt may be perfectly valid
	// and correctly signed — it simply answers a different presentation.
	//
	// That includes another presentation of the same mandate. The reference is
	// sd_hash, which covers the disclosures actually present, so a receipt
	// issued against a presentation that withheld a claim does not answer the
	// full one. Treating those as interchangeable would let a verifier shown
	// less produce evidence implying it was shown more.
	ErrReceiptMismatch = errors.New("ap2: receipt answers a different mandate")

	// ErrBindingUnverifiable means the binding could be neither confirmed nor
	// refuted: the Checkout JWT was withheld from the presentation and the
	// verifier was given no copy of its own.
	//
	// This is a refusal, not a pass. AP2 makes checkout_jwt selectively
	// disclosable and checkout_hash mandatory, so this state is reachable
	// without anybody misbehaving — a verifier that already holds the checkout
	// legitimately does not need to be sent it again. What is not legitimate is
	// treating a hash nobody can recompute as a binding. The claim on its own
	// says only that whoever signed the mandate wrote a hash into it, which is
	// exactly the assertion the recompute rule exists to distrust.
	ErrBindingUnverifiable = errors.New("ap2: checkout binding cannot be verified")

	// ErrDisclosureInsufficient means the presentation did not disclose a
	// constraint on a fact this verifier requires to be constrained.
	//
	// It is the minimisation counterpart of ErrBindingUnverifiable, which is
	// why both carry disclosure_insufficient: in each case the presentation
	// verified perfectly and left the verifier unable to conclude the thing it
	// exists to conclude. The difference is what was missing — there a document
	// to recompute a hash against, here a limit the user set.
	//
	// Its two producers mean two different things, and an earlier version of
	// this comment denied that the second was possible — "no error here could
	// mean 'a constraint was withheld'", on the grounds that a withheld
	// disclosure and a decoy are indistinguishable. The commit that added
	// requireSomeConstraintDisclosed made it possible and left the sentence
	// standing.
	//
	//   - requireConstrained: a fact this verifier named as required is one no
	//     disclosed constraint mentions. It says nothing about what was
	//     withheld, because nothing can.
	//   - requireSomeConstraintDisclosed: the signed payload commits to
	//     constraints and this presentation disclosed none of them — which *is*
	//     "constraints were withheld", counted rather than guessed. Or the
	//     commitment could not be read at all, which is unanswerable and
	//     refuses on the same terms.
	//
	// What stays true is the narrower claim: *which* constraint went, and what
	// it said, is unrecoverable. The count is not.
	ErrDisclosureInsufficient = errors.New("ap2: a constraint this verifier requires was not disclosed")
)

// adapterCodes is the canonical code each of this package's failures carries
// into a rejection receipt.
//
// A table rather than a switch, and directly below the sentinels rather than in
// another file, because the property worth having is that a failure declared
// with no code is visible at a glance. A switch in a second file was what this
// comment used to claim and did not deliver.
//
// Order matters only in that the first match wins, and no error here wraps
// another, so it does not.
var adapterCodes = []struct {
	err  error
	code generated.ErrorCode
}{
	{ErrMisconfigured, generated.ErrorCodeVerifierUnavailable},
	{ErrMandateMalformed, generated.ErrorCodeMandateMalformed},
	{ErrUnsupportedVersion, generated.ErrorCodeMandateVersionUnsupported},
	{ErrWrongMandateType, generated.ErrorCodeMandateVersionUnsupported},
	{ErrCheckoutHashMismatch, generated.ErrorCodeCheckoutHashMismatch},
	{ErrPaymentBindingMismatch, generated.ErrorCodePaymentBindingMismatch},
	{ErrPaymentAmountMismatch, generated.ErrorCodePaymentAmountMismatch},
	{ErrReceiptMismatch, generated.ErrorCodeMandateMalformed},
	{ErrCredentialScopeMismatch, generated.ErrorCodeCredentialScopeMismatch},
	{ErrCredentialExpired, generated.ErrorCodeMandateExpired},
	{ErrBindingUnverifiable, generated.ErrorCodeDisclosureInsufficient},
	{ErrDisclosureInsufficient, generated.ErrorCodeDisclosureInsufficient},
	// evidence.ErrIncomplete is the domain's, not this package's, and it is in
	// this table because Dispute.Verify is the thing that reports it — a
	// dispute bundle missing an artefact is the caller having assembled the
	// call wrong, which is what request_malformed says, and CodeOf has to stay
	// total over everything a caller can get back from here. Left out, it would
	// fall through to verifier_unavailable and blame the arbiter for a gap in
	// what it was handed.
	{evidence.ErrIncomplete, generated.ErrorCodeRequestMalformed},
}

// codeFor maps this package's own failures.
func codeFor(err error) generated.ErrorCode {
	for _, entry := range adapterCodes {
		if is(err, entry.err) {
			return entry.code
		}
	}
	return ""
}

// authzCodeOf maps the authorisation domain's verdicts — the answers to "was
// this agent allowed to make this purchase, within these limits, right now".
//
// Membership is asked of authz rather than listed here, and that is the whole
// design of this function. A list in this file would be a copy of a set that
// lives in another package across four files, free to drift, and silent when it
// does: an authz sentinel nobody added here would answer verifier_unavailable,
// which is #111 all over again in the code that fixed it. adapterCodes above
// can be a list precisely because it sits directly below the sentinels it maps,
// where a gap is visible at a glance. This one cannot have that property, so it
// does not try to.
//
// Two steps rather than one, because authz.CodeOf is total: its default arm
// reaches constraint.CodeOf, whose own default is mandate_malformed, so calling
// it unguarded would answer mandate_malformed for every failure this package
// has never heard of — a verifier telling a counterparty their mandate is bad
// when the truth is that the verifier failed for a reason of its own it cannot
// name. TestNoFailureIsNameless is what fails on that.
//
// An owned error whose code is the empty string keeps that emptiness, which
// CodeOf then treats as "no answer from this population" — see authz.Owns on
// why those two lifecycle sentinels are members with no code, and CodeOf below
// on where they land instead.
//
// # How these errors get here
//
// Not by one route. AuthoriseCheckoutChain and AuthorisePaymentChain return
// authz.AuthoriseCheckout's and authz.AuthorisePayment's errors unchanged, which
// is the obvious one and is right — a verdict about the user's limits is not
// something an adapter should restate in its own words. But this package raises
// them directly too: wrapAgentKey returns authz.ErrAgentKeyMismatch for a cnf
// naming no usable material, decodeConstraints returns
// constraint.ErrUnknownField for an unrecognised constraint type, and
// requireConstrained returns whatever constraint.Parse gave it. Believing the
// single-route story is what hid the dual membership CodeOf's precedence rule
// now settles.
func authzCodeOf(err error) generated.ErrorCode {
	if !authz.Owns(err) {
		return ""
	}
	return authz.CodeOf(err)
}

// CodeOf maps a verification failure to the canonical error code a rejection
// receipt and an RFC 9457 response carry.
//
// Failures raised by pkg/sdjwt travel through here too, and so do the
// authorisation domain's own verdicts, because a caller verifying an AP2
// mandate should not have to know which layer refused it in order to answer
// with a receipt.
//
// Three populations, and only one of the three has its codes written down in
// this file. This package's own sentinels are mapped by adapterCodes here; the
// authorisation domain's are answered by the package that declared them,
// through authzCodeOf; the securing format's are mapped by sdjwtCodeOf, which
// is this package's reading of pkg/sdjwt rather than pkg/sdjwt's own, because a
// library implementing a public standard has no business knowing AP2's error
// vocabulary.
//
// # The order is load-bearing: the most specific verdict wins
//
// An error can belong to two populations at once, so "first non-empty answer
// wins" is a real precedence rule and not an incidental one. The route that
// produces it is ordinary rather than exotic: wrapAgentKey hands
// authz.ErrAgentKeyMismatch to sdjwt.VerifyChain's delegate-key resolver, and
// resolveHolderKey wraps whatever a resolver returns in ErrKeyBindingInvalid.
// The result satisfies errors.Is for both, and the two populations have
// different answers for it:
//
//	agent_key_mismatch    — the cnf endorsed nobody
//	key_binding_invalid   — a key binding did not check out
//
// agent_key_mismatch is the one to carry, and the general rule it is an
// instance of is that the innermost layer to form a view has the most specific
// one. pkg/sdjwt knows a resolver refused; this package knows *why*, because it
// is the layer that refused. So authzCodeOf precedes sdjwtCodeOf, and codeFor
// precedes both — a sentinel this package declares is the most specific thing
// there is about a failure it raised itself, which is what settles
// ErrMandateMalformed arriving from decodeCnf wrapped in ErrKeyBindingInvalid
// the same way.
//
// TestTheMostSpecificVerdictWins pins both. Swapping the two calls below leaves
// every other test in this package green.
//
// A non-nil error never yields the empty string. Empty is not a member of the
// ErrorCode enum, so it is not a code a receipt or a Problem Details response
// can carry — it is a hole that reaches the transport and becomes a 500 naming
// nothing. An unmapped failure is therefore reported as verifier_unavailable,
// which is both a valid code and the true one: the verifier refused for a reason
// of its own that it cannot name. The mapping gap is still a bug, and
// TestEveryFailureHasACode is what fails on it — but it fails in this package's
// tests rather than in a counterparty's dispute.
func CodeOf(err error) generated.ErrorCode {
	if err == nil {
		return ""
	}
	if code := codeFor(err); code != "" {
		return code
	}
	if code := authzCodeOf(err); code != "" {
		return code
	}
	if code := sdjwtCodeOf(err); code != "" {
		return code
	}
	return generated.ErrorCodeVerifierUnavailable
}

// sdjwtCodeOf maps the securing format's failures. The codes are mostly the
// "Securing format" block of contracts/evidence/error_code.json, which was
// written from the protocol documentation rather than from this package — so a
// gap here is a gap in the mapping, not in the vocabulary.
//
// ErrInvalidOptions and ErrNoSuchClaim are the two that leave that block, and
// both are enforced rather than merely commented. TestEverySDJWTSentinelIsMappedOrAllowlisted
// in errors_internal_test.go reads every exported var pkg/sdjwt declares and
// partitions the answers this function may give for one: any code but
// verifier_unavailable, or verifier_unavailable and a reason in that test's own
// allowlist saying why the verifier is confessing about itself. Both halves are
// checked, so neither a sentinel with no arm nor an arm quietly returning
// verifier_unavailable can pass. #147 and #162 were both found by a person
// reading this switch by eye instead — an unmapped sentinel falls through to
// the empty code, which is not in the enum, so a missing arm was invisible
// until that test existed.
//
// ErrInvalidOptions is for the reason ErrMisconfigured gives: pkg/sdjwt raises
// it when Verify is handed a policy it cannot apply, which is the calling
// verifier's fault and not the mandate's. VerifyCheckout guards the two cases
// it could reach today, so this arm is currently unreachable through it — but
// the day a second caller appears the failure is already nameable.
//
// ErrNoSuchClaim is raised only by Blinder.Blind, whose production callers are
// all issuance-side: the surface builds the disclosure paths itself, so a path
// naming nothing is this verifier's own bookkeeping error and not a claim about
// a counterparty's mandate — the same reasoning as ErrInvalidOptions, over a
// different mistake.
func sdjwtCodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case is(err, sdjwt.ErrInvalidOptions), is(err, sdjwt.ErrNoSuchClaim):
		return generated.ErrorCodeVerifierUnavailable
	case is(err, sdjwt.ErrUnexpectedType):
		// request_malformed rather than mandate_malformed, and the distinction
		// is the point of having both. A token whose typ names another artefact
		// parses perfectly and may be correctly signed — nothing about the
		// securing format failed. What went wrong is that this was sent where
		// something else was expected, which is the caller getting the call
		// wrong.
		return generated.ErrorCodeRequestMalformed
	case is(err, sdjwt.ErrSignatureInvalid):
		return generated.ErrorCodeSignatureInvalid
	case is(err, sdjwt.ErrExpired):
		return generated.ErrorCodeMandateExpired
	case is(err, sdjwt.ErrNotYetValid):
		return generated.ErrorCodeMandateNotYetValid
	case is(err, sdjwt.ErrDisclosureUnmatched), is(err, sdjwt.ErrDigestRepeated),
		is(err, sdjwt.ErrDisclosureUnreachable), is(err, sdjwt.ErrClaimConflict):
		return generated.ErrorCodeDisclosureUnmatched
	case is(err, sdjwt.ErrKeyBindingRequired):
		return generated.ErrorCodeKeyBindingRequired
	case is(err, sdjwt.ErrKeyBindingInvalid), is(err, sdjwt.ErrUnexpectedKeyBinding):
		return generated.ErrorCodeKeyBindingInvalid
	case is(err, sdjwt.ErrUnsupportedHashAlg), is(err, sdjwt.ErrUnsupportedAlgorithm):
		return generated.ErrorCodeAlgorithmUnsupported
	case is(err, sdjwt.ErrMalformedSDJWT), is(err, sdjwt.ErrMalformedDisclosure),
		is(err, sdjwt.ErrReservedClaim), is(err, sdjwt.ErrMalformedChain):
		// mandate_malformed, not request_malformed, and the arm above is what
		// settles it read the other way round: ErrUnexpectedType earns
		// request_malformed because it fires only after a successful parse, on a
		// typ read out of a structurally sound token. Every ErrMalformedChain
		// site, by contrast, is the parse itself refusing — which is exactly
		// what error_code.json's "Securing format" block means by
		// mandate_malformed: "not parseable as the securing format requires".
		//
		// The three production ParseChain callers already answer
		// mandate_malformed by hand — merchant/service.go (twice),
		// credprovider/service.go, mpp/service.go — so any other code here would
		// put CodeOf in documented disagreement with the roles about the same
		// error value. #147: this arm was missing, so a malformed chain fell
		// through to the default instead and answered verifier_unavailable —
		// blaming this verifier for a shape only the presenter controlled.
		return generated.ErrorCodeMandateMalformed
	case is(err, sdjwt.ErrDelegatePayloadInvalid):
		// mandate_malformed, on the same reasoning as the arm above: a
		// delegate_payload that is not exactly one disclosed object is not the
		// shape draft section 6 step 3.2 requires, which is "not parseable as
		// the securing format requires" just as much as a chain that never
		// parsed at all.
		//
		// #162: unlike #147's ErrMalformedChain, this one was live rather than
		// latent. sdjwt.VerifyChain returns it directly (pkg/sdjwt/chain.go);
		// verifyDelegationChain and AuthoriseCheckoutChain in this package's
		// chain.go return it unchanged; and every role that calls
		// AuthoriseCheckoutChain hands the result to CodeOf and then to
		// IssueReceipt, which stamps the code into a signed receipt. So a
		// presentation this verifier refused for a reason entirely the
		// presenter's doing came back as a signed statement that *this
		// verifier* could not reach a conclusion. That travels further than
		// #147 ever could: #147's wrong code could only ever have appeared in a
		// Problem Details response, and this one is evidence in a dispute.
		//
		// # One sentinel, two conditions, and why they share a code
		//
		// The obvious argument for mandate_malformed — that no retry can change
		// the presentation, so verifier_unavailable's implicit "try again" is a
		// lie — is only true of one of the two conditions this sentinel covers,
		// and it is worth being exact about which.
		//
		//	two or more elements  the Verifier would have to choose which
		//	                      authorisation it was shown, and one that picks
		//	                      is one an attacker can steer. Nothing about it
		//	                      is retryable and nothing about it is innocent.
		//	zero elements         the delegate withheld the very content it is
		//	                      delegating. A retry that disclosed it *would*
		//	                      succeed, which reads a lot like
		//	                      disclosure_insufficient — "a claim this
		//	                      verifier needs was withheld" — the code
		//	                      ErrBindingUnverifiable already carries for the
		//	                      withheld Checkout JWT.
		//
		// They are told apart in pkg/sdjwt's message and nowhere in its error
		// vocabulary: one sentinel covers both, so this function cannot answer
		// differently without matching on wrapped text, which is not a contract.
		// Splitting it is a change to a package implementing a public standard,
		// where the draft's own step 3.2 states the two as one rule — a decision
		// for whoever needs it, not a side effect of an error-code fix.
		//
		// Given one code for both, mandate_malformed is the one to fail closed
		// on. disclosure_insufficient names something the presenter can fix by
		// showing more, and saying that to the multi-element case would invite
		// the steerable presenter to keep trying; mandate_malformed said to the
		// zero-element case is merely blunter than it could be. Both readings
		// blame the presenter, which is the correction #162 is actually about,
		// and both are vectored in golden_rejection_test.go so a future split
		// cannot re-map one half and leave the other behind unnoticed.
		return generated.ErrorCodeMandateMalformed
	default:
		return ""
	}
}
