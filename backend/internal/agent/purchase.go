// Package agent is the Shopping Agent: the role that assembles a purchase and
// carries it between the other four.
//
// It is the only role an LLM may ever appear in, and it does not appear here.
// internal/agent/interpret is the one permitted home for one, and the Human
// Present flow this package implements has no interpretation step at all — the
// user approves the closed mandates directly, so there is nothing to infer and
// no constraint for anybody to evaluate. That arrives with #15.
//
// What this package does is sequencing and nothing else. Every decision along
// the way belongs to somebody else: the merchant decides whether the mandate
// authorises the purchase, the Credential Provider whether to fund it, the
// processor whether the money is scoped to it. The agent is the party with the
// least authority in the protocol, and code that let it decide anything would be
// modelling the wrong thing.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// Endpoints are the counterparties a purchase passes through.
type Endpoints struct {
	Surface      string
	Merchant     string
	CredProvider string
	MPP          string
}

// Purchase is one run of the Human Present flow.
//
// The fields are filled in as the flow proceeds, so a caller that stops early —
// or a test asserting on a rejection — can see exactly how far it got. A flow
// that returned only its final answer would make every failure look the same.
type Purchase struct {
	// Offer is the merchant-signed Checkout JWT, and Price is what it costs.
	Offer string
	Price generated.Amount

	// The two closed mandates, signed by the user at the Trusted Surface.
	CheckoutMandate string
	PaymentMandate  string

	// Credential is what the Credential Provider returned.
	Credential *generated.PaymentCredential

	// Settled says whether money actually moved. A purchase can have a merchant
	// receipt saying the mandate was good and still not be settled, which is
	// why this is not implied by the absence of an error.
	Settled bool

	// Receipts, in the order they were collected. Every verifier answers with
	// one — including the ones that refused — so this is the evidence trail
	// whether or not the purchase completed.
	Receipts []Receipt
}

// Receipt is a signed answer, tagged with who gave it.
//
// The token is kept rather than the decoded form: a receipt's value is that it
// is signed, and a struct this package decoded is not evidence of anything. #18
// assembles disputes from the tokens.
type Receipt struct {
	From  string
	Token string
}

// Client runs purchases.
type Client struct {
	Endpoints Endpoints
	HTTP      *http.Client
}

// ErrRefused means a counterparty declined, and the purchase stopped there.
//
// It is a sentinel rather than a status code so that a caller can tell "the
// protocol said no" from "the network did". The receipt naming why is in the
// Purchase, because a refusal that lost its receipt would be the failure AP2
// forbids happening one layer up.
var ErrRefused = errors.New("agent: a counterparty refused the purchase")

// The four steps are exported as well as composed, because "an integration test
// covering each rejection point" is not writable against a single call: the
// interesting refusals happen between steps — an approval that expires before it
// is funded, a price that moves after the user has signed — and a flow with no
// seams can only be tested at its ends.
//
// Buy runs the Human Present flow end to end.
//
// The order is the specification's, and each step needs the one before it: the
// merchant's offer is what the user approves, the user's signature is what the
// Credential Provider funds, and the credential is what the merchant is paid
// with. There is no step here the agent could reorder without producing a
// mandate nobody asked for.
//
// A refusal at any point returns ErrRefused with the Purchase filled in as far
// as it got, receipts included. That is deliberate: the receipts are the reason
// the flow is worth running, and a rejection path that discarded them would
// leave exactly the dispute AP2 requires them to settle.
func (c *Client) Buy(ctx context.Context, from, to string, payment generated.PaymentMandate) (Purchase, error) {
	var p Purchase

	if err := c.Quote(ctx, from, to, &p); err != nil {
		return p, err
	}
	if err := c.Approve(ctx, payment, &p); err != nil {
		return p, err
	}
	if err := c.Fund(ctx, &p); err != nil {
		return p, err
	}
	if err := c.Settle(ctx, &p); err != nil {
		return p, err
	}
	return p, nil
}

// quote asks the merchant what the route costs now.
func (c *Client) Quote(ctx context.Context, from, to string, p *Purchase) error {
	var out struct {
		Checkout string           `json:"checkout"`
		Price    generated.Amount `json:"price"`
	}
	url := fmt.Sprintf("%s/checkout?from=%s&to=%s", strings.TrimSuffix(c.Endpoints.Merchant, "/"), from, to)
	if err := c.call(ctx, http.MethodGet, url, nil, &out); err != nil {
		return fmt.Errorf("asking the merchant for a price: %w", err)
	}

	p.Offer = out.Checkout
	p.Price = out.Price
	return nil
}

// approve puts the purchase in front of the user and collects their signature.
//
// The agent sends the offer unchanged. It has no business editing what the user
// is about to sign, and the surface has no business improving it — which is why
// the surface signs exactly what it is shown.
func (c *Client) Approve(ctx context.Context, payment generated.PaymentMandate, p *Purchase) error {
	var out struct {
		CheckoutMandate string `json:"checkout_mandate"`
		PaymentMandate  string `json:"payment_mandate"`
	}
	body := map[string]any{"checkout": p.Offer, "payment": payment}
	if err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.Surface, "/")+"/approve", body, &out); err != nil {
		return fmt.Errorf("asking the user to approve: %w", err)
	}

	p.CheckoutMandate = out.CheckoutMandate
	p.PaymentMandate = out.PaymentMandate
	return nil
}

// fund exchanges the Payment Mandate for a credential scoped to this purchase.
func (c *Client) Fund(ctx context.Context, p *Purchase) error {
	var out struct {
		Receipt    string                       `json:"receipt"`
		Credential *generated.PaymentCredential `json:"credential"`
	}
	body := map[string]any{"mandate": p.PaymentMandate}
	err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.CredProvider, "/")+"/credential", body, &out)

	// The receipt is collected before the error is acted on, and that ordering
	// is the point of the whole exercise. A refusal answers with one, and an
	// agent that returned early on the status would drop the only signed
	// account of why it was refused.
	p.keep("credprovider", out.Receipt)

	if err != nil {
		return fmt.Errorf("asking the Credential Provider to fund the purchase: %w", err)
	}
	if out.Credential == nil {
		return fmt.Errorf("%w: the Credential Provider returned no credential", ErrRefused)
	}
	p.Credential = out.Credential
	return nil
}

// Settle presents the Checkout Mandate, the Payment Mandate and the credential
// to the merchant.
//
// All three, because the merchant is the party that initiates payment — AP2
// gives that leg to the merchant rather than the agent, so the agent never
// speaks to the processor and hands over what the merchant will need to.
//
// Two receipts can come back: the merchant's, about the mandate, and the
// processor's, about the money. They answer different questions and are signed
// by different parties, which is what lets a dispute tell "the mandate was bad"
// from "the mandate was fine and the money did not move".
func (c *Client) Settle(ctx context.Context, p *Purchase) error {
	var out struct {
		Receipt        string `json:"receipt"`
		PaymentReceipt string `json:"payment_receipt"`
		Settled        bool   `json:"settled"`
	}
	body := map[string]any{
		"mandate":    p.CheckoutMandate,
		"checkout":   p.Offer,
		"payment":    p.PaymentMandate,
		"credential": p.Credential,
	}
	err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.Merchant, "/")+"/checkout", body, &out)

	p.keep("merchant", out.Receipt)
	p.keep("mpp", out.PaymentReceipt)
	p.Settled = out.Settled

	if err != nil {
		return fmt.Errorf("presenting the purchase to the merchant: %w", err)
	}
	return nil
}

// keep records a receipt, ignoring an absent one.
//
// Absent rather than empty is the case that matters: the one answer with no
// receipt is a body that would not parse as a mandate, which is Problem Details
// by design because there is nothing to reference.
func (p *Purchase) keep(from, token string) {
	if token == "" {
		return
	}
	p.Receipts = append(p.Receipts, Receipt{From: from, Token: token})
}

// call sends a request and decodes the answer.
//
// A 4xx becomes ErrRefused rather than an error about HTTP, because at this
// layer the difference between "the merchant said no" and "the merchant was not
// there" is the whole distinction a caller acts on.
func (c *Client) call(ctx context.Context, method, url string, body, into any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding the request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		// Every state-changing call carries one, per the standing rule. It is
		// derived from the purchase rather than random so that a retry of the
		// same step is recognised as one — which is what the header is for.
		req.Header.Set("Idempotency-Key", idempotencyKey(method, url, body))
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Decoded before the status is judged: a refusal carries the receipt, and
	// reading the body only on success would throw it away.
	if into != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(into); err != nil {
			return fmt.Errorf("decoding the answer from %s: %w", url, err)
		}
	}

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("%w: %s answered %s", ErrRefused, url, resp.Status)
	default:
		return fmt.Errorf("%s answered %s", url, resp.Status)
	}
}

const maxResponse = 1 << 20

// idempotencyKey is a stable name for one step of one purchase.
func idempotencyKey(method, url string, body any) string {
	encoded, err := json.Marshal(body)
	if err != nil {
		// Unreachable: the same value marshalled successfully a few lines
		// above. Falling back to the URL keeps a retry recognisable rather
		// than failing the request over an impossibility.
		return method + " " + url
	}
	return roles.Fingerprint(method + " " + url + " " + string(encoded))
}
