package constraint

import (
	"fmt"
	"slices"
	"strings"
)

// Op is what a node does.
type Op string

// Group operators combine the nodes beneath them.
const (
	// OpAll holds when every child holds. An empty `of` is a mandate that
	// says nothing, and is refused at parse time rather than silently
	// permitting everything.
	OpAll Op = "all"

	// OpAny holds when at least one child holds.
	OpAny Op = "any"

	// OpNot holds when its single child does not.
	OpNot Op = "not"
)

// Leaf operators compare a field against a value.
const (
	OpEq      Op = "eq"
	OpNeq     Op = "neq"
	OpLt      Op = "lt"
	OpLte     Op = "lte"
	OpGt      Op = "gt"
	OpGte     Op = "gte"
	OpIn      Op = "in"
	OpNin     Op = "nin"
	OpBetween Op = "between"
	OpWithin  Op = "within"
	OpBefore  Op = "before"
	OpAfter   Op = "after"
)

// operator describes one leaf operator: which kinds it applies to, what shape
// its value takes, and how it says itself in a sentence.
type operator struct {
	op Op

	// kinds are the field kinds this operator can be applied to. A comparison
	// on text is refused because "is Belgrade less than Palma" has no answer
	// anybody meant to ask.
	kinds []Kind

	// shape is how the operand is read: one value, a list, or a pair.
	shape operandShape

	// phrase renders the comparison — "is at most", "is one of".
	phrase string
}

type operandShape int

const (
	// operandOne is a single value of the field's kind.
	operandOne operandShape = iota

	// operandList is an array of values of the field's kind.
	operandList

	// operandRange is an object carrying from and to.
	operandRange
)

var comparable = []Kind{KindMoney, KindTime, KindNumber}

// operators is the closed table. A field of kind K accepts exactly the
// operators listing K.
var operators = buildOperators(
	operator{op: OpEq, kinds: []Kind{KindMoney, KindTime, KindNumber, KindText}, shape: operandOne, phrase: "is"},
	operator{op: OpNeq, kinds: []Kind{KindMoney, KindTime, KindNumber, KindText}, shape: operandOne, phrase: "is not"},

	operator{op: OpLt, kinds: comparable, shape: operandOne, phrase: "is under"},
	operator{op: OpLte, kinds: comparable, shape: operandOne, phrase: "is at most"},
	operator{op: OpGt, kinds: comparable, shape: operandOne, phrase: "is over"},
	operator{op: OpGte, kinds: comparable, shape: operandOne, phrase: "is at least"},

	operator{op: OpIn, kinds: []Kind{KindMoney, KindTime, KindNumber, KindText}, shape: operandList, phrase: "is one of"},
	operator{op: OpNin, kinds: []Kind{KindMoney, KindTime, KindNumber, KindText}, shape: operandList, phrase: "is not one of"},

	operator{op: OpBetween, kinds: comparable, shape: operandRange, phrase: "is between"},

	// within, before and after are the same three comparisons as between, lt
	// and gt, named for time because that is how a person says them. They are
	// restricted to time so that the vocabulary has one obvious way to say
	// each thing rather than two that a reader has to tell apart.
	operator{op: OpWithin, kinds: []Kind{KindTime}, shape: operandRange, phrase: "falls within"},
	operator{op: OpBefore, kinds: []Kind{KindTime}, shape: operandOne, phrase: "is before"},
	operator{op: OpAfter, kinds: []Kind{KindTime}, shape: operandOne, phrase: "is after"},
)

func buildOperators(in ...operator) map[Op]operator {
	out := make(map[Op]operator, len(in))
	for _, o := range in {
		out[o.op] = o
	}
	return out
}

// isGroup reports whether op combines children rather than comparing a field.
func isGroup(op Op) bool { return op == OpAll || op == OpAny || op == OpNot }

// lookupOperator resolves a leaf operator and checks it against a field's kind.
func lookupOperator(op Op, f Field) (operator, error) {
	o, ok := operators[op]
	if !ok {
		return operator{}, fmt.Errorf("%w: operator %q", ErrUnknownOperator, op)
	}
	if !slices.Contains(o.kinds, f.Kind) {
		// Caught here, not at evaluation. A mandate asking whether a route is
		// less than another route is malformed, and reporting it as an
		// unsatisfied limit would be a lie about what the user approved.
		return operator{}, fmt.Errorf("%w: %s cannot be applied to %s, which is %s",
			ErrTypeMismatch, op, f.Name, f.Kind)
	}
	return o, nil
}

// operatorsFor lists the leaf operators valid for a kind, sorted.
//
// It reads the same table lookupOperator checks against, which is the whole
// point: what Vocabulary publishes and what a mandate is judged by cannot
// disagree, because there is one table.
func operatorsFor(k Kind) []string {
	out := make([]string, 0, len(operators))
	for op, o := range operators {
		if slices.Contains(o.kinds, k) {
			out = append(out, string(op))
		}
	}
	slices.Sort(out)
	return out
}

// OperatorNames lists the operators this verifier implements, sorted. Exported
// for the same reason FieldNames is.
func OperatorNames() []string {
	out := make([]string, 0, len(operators)+3)
	for op := range operators {
		out = append(out, string(op))
	}
	out = append(out, string(OpAll), string(OpAny), string(OpNot))
	slices.Sort(out)
	return out
}

// compare orders two values of the same kind. It returns -1, 0 or 1, and an
// error when the two cannot be ordered at all.
//
// The one case that cannot: money in different currencies. A cap of 20000 USD
// says nothing about a charge of 18900 EUR, and this package holds no rates and
// should hold none — converting would make the verifier's answer depend on a
// market feed the user never consented to when they set a limit.
func compare(a, b value) (int, error) {
	if a.kind != b.kind {
		return 0, fmt.Errorf("%w: cannot compare %s with %s", ErrTypeMismatch, a.kind, b.kind)
	}
	switch a.kind {
	case KindMoney:
		if !strings.EqualFold(a.money.Currency, b.money.Currency) {
			return 0, fmt.Errorf("%w: %s and %s; this verifier does not convert currencies",
				ErrCurrencyMismatch, a.money.Currency, b.money.Currency)
		}
		return cmpInt(int64(a.money.Amount), int64(b.money.Amount)), nil
	case KindTime:
		return a.time.Compare(b.time), nil
	case KindNumber:
		return cmpInt(a.number, b.number), nil
	case KindText:
		return strings.Compare(a.text, b.text), nil
	default:
		return 0, fmt.Errorf("%w: %s", ErrTypeMismatch, a.kind)
	}
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
