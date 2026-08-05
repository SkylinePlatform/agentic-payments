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
	Field{Name: "item.id", Kind: KindText, Noun: "the item",
		read: func(s Subject) (value, bool) { return text(s.Item.ID) }},
	Field{Name: "item.category", Kind: KindText, Noun: "the item category",
		read: func(s Subject) (value, bool) { return text(s.Item.Category) }},
	Field{Name: "merchant.id", Kind: KindText, Noun: "the merchant",
		read: func(s Subject) (value, bool) { return text(s.Merchant.ID) }},
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

// text builds a text value, reporting absence.
func text(s string) (value, bool) {
	folded := fold(s)
	return value{kind: KindText, text: folded}, folded != ""
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
