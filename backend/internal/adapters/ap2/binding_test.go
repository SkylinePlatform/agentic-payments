package ap2_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// withhold returns the presentation with the Checkout JWT dropped, which is
// what a Shopping Agent sends to a verifier already holding the checkout.
func withhold(t *testing.T, sd *sdjwt.SDJWT) *sdjwt.SDJWT {
	t.Helper()

	presented, err := sd.Present(func(d sdjwt.Disclosure) bool {
		name, _ := d.Name()
		return name != "checkout_jwt"
	})
	require.NoError(t, err, "withholding the checkout")
	return presented
}

// TestTheCheckoutIsWithholdable is the privacy property, and it is asserted
// here rather than assumed because nothing else would notice it breaking.
//
// contracts/authz/checkout_mandate.json marks `checkout` withholdable, and the
// adapter has to translate that canonical name into the wire name before the
// Blinder sees it. If that translation were dropped, the Blinder would be asked
// to hide a claim that does not exist, hide nothing, and issue a mandate with
// the merchant's document permanently visible — while every happy-path test
// went on passing.
func TestTheCheckoutIsWithholdable(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := issue(t, f, mandate())

	names := make([]string, 0, len(sd.Disclosures()))
	for _, d := range sd.Disclosures() {
		if name, ok := d.Name(); ok {
			names = append(names, name)
		}
	}
	assert.Contains(t, names, "checkout_jwt",
		"the merchant's document must be a disclosure, not a plain claim, or it can never be withheld")

	// And withholding it must leave a mandate that still parses and verifies.
	_, err := ap2.VerifyCheckout(reparse(t, withhold(t, sd)), ap2.CheckoutOptions{
		Issuer: f.verifier, Clock: f.clock, Checkout: merchantCheckout,
	})
	require.NoError(t, err, "a verifier holding the checkout must accept a presentation without it")
}

// TestAWithheldCheckoutWithNoCopyIsRefused is the decision this issue turned
// on. The claim is present, the signature is good, and the binding still cannot
// be established — so the mandate does not pass.
//
// A hash nobody can recompute asserts only that whoever signed the mandate
// wrote a hash into it, which is exactly what "recompute, never trust" exists
// to distrust. The refusal is disclosure_insufficient rather than a hash
// mismatch, because nothing mismatched: a claim this verifier needs was
// withheld.
func TestAWithheldCheckoutWithNoCopyIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := withhold(t, issue(t, f, mandate()))

	_, err := ap2.VerifyCheckout(reparse(t, sd), f.options())

	require.ErrorIs(t, err, ap2.ErrBindingUnverifiable)
	assert.Equal(t, generated.ErrorCodeDisclosureInsufficient, ap2.CodeOf(err),
		"the receipt must say a needed claim was withheld, not that the hash was wrong")
}

// TestADisclosedAndHeldCheckoutMustAgree closes the gap between the two
// sources. A verifier that silently preferred one would decide, without saying
// so, whether it was checking the mandate it was shown or the one it expected —
// and an agent that could rely on the presented copy being ignored could
// present anything.
func TestADisclosedAndHeldCheckoutMustAgree(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := issue(t, f, mandate())

	_, err := ap2.VerifyCheckout(reparse(t, sd), ap2.CheckoutOptions{
		Issuer:   f.verifier,
		Clock:    f.clock,
		Checkout: merchantCheckout + "-different",
	})
	require.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
		"two different documents must never both be accepted as the bound checkout")
}

// TestTheBindingTracksTheSDJWTHashAlgorithm is the trap AP2 sets by defining
// checkout_hash against _sd_alg rather than against a fixed algorithm.
//
// A verifier that hardcoded sha-256 would pass every mandate issued with the
// default and fail every sha-384 one — reported as a hash mismatch, which reads
// as tampering rather than as the bug it is.
func TestTheBindingTracksTheSDJWTHashAlgorithm(t *testing.T) {
	t.Parallel()

	for _, alg := range []sdjwt.HashAlg{sdjwt.SHA256, sdjwt.SHA384, sdjwt.SHA512} {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, sdjwt.WithHashAlg(alg))
			got, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())), f.options())
			require.NoError(t, err, "a mandate issued with %s was refused", alg)

			want, err := alg.Digest(merchantCheckout)
			require.NoError(t, err)
			assert.Equal(t, want, got.CheckoutHash,
				"checkout_hash must be the digest under the algorithm _sd_alg names")
		})
	}
}

// TestIssuanceRefusesAMandateWithNothingToBind covers the one thing issuance
// cannot do without: there is no correct binding to be minted for a checkout
// that was not supplied.
func TestIssuanceRefusesAMandateWithNothingToBind(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for _, tc := range []struct {
		name string
		m    generated.CheckoutMandate
	}{
		{"no checkout at all", generated.CheckoutMandate{}},
		{"an empty checkout", func() generated.CheckoutMandate {
			empty := ""
			return generated.CheckoutMandate{Checkout: &empty}
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := ap2.IssueCheckout(t.Context(), f.signer, tc.m, f.blinder)
			require.ErrorIs(t, err, ap2.ErrMandateMalformed)
		})
	}
}

// TestACallerSuppliedHashIsIgnored: the mandate binds what was actually
// supplied, never what the caller claimed about it. Accepting a caller's hash
// would put the one value this mandate exists to establish under the control of
// whoever is least placed to be trusted with it.
func TestACallerSuppliedHashIsIgnored(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	m := mandate()
	m.CheckoutHash = "not-the-hash-of-anything"

	got, err := ap2.VerifyCheckout(reparse(t, issue(t, f, m)), f.options())
	require.NoError(t, err)
	assert.NotEqual(t, "not-the-hash-of-anything", got.CheckoutHash,
		"issuance must recompute the binding rather than copy what it was handed")
}

// PaysFor and the guards under it, tested in this package rather than only
// through the merchant's HTTP handler.
//
// make vectors scopes the conformance suite to internal/adapters/... and pkg/...,
// so an exported adapter method whose only exercise is a role's integration test
// is outside the suite that is supposed to cover it. The guards below were
// untested inside Covers before PaysFor existed; extracting recomputable made
// one untested helper serve two methods, which is the moment to close it.

// TestPaysForRefusesAnotherCheckout is the binding the Payment Mandate exists to
// carry, as its own unit rather than as a purchase.
//
// The refusal is payment_binding_mismatch and not checkout_hash_mismatch, and
// the distinction is the entire reason this method is separate from Covers: by
// the time a caller reaches it the Checkout Mandate has already been verified
// against this same document, so the disagreement is between two mandates
// rather than between one mandate and the document.
func TestPaysForRefusesAnotherCheckout(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))
	m, err := ap2.VerifyCheckout(sd, f.options())
	require.NoError(t, err)

	b, err := ap2.BindingOf(sd, m.CheckoutHash)
	require.NoError(t, err)

	assert.NoError(t, b.PaysFor(merchantCheckout),
		"the document this mandate was issued over has to be the one it pays for")

	err = b.PaysFor(merchantCheckout + "-different")
	require.ErrorIs(t, err, ap2.ErrPaymentBindingMismatch)
	assert.NotErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
		"reporting the checkout's own code here would send a reader to the mandate that was fine")
	assert.Equal(t, generated.ErrorCodePaymentBindingMismatch, ap2.CodeOf(err))
}

// TestABindingWithNothingToCompareIsRefused covers the two states in which
// neither method is answering a question about a mandate at all.
//
// Both matter for the same reason: the alternative to refusing is returning nil,
// and a nil from a binding check reads as "the binding held". A zero Binding
// comes from a caller that never read one out of a mandate, and an empty
// checkout from one that has no document to recompute against — neither is a
// mandate that passed.
func TestABindingWithNothingToCompareIsRefused(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := reparse(t, issue(t, f, mandate()))
	m, err := ap2.VerifyCheckout(sd, f.options())
	require.NoError(t, err)
	held, err := ap2.BindingOf(sd, m.CheckoutHash)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		call func(ap2.Binding) error
		want error
		code generated.ErrorCode
	}{
		{
			name: "a Binding nobody read out of a mandate",
			call: func(ap2.Binding) error { return ap2.Binding{}.PaysFor(merchantCheckout) },
			want: ap2.ErrMisconfigured,
			code: generated.ErrorCodeVerifierUnavailable,
		},
		{
			name: "no checkout to recompute against",
			call: func(b ap2.Binding) error { return b.PaysFor("") },
			want: ap2.ErrBindingUnverifiable,
			code: generated.ErrorCodeDisclosureInsufficient,
		},
		{
			name: "the same two through Covers, which shares the guards",
			call: func(ap2.Binding) error { return ap2.Binding{}.Covers(merchantCheckout) },
			want: ap2.ErrMisconfigured,
			code: generated.ErrorCodeVerifierUnavailable,
		},
		{
			name: "Covers with no checkout",
			call: func(b ap2.Binding) error { return b.Covers("") },
			want: ap2.ErrBindingUnverifiable,
			code: generated.ErrorCodeDisclosureInsufficient,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call(held)
			require.ErrorIs(t, err, tc.want,
				"returning nil here would be a binding check reporting that the binding held")
			assert.Equal(t, tc.code, ap2.CodeOf(err))
		})
	}
}
