package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The discovery half of Human Not Present: everything that happens once, before
// the user walks away.
//
// It is the only part of this flow a model is allowed anywhere near, and even
// here the model is a proposer — what it returns is put in front of the user by
// the Trusted Surface, signed there, and evaluated later by verifiers that never
// see the prompt. Nothing below this file calls an interpreter again: the watch
// loop and the four delegations are deterministic code, which is what beat 4 of
// the built scenario is a screenshot of.

// The query parameters this agent builds the merchant's URLs from.
//
// Spelled here rather than taken from internal/roles/merchant, which exports
// ItemParam, QuantityParam and SearchParam for exactly this caller. The agent is
// a *client* of that endpoint, and importing the seller's package to read three
// strings would link its catalogue, its price schedules, its verification rules
// and its HTTP processor into the buyer — which is not a dependency the agent
// has, and reads as one in every tool that looks at the import graph.
//
// The two spellings are held in step by a test rather than by this comment:
// TestTheAgentSpellsTheMerchantsQueryParameters compares each of these against
// the merchant's own constant. That test can import the merchant because a test
// import is not part of the package's build graph, which is the whole reason the
// arrangement is available.
const (
	itemParam     = "item"
	quantityParam = "quantity"
	searchParam   = "constraints"
)

// itemFieldPrefix and itemIDField are the constraint field names this file
// names, and the only two it does.
//
// Naming a field is not evaluating one. The agent never parses a constraint,
// never builds a subject and never reaches a verdict — internal/agent does not
// import internal/core/authz/constraint, and
// TestTheAgentCannotReachAConstraintEvaluator is what keeps that true. What it
// does here is decide which of the user's limits describe *what* to go looking
// for, which is a question about a search query rather than about a purchase.
const (
	itemFieldPrefix = "item."
	itemIDField     = "item.id"
)

// ErrNothingToBuy means discovery found no candidate to watch.
//
// A refusal rather than an empty result, on the reasoning interpret.ErrNoConstraints
// already applies one layer up: a watch with no item is a process that will sit
// there polling nothing, and the failure would surface as a demonstration where
// nothing ever happens rather than as an error anybody can act on.
var ErrNothingToBuy = errors.New("agent: nothing in this merchant's catalogue matches what the user described")

// Intent is what the discovery half is given: the sentence the user typed, the
// interpreter that reads it, and the key the agent wants endorsed.
//
// The interpreter is a parameter rather than a field on Client because it is
// used exactly once, here, and a Client carrying one would suggest that some
// later call might reach it. None does — that is hard rule 2, and the shape is
// what makes it visible.
type Intent struct {
	// Prompt is what the user typed.
	Prompt string
	// Interpreter turns it into constraints. The demo wires interpret.Demo(),
	// a scripted table; the model-backed implementation is #17's.
	Interpreter interpret.IntentInterpreter
	// AgentKey is the public half of the key this agent signs delegations with,
	// as roles.PublicKey reads it out of the agent's own key set. It ends up in
	// both open mandates' cnf claim.
	AgentKey generated.PublicKey
}

// Authorisation is what the user signed, together with the two things the agent
// needs afterwards that are not in the mandates it can read.
//
// It is the whole of what the watch loop carries forward. Everything in it came
// from somebody else — the constraints from the interpreter, the mandates and
// the instrument from the Trusted Surface, the item from the merchant's own
// catalogue — which is the property that makes the loop below deterministic.
type Authorisation struct {
	// Item is the catalogue offer the watch will poll, and the one the
	// constraints were narrowed to.
	Item string

	// Constraints are the limits as signed: the interpretation, plus the one
	// constraint that narrows it to Item.
	Constraints []generated.Constraint

	// The two open mandates, signed by the user, in SD-JWT compact
	// serialisation.
	OpenCheckoutMandate string
	OpenPaymentMandate  string

	// Rendered is what the surface said each constraint means, in the order
	// they were signed. The agent shows it and never acts on it.
	Rendered []string

	// ExpiresAt is when the pair stops authorising anything.
	ExpiresAt time.Time

	// Instrument is the payment instrument the surface pinned into the open
	// Payment Mandate. The closed one has to reproduce it unchanged or
	// authz.checkPinned refuses the purchase, and the surface is the only party
	// that can honestly say what it pinned — see the field on surface.authorised.
	Instrument generated.PaymentInstrument
}

// Authorise runs the discovery half: interpret, search, narrow, and collect the
// user's signature over the result.
//
// # The order, and why each step needs the one before it
//
// The interpretation is what the user is shown, so it has to exist before the
// surface is called. The search is what turns "a flight to Palma" into a
// specific thing this merchant sells, so it has to run before the narrowing. And
// the narrowing has to happen before the signature, because the point of it is
// that the user approves buying *that* item rather than anything matching a
// description — an open mandate constraining only a category authorises every
// offer in it, for as long as it lives.
//
// # The search is not run with the constraint set the user signs
//
// It is run with the constraints that name *what* to buy — the ones whose field
// sits under `item.` — and not with the ones that say on what terms. That is a
// deliberate narrowing of the query and it is worth being exact about why,
// because the obvious reading is that the agent is dropping a limit.
//
// It is not: the full set, price bound included, is what goes to the Trusted
// Surface, what the user signs, and what every verifier evaluates. Nothing the
// agent does here changes what is enforced.
//
// What it changes is what discovery finds. The built scenario's sentence is
// "buy a flight to Palma **when it drops below $200**", and the price is $240
// when the watch begins — so a search carrying the $200 bound returns nothing at
// all, and there is no candidate to watch.
// TestTheCatalogueAnswersTheScriptedPrompts records exactly that: the flight and
// the bicycle both match nothing at the opening prices and match at the closing
// ones. Searching on the bound would therefore mean the agent could only ever
// discover something it could already buy, which is the one case a watch is not
// for.
//
// The same reasoning is why the *watch* does not poll this endpoint either. A
// search filtered by the user's cap skips the $210 candidate, so the merchant is
// never shown a purchase it has to refuse — and beat 5 of the built scenario,
// the verifier rejecting rather than the agent, becomes undemonstrable. The
// watch polls GET /checkout instead; see Watch.
//
// # Validate is called on what the interpreter returned
//
// AGENTS.md hard rule 4 puts that obligation on every caller of an
// IntentInterpreter, and an implementation calling it internally does not
// discharge it — ScriptedInterpreter does, the model-backed one is #17's, and
// this call site is what holds the next one to it. A constraint naming a field
// no verifier knows would otherwise render on the approval screen, be signed,
// and be refused as constraint_type_unknown an hour later with nobody there.
//
// The constraint this function appends afterwards is not run through Validate a
// second time. It is a fixed shape over a registered field, and the Trusted
// Surface parses the whole set with the verifier's own parser before signing any
// of it — so a defect in it fails the authorisation loudly, at the same place
// and under the same code as a defect in the interpretation.
func (c *Client) Authorise(ctx context.Context, in Intent) (Authorisation, error) {
	var out Authorisation

	if in.Interpreter == nil {
		return out, errors.New("agent: no interpreter to read the prompt with")
	}

	constraints, err := in.Interpreter.Interpret(ctx, in.Prompt)
	if err != nil {
		return out, fmt.Errorf("interpreting %q: %w", in.Prompt, err)
	}
	if err := interpret.Validate(constraints); err != nil {
		return out, fmt.Errorf("the interpretation of %q is not something a verifier could read: %w",
			in.Prompt, err)
	}

	item, err := c.discover(ctx, constraints)
	if err != nil {
		return out, err
	}

	narrowed := narrow(constraints, item)

	var answer struct {
		OpenCheckoutMandate string                      `json:"open_checkout_mandate"`
		OpenPaymentMandate  string                      `json:"open_payment_mandate"`
		Rendered            []string                    `json:"rendered"`
		ExpiresAt           time.Time                   `json:"expires_at"`
		PaymentInstrument   generated.PaymentInstrument `json:"payment_instrument"`
	}
	body := map[string]any{
		"prompt":      in.Prompt,
		"constraints": narrowed,
		"agent_key":   in.AgentKey,
	}
	if err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.Surface, "/")+"/authorise", body, &answer); err != nil {
		return out, fmt.Errorf("asking the user to authorise the watch: %w", err)
	}

	return Authorisation{
		Item:                item,
		Constraints:         narrowed,
		OpenCheckoutMandate: answer.OpenCheckoutMandate,
		OpenPaymentMandate:  answer.OpenPaymentMandate,
		Rendered:            answer.Rendered,
		ExpiresAt:           answer.ExpiresAt,
		Instrument:          answer.PaymentInstrument,
	}, nil
}

// candidate is one offer a search came back with.
//
// Only the identifier is read. The rest of what the merchant publishes — title,
// image, description — is for a person, and the agent is not one; the price is
// deliberately ignored too, because the agent compares no money anywhere and a
// field it read here would be the first place that stopped being true.
type candidate struct {
	ID string `json:"id"`
}

// discover asks the merchant what it sells that matches, and picks one.
//
// **Choosing among candidates is a product decision this demo does not make.**
// The first result wins, and the merchant returns them in catalogue order, so
// the choice is stable rather than considered. A real agent ranks, or asks; each
// of the four scripted prompts matches exactly one offer once the price bound is
// out of the query — TestTheCatalogueAnswersTheScriptedPrompts is where that is
// pinned — so the demo never reaches a case where the difference shows.
func (c *Client) discover(ctx context.Context, constraints []generated.Constraint) (string, error) {
	query := identifying(constraints)
	if len(query) == 0 {
		// The merchant would answer request_malformed for an empty set, which is
		// the right answer to the wrong question: what is actually wrong is one
		// layer up, in an interpretation that placed limits on the terms of a
		// purchase and said nothing about what was being bought.
		return "", fmt.Errorf(
			"%w: the interpretation names nothing to buy — no constraint reads a fact under %q",
			ErrNothingToBuy, itemFieldPrefix)
	}

	encoded, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("encoding the search: %w", err)
	}

	var results struct {
		Offers []candidate `json:"offers"`
	}
	url := fmt.Sprintf("%s/search?%s=%s",
		strings.TrimSuffix(c.Endpoints.Merchant, "/"), searchParam,
		base64.RawURLEncoding.EncodeToString(encoded))
	if err := c.call(ctx, http.MethodGet, url, nil, &results); err != nil {
		return "", fmt.Errorf("searching the merchant's catalogue: %w", err)
	}

	if len(results.Offers) == 0 {
		return "", fmt.Errorf("%w: the search matched no offer", ErrNothingToBuy)
	}
	if results.Offers[0].ID == "" {
		return "", fmt.Errorf("%w: the merchant returned an offer with no identifier",
			ErrNothingToBuy)
	}
	return results.Offers[0].ID, nil
}

// identifying returns the constraints that say what is being bought.
//
// A leaf whose field sits under `item.` — item.id, item.category, item.attr.*.
// Group nodes are left out rather than walked into: a group can mix a bound on
// the price with a fact about the object, and there is no honest way to send
// half of one. The scripted interpretations are four flat lists, which is a
// disclosure decision argued in internal/agent/interpret/scenarios.go rather
// than a convenience here, so nothing in the demo is dropped by this.
//
// What is dropped is dropped from a *query*, never from the mandate. See
// Authorise for why that distinction is the whole of this function.
func identifying(constraints []generated.Constraint) []generated.Constraint {
	out := make([]generated.Constraint, 0, len(constraints))
	for _, c := range constraints {
		if c.Field != nil && strings.HasPrefix(*c.Field, itemFieldPrefix) {
			out = append(out, c)
		}
	}
	return out
}

// narrow returns the interpretation with one constraint added: this exact item.
//
// Appended rather than substituted. The user approved a price bound, a booking
// window and a route; the agent adds which of the merchant's offers it went on
// to pick, and the surface renders all five so the user sees the addition rather
// than being told about it. Replacing anything here would be the agent editing
// what it is about to ask for a signature on.
//
// item.id is a registered field of kind text compared exactly — see
// internal/core/authz/constraint/field.go — so this is a constraint every
// verifier can read, and the merchant builds the subject it is evaluated against
// out of its own catalogue.
func narrow(constraints []generated.Constraint, item string) []generated.Constraint {
	field := itemIDField
	out := make([]generated.Constraint, 0, len(constraints)+1)
	out = append(out, constraints...)
	return append(out, generated.Constraint{Op: "eq", Field: &field, Value: item})
}

// Quote is the merchant's signed commitment to sell one thing at one price, as
// GET /checkout answers it.
//
// Step and Final are the two fields the watch actually turns on, and Price is
// carried for the mandate rather than for a comparison: the agent writes it into
// the closed Payment Mandate because the merchant refuses a payment that does
// not match the offer, and it never compares it against anything. See Watch for
// why that is structural rather than a matter of discipline.
type Quote struct {
	// Checkout is the merchant-signed Checkout JWT.
	Checkout string `json:"checkout"`
	// Price is what the whole purchase costs at this step.
	Price generated.Amount `json:"price"`
	// Step is which entry of the merchant's schedule this price came from,
	// counting from zero.
	Step int `json:"step"`
	// Final says the schedule has run out of moves, so this price holds
	// indefinitely.
	Final bool `json:"final"`
	// ObservedAt is when the merchant priced it, from the merchant's clock.
	ObservedAt time.Time `json:"observed_at"`
}

// QuoteItem asks the merchant what quantity of an item costs now.
//
// The catalogue path rather than the route path, and the difference is not
// cosmetic: an offer quoted by route names no item, so nothing a constraint on
// item.id, item.category or item.attr.* says can be evaluated against it, and
// the merchant refuses a delegation presented against one. Client.Quote, which
// the Human Present flow uses, is the other path and stays as it was.
func (c *Client) QuoteItem(ctx context.Context, item string, quantity int) (Quote, error) {
	var out Quote
	if item == "" {
		return out, errors.New("agent: no item to ask the merchant about")
	}
	if quantity < 1 {
		return out, fmt.Errorf("agent: a quantity of %d buys nothing", quantity)
	}

	// Through url.Values rather than by concatenation: a catalogue identifier is
	// scheme-prefixed by convention — "gtin:05012345678900" — and the colon has
	// a meaning in a URL that it does not have in an identifier.
	query := neturl.Values{}
	query.Set(itemParam, item)
	query.Set(quantityParam, strconv.Itoa(quantity))

	url := strings.TrimSuffix(c.Endpoints.Merchant, "/") + "/checkout?" + query.Encode()
	if err := c.call(ctx, http.MethodGet, url, nil, &out); err != nil {
		return Quote{}, fmt.Errorf("asking the merchant to price %s: %w", item, err)
	}
	return out, nil
}
