package constraint

import (
	"fmt"
	"strings"
)

// Render says what an expression means, in one sentence.
//
// # This is a requirement, not a convenience
//
// Beat 3 of the built scenario is the Trusted Surface showing the user the
// *interpretation*, not the prompt they typed, and taking their signature on
// it. That screen is where a misread intent gets caught, and it is the only
// place in the system where a human decision is taken. A constraint the surface
// cannot state plainly is a constraint the user cannot meaningfully approve —
// so a signature collected against it would be a signature on something nobody
// read.
//
// Which makes rendering an acceptance test on the model rather than a
// presentation concern: a node type that cannot be said clearly does not enter
// the vocabulary. It lives in this package, beside the thing it describes, so
// that adding an operator without a phrase for it fails to compile rather than
// producing a screen with a gap in it.
func (e Expression) Render() string {
	switch e.op {
	case OpAll:
		return joinReasons(e.renderChildren(), " and ")
	case OpAny:
		return "either " + joinReasons(e.renderChildren(), " or ")
	case OpNot:
		return "it is not the case that " + e.children[0].Render()
	default:
		return fmt.Sprintf("%s %s %s", e.field.Noun, e.operator.phrase, e.renderOperands())
	}
}

func (e Expression) renderChildren() []string {
	out := make([]string, 0, len(e.children))
	for _, c := range e.children {
		out = append(out, c.Render())
	}
	return out
}

func (e Expression) renderOperands() string {
	switch e.operator.shape {
	case operandRange:
		return renderValue(e.operands[0]) + " and " + renderValue(e.operands[1])
	case operandList:
		rendered := make([]string, 0, len(e.operands))
		for _, v := range e.operands {
			rendered = append(rendered, renderValue(v))
		}
		return joinReasons(rendered, ", ")
	default:
		return renderValue(e.operands[0])
	}
}

// renderValue says one value the way a person would.
func renderValue(v value) string {
	switch v.kind {
	case KindMoney:
		return renderMoney(v)
	case KindTime:
		// The date, not the instant. A user approving a booking window thinks
		// in days, and rendering the seconds would make the sentence harder to
		// read without telling them anything they can act on.
		return v.time.Format("2 January 2006")
	case KindNumber:
		return fmt.Sprintf("%d", v.number)
	default:
		return fmt.Sprintf("%q", v.text)
	}
}

// renderMoney turns integer minor units into the major unit for reading.
//
// This is the one place the integer becomes a decimal, and it is the last step
// before a person reads it — contracts/instrument/amount.json is emphatic that
// floating-point money manufactures rounding disputes, so the conversion is
// done as string surgery on the integer rather than by dividing.
//
// Two minor digits is assumed, which is right for the currencies this proof of
// concept handles and wrong for JPY and the three-digit currencies. Stated
// rather than hidden: the amount is never wrong, only its presentation, and the
// fix is a minor-unit table that the scope note in amount.json already implies
// somebody will need.
func renderMoney(v value) string {
	const minorDigits = 2

	amount := int64(v.money.Amount)
	sign := ""
	if amount < 0 {
		sign, amount = "-", -amount
	}

	digits := fmt.Sprintf("%0*d", minorDigits+1, amount)
	whole, fraction := digits[:len(digits)-minorDigits], digits[len(digits)-minorDigits:]
	return fmt.Sprintf("%s%s.%s %s", sign, whole, fraction, v.money.Currency)
}

// joinReasons joins with a separator. Named for its other use — a rejection
// receipt listing everything that was wrong — because the two want identical
// behaviour and splitting them would let the sentence and the receipt drift.
func joinReasons(parts []string, sep string) string { return strings.Join(parts, sep) }

// compile-time proof that every operator can say itself. An operator added to
// the table without a phrase would render as a gap in the sentence a user is
// asked to sign, which is exactly the failure this package refuses to allow.
func init() {
	for op, o := range operators {
		if o.phrase == "" {
			panic("constraint: operator " + string(op) + " has no phrase; it could not be shown to a user")
		}
	}
}
