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
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Service is the mock Credential Provider.
type Service struct {
	// ID names this provider in the receipts it signs.
	ID string
	// Rules decide whether a presented Payment Mandate is acceptable.
	Rules ap2.PaymentVerifier
	// Signer holds the provider's key.
	Signer authz.Signer
	// Keys publishes the public half.
	Keys authz.KeySetPublisher
	// Clock stamps receipts and credential expiry.
	Clock authz.Clock
	// CredentialLifetime is how long a minted credential stays redeemable.
	CredentialLifetime time.Duration
}

type request struct {
	// Mandate is the closed Payment Mandate in SD-JWT compact serialisation.
	Mandate string `json:"mandate"`
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
	return roles.Middleware(s.Clock, mux)
}

// fund verifies a Payment Mandate and mints a credential scoped to its checkout.
//
// This role never sees the checkout itself — AP2 sends it the Payment Mandate
// and nothing else, and a closed Payment Mandate carries transaction_id rather
// than the document. So it scopes the credential to the digest it was given,
// and the party that holds the document is the one that later proves the two
// agree. That is not a gap: it is why the digest is in three artefacts.
func (s *Service) fund(w http.ResponseWriter, r *http.Request) {
	var req request
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	presented, err := sdjwt.Parse(req.Mandate)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeMandateMalformed,
			fmt.Sprintf("the mandate is not a readable SD-JWT: %v", err))
		return
	}

	mandate, verdict := s.Rules.VerifyPayment(presented)

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

	if verdict != nil {
		// A refusal still gets a receipt, and gets no credential. Returning one
		// alongside a rejection would be the whole point of this role failing.
		roles.OK(w, http.StatusUnprocessableEntity, issued{Receipt: receipt})
		return
	}

	credential, err := s.mint(mandate.CheckoutHash)
	if err != nil {
		roles.Fail(w, generated.ErrorCodeVerifierUnavailable,
			fmt.Sprintf("minting the credential: %v", err))
		return
	}
	roles.OK(w, http.StatusOK, issued{Receipt: receipt, Credential: &credential})
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
