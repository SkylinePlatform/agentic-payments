package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
)

// Model is one provider's completion endpoint, and nothing about a provider
// crosses it: bytes in, bytes out.
//
// The instruction is what the model is told about the job and the vocabulary;
// the prompt is what the user typed; the schema describes the answer's shape as
// JSON Schema. A provider that has no structured-output mode ignores the schema
// and gets the same instruction, which already states the shape in prose — the
// answer is then checked exactly as hard, because everything downstream of this
// interface treats what comes back as untrusted text either way.
//
// # This is the seam a second provider arrives at
//
// Issue #17's third box is that adding one requires no change outside this
// package, and this interface is what makes that true: ModelInterpreter names no
// provider type, holds no key and builds no request, so a second provider is one
// file beside gemini.go and nothing else here.
//
// Being able to *ask* for it from a command line is a separate cost and worth
// separating: cmd/agent's -interpreter flag has a case per name it accepts, and
// that case is the only line outside this package a second provider touches.
//
// # Why it is not http.RoundTripper or a provider SDK
//
// A RoundTripper would put request building on this side of the seam, which is
// the half that differs most between providers — the endpoint, where the key
// goes, what the structured-output field is called, how the answer is nested.
// An SDK would put a vendor's types in this package's signatures, and
// backend/go.mod has one non-test dependency on purpose: this repository
// implements wire formats rather than taking dependencies for them, which is
// what pkg/httpsig and pkg/sdjwt are. Neither buys anything across a seam this
// narrow.
type Model interface {
	// Complete asks the model for one answer. Implementations perform exactly
	// one call: there is no repair loop above this interface and there must not
	// be one below it — see ModelInterpreter.Interpret for why.
	Complete(ctx context.Context, instruction, prompt string, schema []byte) ([]byte, error)
}

// ModelInterpreter reads a sentence with a language model.
//
// It does four things and deliberately nothing else: it asks the Model, it
// decodes the answer with the same decode ScriptedInterpreter uses, it calls
// Validate, and it declines to propose a narrowing the shop it is reading against
// cannot satisfy. Script's own doc comment promised the second one — the scripted
// constraints are written as JSON "because it is the form a model would answer
// in, which keeps both implementations of IntentInterpreter going through the
// same decoding rather than the scripted one having a private route into the
// type" — and this is the type that makes the sentence true. The fourth is issue
// #254 and is argued at ground.
//
// # What it does not do, and why each one is a refusal
//
// It does not repair or retry, and a constraint naming a field the verifier does
// not know comes back as an error from Interpret rather than being dropped. The
// three obvious alternatives are all worse:
//
//   - **Dropping the offending constraint is the dangerous one.** It converts "a
//     limit the user described that nobody can enforce" into "a mandate with
//     fewer limits", which is the silent widening AGENTS.md's "Open for
//     extension is not open at runtime" paragraph forbids. The user would be
//     shown, and would sign, a set of limits smaller than the sentence they
//     typed, with nothing on the screen saying so.
//   - **Repairing it** means this package deciding what the model meant, which
//     is the interpreter judging its own output — and the whole reason the
//     interpretation goes to a human and then to a verifier is that nobody takes
//     it on trust.
//   - **Retrying** is one more draw from the same distribution, for a demo that
//     has a deterministic path a flag away. One call, and a visible failure.
//
// **The fourth step removes a constraint, and the first bullet is why that needs
// saying rather than hiding.** It is a different input and a different act: the
// bullet is about a limit the *verifier cannot read*, which Validate still
// refuses outright and which ground never sees, since it runs afterwards on a set
// the verifier's own parser has accepted. What it removes is a category the shop
// has no shelf for — readable, enforceable, and matching nothing — and nothing is
// widened by removing it, because agent.Propose pins the set to one item.id
// before the surface is asked for a signature. ground carries the argument in
// full, including the two things it refuses to touch.
//
// A value is safe to share between goroutines once built: nothing mutates it
// after NewModel returns, and Interpret builds the instruction from an immutable
// string, a clock reading and its caller's argument.
type ModelInterpreter struct {
	model Model

	// vocabulary is the instruction's body, built once from the registry.
	vocabulary string

	// schema is the answer's shape as JSON Schema, built once from the same
	// registry.
	schema []byte

	// clock supplies the reference instant a relative date is read against.
	clock authz.Clock
}

// NewModel builds a model-backed interpreter.
//
// Named for NewScripted rather than for the Model interface: the two
// constructors are the pair, and what each returns is an IntentInterpreter.
//
// **It performs no I/O.** The instruction and the schema are derived from the
// constraint registry, which is fixed at initialisation, and the Model is not
// called until Interpret is. That is the property that lets every test in this
// package build one — hard rule 4 forbids a test that depends on a live model,
// and a constructor that dialled anything would put one in every test that
// merely wires the type up.
//
// The clock is here because a sentence like "this summer" is not a date range
// anywhere but in the reader's head, and the model needs a reference instant to
// resolve it against. Reading the wall clock directly is hard rule 5's
// prohibition; a clock nobody injected would also make the instruction differ
// between two runs of the same test.
func NewModel(model Model, clk authz.Clock) (*ModelInterpreter, error) {
	if model == nil {
		return nil, errors.New("interpret: a model-backed interpreter needs a model to call")
	}
	if clk == nil {
		return nil, errors.New("interpret: a model-backed interpreter needs a clock to read relative dates against")
	}

	schema, err := answerSchema()
	if err != nil {
		return nil, fmt.Errorf("interpret: describing the answer's shape: %w", err)
	}
	return &ModelInterpreter{
		model:      model,
		vocabulary: vocabulary(),
		schema:     schema,
		clock:      clk,
	}, nil
}

// Interpret asks the model to read the sentence, and refuses whatever a verifier
// could not.
//
// One call, one answer, no second chance. Everything between the answer arriving
// and this function returning is deterministic: the decoding is
// encoding/json's, and the checking is the verifier's own parser through
// Validate rather than a list of field names kept here — a copy would drift in
// the direction that accepts what the verifier cannot read.
//
// The raw answer is quoted in the error when it fails to decode or to validate.
// The alternative is a demonstration where the model said something wrong and
// nobody can see what, and the answer is a proposal that has reached no screen
// and been signed by nobody, so there is nothing in it to keep back.
//
// # envelope, and why decode still reads the constraints half
//
// The answer is one JSON object, not the bare array this used to be, but the
// constraints inside it go through the very same decode ScriptedInterpreter's
// Interpret does: envelope.Constraints is held as a json.RawMessage rather than
// unmarshalled directly, precisely so that both implementations of
// IntentInterpreter still decode a constraint list through one function. What
// changed is the shape one level up, not how a leaf becomes a generated.Constraint.
//
// # The shelves are used twice, and the second use is the one a test can drive
//
// They go into the instruction, so the model is told what the shop calls things
// rather than left to guess — which is the whole of issue #254. That makes the
// right answer *likely* and cannot make it certain, and no test may drive a live
// model to find out. So ground runs afterwards on what came back: a category the
// merchant does not sell does not become a constraint. Necessary and sufficient
// are different halves and only the second is checkable, which is what
// TestAHumanWordForAShelfDoesNotBecomeAConstraintTheShopCannotSatisfy drives.
//
// **The order is Validate, then ground, then the emptiness check**, and it is not
// interchangeable. Validate first means every refusal this type already made
// happens exactly as it did, in the same order and with the same error, so
// grounding cannot turn a constraint the verifier could not read into one quietly
// removed. ground therefore only ever sees a set the verifier's own parser has
// accepted, and it only removes, so what survives still parses and there is
// nothing to check a second time.
func (m *ModelInterpreter) Interpret(ctx context.Context, prompt string, shelves Shelves) (Interpretation, error) {
	if strings.TrimSpace(prompt) == "" {
		// The same refusal ScriptedInterpreter makes for an unscripted prompt,
		// one step earlier. An empty sentence has no limits in it, so anything
		// that came back would be the model's invention rather than a reading.
		return Interpretation{}, errors.New("interpret: there is no sentence to read")
	}

	answer, err := m.model.Complete(ctx, m.instruction(shelves), prompt, m.schema)
	if err != nil {
		return Interpretation{}, fmt.Errorf("interpret: asking the model to read %q: %w", prompt, err)
	}

	var envelope struct {
		Constraints json.RawMessage `json:"constraints"`
		Quantity    int             `json:"quantity"`
		Trigger     Trigger         `json:"trigger"`
		// A pointer, and it is the only field here that needs to be one.
		//
		// Interpretation.Rank cannot tell an omitted "rank" from one answered as
		// {} — both decode to the zero value — and the two are different answers.
		// Absent is a sentence that ranked nothing, which is most sentences and
		// is exactly Rank's honest zero. Present and empty is a model that said
		// it had a preference and did not say what it was, and reading *that* as
		// silence puts issue #262 straight back. The pointer is what keeps the
		// distinction long enough to refuse the second one.
		Rank *struct {
			By        RankField     `json:"by"`
			Direction RankDirection `json:"direction"`
		} `json:"rank"`
	}
	if err := json.Unmarshal(answer, &envelope); err != nil {
		return Interpretation{}, fmt.Errorf("interpret: the model's answer to %q %w; it said: %s",
			prompt, err, excerpt(answer))
	}

	constraints, err := decode(string(envelope.Constraints))
	if err != nil {
		return Interpretation{}, fmt.Errorf("interpret: the model's answer to %q %w; it said: %s",
			prompt, err, excerpt(answer))
	}

	// The interface's contract. It is the whole reason this type is as short as it
	// is: what a model returns is the one input in this system nobody can
	// predict, so it is the one that must be run past the verifier's parser
	// before a user is shown it.
	//
	// **The trigger is checked by the same call and refused on the same terms.**
	// A model that answered a word this package does not define — or answered
	// nothing, which is what a provider ignoring the schema does — leaves the
	// agent with no honest reading of when the sentence asked to buy, and
	// Trigger records why both available guesses are wrong. So it fails here,
	// loudly, rather than downstream as a purchase at a moment nobody chose.
	//
	// **The rank is checked by the same call and on narrower terms**, and the
	// asymmetry is worth reading rather than assuming from the line above. An
	// omitted rank is a legitimate answer — the sentence preferred nothing, which
	// is most sentences — where an omitted trigger is not. So what Validate
	// refuses is only a preference that was stated and cannot be applied. The one
	// case Validate cannot see is refused immediately below it.
	out := Interpretation{
		Constraints: constraints,
		Quantity:    envelope.Quantity,
		Trigger:     envelope.Trigger,
	}
	if envelope.Rank != nil {
		out.Rank = Rank{By: envelope.Rank.By, Direction: envelope.Rank.Direction}
		if !out.Rank.Stated() {
			// Refused here rather than by Validate, which sees only the value and
			// would read this as the sentence having ranked nothing. See the
			// envelope's own comment for why the two are not the same answer, and
			// ErrUnknownRank for why silence is the one that is legitimate.
			return Interpretation{}, fmt.Errorf(
				"interpret: the model's reading of %q states a preference with nothing in it: %w; it said: %s",
				prompt, ErrUnknownRank, excerpt(answer))
		}
	}
	if err := Validate(out); err != nil {
		return Interpretation{}, fmt.Errorf("interpret: the model's reading of %q is not something a verifier could read: %w; it said: %s",
			prompt, err, excerpt(answer))
	}

	// Issue #254. Every constraint above is one a verifier can read; this asks the
	// narrower question of whether one that selects by category selects anything
	// this shop stocks, and declines to propose the ones that cannot. See ground
	// for why this is not the drop the doc comment on this type forbids.
	out.Constraints, out.DeclinedCategories = ground(out.Constraints, shelves)
	if len(out.Constraints) == 0 {
		// Wrapping ErrNoConstraints rather than minting a sentinel, because that
		// is what is now true of this reading and the callers that translate it
		// already say the right thing: console maps it to 422, and the message
		// below is what names the cause. A reading whose *only* narrowing was a
		// shelf this shop does not stock has nothing left to put in front of
		// anybody, and returning it would be an unbounded mandate with a blank
		// space where the limits should be — which is the one misreading the
		// Trusted Surface cannot catch.
		return Interpretation{}, fmt.Errorf(
			"interpret: the model's reading of %q narrows only by categories %s does not sell: %w; it said: %s",
			prompt, shelvesOrNone(shelves), ErrNoConstraints, excerpt(answer))
	}
	return out, nil
}

// shelvesOrNone names the published shelves for an error message.
//
// The list rather than a count, because the whole failure is a vocabulary
// mismatch and the reader's next question is which words were available. Bounded
// by the same argument Shelves is: a shop has shelves, not offers, so this is a
// line and not a page.
func shelvesOrNone(shelves Shelves) string {
	if len(shelves) == 0 {
		// Unreachable from the caller above — ground returns its input untouched
		// when nothing was published, so an empty reading there was already empty
		// and Validate refused it. Kept because a message reading "categories
		// does not sell" would be worse than one line of defence.
		return "this merchant"
	}
	return "this merchant (it sells " + strings.Join(shelves, ", ") + ")"
}

// instruction is what the model is told: the job, the vocabulary and the shapes,
// plus the shop's own shelves and the instant a relative date is read against.
//
// Built per call rather than once, because the fixed half is the registry's and
// the other two move. A process that stayed up over midnight and kept telling the
// model it was yesterday would resolve "tomorrow" to today, and the mandate would
// be signed before anybody noticed the date on the screen was the wrong one. The
// shelves move for a smaller reason and a more mundane one: they are a
// counterparty's answer, fetched per authorisation, and a merchant serving a live
// catalogue publishes a wider list than the same merchant did before it fetched
// one.
//
// A merchant that published nothing contributes nothing, rather than an empty
// heading. Telling a model that the shop sells no categories is worse than not
// raising the subject: the honest reading of the sentence is then whatever the
// model would have said anyway, and ground leaves it alone to match.
func (m *ModelInterpreter) instruction(shelves Shelves) string {
	return m.vocabulary + shelfInstruction(shelves) + "\n\nThe current date and time is " +
		m.clock.Now().UTC().Format("2006-01-02T15:04:05Z") +
		". Resolve every relative date — \"this summer\", \"next month\", \"by Friday\" — against it.\n"
}

// shelfInstruction is what the model is told about the shop it is reading against.
//
// # The two halves say opposite things, and that is the design
//
// **Categories are closed, so narrow by one.** The list is here, it is the shop's
// own spelling, and the model's job is to pick the shelf that covers what the
// sentence asked for — "a flight" is `flights`, "mascara" is `beauty`. That last
// pair is why the list has to be *given* rather than guessed at: `flight` →
// `flights` is a plural a stemmer could reach, and `mascara` → `beauty` is a
// taxonomy nothing but the list can answer, which is also why the deterministic
// half of this can only drop and never repair. See ground.
//
// **Attribute values are open, so narrow only where the sentence hands you one.**
// Nothing publishes them — Shelves says why an endpoint that did would grow with
// the shop — so the instruction asks for confidence rather than for a lookup: a
// brand or a model name is in the sentence as the shop would file it, and "to
// Palma" is a city where the shop may hold an airport code. Issue #254 measured
// both: `item.attr.brand eq "Essence"` matched, `item.attr.route.destination eq
// "Palma"` matched nothing while the shop said `PMI`.
//
// This half is prose and nothing checks it, which is stated here rather than left
// to be discovered. There is no bounded set to check a route against, so an
// uncertain attribute value is exactly the thing this design gives up on being
// certain about — and the reason that is affordable is that the cost of leaving
// one out is a wider candidate list, while the cost of inventing one is a search
// that matches nothing at all.
//
// # It contradicts the paragraph above it unless it says so, and it does
//
// vocabulary() ends by telling the model to say everything the sentence implies,
// and its own example is "a destination with no origin" — because beat 3 of the
// built scenario is a user reading `from Belgrade` on an approval screen and
// disagreeing with it. Read beside this, that is two orders: infer the origin, and
// do not invent a code you cannot ground. So this text names the collision and
// bounds it. What still has to be stated is anything in a vocabulary the model was
// given in full — a date worked out from "this summer", a bound worked out from
// "cheapest" — and what may be left out is only a fact whose *spelling* belongs to
// the shop.
//
// **What that costs is worth being exact about.** On a model-backed run the route
// no longer reaches the mandate, so the user does not read `from Belgrade` as a
// constraint. They are not left guessing: Propose narrows to one offer and appends
// item.id, so the screen names one flight and shows the merchant's own title for
// it. The inference is still visible — as an identifier and a caption rather than
// as two route constraints — and the scripted table, which is what the
// documentation's beats are asserted against, is untouched and still says BEG.
func shelfInstruction(shelves Shelves) string {
	if len(shelves) == 0 {
		return ""
	}

	return `
The shop this sentence will be searched against sells these categories, and they
are the only values item.category may take. They are the shop's own spellings,
which are usually not the word a person would use:

` + shelves.listing() + `
Pick the one that covers what the sentence asked for — "a flight" is a category
above, and so is "mascara", though neither is spelled the way the sentence spells
it. If none of them covers it, do not constrain item.category at all: a category
this shop has no shelf for matches nothing, every constraint in the answer has to
hold at once, and one invented category therefore throws away a reading that was
otherwise right.

Nobody has told you what values item.attr.<name> holds. So narrow by one only
where the sentence hands you the value the shop itself would file the object
under — a brand, a model name, a code. Where you would have to translate the
sentence's word to get there, leave that attribute out and let the other limits
do the work: "to Palma" is a city, and a shop may file the same flight under an
airport code, so a route destination is a guess.

This is the one place the instruction to say everything the sentence implies
stops, and it stops narrowly. It is not a licence to drop a date or a price you
worked out — those are values in a vocabulary you have been given in full, and
they still belong in the answer. It is only about facts whose spelling belongs to
the shop, and the origin of a flight is the example above being caught by it:
state a route only in the shop's own codes or not at all.

Leaving a fact out costs a longer list of candidates for the person to be shown.
Inventing one costs the whole search.
`
}

// excerpt is as much of a model's answer as belongs in an error message.
//
// Bounded because the answer is whatever came back over a network, and an error
// that carries an unbounded string is a log line nobody can read and a response
// body somebody else chose the size of.
func excerpt(answer []byte) string {
	const enough = 240
	if len(answer) <= enough {
		return string(answer)
	}
	return string(answer[:enough]) + "… (truncated)"
}

// vocabulary is the instruction's fixed body, derived from the registry.
//
// **Derived, never written out.** constraint.Vocabulary is the same table
// lookupOperator checks a mandate against, so a field or an operator added there
// appears here without anybody remembering to add it — and
// TestThePromptNamesEveryFieldTheVerifierKnows is what fails if this stops being
// true.
//
// # This interpreter produces leaf constraints only, and that is a stated
// # narrowing
//
// The registry publishes group operators as well as leaf ones — all, any and not
// today — and the instruction below names them as forbidden rather than omitting
// them. The list is derived by subtraction, so a fourth would appear there too
// without the prose being edited. Two reasons for naming them at all. The
// registry publishes them, so an instruction claiming to list what the verifier
// knows and quietly leaving three out would be a smaller lie of exactly the kind
// this file exists to prevent. And a model that has been told the operators
// exist and must not be used produces flat lists more reliably than one left to
// infer it from an enum.
//
// The narrowing is enforced by the schema rather than by the prose: op is an
// enum of the leaf operators, so a group node is refused at the boundary.
//
// **It used to buy something else as well, and that reason has been spent.**
// internal/agent's identifying() read leaves only when it built the merchant
// search, so an interpretation containing a group would have had part of itself
// dropped from discovery with nothing failing. Keeping this schema flat is what
// made that unreachable rather than merely unexercised. Issue #203 closed the
// drop — a group is asked what it narrows now, and constraint.Narrowing answers
// per node kind — which is the order that was required: the consumer first, the
// producer after.
//
// So the two reasons above are the whole of why the narrowing stands today, and
// widening it is a decision about what a model should be trusted to compose
// rather than something the agent's discovery half is still waiting on.
func vocabulary() string {
	var b strings.Builder

	b.WriteString(`You read one sentence a person typed into a shopping agent and turn it into the
limits a payment verifier will enforce, the basket size the sentence asked for,
whether it asked to buy now or to wait, and which of several offers it would
rather have. You are proposing limits, never deciding a purchase: what you answer
is shown to the person, signed by them, and then enforced by software that never
sees this sentence.

Answer with one JSON object and nothing else, with three required keys —
"constraints", "quantity" and "trigger" — and an optional fourth, "rank".

"constraints" is a flat JSON array of leaf constraints. Every element is an
object with exactly three keys — "op", "field" and "value".

These are the facts a constraint may name. Nothing outside this list can be
enforced, and naming anything else means the whole answer is refused:

`)

	for _, spec := range constraint.Vocabulary() {
		fmt.Fprintf(&b, "  %-18s %-7s %s\n", spec.Name, spec.Kind, strings.Join(spec.Operators, ", "))
	}
	fmt.Fprintf(&b, "  %-18s %-7s %s\n", "item.attr.<name>", constraint.KindText,
		strings.Join(textOperators(), ", "))

	b.WriteString(`
The last line is the open half of the vocabulary: any fact about the object that
the named fields do not cover goes under item.attr., as text — a flight's route
is item.attr.route.origin and item.attr.route.destination.

A value's shape follows the field's type:

  money    an object {"amount": <whole number of minor units>, "currency": "<ISO 4217 code>"}
           — $200.00 is {"amount": 20000, "currency": "USD"}
  time     an RFC 3339 timestamp, such as "2026-06-01T00:00:00Z"
  number   a whole number
  text     a string

and then the operator decides how many of them:

  in, nin           an array of values of the field's type
  between, within   an object {"from": <value>, "to": <value>}
  every other       one value of the field's type

`)

	fmt.Fprintf(&b, `This verifier also implements operators that combine constraints — %s — and you
must not use any of them. "constraints" is a flat list: every constraint in it
has to hold.

"quantity" is a whole number: how many of the item the sentence asks for. Say 1
when nothing in the sentence names a count — that is the ordinary case, true of
almost every sentence you will read — and say the number itself when it does,
such as "two tickets" or "three of these". Do not fold it into a constraint:
"how many to buy" is not a limit a verifier checks at the moment of purchase,
it is the size of the basket the person is about to be shown and asked to
approve, and a bound such as quantity lte 2 says at most two — it does not say
how many to put in the basket. Both can appear in the same sentence and mean
different things: "two tickets... up to $160 all in" is a quantity of 2 and,
separately, a constraint capping the count at 2 as well, because the cap
protects the person if this answer is ever read back and re-priced, while the
quantity is what actually gets bought today.

"trigger" is one of exactly two words, and it is about the sentence rather than
about any price:

  conditional  the sentence made the purchase wait on something changing —
               "when it drops below $200", "if it goes on sale", "once it is
               back in stock". Something is being asked for that is not true
               yet.
  immediate    the sentence asked to buy, on the terms it stated — "two
               tickets, up to $160 all in", "find and buy the cheapest one".
               A cap is not a condition: "up to $160" says what the person will
               pay, not that they want to wait for a better price.

Answer one of those two words. Do not invent a third, do not leave it out, and
do not decide it by looking at what anything costs — you have not been told any
prices, and whether today's price is acceptable is checked later by software
whose whole job that is. The only question here is what kind of sentence you
were given.

"rank" is where a word like "cheapest" goes, and it is the one key you may leave
out. Include it only when the sentence prefers one offer over another; omit it
entirely when the sentence does not, which is most sentences. When you include it,
it is an object with exactly two keys:

  by         "price" — the price the shop is asking today. It is the only thing
             you may rank on, because it is the only orderable fact a shop
             publishes about an offer that a person actually means.
  direction  "ascending" for the lowest first, which is what "cheapest" asks
             for, or "descending" for the highest first.

Say both keys or omit the whole object. Half of a preference cannot be acted on
and the whole answer is refused for it, exactly as it is for a constraint nobody
can read. Do not answer "rank": {} — leave the key out.

A rank is not a limit and does not replace one. It decides which of several offers
already inside the limits gets bought, and it is shown to the person beside the
limits rather than signed with them, because no verifier can check a preference.

Two things decide whether a constraint is a good reading.

Say only what can be checked at the moment of purchase. "Cheapest", "best" and
"fastest" are not refutable — no merchant can establish what the whole market was
offering at an instant — so turn such a sentence into a bound on the amount, and
say the preference itself in "rank" rather than in a constraint. Both belong in
the same answer: the bound is what protects the person if the purchase turns out
to cost more than they thought, and the rank is what makes the word they typed
change which offer is bought.

Say everything the sentence implies, including what it leaves out. A destination
with no origin, a season with no dates, "this one" with no identifier: the person
is about to read your answer on an approval screen, and an inference they
disagree with is the thing that screen exists to catch. An inference they never
see is the thing it cannot.
`, strings.Join(groupOperators(), ", "))

	return b.String()
}

// groupOperators is OperatorNames minus everything any field accepts — the three
// that combine nodes.
//
// Derived by subtraction rather than written out, so that the instruction's
// "must not use" list cannot fall behind a fourth group operator. That is the
// same argument as vocabulary()'s, and it is the reason
// TestThePromptNamesEveryFieldTheVerifierKnows can assert over the whole of
// OperatorNames rather than over a subset somebody maintains.
func groupOperators() []string {
	leaf := make(map[string]struct{})
	for _, spec := range constraint.Vocabulary() {
		for _, op := range spec.Operators {
			leaf[op] = struct{}{}
		}
	}

	var out []string
	for _, op := range constraint.OperatorNames() {
		if _, isLeaf := leaf[op]; !isLeaf {
			out = append(out, op)
		}
	}
	return out
}

// leafOperators is the union of every field's operators, which is what the
// schema's op enum is.
func leafOperators() []string {
	seen := make(map[string]struct{})
	var out []string
	for _, spec := range constraint.Vocabulary() {
		for _, op := range spec.Operators {
			if _, dup := seen[op]; dup {
				continue
			}
			seen[op] = struct{}{}
			out = append(out, op)
		}
	}
	slices.Sort(out)
	return out
}

// textOperators are the operators the open half of the vocabulary accepts.
// item.attr.<name> is text by construction, so they are whichever operators
// apply to a text field — read off one rather than assumed.
func textOperators() []string {
	for _, spec := range constraint.Vocabulary() {
		if spec.Kind == constraint.KindText {
			return spec.Operators
		}
	}
	// Unreachable while any text field is registered, and item.id and
	// item.category both are. Answering with nothing beats answering with a
	// guess: an empty column in the instruction is visible, and a made-up one
	// is not.
	return nil
}

// answerSchema describes the answer as JSON Schema: an object carrying the
// leaf constraints, the basket size, the trigger and the rank, side by side.
//
// # Why an object and not the bare array this used to be
//
// Interpretation carries four facts a model has no other way to answer in one
// call: the limits, how many of the item the sentence asked for, whether it
// asked to buy now, and which of several offers it would rather have. quantity is
// required in the schema for the same reason the vocabulary tells the model to say
// 1 rather than say nothing — an omitted field reads as "the model had no
// opinion", and the honest default for almost every sentence is a stated 1, not a
// silent absence Interpretation.Quantity's zero value would have to stand in for
// either way. rank is the exception and the required list below says why.
//
// # Why "constraints" is not contracts/authz/constraint.json
//
// That schema is the canonical model's, and it is the one recursive type in it —
// `of` $refs the file itself. Structured-output modes will not take a
// self-referencing schema, and the recursion is precisely the group half this
// interpreter does not produce. So this is the same vocabulary with the
// recursion removed rather than a second model: op is an enum of the leaf
// operators taken from the registry, and field is a plain string for the reason
// constraint.json gives for not making it an enum — item.attr.<name> is open, so
// closing the set here would make a route unexpressible.
//
// # The value union is the part that had to be measured
//
// A leaf's value is four different shapes depending on the field's kind and the
// operator, and the canonical schema leaves it untyped. A structured-output mode
// cannot: every subschema needs a type. So the union is spelled out — text,
// number, an amount object, an array of those, or a from/to pair of those — as
// anyOf, including one nesting of anyOf inside the range's bounds, because a
// between on money needs amount objects where a within on a time needs
// timestamps.
//
// That this shape is accepted at all was measured rather than assumed: two
// probes against gemini-flash-latest, carrying this union including the nesting,
// were answered 200 with an amount as an object and a within's bounds as
// timestamps. What the probes did not exercise is the money branch of the
// nested union — a between on an amount — so that one rests on the API having
// accepted the schema rather than on having been seen produced.
//
// A provider that refuses anyOf gets a 400 from its own endpoint, which is a
// loud failure at the boundary, and that is the shape to keep. The alternative
// considered and rejected was typing value as a string and letting the model
// stringify objects into it: that produces a well-formed answer carrying
// `"{\"amount\":15000,…}"` where an object belongs, which Validate then refuses
// a moment later with an error about the value's type rather than about the
// schema, three layers from the cause.
func answerSchema() ([]byte, error) {
	// The scalars a single operand can be. text and number are the two the
	// registry's kinds decode from directly; an RFC 3339 timestamp is text on
	// the wire, so it needs no variant of its own.
	scalars := []any{
		map[string]any{"type": "string"},
		map[string]any{"type": "integer"},
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"amount":   map[string]any{"type": "integer", "description": "minor units — 20000 is 200.00"},
				"currency": map[string]any{"type": "string", "description": "ISO 4217 code, upper case"},
			},
			"required":             []any{"amount", "currency"},
			"additionalProperties": false,
		},
	}

	value := map[string]any{
		"description": "one value of the field's type; an array for in and nin; an object with from and to for between and within",
		"anyOf": append(append([]any{}, scalars...),
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{"anyOf": scalars},
					"to":   map[string]any{"anyOf": scalars},
				},
				"required":             []any{"from", "to"},
				"additionalProperties": false,
			},
			map[string]any{
				"type":  "array",
				"items": map[string]any{"anyOf": scalars},
			},
		),
	}

	ops := make([]any, 0, len(leafOperators()))
	for _, op := range leafOperators() {
		ops = append(ops, op)
	}

	constraints := map[string]any{
		"type":     "array",
		"minItems": 1,
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op":    map[string]any{"type": "string", "enum": ops},
				"field": map[string]any{"type": "string", "description": "one of the named fields, or item.attr.<name>"},
				"value": value,
			},
			"required":             []any{"op", "field", "value"},
			"additionalProperties": false,
		},
	}

	return json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"constraints": constraints,
			"quantity": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "how many of the item the sentence asks for; 1 when nothing in the sentence names a count",
			},
			// An enum of the two triggers rather than a free string, so a
			// provider with a structured-output mode refuses a third word at
			// its own boundary. It is required for the reason quantity is: an
			// omitted field reads as "the model had no opinion", and there is
			// no honest default for this one — see Trigger. A provider that
			// ignores the schema is caught a moment later by Validate, which
			// is the same arrangement every other field here relies on.
			"trigger": map[string]any{
				"type":        "string",
				"enum":        []any{string(TriggerImmediate), string(TriggerConditional)},
				"description": "immediate when the sentence asked to buy on the terms it stated; conditional when it asked to wait for something to change",
			},
			// Two closed enums, which is the whole of what a structured-output
			// mode buys here: the *field names* in this answer are constrained
			// while the values generally are not, so a rank is the one place
			// where both halves can be closed and both are. A provider honouring
			// the schema cannot answer a preference over a fact the agent has no
			// way to order on, and one ignoring it is caught by Validate a moment
			// later — the same arrangement every other field here relies on.
			"rank": map[string]any{
				"type":        "object",
				"description": "which of several matching offers the sentence would rather have; omit the whole object when it names no preference",
				"properties": map[string]any{
					"by": map[string]any{
						"type":        "string",
						"enum":        []any{string(RankByPrice)},
						"description": "the fact to order on — the price the shop is asking today",
					},
					"direction": map[string]any{
						"type":        "string",
						"enum":        []any{string(RankAscending), string(RankDescending)},
						"description": "ascending is lowest first, which is what \"cheapest\" asks for",
					},
				},
				"required":             []any{"by", "direction"},
				"additionalProperties": false,
			},
		},
		// rank is deliberately not in this list, and it is the only optional
		// property in the answer.
		//
		// quantity and trigger are required because an omitted field reads as "the
		// model had no opinion" and neither has an honest absence — see the
		// paragraphs above and Trigger. rank does have one: absent means the
		// sentence ranked nothing, which is the ordinary case and resolves to the
		// catalogue order agent.settle used before issue #262. There is no value
		// that could stand in for it either, since "no preference" is not a
		// direction and not a field, so requiring the key would force a model to
		// invent one of those for almost every sentence it reads.
		//
		// **Untested against a live endpoint, unlike the value union above**, whose
		// comment records two probes. Hard rule 4 forbids a test that would do it
		// and this shape has been reasoned about rather than measured: Gemini's
		// responseSchema takes `required` as a list and permits properties outside
		// it, and a provider whose structured-output mode instead demands that
		// every property be required would answer 400 from its own endpoint. That
		// is a loud failure at the boundary and the shape to keep, on exactly the
		// terms the anyOf comment argues for.
		"required":             []any{"constraints", "quantity", "trigger"},
		"additionalProperties": false,
	})
}
