package constraint

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// maxExactFloat is the largest integer a float64 represents exactly, 2^53, and
// it is the ceiling on any number a constraint can carry.
//
// # Why the ceiling exists and cannot be lifted from here
//
// Constraint.Value is an open type, so every number inside it arrives as a
// float64. That is not something a careful caller can avoid: the generated
// Constraint.UnmarshalJSON validates by re-running a plain json.Unmarshal
// internally, which discards whatever json.Decoder.UseNumber the adapter set
// before handing the value on. Precision above 2^53 is therefore already gone
// by the time this package is reached, no matter who decoded the mandate.
//
// So the guard below refuses such a value rather than comparing it. A loud
// refusal beats a silent change, and this is money.
//
// The ceiling is 2^53 minor units — about ninety trillion in a two-decimal
// currency, which is past any payment this system will authorise. Lifting it
// would mean changing the schema so that value is not an open type, and
// contracts/instrument/amount.json already records what widening the amount
// representation costs.
const maxExactFloat = 1 << 53

// parseOperands reads a node's value into typed operands, checked against the
// field's kind.
//
// Everything here fails at parse time. That is the whole point: by the time an
// expression is evaluated, every comparison it will make is known to be between
// two values of one kind, so evaluation cannot fail for a reason that is really
// a defect in the mandate.
func parseOperands(o operator, f Field, raw any) ([]value, error) {
	switch o.shape {
	case operandOne:
		v, err := parseValue(f, raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s: %w", ErrTypeMismatch, f.Name, o.op, err)
		}
		return []value{v}, nil

	case operandList:
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s %s needs a list", ErrTypeMismatch, f.Name, o.op)
		}
		if len(items) == 0 {
			// An empty list permits nothing, or excludes nothing, depending on
			// the operator — and neither is what somebody meant to sign.
			// Omitting the constraint says "no limit" unambiguously.
			return nil, fmt.Errorf("%w: %s %s needs at least one value", ErrMalformed, f.Name, o.op)
		}
		out := make([]value, 0, len(items))
		for i, item := range items {
			v, err := parseValue(f, item)
			if err != nil {
				return nil, fmt.Errorf("%w: %s %s value %d: %w", ErrTypeMismatch, f.Name, o.op, i, err)
			}
			out = append(out, v)
		}
		return out, nil

	case operandRange:
		bounds, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s %s needs an object with from and to", ErrTypeMismatch, f.Name, o.op)
		}
		from, hasFrom := bounds["from"]
		to, hasTo := bounds["to"]
		if !hasFrom || !hasTo || len(bounds) != 2 {
			return nil, fmt.Errorf("%w: %s %s needs exactly from and to", ErrMalformed, f.Name, o.op)
		}
		low, err := parseValue(f, from)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s from: %w", ErrTypeMismatch, f.Name, o.op, err)
		}
		high, err := parseValue(f, to)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s to: %w", ErrTypeMismatch, f.Name, o.op, err)
		}
		if c, err := compare(low, high); err == nil && c > 0 {
			// An inverted range permits nothing, ever. Two values swapped is a
			// far likelier explanation than an intent to authorise nothing.
			return nil, fmt.Errorf("%w: %s %s has from after to", ErrMalformed, f.Name, o.op)
		}
		return []value{low, high}, nil

	default:
		return nil, fmt.Errorf("%w: %s has no operand shape", ErrMalformed, o.op)
	}
}

// parseValue reads one operand for a field.
//
// It takes the whole Field rather than its Kind because text has two
// comparison rules — folded for labels, exact for identifiers — and an operand
// normalised differently from the subject's value would simply never match.
func parseValue(f Field, raw any) (value, error) {
	if raw == nil {
		return value{}, fmt.Errorf("no value")
	}
	switch f.Kind {
	case KindMoney:
		amount, err := parseMoney(raw)
		if err != nil {
			return value{}, err
		}
		return value{kind: KindMoney, money: amount}, nil

	case KindTime:
		s, ok := raw.(string)
		if !ok {
			return value{}, fmt.Errorf("not an RFC 3339 timestamp")
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return value{}, fmt.Errorf("not an RFC 3339 timestamp: %w", err)
		}
		return value{kind: KindTime, time: t}, nil

	case KindNumber:
		n, err := wholeNumber(raw)
		if err != nil {
			return value{}, err
		}
		return value{kind: KindNumber, number: n}, nil

	case KindText:
		s, ok := raw.(string)
		if !ok {
			return value{}, fmt.Errorf("not text")
		}
		normalised := fold(s)
		if f.exact {
			normalised = strings.TrimSpace(s)
		}
		if normalised == "" {
			return value{}, fmt.Errorf("empty")
		}
		return value{kind: KindText, text: normalised}, nil

	default:
		return value{}, fmt.Errorf("unknown kind %s", f.Kind)
	}
}

// parseMoney reads an Amount from an open value.
//
// It reads the object by hand rather than round-tripping through
// encoding/json. Two reasons, both found by tests rather than by reasoning
// about it. generated.Amount carries a hand-written UnmarshalJSON — the
// schema's own validation — and a custom unmarshaller takes over the whole
// value, so json.Decoder.DisallowUnknownFields never applies to it and an
// amount carrying an extra key would be accepted silently. And the amount has
// to go through wholeNumber, which the generated type's int field cannot do.
func parseMoney(raw any) (generated.Amount, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return generated.Amount{}, fmt.Errorf("not an amount")
	}
	for key := range fields {
		if key != "amount" && key != "currency" {
			// The unknown-operator rule, one level down: a value carrying a key
			// this verifier does not understand may not mean what the reader
			// thinks it does.
			return generated.Amount{}, fmt.Errorf("unknown key %q", key)
		}
	}

	rawAmount, ok := fields["amount"]
	if !ok {
		return generated.Amount{}, fmt.Errorf("no amount")
	}
	minor, err := wholeNumber(rawAmount)
	if err != nil {
		return generated.Amount{}, err
	}
	if minor < 0 {
		return generated.Amount{}, fmt.Errorf("amount %d is negative", minor)
	}

	currency, ok := fields["currency"].(string)
	if !ok {
		return generated.Amount{}, fmt.Errorf("no currency")
	}
	if !validCurrency(currency) {
		return generated.Amount{}, fmt.Errorf("currency %q is not an ISO 4217 code", currency)
	}
	return generated.Amount{Amount: int(minor), Currency: currency}, nil
}

// wholeNumber reads an integer from an open value, refusing anything that may
// already have lost precision.
//
// Under 2^53 a float64 holds an integer exactly, so the common case is lossless.
// At or above it the value has already been changed before this package saw it,
// and refusing loudly beats comparing something quietly wrong. See maxExactFloat
// for why no caller can prevent that.
//
// json.Number is still accepted, because a caller that builds a Constraint in
// Go rather than decoding one keeps its digits and should not be penalised for
// the wire format's limits.
func wholeNumber(raw any) (int64, error) {
	switch n := raw.(type) {
	case json.Number:
		v, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("%s is not a whole number: %w", n, err)
		}
		return v, nil

	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("%v is not a whole number", n)
		}
		if math.Abs(n) >= maxExactFloat {
			return 0, fmt.Errorf(
				"%.0f is at or above 2^53 and cannot have survived decoding intact; "+
					"see the note on maxExactFloat for why this is a ceiling rather than a bug", n)
		}
		return int64(n), nil

	case int:
		return int64(n), nil
	case int64:
		return n, nil

	case string:
		// json.Number accepts a quoted string without complaint — "20000"
		// reads back as 20000 — and the contract says integer. Two wire
		// representations of one claim means the looser arrives from whichever
		// encoder was sloppiest.
		return 0, fmt.Errorf("must be a number, not the string %q", n)

	default:
		return 0, fmt.Errorf("not a number")
	}
}

// validCurrency checks the shape contracts/instrument/amount.json states,
// `^[A-Z]{3}$`. It is not a register lookup; the schema does not do one either.
func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, c := range []byte(code) {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
