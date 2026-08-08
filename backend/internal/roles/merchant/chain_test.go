package merchant_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/merchant"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// POST /checkout under Human Not Present: what arrives is a delegation chain,
// and what makes it safe is the merchant evaluating the user's constraints
// against a purchase the *merchant* describes.
//
// These are the demo's middle beats. The agent watches a flight, presents a
// chain at $210 and is refused because the user's mandate said at most $200,
// and presents again at $189 and settles. The refusal is the interesting half
// and it has to come from here — an agent that declined to present its own
// purchase would prove nothing to anybody, and would leave no signed artefact
// saying a limit was enforced.
//
// The chains are minted with ap2.DelegateCheckout and ap2.DelegatePayment
// rather than through the agent, which does not exist yet (#121). That is not a
// shortcut: those two functions are the issuing half of the same file this
// merchant's verification reads, so what is presented here is what an agent will
// present rather than something assembled to look like it.

// chainMerchantID is who this merchant trades as, and it is also the audience
// every chain addressed to it carries. The two being one string is the whole
// point of ap2.MerchantRules.Audience: a delegation names the verifier it was
// made for, and one addressed elsewhere is refused here.
const chainMerchantID = demoMerchantID

// chainProcessorID is who the third chain is addressed to. This merchant never
// checks it — it is not the audience and could establish nothing by trying —
// so the value matters only in that it is *not* chainMerchantID, which is what
// makes forwarding the merchant's own copy a detectable mistake.
const chainProcessorID = "mock-payment-processor"

// processorNonce stands in for the challenge the agent fetched from the
// processor's own GET /nonce.
//
// A literal rather than a value from a Challenger, and that is the property
// under test rather than a shortcut: there is no processor in this fixture and
// the merchant is not the audience of this value, so nothing here can or should
// establish anything about it. What matters is only that it is visibly *not* the
// merchant's own nonce — which is what lets a test see the difference between a
// merchant forwarding what the agent gave it and one substituting a challenge of
// its own.
const processorNonce = "processor-issued-challenge"

// chainShop is a merchant that accepts delegated purchases, together with the
// three keys one purchase needs.
//
// Three, and each signs something different: the user signs the open mandates,
// the agent signs the two delegations under them, and the merchant signs its
// offers and its receipts. A fixture that used one key throughout would verify
// happily and prove nothing about the delegation, which is the one thing this
// file is about.
type chainShop struct {
	url       string
	merchant  authz.Verifier
	user      authz.Signer
	agent     authz.Signer
	agentKey  generated.PublicKey
	blinder   *sdjwt.Blinder
	processor *merchant.MockProcessor
	clock     *clock.Fake
	catalogue *merchant.Catalogue
}

// newChainShop stands up a merchant wired for both flows.
//
// Both, deliberately. A merchant built for the chain alone could pass every
// test in this file while having broken the Human Present path, and the point of
// #119 is that the two live in one service — so the fixture that proves the
// delegation works is the same fixture TestTheHumanPresentPathIsUntouched drives
// a directly signed purchase through.
func newChainShop(t *testing.T) chainShop {
	t.Helper()

	clk := clock.NewFake(base)
	party := func(name string) (authz.Signer, authz.Verifier, authz.KeySetPublisher) {
		store, err := crypto.NewStore(clk)
		require.NoError(t, err, "standing up the %s key store", name)
		ref, err := store.Generate(crypto.Slot(name), authz.ES256, name)
		require.NoError(t, err, "minting the %s key", name)
		signer, err := store.Signer(crypto.Slot(name))
		require.NoError(t, err)
		verifier, err := store.Resolve(t.Context(), ref)
		require.NoError(t, err)
		return signer, verifier, store
	}

	userSigner, userVerifier, _ := party("user")
	agentSigner, _, agentKeys := party("agent")
	shopSigner, shopVerifier, shopKeys := party("merchant")

	// The public half of the agent's key, in the canonical model's own form.
	// This is what the user endorses in the open mandate's cnf, and what
	// roles.AgentKey resolves back out of it at verification — the two halves of
	// the delegation, taken from one key store so that a test cannot accidentally
	// endorse a key the agent does not hold.
	agentKey, err := roles.PublicKey(t.Context(), agentKeys)
	require.NoError(t, err, "reading the agent's public key")

	inventory, err := merchant.NewDemoInventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the inventory")
	catalogue, err := merchant.NewDemoCatalogue(clk, chainMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err, "seeding the catalogue")
	challenge, err := crypto.NewChallenger(clk, roles.ChallengeTTL)
	require.NoError(t, err, "standing up the challenger")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	// One rule set per mandate, held behind both the direct interface and the
	// chain one — exactly as cmd/merchant wires it. Two literals would let this
	// fixture enforce one policy on the Human Present path and another on the
	// chain, which is the divergence the production wiring is shaped to prevent.
	checkoutRules := ap2.MerchantRules{
		Issuer:             userVerifier,
		Clock:              clk,
		AgentKey:           roles.AgentKey,
		Audience:           chainMerchantID,
		RequireConstrained: []string{"amount"},
	}
	paymentRules := ap2.CredentialProviderRules{
		Issuer:             userVerifier,
		Clock:              clk,
		AgentKey:           roles.AgentKey,
		Audience:           chainMerchantID,
		RequireConstrained: []string{"amount"},
	}

	processor := merchant.NewMockProcessor(t)
	svc := &merchant.Service{
		ID:            chainMerchantID,
		Inventory:     inventory,
		Catalogue:     catalogue,
		Rules:         checkoutRules,
		ChainRules:    checkoutRules,
		Payments:      paymentRules,
		ChainPayments: paymentRules,
		Signer:        shopSigner,
		Own:           shopVerifier,
		Keys:          shopKeys,
		Clock:         clk,
		Challenge:     challenge,
		Processor:     processor,
	}
	handler, err := svc.Handler()
	require.NoError(t, err, "building the merchant handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return chainShop{
		url:       srv.URL,
		merchant:  shopVerifier,
		user:      userSigner,
		agent:     agentSigner,
		agentKey:  agentKey,
		blinder:   blinder,
		processor: processor,
		clock:     clk,
		catalogue: catalogue,
	}
}

// nonce asks the merchant for a challenge to bind a delegation to.
//
// Fetched rather than invented, and that is the property GET /nonce exists for:
// a value the agent made up proves nothing, and crypto.Challenger authenticates
// its own issuance with a MAC rather than by remembering.
func (s chainShop) nonce(t *testing.T) string {
	t.Helper()

	var out struct {
		Nonce string `json:"nonce"`
	}
	require.Equal(t, http.StatusOK, s.get(t, roles.NoncePath, &out),
		"a merchant that verifies chains has to hand out the challenges they are bound to")
	require.NotEmpty(t, out.Nonce)
	return out.Nonce
}

// quoteItem asks the merchant to price a catalogue offer and returns the signed
// document and the line price.
func (s chainShop) quoteItem(t *testing.T, id string, quantity int) (string, generated.Amount) {
	t.Helper()

	query := url.Values{}
	query.Set(merchant.ItemParam, id)
	query.Set(merchant.QuantityParam, strconv.Itoa(quantity))

	var out struct {
		Checkout string           `json:"checkout"`
		Price    generated.Amount `json:"price"`
	}
	require.Equal(t, http.StatusOK, s.get(t, "/checkout?"+query.Encode(), &out),
		"the merchant would not quote %s", id)
	require.NotEmpty(t, out.Checkout)
	return out.Checkout, out.Price
}

// quoteRoute asks for the flight the inventory sells, which is the offer that
// names no item.
func (s chainShop) quoteRoute(t *testing.T) (string, generated.Amount) {
	t.Helper()

	var out struct {
		Checkout string           `json:"checkout"`
		Price    generated.Amount `json:"price"`
	}
	require.Equal(t, http.StatusOK, s.get(t, "/checkout?from=BEG&to=PMI", &out),
		"the route path is the Human Present flow and must keep working")
	require.NotEmpty(t, out.Checkout)
	return out.Checkout, out.Price
}

func (s chainShop) get(t *testing.T, path string, into any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.url+path, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "calling %s", path)
	defer func() { _ = resp.Body.Close() }()

	if into != nil {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(into), "decoding %s", path)
	}
	return resp.StatusCode
}

// present posts a purchase and returns the status and the body.
//
// key is a parameter rather than t.Name() because several tests here present
// twice, and the idempotency middleware remembers the answer to an unsafe
// request: a second call under one key would be answered with the first
// verdict, and a test asserting the second would be reading the first.
func (s chainShop) present(t *testing.T, key string, body map[string]any) (int, map[string]any) {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		s.url+"/checkout", strings.NewReader(string(encoded)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", t.Name()+"/"+key)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "presenting the purchase")
	defer func() { _ = resp.Body.Close() }()

	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// delegation is the pair of nonces one presentation is built from: the one the
// chains are signed against, and the one the request carries.
//
// They are separate fields because the nonce split turns on them differing. A
// value this merchant never issued is refused before any mandate is read; a
// value it did issue that disagrees with the one signed into the chain is a
// verification failure and is answered with a receipt.
type delegation struct {
	signed    string
	presented string
}

// sameNonce is the ordinary case: the chain is signed against the challenge the
// request carries.
func sameNonce(n string) delegation { return delegation{signed: n, presented: n} }

// chains mints the three delegations one Human Not Present purchase needs and
// returns the request body.
//
// Three, because sdjwt.Delegate writes the verifier's identifier into aud and
// sdjwt.VerifyChain compares it: a closed mandate is per verifier, not per
// transaction. Two of them are addressed to this merchant and one to the
// processor, and the merchant forwards the third without reading it.
func (s chainShop) chains(
	t *testing.T,
	constraints []generated.Constraint,
	offer string,
	price generated.Amount,
	n delegation,
) map[string]any {
	t.Helper()
	return s.chainsWith(t, constraints, n, purchasing{
		checkout: offer, paymentFor: offer, pays: price,
	})
}

// purchasing is what the three chains are made about, with the pieces that
// ordinarily agree separated out so a test can make one of them disagree.
//
// checkout is the offer the Checkout Mandate authorises and the merchant is
// shown; paymentFor is the offer the Payment Mandates are bound to; pays is what
// they say they pay. Ordinarily the first two are one document and the third is
// its price — which is exactly why they are three fields here: the merchant's
// last two checks exist to catch the cases where they are not.
type purchasing struct {
	checkout   string
	paymentFor string
	pays       generated.Amount
}

// chainsWith mints the three delegations for one purchasing and returns the
// request body.
func (s chainShop) chainsWith(
	t *testing.T,
	constraints []generated.Constraint,
	n delegation,
	about purchasing,
) map[string]any {
	t.Helper()

	openCheckout, err := ap2.IssueOpenCheckout(t.Context(), s.user, generated.OpenCheckoutMandate{
		AgentKey:    s.agentKey,
		Constraints: constraints,
	}, s.blinder)
	require.NoError(t, err, "the user signing the open Checkout Mandate")

	checkoutChain, err := ap2.DelegateCheckout(t.Context(), s.agent, openCheckout,
		generated.CheckoutMandate{Checkout: &about.checkout},
		sdjwt.KeyBinding{Nonce: n.signed, Audience: chainMerchantID, IssuedAt: s.clock.Now()},
		s.blinder)
	require.NoError(t, err, "the agent delegating the closed Checkout Mandate")

	payment := generated.PaymentMandate{
		Payee:             generated.Merchant{ID: chainMerchantID, Name: "Air Serbia"},
		PaymentAmount:     about.pays,
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}

	toMerchant := s.payChain(t, constraints, payment, about.paymentFor, chainMerchantID, n.signed)
	// Bound to the processor's challenge, not this merchant's. That is what
	// makes the pair the merchant forwards checkable: a merchant that sent its
	// own nonce along with this chain would be presenting the processor two
	// values that do not go together, and the processor would refuse a purchase
	// the merchant had already signed an acceptance for.
	toProcessor := s.payChain(t, constraints, payment, about.paymentFor,
		chainProcessorID, processorNonce)

	scope, err := sdjwt.SHA256.Digest(about.checkout)
	require.NoError(t, err, "scoping the credential to this checkout")

	return map[string]any{
		"mandate_chain":           checkoutChain.String(),
		"payment_chain":           toMerchant.String(),
		"processor_payment_chain": toProcessor.String(),
		"processor_nonce":         processorNonce,
		"nonce":                   n.presented,
		"checkout":                about.checkout,
		"credential": generated.PaymentCredential{
			Token:        "tok_delegated",
			CheckoutHash: scope,
		},
	}
}

// payChain mints one payment delegation for one audience.
func (s chainShop) payChain(
	t *testing.T,
	constraints []generated.Constraint,
	m generated.PaymentMandate,
	offer, audience, nonce string,
) *sdjwt.Chain {
	t.Helper()

	open, err := ap2.IssueOpenPayment(t.Context(), s.user, generated.OpenPaymentMandate{
		AgentKey:    s.agentKey,
		Constraints: constraints,
	}, s.blinder)
	require.NoError(t, err, "the user signing the open Payment Mandate")

	chain, err := ap2.DelegatePayment(t.Context(), s.agent, open, m, offer,
		sdjwt.KeyBinding{Nonce: nonce, Audience: audience, IssuedAt: s.clock.Now()},
		s.blinder)
	require.NoError(t, err, "the agent delegating the closed Payment Mandate to %s", audience)
	return chain
}

// initiations counts what the merchant asked its processor for, by method.
//
// Counted from the test goroutine after the request has returned, rather than
// through a .Once() expectation: testify fails an exceeded expectation by
// calling t.FailNow from whichever goroutine tripped it, and this one is tripped
// inside an HTTP handler.
//
// Which method matters as much as how many. A merchant that settled a delegated
// purchase through InitiatePayment would be presenting the processor a document
// addressed to itself, which the processor must refuse on the audience — so a
// count that did not distinguish them would pass on a purchase that cannot
// settle.
func (s chainShop) initiations(method string) int {
	calls := 0
	for _, c := range s.processor.Calls {
		if c.Method == method {
			calls++
		}
	}
	return calls
}

// settlingChain is the expectation for a processor that is allowed to be asked
// to settle a delegated purchase. Permissive, for the reason settling is.
func settlingChain(p *merchant.MockProcessor) {
	p.EXPECT().InitiatePaymentChain(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return("", true, nil)
}

// forwarded reads back what the merchant actually presented to its processor on
// the delegated leg: the chain and the nonce, in that order.
//
// Read from the test goroutine after the request has returned, which is safe —
// the handler is done with the recorder — and asserted there rather than pinned
// as an argument matcher, because testify fails a matcher from whichever
// goroutine tripped it and this one is tripped inside an HTTP handler.
func (s chainShop) forwarded(t *testing.T) (string, string) {
	t.Helper()

	for _, c := range s.processor.Calls {
		if c.Method != "InitiatePaymentChain" {
			continue
		}
		chain, ok := c.Arguments[1].(string)
		require.True(t, ok, "the chain argument has to be a string")
		nonce, ok := c.Arguments[2].(string)
		require.True(t, ok, "the nonce argument has to be a string")
		return chain, nonce
	}
	require.FailNow(t, "the merchant never presented anything to its processor")
	return "", ""
}

// flightConstraints is the built scenario's mandate: BEG→PMI, at most $200, this
// summer. Character for character the set in internal/agent/interpret's
// scenarios.go, which is what makes the beats these tests assert the beats the
// documentation describes.
func flightConstraints(t *testing.T) []generated.Constraint {
	t.Helper()
	return constraintsFrom(t, flightToPalma)
}

// TestAChainWithinItsConstraintsBuysTheOfferItNames is beat 6: the price has
// come down to $189, the mandate said at most $200, and the purchase completes
// without a person in the loop.
//
// The offer is quoted from the catalogue rather than the route, which is the
// gap this slice closed. Three of the four constraints above read facts about
// the item — the route it flies — and a checkout that named no item would leave
// them unevaluable, so the merchant would be authorising against a mandate it
// could not read most of.
func TestAChainWithinItsConstraintsBuysTheOfferItNames(t *testing.T) {
	t.Parallel()

	s := newChainShop(t)
	settlingChain(s.processor)

	// Two steps on, which is the third price in the schedule.
	s.clock.Advance(2 * merchant.DefaultStep)

	offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
	require.Equal(t, merchant.DemoPriceAccepted, price.Amount,
		"beat 6 is the $189 price; a schedule that has moved makes this a different test")

	body := s.chains(t, flightConstraints(t), offer, price, sameNonce(s.nonce(t)))
	status, out := s.present(t, "accepted", body)

	require.Equal(t, http.StatusOK, status,
		"a purchase inside every limit the user set has to complete")
	assert.Equal(t, true, out["settled"],
		"the merchant initiates payment, so a verified purchase that did not settle "+
			"means the money leg never ran")

	token, ok := out["receipt"].(string)
	require.True(t, ok, "every verdict is answered with a receipt, acceptance included")
	receipt, err := ap2.VerifyReceipt(token, s.merchant)
	require.NoError(t, err, "the receipt must verify against the merchant's published key")
	assert.Equal(t, generated.ReceiptResultSuccess, receipt.Result)
	assert.Equal(t, generated.ReceiptMandateTypeCheckout, receipt.MandateType,
		"the Checkout Mandate is the one AP2 gives this merchant to verify, so a success "+
			"receipt is about that one and not the payment side it read to answer its own question")

	chain, err := sdjwt.ParseChain(body["mandate_chain"].(string))
	require.NoError(t, err)
	assert.NoError(t, ap2.AnswersMandate(receipt, chain),
		"a receipt over a chain names the digest of its delegating hop; one naming anything "+
			"else answers a presentation nobody made")

	assert.Equal(t, 1, s.initiations("InitiatePaymentChain"),
		"a delegated purchase settles through the chain addressed to the processor")
	assert.Equal(t, 0, s.initiations("InitiatePayment"),
		"forwarding the chain addressed to this merchant would be refused by the processor "+
			"on its audience, which is the one thing a per-verifier closed mandate exists to do")

	// What actually went out, which is the half a call count cannot see. Both
	// values belong to the processor's hop and neither is the merchant's, so a
	// merchant that forwarded what it had just verified — or substituted the
	// challenge it had just checked — would be presenting the processor a pair
	// that cannot verify, on a purchase it had already signed an acceptance for.
	sent, nonce := s.forwarded(t)
	assert.Equal(t, body["processor_payment_chain"], sent,
		"the chain addressed to the processor is the one the processor is sent")
	assert.NotEqual(t, body["payment_chain"], sent,
		"and it is not the one addressed to this merchant, which is a different document")
	assert.Equal(t, processorNonce, nonce,
		"a delegation is bound to a challenge the verifier that checks it issued, so the "+
			"processor gets the processor's")
	assert.NotEqual(t, body["nonce"], nonce,
		"this merchant's own challenge is not one the processor ever issued, and sending it "+
			"would be a proof of possession against a value nobody asked for")
}

// TestAChainOverThePriceTheUserApprovedIsRefusedWithAReceipt is beat 5, and it
// is #119's headline.
//
// The agent presents at $210 against a mandate that said at most $200. What
// makes this worth a test is *where* the refusal comes from: not the agent
// declining to try, which would prove nothing and leave nothing signed, but the
// merchant evaluating the user's own constraints against a purchase the merchant
// itself described. The artefact that comes back is a receipt naming the reason,
// not a Problem Details document — a 422 with a good error message looks like a
// working verifier and leaves a dispute with nothing to read.
func TestAChainOverThePriceTheUserApprovedIsRefusedWithAReceipt(t *testing.T) {
	t.Parallel()

	s := newChainShop(t)

	// One step on: the middle price, above the cap.
	s.clock.Advance(merchant.DefaultStep)

	offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
	require.Equal(t, merchant.DemoPriceRejected, price.Amount,
		"beat 5 is the $210 price, the one the verifier refuses")
	require.Greater(t, price.Amount, merchant.DemoPriceCap,
		"the whole beat is that this price is above what the user approved")

	body := s.chains(t, flightConstraints(t), offer, price, sameNonce(s.nonce(t)))
	status, out := s.present(t, "refused", body)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.NotContains(t, out, "payment_receipt",
		"no money leg runs on a purchase the merchant refused")

	token, ok := out["receipt"].(string)
	require.True(t, ok,
		"a refusal that returns no receipt is the failure AP2 forbids, and it is the "+
			"artefact this beat exists to produce")
	receipt, err := ap2.VerifyReceipt(token, s.merchant)
	require.NoError(t, err, "the receipt must verify against the merchant's published key")

	assert.Equal(t, generated.ReceiptResultError, receipt.Result)
	require.NotNil(t, receipt.Error)
	// constraint_violated and not verifier_unavailable, which is the difference
	// between the merchant reporting the user's limit and the merchant blaming
	// itself for one. ap2.CodeOf answered the second for every constraint verdict
	// until #111; correcting the answer is what makes this assertion mean
	// anything, and it is why a reader tempted to "fix" a surprising code here
	// should change nothing — an agent reads this to decide whether coming back
	// with a lower price is worth trying, and verifier_unavailable tells it to
	// retry the same purchase against a merchant it thinks is broken.
	assert.Equal(t, generated.ErrorCodeConstraintViolated, *receipt.Error,
		"the receipt has to name which rule was broken, since that is what the agent acts "+
			"on when it comes back with a lower price")
	require.NotNil(t, receipt.ErrorDescription)
	assert.Contains(t, *receipt.ErrorDescription, "amount",
		"a rejection that does not say which limit was exceeded is one the agent cannot act "+
			"on: the code says a limit was broken and this says which")
	assert.Equal(t, generated.ReceiptMandateTypeCheckout, receipt.MandateType,
		"the limit was on the Checkout Mandate's chain, and a receipt naming the payment "+
			"side would send a reader to the mandate that was fine")

	chain, err := sdjwt.ParseChain(body["mandate_chain"].(string))
	require.NoError(t, err)
	assert.NoError(t, ap2.AnswersMandate(receipt, chain),
		"the receipt has to reference the presentation that failed")

	assert.Equal(t, 0, s.initiations("InitiatePaymentChain"),
		"a merchant that asked for money on a purchase it had just refused would be "+
			"contradicting its own signed answer")
}

// TestADelegatedPaymentForAnotherPurchaseIsRefusedAsThePaymentMandate is the
// chain-shaped half of what #88 found on the Human Present path.
//
// Both chains can be perfect delegations of mandates the user really signed, and
// still not belong together: one authorising this purchase and the other paying
// for a different one, or paying a number this checkout does not cost. Neither
// is caught by verifying either chain — ap2.AuthorisePaymentChain deliberately
// runs no binding check, because a closed Payment Mandate never carries the
// document it binds to — so the merchant, which wrote the document, is the party
// that has to close both loops.
//
// The receipt names the Payment Mandate in both cases, and that is the point of
// pairing a verdict with its artefact: the Checkout Mandate here is faultless.
func TestADelegatedPaymentForAnotherPurchaseIsRefusedAsThePaymentMandate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		about   func(t *testing.T, s chainShop, offer string, price generated.Amount) purchasing
		code    generated.ErrorCode
		because string
	}{
		{
			name: "it pays for a different checkout",
			about: func(t *testing.T, s chainShop, offer string, price generated.Amount) purchasing {
				// A second genuine offer at the same price. ECDSA is randomised,
				// so two quotes of one flight are two documents — which is what
				// lets the amount agree while only the binding does not.
				elsewhere, other := s.quoteItem(t, merchant.DemoFlightID, 1)
				require.Equal(t, price, other, "the schedule must not have moved between quotes")
				require.NotEqual(t, offer, elsewhere, "two quotes have to be two documents")
				return purchasing{checkout: offer, paymentFor: elsewhere, pays: price}
			},
			code:    generated.ErrorCodePaymentBindingMismatch,
			because: "two genuine mandates from two different purchases must not settle against each other",
		},
		{
			name: "it pays a price this checkout does not cost",
			about: func(_ *testing.T, _ chainShop, offer string, price generated.Amount) purchasing {
				return purchasing{checkout: offer, paymentFor: offer, pays: dearer(price, 1)}
			},
			code: generated.ErrorCodePaymentAmountMismatch,
			because: "AP2's binding proves the two documents name one purchase and proves nothing " +
				"about the number, which is why this check is ours",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newChainShop(t)
			s.clock.Advance(2 * merchant.DefaultStep)

			offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
			body := s.chainsWith(t, flightConstraints(t), sameNonce(s.nonce(t)),
				tc.about(t, s, offer, price))

			status, out := s.present(t, "divergent", body)

			require.Equal(t, http.StatusUnprocessableEntity, status, tc.because)
			token, ok := out["receipt"].(string)
			require.True(t, ok, "a refusal that returns no receipt is the failure AP2 forbids")

			receipt, err := ap2.VerifyReceipt(token, s.merchant)
			require.NoError(t, err)
			require.NotNil(t, receipt.Error)
			assert.Equal(t, tc.code, *receipt.Error, tc.because)
			assert.Equal(t, generated.ReceiptMandateTypePayment, receipt.MandateType,
				"the Checkout Mandate was faultless, and a receipt naming it would be a signed "+
					"false statement about a document that was fine")

			paying, err := sdjwt.ParseChain(body["payment_chain"].(string))
			require.NoError(t, err)
			assert.NoError(t, ap2.AnswersMandate(receipt, paying),
				"the receipt has to reference the chain that failed")

			assert.Equal(t, 0, s.initiations("InitiatePaymentChain"))
		})
	}
}

// TestTheSubjectAMerchantBuildsIsTheSubjectItsSearchMatched is the drift this
// slice closes, and it is invisible to either half alone.
//
// Search decides what to show by describing an offer to the constraint
// evaluator; the checkout decides what a mandate authorises by describing the
// same offer to the same evaluator. Two descriptions built two ways would make a
// product appear in a search that a mandate then refuses to buy, or the reverse
// — and the search's tests and the checkout's tests would both keep passing.
func TestTheSubjectAMerchantBuildsIsTheSubjectItsSearchMatched(t *testing.T) {
	t.Parallel()

	t.Run("one offer, two paths, one subject", func(t *testing.T) {
		t.Parallel()

		cat, clk := demoCatalogue(t)
		now := clk.Now()

		// The search side reaches the facts through Price, which is what Search
		// itself walks the catalogue with.
		listed, err := cat.Price(merchant.DemoConcertID)
		require.NoError(t, err, "Price")
		searched := cat.Subject(listed.Offer, listed.Price, 1, now)

		// The checkout side reaches them through Quote — what GET /checkout
		// signs — and Find, which is how settle recovers what an already-signed
		// offer is for.
		quoted, err := cat.Quote(merchant.DemoConcertID, 1)
		require.NoError(t, err, "Quote")
		found, err := cat.Find(merchant.DemoConcertID)
		require.NoError(t, err, "Find")
		bought := cat.Subject(found, quoted.LinePrice, quoted.Quantity, now)

		assert.Equal(t, searched, bought,
			"a product has to appear in a search exactly when a mandate would authorise "+
				"buying it, and that is a claim about two code paths agreeing")
	})

	// The same claim through HTTP, which is where it can actually break: a
	// constraint set either finds the offer and buys it, or finds neither. A row
	// that searched and did not settle would be a shop advertising something it
	// will not sell.
	t.Run("and the two agree about what a mandate authorises", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			name        string
			constraints string
			item        string
			steps       int
			authorised  bool
			because     string
		}{
			{
				name:        "the bicycle, once its price has come down",
				constraints: thisBicycle,
				item:        merchant.DemoBicycleID,
				steps:       1,
				authorised:  true,
				because:     "item.id names one specific object, which is the case a category cannot express",
			},
			{
				name:        "the bicycle, before it has",
				constraints: thisBicycle,
				item:        merchant.DemoBicycleID,
				steps:       0,
				because:     "the opening price is above the cap, so neither half may say yes",
			},
			{
				name:        "the flight, at the price the mandate allows",
				constraints: flightToPalma,
				item:        merchant.DemoFlightID,
				steps:       2,
				authorised:  true,
				because:     "the route pins read item attributes, which only a catalogue offer carries",
			},
			{
				name:        "the flight, at the price it does not",
				constraints: flightToPalma,
				item:        merchant.DemoFlightID,
				steps:       1,
				because:     "beat 5 again, seen from the search side: the offer is out of range for both",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				s := newChainShop(t)
				if tc.authorised {
					settlingChain(s.processor)
				}
				for range tc.steps {
					s.clock.Advance(merchant.DefaultStep)
				}

				constraints := constraintsFrom(t, tc.constraints)

				var found struct {
					Offers []struct {
						ID string `json:"id"`
					} `json:"offers"`
				}
				encoded, err := json.Marshal(constraints)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, s.get(t,
					"/search?"+merchant.SearchParam+"="+
						base64.RawURLEncoding.EncodeToString(encoded), &found),
					"searching the catalogue")

				searched := false
				for _, o := range found.Offers {
					if o.ID == tc.item {
						searched = true
					}
				}
				assert.Equal(t, tc.authorised, searched,
					"the search half of: %s", tc.because)

				offer, price := s.quoteItem(t, tc.item, 1)
				body := s.chains(t, constraints, offer, price, sameNonce(s.nonce(t)))
				status, _ := s.present(t, "settle", body)

				want := http.StatusUnprocessableEntity
				if tc.authorised {
					want = http.StatusOK
				}
				assert.Equal(t, want, status,
					"the checkout half of the same claim: %s", tc.because)
			})
		}
	})
}

// TestAChainAgainstAnOfferThatNamesNoItemIsRefused is the honest half of having
// two offer shapes.
//
// A route offer carries a price and nothing a constraint on what is being bought
// can be evaluated against, and the narrowing an agent applies when it picks
// something always names one. Accepting the chain anyway would authorise a
// purchase against limits the merchant could not read — the failure mode of
// treating an unstated fact as permitted — so it is refused, out loud, saying
// exactly that.
//
// Problem Details rather than a receipt, because no mandate has been examined:
// the offer is established as the merchant's own and then found to name nothing,
// which is a statement about the request.
func TestAChainAgainstAnOfferThatNamesNoItemIsRefused(t *testing.T) {
	t.Parallel()

	s := newChainShop(t)
	s.clock.Advance(2 * merchant.DefaultStep)

	// A genuine offer from this merchant, at a price the mandate allows. The
	// only thing wrong with this purchase is which door the offer came through.
	offer, price := s.quoteRoute(t)
	require.Equal(t, merchant.DemoPriceAccepted, price.Amount,
		"priced inside the cap, so a refusal here cannot be about the money")

	body := s.chains(t, flightConstraints(t), offer, price, sameNonce(s.nonce(t)))
	status, out := s.present(t, "route", body)

	require.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, string(generated.ErrorCodeRequestMalformed), out["code"])
	assert.Contains(t, fmt.Sprint(out["detail"]), "names no item",
		"the refusal has to say what is actually wrong, or the two offer shapes quietly "+
			"merge into one that is decidable for some purchases and not others")
	assert.NotContains(t, out, "receipt",
		"no mandate has been examined, so there is nothing to sign an answer about")
	assert.Equal(t, 0, s.initiations("InitiatePaymentChain"))
}

// TestAnOfferedQuantityIsWhatTheConstraintIsEvaluatedAgainst is why the checkout
// signs a quantity rather than assuming one.
//
// "Two tickets, up to $160 all in" places two bounds, and either alone approves
// something the user did not say. The cap here is deliberately far above any
// price the catalogue holds, so the only thing that can refuse the three-ticket
// purchase is the quantity — a set carrying the scripted $160 cap would be
// refused on the money and would pass this test with the quantity never read.
func TestAnOfferedQuantityIsWhatTheConstraintIsEvaluatedAgainst(t *testing.T) {
	t.Parallel()

	// amount is here because this merchant will not authorise against a mandate
	// that says nothing about it — RequireConstrained, in the wiring above — and
	// is set high enough that it cannot be the reason for any refusal below.
	const atMostTwo = `[
		{"op":"lte","field":"quantity","value":2},
		{"op":"lte","field":"amount","value":{"amount":99999,"currency":"USD"}}
	]`

	for _, tc := range []struct {
		name       string
		quantity   int
		authorised bool
		because    string
	}{
		{
			name:       "two, which is what was approved",
			quantity:   2,
			authorised: true,
			because: "the mirror: a check that refused every basket would satisfy the case " +
				"below without reading the quantity at all",
		},
		{
			name:     "three, which is not",
			quantity: 3,
			because: "a cap on the total cannot tell one ticket at $160 from four at $40, so " +
				"the count is part of what was approved and has to be evaluated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newChainShop(t)
			if tc.authorised {
				settlingChain(s.processor)
			}

			offer, price := s.quoteItem(t, merchant.DemoConcertID, tc.quantity)
			assert.Equal(t, merchant.DemoConcertPrice*tc.quantity, price.Amount,
				"the signed offer prices the whole line, which is what the Payment Mandate "+
					"has to pay and what an amount constraint bounds")

			body := s.chains(t, constraintsFrom(t, atMostTwo), offer, price, sameNonce(s.nonce(t)))
			status, _ := s.present(t, strconv.Itoa(tc.quantity), body)

			want := http.StatusUnprocessableEntity
			if tc.authorised {
				want = http.StatusOK
			}
			assert.Equal(t, want, status, tc.because)
		})
	}
}

// TestTheAmountEvaluatedIsWhatTheWholeBasketCosts is the other half of a
// quantity, and the half a test at one unit cannot see.
//
// The count and the money are two bounds and a purchase can sit inside one and
// outside the other. Three tickets at $75 is within a limit of five and outside
// a cap of $160 — and a merchant that evaluated the *unit* price against that
// cap would authorise it, having compared the user's limit on the purchase
// against the price of a third of it.
func TestTheAmountEvaluatedIsWhatTheWholeBasketCosts(t *testing.T) {
	t.Parallel()

	// Five is deliberately more than any quantity below, so the count cannot be
	// what refuses anything here. The cap is the scripted $160.
	const upToFiveUnderOneSixty = `[
		{"op":"lte","field":"quantity","value":5},
		{"op":"lte","field":"amount","value":{"amount":16000,"currency":"USD"}}
	]`

	for _, tc := range []struct {
		name       string
		quantity   int
		authorised bool
		because    string
	}{
		{
			name:       "two tickets, which come to $150",
			quantity:   2,
			authorised: true,
			because:    "inside both bounds, which is what makes the row below about the money alone",
		},
		{
			name:     "three tickets, which come to $225",
			quantity: 3,
			because: "each ticket is $75 and every one of them is under the cap; what is over it " +
				"is the purchase, which is the only thing the user placed a limit on",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newChainShop(t)
			if tc.authorised {
				settlingChain(s.processor)
			}

			offer, price := s.quoteItem(t, merchant.DemoConcertID, tc.quantity)
			require.Equal(t, merchant.DemoConcertPrice*tc.quantity, price.Amount,
				"the signed offer prices the whole line, or there is no basket here to test")
			require.LessOrEqual(t, merchant.DemoConcertPrice, merchant.DemoConcertCap,
				"one ticket has to be inside the cap, or this proves nothing about which "+
					"number was compared")

			body := s.chains(t, constraintsFrom(t, upToFiveUnderOneSixty), offer, price,
				sameNonce(s.nonce(t)))
			status, _ := s.present(t, strconv.Itoa(tc.quantity), body)

			want := http.StatusUnprocessableEntity
			if tc.authorised {
				want = http.StatusOK
			}
			assert.Equal(t, want, status, tc.because)
		})
	}
}

// TestAChainBlindedWithSHA384StillSettles is why the merchant is handed the
// binding rather than assembling one.
//
// checkout_hash is computed under whatever _sd_alg names, and for a chain the
// one that governs is the *delegating* hop's. A merchant that reached for
// sha-256 — the default, and what every other test here happens to use — would
// recompute a digest that does not match, and refuse a perfectly good purchase
// as payment_binding_mismatch: the agent is paying for something else, reported
// about a disagreement over a default.
//
// Nothing else in this file would notice, because a Blinder built with no
// options is sha-256 on both sides. This is the one test where the two could
// differ.
func TestAChainBlindedWithSHA384StillSettles(t *testing.T) {
	t.Parallel()

	s := newChainShop(t)
	settlingChain(s.processor)

	blinder, err := sdjwt.NewBlinder(sdjwt.WithHashAlg(sdjwt.SHA384))
	require.NoError(t, err, "building a sha-384 blinder")
	s.blinder = blinder

	s.clock.Advance(2 * merchant.DefaultStep)
	offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)

	body := s.chains(t, flightConstraints(t), offer, price, sameNonce(s.nonce(t)))
	status, out := s.present(t, "sha384", body)

	require.Equal(t, http.StatusOK, status,
		"the algorithm the delegating hop declares is the one its binding was made under, "+
			"and a verifier that assumed another would refuse an honest purchase")
	assert.Equal(t, true, out["settled"])
}

// TestAHalfWiredMerchantWillNotStandUp is the Handler guard for the four fields
// the Human Not Present flow needs together.
//
// Each one missing produces the same symptom at runtime — every delegated
// purchase refused — and each for a reason that reads as the caller's fault. A
// merchant that serves traffic in that state is worse than one that will not
// start, because the refusals look like working verification.
func TestAHalfWiredMerchantWillNotStandUp(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		spoil   func(s *merchant.Service)
		because string
	}{
		{
			name:    "chain rules for the checkout and none for the payment",
			spoil:   func(s *merchant.Service) { s.ChainPayments = nil },
			because: "the merchant checks the price as well as the purchase, on both flows",
		},
		{
			name:    "chain rules for the payment and none for the checkout",
			spoil:   func(s *merchant.Service) { s.ChainRules = nil },
			because: "and the mirror, so the guard is not one-directional",
		},
		{
			name:    "chain rules and no challenger",
			spoil:   func(s *merchant.Service) { s.Challenge = nil },
			because: "a delegation is a key binding, and there would be no nonce to bind it to",
		},
		{
			name:    "chain rules and no catalogue",
			spoil:   func(s *merchant.Service) { s.Catalogue = nil },
			because: "there would be nothing to say what the item a constraint names actually is",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := chainCapableService(t)
			tc.spoil(svc)

			_, err := svc.Handler()
			assert.Error(t, err, tc.because)
		})
	}

	t.Run("and a merchant with none of it stands up as Human Present", func(t *testing.T) {
		t.Parallel()

		svc := chainCapableService(t)
		svc.ChainRules, svc.ChainPayments, svc.Challenge, svc.Catalogue = nil, nil, nil, nil

		_, err := svc.Handler()
		assert.NoError(t, err,
			"the mirror: a guard that refused every merchant would satisfy the cases above "+
				"without being a guard, and the Human Present merchant is the one that has "+
				"always worked")
	})
}

// chainCapableService builds a fully wired merchant without serving it, so a
// test can remove one field and ask Handler what it thinks.
func chainCapableService(t *testing.T) *merchant.Service {
	t.Helper()

	clk := clock.NewFake(base)
	store, err := crypto.NewStore(clk)
	require.NoError(t, err)
	ref, err := store.Generate(crypto.Slot("merchant"), authz.ES256, "merchant")
	require.NoError(t, err)
	signer, err := store.Signer(crypto.Slot("merchant"))
	require.NoError(t, err)
	verifier, err := store.Resolve(t.Context(), ref)
	require.NoError(t, err)

	inventory, err := merchant.NewDemoInventory(clk, base, merchant.DefaultStep)
	require.NoError(t, err)
	catalogue, err := merchant.NewDemoCatalogue(clk, chainMerchantID, base, merchant.DefaultStep)
	require.NoError(t, err)
	challenge, err := crypto.NewChallenger(clk, roles.ChallengeTTL)
	require.NoError(t, err)

	rules := ap2.MerchantRules{Issuer: verifier, Clock: clk, AgentKey: roles.AgentKey, Audience: chainMerchantID}
	payments := ap2.CredentialProviderRules{Issuer: verifier, Clock: clk, AgentKey: roles.AgentKey, Audience: chainMerchantID}

	return &merchant.Service{
		ID:            chainMerchantID,
		Inventory:     inventory,
		Catalogue:     catalogue,
		Rules:         rules,
		ChainRules:    rules,
		Payments:      payments,
		ChainPayments: payments,
		Signer:        signer,
		Own:           verifier,
		Keys:          store,
		Clock:         clk,
		Challenge:     challenge,
		Processor:     merchant.NewMockProcessor(t),
	}
}

// TestTheNonceSplitIsTwoDifferentFailures is the pair #116 could only test one
// half of, for want of a handler to present a chain to.
//
// They look alike and are not. A challenge this merchant never issued says
// nothing about any mandate — it is refused before one is read, and there is
// nothing to write a receipt about. A challenge it did issue that disagrees with
// the one signed into the delegating hop is a failed proof of possession: a
// mandate has been examined and found wanting, so the answer is a receipt.
func TestTheNonceSplitIsTwoDifferentFailures(t *testing.T) {
	t.Parallel()

	t.Run("a challenge this merchant did not issue is refused before any mandate is read", func(t *testing.T) {
		t.Parallel()

		s := newChainShop(t)
		s.clock.Advance(2 * merchant.DefaultStep)

		offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
		invented := "bm90LWEtY2hhbGxlbmdl.bm90LWEtbWFj"
		body := s.chains(t, flightConstraints(t), offer, price, delegation{
			signed: invented, presented: invented,
		})

		status, out := s.present(t, "invented", body)

		require.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), out["code"])
		assert.NotContains(t, out, "receipt",
			"a value this merchant never handed out proves nothing about any mandate, and "+
				"a receipt would be a statement it is not in a position to make")
	})

	t.Run("a challenge that disagrees with the one signed into the chain gets a receipt", func(t *testing.T) {
		t.Parallel()

		s := newChainShop(t)
		s.clock.Advance(2 * merchant.DefaultStep)

		offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
		// Both genuine, both this merchant's, and not the same one — which is
		// what makes this a verification failure rather than a forgery.
		body := s.chains(t, flightConstraints(t), offer, price, delegation{
			signed: s.nonce(t), presented: s.nonce(t),
		})

		status, out := s.present(t, "mismatched", body)

		require.Equal(t, http.StatusUnprocessableEntity, status)
		token, ok := out["receipt"].(string)
		require.True(t, ok,
			"a mandate was examined and refused, so the refusal is evidence and has to be signed")

		receipt, err := ap2.VerifyReceipt(token, s.merchant)
		require.NoError(t, err)
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error,
			"the delegation proved possession of a key against a challenge nobody asked for, "+
				"which is a key binding failure and not a malformed request")
	})
}

// TestAMalformedDelegatedPurchaseIsRefusedBeforeAnythingIsParsed covers the wire
// shape's own promise: which flow is being presented is decided by which fields
// are populated, never by looking inside a string for the "~~" a chain happens
// to contain.
func TestAMalformedDelegatedPurchaseIsRefusedBeforeAnythingIsParsed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		body    func(full map[string]any) map[string]any
		because string
	}{
		{
			name: "a checkout chain with no payment chain",
			body: func(full map[string]any) map[string]any {
				delete(full, "payment_chain")
				return full
			},
			because: "the merchant verifies the price as well as the purchase, and half a " +
				"presentation is not a smaller one",
		},
		{
			name: "no chain for the processor",
			body: func(full map[string]any) map[string]any {
				delete(full, "processor_payment_chain")
				return full
			},
			because: "a delegation names its audience, so the merchant's own copy cannot be " +
				"forwarded and a purchase with nothing to forward cannot settle",
		},
		{
			name: "a chain for the processor and no challenge it is bound to",
			body: func(full map[string]any) map[string]any {
				delete(full, "processor_nonce")
				return full
			},
			because: "the merchant cannot supply a challenge the processor issued, so a purchase " +
				"missing one is one it would verify and then fail to settle — having spent a " +
				"nonce and signed an acceptance for money that never moves",
		},
		{
			name: "a directly signed mandate beside a chain",
			body: func(full map[string]any) map[string]any {
				full["mandate"] = "eyJhbGciOiJFUzI1NiJ9.e30.c2ln~"
				return full
			},
			because: "two authorisations in one request, and a merchant that chose between " +
				"them would be choosing what the user approved",
		},
		{
			name: "nothing at all",
			body: func(full map[string]any) map[string]any {
				delete(full, "mandate_chain")
				delete(full, "payment_chain")
				delete(full, "processor_payment_chain")
				return full
			},
			because: "an offer echoed back with no authorisation is not a purchase",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newChainShop(t)
			s.clock.Advance(2 * merchant.DefaultStep)

			offer, price := s.quoteItem(t, merchant.DemoFlightID, 1)
			body := tc.body(s.chains(t, flightConstraints(t), offer, price, sameNonce(s.nonce(t))))

			status, out := s.present(t, "malformed", body)

			assert.Equal(t, http.StatusBadRequest, status, tc.because)
			assert.Equal(t, string(generated.ErrorCodeRequestMalformed), out["code"])
			assert.NotContains(t, out, "receipt",
				"nothing was parsed, so there is no mandate for a receipt to reference")
		})
	}
}

// TestAMerchantThatDoesNotVerifyChainsRefusesOneRatherThanIgnoringIt is the
// other side of the fields being optional.
//
// A merchant with no chain rules is the Human Present flow and nothing else, and
// what it must not do is fall through to the direct path — where no constraint
// is read at all — because a chain happened to arrive.
func TestAMerchantThatDoesNotVerifyChainsRefusesOneRatherThanIgnoringIt(t *testing.T) {
	t.Parallel()

	s := newShop(t)
	offer, _ := s.quote(t)

	status, out := s.settle(t, map[string]any{
		"mandate_chain":           "eyJhbGciOiJFUzI1NiJ9.e30.c2ln~~eyJhbGciOiJFUzI1NiJ9.e30.c2ln~",
		"payment_chain":           "eyJhbGciOiJFUzI1NiJ9.e30.c2ln~~eyJhbGciOiJFUzI1NiJ9.e30.c2ln~",
		"processor_payment_chain": "eyJhbGciOiJFUzI1NiJ9.e30.c2ln~~eyJhbGciOiJFUzI1NiJ9.e30.c2ln~",
		"nonce":                   "whatever",
		"checkout":                offer,
	})

	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, string(generated.ErrorCodeRequestMalformed), out["code"],
		"silently treating a delegation as a directly signed mandate would evaluate none "+
			"of the user's constraints and settle anyway")
}

// TestTheHumanPresentPathIsUntouched is the property this slice most had to
// avoid breaking, and the existing suites are the rest of it.
//
// service_test.go is unchanged, byte for byte, and passing — which is the
// stronger of the two statements and the one worth making. roles_test.go gained
// eleven lines, and none of them is an assertion: refusingProcessor grew the
// second method the Processor interface now declares, because a stub that does
// not satisfy an interface does not compile. Saying "unchanged" of both was
// true when written and stopped being true in the same commit that widened the
// interface.
//
// What this adds is the case they cannot cover: a merchant that *also* accepts
// delegations still settles a directly signed purchase, through the direct
// processor leg, against both the route offer the Human Present flow has always
// used and the catalogue offer this slice added. A merchant that routed a
// Human Present purchase down the chain path would refuse it for want of a
// nonce, and one that forwarded the wrong document to its processor would settle
// nothing.
func TestTheHumanPresentPathIsUntouched(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		quote func(t *testing.T, s chainShop) (string, generated.Amount)
	}{
		{
			name: "against the route offer, which is what make demo has always bought",
			quote: func(t *testing.T, s chainShop) (string, generated.Amount) {
				return s.quoteRoute(t)
			},
		},
		{
			name: "against a catalogue offer, which this slice added",
			quote: func(t *testing.T, s chainShop) (string, generated.Amount) {
				return s.quoteItem(t, merchant.DemoFlightID, 1)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newChainShop(t)
			s.processor.EXPECT().
				InitiatePayment(mock.Anything, mock.Anything, mock.Anything).
				Return("", true, nil)

			offer, price := tc.quote(t, s)

			// Signed by the user directly, which is what Human Present means:
			// no open mandate, no delegation, no nonce.
			checkout, err := ap2.IssueCheckout(t.Context(), s.user,
				generated.CheckoutMandate{Checkout: &offer}, s.blinder)
			require.NoError(t, err, "the user signing the Checkout Mandate")
			payment, err := ap2.IssuePayment(t.Context(), s.user, generated.PaymentMandate{
				Payee:             generated.Merchant{ID: chainMerchantID, Name: "Air Serbia"},
				PaymentAmount:     price,
				PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
			}, offer, s.blinder)
			require.NoError(t, err, "the user signing the Payment Mandate")

			status, out := s.present(t, "direct", map[string]any{
				"mandate":  checkout.String(),
				"payment":  payment.String(),
				"checkout": offer,
			})

			require.Equal(t, http.StatusOK, status,
				"a directly signed purchase has to keep working on a merchant that also "+
					"accepts delegations")
			assert.Equal(t, true, out["settled"])

			assert.Equal(t, 1, s.initiations("InitiatePayment"),
				"a Human Present purchase presents the mandate the user signed, not a chain")
			assert.Equal(t, 0, s.initiations("InitiatePaymentChain"))
		})
	}
}
