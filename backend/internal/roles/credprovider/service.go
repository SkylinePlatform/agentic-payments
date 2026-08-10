// Package credprovider is the mock Credential Provider.
//
// It is mocked because it has to be: no public sandbox lets a party that is not
// a PSP enrol a real card into an AP2 flow. That is a constraint of the
// ecosystem rather than a shortcut, and nothing here should be mistaken for a
// payments system — no card is enrolled, no PAN exists, and the token this
// mints is spendable only against the other mock in this repository.
//
// What is not mocked is the decision. The Payment Mandate is verified for real,
// and a credential is minted only if it holds.
package credprovider

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Service is the mock Credential Provider.
type Service struct {
	// ID names this provider in the receipts it signs.
	ID string
	// Rules decide whether a presented Payment Mandate is acceptable. This is
	// the Human Present half: a mandate the user signed themselves. Required.
	Rules ap2.PaymentVerifier
	// Chains decide whether a delegation chain is — the Human Not Present half,
	// where the agent signed the closed mandate under the key the user's open
	// one endorsed.
	//
	// A second field rather than a wider Rules, mirroring the split
	// ap2.PaymentChainVerifier draws for the same reason: the two modes must
	// not share an entry point a caller could hand a chain to by mistake and
	// have it evaluate no constraints. ap2.CredentialProviderRules satisfies
	// both interfaces, so production wiring assigns the same value twice —
	// which is the point rather than a redundancy, because the day a
	// deployment wants to delegate one and not the other, it can.
	//
	// Optional, and its absence is a real deployment: a provider offering
	// Human Present only. A chain presented to one answers verifier_unavailable
	// rather than being verified against nothing.
	Chains ap2.PaymentChainVerifier
	// Signer holds the provider's key.
	Signer authz.Signer
	// Keys publishes the public half.
	Keys authz.KeySetPublisher
	// Clock stamps receipts and credential expiry.
	Clock authz.Clock
	// CredentialLifetime is how long a minted credential stays redeemable.
	CredentialLifetime time.Duration
	// Challenge issues the nonces this provider's Human Not Present
	// verification checks a delegation's key binding against. Optional; when it
	// is absent the route is not registered, on the terms the merchant's
	// identical field sets out.
	//
	// It is also what makes the nonce a caller sends back checkable. A chain
	// presented to a provider holding no Challenger is refused for the same
	// reason a chain presented to one holding no Chains is: this provider was
	// not stood up to answer that question.
	Challenge *crypto.Challenger
	// Events records the moments this role owns: its verdict on a presented
	// Payment Mandate, and the receipt carrying it. Optional — a nil Emitter
	// records nothing.
	Events *obs.Emitter
}

// request is what POST /credential takes, in either mode.
//
// **The chain has its own field and is never sniffed out of the other one.**
// ap2 gives the two modes different interfaces precisely so that there is no
// single entry point a caller could hand a chain to by mistake, and a request
// shape that guessed — parse as a chain, fall back to a mandate — would put
// that entry point back at the transport, where nothing in the type system is
// watching. Deciding which mode is being used has to be the caller's statement
// rather than this handler's inference.
//
// Exactly one of Mandate and Chain, therefore. Both or neither is
// request_malformed before anything is parsed: a body carrying both does not
// say which mode it means, and answering it by preferring one would be the
// sniffing this shape exists to prevent.
type request struct {
	// Mandate is the closed Payment Mandate in SD-JWT compact serialisation —
	// Human Present, signed by the user.
	Mandate string `json:"mandate"`

	// Chain is the delegation chain in the compact serialisation of the
	// delegate SD-JWT — Human Not Present, an open Payment Mandate the user
	// signed and a closed one the agent signed under the key it endorses.
	Chain string `json:"chain"`

	// Nonce is the challenge this provider issued to the agent, echoed back so
	// that it can be compared against the one the delegation's key binding
	// actually signed.
	//
	// It travels in the request because crypto.Challenger remembers nothing —
	// see its doc comment. The value is checked twice for two different
	// reasons: Challenger.Check establishes that this provider issued it and
	// issued it recently, and ap2.AuthorisePaymentChain establishes that the
	// agent signed over the same string. Neither check subsumes the other, and
	// they fail differently: the first is a caller that has not spoken to this
	// verifier, the second is a proof of possession that does not hold.
	//
	// Read only in the chain mode. A Human Present mandate carries no key
	// binding for a nonce to be part of.
	Nonce string `json:"nonce"`
}

// issued is what a successful POST /credential returns.
//
// The receipt travels with the credential rather than instead of it, because
// the two answer different questions: the receipt says this provider verified
// the mandate and would stand behind that in a dispute, and the credential is
// the thing the money comes out of.
type issued struct {
	Receipt    string                       `json:"receipt"`
	Credential *generated.PaymentCredential `json:"credential,omitempty"`
}

// Handler returns the provider's routes.
func (s *Service) Handler() (http.Handler, error) {
	if s.Rules == nil || s.Signer == nil || s.Keys == nil || s.Clock == nil {
		return nil, errors.New("credprovider: a Service needs rules, a signer, a key set and a clock")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("POST /credential", s.fund)
	if s.Challenge != nil {
		mux.Handle("GET "+roles.NoncePath, roles.Nonce(s.Challenge))
	}
	return roles.Middleware(s.Clock, mux)
}

// examined is one presentation reduced to what the rest of funding needs,
// whichever mode it arrived in: the artefact a receipt names, the verdict on
// it, and the checkout a credential would be scoped to.
//
// The two modes differ in how those three are obtained and in nothing after
// that, which is why the split stops here rather than reaching the response.
// A rejection receipt is issued the same way for a chain as for a mandate, and
// keeping one tail means a mode cannot acquire an exit that skips it.
type examined struct {
	// presented is what the receipt's reference is a digest of: the presented
	// SD-JWT, or the chain's delegating hop.
	presented ap2.Presented
	// checkoutHash is read only when verdict is nil; a refused presentation
	// scopes no credential.
	checkoutHash string
	verdict      error
}

// fund verifies a Payment Mandate and mints a credential scoped to its checkout.
//
// This role never sees the checkout itself — AP2 sends it the Payment Mandate
// and nothing else, and a closed Payment Mandate carries transaction_id rather
// than the document. So it scopes the credential to the digest it was given,
// and the party that holds the document is the one that later proves the two
// agree. That is not a gap: it is why the digest is in three artefacts.
//
// Both modes land here. Under Human Present the user signed the closed mandate
// and it arrives on its own; under Human Not Present the agent signed it as a
// delegation and it arrives inside a chain whose root is the open mandate the
// user signed. What the credential is scoped to is the same claim either way,
// which is why the mode is settled in examine and forgotten immediately after.
func (s *Service) fund(w http.ResponseWriter, r *http.Request) {
	var req request
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	got, ok := s.examine(w, req)
	if !ok {
		// examine has already answered with Problem Details, which is correct
		// exactly when nothing has been examined — see its own comment on the
		// split, and TestAnUnparseableBodyGetsProblemDetailsAndNoReceipt for the
		// rule.
		return
	}

	// Every event below names the checkout this mandate is for, which is what
	// lets the three-lane view hang this role's verdict on the same spine as the
	// merchant's. It is the digest this role verified a signature over rather
	// than one it was told — see examined.checkoutHash, which is read only when
	// the verdict is nil, so a refused presentation contributes no digest to a
	// spine it never joined.
	r = r.WithContext(obs.WithDigest(r.Context(), got.checkoutHash))

	// The verdict is recorded before the receipt that carries it, in the order
	// the two happened. ap2.CodeOf is the same mapping IssueReceipt uses, so the
	// code in the log and the code in the signed answer cannot disagree — which
	// matters because a reader comparing them would have no way to tell which
	// one was wrong.
	if got.verdict != nil {
		s.Events.EmitRejection(r.Context(), string(ap2.CodeOf(got.verdict)),
			"Payment Mandate refused: "+got.verdict.Error())
	} else {
		s.Events.Emit(r.Context(), obs.KindMandateVerified, "Payment Mandate verified")
	}

	receipt, err := ap2.IssueReceipt(r.Context(), got.presented, got.verdict, ap2.ReceiptOptions{
		Issuer:      s.ID,
		MandateType: generated.ReceiptMandateTypePayment,
		Signer:      s.Signer,
		Clock:       s.Clock,
	})
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("issuing the receipt: %v", err))
		return
	}
	s.Events.Emit(r.Context(), obs.KindReceiptIssued, "receipt issued for the Payment Mandate")

	if got.verdict != nil {
		// A refusal still gets a receipt, and gets no credential. Returning one
		// alongside a rejection would be the whole point of this role failing.
		roles.OK(w, http.StatusUnprocessableEntity, issued{Receipt: receipt})
		return
	}

	credential, err := s.mint(got.checkoutHash)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("minting the credential: %v", err))
		return
	}
	roles.OK(w, http.StatusOK, issued{Receipt: receipt, Credential: &credential})
}

// examine settles which mode this request is in and reaches a verdict in it.
//
// It reports false when it has already answered the caller, which is every case
// where **nothing has been examined**: a body that names neither mode or both,
// an artefact that will not parse, a nonce this provider did not issue. Those
// get Problem Details and no receipt, because a receipt whose reference points
// at nothing is worse than none — the rule
// TestAnUnparseableBodyGetsProblemDetailsAndNoReceipt already pins.
//
// True with a non-nil verdict is the other side of that line: something was
// examined and refused, and it gets a signed answer.
func (s *Service) examine(w http.ResponseWriter, req request) (examined, bool) {
	switch {
	case req.Mandate != "" && req.Chain != "":
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"a Payment Mandate and a delegation chain are the two modes this endpoint answers, and a request carrying both does not say which one it means")
		return examined{}, false
	case req.Chain != "":
		return s.examineChain(w, req)
	case req.Mandate != "":
		return s.examineMandate(w, req)
	default:
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"there is nothing here to fund: send a Payment Mandate to be verified, or a delegation chain")
		return examined{}, false
	}
}

// examineMandate is the Human Present path, unchanged from when it was the only
// one.
func (s *Service) examineMandate(w http.ResponseWriter, req request) (examined, bool) {
	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return examined{}, false
	}

	mandate, verdict := s.Rules.VerifyPayment(presented)
	return examined{presented: presented, checkoutHash: mandate.CheckoutHash, verdict: verdict}, true
}

// examineChain is the Human Not Present path: a delegation chain, authorised
// against the constraints in the open mandate at its root.
//
// # The nonce is wrong in two different ways and they are answered differently
//
// crypto.Challenger.Check asks whether this value is one *this provider* handed
// out, recently. A caller that fails it has not spoken to this verifier, or has
// waited too long; no mandate has been read at that point, so the answer is
// Problem Details carrying request_malformed and no receipt.
//
// ap2.AuthorisePaymentChain asks a different question with the same value:
// whether the agent's *signature* covers it. A chain minted against some other
// live challenge passes Check and fails here, as sdjwt.ErrKeyBindingInvalid —
// a proof of possession that does not hold, on a chain that has been examined,
// so it gets a receipt naming key_binding_invalid.
//
// Neither check subsumes the other. Drop the first and this provider accepts a
// nonce nobody issued, so long as the agent signed it — which is the agent
// choosing its own challenge. Drop the second and it accepts a live challenge
// attached to a delegation signed over a different one, which is a proof lifted
// from another exchange.
//
// # No subject is built here
//
// A Credential Provider cannot describe the purchase it is being asked to fund
// until the chain carrying it has verified: its only source for the amount and
// the payee is the closed mandate inside. ap2.AuthorisePaymentChain derives the
// subject itself, from the verified closed mandate and its own clock — see
// ap2.PaymentSubject. That deletion is what made this method writable at all.
func (s *Service) examineChain(w http.ResponseWriter, req request) (examined, bool) {
	if s.Chains == nil || s.Challenge == nil {
		// The verifier's own gap rather than the caller's mistake: this
		// provider offers Human Present only, and saying so as
		// request_malformed would send the one party that did nothing wrong
		// away to debug a well-formed request.
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			"this provider was not stood up to verify a delegation chain")
		return examined{}, false
	}
	if err := s.Challenge.Check(req.Nonce); err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("the nonce is not one this provider issued and is still honouring: %v", err))
		return examined{}, false
	}

	chain, err := sdjwt.ParseChain(req.Chain)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the chain is not a readable delegate SD-JWT: %v", err))
		return examined{}, false
	}

	authorised, verdict := s.Chains.AuthorisePaymentChain(chain, req.Nonce)
	return examined{
		presented:    chain,
		checkoutHash: authorised.Closed.CheckoutHash,
		verdict:      verdict,
	}, true
}

// mint produces a credential good for one checkout and nothing else.
func (s *Service) mint(checkoutHash string) (generated.PaymentCredential, error) {
	// crypto/rand, not math/rand — this is the value that stands in for money,
	// and math/rand is banned everywhere in this module for exactly this reason.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return generated.PaymentCredential{}, fmt.Errorf("token entropy: %w", err)
	}

	expires := s.Clock.Now().Add(s.credentialLifetime())
	return generated.PaymentCredential{
		Token:        "tok_" + base64.RawURLEncoding.EncodeToString(raw),
		CheckoutHash: checkoutHash,
		ExpiresAt:    &expires,
	}, nil
}

func (s *Service) credentialLifetime() time.Duration {
	if s.CredentialLifetime <= 0 {
		// Long enough to reach the processor, short enough that a credential
		// left lying about stops being worth anything quickly.
		return 10 * time.Minute
	}
	return s.CredentialLifetime
}
