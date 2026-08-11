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
	// internal/agent may not import this package — a constraint is evaluated
	// by the verifier, never by the party that assembled the purchase, and
	// TestTheAgentCannotReachAConstraintEvaluator holds that against the import
	// graph — so it cannot read this column here either. It reads the bit off
	// the FieldSpec values Vocabulary returns instead, and
	// TestTheAgentsPrefixesAgreeWithFieldSelectivity in internal/agent holds the
	// two string prefixes authorise.go still carries against this column, in
	// both directions. See issue #132.
	//
	// Unexported for the reason exact is, and the reason is not consistency: a
	// Field is the registry's own entry and nothing outside this package ever
	// holds one, so exporting the column would publish it twice — here, where
	// it cannot be read usefully, and on FieldSpec, where it can. Vocabulary is
	// in this package and reads it either way.
	selective bool

	// read pulls the value out of a subject. The second result is false when
	// the purchase does not state this fact at all, which is never the same as
	// stating an empty one.
	read func(Subject) (value, bool)
}

// AttributePrefix addresses a fact belonging to one kind of purchase rather
// than to purchases in general — "item.attr.route.origin".
//
// Exported because the open half of the vocabulary is the half no list can
// answer for. Vocabulary and FieldNames both omit it on purpose — it is a rule
// rather than a table — so a caller reasoning about the family has nothing to
// hold but this string, and until it was published the only way to name the
// family outside this package was to write the literal again.
//
// The caller that made it worth publishing is a test. Every name under this
// prefix is selective, and none of them is in Vocabulary, so what carries a
// route into a merchant search is internal/agent's itemFieldPrefix matching
// "item." — which works only because this constant happens to begin with it, a
// coincidence between two literals in two packages that no compiler relates.
// TestTheAgentsPrefixesAgreeWithFieldSelectivity names this constant so the
// coincidence is checked rather than argued, and the case is not hypothetical:
// the built scenario's flight is discovered by item.attr.route.origin and
// item.attr.route.destination and by nothing else, so a rename that moved the
// family out from under "item." would leave that search matching every flight
// this merchant sells.
const AttributePrefix = "item.attr."

// fields is the closed registry of facts every purchase has.
//
// Adding one is a single entry here, after which every operator that fits its
// kind works on it immediately. That is the whole point of the field-by-
// operator matrix: generality without a growing list of named constraint types,
// each of which would need its own parser, evaluator and renderer.
var fields = buildFields(
	Field{Name: "amount", Kind: KindMoney, Noun: "the amount",
		read: func(s Subject) (value, bool) {
			return value{kind: KindMoney, money: s.Amount}, s.Amount.Currency != ""
		}},
	Field{Name: "at", Kind: KindTime, Noun: "the time of purchase",
		read: func(s Subject) (value, bool) {
			return value{kind: KindTime, time: s.At}, !s.At.IsZero()
		}},
	Field{Name: "quantity", Kind: KindNumber, Noun: "the quantity",
		read: func(s Subject) (value, bool) {
			return value{kind: KindNumber, number: int64(s.Quantity)}, s.Quantity > 0
		}},
	Field{Name: "item.id", Kind: KindText, Noun: "the item", exact: true, selective: true,
		read: func(s Subject) (value, bool) { return exactText(s.Item.ID) }},
	Field{Name: "item.category", Kind: KindText, Noun: "the item category", selective: true,
		read: func(s Subject) (value, bool) { return text(s.Item.Category) }},
	Field{Name: "merchant.id", Kind: KindText, Noun: "the merchant", exact: true, selective: true,
		read: func(s Subject) (value, bool) { return exactText(s.Merchant.ID) }},
	Field{Name: "merchant.category", Kind: KindText, Noun: "the merchant category", selective: true,
		read: func(s Subject) (value, bool) { return text(s.Merchant.Category) }},
)

func buildFields(in ...Field) map[string]Field {
	out := make(map[string]Field, len(in))
	for _, f := range in {
		out[f.Name] = f
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
	if attr, ok := strings.CutPrefix(name, AttributePrefix); ok && attr != "" {
		// Attributes are always text: core does not know what a flight is, so
		// it cannot know that an origin is an airport code.
		//
		// Selective, and deliberately so: Catalogue.Subject fills Item.Attributes
		// from the same offer it fills Item.ID and Item.Category from, so a
		// search naming item.attr.route.origin is answerable exactly as one
		// naming item.id is. It is never enumerated by FieldNames or Vocabulary
		// — this field is minted per name rather than held in the closed
		// registry — so no caller outside this package ever reads the bit off
		// it. What carries the family into a search is internal/agent's
		// itemFieldPrefix matching "item.", not this column; AttributePrefix is
		// exported so that a test can hold those two literals together rather
		// than a comment claiming they agree.
		return Field{
			Name:      name,
			Kind:      KindText,
			Noun:      "the item's " + strings.ReplaceAll(attr, ".", " "),
			selective: true,
			read: func(s Subject) (value, bool) {
				raw, ok := s.attribute(attr)
				if !ok {
					return value{kind: KindText}, false
				}
				return text(raw)
			},
		}, nil
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
// value, the operators that may be applied to it, and whether it is selective.
//
// It carries no Noun and no reader. The sentence a constraint renders as is
// Render's job and the value is pulled out of a Subject only during evaluation,
// so publishing either here would offer a caller a second way to do something
// this package already does once. Selective is not in that category — it
// names neither a rendering nor an evaluation, only which of two purposes a
// field serves — so it is published rather than held back with them.
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

	// Selective mirrors Field.selective: true when this field describes what
	// to go looking for rather than a term the purchase has to meet. See that
	// field's comment for why internal/agent needs it and cannot read it any
	// other way.
	Selective bool
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
			Selective: f.selective,
		})
	}
	slices.SortFunc(out, func(a, b FieldSpec) int { return strings.Compare(a.Name, b.Name) })
	return out
}
