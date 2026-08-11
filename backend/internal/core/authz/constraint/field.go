package constraint

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Kind is the type of a field's value, and it is what decides which operators
// may be applied to it.
//
// It exists so that `amount lte "flights"` fails when the mandate is parsed
// rather than when a purchase is judged. That distinction is the one this
// package is built around: a malformed constraint is a broken mandate, an
// unsatisfied one is a refused purchase, and a verifier that reported the first
// as the second would tell a user their limit was exceeded when in fact nobody
// could read it.
type Kind string

const (
	// KindMoney is an Amount: an integer in minor units and a currency.
	KindMoney Kind = "money"

	// KindTime is an RFC 3339 instant.
	KindTime Kind = "time"

	// KindNumber is a whole number — a count, not a measurement.
	KindNumber Kind = "number"

	// KindText is a label compared as text, folded for case and surrounding
	// space.
	KindText Kind = "text"
)

// value is one comparable value, of exactly one Kind.
//
// A tagged union rather than `any`, so that every comparison in this package is
// a comparison between two things known to be the same kind, checked once at
// parse time instead of at every evaluation.
type value struct {
	kind   Kind
	money  generated.Amount
	time   time.Time
	number int64
	text   string
}

// Field is one fact about a purchase that a constraint can compare.
type Field struct {
	// Name is how a constraint addresses it: "amount", "item.category".
	Name string

	// Kind is the type of its value, and therefore which operators apply.
	Kind Kind

	// Noun is how the renderer says it in a sentence — "the amount", "the
	// item category". Held here rather than derived from Name so that the
	// sentence reads like English rather than like a field path.
	Noun string

	// exact stops a text value being case-folded before comparison.
	//
	// Set on identifiers and not on labels, because the two are different
	// things. A category is a word two parties are trying to agree on, so
	// "Flights" and "flights" are the same word and folding them stops a
	// spelling difference reading as a policy decision. An identifier is a
	// key: whether "ABC" and "abc" name the same thing is the identifier
	// scheme's business, and folding decides it on the scheme's behalf. Most
	// schemes say no — a base64 or case-sensitive SKU has genuinely distinct
	// values differing only in case — so folding silently merges two things
	// the user did not approve as one.
	exact bool

	// selective marks a field as describing *what to go looking for*, rather
	// than a term the purchase has to meet at the point of sale.
	//
	// internal/agent's discovery half needs exactly this bit before it builds a
	// merchant search: a query naming the item or the merchant is answered by a
	// catalogue search, while a query naming the price, the time or the
	// quantity is not — the watch loop exists to wait for exactly those to be
	// satisfied, and a search carrying the user's price bound back to the
	// merchant would return nothing at all while the price is still too high.
	//
	// Nothing to do with selective *disclosure*, which is SD-JWT's, is decided
	// per mandate rather than per field, and lives in
	// internal/adapters/ap2/disclose.go. The two words collide and the concepts
	// do not touch.
	//
	// Never set in a Field literal. term and selector are what set it, and
	// buildFields takes only what they return, so the classification is a thing
	// the compiler makes a new field state rather than a thing a reviewer has to
	// notice was left out — see registration. Selective is how it is read, by
	// name and for both halves of the vocabulary; nothing outside this package
	// holds a Field.
	selective bool

	// read pulls the value out of a subject. The second result is false when
	// the purchase does not state this fact at all, which is never the same as
	// stating an empty one.
	read func(Subject) (value, bool)
}

// attributePrefix addresses a fact belonging to one kind of purchase rather
// than to purchases in general — "item.attr.route.origin".
//
// **It used to be exported, and #132's second step is what took that back.** The
// open half of the vocabulary is the half no list can answer for: Vocabulary and
// FieldNames both omit it on purpose, because it is a rule rather than a table.
// So a caller classifying a route had nothing to hold but this string, and the
// only thing that carried one into a merchant search was internal/agent's own
// "item." prefix matching it — a coincidence between two literals in two
// packages that no compiler relates, which #169 published this constant to let a
// test assert. Selective answers for the family directly now, through
// lookupField, so the caller that made the export worth having has a function to
// call instead of a prefix to reproduce, and the coincidence has stopped being
// load-bearing.
//
// The case it was load-bearing for was never hypothetical: the built scenario's
// flight is discovered by item.attr.route.origin and item.attr.route.destination
// and by nothing else, so a rename that moved the family out from under "item."
// used to leave that search matching every flight this merchant sells. After
// this it is a rename inside one package.
const attributePrefix = "item.attr."

// registration is one entry of the closed registry, together with the
// classification a new field is not allowed to skip.
//
// The type exists so that term and selector are the only way in. buildFields
// takes registrations rather than Fields, so a field added as a bare Field
// literal — the natural thing to write, and the thing that leaves selective at
// its zero value — does not compile. That matters because the zero value is a
// real answer rather than an absent one: false means *this is a term of the
// purchase*, so a forgotten column is not a gap anybody notices, it is a field
// quietly withheld from every catalogue search.
//
// Issue #132's first step held that with a test in internal/agent, which could
// only work while a second copy of the fact existed to disagree with. Deleting
// the copy is what this step is for, so the guard had to become something else,
// and the compiler is the strongest thing available.
//
// **Strongest, not total, and the gap has a backstop.** Go has no file-scoped
// visibility, so anything in this package could still write
// registration{field: Field{…}} and set nothing — which is true of every
// in-package invariant in the language. What narrows it to almost nothing is
// TestSelectiveAnswersForBothHalvesOfTheVocabulary: it demands a row per
// registered name saying which of the two the field is, so a bypass meant as a
// selector fails there rather than passing quietly. The residue is a bypass
// whose author also writes "term" in that table, and that is a field correctly
// described as a term.
type registration struct{ field Field }

// term registers a fact that states a term the purchase has to meet — a bound
// the verifier checks at the moment of sale, and the thing a watch waits for.
func term(f Field) registration {
	f.selective = false
	return registration{field: f}
}

// selector registers a fact that says what to go looking for — something a
// merchant's catalogue can answer before any purchase is on the table.
func selector(f Field) registration {
	f.selective = true
	return registration{field: f}
}

// fields is the closed registry of facts every purchase has.
//
// Adding one is a single entry here, after which every operator that fits its
// kind works on it immediately. That is the whole point of the field-by-
// operator matrix: generality without a growing list of named constraint types,
// each of which would need its own parser, evaluator and renderer.
//
// One entry, and the wrapper is part of it rather than a second place: term and
// selector are how the entry states which of the two kinds of fact it is, and
// there is no way to write the line without saying.
var fields = buildFields(
	term(Field{Name: "amount", Kind: KindMoney, Noun: "the amount",
		read: func(s Subject) (value, bool) {
			return value{kind: KindMoney, money: s.Amount}, s.Amount.Currency != ""
		}}),
	term(Field{Name: "at", Kind: KindTime, Noun: "the time of purchase",
		read: func(s Subject) (value, bool) {
			return value{kind: KindTime, time: s.At}, !s.At.IsZero()
		}}),
	term(Field{Name: "quantity", Kind: KindNumber, Noun: "the quantity",
		read: func(s Subject) (value, bool) {
			return value{kind: KindNumber, number: int64(s.Quantity)}, s.Quantity > 0
		}}),
	selector(Field{Name: "item.id", Kind: KindText, Noun: "the item", exact: true,
		read: func(s Subject) (value, bool) { return exactText(s.Item.ID) }}),
	selector(Field{Name: "item.category", Kind: KindText, Noun: "the item category",
		read: func(s Subject) (value, bool) { return text(s.Item.Category) }}),
	selector(Field{Name: "merchant.id", Kind: KindText, Noun: "the merchant", exact: true,
		read: func(s Subject) (value, bool) { return exactText(s.Merchant.ID) }}),
	selector(Field{Name: "merchant.category", Kind: KindText, Noun: "the merchant category",
		read: func(s Subject) (value, bool) { return text(s.Merchant.Category) }}),
)

func buildFields(in ...registration) map[string]Field {
	out := make(map[string]Field, len(in))
	for _, r := range in {
		out[r.field.Name] = r.field
	}
	return out
}

// text builds a folded text value, reporting absence.
func text(s string) (value, bool) {
	folded := fold(s)
	return value{kind: KindText, text: folded}, folded != ""
}

// exactText builds a text value compared as written, for identifiers.
// Surrounding space still goes: that is transport noise, not identity.
func exactText(s string) (value, bool) {
	trimmed := strings.TrimSpace(s)
	return value{kind: KindText, text: trimmed}, trimmed != ""
}

// fold is how text is compared: lower case, surrounding space removed.
//
// A decision rather than tidiness. An allow-list written "Flights" by the
// interpreter and sent "flights" by the merchant would otherwise refuse a
// purchase the user approved, and the failure would look like a policy
// decision rather than a spelling one. It is the ASCII fold, which suits the
// identifier-shaped labels these carry and does not pretend to handle natural
// language.
func fold(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// lookupField resolves a field name, including the item-attribute form.
func lookupField(name string) (Field, error) {
	if f, ok := fields[name]; ok {
		return f, nil
	}
	if attr, ok := strings.CutPrefix(name, attributePrefix); ok && attr != "" {
		// Attributes are always text: core does not know what a flight is, so
		// it cannot know that an origin is an airport code.
		//
		// Selective, and deliberately so: Catalogue.Subject fills Item.Attributes
		// from the same offer it fills Item.ID and Item.Category from, so a
		// search naming item.attr.route.origin is answerable exactly as one
		// naming item.id is. It is never enumerated by FieldNames or Vocabulary
		// — this field is minted per name rather than held in the closed
		// registry — so a caller that had only a listing to walk could not
		// classify one at all. Selective asks by name and comes through here,
		// which is what makes the open half answerable by the code that mints it
		// rather than by a prefix reproduced somewhere else.
		//
		// Classified through selector and then unwrapped, rather than by setting
		// the column here. This field is minted per call and registered nowhere,
		// so it never reaches buildFields — but writing selective: true in a
		// literal is the one habit the registration type exists to make
		// unavailable, and a second spelling of it in the file that bans the
		// first is how the ban stops being read as one.
		return selector(Field{
			Name: name,
			Kind: KindText,
			Noun: "the item's " + strings.ReplaceAll(attr, ".", " "),
			read: func(s Subject) (value, bool) {
				raw, ok := s.attribute(attr)
				if !ok {
					return value{kind: KindText}, false
				}
				return text(raw)
			},
		}).field, nil
	}
	return Field{}, fmt.Errorf("%w: field %q", ErrUnknownField, name)
}

// FieldNames lists the fields this verifier knows, sorted.
//
// It is exported for the same reason Registry.Types was in the earlier design:
// a verifier that can say what it understands gives a profile or negotiation
// mechanism something to work with, and costs nothing to publish. It does not
// include the item-attribute form, which is open by construction.
func FieldNames() []string {
	out := make([]string, 0, len(fields))
	for name := range fields {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// FieldSpec is one entry of the closed registry as a caller outside this
// package can see it: how a constraint addresses the fact, the type of its
// value, and the operators that may be applied to it.
//
// It carries no Noun and no reader. The sentence a constraint renders as is
// Render's job and the value is pulled out of a Subject only during evaluation,
// so publishing either here would offer a caller a second way to do something
// this package already does once.
//
// **It carried Selective between #132's first and second steps, and that is the
// same rule catching up with it.** The bit was put here because a caller walking
// the vocabulary was the only reader it had. Selective answers by name now, and
// for the open half of the vocabulary as well, which no walk can reach — so
// leaving the column would publish the question twice, with the copy on this
// struct being the one that cannot answer it in full.
type FieldSpec struct {
	// Name is how a constraint addresses it: "amount", "item.category".
	Name string

	// Kind is the type of its value.
	Kind Kind

	// Operators are the leaf operators valid for that kind, sorted. The three
	// group operators are absent, because they combine nodes rather than
	// compare a field and no field accepts one — OperatorNames is where the
	// whole set including those three is published.
	Operators []string
}

// Selective reports whether a constraint on this field says *what to go looking
// for*, rather than stating a term the purchase has to meet.
//
// It is the whole of the fact, and issue #132 is why that sentence is worth
// writing. internal/agent's discovery half needs the bit before it can build a
// merchant search — a query naming the item or the merchant is answered by a
// catalogue, while one naming the price is not, because the watch loop exists to
// wait for exactly that and a search carrying the user's price bound returns
// nothing at all while the price is still too high. It cannot import this
// package to ask: a constraint is evaluated by the verifier and never by the
// party that assembled the purchase, and TestTheAgentCannotReachAConstraintEvaluator
// holds that against the import graph. So it kept two string prefixes saying the
// same thing, and a test held the two in step.
//
// **Publishing the answer is what let the copy go.** internal/agent/interpret
// already imports this package, for the reason AGENTS.md's hard rule 4 gives —
// Validate runs the verifier's own parser rather than keeping a second list of
// field names, because a copy drifts in the direction that accepts what the
// verifier cannot read — and internal/agent already imports interpret. So the
// answer reaches the caller over an edge that was already there, through the one
// package whose job is exactly this: being the agent's window onto the
// vocabulary without being a copy of it.
//
// **What crosses that edge is Narrowing, not this**, and issue #203 is why the
// paragraph above describes the route rather than the traffic on it. A constraint
// is not always a leaf: a group carries op and of and never a field, so the
// leaf-level answer had nothing to say about one and internal/agent dropped every
// group from its search in silence. Narrowing is the same question asked of a
// node, and it is what interpret forwards now — built out of this for a leaf and,
// through whollySelective, for the whole of a not. This stays exported because it
// is the primitive that answer is assembled from and the thing the vocabulary's
// own tests state field by field; nothing outside this package calls it at run
// time, and a caller that wants to know what to search on should be asking about
// the node it holds rather than about a name inside it.
//
// # Both halves, and an unknown name
//
// It answers for the closed registry and for item.attr.<name> alike, because it
// resolves through lookupField rather than reading the table. That is the half
// no listing can reach: attributes are minted per name, so a caller with only
// Vocabulary to walk had to classify a route by noticing the family's prefix
// begins with "item." — a coincidence between two literals that no compiler
// relates.
//
// A name this verifier does not know is not selective. It is a constraint that
// will be refused as constraint_type_unknown at the moment of purchase, and
// answering true would put it in a merchant's catalogue query, where it can only
// be refused as malformed or quietly match nothing.
//
// Nothing here concerns selective *disclosure*, which is SD-JWT's and is decided
// per mandate rather than per field — see internal/adapters/ap2/disclose.go.
func Selective(field string) bool {
	f, err := lookupField(field)
	return err == nil && f.selective
}

// Vocabulary lists the closed part of the constraint vocabulary: every field
// this verifier knows, with its kind and the operators that fit it.
//
// FieldNames answers half of that and OperatorNames the other half, and a
// caller that has to put the two together cannot: which operators a field
// accepts is decided by its kind, and neither function publishes a kind. The
// caller this was added for is the model-backed IntentInterpreter in
// internal/agent/interpret, which describes the vocabulary to a language model
// and must describe the one the verifier will actually apply. Deriving the
// pairing outside this package means a second copy of the field-by-operator
// matrix, and a copy drifts in the one direction that costs something: naming a
// comparison the verifier goes on to refuse.
//
// The open half is deliberately absent, on the grounds FieldNames gives:
// item.attr.<name> admits any name and is text by construction, so it is a rule
// rather than a list.
func Vocabulary() []FieldSpec {
	out := make([]FieldSpec, 0, len(fields))
	for _, f := range fields {
		out = append(out, FieldSpec{
			Name:      f.Name,
			Kind:      f.Kind,
			Operators: operatorsFor(f.Kind),
		})
	}
	slices.SortFunc(out, func(a, b FieldSpec) int { return strings.Compare(a.Name, b.Name) })
	return out
}
