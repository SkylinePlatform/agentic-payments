package roles_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/mpp"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The payment side of Human Not Present, at the layer a caller meets it.
//
// What is proved in internal/adapters/ap2 is that a chain verifies and that its
// constraints are evaluated. What can only be proved here is that a role
// actually reaches that code: that a chain arriving over HTTP is routed to the
// chain entry point rather than the single-mandate one, that a refusal still
// comes back with a signed receipt, and that the Human Present path both roles
// already served is untouched by any of it.

const (
	credProviderID = "mock-credential-provider"
	processorID    = "mock-payment-processor"

	// payeeID is the merchant the open Payment Mandate below pins, and the one
	// the closed mandate has to reproduce.
	payeeID = "air-serbia"
)

// capOnAmount and pinOnPayee are two limits a payment-side verifier can apply
// in full: ap2.PaymentSubject states the amount and the payee, so neither is
// withheld by Minimise and neither is refused in ignorance.
const (
	capOnAmount = `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`
	pinOnPayee  = `{"op":"eq","field":"merchant.id","value":"air-serbia"}`

	// pinOnRoute is a limit it cannot apply at all — a Payment Mandate names no
	// item — so a mandate carrying only this one narrows to nothing.
	pinOnRoute = `{"op":"eq","field":"item.attr.route.origin","value":"BEG"}`
)

// delegation is the Human Not Present fixture: a user who signed an open
// Payment Mandate, and an agent holding the key that mandate endorses.
//
// The agent's key really is a second key pair with its own store, and the
// endorsement really does travel through cnf — roles.PublicKey writes it in and
// roles.AgentKey reads it back out, which is the pair those two functions exist
// for. A fixture that endorsed a fixed literal would verify a signature against
// a key nobody signed with.
type delegation struct {
	user    party
	agent   party
	blinder *sdjwt.Blinder
	open    *sdjwt.SDJWT
}

func newDelegation(t *testing.T, constraints ...string) delegation {
	t.Helper()

	user := newParty(t, "user")
	agent := newParty(t, "agent")

	agentKey, err := roles.PublicKey(t.Context(), agent.keys)
	require.NoError(t, err, "reading the key the open mandate will endorse")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	cs := make([]generated.Constraint, 0, len(constraints))
	for _, raw := range constraints {
		var c generated.Constraint
		require.NoError(t, json.Unmarshal([]byte(raw), &c),
			"the test's own fixture has to be a valid constraint")
		cs = append(cs, c)
	}

	payee := generated.Merchant{ID: payeeID, Name: "Air Serbia"}
	open, err := ap2.IssueOpenPayment(t.Context(), user.signer, generated.OpenPaymentMandate{
		AgentKey:    agentKey,
		Constraints: cs,
		Payee:       &payee,
	}, blinder)
	require.NoError(t, err, "the user signing the open Payment Mandate")

	return delegation{user: user, agent: agent, blinder: blinder, open: open}
}

// chainFor is the agent signing a closed Payment Mandate as a delegation,
// addressed to one verifier and bound to one challenge.
//
// One chain per verifier, which is what audience and nonce being parameters
// says: ap2.DelegatePayment writes both into the delegating hop and
// sdjwt.VerifyChain compares them, so a mandate three roles have to read is
// three delegations rather than one presented three times.
func (d delegation) chainFor(t *testing.T, audience, nonce string, amountMinor int) string {
	t.Helper()

	chain, err := ap2.DelegatePayment(t.Context(), d.agent.signer, d.open, generated.PaymentMandate{
		Payee:             generated.Merchant{ID: payeeID, Name: "Air Serbia"},
		PaymentAmount:     generated.Amount{Amount: amountMinor, Currency: "USD"},
		PaymentInstrument: pinnedInstrument,
	}, offerJWT, sdjwt.KeyBinding{
		Nonce: nonce, Audience: audience, IssuedAt: d.user.clock.Now(),
	}, d.blinder)
	require.NoError(t, err, "the agent delegating to a closed Payment Mandate")
	return chain.String()
}

// theCredentialProvider stands up a provider serving both modes, wired the way
// cmd/credprovider wires one: a single rule set held behind two interfaces.
func theCredentialProvider(t *testing.T, user, provider party) *httptest.Server {
	t.Helper()

	challenge, err := crypto.NewChallenger(provider.clock, roles.ChallengeTTL)
	require.NoError(t, err, "minting this provider's own challenge key")

	rules := ap2.CredentialProviderRules{
		Issuer:   user.verifier,
		Clock:    provider.clock,
		AgentKey: roles.AgentKey,
		Audience: credProviderID,
		// The same policy cmd/credprovider sets: this provider will not fund a
		// purchase against a mandate that says nothing about the amount.
		RequireConstrained: []string{"amount"},
	}
	svc := &credprovider.Service{
		ID:        credProviderID,
		Rules:     rules,
		Chains:    rules,
		Signer:    provider.signer,
		Keys:      provider.keys,
		Clock:     provider.clock,
		Challenge: challenge,
	}
	handler, err := svc.Handler()
	return serve(t, handler, err)
}

// theProcessor stands up an MPP serving both modes, wired the way cmd/mpp wires
// one. Its audience is its own, never the provider's.
func theProcessor(t *testing.T, user, processor party) *httptest.Server {
	t.Helper()

	challenge, err := crypto.NewChallenger(processor.clock, roles.ChallengeTTL)
	require.NoError(t, err, "minting this processor's own challenge key")

	payments := ap2.CredentialProviderRules{
		Issuer:             user.verifier,
		Clock:              processor.clock,
		AgentKey:           roles.AgentKey,
		Audience:           processorID,
		RequireConstrained: []string{"amount"},
	}
	svc := &mpp.Service{
		ID:            processorID,
		Payments:      payments,
		PaymentChains: payments,
		Rules:         ap2.MPPRules{Clock: processor.clock},
		Signer:        processor.signer,
		Keys:          processor.keys,
		Clock:         processor.clock,
		Challenge:     challenge,
	}
	handler, err := svc.Handler()
	return serve(t, handler, err)
}

// nonceFrom fetches a challenge the way an agent does, from the verifier that
// will later check it.
//
// A test that invented its own string would be checking nothing: crypto.
// Challenger refuses every value it did not issue, so the endpoint is not a
// convenience here, it is the only source of an acceptable one.
func nonceFrom(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+roles.NoncePath, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err, "asking for a challenge")
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "this verifier would not issue a challenge")

	var out struct {
		Nonce string `json:"nonce"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.Nonce)
	return out.Nonce
}

// funded is POST /credential's answer, in both modes.
type funded struct {
	Receipt    string                       `json:"receipt"`
	Credential *generated.PaymentCredential `json:"credential"`
}

// settled is POST /payment's.
type settledOutcome struct {
	Receipt string `json:"receipt"`
	Settled bool   `json:"settled"`
}

// mintCredential runs the funding leg so a settlement test has real money to
// spend.
//
// The credential has to come from the provider rather than be assembled here:
// what the processor checks is that its checkout hash matches the one inside
// the chain, and a hash the test computed would be the test agreeing with
// itself.
func mintCredential(t *testing.T, d delegation) *generated.PaymentCredential {
	t.Helper()

	srv := theCredentialProvider(t, d.user, newParty(t, "credprovider"))
	nonce := nonceFrom(t, srv)

	var minted funded
	require.Equal(t, http.StatusOK, post(t, srv.URL+"/credential", map[string]any{
		"chain": d.chainFor(t, credProviderID, nonce, 18900),
		"nonce": nonce,
	}, &minted), "the funding leg has to succeed before there is money to spend")
	require.NotNil(t, minted.Credential)
	return minted.Credential
}

// TestFundingADelegatedPaymentMandateScopesTheCredentialToItsCheckout is the
// Human Not Present counterpart of the funding leg.
//
// Every signature in this story belongs to a different party: the user signed
// the open mandate and the limits in it, the agent signed the closed one under
// the key that mandate endorses, and the provider signs the receipt. Nothing
// the agent could have written on its own gets it a credential.
//
// The credential is scoped to the checkout hash out of the *closed mandate
// inside the chain*, which is the property worth pinning at this layer: a
// provider that read it from anywhere else would be scoping money to a digest
// the caller chose.
func TestFundingADelegatedPaymentMandateScopesTheCredentialToItsCheckout(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	srv := theCredentialProvider(t, d.user, provider)

	nonce := nonceFrom(t, srv)

	var out funded
	status := post(t, srv.URL+"/credential", map[string]any{
		"chain": d.chainFor(t, credProviderID, nonce, 18900),
		"nonce": nonce,
	}, &out)

	require.Equal(t, http.StatusOK, status, "a chain inside the limits the user signed has to fund")
	require.NotNil(t, out.Credential, "a purchase this provider authorised has to come back with the money")

	wantHash, err := sdjwt.SHA256.Digest(offerJWT)
	require.NoError(t, err)
	assert.Equal(t, wantHash, out.Credential.CheckoutHash,
		"the credential has to be scoped to the checkout the closed mandate inside the chain named, and to nothing else")

	receipt, err := ap2.VerifyReceipt(out.Receipt, provider.verifier)
	require.NoError(t, err, "the receipt has to verify against this provider's published key")
	assert.Equal(t, generated.ReceiptResultSuccess, receipt.Result)
}

// TestAPaymentChainAddressedToTheMerchantIsRefusedByTheCredentialProvider is
// the audience binding, and it is what makes a closed mandate under Human Not
// Present per-verifier rather than per-transaction.
//
// The chain here is entirely genuine: the user's limits, the agent's signature,
// a challenge this provider issued minutes ago and the agent duly signed over.
// The only thing wrong with it is that the agent addressed it to the merchant.
// Accepting it would mean one delegation, minted once, spendable at every
// verifier that ever sees it — which is exactly what aud exists to stop, and
// exactly what makes "one chain per verifier" a rule rather than a convention.
func TestAPaymentChainAddressedToTheMerchantIsRefusedByTheCredentialProvider(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	srv := theCredentialProvider(t, d.user, provider)

	nonce := nonceFrom(t, srv)

	var out funded
	status := post(t, srv.URL+"/credential", map[string]any{
		// Addressed to the merchant, presented to the Credential Provider.
		"chain": d.chainFor(t, payeeID, nonce, 18900),
		"nonce": nonce,
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Nil(t, out.Credential, "money must not move on a proof addressed to somebody else")

	receipt, err := ap2.VerifyReceipt(out.Receipt, provider.verifier)
	require.NoError(t, err,
		"a chain has been examined by now, so the refusal is signed evidence rather than a Problem Details document")
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error,
		"the agent's own signature covers aud, so this is a proof of possession that does not hold here")
}

// TestAPaymentChainThatDisclosedNoConstraintIsRefused is the always-on floor,
// reached through a role rather than through the adapter directly.
//
// Nobody misbehaved to produce this. The user signed one limit, that limit
// reads a fact a Credential Provider cannot state, and Minimise correctly
// withheld it — leaving a presentation of no constraints at all. Without the
// floor that arrives as constraint.Report{}, which is satisfied, and the
// provider funds a purchase against limits it never read.
//
// The assertion is on the description rather than only the code, because this
// provider's own RequireConstrained would refuse the same presentation under
// the same code one line later. What is being pinned is the floor, which no
// caller can configure away.
func TestAPaymentChainThatDisclosedNoConstraintIsRefused(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	d := newDelegation(t, pinOnRoute) // the one limit this verifier cannot apply
	srv := theCredentialProvider(t, d.user, provider)

	nonce := nonceFrom(t, srv)

	var out funded
	status := post(t, srv.URL+"/credential", map[string]any{
		"chain": d.chainFor(t, credProviderID, nonce, 18900),
		"nonce": nonce,
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Nil(t, out.Credential,
		"a presentation of no constraints must not read as a mandate that set none, or the user's limits are enforced by nobody")

	receipt, err := ap2.VerifyReceipt(out.Receipt, provider.verifier)
	require.NoError(t, err)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeDisclosureInsufficient, *receipt.Error)
	require.NotNil(t, receipt.ErrorDescription)
	assert.Contains(t, *receipt.ErrorDescription, "disclosed none of them",
		"the floor is what refused this, not this provider's own RequireConstrained, which would answer under the same code")
}

// TestAPaymentChainOverTheUsersCapIsRefusedWithAReceipt is the constraint the
// user actually set, evaluated by the verifier against a subject it built
// itself.
//
// The amount is the agent's own: it signed a closed mandate for $500 against a
// cap of $200, and ap2.PaymentSubject reads that number off the mandate rather
// than taking it from anybody. So the refusal is the user's limit being
// applied to a figure the agent committed to under its own key.
func TestAPaymentChainOverTheUsersCapIsRefusedWithAReceipt(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	srv := theCredentialProvider(t, d.user, provider)

	nonce := nonceFrom(t, srv)

	var out funded
	status := post(t, srv.URL+"/credential", map[string]any{
		"chain": d.chainFor(t, credProviderID, nonce, 50000), // $500 against a $200 cap
		"nonce": nonce,
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Nil(t, out.Credential, "the whole point of the cap is that this does not get funded")

	receipt, err := ap2.VerifyReceipt(out.Receipt, provider.verifier)
	require.NoError(t, err)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeConstraintViolated, *receipt.Error,
		"a broken limit has to be named as one, because that is what the agent acts on when it comes back with a lower price")
}

// TestTheTwoWaysANonceIsWrongAreAnsweredDifferently is the split #116 left half
// done, and the halves are not interchangeable.
//
// A challenge this verifier never issued is refused before any mandate is read,
// so there is nothing to sign an answer about and the answer is Problem Details
// carrying request_malformed. A challenge it *did* issue, attached to a
// delegation signed over a different one, is a proof of possession that does
// not hold on a chain that has been examined — a receipt, naming
// key_binding_invalid.
//
// A verifier that reported the second as request_malformed would leave a
// dispute with no signed statement about a mandate it did look at; one that
// reported the first as a receipt would be signing evidence about nothing.
func TestTheTwoWaysANonceIsWrongAreAnsweredDifferently(t *testing.T) {
	t.Parallel()

	t.Run("a challenge this verifier never issued gets Problem Details", func(t *testing.T) {
		t.Parallel()

		provider := newParty(t, "credprovider")
		d := newDelegation(t, capOnAmount, pinOnPayee)
		srv := theCredentialProvider(t, d.user, provider)

		var body map[string]any
		status := post(t, srv.URL+"/credential", map[string]any{
			"chain": d.chainFor(t, credProviderID, "n-invented-by-the-agent", 18900),
			"nonce": "n-invented-by-the-agent",
		}, &body)

		assert.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"],
			"nothing has been examined, so this is the caller getting the call wrong")
		assert.NotContains(t, body, "receipt",
			"a receipt whose reference points at a mandate nobody read is worse than none")
	})

	t.Run("a live challenge the delegation did not sign gets a receipt", func(t *testing.T) {
		t.Parallel()

		provider := newParty(t, "credprovider")
		d := newDelegation(t, capOnAmount, pinOnPayee)
		srv := theCredentialProvider(t, d.user, provider)

		// Two challenges, both genuinely this provider's and both live. The
		// agent signs over the first and presents the second, which is what a
		// proof lifted from another exchange looks like.
		signed, presented := nonceFrom(t, srv), nonceFrom(t, srv)
		require.NotEqual(t, signed, presented,
			"a challenger that answered one value twice would make this test prove nothing")

		var out funded
		status := post(t, srv.URL+"/credential", map[string]any{
			"chain": d.chainFor(t, credProviderID, signed, 18900),
			"nonce": presented,
		}, &out)

		require.Equal(t, http.StatusUnprocessableEntity, status)
		receipt, err := ap2.VerifyReceipt(out.Receipt, provider.verifier)
		require.NoError(t, err)
		require.NotNil(t, receipt.Error)
		assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error,
			"the nonce is one this provider issued, so what failed is the agent's signature over it")
	})
}

// TestAChainIsNeverSniffedOutOfTheMandateField is the request shape holding the
// line ap2's two interfaces draw.
//
// The adapter gives Human Present and Human Not Present different entry points
// so that a chain cannot reach the one that evaluates no constraints. A handler
// that guessed — parse as a chain, fall back to a mandate — would put that
// entry point back at the transport, where no interface is watching. So the
// mode is the caller's statement, and a body that makes two statements or none
// is refused before anything is parsed.
// **The "both" case has to be otherwise fundable**, or it proves nothing. A
// body carrying a chain this provider would refuse anyway is rejected with or
// without the guard, and a test built that way stays green while the guard is
// deleted — which is how it was written first. So the chain and the nonce here
// are the ones TestFundingADelegatedPaymentMandateScopesTheCredentialToItsCheckout
// funds, and the only difference is the mandate field beside them.
func TestAChainIsNeverSniffedOutOfTheMandateField(t *testing.T) {
	t.Parallel()

	t.Run("both", func(t *testing.T) {
		t.Parallel()

		provider := newParty(t, "credprovider")
		d := newDelegation(t, capOnAmount, pinOnPayee)
		srv := theCredentialProvider(t, d.user, provider)

		nonce := nonceFrom(t, srv)

		var body map[string]any
		status := post(t, srv.URL+"/credential", map[string]any{
			"mandate": "a Human Present mandate nobody asked this provider to read",
			"chain":   d.chainFor(t, credProviderID, nonce, 18900),
			"nonce":   nonce,
		}, &body)

		assert.Equal(t, http.StatusBadRequest, status,
			"the chain alone would have funded this; a request carrying both does not say which mode it means, and preferring one would be the sniffing this shape exists to prevent")
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"])
		assert.NotContains(t, body, "credential",
			"a provider that quietly picked the chain would have paid out here")
	})

	t.Run("neither", func(t *testing.T) {
		t.Parallel()

		provider := newParty(t, "credprovider")
		d := newDelegation(t, capOnAmount)
		srv := theCredentialProvider(t, d.user, provider)

		var body map[string]any
		status := post(t, srv.URL+"/credential", map[string]any{"nonce": "n"}, &body)

		assert.Equal(t, http.StatusBadRequest, status, "there is nothing here to verify at all")
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"])
		assert.NotContains(t, body, "receipt",
			"nothing was parsed, so nothing was examined, and a receipt referencing nothing is worse than none")
	})
}

// TestAProviderThatServesHumanPresentOnlySaysSoRatherThanVerifyingNothing
// covers the deployment that leaves the chain fields unset.
//
// It is the verifier's own gap and not the caller's mistake, so the code is
// verifier_unavailable. Answering request_malformed would send the one party
// that did nothing wrong away to debug a request that was fine.
//
// Two ways to be unable to answer, and the second is why the guard reads
// `Chains == nil || Challenge == nil` rather than naming one. Narrowing it to
// Chains alone reddens nothing by itself — which is exactly the state this
// subtest exists to make visible: a provider holding chain rules and no
// challenger would then nil-dereference inside Challenge.Check instead of
// saying it cannot help.
func TestAProviderThatServesHumanPresentOnlySaysSoRatherThanVerifyingNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		svc  func(d delegation, provider party) *credprovider.Service
	}{
		{
			// No Chains and no Challenge: exactly the service the Human Present
			// tests in roles_test.go stand up.
			name: "no chain rules",
			svc: func(d delegation, provider party) *credprovider.Service {
				challenge, err := crypto.NewChallenger(provider.clock, roles.ChallengeTTL)
				require.NoError(t, err)
				return &credprovider.Service{
					ID:        credProviderID,
					Rules:     ap2.CredentialProviderRules{Issuer: d.user.verifier, Clock: provider.clock},
					Signer:    provider.signer,
					Keys:      provider.keys,
					Clock:     provider.clock,
					Challenge: challenge,
				}
			},
		},
		{
			name: "chain rules but no challenger",
			svc: func(d delegation, provider party) *credprovider.Service {
				rules := ap2.CredentialProviderRules{
					Issuer: d.user.verifier, Clock: provider.clock,
					AgentKey: roles.AgentKey, Audience: credProviderID,
				}
				return &credprovider.Service{
					ID:     credProviderID,
					Rules:  rules,
					Chains: rules,
					Signer: provider.signer,
					Keys:   provider.keys,
					Clock:  provider.clock,
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider := newParty(t, "credprovider")
			d := newDelegation(t, capOnAmount, pinOnPayee)

			handler, err := tc.svc(d, provider).Handler()
			srv := serve(t, handler, err)

			var body map[string]any
			status := post(t, srv.URL+"/credential", map[string]any{
				"chain": d.chainFor(t, credProviderID, "n-anything", 18900),
				"nonce": "n-anything",
			}, &body)

			assert.Equal(t, http.StatusServiceUnavailable, status)
			assert.Equal(t, string(generated.ErrorCodeVerifierUnavailable), body["code"],
				"the request was well formed; this provider simply does not answer that question")
		})
	}
}

// TestTheProcessorSettlesADelegatedPayment is the last link under Human Not
// Present: the credential the provider minted, spent against a chain the
// processor verifies for itself.
//
// The chain presented here is a *different document* from the one the provider
// read — its own audience, its own nonce — which is the property that makes
// "one chain per verifier" visible end to end rather than asserted in a
// comment.
func TestTheProcessorSettlesADelegatedPayment(t *testing.T) {
	t.Parallel()

	processor := newParty(t, "mpp")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	credential := mintCredential(t, d)

	processorSrv := theProcessor(t, d.user, processor)
	processorNonce := nonceFrom(t, processorSrv)

	var out settledOutcome
	status := post(t, processorSrv.URL+"/payment", map[string]any{
		"chain":      d.chainFor(t, processorID, processorNonce, 18900),
		"nonce":      processorNonce,
		"credential": credential,
	}, &out)

	require.Equal(t, http.StatusOK, status)
	assert.True(t, out.Settled,
		"a credential scoped to the checkout the chain names, spent on that checkout, is the whole of what this role checks")

	receipt, err := ap2.VerifyReceipt(out.Receipt, processor.verifier)
	require.NoError(t, err)
	assert.Equal(t, generated.ReceiptResultSuccess, receipt.Result)
}

// TestTheProcessorRefusesACredentialForAnotherPurchaseUnderAChain is
// TestTheProcessorRefusesACredentialForAnotherPurchase's Human Not Present
// counterpart, and it is not a symmetry exercise: the digest the scope check
// compares against comes from a different place in each mode — a verified
// standalone mandate there, the delegated hop of a verified chain here — so a
// chain branch that read it from the request instead would pass every other
// test in this file.
func TestTheProcessorRefusesACredentialForAnotherPurchaseUnderAChain(t *testing.T) {
	t.Parallel()

	processor := newParty(t, "mpp")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	srv := theProcessor(t, d.user, processor)

	elsewhere, err := sdjwt.SHA256.Digest(otherOfferJWT)
	require.NoError(t, err)

	nonce := nonceFrom(t, srv)
	var out settledOutcome
	status := post(t, srv.URL+"/payment", map[string]any{
		"chain": d.chainFor(t, processorID, nonce, 18900),
		"nonce": nonce,
		"credential": generated.PaymentCredential{
			Token:        "tok_for_something_else",
			CheckoutHash: elsewhere,
		},
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.False(t, out.Settled, "money must not move for a purchase this credential does not cover")

	receipt, err := ap2.VerifyReceipt(out.Receipt, processor.verifier)
	require.NoError(t, err)
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeCredentialScopeMismatch, *receipt.Error,
		"the mandates are fine and the money is wrong, which is its own rejection under a chain exactly as it is without one")
}

// TestTheProcessorGuardsItsChainBranchTheSameWay is F2 of the review, and the
// reason it is worth its length is not symmetry.
//
// mpp/service.go is a near-copy of credprovider/service.go, and the realistic
// failure is not either being written wrong today — the reviewer probed all
// three of these and got the right answer from each. It is the *next* edit
// touching one and not the other. Someone reorders mpp.examine's switch, a body
// carrying both fields takes the mandate branch, and a chain-mode caller reaches
// the entry point that evaluates no constraints — precisely what ap2's two
// interfaces exist to make unexpressible — with a green suite, because every
// test naming that guard stopped at the Credential Provider.
//
// So each subtest here is the mutation that would otherwise go unnoticed,
// asserted at the processor.
func TestTheProcessorGuardsItsChainBranchTheSameWay(t *testing.T) {
	t.Parallel()

	t.Run("a body carrying both a mandate and a chain is refused", func(t *testing.T) {
		t.Parallel()

		// Otherwise settleable, on the lesson the Credential Provider's own
		// version of this test had to learn: a request that would be refused
		// anyway is refused with or without the guard, and a test built that
		// way stays green while the guard is deleted.
		processor := newParty(t, "mpp")
		d := newDelegation(t, capOnAmount, pinOnPayee)
		credential := mintCredential(t, d)

		srv := theProcessor(t, d.user, processor)
		nonce := nonceFrom(t, srv)

		var body map[string]any
		status := post(t, srv.URL+"/payment", map[string]any{
			"mandate":    "a Human Present mandate nobody asked this processor to read",
			"chain":      d.chainFor(t, processorID, nonce, 18900),
			"nonce":      nonce,
			"credential": credential,
		}, &body)

		assert.Equal(t, http.StatusBadRequest, status,
			"the chain alone would have settled this; a request carrying both does not say which mode it means")
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"])
		assert.NotEqual(t, true, body["settled"],
			"a processor that quietly picked the chain would have moved money here")
	})

	t.Run("a body carrying neither is refused", func(t *testing.T) {
		t.Parallel()

		processor := newParty(t, "mpp")
		d := newDelegation(t, capOnAmount)
		srv := theProcessor(t, d.user, processor)

		var body map[string]any
		status := post(t, srv.URL+"/payment", map[string]any{"nonce": "n"}, &body)

		assert.Equal(t, http.StatusBadRequest, status, "there is nothing here to settle against")
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"])
		assert.NotContains(t, body, "receipt", "nothing was parsed, so nothing was examined")
	})

	t.Run("a challenge this processor never issued is refused before anything is read", func(t *testing.T) {
		t.Parallel()

		processor := newParty(t, "mpp")
		d := newDelegation(t, capOnAmount, pinOnPayee)
		credential := mintCredential(t, d)
		srv := theProcessor(t, d.user, processor)

		var body map[string]any
		status := post(t, srv.URL+"/payment", map[string]any{
			"chain":      d.chainFor(t, processorID, "n-invented-by-the-agent", 18900),
			"nonce":      "n-invented-by-the-agent",
			"credential": credential,
		}, &body)

		assert.Equal(t, http.StatusBadRequest, status)
		assert.Equal(t, string(generated.ErrorCodeRequestMalformed), body["code"],
			"the agent signed this nonce, so a processor that only compared the signature would accept a challenge it never issued")
		assert.NotContains(t, body, "receipt")
	})

	t.Run("a processor not stood up for chains says so", func(t *testing.T) {
		t.Parallel()

		processor := newParty(t, "mpp")
		d := newDelegation(t, capOnAmount, pinOnPayee)

		// Two ways to be unable to answer, and both are the operator's omission
		// rather than the caller's mistake. The second is the one no mutation
		// finds on its own: narrowing the guard to PaymentChains alone leaves a
		// processor holding rules and no challenger, which nil-dereferences
		// inside Challenge.Check rather than answering anything.
		for _, tc := range []struct {
			name string
			svc  func() *mpp.Service
		}{
			{
				name: "no chain rules",
				svc: func() *mpp.Service {
					challenge, err := crypto.NewChallenger(processor.clock, roles.ChallengeTTL)
					require.NoError(t, err)
					return &mpp.Service{
						ID:        processorID,
						Payments:  ap2.CredentialProviderRules{Issuer: d.user.verifier, Clock: processor.clock},
						Rules:     ap2.MPPRules{Clock: processor.clock},
						Signer:    processor.signer,
						Keys:      processor.keys,
						Clock:     processor.clock,
						Challenge: challenge,
					}
				},
			},
			{
				name: "chain rules but no challenger",
				svc: func() *mpp.Service {
					payments := ap2.CredentialProviderRules{
						Issuer: d.user.verifier, Clock: processor.clock,
						AgentKey: roles.AgentKey, Audience: processorID,
					}
					return &mpp.Service{
						ID:            processorID,
						Payments:      payments,
						PaymentChains: payments,
						Rules:         ap2.MPPRules{Clock: processor.clock},
						Signer:        processor.signer,
						Keys:          processor.keys,
						Clock:         processor.clock,
					}
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				handler, err := tc.svc().Handler()
				srv := serve(t, handler, err)

				var body map[string]any
				status := post(t, srv.URL+"/payment", map[string]any{
					"chain": d.chainFor(t, processorID, "n-anything", 18900),
					"nonce": "n-anything",
					"credential": generated.PaymentCredential{
						Token: "tok_anything", CheckoutHash: "anything",
					},
				}, &body)

				assert.Equal(t, http.StatusServiceUnavailable, status)
				assert.Equal(t, string(generated.ErrorCodeVerifierUnavailable), body["code"],
					"the request was well formed; this processor simply does not answer that question")
			})
		}
	})
}

// TestAPaymentChainAddressedToTheCredentialProviderIsRefusedByTheProcessor is
// the audience binding at the other end of the payment leg.
//
// It holds today only because both roles route through one function, which is
// exactly why it needs saying here: the property a reader cares about is that
// **this processor** refuses a proof addressed to somebody else, and a shared
// implementation is a reason it currently works rather than a reason it will
// keep working.
//
// The chain is otherwise perfect — the user's limits, the agent's signature,
// this processor's own live challenge — and the credential really is scoped to
// the purchase. The only thing wrong is the aud claim.
func TestAPaymentChainAddressedToTheCredentialProviderIsRefusedByTheProcessor(t *testing.T) {
	t.Parallel()

	processor := newParty(t, "mpp")
	d := newDelegation(t, capOnAmount, pinOnPayee)
	credential := mintCredential(t, d)

	srv := theProcessor(t, d.user, processor)
	nonce := nonceFrom(t, srv)

	var out settledOutcome
	status := post(t, srv.URL+"/payment", map[string]any{
		// Addressed to the Credential Provider, presented to the processor.
		"chain":      d.chainFor(t, credProviderID, nonce, 18900),
		"nonce":      nonce,
		"credential": credential,
	}, &out)

	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.False(t, out.Settled,
		"one delegation spendable at every verifier that sees it is what aud exists to stop, and money is the last place to discover otherwise")

	receipt, err := ap2.VerifyReceipt(out.Receipt, processor.verifier)
	require.NoError(t, err,
		"a chain has been examined by now, so the refusal is signed evidence rather than a Problem Details document")
	require.NotNil(t, receipt.Error)
	assert.Equal(t, generated.ErrorCodeKeyBindingInvalid, *receipt.Error)
}

// TestTheHumanPresentPathIsUntouched is the guard on everything above.
//
// Both roles grew a second entry point, and the failure that would be easiest
// to miss is not a broken chain — it is a Human Present purchase quietly
// routed somewhere new, or refused for want of a nonce it never had. So the
// original flow is run end to end through both roles, on a service configured
// exactly as the chain-serving one is, and nothing in the request mentions a
// chain at all.
func TestTheHumanPresentPathIsUntouched(t *testing.T) {
	t.Parallel()

	provider := newParty(t, "credprovider")
	processor := newParty(t, "mpp")
	user := newParty(t, "user")
	surfaceSrv := theSurface(t, user)

	providerSrv := theCredentialProvider(t, user, provider)
	processorSrv := theProcessor(t, user, processor)

	var approved struct {
		PaymentMandate string `json:"payment_mandate"`
	}
	require.Equal(t, http.StatusOK, post(t, surfaceSrv.URL+"/approve", map[string]any{
		"checkout": offerJWT,
		"payment":  paymentBody(),
	}, &approved), "the user approving a purchase on the Trusted Surface")

	t.Run("the Credential Provider funds it", func(t *testing.T) {
		var out funded
		status := post(t, providerSrv.URL+"/credential", map[string]any{
			"mandate": approved.PaymentMandate,
		}, &out)

		require.Equal(t, http.StatusOK, status,
			"a mandate the user signed themselves carries no key binding, and a provider that had grown to require one would refuse every Human Present purchase")
		require.NotNil(t, out.Credential)

		wantHash, err := sdjwt.SHA256.Digest(offerJWT)
		require.NoError(t, err)
		assert.Equal(t, wantHash, out.Credential.CheckoutHash,
			"scoped to the same checkout it always was")

		t.Run("and the processor settles it", func(t *testing.T) {
			var settled struct {
				Settled bool `json:"settled"`
			}
			status := post(t, processorSrv.URL+"/payment", map[string]any{
				"mandate":    approved.PaymentMandate,
				"credential": out.Credential,
			}, &settled)

			require.Equal(t, http.StatusOK, status)
			assert.True(t, settled.Settled,
				"the Human Present leg has to reach the same answer it did before either role knew what a chain was")
		})
	})
}
