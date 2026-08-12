// Package agent is the Shopping Agent: the role that assembles a purchase and
// carries it between the other four.
//
// It is the only role an LLM may ever appear in, and it does not appear here.
// internal/agent/interpret is the one permitted home for one, and this file's
// Human Present flow has no interpretation step at all — the user approves the
// closed mandates directly, so there is nothing to infer and no constraint for
// anybody to evaluate.
//
// The Human Not Present flow is in authorise.go, tracker.go, chain.go and
// watch.go. It has exactly one interpretation step, in Client.Authorise, which
// runs once before the user signs; nothing in the watch loop or in an attempt
// calls an interpreter, and internal/agent imports no constraint evaluator at
// all — TestTheAgentCannotReachAConstraintEvaluator is what keeps that true.
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
	"sync"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
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
//
// **Not safe to copy once used.** It memoises the merchant's shelves behind a
// mutex — see Client.shelves — so a copy taken after the first fetch would carry
// a copy of that lock. Every holder in this module already keeps a *Client;
// nothing here takes one by value.
type Client struct {
	Endpoints Endpoints
	// HTTP is the client to use; nil means a client with counterpartyTimeout on
	// it — see there, and note that it is *not* http.DefaultClient, which has no
	// timeout at all. Whatever it is, requests go out through
	// transport.Correlating, so no call site here can drop the correlation ID by
	// forgetting a header.
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

	// shelvesMu guards the two fields below, which memoise the one optional
	// fetch this client repeats — see Client.shelves.
	shelvesMu     sync.Mutex
	shelvesKnown  bool
	shelvesCached interpret.Shelves
}

// counterpartyTimeout bounds one call to a counterparty when the caller supplied
// no http.Client of its own.
//
// The value it replaces is **none at all**. `HTTP` nil meant http.DefaultClient,
// whose Timeout is zero, so every call in this file — the merchant's search, the
// merchant's shelves, the Trusted Surface's /authorise, the four verifiers a
// purchase passes through — would wait for as long as a counterparty held the
// connection open. Issue #299 measured what that looks like from a browser:
// GET /shelves in front of a model call in front of GET /search, with nothing on
// screen and no ceiling anywhere but the model's own 60 seconds.
//
// Fifteen seconds, chosen against the widest answer any of them gives rather
// than measured as a latency. maxResponse's comment records that one: 124.5 KiB
// of search results over loopback, which is milliseconds — and it was 430.4 KiB
// when this constant was chosen, which issue #300 took to a quarter of the size
// without moving anything this number was weighed against. Nothing in this project
// legitimately takes seconds — the roles are in-process mocks over a local
// socket — so this is far enough above every real answer to never fire on one,
// and far enough below a person's patience that a counterparty which has stopped
// answering is reported as such rather than waited on.
//
// **It is a floor and not a policy**, on interpret.geminiTimeout's own terms: a
// caller whose context expires sooner still wins, because http.Client.Timeout and
// the request's context are both live and the earlier one ends the call.
const counterpartyTimeout = 15 * time.Second

// timedOut is the client this package uses when a caller supplied none.
//
// A package-level value rather than one built per call, so that connection
// pooling behaves as it did: transport.Correlating copies whatever it is handed
// and replaces only the Transport, and a fresh http.Client per request would
// otherwise hand it a fresh zero Transport each time.
var timedOut = &http.Client{Timeout: counterpartyTimeout}

// httpClient is what this client's calls actually go out on.
//
// The nil case is the one that matters: it used to fall through to
// http.DefaultClient inside transport.Correlating, which is where the missing
// timeout came from. Every caller in this repository leaves HTTP nil.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return timedOut
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
	//
	// p.PaymentMandate is a bare closed SD-JWT, not a chain — the Trusted
	// Surface signed it for the user under Human Present, rather than the agent
	// delegating it under Human Not Present — so the digest comes off it via
	// PaymentDigestOfMandate rather than internal/agent/chain.go's
	// PaymentDigestOf. See PaymentDigestOfMandate's doc comment for why reading
	// it unverified is still sound: nothing here trusts the value for anything
	// but this event.
	// p.Price is the amount this agent held throughout — the merchant's own
	// quote from Client.Quote, unchanged, and the same value already signed
	// into the Payment Mandate being presented here. Nothing is read back out
	// of the mandate the way the digest is: PaymentAmount is never recomputed
	// the way checkout_hash is, so the value this agent quoted is the value it
	// presented, and using it directly is "the amount it held" rather than a
	// shortcut.
	c.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.PaymentDigestOfMandate(p.PaymentMandate))),
		obs.KindMandatePresented, "Payment Mandate presented to the Credential Provider",
		obs.WithAmount(p.Price),
		// Closed. Under Human Present the user signed a mandate naming this
		// exact transaction, and a verifier is never handed anything else.
		obs.WithMandate(obs.MandatePayment, obs.MandateClosed))

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
	// One event, for the Checkout Mandate. The Payment Mandate travels in the
	// same body and the merchant does now reach a verdict on it — signature,
	// vct, the binding, and the price against the offer it made, per #88 — but
	// the agent is not the party to say so: it emits what it presented, and
	// every verdict in this flow is emitted by whoever reached it. The merchant
	// emits its own presentation again when it passes the mandate to the
	// processor, which is the hop the agent has no part in.
	//
	// CheckoutDigestOfMandate, not chain.go's CheckoutDigestOf, for the reason
	// Fund's own comment gives: p.CheckoutMandate is a bare mandate the surface
	// signed, not a chain the agent signed.
	// p.Price, on the same footing Fund's own comment gives: the price this
	// agent quoted and is presenting, held unchanged since Client.Quote.
	c.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.CheckoutDigestOfMandate(p.CheckoutMandate))),
		obs.KindMandatePresented, "Checkout Mandate presented to the merchant",
		obs.WithAmount(p.Price),
		obs.WithMandate(obs.MandateCheckout, obs.MandateClosed))

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
	resp, err := transport.Correlating(c.httpClient()).Do(req)
	if err != nil {
		return fmt.Errorf("calling %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Decoded before the status is judged: a refusal carries the receipt, and
	// reading the body only on success would throw it away.
	//
	// transport.RefusingOver rather than io.LimitReader, and the difference is
	// the whole of issue #251: a limit that reports EOF at the cap hands a
	// counterparty's document on one byte shorter than it was sent, and the error
	// that eventually arrives — if one arrives at all — names the document rather
	// than the cap. See that function, and maxResponse below for how close the
	// widest answer this client actually receives has got.
	if into != nil {
		if err := json.NewDecoder(transport.RefusingOver(resp.Body, maxResponse)).Decode(into); err != nil {
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

// maxResponse is the largest answer this client will read from a counterparty.
//
// It is a refusal and not a truncation — see call above, and
// transport.RefusingOver for why the distinction is the point.
//
// # The measured worst case, so the next widening has something to compare against
//
// GET /search is the answer that fills this, and the widest one a merchant can
// give is a query every offer satisfies against the catalogue `make demo-live`
// assembles: `[{"op":"eq","field":"merchant.id","value":"air-serbia"}]` over the
// committed file plus the shop's whole stock, priced at 2026-08-12T12:00:00Z.
//
//	257 offers        63 from deploy/catalogue.json, 194 fetched from the shop
//	127,442 bytes     124.5 KiB on the wire
//	 17,109 bytes      16.7 KiB of it the shop's own photograph URLs — 13.4%
//	110,333 bytes     107.7 KiB is everything else, pictures removed
//	8.23×             headroom against this constant
//
// **Issue #300 is why that fourth line used to be the first three.** Until it,
// every fetched offer carried an inline `data:image/svg+xml;base64` mark, and
// the same query came to 440,753 bytes of which 322.3 KiB — 74.9% — was
// pictures, at 2.38× headroom. Pointing a fetched offer at the shop's CDN
// replaced roughly 1.7 KiB of base64 per row with an 88-byte URL, so the answer
// lost 71% of its size as a side effect of a decision taken for entirely
// different reasons. That is worth recording precisely because it was not the
// argument: #251 named leaving the pictures out of a search answer as one of the
// two ways to make this constant comfortable, and #300 did something adjacent to
// it by accident.
//
// The trend is what the numbers are written down for: #234 took the shop from 4
// offers to 64, #243 added 194 more. The offer count has not moved and will
// again, and the next thing to grow will not be the pictures.
//
// **Widening this constant is not the answer to the next measurement that gets
// close.** Issue #251 records the two that are — leaving the pictures out of a
// search answer, and paginating — and deferred both rather than rejecting them:
// pagination reaches this file's callers, `settle`'s first candidate and the
// console's proposal shape, and it collides with an agent that ranks a candidate
// set, which needs all of it. What #251 did was make the failure honest, because
// a cap that silently shortens a document is the wrong thing to be approaching
// whatever the number is.
//
// TestTheWidestAnswerAMerchantCanGiveFitsThisLimit assembles that same answer
// under `make check`, over the response recorded at shop/data/ — which was
// retaken from the live shop on 12 August 2026 and is byte for byte what it
// answered, so this is the live measurement rather than a stand-in for one.
//
// **It bounds those figures rather than pinning them**, and the difference is
// worth being exact about, because "the test re-derives every number above" is
// what this paragraph claimed until the architect review of #300 went and
// looked. What the test holds is a floor and a ceiling on the size, a ceiling
// on the pictures' share and a floor on the photographs' bytes — four bounds,
// each of which a mutation can redden. A byte count asserted exactly would fail
// on every price tick and every catalogue edit, which is a test people update
// without reading. So the rows above can drift within those bounds while the
// suite stays green: they are a reading taken on a date, and the bounds are what
// stops the reading becoming fiction rather than what keeps it current.
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
