// Package surface is the mock Trusted Surface.
//
// AP2 requires this role to be non-agentic: no LLM, ever, in the thing that
// shows a user what they are about to authorise and takes their signature. The
// reason is direct — a surface that could be talked into misdescribing a
// purchase is a surface whose signature means nothing.
//
// This package keeps that promise structurally rather than carefully. It
// imports nothing that reaches internal/agent, and
// TestTheTrustedSurfaceCannotReachAnInterpreter walks the transitive import
// graph to prove it. A comment asking future maintainers not to import an
// interpreter is a comment; the test is what makes the mistake fail.
//
// What it shows is deliberately dull: the fields of the mandate, as they will be
// signed. There is no summarisation step, because a summary is a place for a
// description to drift from what the signature covers.
package surface

import (
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

// Service is the mock Trusted Surface.
//
// It holds the user's key. In Human Present mode this is the role that signs
// the closed mandates, which is what makes the whole flow the user's decision
// rather than the agent's.
type Service struct {
	// Signer holds the user's key.
	Signer authz.Signer
	// Keys publishes the public half, so a merchant can check what the user
	// signed without having met them before.
	Keys authz.KeySetPublisher
	// Clock stamps the mandates.
	Clock authz.Clock
	// Blinder decides what may be withheld from which verifier.
	Blinder *sdjwt.Blinder
}

// approval is what POST /approve takes: the purchase, exactly as it will be
// signed.
//
// The agent assembles this and the surface signs it. The surface does not
// improve it, summarise it or fill anything in — every field here ends up under
// the user's signature, so a surface that altered one would be signing something
// the user was not shown.
type approval struct {
	// Checkout is the merchant-signed offer this purchase is for.
	Checkout string `json:"checkout"`
	// Payment is the payment side of the same purchase. Its checkout_hash is
	// ignored and recomputed from Checkout, as IssuePayment always does.
	Payment generated.PaymentMandate `json:"payment"`
}

// approved is what comes back: the two closed mandates, signed by the user.
type approved struct {
	CheckoutMandate string `json:"checkout_mandate"`
	PaymentMandate  string `json:"payment_mandate"`
}

// Handler returns the surface's routes.
func (s *Service) Handler() (http.Handler, error) {
	if s.Signer == nil || s.Keys == nil || s.Clock == nil || s.Blinder == nil {
		return nil, errors.New("surface: a Service needs a signer, a key set, a clock and a blinder")
	}

	mux := http.NewServeMux()
	mux.Handle("GET "+roles.JWKSPath, roles.JWKS(s.Keys))
	mux.HandleFunc("POST /approve", s.approve)
	return roles.Middleware(s.Clock, mux)
}

// approve signs the closed mandates.
//
// Both, in one call, because they are one decision. A user who approved the
// purchase and not the payment for it has approved nothing usable, and two
// endpoints would invite exactly that state.
//
// This is Human Present mode: the user signs the closed mandates directly, so
// there is no open mandate and no constraint for anybody to evaluate. Human Not
// Present is #15, where the agent signs and the open mandate is what a verifier
// checks it against.
func (s *Service) approve(w http.ResponseWriter, r *http.Request) {
	var req approval
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if req.Checkout == "" {
		roles.Fail(w, generated.ErrorCodeRequestMalformed,
			"there is nothing to approve without the merchant's offer")
		return
	}

	checkout := generated.CheckoutMandate{Checkout: &req.Checkout}
	stamp(s.Clock, &checkout.IssuedAt, &checkout.ExpiresAt)

	signedCheckout, err := ap2.IssueCheckout(r.Context(), s.Signer, checkout, s.Blinder)
	if err != nil {
		reject(w, "signing the Checkout Mandate", err)
		return
	}

	payment := req.Payment
	stamp(s.Clock, &payment.IssuedAt, &payment.ExpiresAt)

	signedPayment, err := ap2.IssuePayment(r.Context(), s.Signer, payment, req.Checkout, s.Blinder)
	if err != nil {
		reject(w, "signing the Payment Mandate", err)
		return
	}

	roles.OK(w, http.StatusOK, approved{
		CheckoutMandate: signedCheckout.String(),
		PaymentMandate:  signedPayment.String(),
	})
}

// mandateLifetime is how long a signed closed mandate stays usable.
//
// Short on purpose. A closed mandate is bound to one transaction that is
// happening now, so a long life buys nothing and leaves a signed instruction
// lying around. AP2 says the same of open mandates: exp should be the smallest
// value that lets the agent finish.
const mandateLifetime = 15 * time.Minute

func stamp(clk authz.Clock, issuedAt, expiresAt **time.Time) {
	now := clk.Now()
	expiry := now.Add(mandateLifetime)
	*issuedAt = &now
	*expiresAt = &expiry
}

// reject answers a signing failure.
//
// No receipt here, and that is not an oversight. A receipt is a verifier's
// answer to a mandate somebody presented; this role is the one *making* the
// mandate, so there is nothing yet to reference and nobody to hold to it.
func reject(w http.ResponseWriter, doing string, err error) {
	code := ap2.CodeOf(err)
	if code == "" {
		code = generated.ErrorCodeVerifierUnavailable
	}
	roles.Fail(w, code, fmt.Sprintf("%s: %v", doing, err))
}
