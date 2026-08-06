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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Service is the mock Merchant Payment Processor.
type Service struct {
	// ID names this processor in the receipts it signs.
	ID string
	// Payments verify the Payment Mandate. The processor needs the purchase
	// this credential is being spent on, and it must come from claims a
	// signature covers rather than from a field beside them.
	Payments ap2.PaymentVerifier
	// Rules decide whether a credential may be spent on that purchase.
	Rules ap2.CredentialVerifier
	// Signer holds the processor's key.
	Signer authz.Signer
	// Keys publishes the public half.
	Keys authz.KeySetPublisher
	// Clock stamps receipts.
	Clock authz.Clock
}

// settlement is what POST /payment takes.
//
// Both the Payment Mandate and the credential, because the processor's question
// needs both: the mandate says which purchase is being paid for, and the
// credential says which purchase the money is good for. Sending only the
// credential would leave it comparing a digest against a digest the same party
// supplied.
type settlement struct {
	Mandate    string                      `json:"mandate"`
	Credential generated.PaymentCredential `json:"credential"`
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
	return roles.Middleware(s.Clock, mux)
}

// settle checks that the money being spent is the money scoped to this purchase.
//
// The Payment Mandate is verified here rather than taken on the word of whoever
// already did. That is not distrust of the Credential Provider — it is that this
// role's answer depends on which purchase the mandate names, and a claim read
// out of an unverified payload is a claim the caller chose. Presenting a mandate
// whose transaction_id had been edited to match a stolen credential would
// otherwise pass the scope check exactly.
func (s *Service) settle(w http.ResponseWriter, r *http.Request) {
	var req settlement
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return
	}

	verdict := s.verdict(presented, req.Credential)

	receipt, err := ap2.IssueReceipt(r.Context(), presented, verdict, ap2.ReceiptOptions{
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

	status := http.StatusOK
	if verdict != nil {
		status = http.StatusUnprocessableEntity
	}
	roles.OK(w, status, outcome{Receipt: receipt, Settled: verdict == nil})
}

// verdict verifies the mandate, then asks whether the credential belongs to the
// purchase it names.
//
// Two questions in order, and the order is the point: the digest the scope check
// compares against only means anything once a signature has been established
// over the claim carrying it.
func (s *Service) verdict(presented *sdjwt.SDJWT, credential generated.PaymentCredential) error {
	mandate, err := s.Payments.VerifyPayment(presented)
	if err != nil {
		return err
	}
	return s.Rules.VerifyCredential(credential, mandate.CheckoutHash)
}
