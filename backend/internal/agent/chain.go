package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// One purchase attempt under Human Not Present: three challenges, four
// delegations, two presentations.
//
// # Why four, when there are two mandates
//
// sdjwt.Delegate writes the verifier's identifier into `aud` and
// sdjwt.VerifyChain compares it, so **a closed mandate is per verifier, not per
// transaction**. The Checkout Mandate is read by one party and the Payment
// Mandate by three, which is four delegations of two open mandates:
//
//	chain                       audience       nonce from     presented as
//	Checkout Mandate            merchant       merchant       mandate_chain
//	Payment Mandate             credprovider   credprovider   chain
//	Payment Mandate             merchant       merchant       payment_chain
//	Payment Mandate             processor      processor      processor_payment_chain
//
// Three challenges rather than four: the merchant issues one and checks it once,
// running it against both of the chains addressed to it — merchant.examineChain
// calls Challenge.Check(req.Nonce) and passes the same value to
// AuthoriseCheckoutChain and AuthorisePaymentChain. The processor's challenge
// travels beside its chain as processor_nonce and the merchant forwards it
// unread, because a key binding is checked by whoever issued the value it names.
//
// An agent that minted one payment chain and presented it three times is
// refused. Which verifier refuses is not symmetric and is worth knowing: reusing
// the Credential Provider's copy is *funded* by the Credential Provider — it is
// that chain's audience — and refused by the **merchant**, with a signed receipt
// naming key_binding_invalid.
//
// Both directions are tested rather than reasoned about, because a comment about
// which party refuses is exactly the kind that survives the code changing under
// it: TestAChainAddressedToOneVerifierIsRefusedByAnother presents the Credential
// Provider's chain to the merchant and the merchant's to the Credential
// Provider, and asserts a signed refusal from each.
//
// The receipt's *description* says the nonce does not match rather than naming
// the audience, because a chain minted for one verifier carries that verifier's
// challenge as well as its identifier and the nonce is compared first. That
// changes the sentence and nothing else — both comparisons wrap
// sdjwt.ErrKeyBindingInvalid and both arrive as key_binding_invalid, which is
// one claim either way: this proof was made for somebody else.

// closedMandateLifetime is how long the mandates this agent signs stay usable.
//
// Fifteen minutes, which is the same number the Trusted Surface puts on the
// closed mandates it signs, and chosen the same way: a closed mandate is bound
// to one transaction that is happening now, so a long life buys nothing and
// leaves a signed instruction lying about. It is written here rather than shared
// because it is this agent's own decision about its own signature — the surface
// answering it for the mandates *it* signs is a different party answering a
// different question, and a constant reached across the two would make one
// role's policy the other's by accident.
//
// Nothing checks it today. AuthoriseCheckoutChain and AuthorisePaymentChain run
// the window check against the *open* mandate, because that is the user's
// authorisation; the closed mandate's own exp is decoded into the canonical
// model by decodeCheckout and decodePayment and then never compared to a clock.
// It is set anyway, because a signed instruction with no expiry is worse than one whose
// expiry nobody currently checks, and because the day a verifier does check it
// the agent should not be the reason it fails.
const closedMandateLifetime = 15 * time.Minute

// Delegated is one Human Not Present purchase attempt: what was minted for it,
// what came back, and every receipt collected along the way.
//
// # Why this is not a Purchase
//
// Purchase carries one Checkout Mandate and one Payment Mandate, and its
// Evidence method assembles them into the bundle a dispute is decided from.
// Neither field can hold what this attempt has: there are three payment chains
// and no single "the Payment Mandate", and a delegation chain is not a closed
// mandate — ap2.Dispute runs sdjwt.Parse over those two fields, which a chain is
// not. Filling them with chains would produce a bundle that looks assembled and
// verifies nowhere, which is worse than not producing one.
//
// So the dispute path for a delegated purchase is not written yet, and saying so
// is better than a field that half works. What is shared is Receipt and the
// trail: every verifier answers with one, including the ones that refused, and
// that is as true here as it is under Human Present.
type Delegated struct {
	// ID names this attempt. It is a fingerprint of the four documents, so
	// re-presenting the same Delegated is the same attempt and re-minting is a
	// different one — which is exactly the distinction Tracker.Attempt turns on.
	ID string

	// Offer is the merchant's signed Checkout JWT, and Price is what it costs.
	// Both come from the quote this attempt was begun against.
	Offer string
	Price generated.Amount

	// CheckoutChain is the closed Checkout Mandate, addressed to the merchant.
	CheckoutChain string

	// The three payment chains, one per verifier that reads a Payment Mandate.
	CredentialChain string
	MerchantChain   string
	ProcessorChain  string

	// The challenges each chain's delegating hop is bound to. MerchantNonce
	// covers both of the chains addressed to the merchant.
	MerchantNonce     string
	CredProviderNonce string
	ProcessorNonce    string

	// Credential is what the Credential Provider returned. Nil until Fund has
	// succeeded, which is what lets a re-delivery skip a leg that already
	// answered.
	Credential *generated.PaymentCredential

	// Settled says whether money actually moved.
	Settled bool

	// Receipts, in the order they were collected. See Purchase.Receipts: the
	// refusals are in here too, and they are the point.
	Receipts []Receipt
}

// keep records a receipt, ignoring an absent one. Purchase.keep's counterpart,
// on the same terms and for the same reason.
func (d *Delegated) keep(from, token string) {
	if token == "" {
		return
	}
	d.Receipts = append(d.Receipts, Receipt{From: from, Token: token})
}

// Receipt returns the latest token a named party signed on this attempt, or the
// empty string.
//
// The latest rather than the first, on Purchase.receipt's reasoning: the trail
// keeps every answer because a signed refusal must not be deletable, and a
// reader asking what a party said about this attempt wants what it said last.
func (d *Delegated) Receipt(from string) string {
	latest := ""
	for _, r := range d.Receipts {
		if r.From == from {
			latest = r.Token
		}
	}
	return latest
}

// Delegate mints the four closed mandates one attempt needs.
//
// It presents nothing. Nothing here is an attempt in the rejection-receipt
// rule's sense — no verifier has been asked anything — which is why Watch.Run
// calls this outside Tracker.Attempt and Fund and Settle inside it.
//
// It does spend three challenges, and that is the one cost of the split. A
// delegation that is minted and never presented leaves three nonces used up at
// three verifiers, which costs nothing today because crypto.Challenger remembers
// nothing about what it issued.
//
// # What the agent decides here, and what it does not
//
// It decides nothing about money. Price is copied from the merchant's own quote
// into the closed Payment Mandate because the merchant refuses a payment that
// does not match the offer it made — see ap2.AmountMatches — and it is never
// compared against anything. The instrument comes from the Trusted Surface,
// which is the party that chose it; the payee's identifier and name are
// configuration, because generated.Merchant.Name is required by the schema and
// the mock merchant publishes no name anywhere. A real agent reads that from the
// merchant's own metadata.
//
// The one thing it does decide is which document each delegation is addressed
// to, and getting that wrong is a refusal rather than a silent widening.
func (w *Watch) Delegate(ctx context.Context, q Quote) (*Delegated, error) {
	if err := w.valid(); err != nil {
		return nil, err
	}
	if q.Checkout == "" {
		return nil, errors.New("agent: there is nothing to buy without the merchant's signed offer")
	}

	open, err := w.openMandates()
	if err != nil {
		return nil, err
	}

	// Three round trips, before anything is signed. A challenge is what proves
	// to each verifier that this delegation was made for it and now, and a
	// verifier that did not issue the value refuses before reading any mandate.
	merchantNonce, err := w.nonce(ctx, w.Client.Endpoints.Merchant, "merchant")
	if err != nil {
		return nil, err
	}
	credNonce, err := w.nonce(ctx, w.Client.Endpoints.CredProvider, "Credential Provider")
	if err != nil {
		return nil, err
	}
	processorNonce, err := w.nonce(ctx, w.Client.Endpoints.MPP, "Merchant Payment Processor")
	if err != nil {
		return nil, err
	}

	now := w.Clock.Now()
	expiry := now.Add(closedMandateLifetime)

	checkout := generated.CheckoutMandate{
		// checkout_hash is left out: DelegateCheckout recomputes it from this
		// document, the way every issuing path in the adapter does.
		Checkout:  &q.Checkout,
		IssuedAt:  &now,
		ExpiresAt: &expiry,
	}
	payment := generated.PaymentMandate{
		// The same: transaction_id is recomputed from the offer.
		Payee:             w.Merchant,
		PaymentAmount:     q.Price,
		PaymentInstrument: w.Authorisation.Instrument,
		IssuedAt:          &now,
		ExpiresAt:         &expiry,
	}

	checkoutChain, err := ap2.DelegateCheckout(ctx, w.Signer, open.checkout, checkout,
		sdjwt.KeyBinding{Nonce: merchantNonce, Audience: w.Merchant.ID, IssuedAt: now}, w.Blinder)
	if err != nil {
		return nil, fmt.Errorf("signing the closed Checkout Mandate for the merchant: %w", err)
	}
	w.Client.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.CheckoutDigestOf(checkoutChain.String()))), obs.KindMandateConstructed,
		"closed Checkout Mandate signed by the agent, under the open mandate the user signed")

	// One per verifier. The audience and the nonce are the only things that
	// differ between the three, and they are the whole reason there are three.
	credentialChain, err := w.delegatePayment(ctx, open.payment, payment, q.Checkout,
		w.CredProviderID, credNonce, now)
	if err != nil {
		return nil, err
	}
	merchantChain, err := w.delegatePayment(ctx, open.payment, payment, q.Checkout,
		w.Merchant.ID, merchantNonce, now)
	if err != nil {
		return nil, err
	}
	processorChain, err := w.delegatePayment(ctx, open.payment, payment, q.Checkout,
		w.ProcessorID, processorNonce, now)
	if err != nil {
		return nil, err
	}
	// Any one of the three carries the same digest — the same q.Checkout, the
	// same payment claims, the audience and the nonce are the only things that
	// differ between them — so the first one minted is as representative of
	// this attempt's Payment Mandate as the other two.
	w.Client.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.PaymentDigestOf(credentialChain))), obs.KindMandateConstructed,
		"closed Payment Mandate signed by the agent, once for each of the three verifiers that reads it")

	d := &Delegated{
		Offer:             q.Checkout,
		Price:             q.Price,
		CheckoutChain:     checkoutChain.String(),
		CredentialChain:   credentialChain,
		MerchantChain:     merchantChain,
		ProcessorChain:    processorChain,
		MerchantNonce:     merchantNonce,
		CredProviderNonce: credNonce,
		ProcessorNonce:    processorNonce,
	}
	// Over the four documents rather than over the quote, because what makes two
	// deliveries one attempt is that they present the same bytes. Two attempts
	// against the same quote carry different challenges and are genuinely two.
	d.ID = roles.Fingerprint(strings.Join([]string{
		d.CheckoutChain, d.CredentialChain, d.MerchantChain, d.ProcessorChain,
	}, " "))
	return d, nil
}

// delegatePayment signs one closed Payment Mandate, addressed to one verifier.
func (w *Watch) delegatePayment(
	ctx context.Context,
	open *sdjwt.SDJWT,
	m generated.PaymentMandate,
	checkoutJWT, audience, nonce string,
	now time.Time,
) (string, error) {
	chain, err := ap2.DelegatePayment(ctx, w.Signer, open, m, checkoutJWT,
		sdjwt.KeyBinding{Nonce: nonce, Audience: audience, IssuedAt: now}, w.Blinder)
	if err != nil {
		return "", fmt.Errorf("signing the closed Payment Mandate for %s: %w", audience, err)
	}
	return chain.String(), nil
}

// openPair is the two open mandates parsed back out of their compact
// serialisations.
//
// Parsed per attempt rather than once, because ap2.Minimise narrows a copy and
// sdjwt.Delegate refuses a mandate that already carries a key binding — so a
// shared value would be one edit away from an attempt inheriting the previous
// one's presentation. Parsing is cheap and the alternative is a bug nothing
// would catch until the second attempt.
type openPair struct {
	checkout *sdjwt.SDJWT
	payment  *sdjwt.SDJWT
}

func (w *Watch) openMandates() (openPair, error) {
	var out openPair

	checkout, err := sdjwt.Parse(w.Authorisation.OpenCheckoutMandate)
	if err != nil {
		return out, fmt.Errorf("reading the open Checkout Mandate the user signed: %w", err)
	}
	payment, err := sdjwt.Parse(w.Authorisation.OpenPaymentMandate)
	if err != nil {
		return out, fmt.Errorf("reading the open Payment Mandate the user signed: %w", err)
	}
	return openPair{checkout: checkout, payment: payment}, nil
}

// nonce asks one verifier for a challenge.
func (w *Watch) nonce(ctx context.Context, base, role string) (string, error) {
	var out struct {
		Nonce string `json:"nonce"`
	}
	url := strings.TrimSuffix(base, "/") + roles.NoncePath
	if err := w.Client.call(ctx, http.MethodGet, url, nil, &out); err != nil {
		return "", fmt.Errorf("asking the %s for a challenge: %w", role, err)
	}
	if out.Nonce == "" {
		return "", fmt.Errorf("the %s answered with no challenge", role)
	}
	return out.Nonce, nil
}

// Fund exchanges the delegated Payment Mandate for a credential scoped to this
// purchase.
//
// It is exported alongside Deliver for the reason Client.Fund is: the
// interesting refusals happen between the legs, and a flow with no seams can
// only be tested at its ends. It is also what makes
// TestTheCredentialProvidersReceiptDoesNotSpendTheMandate writable — that test
// asserts the state *between* Fund and Settle, which is exactly where the
// per-hop reading of the lifecycle machine would have spent the mandate.
//
// Calling it twice with the same Delegated is a re-delivery rather than a second
// purchase: the body is unchanged, so idempotencyKey derives the same header and
// the provider recognises the retry. Calling it after it has succeeded is a
// no-op, because a credential is already held.
func (w *Watch) Fund(ctx context.Context, d *Delegated) error {
	if d == nil {
		return errors.New("agent: nothing to fund")
	}
	if d.Credential != nil {
		return nil
	}

	var out struct {
		Receipt    string                       `json:"receipt"`
		Credential *generated.PaymentCredential `json:"credential"`
	}
	// Before the call rather than after it, on Client.Fund's reasoning: a log
	// showing a presentation with no verdict under it is the true shape of a hop
	// that never landed, and emitting afterwards would show nothing at all.
	w.Client.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.PaymentDigestOf(d.CredentialChain))), obs.KindMandatePresented,
		"delegated Payment Mandate presented to the Credential Provider")

	body := map[string]any{"chain": d.CredentialChain, "nonce": d.CredProviderNonce}
	err := w.Client.call(ctx, http.MethodPost,
		strings.TrimSuffix(w.Client.Endpoints.CredProvider, "/")+"/credential", body, &out)

	// The receipt is collected before the error is acted on. A refusal answers
	// with one, and returning early on the status would drop the only signed
	// account of why.
	d.keep(fromCredProvider, out.Receipt)

	if err != nil {
		return fmt.Errorf("asking the Credential Provider to fund the purchase: %w", err)
	}
	if out.Credential == nil {
		return fmt.Errorf("%w: the Credential Provider returned no credential", ErrRefused)
	}
	d.Credential = out.Credential
	return nil
}

// Settle presents the three documents the merchant needs and the credential.
//
// Three chains and two nonces, because the merchant is the party that initiates
// payment: it verifies the two addressed to it and forwards the third, with the
// challenge the processor issued, unread. The agent never speaks to the
// processor — see merchant.purchase.ProcessorPaymentChain for why that document
// cannot be the merchant's own copy forwarded.
//
// Two receipts can come back: the merchant's, about the mandates, and the
// processor's, about the money. They answer different questions and are signed
// by different parties.
func (w *Watch) Settle(ctx context.Context, d *Delegated) error {
	if d == nil {
		return errors.New("agent: nothing to settle")
	}
	if d.Credential == nil {
		return errors.New("agent: this purchase has no credential, so the merchant has nothing to be paid with")
	}

	var out struct {
		Receipt        string `json:"receipt"`
		PaymentReceipt string `json:"payment_receipt"`
		Settled        bool   `json:"settled"`
	}
	// One event, for the Checkout Mandate. The merchant reaches a verdict on the
	// payment side too, and emits its own presentation when it passes the third
	// chain to the processor — every verdict in this flow is emitted by whoever
	// reached it, and the agent emits only what it presented.
	w.Client.Events.Emit(obs.WithDigest(ctx, reportDigest(ap2.CheckoutDigestOf(d.CheckoutChain))), obs.KindMandatePresented,
		"delegated Checkout Mandate presented to the merchant")

	body := map[string]any{
		"mandate_chain":           d.CheckoutChain,
		"payment_chain":           d.MerchantChain,
		"processor_payment_chain": d.ProcessorChain,
		"nonce":                   d.MerchantNonce,
		"processor_nonce":         d.ProcessorNonce,
		"checkout":                d.Offer,
		"credential":              d.Credential,
	}
	err := w.Client.call(ctx, http.MethodPost,
		strings.TrimSuffix(w.Client.Endpoints.Merchant, "/")+"/checkout", body, &out)

	d.keep(fromMerchant, out.Receipt)
	d.keep(fromMPP, out.PaymentReceipt)
	d.Settled = out.Settled

	if err != nil {
		return fmt.Errorf("presenting the delegated purchase to the merchant: %w", err)
	}
	return nil
}

// Deliver presents an attempt: fund it, then settle it.
//
// Both legs present the *same* Payment Mandate — a different document each, one
// per verifier, but one mandate — which is why this is one attempt and not two.
// authz/lifecycle.go predicts the bug that follows from reading it the other
// way: a machine stepped per hop reaches StateSpent at the Credential Provider
// and refuses the merchant, killing the purchase after the credential has been
// issued.
//
// Re-delivering is calling this again with the same Delegated. Fund returns
// immediately when a credential is already held, so a delivery that failed at
// the merchant re-presents to the merchant alone, under the same idempotency key
// it used the first time.
func (w *Watch) Deliver(ctx context.Context, d *Delegated) error {
	if err := w.Fund(ctx, d); err != nil {
		return err
	}
	return w.Settle(ctx, d)
}

// verdictOf reads a delivery's outcome as the rejection-receipt rule sees it.
//
// ErrRefused is checked first and on its own: Client.call turns every 4xx into
// it, and every 4xx on this path carries a signed receipt saying why. That is a
// verifier having answered, which is what licenses the next attempt.
//
// Anything else that failed is a delivery nobody answered — a connection that
// did not open, a body that did not decode, a 5xx — and is deliberately not a
// rejection. Nothing licenses either event, so the attempt stays outstanding;
// see Tracker.Attempt.
//
// The last arm is unreachable against the roles in this repository: the merchant
// answers 422 when the processor refuses, which Client.call has already turned
// into ErrRefused. It is written out anyway rather than folded into the
// accepted arm, because "no error and no money" must never read as a purchase
// that went through.
func verdictOf(d *Delegated, err error) Verdict {
	switch {
	case errors.Is(err, ErrRefused):
		return VerdictRejected
	case err != nil:
		return VerdictUnanswered
	case d != nil && d.Settled:
		return VerdictAccepted
	default:
		return VerdictRejected
	}
}
