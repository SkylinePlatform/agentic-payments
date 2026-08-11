package ap2_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// TestEveryFailureHasACode is the test the error-code mapping exists for.
//
// Every rejection this adapter can produce has to be nameable in a receipt,
// because AP2 answers a refusal with one and a receipt naming nothing is not
// evidence of anything. The mapping is a table rather than a switch precisely so
// this test can be written; a switch with a default silently answers "" for a
// sentinel somebody forgot, and "" is not in the enum.
//
// The list below is deliberately spelled out rather than derived from the table
// the implementation uses. A test that read the same table would agree with it
// by construction and prove nothing — including agreeing that a missing entry is
// missing.
func TestEveryFailureHasACode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		{ap2.ErrMisconfigured, generated.ErrorCodeVerifierUnavailable},
		{ap2.ErrMandateMalformed, generated.ErrorCodeMandateMalformed},
		{ap2.ErrUnsupportedVersion, generated.ErrorCodeMandateVersionUnsupported},
		{ap2.ErrWrongMandateType, generated.ErrorCodeMandateVersionUnsupported},
		{ap2.ErrCheckoutHashMismatch, generated.ErrorCodeCheckoutHashMismatch},
		{ap2.ErrPaymentBindingMismatch, generated.ErrorCodePaymentBindingMismatch},
		{ap2.ErrPaymentAmountMismatch, generated.ErrorCodePaymentAmountMismatch},
		{ap2.ErrReceiptMismatch, generated.ErrorCodeMandateMalformed},
		{ap2.ErrCredentialScopeMismatch, generated.ErrorCodeCredentialScopeMismatch},
		{ap2.ErrCredentialExpired, generated.ErrorCodeMandateExpired},
		{ap2.ErrBindingUnverifiable, generated.ErrorCodeDisclosureInsufficient},
		// The second failure carrying disclosure_insufficient, and the pair is
		// not an accident: one is a document withheld from a binding check, the
		// other a limit withheld from a constraint check, and in both the
		// presentation verified perfectly and left the verifier unable to
		// conclude. Two sentinels because the reader has to go to two different
		// places; one code because the counterparty's next move is the same —
		// present more.
		{ap2.ErrDisclosureInsufficient, generated.ErrorCodeDisclosureInsufficient},
		// The domain's, not this adapter's, and in the list because Dispute.Verify
		// hands it back to a caller who has no other way to name it. Left
		// unmapped it would arrive as verifier_unavailable, blaming the arbiter
		// for a gap in what it was handed.
		{evidence.ErrIncomplete, generated.ErrorCodeRequestMalformed},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ap2.CodeOf(tc.err))
			assert.Equal(t, tc.want, ap2.CodeOf(fmt.Errorf("wrapped: %w", tc.err)),
				"every failure reaches a caller wrapped in context, so the wrapped form is the real one")
		})
	}
}

// TestTheSecuringFormatsFailuresAreNameableToo covers the other half. A caller
// verifying an AP2 mandate should not have to know which layer refused it in
// order to answer with a receipt, so pkg/sdjwt's sentinels map here as well.
func TestTheSecuringFormatsFailuresAreNameableToo(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		{sdjwt.ErrSignatureInvalid, generated.ErrorCodeSignatureInvalid},
		{sdjwt.ErrExpired, generated.ErrorCodeMandateExpired},
		{sdjwt.ErrNotYetValid, generated.ErrorCodeMandateNotYetValid},
		{sdjwt.ErrDisclosureUnmatched, generated.ErrorCodeDisclosureUnmatched},
		{sdjwt.ErrKeyBindingRequired, generated.ErrorCodeKeyBindingRequired},
		{sdjwt.ErrKeyBindingInvalid, generated.ErrorCodeKeyBindingInvalid},
		{sdjwt.ErrUnsupportedHashAlg, generated.ErrorCodeAlgorithmUnsupported},
		{sdjwt.ErrMalformedSDJWT, generated.ErrorCodeMandateMalformed},
		// A well-formed token of the wrong shape, not a broken one — see
		// ErrMalformedChain's own comment in pkg/sdjwt. It used to fall through
		// sdjwtCodeOf's default arm and answer verifier_unavailable, blaming this
		// verifier for a presentation that was simply not a chain. #147.
		{sdjwt.ErrMalformedChain, generated.ErrorCodeMandateMalformed},
		// #162: not a broken chain but a well-formed one whose delegate_payload
		// disclosed zero or two elements — draft §6 step 3.2 requires exactly
		// one. Unlike ErrMalformedChain above, this one was live rather than
		// latent: AuthoriseCheckoutChain returns it unchanged, so it used to
		// reach a signed rejection receipt reading verifier_unavailable,
		// telling the counterparty to retry a shape no retry changes.
		{sdjwt.ErrDelegatePayloadInvalid, generated.ErrorCodeMandateMalformed},
		{sdjwt.ErrInvalidOptions, generated.ErrorCodeVerifierUnavailable},
		// The verifier's own bookkeeping, not the mandate's: raised only by
		// Blinder.Blind over a path the issuer built itself. See sdjwtCodeOf's
		// own doc comment and errors_internal_test.go's allowlist for why this
		// is deliberately unmapped to anything naming a fault in a mandate.
		{sdjwt.ErrNoSuchClaim, generated.ErrorCodeVerifierUnavailable},
		{sdjwt.ErrUnexpectedType, generated.ErrorCodeRequestMalformed},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ap2.CodeOf(fmt.Errorf("verifying: %w", tc.err)))
		})
	}
}

// TestTheAuthorisationDomainsVerdictsKeepTheirOwnCodes is the third population,
// and the one whose absence was the defect in #111.
//
// Every error here collapsed to verifier_unavailable before the delegation in
// CodeOf existed — so a merchant refusing a purchase because it broke the
// user's spending limit issued a receipt saying the merchant had a fault of its
// own. The two readings send a dispute to different places, and they tell an
// agent to do opposite things: verifier_unavailable reads as *retry*, where the
// correct behaviour is *come back with a lower price*.
//
// Spelled out rather than derived, for the reason TestEveryFailureHasACode
// gives, and here there is a second one: the wanted codes below are written from
// the three outcomes constraint.CodeOf documents and the verdicts authz.CodeOf
// documents, not from either function. A test that called them would agree with
// them by construction — including agreeing that a sentinel nobody delegated is
// answered by the default arm.
func TestTheAuthorisationDomainsVerdictsKeepTheirOwnCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		// Read, evaluated, and the answer was no. This is the demo's central
		// beat, and the one code an agent acts on.
		{constraint.ErrViolated, generated.ErrorCodeConstraintViolated},
		// The verifier could not form a view at all, because it does not hold
		// the field or the operator. Distinct from the row above precisely so a
		// counterparty is not told the limits were checked when they were not.
		{constraint.ErrUnknownField, generated.ErrorCodeConstraintTypeUnknown},
		{constraint.ErrUnknownOperator, generated.ErrorCodeConstraintTypeUnknown},
		// The constraint could not be read.
		{constraint.ErrTypeMismatch, generated.ErrorCodeMandateMalformed},
		{constraint.ErrMalformed, generated.ErrorCodeMandateMalformed},
		{constraint.ErrTooDeep, generated.ErrorCodeMandateMalformed},
		{constraint.ErrCurrencyMismatch, generated.ErrorCodeMandateMalformed},

		{authz.ErrAgentKeyMismatch, generated.ErrorCodeAgentKeyMismatch},
		{authz.ErrNoEndorsedKey, generated.ErrorCodeAgentKeyMismatch},
		// The open mandate's own life, not the securing format's. sdjwt.ErrExpired
		// carries the same code from the other side, which is correct: whether the
		// envelope or the authorisation ran out, what the holder must do is get a
		// new one.
		{authz.ErrExpired, generated.ErrorCodeMandateExpired},
		{authz.ErrNotYetValid, generated.ErrorCodeMandateNotYetValid},
		// A pinned value is a limit of the strictest kind, so a closed mandate
		// that rewrote one is a violation rather than a malformed document.
		{authz.ErrPinnedFieldChanged, generated.ErrorCodeConstraintViolated},
		{authz.ErrMalformedMandate, generated.ErrorCodeMandateMalformed},
		{authz.ErrOpenMandateOutstanding, generated.ErrorCodeOpenMandateOutstanding},
		{authz.ErrMandateSpent, generated.ErrorCodeOpenMandateOutstanding},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ap2.CodeOf(tc.err))
			assert.Equal(t, tc.want, ap2.CodeOf(fmt.Errorf("authorising: %w", tc.err)),
				"AuthoriseCheckoutChain hands these back wrapped in context, so the wrapped form is the one a receipt is built from")
		})
	}
}

// TestTheMostSpecificVerdictWins pins CodeOf's precedence between populations,
// for the errors that are genuinely in two at once.
//
// Both cases below are built the way production builds them rather than by
// hand: sdjwt.resolveHolderKey wraps whatever the delegate-key resolver returns
// in ErrKeyBindingInvalid, and this package's wrapAgentKey is that resolver.
// TestAnOpenMandateWhoseCnfNamesNoUsableKeyIsRefused drives the whole chain to
// reach the first of them; this states the rule directly, so that the reason
// the answer is agent_key_mismatch is written down somewhere other than in the
// order of three function calls.
//
// Without this test, swapping authzCodeOf and sdjwtCodeOf in CodeOf leaves the
// whole package green while changing the code that reaches a signed receipt.
func TestTheMostSpecificVerdictWins(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want generated.ErrorCode
		also error
	}{
		{
			// The authorisation domain's verdict inside the securing format's.
			// pkg/sdjwt knows a resolver refused; this package knows the cnf
			// endorsed nobody, and that is what a counterparty can act on.
			name: "an unendorsed agent key, refused inside a key binding",
			err: fmt.Errorf("%w: resolve holder key: %w",
				sdjwt.ErrKeyBindingInvalid, authz.ErrAgentKeyMismatch),
			want: generated.ErrorCodeAgentKeyMismatch,
			also: sdjwt.ErrKeyBindingInvalid,
		},
		{
			// This package's own sentinel in the same position, from decodeCnf.
			// codeFor runs first, so this is the arm that settles it — and it
			// has to, because authz does not own ErrMandateMalformed and would
			// pass it through to the securing format's answer.
			name: "a malformed cnf, refused inside a key binding",
			err: fmt.Errorf("%w: resolve holder key: %w",
				sdjwt.ErrKeyBindingInvalid, ap2.ErrMandateMalformed),
			want: generated.ErrorCodeMandateMalformed,
			also: sdjwt.ErrKeyBindingInvalid,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tc.err, tc.also,
				"the premise of this test is dual membership; if it holds only one way there is no precedence to pin")
			assert.Equal(t, tc.want, ap2.CodeOf(tc.err),
				"the innermost layer to form a view has the most specific one, and that is the finding a dispute reads")
		})
	}
}

// TestAnAgentsOwnBookkeepingIsNotACounterpartysProblem covers the two sentinels
// authz.CodeOf answers with the empty code on purpose.
//
// They are the agent refusing itself — a receipt applied where no presentation
// is outstanding, a state the machine does not define — and authz declines to
// name a code because there is no verdict about anybody's artefact to name.
// This function cannot pass that on: "" is not in the enum, and a rejection
// carrying it is the hole CodeOf exists to prevent. So the two rules do not
// contradict each other, they meet here, and what they agree on is
// verifier_unavailable: an error of this kind reaching a receipt at all means a
// verifier's own bookkeeping went wrong, which is a fault of its own and
// nothing to do with the mandate it was shown.
func TestAnAgentsOwnBookkeepingIsNotACounterpartysProblem(t *testing.T) {
	t.Parallel()

	for _, err := range []error{authz.ErrNoPresentationOutstanding, authz.ErrUnknownTransition} {
		t.Run(err.Error(), func(t *testing.T) {
			t.Parallel()

			assert.Empty(t, authz.CodeOf(err),
				"the owning package still declines to name one; this test is worthless if that changes silently")
			assert.Equal(t, generated.ErrorCodeVerifierUnavailable, ap2.CodeOf(err),
				"whatever the domain declines to name, a receipt still has to carry something in the enum")
		})
	}
}

// TestNoFailureIsNameless is the backstop, and the reason it matters is where
// the empty string ends up rather than that it is untidy.
//
// "" is not a member of the ErrorCode enum. A rejection carrying it produces a
// receipt naming no reason and a Problem Details response the transport cannot
// classify, so the counterparty is told only that something went wrong — for a
// refusal this package had a perfectly good reason for. A mapping gap has to
// fail here, in this package's tests, rather than in somebody's dispute.
func TestNoFailureIsNameless(t *testing.T) {
	t.Parallel()

	assert.Equal(t, generated.ErrorCodeVerifierUnavailable,
		ap2.CodeOf(errors.New("something this package has never heard of")),
		"an unmapped failure is still the verifier refusing for its own reasons, which is a nameable thing")

	assert.Empty(t, ap2.CodeOf(nil),
		"success is the one case with no code, because there is no rejection to name")
}
