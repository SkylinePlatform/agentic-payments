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
	"unicode"
	"unicode/utf8"

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

// shelvesPath is where the merchant publishes the categories it sells.
//
// A fourth copy on the same terms as the three parameters above, and the one that
// most needs the test holding it in step. A misspelled parameter produces a
// refusal three files from its cause; a misspelled path here produces *nothing*,
// because Client.shelves treats an unanswered fetch as a merchant that publishes
// no shelves and carries on. That is the right behaviour for a counterparty which
// does not offer the endpoint and the wrong one for a typo, so what tells the two
// apart is TestTheAgentSpellsTheMerchantsQueryParameters comparing this against
// merchant.ShelvesPath.
const shelvesPath = "/shelves"

// itemIDField is the one field this file writes.
//
// **Two string prefixes used to sit beside it, and issue #132 is the whole story
// of why they are gone.** Which constraints say *what* to buy or *from whom*, as
// opposed to the terms a purchase has to meet, was decided here by testing field
// names against "item." and "merchant.". That was a second statement of a fact
// the registry already held as a column on constraint.Field — where AGENTS.md's
// "Open for extension" row says a fact about a purchase goes — kept here because
// this package may not import the registry to ask. It reads it off
// interpret.Narrowing now, which is the verifier's own answer arriving over an
// import this package already had.
//
// Naming a field is still not evaluating one. The agent never parses a
// constraint, never builds a subject and never reaches a verdict —
// internal/agent does not import internal/core/authz/constraint, and
// TestTheAgentCannotReachAConstraintEvaluator is what keeps that true.
//
// What the copy cost while it existed is worth keeping written down, because
// both halves were live rather than theoretical:
//
//   - **A selective field registered outside the two stems was dropped from
//     discovery**: the query stopped carrying it and a search returned more
//     candidates than it should, with nothing failing to compile. #132's first
//     step made that a red test; this one makes it unrepresentable, since there
//     is no longer a second list to be missing from.
//   - **A field under one of the stems that the registry does not know was
//     classified as selective.** item.colour matched "item.", so identifying
//     put it in the query it built. Be precise about how far it got: Propose
//     calls interpret.Validate before it searches anything, and a name no
//     verifier can read fails there, so on that path the interpretation was
//     refused before this function ran. Discover is the exported entry point
//     with nothing in front of it. So the prefix was the second of two guards
//     and it was the one giving the wrong answer, which is worth closing on its
//     own terms — a guard standing behind another guard still has to be right,
//     or the day the first one moves is the day nobody notices. The registry
//     answers false for a name it cannot read.
//
// One silent drop outlived that step, and issue #203 is where it went:
//
//   - **Group nodes were dropped whole.** identifying read leaves only, on the
//     ground that a group can mix a bound on the price with a fact about the
//     object and there is no honest way to send half of one. That turned out to
//     be true of one node kind and false of another — half of a conjunction is
//     exactly honest, half of a disjunction is not, and a negation may travel
//     only whole — so the answer is per node kind and lives beside the field's
//     at constraint.Narrowing. The drop is closed **before** an interpreter is
//     widened to produce a group rather than after, which is the order that
//     matters: both interpreters produce flat lists today, so the case was
//     unreachable rather than merely unexercised, and a producer widened first
//     would have had part of every interpretation silently withheld from the
//     merchant.
const itemIDField = "item.id"

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
	// Interpreter turns it into constraints. The demo wires interpret.Demo(),
	// a scripted table; cmd/agent -interpreter gemini wires a model behind this
	// same interface.
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

	// Prompt is the sentence the user typed, as this agent received it.
	//
	// **It sits here for Quantity's reason and carries a sharper version of
	// Quantity's caveat.** Nothing signs it, and unlike a basket size there is
	// nothing about it a verifier could ever check: the user signs the
	// *interpretation*, which is the whole security property POST /authorise
	// exists for — see surface.authorisation's own field, which says in as many
	// words that this string is unsigned, unbound and chosen by whoever made the
	// request. The Trusted Surface never returns it, so this is the agent's own
	// copy travelling forward rather than anything coming back.
	//
	// It is here because the event log is where "typed" sits beside "signed",
	// and issue #213's User lane is the screen that shows the pair. A screen
	// drawing it has to say which of the two it is;
	// internal/agent/console/view.go's runView already names the same two fields
	// `typed` and `signed` on exactly those terms.
	//
	// Empty is legitimate and not defaulted. An Authorisation assembled field by
	// field somewhere else — a browser that posts one to POST /watches, a test
	// fixture — may carry none, and console.Service.Start is where the browser's
	// own prompt is joined to it. What must never happen is a prompt being
	// invented here to fill the gap.
	Prompt string `json:"prompt"`

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
	//
	// It is a field and not a read of the mandates because the watch loop *acts*
	// on it. The other instant the pair carries — when the user signed — is a
	// caption rather than something acted on, and is the SignedAt method in
	// signed.go instead, which is where that line is drawn.
	ExpiresAt time.Time `json:"expires_at"`

	// Instrument is the payment instrument the surface pinned into the open
	// Payment Mandate. The closed one has to reproduce it unchanged or
	// authz.checkPinned refuses the purchase, and the surface is the only party
	// that can honestly say what it pinned — see the field on surface.authorised.
	Instrument generated.PaymentInstrument `json:"payment_instrument"`

	// Quantity is the basket size the interpretation proposed: how many of
	// Item the watch is to buy. Zero carries Proposal.Quantity's meaning —
	// the sentence named no count — and leaves the number to whoever holds
	// one.
	//
	// This is issue #133's field, and the reason it exists here rather than
	// being read off Constraints is Interpretation's own: "how many to buy" is
	// not a fact a verifier evaluates, so it is not a constraint, and reading
	// a quantity bound as an instruction would be this package deciding what
	// the user meant from a limit they set.
	//
	// **Nothing signs it, and this is the field to be careful about for that
	// reason.** The Trusted Surface never sees a basket size: it signs the
	// constraints, and `quantity lte 2` — a limit a verifier does check — is
	// the only thing about a count that a mandate carries. So this number is
	// the agent's stated intent, bounded by what was signed rather than part
	// of it, and a screen that shows it has to say which of the two it is.
	// frontend/src/routes/consent/Consent.tsx puts it outside the signed box
	// on exactly that ground.
	Quantity int `json:"quantity"`

	// Trigger is when the sentence asked for the purchase, as the interpreter
	// read it: an instruction to buy, or a purchase conditional on something
	// changing. Watch.Run is what spends it, and it is the whole of the
	// difference between one attempt now and a poll that waits for the
	// merchant's commitment to move.
	//
	// **It sits here for Quantity's reason and carries Quantity's caveat.**
	// Nothing signs it — the Trusted Surface signs constraints, and "when the
	// person asked to buy" is not one: no verifier can refute it at the point
	// of sale, which is the criterion the constraint registry is closed on. So
	// this is the agent's stated intent, bounded by what was signed rather than
	// part of it, and a screen showing it has to say which of the two it is.
	//
	// **An empty trigger means a watch**, and that is a decision rather than a
	// gap. interpret.Validate refuses an interpretation that states none, so
	// nothing this package produces arrives empty; what can is an Authorisation
	// assembled field by field somewhere else — a browser that collected its own
	// signature and has not been taught this field yet, or a test fixture. For
	// those, watching is the reading that cannot buy something the sentence did
	// not ask to buy now, which is the direction to be wrong in. A trigger that
	// is neither empty nor one interpret defines is refused outright by
	// Watch.valid rather than read as either.
	Trigger interpret.Trigger `json:"trigger"`
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
// of their sentence, the offer it narrowed to, the key it wants endorsed, and
// the basket size the sentence asked for.
//
// Nothing in it is signed and nothing about it is remembered. It is the input to
// a decision, and if the decision is no there is nothing to clean up.
type Proposal struct {
	Item  string
	Offer Offer
	// Offers is every candidate the search behind Offer actually found, in the
	// same order settle chose Offer from — "the agent serves the offers it
	// already found" rather than a second search the console would otherwise
	// have to run itself.
	//
	// **That order is the merchant's catalogue order until a sentence states a
	// preference**, and then it is the order the preference asked for; see
	// ranked and Rank below. The list is sorted rather than left alone with the
	// choice made out of band on purpose, and it is the property that makes an
	// unsigned rank auditable: a person reading the consent screen sees the
	// preference, sees every candidate the agent had, and sees the chosen one at
	// the head of them. A screen showing the merchant's order beside a choice
	// taken from somewhere in the middle of it would be asking to be trusted
	// instead.
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

	// Quantity is how many of Item the interpretation proposed to buy, and
	// **zero means the sentence named no count at all** — not "one".
	//
	// The distinction is the whole of what this field is for, and collapsing it
	// here is how it gets lost. Four of the five scripted sentences say nothing
	// about how many, and for those the interpreter has no opinion to offer; a
	// caller that has one — cmd/agent's -quantity, POST /watches's own quantity
	// — is then the next place to look, which is the precedence
	// console.Service.Start and cmd/agent's watchOnce both write down. Resolve
	// the zero any earlier and that precedence can never fire: every
	// authorisation arrives naming a number, an operator's own is silently
	// discarded, and a flag documented as a fallback has no path that reaches
	// it.
	//
	// The one caller with nothing to fall back to is a browser, and
	// console.Service.propose is where the zero becomes a one for it — at the
	// wire, once, because a consent screen has to display the number that will
	// actually be spent. Issue #133.
	Quantity int

	// Trigger is whether the sentence asked to buy now or to wait for
	// something to change — see interpret.Trigger and Authorisation.Trigger.
	//
	// Unlike Quantity it is never empty here: Propose calls interpret.Validate,
	// which refuses an interpretation that states no trigger, so there is
	// nothing for a caller downstream to resolve and no precedence to write
	// down. It is on a Proposal at all because a proposal is what a person is
	// shown, and "buy now, up to $160" and "buy when the price moves, up to
	// $160" are different authorisations that render identically from the
	// constraints alone. Issue #198's first trap.
	Trigger interpret.Trigger

	// Rank is the preference the sentence stated between offers that all satisfy
	// the limits — see interpret.Rank — and the zero value means it stated none,
	// which is most sentences.
	//
	// **It has already been spent by the time this exists**, and that is the one
	// way it differs from Quantity and Trigger. Both of those are carried forward
	// because the watch loop spends them *after* the user signs: Watch.Run reads
	// the trigger to decide whether to buy at once, and a quantity goes into every
	// closed mandate. A rank is applied inside the same call that produced this
	// Proposal — settle used it to choose Item out of Offers — so nothing
	// downstream of here reads it, and it is deliberately **not** on
	// Authorisation. A field on the thing the watch loop carries would suggest
	// some later step still gets to reorder something, and none does.
	//
	// So it is here for exactly one reason: a proposal is what a person is shown,
	// and this is the difference between a screen that says *bought this one* and a
	// screen that says *bought this one, because you asked for the cheapest, and
	// here are the other three*. That is what makes an unsigned preference
	// checkable rather than merely harmless — see interpret.Rank's "Why a rank need
	// not be signed", which leans on it.
	Rank interpret.Rank
}

// Propose runs the discovery half: interpret, search, narrow — everything
// Authorise does, short of collecting the user's signature. A consent screen
// needs exactly this: something to render that does not yet exist as a
// mandate.
//
// # The order, and why each step needs the one before it
//
// The merchant's shelves come first, because they are what the sentence is read
// *against*: issue #254 is a model narrowing by `item.category eq "flight"` at a
// shop whose shelf is called `flights`, and nobody had told it. See shelves for
// why the agent is the party that asks, and why an unanswered question is not a
// failure here. The interpretation is what the user is shown, so it has to exist
// before the surface is called. The search is what turns "a flight to Palma" into
// a specific thing this merchant sells, so it has to run before the narrowing.
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
// It is run with the constraints that name *what* to buy — whatever part of the
// set the registry says a catalogue can be asked about, which is identifying's
// answer and not a prefix on a field name — and not with the ones that say on
// what terms. That is a deliberate narrowing of the query and it is worth being
// exact about why, because the obvious reading is that the agent is dropping a
// limit.
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

	// Asked before the sentence is read, because reading it is what the answer is
	// for. The error is dropped on purpose — see shelves.
	shelves, _ := c.shelves(ctx)

	interpretation, err := in.Interpreter.Interpret(ctx, in.Prompt, shelves)
	if err != nil {
		return out, fmt.Errorf("interpreting %q: %w", in.Prompt, err)
	}
	if err := interpret.Validate(interpretation); err != nil {
		return out, fmt.Errorf("the interpretation of %q is not something a verifier could read: %w",
			in.Prompt, err)
	}

	item, offer, offers, err := c.settle(ctx, interpretation.Constraints, in.Item, interpretation.Rank)
	if err != nil {
		return out, declined(err, interpretation.DeclinedCategories)
	}

	return Proposal{
		Item:        item,
		Offer:       offer,
		Offers:      offers,
		Constraints: narrow(interpretation.Constraints, item),
		AgentKey:    in.AgentKey,
		// Carried exactly as the interpreter answered it, zero included: a
		// sentence that named no count has to still look like one here, or
		// every caller holding a number of its own loses it. See
		// Proposal.Quantity.
		Quantity: interpretation.Quantity,
		// Validate above has already refused an interpretation that states no
		// trigger, so this is one of the two and never an absence. Issue #198.
		Trigger: interpretation.Trigger,
		// The preference settle just spent, carried so a screen can say why this
		// offer and not one of the others — see Proposal.Rank. Zero when the
		// sentence ranked nothing, and unlike the trigger there is nothing here
		// for Validate to have refused: silence is a legitimate answer.
		Rank: interpretation.Rank,
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
//
// # A caller-named item outranks any preference
//
// The rank chooses among candidates. A caller who named one has already chosen, so
// when chosen is set nothing here reorders what the merchant answered with — see
// Intent.Item, and interpret.Rank for what a rank is allowed to decide.
//
// **That is a guarantee and not a tidiness.** A search on item.id has exactly one
// honest answer, and the refusal below compares the merchant's own first answer
// against the identifier that was asked for. Ranking first would let a merchant
// that answered `[something cheaper, the offer asked for]` sort its way past that
// check, so the preference would have laundered a refusal into a purchase of an
// offer the caller never picked. TestARankCannotLaunderAMerchantsWrongAnswer is
// the test that fails when the two are reordered.
func (c *Client) settle(
	ctx context.Context, constraints []generated.Constraint, chosen string,
	preference interpret.Rank,
) (string, Offer, []Offer, error) {
	found, err := c.candidates(ctx, constraints, chosen)
	if err != nil {
		return "", Offer{}, nil, err
	}
	if len(found) == 0 {
		return "", Offer{}, nil, fmt.Errorf("%w: the search matched no offer", ErrNothingToBuy)
	}

	if chosen == "" {
		if found, err = ranked(found, preference); err != nil {
			return "", Offer{}, nil, err
		}
	}

	// The first result wins, and what decides which one that is now comes from the
	// sentence: ranked has put the candidates in the order the preference asked
	// for, or handed back the merchant's own catalogue order when the sentence
	// stated no preference.
	//
	// **This used to be found[0] over the merchant's order unconditionally**, under
	// a comment saying that a real agent ranks or asks and that choosing among
	// candidates was a product decision this demo did not make. Issue #262 is what
	// that cost once the shelf grew: *"find and buy telescopic ladders, cheapest"*
	// put its ranking word into an amount bound, which is a term evaluated at
	// checkout and absent from the query, so the word reached the merchant as
	// nothing at all and the demonstration bought whichever offer sorted first in a
	// shelf it did not choose — a fetched one in 23 of 30 categories.
	//
	// TestTheCatalogueAnswersTheScriptedPrompts, in internal/roles/merchant,
	// does *not* pin this: it searches with the whole constraint set, which is
	// the query this path deliberately does not send. What does is
	// TestProposeBuysThePreferredOfferAndTheFirstOtherwise, which is the test
	// TestProposeTakesTheFirstCandidateRegardlessOfPriceOrTitle became.
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
	// The Propose caller decides what to do with more than one; the only choosing
	// settle does is found[0], over an order ranked either sorted or left exactly
	// as the merchant sent it.
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
		Item: proposal.Item,
		// The same string that went to the surface a line above, carried forward
		// rather than read back out of the answer: /authorise never returns it,
		// and it must not — the user signs the interpretation and not their own
		// words. See Authorisation.Prompt.
		Prompt:              prompt,
		Constraints:         proposal.Constraints,
		OpenCheckoutMandate: answer.OpenCheckoutMandate,
		OpenPaymentMandate:  answer.OpenPaymentMandate,
		Rendered:            answer.Rendered,
		ExpiresAt:           answer.ExpiresAt,
		Instrument:          answer.PaymentInstrument,
		// The proposal's own, not asked of the surface: the Trusted Surface
		// signs constraints, and a basket size is deliberately not one — see
		// Authorisation.Quantity. So this is the number the interpretation
		// proposed surviving the signing step unchanged, zero included, rather
		// than being re-derived from anything the surface said. This path has
		// no consent screen on it at all — the browser collects its own
		// signature and posts the result to POST /watches — which is another
		// way of saying that nothing here has been read by a person.
		Quantity: proposal.Quantity,
		// The proposal's own for the same reason, and it reaches the surface
		// for none: a trigger is not a constraint, so there is nothing about it
		// for a signature to cover. See Authorisation.Trigger.
		Trigger: proposal.Trigger,
	}, nil
}

// candidate is one offer a search came back with.
//
// # What discovery reads, and what this type now carries
//
// Discovery reads the identifier and nothing else, and
// TestDiscoverStillChoosesOnTheIdentifierAlone is what keeps it true. Discover
// returns every identifier a search found, in the order the merchant sent them,
// and no field of this type decides which of them a caller gets.
//
// What changed is that Propose serves a caller that **is** a person. The rest of
// what the merchant publishes — title, image, description, retailer, and the
// price today — is for that person to read, and the agent carries it through
// rather than discarding it. Carrying is not reading. The consent screen needs
// it because `the item is gtin:05014477390221` is the identifier a constraint
// carries and is nothing anybody can act on.
//
// # Price is the one field settle may order on, and only when a sentence asked
//
// This comment used to say that the agent compares no money anywhere. That stopped
// being true with issue #262: Price is read by ranked when — and only when — the
// interpretation carried a preference, which is the one thing that can put the
// candidates in an order the merchant did not choose. What has not changed is the
// half that matters. No money here is ever compared to a *limit*: the comparison is
// between two offers the merchant already said match, and rank.go's own comment is
// where the difference between ordering and evaluating is argued out.
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
		return nil, fmt.Errorf("%w: %s", ErrNothingToBuy, nothingIdentifies(constraints))
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

// shelves asks the merchant which categories it sells, for the interpreter to
// read the sentence against.
//
// # Why the agent fetches this and the interpreter does not
//
// interpret.NewModel and interpret.NewGemini perform no I/O, which is what makes
// cmd/agent's TestInterpreterFor legal under hard rule 4, and it is not a property
// to spend: a constructor that dialled would put a live model one careless test
// away. The agent already holds the merchant's endpoint, already calls the
// interpreter exactly once per authorisation, and is the party that knows *which*
// merchant this sentence will be searched against — so this is where the fetch
// belongs, and interpret.Shelves arrives there as data.
//
// # An answer nobody gave is not an error, and the next call is what makes that safe
//
// Every failure here — no endpoint, a 404, a body that will not decode, a
// merchant that is not listening — comes back as no shelves, and Propose carries
// on. Two reasons, and the second is the one that settles it.
//
// A merchant that does not publish its shelves is a merchant the model has to
// guess at, which is exactly where issue #254 found things; that is a worse
// reading of the sentence and not a broken flow, and failing an authorisation
// because a counterparty declined an optional question would leave this agent able
// to shop at precisely one shop. The precedent is -interpreter auto's, read one
// party along: an unset key is an answer and falls back, a broken one stops the
// process.
//
// And a merchant that is *genuinely* unreachable still fails loudly, a few lines
// later and for a better reason: candidates asks the same host for a search, and
// that call is not optional. So the only case this silence covers is a merchant
// that is up and does not publish — which is the case it is for.
//
// **What it cannot cover is a typo in the path**, since that is indistinguishable
// from a merchant which does not publish. shelvesPath is held against
// merchant.ShelvesPath by a test for exactly that reason.
//
// # The answer is bounded, and this is the one call where that is about a model
//
// maxTitle's argument, one endpoint along and with a sharper edge. That constant
// bounds a name this agent republishes onto a screen; this bounds a list this
// agent puts into a language model's *instruction*, which is a path from a
// counterparty's bytes to the constraints proposed for a user to sign. The
// interpretation still reaches a person and then a verifier, so the blast radius
// is the one this whole design rests on — but a shop that answered with a
// megabyte of prose would be writing part of that instruction, and there is no
// reason to let it.
//
// So the answer has to look like a vocabulary: no more entries than a shop has
// aisles, each a short label with no control characters in it. **Refused whole
// rather than filtered**, because a filtered vocabulary is worse than none — a
// model shown half a shop's shelves narrows confidently by the wrong one, and
// editing a counterparty's list would be this agent deciding what the shop meant.
func (c *Client) shelves(ctx context.Context) (interpret.Shelves, error) {
	var answer struct {
		Categories []string `json:"categories"`
	}
	url := strings.TrimSuffix(c.Endpoints.Merchant, "/") + shelvesPath
	if err := c.call(ctx, http.MethodGet, url, nil, &answer); err != nil {
		return nil, fmt.Errorf("asking the merchant which categories it sells: %w", err)
	}

	if len(answer.Categories) > maxShelves {
		return nil, fmt.Errorf(
			"agent: the merchant published %d categories and a shop's vocabulary is bounded at %d",
			len(answer.Categories), maxShelves)
	}
	// Trimmed on the way through, so that what is checked is what is repeated.
	// Surrounding space is "transport noise, not identity" — constraint's own
	// exactText says exactly that of an identifier, and constraint.FoldText trims
	// before comparing a category, so nothing is lost and the alternative is a
	// bound measured on one string and a listing printed from another.
	shelves := make(interpret.Shelves, 0, len(answer.Categories))
	for _, shelf := range answer.Categories {
		trimmed := strings.TrimSpace(shelf)
		if err := labelled(trimmed); err != nil {
			return nil, fmt.Errorf("agent: the merchant published a category that is not a label: %w", err)
		}
		shelves = append(shelves, trimmed)
	}
	return shelves, nil
}

// declined adds the categories the interpreter would not propose to a discovery
// failure, and leaves every other failure alone.
//
// Issue #254's legibility half, one hop from where it is decided. A reading that
// narrowed only by a shelf this shop does not stock arrives here with nothing
// selective left in it, and nothingIdentifies then says "the interpretation names
// nothing to go looking for" — which is true of the set that survived and
// misattributes the cause, since the interpretation named something and
// interpret.ground removed it. That sentence is the whole of what an operator has
// to act on, and #254's complaint is precisely a demonstration that *reads as* a
// broken interpreter.
//
// **Only on the way out, and only on a failure.** A successful proposal says
// nothing about this: it found the offer, the surface will render what was signed,
// and the buyer's own working is not something a screen should carry — see
// interpret.Interpretation.DeclinedCategories. The error is wrapped rather than
// replaced, so errors.Is still reaches ErrNothingToBuy and console's 422 mapping
// is untouched.
func declined(err error, categories []string) error {
	if err == nil || len(categories) == 0 {
		return err
	}
	return fmt.Errorf("%w; the interpreter also declined to narrow by %s, which this merchant has no shelf for",
		err, strings.Join(categories, ", "))
}

// labelled reports whether one published category, already trimmed, is shaped like
// a shelf name.
//
// The line-break check is the one worth naming: the instruction lists these one per
// line, so a break inside an entry is a shop writing lines of its own into the text
// that tells a model what its job is. Bounded in runes rather than bytes for
// maxTitle's reason — the question is how much of a label a reader, human or
// otherwise, is being handed.
func labelled(shelf string) error {
	switch {
	case shelf == "":
		return errors.New("it is blank, and no offer can be filed under nothing")
	case utf8.RuneCountInString(shelf) > maxShelfName:
		return fmt.Errorf("%q is %d characters and a shelf name is bounded at %d",
			shelf, utf8.RuneCountInString(shelf), maxShelfName)
	case strings.ContainsFunc(shelf, breaksALine):
		return fmt.Errorf("%q carries a line break, and these are listed one per line "+
			"in the text that tells a model what its job is", shelf)
	default:
		return nil
	}
}

// breaksALine reports whether a rune would start a new line in the text a category
// is listed in.
//
// unicode.IsControl on its own is the check that looks right and is not: U+2028
// LINE SEPARATOR and U+2029 PARAGRAPH SEPARATOR are categories Zl and Zp rather
// than Cc, so a category joining "ladders" to a second line with one of those
// carries no control character at all and is two lines to anything that renders
// or tokenises it. Naming them is cheaper than reasoning about which consumer
// honours which, and they are written as escapes below because a literal one in
// this file would be a character no reviewer can see.
func breaksALine(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}

// maxShelves and maxShelfName are how much of a shop's vocabulary this agent will
// repeat to a model.
//
// Bounds on this agent's willingness to repeat, not claims about how a shop may
// organise itself — maxTitle draws the same distinction in the same words. What
// sets the numbers is what a vocabulary is: deploy/catalogue.json has 7
// categories, the recorded shop snapshot has 24, and the two together come to 30,
// so 128 is four times the widest shop this repository has ever served and still
// refuses a catalogue dumped in whole. The longest of those 30 names is
// `kitchen-accessories` at 19 characters, and 48 leaves room for a longer one
// while refusing a sentence.
const (
	maxShelves   = 128
	maxShelfName = 48
)

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

// Describe asks the merchant to publish what one offer is, by identifier.
//
// It is settle's description half with the interpretation taken out. A caller
// that already holds an identifier and needs the words a person can read has no
// prompt to re-interpret and no business inventing one — a console listing a
// watch it started an hour ago is the case this was added for, and issue #242 is
// where it came from: `gtin:05012345678900` is the string a constraint carries,
// and candidate's own comment already says it "is nothing anybody can act on".
//
// **Asked rather than remembered, which is settle's rule read literally**: "the
// description has to come from the party that publishes it, and inventing one
// here would put the shop's own words in the buyer's mouth." The alternative
// considered and rejected was a title travelling on Authorisation — that type is
// also a wire shape a browser posts, so the name would then be one this agent
// republished on the word of whoever made the request, and the two ways into a
// watch would differ in who said it.
//
// **Nothing about the answer is signed, and nothing about it can be.** Offer's
// own comment is that no verifier sees a title and no constraint addresses one.
// So a caller that draws this has to say whose word it is; a caller that
// *decides* anything from it has misread what it is for, exactly as with
// Offer itself.
//
// **And because nothing signs it, the answer is bounded here** — see maxTitle.
func (c *Client) Describe(ctx context.Context, item string) (Offer, error) {
	if item == "" {
		// QuoteItem guards the same argument the same way. Without it the query
		// falls back to identifying(nil), which is empty, and the merchant is
		// asked a malformed question this package should never have put to it.
		return Offer{}, errors.New("agent: no item to ask the merchant to describe")
	}

	// nil constraints, because a named item is what candidates queries on: the
	// interpretation plays no part once an identifier is in hand, which is the
	// same substitution settle relies on.
	found, err := c.candidates(ctx, nil, item)
	if err != nil {
		return Offer{}, err
	}
	if len(found) == 0 {
		return Offer{}, fmt.Errorf("%w: the merchant lists no offer identified as %q",
			ErrNothingToBuy, item)
	}
	// settle's refusal, one call along and for the same reason — a search on
	// item.id has exactly one honest answer. It matters more here than there:
	// a constraint naming the wrong item is refused by a verifier at the moment
	// of purchase, and a *name* taken from the wrong offer is a screen calmly
	// telling somebody they bought something they did not, with every signature
	// in the transaction still valid.
	if found[0].ID != item {
		return Offer{}, fmt.Errorf(
			"%w: %w: asked the merchant to describe the offer identified as %q and it answered with %q instead",
			ErrMerchantAnsweredDifferently, ErrNothingToBuy, item, found[0].ID)
	}
	// The right offer can still answer with something that is not a name. See
	// maxTitle: the identity check above cannot catch it, because the offer is
	// the one that was asked about.
	if n := utf8.RuneCountInString(found[0].Title); n > maxTitle {
		return Offer{}, fmt.Errorf(
			"agent: the merchant's name for %q is %d characters and a caption is bounded at %d",
			item, n, maxTitle)
	}
	return Offer(found[0]), nil
}

// maxTitle is how long a name this call will repeat can be.
//
// **obs.maxIDLen's argument, one field along and one type size up.** That
// constant bounds an adopted correlation ID on the recorded ground that "an
// inbound header is attacker-controlled and ends up in an SSE frame and a log
// line, so it is bounded before either" — and a title is the same kind of
// string with more of a screen behind it. Issue #242 put it in an `<h2>` at the
// head of the three-lane view, above a digest three parties computed
// independently, so the merchant now writes the largest sentence on a page that
// also shows signed mandates. Describe's other refusal covers a merchant
// answering about the *wrong* offer; this one covers the right offer answering
// with something that is not a name.
//
// It is a bound on *this agent's willingness to repeat*, not a claim about what
// a shop may call its own stock. What sets the number is what a caption is: the
// longest title deploy/catalogue.json ships is 46 characters and the longest of
// the 194 in issue #160's wider snapshot is 41, so 120 is nearly three times the
// longest real one and still refuses a paragraph. Runes rather than bytes,
// because `Belgrade → Palma de Mallorca` is 28 characters and 30 bytes and the
// bound is about what a person reads.
//
// **Refused rather than truncated, and that is the whole of the choice.** An
// ellipsis would be this agent putting words in the shop's mouth under a line
// that says these are the shop's own words — settle's rule, which Describe's own
// comment quotes, forbidding exactly that. A refusal lands on console.Run.title
// as no name, which is the state that type already documents for "a counterparty
// answering nonsense" and which the screen already draws as the header it drew
// before #242.
const maxTitle = 120

// identifying returns the constraints that say what to go looking for.
//
// A leaf whose field the registry calls selective — item.id, item.category,
// item.attr.*, merchant.id, merchant.category today, and whatever else is
// registered as one tomorrow without this function changing — and, from a group,
// whatever part of it a catalogue can be asked about. Everything else is a term
// of the purchase rather than a description of one, and the terms are what the
// watch is waiting to be met.
//
// **The classification is asked for rather than reproduced**, which is issue
// #132 and is argued at interpret.Narrowing. This function decides only what to
// do with the answer, and after issue #203 there is no decision left here that
// the registry does not make.
//
// **A group used to be dropped whole, and that was the last one.** This loop
// tested c.Field for nil and went round again, so all, any and not — which carry
// op and of, never a field — never travelled, with nothing logged and nothing
// failing. What a group contributes is not the same answer for the three of
// them, and it is not one this package could reach by walking into children: an
// all may send the part of itself a catalogue can answer, an any may not send
// one branch without the others, and a not may travel only when every fact
// beneath it is answerable. constraint.Narrowing is where those three are argued
// from the one property that separates them, and the answers arrive here as
// constraints to append.
//
// So a group now contributes between nothing and several entries, which is why
// this appends a list rather than deciding a constraint's fate one at a time.
// The list it appends to is conjunctive — every constraint in a search must hold
// — and that is exactly why an all's children may join it individually.
//
// What is dropped is dropped from a *query*, never from the mandate. See
// Authorise for why that distinction is the whole of this function.
func identifying(constraints []generated.Constraint) []generated.Constraint {
	out := make([]generated.Constraint, 0, len(constraints))
	for _, c := range constraints {
		out = append(out, interpret.Narrowing(c)...)
	}
	return out
}

// nothingIdentifies says why a query came out empty, in terms that tell the two
// cases apart.
//
// The message this replaced counted the whole set and said none of them read a
// fact the registry calls selective. That was true of an interpretation which
// placed bounds and named no object, and equally true of one whose every
// constraint was a group — and issue #203 is the second of those being a defect
// rather than a reading. A reader met the same sentence either way, so the one
// case where something the user wrote about the object failed to reach the
// merchant looked exactly like the case where they never wrote one.
//
// It counts nodes rather than asking the registry anything, because the
// distinction it is drawing is structural: a leaf carries a field, a group
// carries children. *Why* each of them narrowed nothing is
// constraint.Narrowing's answer and is not restated here — a leaf usually
// because its field states a term of the purchase, a group because what it says
// about the object does not survive on its own — and the counts are what a
// reader needs to know which of the two questions to go and ask.
func nothingIdentifies(constraints []generated.Constraint) string {
	if len(constraints) == 0 {
		return "the interpretation placed no constraints at all, so there is nothing to go looking for"
	}

	var leaves, groups, neither int
	for _, c := range constraints {
		switch {
		case c.Field != nil:
			leaves++
		case len(c.Of) > 0:
			groups++
		default:
			neither++
		}
	}

	counts := make([]string, 0, 3)
	if leaves > 0 {
		counts = append(counts, tally(leaves, "leaf", "leaves"))
	}
	if groups > 0 {
		counts = append(counts, tally(groups, "group", "groups"))
	}
	if neither > 0 {
		counts = append(counts, tally(neither,
			"node carrying neither a field nor children",
			"nodes carrying neither a field nor children"))
	}

	return fmt.Sprintf(
		"the interpretation names nothing to go looking for — of its %d constraints, none narrows a merchant's catalogue search: %s",
		len(constraints), strings.Join(counts, ", "))
}

// tally renders one count of that message with its noun agreeing with the
// number.
//
// Four lines for grammar, and worth it here: this sentence is the whole of what
// a person has to act on when discovery finds nothing, and "1 groups" is the
// sort of thing that makes a reader stop trusting the numbers beside it.
func tally(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
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
