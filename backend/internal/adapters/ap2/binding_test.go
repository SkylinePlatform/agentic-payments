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
