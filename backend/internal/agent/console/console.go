// Package console is the Shopping Agent's own read surface: start a watch, and
// read where each mandate in it stands.
//
// The agent is the only party that knows where an open mandate stands. No
// verifier does — internal/adapters/ap2's rule sets hold no state, and each one
// is shown a single presentation carrying no record of any other — so a console
// reading a mandate's state from anywhere else would be reading an inference.
// This package is the owner serving what it owns.
//
// # The state may be served by its owner; it must not become a protocol artefact
//
// That distinction is the whole of this package's design, and three things keep
// it structural rather than a sentence somebody has to remember:
//
//   - **Nothing in contracts/ gains a mandate-state field.** The wire shape is
//     the hand-written DTO in view.go, which is the serialisation rule working
//     as written — a database column, a protocol claim and a screen's JSON are
//     three different reasons to change, and the canonical model carries none of
//     them. TestNoContractCarriesAMandateState walks the schemas and fails if one
//     ever does.
//   - **The spelling comes from authz.MandateState.String()**, so there is no
//     second table to drift from the first.
//     TestTheStateNamesTheConsoleServesAreTheOnesTheMachineWrites drives a watch
//     through all three and reads them back off the wire.
//   - **No route accepts a state.** The DTO is only ever marshalled; the request
//     shapes in this file decode a prompt, an item and a quantity, and nothing
//     else, and the paths carry a watch's name and an attempt's number. An agent
//     that could be *told* where its mandate stood would be taking somebody
//     else's word for its own bookkeeping.
//
// # The agent reports what it presented and what came back, never a verdict
//
// An attempt row carries the receipt **tokens** and, when something failed, the
// error the delivery returned. It carries no generated.ErrorCode, and that
// absence is deliberate: purchase.go is explicit that "an agent reporting that a
// mandate was verified would be reporting somebody else's decision as its own",
// and a rendered `constraint_violated` here would be exactly that — the buyer
// narrating the verifier's finding. Whoever wants the code decodes the receipt,
// which is signed, and which is #21's Mandate Inspector.
//
// The presentations themselves are the other half of that sentence, and they are
// a sub-resource: GET /watches/{id}/attempts/{n}/presented answers with the four
// chains one attempt put on the wire, each beside the audience it was addressed
// to. It is the same rule at its sharpest — these are the documents, so a status
// field beside one would read as the document's own — and it is the one thing an
// Inspector cannot derive from anywhere else, because what a verifier was shown
// is the difference between issuance and presentation. Nothing here decodes one;
// that is the browser's job, and it is what the chain being served unaltered is
// for.
//
// Nothing in this package is evidence, for the same reason the collector is not:
// it is the buyer's account of a transaction. `console-containment` in
// backend/.golangci.yml is what makes that a property — only cmd/agent may
// import this package, so a merchant cannot read the buyer's opinion as fact.
package console

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
)

// Watcher is the agent this console drives.
//
// Four methods. Three of them are moments: Propose inside one request,
// Authorise inside another, Watch in the goroutine that one leaves behind. The
// fourth, Examples, is not a moment at all — it is a static lookup with
// nothing to call it at, answered from whatever the interpreter was built
// with. One port rather than a Proposer beside a Watcher: two fields would
// allow a Service wired to propose from one agent and watch with another,
// which is a state nobody wants and nothing would prevent.
type Watcher interface {
	// Propose reads the prompt and settles on an offer, signing nothing.
	Propose(ctx context.Context, prompt, item string) (agent.Proposal, error)

	// Examples lists the sentences this agent's interpreter is scripted for,
	// empty when it has none. A model-backed interpreter has no menu because
	// any sentence is admissible.
	Examples() []string

	// Authorise puts the interpretation of prompt in front of the user and
	// returns what they signed. item, when set, is an offer the caller has
	// already chosen; see agent.Intent.Item.
	Authorise(ctx context.Context, prompt, item string) (agent.Authorisation, error)

	// Watch spends that authorisation until it buys, runs out of schedule or
	// ctx ends, telling p about each attempt as it is applied.
	Watch(ctx context.Context, auth agent.Authorisation, quantity int, p agent.Progress) (agent.Watched, error)
}

// DefaultLimit is how many watches one console will hold.
//
// Eight, and chosen against the demonstration rather than measured. Two watches
// are two authorisations and two open-mandate pairs, so they are two Trackers
// and no rule is broken — agent.Tracker's "not safe to share" is about one
// tracker, not one agent. What a bound buys is that a console page with a button
// on it cannot turn a mock stack into a few hundred goroutines each polling a
// merchant, which is a way to make a working demonstration look broken.
const DefaultLimit = 8

// ErrTooManyWatches means this console is already holding as many watches as it
// will.
//
// A refusal before the Trusted Surface is called rather than after, because an
// authorisation that succeeds into a full registry has collected a signature
// nothing is going to spend.
var ErrTooManyWatches = errors.New("console: this agent is already watching as many things as it will")

// Service is the console: the watches this process has started, and the routes
// that read them.
//
// The zero value is not usable — Watcher and Clock are both required — and a
// value must not be copied once Handler or Start has been called.
type Service struct {
	// Watcher is the agent this console drives. Required.
	Watcher Watcher

	// Clock is what the idempotency middleware ages its records against.
	// Required, and it is the injected clock for the standing reason.
	Clock authz.Clock

	// Limit is how many watches this console will hold at once. Zero means
	// DefaultLimit.
	Limit int

	// mu guards the three fields below and nothing inside a Run — a Run carries
	// its own lock, because the watch goroutine writes to one while a request is
	// reading another.
	mu sync.Mutex
	// pending counts authorisations in flight: started, not yet registered.
	// Without it two simultaneous requests both see room in a registry that has
	// room for one of them.
	pending int
	order   []*Run
	byID    map[string]*Run
}

// Watching is what one watch is started with.
//
// It carries no state and no constraint. The sentence is the user's, the item is
// the merchant's, the quantity is a number — and everything that says on what
// terms the purchase may happen comes back from the Trusted Surface, signed.
type Watching struct {
	// Prompt is what the user typed. Required.
	Prompt string
	// Item, when set, is the offer the caller already picked; see
	// agent.Intent.Item for why naming one skips the search and nothing else.
	Item string
	// Quantity is how many to buy. Zero means one.
	Quantity int
}

// Handler returns the console's routes, wrapped in the middleware every role
// runs.
//
// Through roles.Middleware rather than from the bare mux, and that is not
// housekeeping: POST /watches spends a user's open mandate, so a double-clicked
// button has to be answered from the store rather than run twice.
// TestStartingAWatchWithoutAnIdempotencyKeyIsRefused and
// TestARepeatedKeyStartsOneWatch are what fail when this is built any other way.
func (s *Service) Handler() (http.Handler, error) {
	if s.Watcher == nil {
		return nil, errors.New("console: a console needs an agent to start watches with")
	}
	if s.Clock == nil {
		return nil, errors.New("console: a console needs a clock; the idempotency store ages its records against one")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /watches", s.start)
	mux.HandleFunc("GET /watches", s.list)
	mux.HandleFunc("GET /watches/{id}", s.read)

	mux.HandleFunc("POST /proposals", s.propose)
	// A GET, so it sits outside the idempotency middleware by method semantics
	// rather than by a route exemption — the argument GET /watches/{id}/… above
	// already makes here. It reads a table fixed at construction.
	mux.HandleFunc("GET /examples", s.examples)

	// A GET, which is what settles the idempotency question for it: RFC 9110
	// §9.2.1 safe methods sit outside the middleware by method semantics rather
	// than by a route exemption, which is the argument GET /nonce and GET
	// /search already make in this repository. It reads what an attempt put on
	// the wire minutes ago and changes nothing, so there is no state a repeated
	// call could double — and the standing rule that every state-changing
	// operation takes an idempotency key is untouched, because this changes
	// nothing.
	mux.HandleFunc("GET /watches/{id}/attempts/{n}/presented", s.readPresented)

	// Matching the collector's, byte for byte, because deploy/demo.json wants a
	// health check and a runner that had to learn a second shape for the eighth
	// process would be a runner with a special case in it.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	return roles.Middleware(s.Clock, mux)
}

// Start authorises a prompt and leaves a watch running against what was signed.
//
// # Synchronous authorisation, asynchronous watch
//
// The signature is collected before this returns, so what comes back names an
// authorisation that exists: the two open mandates, the sentences the surface
// rendered, the item and the expiry. The polling loop is what runs on afterwards.
//
// The other order is worse in a way that is easy to miss. Registering first and
// authorising in the goroutine returns an identifier for something that may fail
// a second later — a row the console has to draw and cannot explain — and it
// interacts badly with the idempotency store, which would remember a 201 for a
// watch that never came into being.
//
// # ctx governs both halves
//
// Authorise runs under it and so does the watch, so the caller decides how a
// watch ends. cmd/agent hands the process's signal context to the watch it
// starts on startup, which is what makes Ctrl-C leave that run reading "stopped";
// the HTTP handler hands context.WithoutCancel(r.Context()), because a browser
// navigating away is not a reason to abandon an open mandate the user has just
// signed, and the request's correlation ID travels either way.
func (s *Service) Start(ctx context.Context, in Watching) (*Run, error) {
	if s.Watcher == nil {
		return nil, errors.New("console: a console needs an agent to start watches with")
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, errors.New("console: a watch needs the sentence the user typed")
	}

	quantity := in.Quantity
	if quantity < 1 {
		quantity = 1
	}

	if err := s.reserve(); err != nil {
		return nil, err
	}

	auth, err := s.Watcher.Authorise(ctx, in.Prompt, in.Item)
	if err != nil {
		s.release()
		return nil, err
	}

	id, err := newID()
	if err != nil {
		s.release()
		return nil, err
	}

	run := &Run{
		id: id,
		// From the context rather than minted here: under the handler it is the
		// identifier transport.Correlation put on the request, so the row and
		// every event the roles emit for it carry the same label. It is empty
		// when nothing set one, which is a thinner screenshot and never a failed
		// watch.
		correlationID: obs.CorrelationID(ctx),
		typed:         in.Prompt,
		signed:        append([]string(nil), auth.Rendered...),
		item:          auth.Item,
		quantity:      quantity,
		expiresAt:     auth.ExpiresAt,
		index:         make(map[string]int),
		done:          make(chan struct{}),
	}
	s.register(run)

	go func() {
		defer close(run.done)
		watched, err := s.Watcher.Watch(ctx, auth, quantity, run)
		run.finished(watched, err)
	}()

	return run, nil
}

// reserve takes a slot, or refuses.
func (s *Service) reserve() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := s.Limit
	if limit < 1 {
		limit = DefaultLimit
	}
	if len(s.order)+s.pending >= limit {
		return fmt.Errorf("%w: %d is the most it will hold at once", ErrTooManyWatches, limit)
	}
	s.pending++
	return nil
}

// release gives a reserved slot back, for an authorisation that did not happen.
func (s *Service) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending--
}

// register turns a reserved slot into a watch anybody can read.
func (s *Service) register(run *Run) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pending--
	if s.byID == nil {
		s.byID = make(map[string]*Run)
	}
	s.byID[run.id] = run
	// Appended, so GET /watches reads oldest first and a console that reloads
	// finds its rows where it left them.
	s.order = append(s.order, run)
}

// lookup finds a watch by the name it was given.
func (s *Service) lookup(id string) (*Run, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.byID[id]
	return run, ok
}

// all returns the watches in the order they were started.
func (s *Service) all() []*Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Run(nil), s.order...)
}

// start is POST /watches.
func (s *Service) start(w http.ResponseWriter, r *http.Request) {
	// Prompt, item and quantity. **Nothing here reads a state**, and that is the
	// third of the three arrangements in the package comment: the agent states
	// where its own mandates stand and nobody may send it an answer.
	var req struct {
		Prompt   string `json:"prompt"`
		Item     string `json:"item"`
		Quantity int    `json:"quantity"`
	}
	if !roles.DecodeJSON(w, r, &req) {
		return
	}

	run, err := s.Start(context.WithoutCancel(r.Context()), Watching{
		Prompt:   req.Prompt,
		Item:     req.Item,
		Quantity: req.Quantity,
	})
	switch {
	case errors.Is(err, ErrTooManyWatches):
		// Plain rather than Problem Details, and the reason is this branch's own
		// theme: generated.ErrorCode is a verifier's vocabulary for what is
		// wrong with a mandate, and an agent minting an entry in it to describe
		// its own bookkeeping would be the same overreach as reporting a
		// verdict. The request-handling codes below are a different thing — they
		// describe the call, not a mandate — which is why roles.DecodeJSON above
		// answers with one.
		http.Error(w, err.Error(), http.StatusTooManyRequests)
		return
	case errors.Is(err, interpret.ErrNoScript),
		errors.Is(err, interpret.ErrNoConstraints),
		errors.Is(err, agent.ErrNothingToBuy):
		// The request was well formed and this agent cannot turn it into a
		// watch: a sentence its interpreter has no reading for, an
		// interpretation that placed no limits at all, or a search that matched
		// nothing this merchant sells. All three are the agent's own account of
		// its own failure, which is a different thing from reporting a
		// verifier's verdict — nobody has been asked to authorise anything yet.
		//
		// Told apart from the arm below because a console does different things
		// with them. #109's picker offers the five sentences the scripted
		// interpreter knows, so "this is not one of them" is something the
		// screen can say; "the Trusted Surface did not answer" is not.
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		// Including a refusal from the Trusted Surface: the user did not sign,
		// so there is no watch and no row. Reported as this agent's own failure
		// rather than translated, because translating it is the thing it must
		// not do.
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	roles.OK(w, http.StatusCreated, run.started())
}

// propose is POST /proposals.
//
// A pure function of the prompt: interpret, search, narrow, answer, remember
// nothing. r.Context() rather than context.WithoutCancel, unlike start below —
// a proposal that outlives the request has nobody to answer, because nothing
// is signed and nothing is registered for a later caller to read.
//
// The error mapping is Service.start's, taken as it stands rather than restated:
// that switch already carries the reasoning, and a second table would be a second
// truth about the same errors. There is no ErrTooManyWatches arm because nothing
// is reserved here.
func (s *Service) propose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
		Item   string `json:"item"`
	}
	if !roles.DecodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		http.Error(w, "console: a proposal needs the sentence the user typed", http.StatusUnprocessableEntity)
		return
	}

	proposal, err := s.Watcher.Propose(r.Context(), req.Prompt, req.Item)
	switch {
	case errors.Is(err, interpret.ErrNoScript),
		errors.Is(err, interpret.ErrNoConstraints),
		errors.Is(err, agent.ErrNothingToBuy):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	roles.OK(w, http.StatusOK, proposed{
		Prompt:         req.Prompt,
		Constraints:    proposal.Constraints,
		AgentKey:       proposal.AgentKey,
		Item:           proposal.Item,
		Offer:          proposal.Offer,
		WatchSlotsFree: s.free(),
	})
}

// examples is GET /examples.
func (s *Service) examples(w http.ResponseWriter, _ *http.Request) {
	menu := s.Watcher.Examples()
	if menu == nil {
		// A named empty array rather than null, so a caller iterating it needs
		// no branch for the model-backed case.
		menu = []string{}
	}
	roles.OK(w, http.StatusOK, examples{Examples: menu})
}

// free is how many more watches this console will hold, counting the ones in
// flight. It reserves nothing.
func (s *Service) free() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	limit := s.Limit
	if limit < 1 {
		limit = DefaultLimit
	}
	return max(0, limit-len(s.order)-s.pending)
}

// list is GET /watches.
func (s *Service) list(w http.ResponseWriter, _ *http.Request) {
	runs := s.all()
	out := make([]summary, 0, len(runs))
	for _, run := range runs {
		out = append(out, run.summary())
	}
	// A named field rather than a bare array, so the answer has somewhere to
	// grow a cursor or a count without every reader changing shape.
	roles.OK(w, http.StatusOK, map[string]any{"watches": out})
}

// read is GET /watches/{id}.
func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	run, ok := s.lookup(r.PathValue("id"))
	if !ok {
		http.Error(w, "console: no watch by that name", http.StatusNotFound)
		return
	}
	roles.OK(w, http.StatusOK, run.view())
}

// readPresented is GET /watches/{id}/attempts/{n}/presented.
//
// Three ways to answer 404 and a different sentence for each: a watch nobody
// started, a number that is not one, and an attempt that watch never made are
// three different mistakes, and a caller given one message for all of them
// cannot tell a stale identifier from a number it counted wrong.
func (s *Service) readPresented(w http.ResponseWriter, r *http.Request) {
	run, ok := s.lookup(r.PathValue("id"))
	if !ok {
		http.Error(w, "console: no watch by that name", http.StatusNotFound)
		return
	}

	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		http.Error(w, "console: an attempt is named by its number, counting from one",
			http.StatusNotFound)
		return
	}

	out, ok := run.presented(n)
	if !ok {
		// Not an empty presentation. An attempt that does not exist and an
		// attempt that presented nothing are different facts, and this endpoint
		// exists to be believed about the second one — see Run.presented. It is
		// TestAnUnknownWatchIsNotFound's reasoning one level down.
		http.Error(w, "console: this watch has no attempt by that number", http.StatusNotFound)
		return
	}
	roles.OK(w, http.StatusOK, out)
}

// newID mints the name a watch is known by.
//
// Eight characters of base64url over six random bytes, on obs.NewCorrelationID's
// reasoning and for the same audience: it is read off a screen, typed into a
// curl command and put in a screenshot, and a UUID is four times as long for
// entropy nothing here needs. It is not a secret and guessing one buys nothing —
// this surface is the agent talking to its own console.
func newID() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("console: naming the watch: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Compile-time proof that a Run is what the watch reports to. It is what makes
// a method added to or renamed on the port fail here rather than at the go
// statement in Start, where the failure would name a goroutine instead of an
// interface.
var _ agent.Progress = (*Run)(nil)
