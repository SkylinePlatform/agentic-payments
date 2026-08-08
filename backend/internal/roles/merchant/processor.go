package merchant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// Processor is the Merchant Payment Processor, as the merchant sees it.
//
// An interface because AP2 has the *merchant* initiate payment, so this call is
// the merchant's — and a merchant that constructed its processor inline could
// not be tested without one listening, nor pointed at a different one. The same
// reasoning that makes the verification rules injectable applies here: the
// party is a dependency, not an implementation detail.
// # Two methods, and the second is not a wider version of the first
//
// The Human Present and Human Not Present legs are separate methods here for
// the reason ap2 declares CheckoutVerifier and CheckoutChainVerifier as
// separate interfaces rather than growing one an optional argument: a single
// entry point taking whichever string the merchant happens to hold is one where
// the receiving end has to work out which shape arrived, and working it out
// means guessing from the bytes. purchase, on this merchant's own endpoint,
// keeps the same promise in the other direction — named fields, never a search
// for the "~~" a chain happens to contain — and an interface that undid it here
// would leave the promise kept on the way in and broken on the way out.
//
// The merchant reads neither presentation. It is the audience of neither the
// processor's chain nor the processor's nonce, so it could establish nothing by
// parsing either, and the method name is what carries the shape.
type Processor interface {
	// InitiatePayment presents the payment side of a purchase and returns the
	// processor's signed answer. A refusal is not an error — the receipt is the
	// answer either way, and settled is what says which it was.
	InitiatePayment(
		ctx context.Context,
		paymentMandate string,
		credential generated.PaymentCredential,
	) (receipt string, settled bool, err error)

	// InitiatePaymentChain is the same call for a Human Not Present purchase,
	// where what is presented is a delegation chain: the open Payment Mandate
	// the user signed and the closed one the agent signed under it, addressed to
	// the processor.
	//
	// nonce is the challenge that chain's delegating hop is bound to, and it is
	// the *processor's* rather than this merchant's. A delegation is a key
	// binding, and a key binding is checked by the verifier that issued the
	// value it names — so the agent fetches this one from the processor's own
	// GET /nonce and hands it here beside the chain it belongs to. The merchant
	// forwards it unread for exactly the reason it forwards the chain unread: it
	// is not the party that can say whether the challenge is good.
	//
	// It is a parameter rather than something the merchant supplies, because
	// there is nothing correct it could supply. Passing its own nonce would be
	// presenting the processor a value the processor never issued, and minting a
	// fresh one would be inventing a challenge nobody asked for.
	InitiatePaymentChain(
		ctx context.Context,
		paymentChain string,
		nonce string,
		credential generated.PaymentCredential,
	) (receipt string, settled bool, err error)
}

// HTTPProcessor talks to a Merchant Payment Processor over HTTP.
type HTTPProcessor struct {
	// Base is the processor's root, e.g. "http://localhost:8083".
	Base string
	// Client is the HTTP client to use. Zero means http.DefaultClient. Whatever
	// it is, requests go out through transport.Correlating — a caller supplying
	// its own client does not have to remember the header.
	Client *http.Client
}

var _ Processor = (*HTTPProcessor)(nil)

// InitiatePayment sends the Payment Mandate and the credential to the processor.
//
// A 4xx is a refusal rather than a transport failure, and it carries a receipt.
// Returning an error on the status would discard the processor's signed account
// of why it would not move the money — which is the one thing a merchant will
// want when the customer asks.
func (p *HTTPProcessor) InitiatePayment(
	ctx context.Context,
	paymentMandate string,
	credential generated.PaymentCredential,
) (string, bool, error) {
	return p.present(ctx, map[string]any{
		"mandate":    paymentMandate,
		"credential": credential,
	})
}

// InitiatePaymentChain sends the delegated Payment Mandate, the challenge it is
// bound to and the credential to the processor.
//
// The chain travels under its own member, never under "mandate", which is the
// same promise merchant.purchase makes on its own endpoint: a role must not have
// to look inside a string to learn which shape it was handed.
//
// The member is "chain" and not "payment_chain", and the asymmetry with this
// merchant's own request shape is deliberate rather than an inconsistency. Three
// distinct documents arrive here together and have to be told apart, so they are
// named for which is which; the processor receives exactly one and a qualifier
// would be distinguishing it from nothing. "chain" is also what the Credential
// Provider reads, so a reader meets one name twice rather than two names once.
//
// **The mock Merchant Payment Processor does not read either member yet.** Its
// POST /payment takes "mandate" and parses it with sdjwt.Parse, so a chain sent
// today is answered with a refusal rather than settled. That is slice 6's to
// close — issue #120 adds the chain branch to mpp.settle, reading "chain" and
// "nonce", which is where both names come from — and it is stated here rather
// than left for a reader to discover, because between now and then this method
// is correct and the round trip it belongs to is not complete.
func (p *HTTPProcessor) InitiatePaymentChain(
	ctx context.Context,
	paymentChain string,
	nonce string,
	credential generated.PaymentCredential,
) (string, bool, error) {
	return p.present(ctx, map[string]any{
		"chain":      paymentChain,
		"nonce":      nonce,
		"credential": credential,
	})
}

// present posts one settlement body and reads the processor's answer.
//
// Shared by the two entry points because nothing about the call differs between
// them except which member carries the presentation: same path, same
// idempotency key derivation, same correlating client, same reading of the
// answer. Two copies would be two places for the 5xx rule below to drift.
func (p *HTTPProcessor) present(
	ctx context.Context, payload map[string]any,
) (string, bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", false, fmt.Errorf("encoding the settlement: %w", err)
	}

	url := strings.TrimSuffix(p.Base, "/") + "/payment"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", false, fmt.Errorf("building the settlement request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", roles.Fingerprint(url+" "+string(body)))

	// Through the correlating client, so the processor's verdict joins the group
	// the mandate that caused it belongs to. This hop is the one where losing
	// the identifier would be least visible and most costly: it is the only leg
	// the agent has no part in, so nothing else on the path would notice that a
	// transaction had quietly become two.
	resp, err := transport.Correlating(p.Client).Do(req)
	if err != nil {
		return "", false, fmt.Errorf("calling the processor at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var out struct {
		Receipt string `json:"receipt"`
		Settled bool   `json:"settled"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding the processor's answer: %w", err)
	}

	// 5xx is the processor failing rather than refusing, and there is no signed
	// answer in it to pass on.
	if resp.StatusCode >= 500 {
		return "", false, fmt.Errorf("the processor answered %s", resp.Status)
	}
	return out.Receipt, out.Settled, nil
}
