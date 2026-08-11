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

// itemFieldPrefix and merchantFieldPrefix are the two prefixes under which a
// constraint says *what* to buy or *from whom*, as opposed to the terms a
// purchase has to meet. itemIDField is the one field this file writes.
//
// The registry holds seven named fields plus item.attr.*. Three of them —
// amount, at, quantity — are terms: they describe a purchase rather than pick
// one out, and they are the ones the watch exists to wait for. The other four
// sit under these two prefixes, and all four are things Catalogue.Search can
// evaluate: Catalogue.Subject fills Item.ID, Item.Category, Item.Attributes and
// Merchant, so a query carrying merchant.id or merchant.category is answered
// rather than ignored. That is why `merchant.` is here and not only `item.` —
// "buy it from this shop" is as much a description of what to go looking for as
// "buy this bicycle" is.
//
// Naming a field is not evaluating one. The agent never parses a constraint,
// never builds a subject and never reaches a verdict — internal/agent does not
// import internal/core/authz/constraint, and
// TestTheAgentCannotReachAConstraintEvaluator is what keeps that true.
//
// # A prefix is the wrong place for this, and the right place is unreachable
//
// Whether a field is selective is a property *of the field*, so its home is a
// column on constraint.Field beside Kind, Noun and exact — which is exactly
// where AGENTS.md's "Open for extension" row says a new fact about a purchase
// goes. **The agent cannot read it there.** Doing so means importing the
// constraint package, and that import is the thing #121's fourth box forbids and
// TestTheAgentCannotReachAConstraintEvaluator fails on. So the knowledge is
// duplicated here as a string prefix, in the one package that must not ask the
// registry.
//
// That tension is real and is tracked as issue #132, which sets out the three
// ways out. The third — a test walking the registry from the *test* package —
// has landed; the duplication itself has not gone, so the issue stays open.
// Two consequences worth knowing before the next field lands, and they no
// longer both announce themselves the same way:
//
//   - **A selective field added to the registry outside these prefixes is
//     dropped from discovery**: the query simply stops carrying it and a search
//     returns more candidates than it should. Nothing fails to compile, and
//     until #132's first step nothing went red either.
//     TestTheAgentsPrefixesAgreeWithFieldSelectivity is what goes red now. It
//     walks constraint.Vocabulary() from this package's test binary and holds
//     these two prefixes against the registry's own selective column in both
//     directions — the reverse one because a prefix that widened to swallow
//     `amount` would send the user's price bound to the search, which is the
//     one case the watch exists for — and names constraint.AttributePrefix so
//     that item.attr.*, which is selective and is in no registry to walk, is
//     caught by argument no longer.
//   - **Group nodes are dropped whole, and that one is still silent.**
//     identifying reads leaves only, because
//     a group can mix a bound on the price with a fact about the object and
//     there is no honest way to send half of one. All five scripted
//     interpretations are flat lists, and the model-backed interpreter of #17
//     produces leaves only for exactly this reason — its structured-output
//     schema's op enum carries no group operator, and
//     TestTheSchemaDescribesLeafConstraintsOnly is what keeps that true. So the
//     case is currently unreachable rather than merely unexercised, and closing
//     the drop is what has to come before an interpreter is widened to produce
//     one.
const (
	itemFieldPrefix     = "item."
	merchantFieldPrefix = "merchant."
	itemIDField         = "item.id"
)

// ErrNothingToBuy means discovery found no candidate to watch.
//
// A refusal rather than an empty result, on the reasoning interpret.ErrNoConstraints
// already applies one layer up: a watch with no item is a process that will sit
// there polling nothing, and the failure would surface as a demonstration where
// nothing ever happens rather than as an error anybody can act on.
var ErrNothingToBuy = errors.New("agent: nothing in this merchant's catalogue matches what the user described")

// ErrMerchantAnsweredDifferently means a search that named one identifier came
// back naming another.
//
// Wrapped alongside ErrNothingToBuy in settle rather than instead of it — a
// caller checking only for ErrNothingToBuy still finds this is a case of it,
// which is true: there is still nothing here this proposal can use. But it is
// not the *same* failure, and console.go's two switches have to tell them
// apart. ErrNothingToBuy on its own is this agent's own account of a request
// it cannot turn into a watch — an interpretation named something and the
// catalogue has none of it. This is a different party misbehaving: the search
// asked for one identifier and got back another, which is prefix-matching, a
// bug, or hostile, never an honest answer to the question asked — see settle.
// So it belongs with "the Trusted Surface did not answer", not with "this is
// not one of the sentences the interpreter knows".
var ErrMerchantAnsweredDifferently = errors.New(
	"agent: the merchant answered a search for one offer by naming a different one")

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
	// Interpreter turns it into constraints. cmd/agent -interpreter scripted
	// wires interpret.Demo(), a scripted table, and -interpreter gemini wires a
	// model behind this same interface; the demo asks for -interpreter auto,
	// which is the first of those unless GEMINI_API_KEY is exported.
	Interpreter interpret.IntentInterpreter
	// AgentKey is the public half of the key this agent signs delegations with,
	// as roles.PublicKey reads it out of the agent's own key set. It ends up in
	// both open mandates' cnf claim.
	AgentKey generated.PublicKey

	// Item, when set, is the catalogue offer the caller has already chosen.
	//
	// It is for a caller that has one: the shopping console of #109 shows the
	// merchant's own search results in a table and the user picks a row, so by
	// the time this agent is asked for a mandate the choosing has happened
	// somewhere a person could see it. Empty is the command line's case, where
	// nobody was there and Discover picks the first match.
	//
	// **What changes is the query, never the narrowing, and the merchant is
	// still asked.** A set item turns the search into one identifier instead of
	// the interpretation — see settle and candidates — rather than skipping it.
	// The constraint saying this exact item is appended either way, so the
	// Trusted Surface renders `the item is …` and the user reads it before
	// signing — which is the property #22 exists for and is exactly what a
	// caller-supplied item would otherwise quietly bypass.
	//
	// Because the merchant is still asked, this path can now fail where a
	// caller-named item previously could not: ErrNothingToBuy if the identifier
	// no longer exists, or if the merchant answers with a different one than was
	// asked for — settle refuses that rather than trusting either side over the
	// other — or a transport error reaching the merchant at all.
	//
	// **The agent does not check it against the rest of the interpretation**,
	// and that is not an oversight to be closed later. Asking whether this offer
	// satisfies a bound the user set is evaluating a constraint, which AGENTS.md
	// gives to the verifier and to nobody else; internal/agent cannot even
	// import the evaluator, and TestTheAgentCannotReachAConstraintEvaluator is
	// what keeps that true. A caller that names an item its own prompt does not
	// describe gets a mandate saying both things, signed by the user, and
	// refused at the moment of purchase by the party whose job that is.
	Item string
}

// Authorisation is what the user signed, together with the two things the agent
// needs afterwards that are not in the mandates it can read.
//
// It is the whole of what the watch loop carries forward. Everything in it came
// from somebody else — the constraints from the interpreter, the mandates and
// the instrument from the Trusted Surface, the item from the merchant's own
// catalogue — which is the property that makes the loop below deterministic.
//
// It carries JSON tags because it is also a wire shape: console.Watching.
// Authorisation decodes one from a browser that already collected the user's
// signature at a Trusted Surface the agent was never on the connection for.
// Hand-written, on the same terms as every other shape internal/agent/console
// serialises, and deliberately not in contracts/ — two SD-JWTs and the
// sentences a surface rendered are presentation, not the canonical model.
type Authorisation struct {
	// Item is the catalogue offer the watch will poll, and the one the
	// constraints were narrowed to.
	Item string `json:"item"`

	// Constraints are the limits as signed: the interpretation, plus the one
	// constraint that narrows it to Item.
	Constraints []generated.Constraint `json:"constraints"`

	// The two open mandates, signed by the user, in SD-JWT compact
	// serialisation.
	OpenCheckoutMandate string `json:"open_checkout_mandate"`
	OpenPaymentMandate  string `json:"open_payment_mandate"`

	// Rendered is what the surface said each constraint means, in the order
	// they were signed. The agent shows it and never acts on it.
	Rendered []string `json:"rendered"`

	// ExpiresAt is when the pair stops authorising anything.
	ExpiresAt time.Time `json:"expires_at"`

	// Instrument is the payment instrument the surface pinned into the open
	// Payment Mandate. The closed one has to reproduce it unchanged or
	// authz.checkPinned refuses the purchase, and the surface is the only party
	// that can honestly say what it pinned — see the field on surface.authorised.
	Instrument generated.PaymentInstrument `json:"payment_instrument"`
}

// Authorise runs Propose and then collects the user's signature over the
// result, at the Trusted Surface. See Propose for the discovery half and sign
// for the one call this adds.
func (c *Client) Authorise(ctx context.Context, in Intent) (Authorisation, error) {
	proposal, err := c.Propose(ctx, in)
	if err != nil {
		return Authorisation{}, err
	}
	return c.sign(ctx, in.Prompt, proposal)
}

// Offer is the merchant's own description of one thing it sells.
//
// It exists so a consent screen can say what an identifier refers to. No
// verifier sees it, no constraint addresses it, and nothing in this package
// compares any of it — see candidate. It is deliberately not in contracts/:
// how a shop describes its stock is presentation, and putting it in the
// canonical model would mean core knew what a flight is.
//
// Step and Final are the same two fields Quote carries for the offer a watch
// is already running against — the position in the merchant's price schedule,
// and whether it has run out of moves. #109's product table shows them beside
// a candidate nobody has started watching yet, on the same reasoning the
// consent screen's offer card already carries a price: a person deciding
// whether to watch something benefits from knowing whether the number in front
// of them can still move.
type Offer struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	ImageURL    string           `json:"image_url"`
	Retailer    string           `json:"retailer"`
	Price       generated.Amount `json:"price"`
	Step        int              `json:"step"`
	Final       bool             `json:"final"`
}

// Proposal is what the agent puts in front of a person: the limits it read out
// of their sentence, the offer it narrowed to, and the key it wants endorsed.
//
// Nothing in it is signed and nothing about it is remembered. It is the input to
// a decision, and if the decision is no there is nothing to clean up.
type Proposal struct {
	Item  string
	Offer Offer
	// Offers is every candidate the search behind Offer actually found, in the
	// same catalogue order settle chose Offer from — "the agent serves the
	// offers it already found" rather than a second search the console would
	// otherwise have to run itself.
	//
	// #109's product table is why this exists: a browser that ran its own
	// search would have to decide which of the interpretation's constraints
	// describe *what to buy* rather than *on what terms*, and that decision is
	// identifying's, made once, here — duplicating it in the console or the
	// browser is the drift AGENTS.md's "open for extension" row and this
	// package's own comments on identifying both warn about. Carrying the list
	// this call already has costs nothing further: it is candidates' own
	// result, kept instead of discarded down to found[0].
	//
	// Never empty when Offer is populated — Offer is Offers[0] by construction,
	// see settle — and today it never holds more than one element either,
	// because every scripted interpretation narrows the demo catalogue to
	// exactly one candidate. Sized for #160's wider catalogue regardless.
	Offers      []Offer
	Constraints []generated.Constraint
	AgentKey    generated.PublicKey
}

// Propose runs the discovery half: interpret, search, narrow — everything
// Authorise does, short of collecting the user's signature. A consent screen
// needs exactly this: something to render that does not yet exist as a
// mandate.
//
// # The order, and why each step needs the one before it
//
// The interpretation is what the user is shown, so it has to exist before the
// surface is called. The search is what turns "a flight to Palma" into a
// specific thing this merchant sells, so it has to run before the narrowing.
// Intent.Item changes what that search asks for — one identifier instead of
// the interpretation — but never skips it; see settle and Intent.Item for why
// the merchant is still asked. And the narrowing has to happen before the
// signature, because the point of it is that the user approves buying *that*
// item rather than anything matching a description — an open mandate
// constraining only a category authorises every offer in it, for as long as it
// lives.
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
// discharge it. Both implementations do call it, and interpret's conformance
// suite is what holds them to that; this call site covers the other half, an
// implementation that forgot. A constraint naming a field no verifier knows
// would otherwise render on the approval screen, be signed, and be refused as
// constraint_type_unknown an hour later with nobody there.
//
// The constraint this function appends afterwards is not run through Validate a
// second time. It is a fixed shape over a registered field, and the Trusted
// Surface parses the whole set with the verifier's own parser before signing any
// of it — so a defect in it fails the authorisation loudly, at the same place
// and under the same code as a defect in the interpretation.
func (c *Client) Propose(ctx context.Context, in Intent) (Proposal, error) {
	var out Proposal

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

	item, offer, offers, err := c.settle(ctx, constraints, in.Item)
	if err != nil {
		return out, err
	}

	return Proposal{
		Item:        item,
		Offer:       offer,
		Offers:      offers,
		Constraints: narrow(constraints, item),
		AgentKey:    in.AgentKey,
	}, nil
}

// settle picks the offer this proposal is about, describes it, and reports
// every candidate the search behind it found.
//
// A caller that already named one changes what candidates asks the merchant
// for, never whether it asks; see Intent.Item. The description is fetched
// either way, because a screen needs it whether the picking happened here or
// upstream — and asking is also what lets a caller-named item be refused
// rather than trusted blindly, below.
func (c *Client) settle(
	ctx context.Context, constraints []generated.Constraint, chosen string,
) (string, Offer, []Offer, error) {
	found, err := c.candidates(ctx, constraints, chosen)
	if err != nil {
		return "", Offer{}, nil, err
	}
	if len(found) == 0 {
		return "", Offer{}, nil, fmt.Errorf("%w: the search matched no offer", ErrNothingToBuy)
	}

	// The first result wins, and the merchant returns them in catalogue order,
	// so the choice is stable rather than considered. A real agent ranks, or
	// asks. Choosing among candidates is a product decision this demo does not
	// make.
	//
	// TestTheCatalogueAnswersTheScriptedPrompts, in internal/roles/merchant,
	// does *not* pin this: it searches with the whole constraint set, which is
	// the query this path deliberately does not send. What does is
	// TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle.
	c0 := found[0]

	// When a caller named an item, candidates asked the merchant about that one
	// identifier, and a search on item.id has exactly one honest answer. A
	// merchant that comes back with a different one is answering a question it
	// was not asked — prefix-matching, a bug, or hostile — and the identifier
	// and the description this proposal carries would describe two different
	// things. Refused rather than trusted over the caller's own choice.
	if chosen != "" && c0.ID != chosen {
		return "", Offer{}, nil, fmt.Errorf(
			"%w: %w: asked the merchant for the offer identified as %q and it answered with %q instead",
			ErrMerchantAnsweredDifferently, ErrNothingToBuy, chosen, c0.ID)
	}

	// candidate and Offer carry the same fields for the same reason, so the
	// conversions below are not a shortcut past a copy — they are that copy.
	// The Propose caller decides what to do with more than one; settle itself
	// still chooses nothing beyond found[0].
	offers := make([]Offer, len(found))
	for i, f := range found {
		offers[i] = Offer(f)
	}
	return c0.ID, offers[0], offers, nil
}

// sign posts the proposal's constraints and key to the Trusted Surface and
// assembles what the user signed into an Authorisation.
//
// The last of Authorise's four steps, held apart from Propose for the reason
// #22 exists: nothing above this call may have collected a signature, and
// keeping it in its own function is what makes that true by construction rather
// than by review.
func (c *Client) sign(ctx context.Context, prompt string, proposal Proposal) (Authorisation, error) {
	var out Authorisation

	var answer struct {
		OpenCheckoutMandate string                      `json:"open_checkout_mandate"`
		OpenPaymentMandate  string                      `json:"open_payment_mandate"`
		Rendered            []string                    `json:"rendered"`
		ExpiresAt           time.Time                   `json:"expires_at"`
		PaymentInstrument   generated.PaymentInstrument `json:"payment_instrument"`
	}
	body := map[string]any{
		"prompt":      prompt,
		"constraints": proposal.Constraints,
		"agent_key":   proposal.AgentKey,
	}
	if err := c.call(ctx, http.MethodPost,
		strings.TrimSuffix(c.Endpoints.Surface, "/")+"/authorise", body, &answer); err != nil {
		return out, fmt.Errorf("asking the user to authorise the watch: %w", err)
	}

	return Authorisation{
		Item:                proposal.Item,
		Constraints:         proposal.Constraints,
		OpenCheckoutMandate: answer.OpenCheckoutMandate,
		OpenPaymentMandate:  answer.OpenPaymentMandate,
		Rendered:            answer.Rendered,
		ExpiresAt:           answer.ExpiresAt,
		Instrument:          answer.PaymentInstrument,
	}, nil
}

// candidate is one offer a search came back with.
//
// # What discovery reads, and what this type now carries
//
// Discovery reads the identifier and nothing else. That has not changed and
// TestDiscoverStillChoosesOnTheIdentifierAlone is what keeps it true: the agent
// compares no money anywhere, ranks nothing, and a field it selected on here
// would be the first place that stopped being so.
//
// What changed is that Propose serves a caller that **is** a person. The rest of
// what the merchant publishes — title, image, description, retailer, and the
// price today — is for that person to read, and the agent carries it through
// rather than discarding it. Carrying is not reading. The consent screen needs
// it because `the item is gtin:05014477390221` is the identifier a constraint
// carries and is nothing anybody can act on.
type candidate struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	ImageURL    string           `json:"image_url"`
	Retailer    string           `json:"retailer"`
	Price       generated.Amount `json:"price"`
	// Step and Final mirror merchant.PricedOffer's own fields — see Offer's
	// doc comment for why the product table wants them on a candidate nobody
	// has started watching yet.
	Step  int  `json:"step"`
	Final bool `json:"final"`
}

// candidates asks the merchant what it sells that a set of constraints
// describes, keeping everything it published.
//
// The body of what Discover used to be. Discover is now a thin projection of
// this onto identifiers, so there is one search and one decode rather than two
// that could come to disagree about what an offer is.
//
// chosen, when set, is an offer the caller already picked, and the query becomes
// that one identifier. The merchant is still asked — the description has to come
// from the party that publishes it, and inventing one here would put the shop's
// own words in the buyer's mouth.
func (c *Client) candidates(
	ctx context.Context, constraints []generated.Constraint, chosen string,
) ([]candidate, error) {
	query := identifying(constraints)
	if chosen != "" {
		field := itemIDField
		query = []generated.Constraint{{Op: "eq", Field: &field, Value: chosen}}
	}
	if len(query) == 0 {
		// The merchant would answer request_malformed for an empty set, which is
		// the right answer to the wrong question: what is actually wrong is one
		// layer up, in an interpretation that placed limits on the terms of a
		// purchase and named neither an item nor a merchant.
		return nil, fmt.Errorf(
			"%w: the interpretation names nothing to go looking for — of its %d constraints, none reads a fact under %q or %q",
			ErrNothingToBuy, len(constraints), itemFieldPrefix, merchantFieldPrefix)
	}

	encoded, err := json.Marshal(query)
	if err != nil {
		return nil, fmt.Errorf("encoding the search: %w", err)
	}

	var results struct {
		Offers []candidate `json:"offers"`
	}
	url := fmt.Sprintf("%s/search?%s=%s",
		strings.TrimSuffix(c.Endpoints.Merchant, "/"), searchParam,
		base64.RawURLEncoding.EncodeToString(encoded))
	if err := c.call(ctx, http.MethodGet, url, nil, &results); err != nil {
		return nil, fmt.Errorf("searching the merchant's catalogue: %w", err)
	}

	for _, o := range results.Offers {
		if o.ID == "" {
			return nil, fmt.Errorf("%w: the merchant returned an offer with no identifier",
				ErrNothingToBuy)
		}
	}
	return results.Offers, nil
}

// Discover asks the merchant what it sells that an interpretation describes, in
// catalogue order.
//
// Exported because it is the only way to see how many candidates a query
// actually found. Authorise takes the first and discards the rest, so a test
// driving Authorise cannot tell one candidate from five —
// TestEveryScriptedPromptFindsOneCandidate calls this instead, and asserts the
// count as well as the identifier. That was written after the first version of
// that test was found to assert only the identifier, which stayed green while
// the claim it was cited for became false.
func (c *Client) Discover(ctx context.Context, constraints []generated.Constraint) ([]string, error) {
	found, err := c.candidates(ctx, constraints, "")
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(found))
	for _, o := range found {
		out = append(out, o.ID)
	}
	return out, nil
}

// identifying returns the constraints that say what to go looking for.
//
// A leaf whose field sits under one of selectivePrefixes — item.id,
// item.category, item.attr.*, merchant.id, merchant.category. Everything else is
// a term of the purchase rather than a description of one, and the terms are
// what the watch is waiting to be met.
//
// Group nodes are left out rather than walked into, and a selective field
// registered outside those two prefixes would be left out too. Both are argued
// where the prefixes are declared. The second now fails a test rather than
// passing quietly; the first is still silent, and still waiting on an
// interpreter that can produce a group at all.
//
// What is dropped is dropped from a *query*, never from the mandate. See
// Authorise for why that distinction is the whole of this function.
func identifying(constraints []generated.Constraint) []generated.Constraint {
	out := make([]generated.Constraint, 0, len(constraints))
	for _, c := range constraints {
		if c.Field == nil {
			continue
		}
		if strings.HasPrefix(*c.Field, itemFieldPrefix) ||
			strings.HasPrefix(*c.Field, merchantFieldPrefix) {
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
