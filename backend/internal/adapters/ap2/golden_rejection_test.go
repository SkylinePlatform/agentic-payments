package ap2_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The rejection half of the conformance suite: not what this implementation
// produces, but what it refuses, and what it calls each refusal.
//
// golden_test.go pins the artefacts a second implementation has to reproduce to
// interoperate on the happy path — the vct strings, the digests, two whole wire
// serialisations. This file pins the other question, and it is the one a second
// implementer asks first: *given a mandate my code produced, what will yours say
// when it says no?* An implementation that agrees on every byte of a valid
// mandate and answers `verifier_unavailable` where this one answers
// `constraint_violated` does not interoperate. The first tells an agent to retry
// the identical purchase; the second tells it to come back cheaper.
//
// # What a row pins, and what it cannot
//
// Each row is an artefact, a real verification entry point, and the canonical
// error code the refusal carries. Nothing here pins bytes. Every mandate below
// is signed with the ES256 key newFixture mints, so the token differs on every
// run — the constraint golden_test.go's own comment sets out at length, and the
// reason two of its vectors sign with hmacAuthzKey instead. A rejection vector
// does not need the bytes: what a second implementation reproduces is the
// *verdict*, and the verdict is a pair (this input, that code).
//
// So the mutation these rows cannot catch is a wrong expectation. A row whose
// `code` was edited to match a broken implementation passes, by construction —
// the vector *is* the expectation, and there is no third party to appeal to.
// What protects it is review and the sentence beside it, which is why every row
// carries one and why `reading` is what the assertion prints on failure.
//
// # Why the sentinel is asserted as well as the code
//
// Several codes have more than one producer — `disclosure_insufficient` has two
// with different meanings, `mandate_version_unsupported` has two, and
// `mandate_malformed` has many. A row asserting only the code would stay green
// if the artefact started failing somewhere else entirely that happened to carry
// the same code, which is a vector passing for the wrong reason. The sentinel
// pins the route; the code pins what a counterparty is told.

// rejectionVector is one refusal: an artefact, the route it takes, and the
// canonical code a receipt and an RFC 9457 response carry for it.
type rejectionVector struct {
	// name is the scenario, as it appears in `make vectors` output. It is
	// deliberately a sentence about the input rather than about the code — the
	// code is the answer, and a reader scanning the suite is looking for the
	// question.
	name string

	// code is what this implementation calls the refusal. This is the value a
	// second implementation has to agree with.
	code generated.ErrorCode

	// sentinel is the Go error the route raises, asserted so that a row cannot
	// pass by failing somewhere else that shares the code.
	sentinel error

	// reading is the one sentence a reader of this vector needs. It is the
	// assertion's failure message, so a broken row explains why it mattered
	// rather than only which two strings differed.
	reading string

	// provoke builds the artefact and drives a real verification entry point.
	// It returns the error that entry point returned; a nil return fails the
	// row, since CodeOf(nil) is the empty string and the empty string is not in
	// the enum.
	provoke func(t *testing.T) error
}

// cnfClaim renders a public key as the RFC 7800 confirmation claim, for the
// mandates below that are minted claim by claim rather than through this
// package's own issuers.
//
// generated.PublicKey's JSON tags are JWK member names — kty, crv, x, y, kid
// from RFC 7517 §4 and RFC 7518 §6 — so this is a re-encoding rather than a
// translation, and it lands on what ap2.encodeCnf writes. open_internal_test.go's
// TestCnfCarriesTheWholeKeyNotAReference is what holds that agreement.
func cnfClaim(t *testing.T, key generated.PublicKey) map[string]any {
	t.Helper()

	raw, err := json.Marshal(key)
	require.NoError(t, err, "encoding the agent key as a JWK")
	var jwk map[string]any
	require.NoError(t, json.Unmarshal(raw, &jwk), "reading the JWK back as an object")
	return map[string]any{"jwk": jwk}
}

// appendDisclosure splices an extra Disclosure onto a presentation's wire form.
//
// The serialisation is `<jwt>~<d1>~…~<kb-or-empty>`, so the trailing separator
// has to come off before the new part goes on and back afterwards. Doing it as
// string surgery rather than through the library is the point: an SDJWT built by
// this package can only hold Disclosures it committed to, and the presentation
// this produces is one a Holder can send and a Verifier must refuse.
func appendDisclosure(presented string, extra sdjwt.Disclosure) string {
	return strings.TrimSuffix(presented, "~") + "~" + extra.String() + "~"
}

// rejectionVectors is the suite, as data.
//
// A function rather than a package-level var so that each row's closure is built
// fresh per run, and so that TestGoldenEveryErrorCodeIsClassified can read the
// set of codes this suite claims to cover without executing any of them. That
// separation is what makes the classification check meaningful: it compares a
// declared claim against a declared classification, and the run below is what
// proves the claim was true.
func rejectionVectors() []rejectionVector {
	return []rejectionVector{
		// ── Request handling ────────────────────────────────────────────────
		{
			name:     "a mandate presented where a receipt was expected",
			code:     generated.ErrorCodeRequestMalformed,
			sentinel: sdjwt.ErrUnexpectedType,
			reading: "every artefact in AP2 is a compact JWS signed by the same keys, so what makes a token a receipt rather than a mandate is the header saying so; " +
				"a verifier that read the claims it expected and ignored the rest would accept whichever it was handed",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := reparse(t, issue(t, f, mandate()))
				_, err := ap2.VerifyReceipt(sd.IssuerJWT(), f.verifier)
				return err
			},
		},

		// ── Securing format ─────────────────────────────────────────────────
		{
			name:     "a closed Checkout Mandate carrying no checkout_hash",
			code:     generated.ErrorCodeMandateMalformed,
			sentinel: ap2.ErrMandateMalformed,
			reading: "checkout_hash is the whole of the binding and AP2 makes it mandatory while checkout_jwt is withholdable, " +
				"so a mandate missing the hash is not a minimised presentation, it is a mandate that authorises nothing in particular",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := issueClaims(t, f, map[string]any{
					"vct":          ap2.VCTCheckoutClosed,
					"checkout_jwt": merchantCheckout,
				})
				_, err := ap2.VerifyCheckout(reparse(t, sd), f.options())
				return err
			},
		},
		{
			name:     "a mandate verified against a key that did not sign it",
			code:     generated.ErrorCodeSignatureInvalid,
			sentinel: sdjwt.ErrSignatureInvalid,
			reading: "both keys here are ES256, so the header's alg matches and only the bytes disagree — " +
				"this is the plain forgery case, distinct from the algorithm confusion the next vector covers",
			provoke: func(t *testing.T) error {
				issuer := newFixture(t)
				stranger := newFixture(t)

				opts := issuer.options()
				opts.Issuer = stranger.verifier
				_, err := ap2.VerifyCheckout(reparse(t, issue(t, issuer, mandate())), opts)
				return err
			},
		},
		{
			name:     "a mandate declaring a truncated disclosure hash",
			code:     generated.ErrorCodeAlgorithmUnsupported,
			sentinel: sdjwt.ErrUnsupportedHashAlg,
			reading: "sha-256-64 is a real entry in the IANA Named Information registry and is truncated to 64 bits, " +
				"so membership of that registry is not the acceptance criterion — a verifier that computed whatever _sd_alg named would accept digests a forger can collide",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
				require.NoError(t, err, "computing the binding")

				// Built through sdjwt.Issue rather than through the Blinder,
				// which writes _sd_alg itself and would overwrite this.
				sd, err := sdjwt.Issue(t.Context(), ap2.JOSESigner(f.signer), map[string]any{
					"vct":           ap2.VCTCheckoutClosed,
					"checkout_jwt":  merchantCheckout,
					"checkout_hash": hash,
					"_sd_alg":       "sha-256-64",
				}, nil)
				require.NoError(t, err, "issuing the mandate the vector is about")

				_, err = ap2.VerifyCheckout(reparse(t, sd), f.options())
				return err
			},
		},
		{
			name:     "a mandate whose exp has passed",
			code:     generated.ErrorCodeMandateExpired,
			sentinel: sdjwt.ErrExpired,
			reading: "expiry is judged against the clock the verifier was given rather than the one on the wall, " +
				"which is what makes this row a test at all rather than a two-day wait",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := reparse(t, issue(t, f, mandate()))
				f.clock.Set(expires.Add(time.Second))

				_, err := ap2.VerifyCheckout(sd, f.options())
				return err
			},
		},
		{
			name:     "a mandate whose nbf has not arrived",
			code:     generated.ErrorCodeMandateNotYetValid,
			sentinel: sdjwt.ErrNotYetValid,
			reading:  "the mirror of expiry, and a separate code because the holder's next move differs: an expired mandate needs reissuing and this one needs waiting for",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				hash, err := f.blinder.HashAlg().Digest(merchantCheckout)
				require.NoError(t, err, "computing the binding")

				sd := issueClaims(t, f, map[string]any{
					"vct":           ap2.VCTCheckoutClosed,
					"checkout_jwt":  merchantCheckout,
					"checkout_hash": hash,
					"nbf":           base.Add(time.Hour).Unix(),
				}, "checkout_jwt")

				_, err = ap2.VerifyCheckout(reparse(t, sd), f.options())
				return err
			},
		},
		{
			name:     "a presentation carrying a Disclosure the issuer never committed to",
			code:     generated.ErrorCodeDisclosureUnmatched,
			sentinel: sdjwt.ErrDisclosureUnmatched,
			reading: "the digest of a Disclosure is what the issuer's signature covers, so an unmatched one is a claim somebody added after signing; " +
				"accepting it silently would let a Holder write claims into a credential it only holds",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				presented := reparse(t, issue(t, f, mandate()))

				// A second mandate over a different checkout, so its
				// checkout_jwt Disclosure discloses a different value and
				// therefore hashes to a digest the first mandate's payload
				// cannot contain. The two fixtures draw from identical salt
				// sources, so an identical value would produce an identical
				// Disclosure and prove nothing.
				other := newFixture(t)
				foreign := issue(t, other, checkoutFor(otherCheckout)).Disclosures()
				require.NotEmpty(t, foreign, "the vector needs a Disclosure to splice in")

				spliced, err := sdjwt.Parse(appendDisclosure(presented.String(), foreign[0]))
				require.NoError(t, err, "a spliced presentation still has to parse; the refusal under test is a verification one")

				_, err = ap2.VerifyCheckout(spliced, f.options())
				return err
			},
		},
		{
			name:     "a presentation that withheld the checkout from a verifier holding no copy",
			code:     generated.ErrorCodeDisclosureInsufficient,
			sentinel: ap2.ErrBindingUnverifiable,
			reading: "a hash nobody can recompute is not a binding — the claim says only that whoever signed the mandate wrote a hash into it, " +
				"which is exactly the assertion the recompute rule exists to distrust",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := reparse(t, issue(t, f, mandate()))

				withheld, err := sd.Present(func(sdjwt.Disclosure) bool { return false })
				require.NoError(t, err, "withholding checkout_jwt is a legitimate presentation, and has to build")

				_, err = ap2.VerifyCheckout(reparse(t, withheld), f.options())
				return err
			},
		},
		{
			name:     "a chain narrowed past the limit this verifier insists on seeing",
			code:     generated.ErrorCodeDisclosureInsufficient,
			sentinel: ap2.ErrDisclosureInsufficient,
			reading: "the second producer of this code and the one that matters under Human Not Present: which constraint was withheld is unrecoverable, " +
				"so a verifier states what it needs constrained rather than inspecting what it was denied",
			provoke: func(t *testing.T) error {
				fx := chainFixture(t, 18900)
				// merchant.id is in constraint.FieldNames and the built
				// scenario's four constraints are amount, at and the two route
				// attributes — so this presentation is complete and the policy
				// is still unsatisfied, which is the case the code is for.
				// Naming "amount" would make the row pass whether or not the
				// check ran; naming a field the registry does not hold would
				// make it pass for the reason ChainOptions.RequireConstrained
				// warns about, a policy that can never be satisfied by anything.
				fx.opts.RequireConstrained = []string{"merchant.id"}

				_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
				return err
			},
		},
		{
			name:     "a required proof of possession that did not arrive",
			code:     generated.ErrorCodeKeyBindingRequired,
			sentinel: sdjwt.ErrKeyBindingRequired,
			reading: "RFC 9901 §7.3 step 1 makes this come from policy and never from what the presenter chose to send; " +
				"a verifier that checks key binding only when it is present does not check it",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				opts := f.options()
				opts.KeyBinding = ap2.KeyBindingPolicy{
					HolderKey: resolveTo(f.verifier),
					Required:  true,
					Audience:  verifierAudience,
					Nonce:     transactionNonce,
				}

				_, err := ap2.VerifyCheckout(issue(t, f, mandate()), opts)
				return err
			},
		},
		{
			name:     "a proof of possession minted for another transaction",
			code:     generated.ErrorCodeKeyBindingInvalid,
			sentinel: sdjwt.ErrKeyBindingInvalid,
			reading:  "the nonce is what stops a proof being lifted off one purchase and replayed onto another",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				bound := boundMandate(t, f, f.signer, sdjwt.KeyBinding{
					Nonce: "n-some-other-purchase", Audience: verifierAudience, IssuedAt: f.clock.Now(),
				})

				opts := f.options()
				opts.KeyBinding = ap2.KeyBindingPolicy{
					HolderKey: resolveTo(f.verifier),
					Required:  true,
					Audience:  verifierAudience,
					Nonce:     transactionNonce,
				}

				_, err := ap2.VerifyCheckout(bound, opts)
				return err
			},
		},

		// ── Authorisation ───────────────────────────────────────────────────
		{
			name:     "a credential type carrying a version this verifier does not implement",
			code:     generated.ErrorCodeMandateVersionUnsupported,
			sentinel: ap2.ErrUnsupportedVersion,
			reading: "the version suffix on vct exists to make this refusable rather than guessable, so it is refused rather than guessed — " +
				"an implementation that matched the prefix would accept a mandate whose semantics it has never seen",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				_, err := ap2.VerifyCheckout(
					reparse(t, issueWithVCT(t, f, ap2.VCTCheckoutClosed+"7")), f.options())
				return err
			},
		},
		{
			name:     "an open Checkout Mandate presented where a closed one belongs",
			code:     generated.ErrorCodeMandateVersionUnsupported,
			sentinel: ap2.ErrWrongMandateType,
			reading: "the second producer of this code: the vct names one of AP2's four mandates and not the one being verified, " +
				"which is the escalation the open/closed split exists to prevent rather than an unknown credential",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				_, err := ap2.VerifyCheckout(
					reparse(t, issueWithVCT(t, f, ap2.VCTCheckoutOpen)), f.options())
				return err
			},
		},
		{
			name:     "a Checkout Mandate checked against a different merchant document",
			code:     generated.ErrorCodeCheckoutHashMismatch,
			sentinel: ap2.ErrCheckoutHashMismatch,
			reading:  "the mandate authorises a purchase, and this is a different purchase — the substitution the binding exists to catch",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := issue(t, f, mandate())

				// The document is withheld, so the verifier falls back to the
				// copy it holds — and the copy is another checkout. Both halves
				// are needed: a disclosed checkout_jwt would be compared against
				// opts.Checkout first and refused as a substitution before the
				// digest was ever recomputed.
				withheld, err := sd.Present(func(sdjwt.Disclosure) bool { return false })
				require.NoError(t, err, "withholding checkout_jwt")

				opts := f.options()
				opts.Checkout = otherCheckout
				_, err = ap2.VerifyCheckout(reparse(t, withheld), opts)
				return err
			},
		},
		{
			name:     "a Payment Mandate bound to a checkout other than the one being paid for",
			code:     generated.ErrorCodePaymentBindingMismatch,
			sentinel: ap2.ErrPaymentBindingMismatch,
			reading: "authorisation to buy and authorisation to pay were given for two different purchases; " +
				"a separate code from checkout_hash_mismatch because both mandates may be perfectly valid and simply not belong together",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := reparse(t, issuePayment(t, f, payment(), otherCheckout))

				m, err := ap2.VerifyPayment(sd, ap2.PaymentOptions{Issuer: f.verifier, Clock: f.clock})
				require.NoError(t, err, "the mandate itself is well formed; only its binding names another purchase")

				b, err := ap2.BindingOf(sd, m.CheckoutHash)
				require.NoError(t, err, "reading the binding out of the presentation")
				return b.PaysFor(merchantCheckout)
			},
		},
		{
			name:     "a Payment Mandate paying an amount the checkout does not cost",
			code:     generated.ErrorCodePaymentAmountMismatch,
			sentinel: ap2.ErrPaymentAmountMismatch,
			reading: "the one code in this suite that no AP2 rule produces — the specification binds the two documents by hash and says nothing about the number, " +
				"so a mandate paying 1 USD for a correctly bound 189 USD checkout conforms and is refused here anyway",
			provoke: func(t *testing.T) error {
				underpaying := payment()
				underpaying.PaymentAmount = generated.Amount{Amount: 100, Currency: "USD"}
				return ap2.AmountMatches(underpaying, generated.Amount{Amount: 18900, Currency: "USD"})
			},
		},
		{
			name:     "a Human Not Present purchase above the cap the user set",
			code:     generated.ErrorCodeConstraintViolated,
			sentinel: constraint.ErrViolated,
			reading: "the demo's central beat and the one code an agent acts on: the limits were read, evaluated, and the answer was no, " +
				"which tells the agent to come back with a lower price rather than to retry",
			provoke: func(t *testing.T) error {
				// Beat 5 of the built scenario: $210 against a $200 cap.
				fx := chainFixture(t, 21000)
				_, err := ap2.AuthoriseCheckoutChain(fx.chain, fx.subject, fx.checkoutJWT, fx.opts)
				return err
			},
		},
		{
			name:     "an open mandate constraining a fact this verifier cannot evaluate",
			code:     generated.ErrorCodeConstraintTypeUnknown,
			sentinel: constraint.ErrUnknownField,
			reading: "checkout.line_items is AP2's own constraint and one this verifier does not implement; rejected, never skipped, " +
				"because silently ignoring a limit nobody understands lets the purchase proceed while misrepresenting what was approved",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				sd := issueClaims(t, f, map[string]any{
					"vct":         ap2.VCTCheckoutOpen,
					"cnf":         cnfClaim(t, agentJWK(t)),
					"constraints": []any{map[string]any{"type": "checkout.line_items", "items": []any{}}},
				}, "constraints[]")

				_, err := ap2.VerifyOpenCheckout(reparse(t, sd), ap2.OpenOptions{
					Issuer: f.verifier, Clock: f.clock,
				})
				return err
			},
		},
		{
			name:     "a delegation whose open mandate endorses no usable key",
			code:     generated.ErrorCodeAgentKeyMismatch,
			sentinel: authz.ErrAgentKeyMismatch,
			reading: "the mandate is well formed and the key is the problem, so the two are not reported the same way; " +
				"this error is also a key binding failure and CodeOf's precedence is what makes the receipt say which layer actually knows why",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				agentSigner, agentVerifier := agentKeys(t, f.clock)

				// A key type and no coordinates: decodes cleanly, identifies
				// nobody. IssueOpenCheckout runs the same authz.UsableKey guard
				// and refuses to mint this, so it stands for a mandate minted by
				// an issuer this package did not write.
				root := issueClaims(t, f, map[string]any{
					"vct": ap2.VCTCheckoutOpen,
					"cnf": map[string]any{"jwk": map[string]any{"kty": "EC"}},
				})

				_, err := ap2.AuthoriseCheckoutChain(
					buildClosedCheckoutChain(t, f, root, agentSigner, merchantCheckout),
					purchaseAt(18900), merchantCheckout, ap2.ChainOptions{
						Issuer:   f.verifier,
						AgentKey: resolveTo(agentVerifier),
						Clock:    f.clock,
						Audience: chainAudience,
						Nonce:    chainNonce,
					})
				return err
			},
		},

		// ── Instrument ──────────────────────────────────────────────────────
		{
			name:     "a payment credential scoped to another checkout",
			code:     generated.ErrorCodeCredentialScopeMismatch,
			sentinel: ap2.ErrCredentialScopeMismatch,
			reading: "the third and last place the same digest appears: the mandates may agree with each other perfectly and the money still be wrong, " +
				"which is a different finding from the two mandates disagreeing",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				scoped, err := sdjwt.SHA256.Digest(otherCheckout)
				require.NoError(t, err, "computing the digest the credential is scoped to")
				paying, err := sdjwt.SHA256.Digest(merchantCheckout)
				require.NoError(t, err, "computing the digest of the purchase being paid for")

				return ap2.MPPRules{Clock: f.clock}.VerifyCredential(credentialFor(scoped), paying)
			},
		},

		// ── Service ─────────────────────────────────────────────────────────
		{
			name:     "a verifier stood up without a clock",
			code:     generated.ErrorCodeVerifierUnavailable,
			sentinel: ap2.ErrMisconfigured,
			reading: "the only 5xx in the taxonomy, and the distinction it draws is the one a dispute reads: " +
				"answering mandate_malformed here would send the one party who did nothing wrong away to debug their own request",
			provoke: func(t *testing.T) error {
				f := newFixture(t)
				_, err := ap2.VerifyCheckout(reparse(t, issue(t, f, mandate())),
					ap2.CheckoutOptions{Issuer: f.verifier})
				return err
			},
		},
	}
}

// TestGoldenRejectionVectors runs the suite.
func TestGoldenRejectionVectors(t *testing.T) {
	t.Parallel()

	for _, v := range rejectionVectors() {
		t.Run(v.name, func(t *testing.T) {
			t.Parallel()

			err := v.provoke(t)
			require.Error(t, err, "%s — this vector's whole subject is a refusal, so an acceptance here is the finding", v.reading)
			assert.ErrorIs(t, err, v.sentinel,
				"the code alone would stay green if this artefact started failing somewhere else that shares it; the sentinel is what pins the route")
			assert.Equal(t, v.code, ap2.CodeOf(err), v.reading)
		})
	}
}

// classification is testdata/rejections.json: every code the contract declares,
// and what this implementation does about it.
type classification struct {
	Codes map[generated.ErrorCode]struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	} `json:"codes"`
}

// The five statuses a code can carry. The differences between them are the
// point of having five rather than "covered" and "not covered": a reader
// deciding whether to trust this suite needs to tell "we refuse this and here
// is the input" from "another milestone owns it" from "our HTTP layer owns it"
// from "the agent refuses itself with it" from "nothing here produces it".
// Collapsed into one bucket, a genuine gap would hide behind a scoping
// decision.
const (
	// statusVectored: a row in rejectionVectors provokes it.
	statusVectored = "vectored"

	// statusTAP: Visa TAP's identity axis, milestone #24–#33.
	// internal/adapters/tap is a doc.go and an empty testdata directory, so
	// there is no input to construct rather than no willingness to construct
	// one.
	statusTAP = "tap"

	// statusTransport: produced by this project's HTTP layer rather than by a
	// mandate verifier, answering a rule ADR 0001 sets rather than one AP2 or
	// TAP states. A second implementation of AP2 has nothing to reproduce, and
	// the behaviour is tested where it is produced.
	statusTransport = "transport"

	// statusAgentSide: produced, and by the agent about its own presentation
	// rather than by a verifier about a counterparty's. It never travels, so
	// there is no artefact to hand a second implementation and no answer to
	// compare — which is what a vector is. Enforced where it is produced.
	statusAgentSide = "agent-side"

	// statusNotProduced: nothing in this implementation produces it, and the
	// note says why. This is the status that must never be guessed —
	// TestGoldenTheCodesNothingProducesAndWhatArrivesInstead pins what happens
	// instead for every code carrying it.
	statusNotProduced = "not-produced"
)

// errorCodeSchema is contracts/ read from the module it generates into, the same
// path and for the same reason internal/platform/problem's own suite reads it:
// the classification has to be checked against what the contract declares, and
// checking it against the generated Go constants would agree with a stale schema
// by construction.
const errorCodeSchema = "../../../../contracts/evidence/error_code.json"

func declaredCodes(t *testing.T) []generated.ErrorCode {
	t.Helper()

	raw, err := os.ReadFile(errorCodeSchema)
	require.NoError(t, err, "reading %s", errorCodeSchema)
	var schema struct {
		Enum []generated.ErrorCode `json:"enum"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema), "parsing %s", errorCodeSchema)
	require.NotEmpty(t, schema.Enum, "a contract declaring no codes would make every check below vacuous")
	return schema.Enum
}

func loadClassification(t *testing.T) classification {
	t.Helper()

	raw, err := os.ReadFile("testdata/rejections.json")
	require.NoError(t, err, "reading the rejection classification")
	var c classification
	require.NoError(t, json.Unmarshal(raw, &c), "decoding the rejection classification")
	return c
}

// TestGoldenEveryErrorCodeIsClassified is the test that keeps this suite honest,
// and it is worth more than any single vector above.
//
// A rejection code is a promise to a counterparty. contracts/evidence/error_code.json
// describes the domain rather than what is built — its own description says so —
// so a code can be added there for a rule nobody has implemented yet, and
// nothing anywhere would notice that this implementation cannot produce it. The
// failure mode is not a broken build; it is a second implementer reading the
// enum as a specification of what to expect and waiting for a code that never
// arrives.
//
// So every declared code is either vectored above or classified with a reason,
// and both directions are checked. A code with no entry fails, which is what
// catches an addition to contracts/. An entry naming no declared code fails too,
// which is the mirror: a classification for a code nobody defines any more is a
// claim about a vocabulary that has moved, and it is the entry a removal would
// otherwise leave behind, still asserting something about the implementation.
func TestGoldenEveryErrorCodeIsClassified(t *testing.T) {
	t.Parallel()

	declared := declaredCodes(t)
	classified := loadClassification(t)

	vectored := make(map[generated.ErrorCode]int, len(declared))
	for _, v := range rejectionVectors() {
		vectored[v.code]++
	}

	for _, code := range declared {
		entry, ok := classified.Codes[code]
		require.True(t, ok,
			"%q is declared in contracts/ and this suite says nothing about it — a code nobody has classified is one a counterparty may be promised and never sent", code)
		assert.NotEmpty(t, entry.Note,
			"%q carries a status and no reason, and the reason is the part a second implementer reads", code)

		switch entry.Status {
		case statusVectored:
			assert.NotZero(t, vectored[code],
				"%q is classified as vectored and no row in rejectionVectors produces it, so the classification is describing a suite that does not exist", code)
		case statusTAP, statusTransport, statusAgentSide, statusNotProduced:
			assert.Zero(t, vectored[code],
				"%q is vectored by this suite and classified as though it were not; the classification is what a reader trusts, so it has to be the one that moves", code)
		default:
			t.Errorf("%q carries status %q, which is not one of the five this suite defines", code, entry.Status)
		}
	}

	known := make(map[generated.ErrorCode]struct{}, len(declared))
	for _, code := range declared {
		known[code] = struct{}{}
	}
	for code := range classified.Codes {
		assert.Contains(t, known, code,
			"%q is classified here and no longer declared in contracts/; a stale entry outlives the vocabulary it describes and reads as a promise the schema has withdrawn", code)
	}
}

// TestGoldenTheCodesNothingProducesAndWhatArrivesInstead is what stops
// "not-produced" being a guess.
//
// The classification file says three codes have no producer in this
// implementation. That is a strong claim — a wrong one stops the next person
// looking — so each is stated here as the thing that arrives instead, asserted
// against the code rather than described in prose. If a mapping is ever added,
// this test goes red and the classification has to move with it.
func TestGoldenTheCodesNothingProducesAndWhatArrivesInstead(t *testing.T) {
	t.Parallel()

	t.Run("key_unknown and key_expired: the key store's failures are the verifier's own", func(t *testing.T) {
		t.Parallel()

		// authz declares both sentinels and deliberately excludes them from
		// Owns — see that function's own comment, which records the decision.
		// A verifier that cannot resolve its own kid has a fault of its own, so
		// what reaches the receipt is verifier_unavailable and never a verdict
		// about the counterparty's mandate.
		for _, err := range []error{authz.ErrKeyNotFound, authz.ErrKeyExpired} {
			assert.False(t, authz.Owns(err),
				"the day authz owns one of these, key_unknown and key_expired become producible and this classification is stale")
			assert.Equal(t, generated.ErrorCodeVerifierUnavailable, ap2.CodeOf(err),
				"a key store that lost its own key has not been shown a bad mandate, and telling a counterparty otherwise sends a dispute to the wrong party")
		}
	})

	t.Run("open_mandate_required: a delegation that is not a chain does not parse as one", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		sd := reparse(t, issue(t, f, mandate()))

		// The code names a verifier policy — this closed mandate was signed by
		// an agent, so where is the open one that endorsed the key. This
		// implementation never reaches it, because under Human Not Present the
		// delegation *is* a chain: a lone mandate offered where a chain belongs
		// is refused by the parser, one step before any policy could ask the
		// question. Under Human Present the user signs the closed mandate
		// directly and no open mandate is required at all.
		_, err := sdjwt.ParseChain(sd.String())
		require.Error(t, err, "a single-hop presentation is not a delegation and must not read as one")
		assert.ErrorIs(t, err, sdjwt.ErrMalformedChain,
			"the refusal is structural, which is why no verifier in this implementation is ever in a position to answer open_mandate_required")
	})
}
