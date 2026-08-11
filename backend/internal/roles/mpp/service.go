// Package mpp is the mock Merchant Payment Processor.
//
// It is the last party in the chain and the only one that moves money — which
// in this project means recording that it would have. Nothing settles, no rail
// is contacted, and the token it redeems was minted by the mock Credential
// Provider next door. What is real is the check it performs before any of that:
// the credential must be scoped to the purchase being paid for.
package mpp

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Service is the mock Merchant Payment Processor.
type Service struct {
	// ID names this processor in the receipts it signs.
	ID string
	// Payments verify the Payment Mandate. The processor needs the purchase
	// this credential is being spent on, and it must come from claims a
	// signature covers rather than from a field beside them. This is the Human
	// Present half. Required.
	Payments ap2.PaymentVerifier
	// PaymentChains verify a delegation chain instead — the Human Not Present
	// half, where the closed Payment Mandate is a Key Binding JWT the agent
	// signed under the key the user's open mandate endorsed.
	//
	// A second field rather than a wider Payments, mirroring the split
	// ap2.PaymentChainVerifier draws: the two modes must not share an entry
	// point a caller could hand a chain to by mistake and have it evaluate no
	// constraints. ap2.CredentialProviderRules satisfies both, so production
	// wiring assigns the same value twice.
	//
	// Optional; a processor without it settles Human Present purchases only,
	// and answers verifier_unavailable to a chain.
	//
	// The rule set is the Credential Provider's, and that is the reach gap
	// ap2's ForPayment row records rather than an oversight here: AP2 has this
	// mandate verified by three parties and evaluations[ForPayment] credits
	// them all with what the narrowest holds. An MPP sits merchant-side and may
	// hold the checkout, in which case constraints it could have enforced are
	// withheld from it — safe, and not right. Closing it is a third row and a
	// third rule set, not a field here.
	PaymentChains ap2.PaymentChainVerifier
	// Rules decide whether a credential may be spent on that purchase.
	Rules ap2.CredentialVerifier
	// Signer holds the processor's key.
	Signer authz.Signer
	// Keys publishes the public half.
	Keys authz.KeySetPublisher
	// Clock stamps receipts.
	Clock authz.Clock
	// Challenge issues the nonces this processor's Human Not Present
	// verification checks a delegation's key binding against. Optional; when it
	// is absent the route is not registered, on the terms the merchant's
	// identical field sets out.
	Challenge *crypto.Challenger
	// Events records the moments this role owns: its verdict on the payment
	// side of a purchase, and the receipt carrying it. Optional — a nil Emitter
	// records nothing.
	Events *obs.Emitter
}

// settlement is what POST /payment takes.
//
// Both the Payment Mandate and the credential, because the processor's question
// needs both: the mandate says which purchase is being paid for, and the
// credential says which purchase the money is good for. Sending only the
// credential would leave it comparing a digest against a digest the same party
// supplied.
//
// The mandate arrives in one of two shapes and **the chain has its own field**,
// on the terms credprovider's identical request shape sets out: ap2 gives the
// two modes different interfaces so that a chain cannot reach the entry point
// that evaluates no constraints, and a body this handler sniffed would put that
// entry point back at the transport. Exactly one of Mandate and Chain; both or
// neither is request_malformed before anything is parsed.
type settlement struct {
	// Mandate is the closed Payment Mandate in SD-JWT compact serialisation —
	// Human Present, signed by the user.
	Mandate string `json:"mandate"`

	// Chain is the delegation chain — Human Not Present, the open Payment
	// Mandate the user signed and the closed one the agent signed under the key
	// it endorses.
	Chain string `json:"chain"`

	// Nonce is the challenge this processor issued, echoed back so the
	// delegation's key binding can be compared against it. See credprovider's
	// examineChain for why the same value is checked twice, and why the two
	// failures are answered differently.
	//
	// It is this processor's own challenge and not the Credential Provider's,
	// for the reason its Audience is its own: sdjwt.Delegate writes both into
	// the delegating hop, so a payment chain presented here is a document
	// minted for this verifier rather than one forwarded from the last.
	Nonce string `json:"nonce"`

	Credential generated.PaymentCredential `json:"credential"`
}

// examined is one settlement reduced to what the response needs: the artefact a
// receipt names, and the single verdict covering both of this role's questions.
//
// The two modes differ in how those are obtained and in nothing afterwards, so
// the split stops here. One tail means a mode cannot acquire an exit that skips
// the receipt, which AP2 requires in both directions.
type examined struct {
	// presented is what the receipt's reference is a digest of: the presented
	// SD-JWT, or the chain's delegating hop.
	presented ap2.Presented
	// checkoutHash is the purchase the mandate names, read only once a
	// signature has established it. A refused presentation leaves it empty,
	// which is the honest value: this role has confirmed no checkout to put a
	// verdict against.
	checkoutHash string
	// amount is the payment amount the verified mandate declared, set on the
	// same terms checkoutHash is: once the mandate's own signature has
	// established it, even if the credential-scope check that runs afterwards
	// then refuses. Neither of this role's two questions is about the amount —
	// it runs no ap2.AmountMatches — so a rejection of the mandate itself,
	// rather than of the credential over it, leaves this nil rather than risk a
	// zero-value Amount off a mandate that never fully decoded reading as a
	// genuine zero.
	amount  *generated.Amount
	verdict error
}

type outcome struct {
	Receipt string `json:"receipt"`
	// Settled is deliberately explicit rather than implied by the status code.
	// A caller reading JSON should not have to reconstruct the answer from the
	// transport, and the receipt — which is the evidence — says the same thing.
	Settled bool `json:"settled"`
}

// Handler returns the processor's routes.
func (s *Service) Handler() (http.Handler, error) {
	if s.Payments == nil || s.Rules == nil || s.Signer == nil || s.Keys == nil || s.Clock == nil {
		return nil, errors.New(
			"mpp: a Service needs payment rules, credential rules, a signer, a key set and a clock")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("POST /payment", s.settle)
	if s.Challenge != nil {
		mux.Handle("GET "+roles.NoncePath, roles.Nonce(s.Challenge))
	}
	return roles.Middleware(s.Clock, mux)
}

// amountOpt turns a possibly-nil amount into the EventOpt slice Emit and
// EmitRejection take, so a call site with nothing reliable to report — see
// examined's own comment on amount — attaches nothing rather than a zero-value
// generated.Amount that would read as a genuine zero on the log.
//
// Duplicated from credprovider's identical helper rather than shared: the two
// packages already duplicate the whole examined/examine shape on the "no
// internal/common" rule, and a third protocol is what is meant to reveal
// whether this particular seam is real.
func amountOpt(amount *generated.Amount) []obs.EventOpt {
	if amount == nil {
		return nil
	}
	return []obs.EventOpt{obs.WithAmount(*amount)}
}

// settle checks that the money being spent is the money scoped to this purchase.
//
// The Payment Mandate is verified here rather than taken on the word of whoever
// already did. That is not distrust of the Credential Provider — it is that this
// role's answer depends on which purchase the mandate names, and a claim read
// out of an unverified payload is a claim the caller chose. Presenting a mandate
// whose transaction_id had been edited to match a stolen credential would
// otherwise pass the scope check exactly.
//
// Both modes land here, and the ordering that makes this role's answer sound is
// the same in each: whichever shape the mandate arrived in, its claims are
// established by a signature before the digest inside them is compared against
// the credential's.
func (s *Service) settle(w http.ResponseWriter, r *http.Request) {
	var req settlement
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	got, ok := s.examine(w, req)
	if !ok {
		// Answered already, with Problem Details and no receipt, which is
		// correct exactly where nothing was examined. See examine.
		return
	}

	// Every event below names the checkout the mandate is for, so this role's
	// answer lands on the same spine as the merchant's and the Credential
	// Provider's — three parties, three signatures, one digest, which is the
	// claim the three-lane view exists to make.
	r = r.WithContext(obs.WithDigest(r.Context(), got.checkoutHash))

	// Two questions were asked — is the mandate good, and is this credential
	// scoped to it — and one verdict comes back, so one event says so. The code
	// is what distinguishes them, and it is the same code the receipt carries.
	//
	// got.amount is nil whenever the mandate itself never verified — see
	// examined's own comment — and amountOpt turns that absence into no option
	// at all rather than a zero-value Amount that would read as a genuine one.
	opts := amountOpt(got.amount)
	if got.verdict != nil {
		s.Events.EmitRejection(r.Context(), string(ap2.CodeOf(got.verdict)),
			"payment refused: "+got.verdict.Error(), opts...)
	} else {
		s.Events.Emit(r.Context(), obs.KindMandateVerified,
			"Payment Mandate verified and the credential is scoped to it", opts...)
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
	s.Events.Emit(r.Context(), obs.KindReceiptIssued, "receipt issued for the payment")

	status := http.StatusOK
	if got.verdict != nil {
		status = http.StatusUnprocessableEntity
	}
	roles.OK(w, status, outcome{Receipt: receipt, Settled: got.verdict == nil})
}

// examine settles which mode this settlement is in, verifies the mandate that
// way, and then asks whether the credential belongs to the purchase it names.
//
// It reports false when it has already answered the caller, which is every case
// where nothing was examined — a body naming neither mode or both, an artefact
// that will not parse, a nonce this processor did not issue. Those get Problem
// Details and no receipt.
func (s *Service) examine(w http.ResponseWriter, req settlement) (examined, bool) {
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
			"there is nothing here to settle against: send a Payment Mandate, or a delegation chain")
		return examined{}, false
	}
}

// examineMandate is the Human Present path: verify the mandate, then ask
// whether the credential belongs to the purchase it names.
//
// Two questions in order, and the order is the point: the digest the scope check
// compares against only means anything once a signature has been established
// over the claim carrying it.
func (s *Service) examineMandate(w http.ResponseWriter, req settlement) (examined, bool) {
	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return examined{}, false
	}

	mandate, verdict := s.Payments.VerifyPayment(presented)
	if verdict != nil {
		return examined{presented: presented, verdict: verdict}, true
	}
	amount := mandate.PaymentAmount
	return examined{
		presented:    presented,
		checkoutHash: mandate.CheckoutHash,
		amount:       &amount,
		verdict:      s.Rules.VerifyCredential(req.Credential, mandate.CheckoutHash),
	}, true
}

// examineChain is the Human Not Present path, and asks the same two questions
// in the same order of a chain rather than a single mandate.
//
// The credential is compared against the closed mandate *inside* the chain, so
// the digest still comes from claims a signature covers — the agent's, over a
// delegation the user's own open mandate endorsed. A processor that read the
// digest from anywhere else would be comparing a value the caller supplied
// against another value the caller supplied.
//
// The nonce is checked twice for two different reasons and the two failures are
// answered differently; credprovider's examineChain sets that out in full, and
// nothing about it differs here.
func (s *Service) examineChain(w http.ResponseWriter, req settlement) (examined, bool) {
	if s.PaymentChains == nil || s.Challenge == nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			"this processor was not stood up to verify a delegation chain")
		return examined{}, false
	}
	if err := s.Challenge.Check(req.Nonce); err != nil {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			fmt.Sprintf("the nonce is not one this processor issued and is still honouring: %v", err))
		return examined{}, false
	}

	chain, err := sdjwt.ParseChain(req.Chain)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the chain is not a readable delegate SD-JWT: %v", err))
		return examined{}, false
	}

	authorised, verdict := s.PaymentChains.AuthorisePaymentChain(chain, req.Nonce)
	if verdict != nil {
		return examined{presented: chain, verdict: verdict}, true
	}
	amount := authorised.Closed.PaymentAmount
	return examined{
		presented:    chain,
		checkoutHash: authorised.Closed.CheckoutHash,
		amount:       &amount,
		verdict:      s.Rules.VerifyCredential(req.Credential, authorised.Closed.CheckoutHash),
	}, true
}
