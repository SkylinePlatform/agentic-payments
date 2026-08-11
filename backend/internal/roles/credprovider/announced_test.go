package credprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/adapters/ap2"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/clock"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/credprovider"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// POST /credential signs this role's receipt and then mints the credential the
// money comes out of, and this file is about what the event log may say in
// between.
//
// # The defect this was written red against
//
// fund emitted receipt_issued the moment ap2.IssueReceipt returned, and then
// called mint. A failure there answered 503 and dropped the receipt, having
// already announced it; transport.Idempotency does not remember a 5xx, so the
// retry ran the handler again and announced a second one for one presentation.
// The shape is the merchant's next door, and #212's before it.
//
// # Why the Trusted Surface's fix does not transfer, in the sharpest form
//
// #212 made a pair of signatures one unit of work that does not take the
// caller's cancellation. mint takes no context at all: there is nothing here
// for context.WithoutCancel to be applied to, and the failure it removes is not
// the failure this role has. What is left is the ordering — the log names a
// receipt at the moment the caller is handed one, and not before.
//
// # The reachability this cost, stated rather than hidden
//
// mint fails only when its entropy source does, which crypto/rand practically
// never does. That is why the Service takes an Entropy reader: an untestable
// branch handing out the thing that stands in for money is worse than a field
// whose doc says to leave it nil.

// errNoEntropy is the failure that arrives after the receipt exists.
var errNoEntropy = errors.New("the entropy source refused")

// TestAReceiptThePayerNeverGetsIsNeverAnnounced is the merchant's property one
// role along, and it rests on the same reading of ADR 0003: the log is
// observability and never evidence, so nobody can appeal to a false line in it
// and everybody reads it.
func TestAReceiptThePayerNeverGetsIsNeverAnnounced(t *testing.T) {
	t.Parallel()

	t.Run("a credential that cannot be minted announces no receipt", func(t *testing.T) {
		t.Parallel()

		entropy := &refusingEntropy{}
		events, log := recordingEmitter(t)
		p := newProvider(t, events, entropy)

		answer := p.fund(t, p.mandate(t))
		require.Equal(t, http.StatusServiceUnavailable, answer.Code,
			"a provider that cannot mint has failed for its own reasons, and the caller has to "+
				"be told it may try again")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		require.Equal(t, 1, entropy.reads(),
			"the test is worthless unless the receipt had already been signed when the failure "+
				"arrived — mint runs after IssueReceipt, so an entropy source that was never "+
				"read means the handler stopped short of the moment this is about")
		assert.Zero(t, log.issued(),
			"the receipt was signed and then dropped, so nobody holds it; a line saying it was "+
				"issued is the log naming an artefact that does not exist, and the retry a "+
				"released 5xx invites would add a second one for the same presentation")
	})

	t.Run("and the control: a credential that is minted announces one", func(t *testing.T) {
		t.Parallel()

		events, log := recordingEmitter(t)
		p := newProvider(t, events, nil)

		answer := p.fund(t, p.mandate(t))
		require.Equal(t, http.StatusOK, answer.Code, "the credential this role exists to mint")

		var out struct {
			Receipt    string                       `json:"receipt"`
			Credential *generated.PaymentCredential `json:"credential"`
		}
		require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &out), "reading the answer")
		require.NotEmpty(t, out.Receipt, "the receipt the log line below is about")
		require.NotNil(t, out.Credential, "and the credential it travels with")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		assert.Equal(t, 1, log.issued(),
			"a count that cannot go up measures nothing above it")
	})

	t.Run("and a refusal, which is answered with a receipt and announces it", func(t *testing.T) {
		t.Parallel()

		events, log := recordingEmitter(t)
		p := newProvider(t, events, nil)

		// Signed by somebody who is not the user this provider verifies
		// against. AP2 answers a refusal with a receipt and no credential, so
		// this is the branch that hands one over without ever reaching mint.
		answer := p.fund(t, p.mandateFrom(t, p.stranger))
		require.Equal(t, http.StatusUnprocessableEntity, answer.Code,
			"a mandate nobody this provider trusts signed")

		require.NoError(t, events.Close(t.Context()), "draining the event log")
		assert.Equal(t, 1, log.issued(),
			"moving the announcement to where the receipt is handed over must not lose the "+
				"branch that hands one over without minting anything")
	})
}

// provider is a standing Credential Provider and the keys around it.
type provider struct {
	handler  http.Handler
	user     authz.Signer
	stranger authz.Signer
	blinder  *sdjwt.Blinder
}

// newProvider stands up a Human Present Credential Provider: the user signed
// the closed Payment Mandate themselves, at the Trusted Surface.
//
// entropy may be nil, which is crypto/rand and what any deployment uses.
func newProvider(t *testing.T, events *obs.Emitter, entropy io.Reader) provider {
	t.Helper()

	clk := clock.NewFake(theHourTheUserPaid)
	key := func(name string) (authz.Signer, authz.Verifier, authz.KeySetPublisher) {
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

	userSigner, userVerifier, _ := key("user")
	strangerSigner, _, _ := key("stranger")
	providerSigner, _, providerKeys := key("credprovider")

	blinder, err := sdjwt.NewBlinder()
	require.NoError(t, err, "building the blinder")

	svc := &credprovider.Service{
		ID:      "mock-credential-provider",
		Rules:   ap2.CredentialProviderRules{Issuer: userVerifier, Clock: clk},
		Signer:  providerSigner,
		Keys:    providerKeys,
		Clock:   clk,
		Events:  events,
		Entropy: entropy,
	}

	handler, err := svc.Handler()
	require.NoError(t, err, "building the provider handler")

	return provider{handler: handler, user: userSigner, stranger: strangerSigner, blinder: blinder}
}

// mandate signs the closed Payment Mandate a user would have approved.
func (p provider) mandate(t *testing.T) string {
	t.Helper()
	return p.mandateFrom(t, p.user)
}

// mandateFrom is mandate with the key named, so a test can present one this
// provider will refuse without spoiling anything else about it.
func (p provider) mandateFrom(t *testing.T, signer authz.Signer) string {
	t.Helper()

	// The offer is any string: this role never sees the checkout, only the
	// digest of it that the mandate carries. See fund's own comment.
	const offer = "the merchant's signed offer, which this role is never shown"
	payment, err := ap2.IssuePayment(t.Context(), signer, generated.PaymentMandate{
		Payee:             generated.Merchant{ID: "air-serbia", Name: "Air Serbia"},
		PaymentAmount:     generated.Amount{Amount: 18900, Currency: "USD"},
		PaymentInstrument: generated.PaymentInstrument{ID: "card-4242", Type: "CARD"},
	}, offer, p.blinder)
	require.NoError(t, err, "signing the Payment Mandate")

	return payment.String()
}

// fund presents one mandate and returns what the caller was told.
//
// ServeHTTP directly rather than through a server, so the handler runs on the
// test goroutine: everything this file counts is then a synchronised read
// rather than a race with a connection.
func (p provider) fund(t *testing.T, mandate string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]any{"mandate": mandate})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/credential",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(transport.KeyHeader, t.Name())

	rec := httptest.NewRecorder()
	p.handler.ServeHTTP(rec, req)
	return rec
}

// refusingEntropy is a source that has none, and counts the asking.
//
// The count is what proves the handler reached mint, which is what proves the
// receipt was already signed when the failure arrived. Guarded rather than a
// bare int: this file drives the handler on the test goroutine, so nothing here
// needs the mutex today, and a helper that is safe only at some call sites is
// one the next caller gets wrong.
type refusingEntropy struct {
	mu sync.Mutex
	n  int
}

func (e *refusingEntropy) Read([]byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.n++
	return 0, errNoEntropy
}

func (e *refusingEntropy) reads() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.n
}

// recordingEmitter returns an Emitter writing into a log the test can read once
// it has closed it.
//
// Close performs a final drain and waits for the sender to stop, so reading the
// log after it is a synchronised read from the test goroutine rather than a
// poll. The cleanup closes it again for a test that returned early; a second
// Close is a no-op.
func recordingEmitter(t *testing.T) (*obs.Emitter, *receiptLog) {
	t.Helper()

	log := &receiptLog{}
	emitter, err := obs.NewEmitter(clock.NewFake(theHourTheUserPaid), "credprovider",
		obs.WithSink(log))
	require.NoError(t, err, "building the emitter")
	t.Cleanup(func() { _ = emitter.Close(context.Background()) })
	return emitter, log
}

// receiptLog is an obs.Sink that keeps what this provider said.
//
// Hand-rolled rather than generated: .mockery.yml writes a mock into the
// package that owns the interface, so obs.MockSink is compiled only into that
// package's own test binary. The mutex is why that rule exists — Send runs on
// the Emitter's sender goroutine — and issued() is called from the test
// goroutine after Close.
type receiptLog struct {
	mu     sync.Mutex
	events []obs.Event
}

func (l *receiptLog) Send(_ context.Context, batch []obs.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, batch...)
	return nil
}

// issued counts the receipts this log says came into being.
func (l *receiptLog) issued() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	var n int
	for _, ev := range l.events {
		if ev.Kind == obs.KindReceiptIssued {
			n++
		}
	}
	return n
}

// theHourTheUserPaid is the instant every clock in this file reads.
var theHourTheUserPaid = time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
