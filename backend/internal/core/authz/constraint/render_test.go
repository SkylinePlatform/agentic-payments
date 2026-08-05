package constraint_test

import (
	"strings"
	"testing"
)

// TestRenderSaysWhatTheUserIsSigning is the acceptance test the model is held
// to, not a formatting check.
//
// Beat 3 of the built scenario is the Trusted Surface showing the user the
// interpretation and taking their signature on it — the one place a human
// decision is taken, and the place a misread intent is caught. A constraint
// that cannot be stated plainly is one the user cannot meaningfully approve, so
// a signature collected against it would be a signature on something nobody
// read.
//
// The expected strings below are therefore part of the specification of this
// package rather than an implementation detail. If one of them has to become
// less readable to accommodate a new node type, the node type is the thing that
// is wrong.
func TestRenderSaysWhatTheUserIsSigning(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{
			"the built scenario, whole",
			`{"op":"all","of":[
				{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
				{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
				{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
				{"op":"eq","field":"item.attr.route.destination","value":"PMI"}]}`,
			`the amount is at most 200.00 USD and the time of purchase falls within 1 June 2026 and 31 August 2026 ` +
				`and the item's route origin is "beg" and the item's route destination is "pmi"`,
		},
		{
			"a cap",
			`{"op":"lte","field":"amount","value":{"amount":18900,"currency":"USD"}}`,
			"the amount is at most 189.00 USD",
		},
		{
			// The one place the integer becomes a decimal, and it is the last
			// step before a person reads it.
			"an amount under one unit",
			`{"op":"lte","field":"amount","value":{"amount":7,"currency":"EUR"}}`,
			"the amount is at most 0.07 EUR",
		},
		{
			"a quantity",
			`{"op":"lte","field":"quantity","value":2}`,
			"the quantity is at most 2",
		},
		{
			"a specific thing",
			`{"op":"eq","field":"item.id","value":"gtin:05012345678900"}`,
			`the item is "gtin:05012345678900"`,
		},
		{
			"a list",
			`{"op":"in","field":"item.category","value":["flights","hotels"]}`,
			`the item category is one of "flights", "hotels"`,
		},
		{
			"an exclusion",
			`{"op":"nin","field":"merchant.category","value":["casino"]}`,
			`the merchant category is not one of "casino"`,
		},
		{
			"a choice",
			`{"op":"any","of":[
				{"op":"eq","field":"item.category","value":"flights"},
				{"op":"eq","field":"item.category","value":"hotels"}]}`,
			`either the item category is "flights" or the item category is "hotels"`,
		},
		{
			"a negation",
			`{"op":"not","of":[{"op":"eq","field":"merchant.category","value":"casino"}]}`,
			`it is not the case that the merchant category is "casino"`,
		},
		{
			"a deadline",
			`{"op":"before","field":"at","value":"2026-12-24T00:00:00Z"}`,
			"the time of purchase is before 24 December 2026",
		},
		{
			"a band",
			`{"op":"between","field":"quantity","value":{"from":2,"to":6}}`,
			"the quantity is between 2 and 6",
		},
		{
			"nesting",
			`{"op":"all","of":[
				{"op":"lte","field":"amount","value":{"amount":50000,"currency":"USD"}},
				{"op":"any","of":[
					{"op":"eq","field":"item.category","value":"flights"},
					{"op":"eq","field":"item.category","value":"trains"}]}]}`,
			`the amount is at most 500.00 USD and either the item category is "flights" ` +
				`or the item category is "trains"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := parse(t, tc.raw).Render(); got != tc.want {
				t.Errorf("Render()\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestEveryOperatorCanSayItself is the structural half of the same rule. The
// package panics at init if an operator has no phrase, so this asserts the
// stronger thing: that every operator in the table produces a sentence with no
// gap in it.
func TestEveryOperatorCanSayItself(t *testing.T) {
	t.Parallel()

	// One well-formed constraint per operator, chosen so the operand fits.
	for _, raw := range []string{
		`{"op":"eq","field":"item.category","value":"flights"}`,
		`{"op":"neq","field":"item.category","value":"flights"}`,
		`{"op":"lt","field":"quantity","value":5}`,
		`{"op":"lte","field":"quantity","value":5}`,
		`{"op":"gt","field":"quantity","value":5}`,
		`{"op":"gte","field":"quantity","value":5}`,
		`{"op":"in","field":"item.category","value":["flights"]}`,
		`{"op":"nin","field":"item.category","value":["flights"]}`,
		`{"op":"between","field":"quantity","value":{"from":1,"to":5}}`,
		`{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T00:00:00Z"}}`,
		`{"op":"before","field":"at","value":"2026-06-01T00:00:00Z"}`,
		`{"op":"after","field":"at","value":"2026-06-01T00:00:00Z"}`,
		`{"op":"all","of":[{"op":"eq","field":"item.category","value":"flights"}]}`,
		`{"op":"any","of":[{"op":"eq","field":"item.category","value":"flights"}]}`,
		`{"op":"not","of":[{"op":"eq","field":"item.category","value":"flights"}]}`,
	} {
		sentence := parse(t, raw).Render()
		switch {
		case sentence == "":
			t.Errorf("%s rendered nothing", raw)
		case strings.Contains(sentence, "  "):
			t.Errorf("%s rendered with a gap where a phrase should be: %q", raw, sentence)
		case strings.HasSuffix(sentence, " "):
			t.Errorf("%s rendered with a trailing gap: %q", raw, sentence)
		}
	}
}

// TestReasonsQuoteTheConstraint checks that a rejection explains itself in the
// same words the user approved, rather than in field paths and operator codes.
// A receipt nobody can read is a receipt that settles nothing.
func TestReasonsQuoteTheConstraint(t *testing.T) {
	t.Parallel()

	s := flight()
	s.Amount.Amount = 99999

	got := evaluate(t, `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`, s)
	if got.Satisfied {
		t.Fatal("an amount far over the cap was satisfied")
	}
	for _, want := range []string{"the amount", "999.99 USD", "at most 200.00 USD"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("the reason does not contain %q: %s", want, got.Reason)
		}
	}
	// And no field paths or operator codes leak into it.
	for _, leak := range []string{"lte", "field", "KindMoney"} {
		if strings.Contains(got.Reason, leak) {
			t.Errorf("the reason leaks %q at the reader: %s", leak, got.Reason)
		}
	}
}
