package merchant

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
	//
	// **settled false with no receipt is not a refusal and an implementation
	// must not report one.** A refusal is a verdict, and AP2 has a verdict
	// answered with a receipt, so the two values together are what say the
	// processor declined; either alone is the zero value of a struct nobody
	// filled in. An implementation that could not obtain a verdict returns an
	// error — see ErrSettlementInFlight for the case that made the difference
	// matter, and HTTPProcessor.present for what it costs to get this wrong.
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

// ErrSettlementInFlight means the processor is already settling this exact
// payment under this exact key, and has not finished.
//
// It is the processor's transport.Idempotency answering idempotency_in_flight,
// which is not a refusal and was reported as one until issue #232 — the middle
// of a settlement rendered to the buyer as a settled decline.
//
// **Nothing about money is at risk when it arrives**, which is why it took an
// architect review to find. present derives its key from the URL and the body,
// so the attempt already running under that key is *this* settlement; the
// processor answering this way is the guarantee working, not failing. What was
// wrong is only what the buyer was told, and telling a buyer their payment was
// declined when it may yet go through is the failure this project's whole error
// vocabulary exists to prevent.
//
// A sentinel rather than one more unexported wrapped error, because it is the
// one answer on this hop whose remedy is "present the identical request again":
// everything else present can fail on has to change first. Nothing in this
// package branches on it today — settle answers verifier_unavailable to every
// failure to reach the processor, and correctly, since none of them is a verdict
// about the buyer's mandate — so this is what lets a caller that needs the
// distinction have it without parsing a sentence.
var ErrSettlementInFlight = errors.New(
	"merchant: the processor is already settling this payment under the key it was presented under")

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
// A refusal is not a transport failure, and it carries a receipt. Returning an
// error on the status would discard the processor's signed account of why it
// would not move the money — which is the one thing a merchant will want when
// the customer asks.
//
// That sentence used to read "a 4xx is a refusal", and the correction is issue
// #232: the receipt is what makes an answer a refusal, and it is one of the four
// non-2xx statuses under 500 this processor can send that carries one. 422 does;
// 400, 409 and 413 are Problem Details and carry nothing. See present.
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
// would be distinguishing it from nothing.
//
// Both names are checkable in this tree rather than agreed in a conversation,
// which is the only kind of claim worth making about somebody else's wire
// format: internal/roles/mpp's settlement tags a Chain field as chain and a
// Nonce field as nonce, and internal/roles/credprovider tags the same pair. So a
// reader meets one name twice rather than two names once, and can confirm it
// with a grep instead of taking this comment's word for it.
//
// That was not true when this method was written. The chain branch on both
// payment roles arrived with #120, and until it did, a chain sent here was
// answered with a refusal rather than settled — which is worth recording
// because the two halves of this hop were written on separate branches, and a
// member name is exactly the kind of thing that agrees in a conversation and
// disagrees in the code. TestTheProcessorIsSentTheMembersItReads is what keeps
// this side from drifting now that both exist.
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
// answer. Two copies would be two places for the rules below to drift — and
// there are two of them now rather than one, which is the second argument for
// this having stayed one function.
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

	// Both shapes in one decode, because both are JSON and which one arrived is
	// what this function has to work out. receipt and settled are the outcome
	// internal/roles/mpp answers a verdict with; code and detail are RFC 9457's,
	// which is what internal/platform/problem writes for everything that is not
	// one. A body carrying neither pair is neither, and falls out below.
	var out struct {
		Receipt string              `json:"receipt"`
		Settled bool                `json:"settled"`
		Code    generated.ErrorCode `json:"code"`
		Detail  string              `json:"detail"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return "", false, fmt.Errorf("decoding the processor's answer: %w", err)
	}

	// 5xx is the processor failing rather than refusing, and there is no signed
	// answer in it to pass on.
	if resp.StatusCode >= 500 {
		return "", false, fmt.Errorf("the processor answered %s", resp.Status)
	}

	// And below 500 the same question has to be asked, which it was not until
	// issue #232. A refusal is a *verdict*, and this processor answers a verdict
	// with its signed receipt — every other answer it can give is Problem Details
	// with no receipt in it, which decodes to an empty receipt and settled false.
	// That is byte for byte what a genuine decline looks like to the code below,
	// so the merchant was reporting "the processor declined this payment" for an
	// answer that said no such thing.
	//
	// **Which way the default goes is the whole of the fix.** The test is what
	// came back rather than which status it came back under: an answer that
	// neither settles nor carries the processor's account of why it did not is
	// the processor having reached no conclusion this merchant may repeat. A list
	// of statuses to exclude would default the other way and would be wrong again
	// the first time the processor grew an answer nobody here had thought of —
	// which is the reasoning #206 settled the same shape on one role along.
	//
	// settled is read as well as the receipt, so that a processor answering that
	// the money moved is never turned into a failure by this branch. Money having
	// moved with no receipt beside it is a different complaint from #232's, and
	// refusing to acknowledge a settlement is not the way to make it.
	if !out.Settled && out.Receipt == "" {
		if out.Code == generated.ErrorCodeIdempotencyInFlight {
			// Named apart from the rest because the caller's move differs: this
			// says the identical settlement is already running under the identical
			// key, so presenting it again is what obtains the processor's answer
			// rather than a second settlement. Every other answer here is
			// something that has to change first.
			return "", false, fmt.Errorf("%w: %s answered %s: %s",
				ErrSettlementInFlight, url, resp.Status, out.Detail)
		}
		return "", false, fmt.Errorf(
			"the processor answered %s with no verdict in it (%s): %s", resp.Status, out.Code, out.Detail)
	}
	return out.Receipt, out.Settled, nil
}
