package ap2_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The built scenario's checkout: the merchant-signed JWT the mandate authorises
// the purchase of. Its contents are opaque to this package on purpose — the
// binding is a digest, and nothing here reads inside the merchant's document.
const merchantCheckout = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1QTUkiLCJhbW91bnQiOjE4OTAwfQ.c2ln"

var (
	// base is the built scenario's clock. Signature expiry has to be driven,
	// not waited for — AGENTS.md hard rule 5.
	base    = time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	issued  = base
	expires = base.Add(48 * time.Hour)
)

const testSlot = crypto.Slot("ap2-checkout")

// fixture is everything a test needs to issue and verify one mandate.
type fixture struct {
	signer   authz.Signer
	verifier authz.Verifier
	clock    *clock.Fake
	blinder  *sdjwt.Blinder
}

// newFixture builds a store with one ES256 key and a deterministic blinder.
//
// The key signs the Checkout Mandate, not the checkout inside it, and the two
// are worth keeping apart because the rule that is easy to misremember belongs
// to the other one. AP2 requires the merchant's *Checkout JWT* to carry a
// non-deterministic signature, so that checkout_hash — which the mandate
// publishes in the clear even when checkout_jwt is withheld — is not open to a
// rainbow table over plausible checkouts. Nothing in the specification
// constrains what algorithm signs the mandate envelope, and IssueCheckout
// accordingly accepts any authz.Signer.
//
// So this fixture does not exercise that rule at all: merchantCheckout is a
// fixed constant and is never signed by this key. ES256 is here because it is
// what the rest of the project mints, not because a test signing the mandate
// with EdDSA would be violating anything.
//
// **One fixture per parallel subtest.** The Blinder draws its salts from a
// single strings.Reader, so two parallel subtests issuing through one fixture
// read and advance the same reader at once — a data race, and one that surfaces
// as an unrelated test failing rather than as anything pointing here. Build a
// fixture inside each t.Parallel() body rather than sharing one above them.
func newFixture(t *testing.T, opts ...sdjwt.BlinderOption) fixture {
	t.Helper()

	c := clock.NewFake(base)
	store, err := crypto.NewStore(c)
	require.NoError(t, err, "standing up the key store")
	ref, err := store.Generate(testSlot, authz.ES256, "test-generate")
	require.NoError(t, err, "generating the issuer key")

	signer, err := store.Signer(testSlot)
	require.NoError(t, err, "obtaining a signer")
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err, "resolving the verifier")

	opts = append([]sdjwt.BlinderOption{sdjwt.WithSaltSource(newSalts())}, opts...)
	blinder, err := sdjwt.NewBlinder(opts...)
	require.NoError(t, err, "building the blinder")

	return fixture{signer: signer, verifier: verifier, clock: c, blinder: blinder}
}

func (f fixture) options() ap2.CheckoutOptions {
	return ap2.CheckoutOptions{Issuer: f.verifier, Clock: f.clock}
}

// newSalts returns a deterministic salt source, so that a golden vector is
// reproducible. Test-only: real issuance takes randomness from the platform.
func newSalts() *strings.Reader {
	return strings.NewReader(strings.Repeat("0123456789abcdef", 64))
}

func mandate() generated.CheckoutMandate { return checkoutFor(merchantCheckout) }

// checkoutFor builds a Checkout Mandate for a named document, so that a test
// can put two different purchases in play at once.
func checkoutFor(checkout string) generated.CheckoutMandate {
	return generated.CheckoutMandate{
		Checkout:  &checkout,
		IssuedAt:  &issued,
		ExpiresAt: &expires,
	}
}

// issue is the happy path, used by the tests that are about something else.
func issue(t *testing.T, f fixture, m generated.CheckoutMandate) *sdjwt.SDJWT {
	t.Helper()

	sd, err := ap2.IssueCheckout(t.Context(), f.signer, m, f.blinder)
	require.NoError(t, err, "issuing a well-formed mandate")
	return sd
}

// reparse sends the mandate through its wire form, so that every test verifies
// what a verifier would actually receive rather than the object in memory.
func reparse(t *testing.T, sd *sdjwt.SDJWT) *sdjwt.SDJWT {
	t.Helper()

	got, err := sdjwt.Parse(sd.String())
	require.NoError(t, err, "reparsing the compact serialisation")
	return got
}

func TestARoundTripCarriesTheCheckoutAndItsBinding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	got, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())), f.options())

	require.NoError(t, err, "a faithfully issued mandate was refused")
	require.NotNil(t, got.Checkout)
	assert.Equal(t, merchantCheckout, *got.Checkout,
		"the verifier must recover the exact document the hash was taken over")
	assert.NotEmpty(t, got.CheckoutHash)
	require.NotNil(t, got.ExpiresAt)
	assert.True(t, expires.Equal(*got.ExpiresAt),
		"exp is the one claim that decides whether a mandate is still live")
}

// TestATamperedCheckoutIsDetected is the rule issue #5 states outright:
// recompute the hash, never trust the claim. An agent that swaps the checkout
// after the user signed has not exceeded a limit — it has changed what was
// authorised.
//
// The presentation withholds the checkout deliberately. With the document
// disclosed, a verifier can refuse this by noticing that the two copies
// disagree — which is a real check, and not this one. Withholding it removes
// that shortcut and leaves the recomputed hash as the only thing standing
// between the mandate and acceptance.
func TestATamperedCheckoutIsDetected(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	sd := withhold(t, issue(t, f, mandate()))

	_, err := ap2.VerifyCheckout(reparse(t, sd), ap2.CheckoutOptions{
		Issuer:   f.verifier,
		Clock:    f.clock,
		Checkout: "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1DREciLCJhbW91bnQiOjk5OTk5fQ.c2ln",
	})

	require.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch)
	assert.Equal(t, generated.ErrorCodeCheckoutHashMismatch, ap2.CodeOf(err),
		"the rejection receipt has to name a reason the reader can act on")
}

// TestAMandateMayNotVouchForItsOwnBinding is the attack the recompute rule
// exists for, and the mutation this package most needs to fail.
//
// The mandate is internally consistent by every other measure: the signature is
// good, the disclosure matches its digest, the vct is right. Only the
// checkout_hash claim is a lie — it is the digest of a cheap checkout, while
// the document disclosed alongside it is an expensive one. An issuer can
// produce this; only recomputing catches it.
//
// A verifier that compares the claim to itself passes this, and passes every
// honest mandate too, so no happy-path test can tell the difference.
func TestAMandateMayNotVouchForItsOwnBinding(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	const expensive = "eyJhbGciOiJFUzI1NiJ9.eyJyb3V0ZSI6IkJFRy1DREciLCJhbW91bnQiOjk5OTk5fQ.c2ln"
	honestHash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing the hash of the cheap checkout")

	sd := issueClaims(t, f, map[string]any{
		"vct":           ap2.VCTCheckoutClosed,
		"checkout_jwt":  expensive,  // what the merchant is shown
		"checkout_hash": honestHash, // what the user authorised
	}, "checkout_jwt")

	_, err = ap2.VerifyCheckout(reparse(t, sd), f.options())
	require.ErrorIs(t, err, ap2.ErrCheckoutHashMismatch,
		"a checkout_hash that does not describe the checkout beside it must be refused")
}

func TestAWrongVCTIsRejected(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		vct  any
		want error
	}{
		{"an open Checkout Mandate where a closed one belongs",
			ap2.VCTCheckoutOpen, ap2.ErrWrongMandateType},
		{"a Payment Mandate", ap2.VCTPaymentClosed, ap2.ErrWrongMandateType},
		{"a later schema version of the same credential type",
			"mandate.checkout.2", ap2.ErrUnsupportedVersion},
		{"the suffix dropped", "mandate.checkout", ap2.ErrUnsupportedVersion},
		{"not a string at all", 1, ap2.ErrMandateMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			sd := issueWithVCT(t, f, tc.vct)

			_, err := ap2.VerifyCheckout(reparse(t, sd), f.options())
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestTheVersionSuffixIsMatchedExactly is the trap AGENTS.md names: the vct
// carries a schema version and an implementation must match the whole string.
// A verifier that matched a prefix would accept "mandate.checkout.2".
func TestTheVersionSuffixIsMatchedExactly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	_, err := ap2.VerifyCheckout(reparse(t, issueWithVCT(t, f, "mandate.checkout.2")), f.options())

	require.ErrorIs(t, err, ap2.ErrUnsupportedVersion)
	assert.Equal(t, generated.ErrorCodeMandateVersionUnsupported, ap2.CodeOf(err),
		"a version this verifier does not implement is a different refusal from a malformed one")
}

// TestAMisconfiguredCallerIsNotTheMandatesFault covers the five ways this
// package can be handed nothing to work with, and asserts that none of them is
// reported as a problem with the mandate.
//
// The distinction only becomes visible at the receipt, and there it is
// irreversible. A verifier stood up without a clock, or an issuer that dropped
// its signer, has not been shown a bad mandate — answering the counterparty
// with mandate_malformed would send the one party who did nothing wrong away to
// debug a request that was fine. verifier_unavailable says whose problem it is.
//
// The missing signer is also the case that used to panic rather than return:
// joseSigner wrapped the interface before sdjwt.Issue could check it, so a nil
// arrived inside a non-nil struct, slipped past pkg/sdjwt's own guard, and
// surfaced as a nil dereference inside Algorithm(). The guards below are what
// caught it at the time; TestTheBridgesPreserveNil is what stops the wrapper
// creating it in the first place.
func TestAMisconfiguredCallerIsNotTheMandatesFault(t *testing.T) {
	t.Parallel()

	t.Run("issuing with no signer", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.IssueCheckout(t.Context(), nil, mandate(), f.blinder)
		assertMisconfigured(t, err)
	})

	t.Run("issuing with no blinder", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.IssueCheckout(t.Context(), f.signer, mandate(), nil)
		assertMisconfigured(t, err)
	})

	t.Run("verifying with no issuer key", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())),
			ap2.CheckoutOptions{Clock: f.clock})
		assertMisconfigured(t, err)
	})

	t.Run("verifying with no clock", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())),
			ap2.CheckoutOptions{Issuer: f.verifier})
		assertMisconfigured(t, err)
	})

	t.Run("verifying nothing at all", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		_, err := ap2.VerifyCheckout(nil, f.options())
		assertMisconfigured(t, err)
	})
}

// assertMisconfigured checks that a failure blames the caller and not the
// mandate. Both halves matter and they are read by different audiences: the
// sentinel is what a Go caller branches on, the code is what a counterparty
// finds in the receipt.
//
// assert rather than require throughout, per AGENTS.md — a shared assertion
// helper must stay safe if a later caller reaches it from a goroutine.
func assertMisconfigured(t *testing.T, err error) {
	t.Helper()

	assert.ErrorIs(t, err, ap2.ErrMisconfigured)
	assert.NotErrorIs(t, err, ap2.ErrMandateMalformed,
		"blaming the mandate here would point a dispute at the wrong party")
	assert.Equal(t, generated.ErrorCodeVerifierUnavailable, ap2.CodeOf(err),
		"the receipt has to say the failure was not the counterparty's")
}

// TestTheBridgesPreserveNil moves the invariant off the entry-point guards and
// onto the wrapper.
//
// Wrapping an interface in a struct is what makes a nil arrive non-nil, and the
// guards in this package were the only thing standing between that and a panic
// several frames inside pkg/sdjwt — which meant every new entry point had to
// remember one, with no help from the compiler. So the constructors return a nil
// interface for a nil inner value, and pkg/sdjwt's own checks get to run.
//
// Calling sdjwt directly is the point: it is the path an entry point that forgot
// its guard would take, and before the change it panicked.
func TestTheBridgesPreserveNil(t *testing.T) {
	t.Parallel()

	t.Run("a nil signer does not become a non-nil sdjwt.Signer", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, ap2.JOSESigner(nil), "the wrapper manufactured a signer out of nothing")

		_, err := sdjwt.Issue(t.Context(), ap2.JOSESigner(nil), map[string]any{"vct": "x"}, nil)
		assert.ErrorIs(t, err, sdjwt.ErrUnsupportedAlgorithm,
			"pkg/sdjwt has a nil check for this and has to be allowed to reach it")
	})

	t.Run("a nil verifier does not become a non-nil sdjwt.Verifier", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, ap2.JOSEVerifier(nil), "the wrapper manufactured a verifier out of nothing")

		_, err := sdjwt.VerifyJWT("a.b.c", "kb+jwt", ap2.JOSEVerifier(nil))
		assert.ErrorIs(t, err, sdjwt.ErrInvalidOptions,
			"pkg/sdjwt has a nil check for this and has to be allowed to reach it")
	})
}

// issueClaims signs an arbitrary claim set, which the exported constructors
// correctly refuse to do. Verification has to refuse what a hostile or an older
// issuer can produce, not only what this package produces — and every
// interesting failure here is one IssueCheckout cannot be talked into making.
// blind names the claims to withhold, and is variadic because the two mandates
// do not agree on what is withholdable: a Checkout Mandate hides checkout_jwt,
// a Payment Mandate hides risk_data or nothing at all. The Blinder refuses a
// path it cannot find, so this cannot be hardcoded to either.
func issueClaims(t *testing.T, f fixture, claims map[string]any, blind ...string) *sdjwt.SDJWT {
	t.Helper()

	payload, disclosures, err := f.blinder.Blind(claims, blind...)
	require.NoError(t, err, "blinding")

	sd, err := sdjwt.Issue(t.Context(), ap2.JOSESigner(f.signer), payload, disclosures)
	require.NoError(t, err, "issuing")
	return sd
}

// issueWithVCT mints an otherwise well-formed mandate carrying an arbitrary
// credential type.
func issueWithVCT(t *testing.T, f fixture, vct any) *sdjwt.SDJWT {
	t.Helper()

	hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
	require.NoError(t, err, "computing the binding")

	return issueClaims(t, f, map[string]any{
		"vct":           vct,
		"checkout_jwt":  merchantCheckout,
		"checkout_hash": hash,
	}, "checkout_jwt")
}
