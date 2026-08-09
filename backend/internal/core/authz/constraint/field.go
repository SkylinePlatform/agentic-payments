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

	// read pulls the value out of a subject. The second result is false when
	// the purchase does not state this fact at all, which is never the same as
	// stating an empty one.
	read func(Subject) (value, bool)
}

// attributePrefix addresses a fact belonging to one kind of purchase rather
// than to purchases in general — "item.attr.route.origin".
const attributePrefix = "item.attr."

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
	Field{Name: "item.id", Kind: KindText, Noun: "the item", exact: true,
		read: func(s Subject) (value, bool) { return exactText(s.Item.ID) }},
	Field{Name: "item.category", Kind: KindText, Noun: "the item category",
		read: func(s Subject) (value, bool) { return text(s.Item.Category) }},
	Field{Name: "merchant.id", Kind: KindText, Noun: "the merchant", exact: true,
		read: func(s Subject) (value, bool) { return exactText(s.Merchant.ID) }},
	Field{Name: "merchant.category", Kind: KindText, Noun: "the merchant category",
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
	if attr, ok := strings.CutPrefix(name, attributePrefix); ok && attr != "" {
		// Attributes are always text: core does not know what a flight is, so
		// it cannot know that an origin is an airport code.
		return Field{
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
// value, and the operators that may be applied to it.
//
// It carries no Noun and no reader. The sentence a constraint renders as is
// Render's job and the value is pulled out of a Subject only during evaluation,
// so publishing either here would offer a caller a second way to do something
// this package already does once.
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
		out = append(out, FieldSpec{Name: f.Name, Kind: f.Kind, Operators: operatorsFor(f.Kind)})
	}
	slices.SortFunc(out, func(a, b FieldSpec) int { return strings.Compare(a.Name, b.Name) })
	return out
}
