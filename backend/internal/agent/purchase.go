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

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/evidence"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
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
// is signed, and a struct this package decoded is not evidence of anything.
// Evidence, below, assembles the dispute bundle out of these tokens unchanged.
type Receipt struct {
	From  string
	Token string
}

// Client runs purchases.
type Client struct {
	Endpoints Endpoints
	// HTTP is the client to use; nil means http.DefaultClient. Whatever it is,
	// requests go out through transport.Correlating, so no call site here can
	// drop the correlation ID by forgetting a header.
	HTTP *http.Client
	// Events records the moments this role owns: presenting a mandate to a
	// verifier. Optional — a nil Emitter records nothing, which is what a unit
	// test wants.
	//
	// The agent emits nothing about verdicts, and that absence is the design.
	// It is the party with the least authority in the protocol, so an agent
	// reporting that a mandate was verified would be reporting somebody else's
	// decision as its own — and the event log is what the three-lane view
	// teaches from.
	Events *obs.Emitter
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

	// A transaction begins here — ADR 0003 calls this the entry point, and it is
	// before any HTTP request exists to adopt a header from, so this is where
	// the identifier has to be minted. Ensure rather than mint: a caller that
	// already has one, as cmd/agent does when it quotes before buying, keeps it,
	// because no hop regenerates the value.
	//
	// The error is deliberately dropped. EnsureCorrelationID returns the context
	// unchanged when its entropy source fails, so the purchase proceeds without
	// a label rather than failing over one — the same choice the correlation
	// middleware makes, for the same reason.
	ctx, _, _ = obs.EnsureCorrelationID(ctx, nil)

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
	// Before the call rather than after it. On the happy path the two orders are
	// indistinguishable; where they differ is a hop that never lands, and a log
	// showing a presentation with no verdict under it is the true shape of that
	// failure. Emitting afterwards would show nothing at all.
	c.Events.Emit(ctx, obs.KindMandatePresented,
		"Payment Mandate presented to the Credential Provider")

	body := map[string]any{"mandate": p.PaymentMandate}
	err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.CredProvider, "/")+"/credential", body, &out)

	// The receipt is collected before the error is acted on, and that ordering
	// is the point of the whole exercise. A refusal answers with one, and an
	// agent that returned early on the status would drop the only signed
	// account of why it was refused.
	p.keep(fromCredProvider, out.Receipt)

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
	// One event for the Checkout Mandate, which is the one the merchant will
	// decide about. The Payment Mandate travels in the same body and is not
	// presented to the merchant for a verdict — the merchant passes it on, and
	// emits its own presentation when it does.
	c.Events.Emit(ctx, obs.KindMandatePresented, "Checkout Mandate presented to the merchant")

	body := map[string]any{
		"mandate":    p.CheckoutMandate,
		"checkout":   p.Offer,
		"payment":    p.PaymentMandate,
		"credential": p.Credential,
	}
	err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.Merchant, "/")+"/checkout", body, &out)

	p.keep(fromMerchant, out.Receipt)
	p.keep(fromMPP, out.PaymentReceipt)
	p.Settled = out.Settled

	if err != nil {
		return fmt.Errorf("presenting the purchase to the merchant: %w", err)
	}
	return nil
}

// Who signed each receipt this flow collects. Named because Evidence has to
// pick two of the three out again, and a bundle assembled by matching string
// literals would be one typo away from a dispute built on the wrong party's
// answer — with nothing failing, because the receipt would simply be absent.
//
// The constants prevent the typo and nothing else. Which of a party's answers
// a bundle carries when there is more than one is receipt's problem, below.
const (
	fromCredProvider = "credprovider"
	fromMerchant     = "merchant"
	fromMPP          = "mpp"
)

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

// Evidence assembles this purchase into the bundle a dispute is decided from.
//
// It decodes nothing and verifies nothing. Every field is a token exactly as it
// arrived, because the agent is the party with the least authority in the
// protocol and a bundle it had interpreted would be its own account of a
// transaction rather than the counterparties' signatures over one. The
// verification is internal/adapters/ap2.Dispute's, and the arbiter running it
// brings its own keys.
//
// The Payment Receipt is the processor's. Two parties answer the Payment Mandate
// in this flow — the Credential Provider when it funds the purchase, and the
// Merchant Payment Processor when it is asked to move the money — and both
// receipts are genuine answers to the same presentation. A bundle carries one,
// because the arbiter brings one key to check it with, so which one is a choice
// and this is it: the processor's is the answer that says whether money moved,
// which is the question a dispute is opened about. A purchase that never reached
// the processor has no complete bundle, and Bundle.Validate says so rather than
// quietly substituting the Credential Provider's answer to a different question.
//
// An incomplete purchase yields an incomplete bundle rather than an error. The
// gaps are the assembly's finding, they are all reported at once, and a caller
// holding a half-run purchase is better served by a bundle it can hand to
// Validate than by a second error path here.
//
// A retried step leaves a party having answered more than once, and the bundle
// carries the latest answer from each. **The invariant is that the bundle
// describes the purchase as of its last attempt, which is the same thing
// Settled describes** — the two cannot disagree, and a bundle built from the
// first answer could: Settle a purchase whose credential the processor refuses,
// then Settle it again with a good one, and a first-answer bundle would be a
// verifying chain saying the money did not move while Settled says it did.
func (p Purchase) Evidence() evidence.Bundle {
	return evidence.Bundle{
		Checkout:        p.Offer,
		CheckoutMandate: p.CheckoutMandate,
		CheckoutReceipt: p.receipt(fromMerchant),
		PaymentMandate:  p.PaymentMandate,
		PaymentReceipt:  p.receipt(fromMPP),
	}
}

// receipt returns the latest token a named party signed, or the empty string.
//
// The **latest**, and the direction is the whole of it. keep appends and never
// replaces, because Receipts is the evidence trail and an agent that retried
// after a refusal must not be able to delete the signed refusal — that is the
// fact AP2 makes the rejection receipt mandatory to produce. So the trail keeps
// every answer, and the bundle takes the one that describes the purchase now.
//
// Reading the first instead is a verifying chain that lies. Settle a purchase
// whose credential the processor refuses, settle it again with a good one, and
// the receipts are [credprovider, merchant, mpp, merchant, mpp]: the money moved,
// Settled is true, and a first-answer bundle produces five links that all verify
// over a signed statement that it did not. Nothing in the chain could catch it,
// because every artefact in it is genuine.
func (p Purchase) receipt(from string) string {
	latest := ""
	for _, r := range p.Receipts {
		if r.From == from {
			latest = r.Token
		}
	}
	return latest
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

	// transport.Correlating rather than a header set here, and that is not
	// interchangeable: "no hop drops the identifier" has to hold at call sites
	// written later as well as at these four, and a client built with the
	// round-tripper cannot forget where a call site can.
	resp, err := transport.Correlating(c.HTTP).Do(req)
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
