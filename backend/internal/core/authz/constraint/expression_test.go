package constraint_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario's numbers, from docs/business/use-cases.md.
var (
	windowOpens  = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	windowCloses = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	insideWindow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
)

// node builds a constraint from JSON, which is how one actually arrives — off
// the wire, inside a signed mandate. Writing the tests this way means the
// fixtures double as documentation of the format.
func node(t *testing.T, raw string) generated.Constraint {
	t.Helper()
	// A plain Unmarshal, deliberately, because it is what any caller gets.
	// Setting json.Decoder.UseNumber here would look more careful and do
	// nothing: the generated Constraint.UnmarshalJSON re-runs a plain
	// json.Unmarshal internally, so every number arrives as a float64 whatever
	// the caller asked for. That ceiling is what
	// TestAmountsPastTheCeilingAreRefused pins.
	var c generated.Constraint
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("the test's own fixture is not valid JSON: %v\n%s", err, raw)
	}
	return c
}

func flight() constraint.Subject {
	return constraint.Subject{
		Amount:   generated.Amount{Amount: 18900, Currency: "USD"},
		At:       insideWindow,
		Quantity: 1,
		Item: constraint.Item{
			Category:   "flights",
			ID:         "iata:JU324",
			Attributes: map[string]string{"route.origin": "BEG", "route.destination": "PMI"},
		},
		Merchant: constraint.Party{ID: "air-serbia", Category: "airline"},
	}
}

func parse(t *testing.T, raw string) constraint.Expression {
	t.Helper()
	e, err := constraint.Parse(node(t, raw))
	require.NoError(t, err, "Parse")
	return e
}

func evaluate(t *testing.T, raw string, s constraint.Subject) constraint.Result {
	t.Helper()
	return parse(t, raw).Evaluate(s)
}

// ---------------------------------------------------------------------------
// The three use cases that motivated the model. Each is the point of the work.
// ---------------------------------------------------------------------------

// TestBuiltScenario is beats 5 and 6 as arithmetic, against the new model: the
// same constraint set answering differently at $210 and $189, decided by the
// verifier and never by the agent.
func TestBuiltScenario(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"all","of":[
		{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}},
		{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}},
		{"op":"eq","field":"item.attr.route.origin","value":"BEG"},
		{"op":"eq","field":"item.attr.route.destination","value":"PMI"}
	]}`

	for _, tc := range []struct {
		name  string
		price int
		want  bool
	}{
		{"beat 4 — watched at $240", 24000, false},
		{"beat 5 — a candidate at $210, above the cap", 21000, false},
		{"beat 6 — $189, inside what the user approved", 18900, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := flight()
			s.Amount = generated.Amount{Amount: tc.price, Currency: "USD"}
			if got := evaluate(t, mandate, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v — %s", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

// TestBicycleByIdentity is the case the old model could not express: a specific
// thing, not a class of thing. "Buy me this bicycle when it drops below $400."
//
// The waiting is not in the mandate at all — it is the agent's behaviour, the
// same as beat 4. The mandate says only what the agent may do when it acts.
func TestBicycleByIdentity(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"all","of":[
		{"op":"eq","field":"item.id","value":"gtin:05012345678900"},
		{"op":"lte","field":"amount","value":{"amount":40000,"currency":"USD"}}
	]}`

	bicycle := func(id string, price int) constraint.Subject {
		return constraint.Subject{
			Amount:   generated.Amount{Amount: price, Currency: "USD"},
			At:       insideWindow,
			Quantity: 1,
			Item:     constraint.Item{Category: "bicycles", ID: id},
			Merchant: constraint.Party{ID: "velo", Category: "sporting-goods"},
		}
	}

	if got := evaluate(t, mandate, bicycle("gtin:05012345678900", 38000)); !got.Satisfied {
		t.Errorf("the approved bicycle under the cap was refused: %s", got.Reason)
	}
	if got := evaluate(t, mandate, bicycle("gtin:05012345678900", 41000)); got.Satisfied {
		t.Error("a price over the cap was allowed")
	}
	// A different bicycle at a lower price is still a different bicycle.
	if got := evaluate(t, mandate, bicycle("gtin:09999999999999", 10000)); got.Satisfied {
		t.Error("a bicycle the user did not approve was allowed because it was cheap")
	}
}

// TestConcertTicketsWithQuantity is the other case the old model could not
// express: how many. A cap on the total cannot distinguish "one ticket, up to
// $80" from "four at twenty".
func TestConcertTicketsWithQuantity(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"all","of":[
		{"op":"eq","field":"item.id","value":"event:vlado-georgijev-2026-11-14"},
		{"op":"lte","field":"quantity","value":2},
		{"op":"lte","field":"amount","value":{"amount":16000,"currency":"USD"}}
	]}`

	tickets := func(n, total int) constraint.Subject {
		return constraint.Subject{
			Amount:   generated.Amount{Amount: total, Currency: "USD"},
			At:       insideWindow,
			Quantity: n,
			Item:     constraint.Item{Category: "concert-tickets", ID: "event:vlado-georgijev-2026-11-14"},
			Merchant: constraint.Party{ID: "tickets-rs", Category: "ticketing"},
		}
	}

	if got := evaluate(t, mandate, tickets(2, 15000)); !got.Satisfied {
		t.Errorf("two tickets under the cap were refused: %s", got.Reason)
	}
	// The count is the point: four tickets inside the same total is not what
	// "up to two" approved, and a cap on the total alone could not tell.
	if got := evaluate(t, mandate, tickets(4, 15000)); got.Satisfied {
		t.Error("four tickets were allowed by a mandate approving two")
	}
}

// TestLaddersAnywhere is the third case, and the one where the model's edge
// shows. "Find and buy telescopic ladders, cheapest."
//
// The mandate names no merchant, so any merchant's verifier will accept it.
// What the mandate cannot say is "cheapest" — that is not refutable at the
// point of purchase, because no merchant can know what the whole market was
// offering at an instant, and the merchant is the least neutral party to ask.
// The interpreter's job is to turn the objective into the bound below; the
// search is the agent's behaviour and nobody verifies it.
func TestLaddersAnywhere(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"all","of":[
		{"op":"eq","field":"item.category","value":"ladders"},
		{"op":"lte","field":"amount","value":{"amount":15000,"currency":"USD"}}
	]}`

	ladders := func(merchant string, price int) constraint.Subject {
		return constraint.Subject{
			Amount:   generated.Amount{Amount: price, Currency: "USD"},
			At:       insideWindow,
			Quantity: 1,
			Item:     constraint.Item{Category: "ladders", ID: "gtin:04000000000001"},
			Merchant: constraint.Party{ID: merchant, Category: "hardware"},
		}
	}

	for _, merchant := range []string{"obi", "bauhaus", "some-marketplace"} {
		if got := evaluate(t, mandate, ladders(merchant, 12000)); !got.Satisfied {
			t.Errorf("%s was refused by a mandate naming no merchant: %s", merchant, got.Reason)
		}
	}
	// And the bound is what actually protects the user: a merchant selling at
	// $160 is refused even though the agent chose it.
	if got := evaluate(t, mandate, ladders("obi", 16000)); got.Satisfied {
		t.Error("a price over the bound was allowed")
	}
}

// ---------------------------------------------------------------------------
// The tree
// ---------------------------------------------------------------------------

func TestGroups(t *testing.T) {
	t.Parallel()

	// Either a flight to Palma, or any hotel — the composition the old
	// conjunctive list could not express at all.
	const mandate = `{"op":"any","of":[
		{"op":"all","of":[
			{"op":"eq","field":"item.category","value":"flights"},
			{"op":"eq","field":"item.attr.route.destination","value":"PMI"}]},
		{"op":"eq","field":"item.category","value":"hotels"}
	]}`

	if got := evaluate(t, mandate, flight()); !got.Satisfied {
		t.Errorf("the flight branch did not hold: %s", got.Reason)
	}

	hotel := flight()
	hotel.Item = constraint.Item{Category: "hotels", ID: "hotel:portals-nous"}
	if got := evaluate(t, mandate, hotel); !got.Satisfied {
		t.Errorf("the hotel branch did not hold: %s", got.Reason)
	}

	car := flight()
	car.Item = constraint.Item{Category: "car-hire", ID: "car:1"}
	if got := evaluate(t, mandate, car); got.Satisfied {
		t.Error("car hire satisfied a mandate approving flights or hotels")
	}
}

func TestNot(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"not","of":[{"op":"in","field":"merchant.category","value":["casino","crypto-exchange"]}]}`

	if got := evaluate(t, mandate, flight()); !got.Satisfied {
		t.Errorf("an airline was refused by a mandate excluding casinos: %s", got.Reason)
	}

	casino := flight()
	casino.Merchant = constraint.Party{ID: "x", Category: "casino"}
	if got := evaluate(t, mandate, casino); got.Satisfied {
		t.Error("a casino satisfied a mandate excluding casinos")
	}
}

// TestNotTakesExactlyOneChild pins a refusal rather than a behaviour. "Not" over
// several children has two readings — none of them, or not all of them — and
// picking one silently would make a mandate mean something its author did not
// choose.
func TestNotTakesExactlyOneChild(t *testing.T) {
	t.Parallel()

	_, err := constraint.Parse(node(t, `{"op":"not","of":[
		{"op":"eq","field":"item.category","value":"a"},
		{"op":"eq","field":"item.category","value":"b"}]}`))
	assert.ErrorIs(t, err, constraint.ErrMalformed, "err = %v, want ErrMalformed", err)
}

// TestEmptyGroupIsRefused covers the two vacuous readings. `all` of nothing
// would permit every purchase and `any` of nothing would refuse them all;
// neither is something a person meant to sign, and both look more like a
// constraint that lost its children in transit.
func TestEmptyGroupIsRefused(t *testing.T) {
	t.Parallel()

	for _, op := range []string{"all", "any", "not"} {
		if _, err := constraint.Parse(node(t, `{"op":"`+op+`","of":[]}`)); !errors.Is(err, constraint.ErrMalformed) {
			t.Errorf("%s with no children: err = %v, want ErrMalformed", op, err)
		}
	}
}

func TestDepthIsBounded(t *testing.T) {
	t.Parallel()

	// A verifier evaluates this on the request path against input it did not
	// write, so the recursion is bounded by something other than good
	// intentions.
	deep := `{"op":"eq","field":"item.category","value":"flights"}`
	for range constraint.MaxDepth + 2 {
		deep = `{"op":"all","of":[` + deep + `]}`
	}
	if _, err := constraint.Parse(node(t, deep)); !errors.Is(err, constraint.ErrTooDeep) {
		t.Errorf("err = %v, want ErrTooDeep", err)
	}

	shallow := `{"op":"eq","field":"item.category","value":"flights"}`
	for range constraint.MaxDepth - 1 {
		shallow = `{"op":"all","of":[` + shallow + `]}`
	}
	if _, err := constraint.Parse(node(t, shallow)); err != nil {
		t.Errorf("a legal depth was refused: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Type checking at parse time
// ---------------------------------------------------------------------------

// TestTypeMismatchesFailAtParse is the distinction the package is built around.
// A mandate asking whether a route is less than another route is malformed; a
// verifier reporting that as an unsatisfied limit would tell a user their limit
// was exceeded when in fact nobody could read it.
func TestTypeMismatchesFailAtParse(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		want error
	}{
		{"ordering text", `{"op":"lt","field":"item.category","value":"flights"}`, constraint.ErrTypeMismatch},
		{"a word where money goes", `{"op":"lte","field":"amount","value":"cheap"}`, constraint.ErrTypeMismatch},
		{"a number where money goes", `{"op":"lte","field":"amount","value":20000}`, constraint.ErrTypeMismatch},
		{"a date that is not a date", `{"op":"before","field":"at","value":"next summer"}`, constraint.ErrTypeMismatch},
		{"within on a number", `{"op":"within","field":"quantity","value":{"from":1,"to":2}}`, constraint.ErrTypeMismatch},
		{"a list where one value goes", `{"op":"lte","field":"quantity","value":[1,2]}`, constraint.ErrTypeMismatch},
		{"one value where a list goes", `{"op":"in","field":"item.category","value":"flights"}`, constraint.ErrTypeMismatch},
		{"a range missing a bound", `{"op":"between","field":"quantity","value":{"from":1}}`, constraint.ErrMalformed},
		{"an inverted range", `{"op":"between","field":"quantity","value":{"from":9,"to":1}}`, constraint.ErrMalformed},
		{"an empty list", `{"op":"in","field":"item.category","value":[]}`, constraint.ErrMalformed},
		{"a negative amount", `{"op":"lte","field":"amount","value":{"amount":-1,"currency":"USD"}}`, constraint.ErrTypeMismatch},
		{"a currency that is not a code", `{"op":"lte","field":"amount","value":{"amount":1,"currency":"usd"}}`, constraint.ErrTypeMismatch},
		{"an unknown key in an amount", `{"op":"lte","field":"amount","value":{"amount":1,"currency":"USD","vat":true}}`, constraint.ErrTypeMismatch},

		{"an unknown field", `{"op":"eq","field":"colour","value":"red"}`, constraint.ErrUnknownField},
		{"an unknown operator", `{"op":"resembles","field":"item.category","value":"flights"}`, constraint.ErrUnknownOperator},

		{"a leaf with no field", `{"op":"eq","value":"flights"}`, constraint.ErrMalformed},
		{"a leaf with children", `{"op":"eq","field":"item.category","value":"a","of":[{"op":"all","of":[]}]}`, constraint.ErrMalformed},
		{"a group with a field", `{"op":"all","field":"amount","of":[{"op":"eq","field":"item.category","value":"a"}]}`, constraint.ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := constraint.Parse(node(t, tc.raw))
			assert.ErrorIs(t, err, tc.want, "err = %v, want %v", err, tc.want)
		})
	}
}

// TestUnknownIsRejectedNotSkipped is the rule that makes every other guarantee
// mean something. Skipping a constraint the verifier cannot evaluate turns a
// limit the user set into a limit nobody enforces.
func TestUnknownIsRejectedNotSkipped(t *testing.T) {
	t.Parallel()

	cs := []generated.Constraint{
		node(t, `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`),
		node(t, `{"op":"eq","field":"velocity.perDay","value":"3"}`),
	}

	report, err := constraint.Evaluate(cs, flight())
	require.ErrorIs(t, err, constraint.ErrUnknownField, "err = %v, want ErrUnknownField", err)
	// No partial answer: a report saying "satisfied" while one constraint was
	// never evaluated is the silent skip in a different costume.
	if len(report.Results) != 0 {
		t.Errorf("a report was returned alongside the error: %+v", report)
	}
	if got := constraint.CodeOf(err); got != generated.ErrorCodeConstraintTypeUnknown {
		t.Errorf("CodeOf = %q, want %q", got, generated.ErrorCodeConstraintTypeUnknown)
	}
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want generated.ErrorCode
	}{
		{nil, ""},
		{constraint.ErrUnknownField, generated.ErrorCodeConstraintTypeUnknown},
		{constraint.ErrUnknownOperator, generated.ErrorCodeConstraintTypeUnknown},
		// Malformed parameters are a broken mandate, not a refused purchase. A
		// caller told "constraint violated" would report to the user that their
		// limit was exceeded, when in fact it could not be read.
		{constraint.ErrTypeMismatch, generated.ErrorCodeMandateMalformed},
		{constraint.ErrMalformed, generated.ErrorCodeMandateMalformed},
		{constraint.ErrTooDeep, generated.ErrorCodeMandateMalformed},
	} {
		if got := constraint.CodeOf(tc.err); got != tc.want {
			t.Errorf("CodeOf(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Evaluation edges
// ---------------------------------------------------------------------------

// TestUnstatedFactIsNotSatisfied is the failure an agent would find first, by
// simply omitting a field. A fact the purchase does not carry cannot be shown
// to be inside a limit the user approved.
func TestUnstatedFactIsNotSatisfied(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		blot func(*constraint.Subject)
	}{
		{"no category", `{"op":"eq","field":"item.category","value":"flights"}`,
			func(s *constraint.Subject) { s.Item.Category = "" }},
		{"no item id", `{"op":"eq","field":"item.id","value":"x"}`,
			func(s *constraint.Subject) { s.Item.ID = "" }},
		{"no quantity", `{"op":"lte","field":"quantity","value":2}`,
			func(s *constraint.Subject) { s.Quantity = 0 }},
		{"no attribute", `{"op":"eq","field":"item.attr.route.origin","value":"BEG"}`,
			func(s *constraint.Subject) { s.Item.Attributes = nil }},
		{"no merchant", `{"op":"eq","field":"merchant.id","value":"air-serbia"}`,
			func(s *constraint.Subject) { s.Merchant.ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := flight()
			tc.blot(&s)
			got := evaluate(t, tc.raw, s)
			if got.Satisfied {
				t.Error("an unstated fact satisfied a constraint about it")
			}
			if !strings.Contains(got.Reason, "not stated") {
				t.Errorf("the reason does not say the fact was missing: %q", got.Reason)
			}
		})
	}
}

// TestCurrencyMismatchRefuses covers the comparison this package will not make.
// A cap of 20000 USD says nothing about a charge of 18900 EUR, and converting
// would make the verifier's answer depend on a market feed the user never
// consented to.
func TestCurrencyMismatchRefuses(t *testing.T) {
	t.Parallel()

	const mandate = `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`

	s := flight()
	s.Amount = generated.Amount{Amount: 100, Currency: "EUR"}
	got := evaluate(t, mandate, s)
	if got.Satisfied {
		t.Error("an amount in another currency was allowed under a USD cap")
	}
	if !strings.Contains(got.Reason, "convert") {
		t.Errorf("the reason does not explain the refusal: %q", got.Reason)
	}
}

func TestBoundsAreInclusive(t *testing.T) {
	t.Parallel()

	const cap = `{"op":"lte","field":"amount","value":{"amount":20000,"currency":"USD"}}`
	const window = `{"op":"within","field":"at","value":{"from":"2026-06-01T00:00:00Z","to":"2026-08-31T23:59:59Z"}}`

	for _, tc := range []struct {
		name string
		raw  string
		at   time.Time
		amt  int
		want bool
	}{
		{"exactly at the cap", cap, insideWindow, 20000, true},
		{"one minor unit over", cap, insideWindow, 20001, false},
		{"exactly when the window opens", window, windowOpens, 100, true},
		{"exactly when it closes", window, windowCloses, 100, true},
		{"a nanosecond before it opens", window, windowOpens.Add(-time.Nanosecond), 100, false},
		{"a nanosecond after it closes", window, windowCloses.Add(time.Nanosecond), 100, false},
		// The same instant in another zone is the same instant.
		{"the window, in another zone", window, insideWindow.In(time.FixedZone("UTC+7", 7*3600)), 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := flight()
			s.At = tc.at
			s.Amount = generated.Amount{Amount: tc.amt, Currency: "USD"}
			if got := evaluate(t, tc.raw, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v — %s", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

// TestConjunctionAtTheTopLevel pins what survives from the earlier model:
// appending to the top-level list cannot widen authority. Inside an `any` it
// can, which is the price of the expression language and is documented.
func TestConjunctionAtTheTopLevel(t *testing.T) {
	t.Parallel()

	strict := node(t, `{"op":"lte","field":"amount","value":{"amount":5000,"currency":"USD"}}`)
	loose := node(t, `{"op":"lte","field":"amount","value":{"amount":50000,"currency":"USD"}}`)

	for _, tc := range []struct {
		name  string
		order []generated.Constraint
		want  bool
	}{
		{"strict first", []generated.Constraint{strict, loose}, false},
		{"loose first", []generated.Constraint{loose, strict}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := flight()
			s.Amount = generated.Amount{Amount: 20000, Currency: "USD"}
			report, err := constraint.Evaluate(tc.order, s)
			require.NoError(t, err, "Evaluate")
			assert.Equal(t, tc.want, report.Satisfied(), "the stricter did not win")
		})
	}
}

func TestNoConstraintsIsSatisfied(t *testing.T) {
	t.Parallel()

	// A mandate carrying no constraints is one where the user placed no
	// limits, which is different from one whose limits could not be read.
	report, err := constraint.Evaluate(nil, flight())
	require.NoError(t, err, "Evaluate")
	if !report.Satisfied() {
		t.Error("a mandate with no constraints was not satisfied")
	}
}

func TestEveryViolationIsReported(t *testing.T) {
	t.Parallel()

	cs := []generated.Constraint{
		node(t, `{"op":"lte","field":"amount","value":{"amount":100,"currency":"USD"}}`),
		node(t, `{"op":"eq","field":"item.category","value":"hotels"}`),
		node(t, `{"op":"lte","field":"quantity","value":0}`),
	}

	report, err := constraint.Evaluate(cs, flight())
	require.NoError(t, err, "Evaluate")
	if got := len(report.Violations()); got != 3 {
		t.Errorf("%d violations, want 3 — evaluation stopped early", got)
	}
	for _, v := range report.Violations() {
		if v.Reason == "" {
			t.Error("a violation carried no reason")
		}
	}
}

// TestAmountsPastTheCeilingAreRefused pins a loud refusal in place of a silent
// change.
//
// Constraint.Value is an open type and the generated UnmarshalJSON re-runs a
// plain json.Unmarshal internally, so every number in a constraint arrives as a
// float64 whatever the adapter did. Below 2^53 that is exact. At or above it the
// value has already been altered before this package saw it — and this is money,
// where quietly comparing a changed number is the worst available outcome.
//
// The ceiling is about ninety trillion in a two-decimal currency, so nothing a
// real authorisation needs is lost. What matters is that crossing it fails
// rather than lies.
func TestAmountsPastTheCeilingAreRefused(t *testing.T) {
	t.Parallel()

	// 2^53 + 1, the first integer a float64 cannot hold.
	_, err := constraint.Parse(node(t,
		`{"op":"lte","field":"amount","value":{"amount":9007199254740993,"currency":"USD"}}`))
	require.ErrorIs(t, err, constraint.ErrTypeMismatch, "err = %v, want ErrTypeMismatch", err)
	if !strings.Contains(err.Error(), "2^53") {
		t.Errorf("the error does not explain the ceiling: %v", err)
	}

	// Everything below it is exact and accepted.
	if _, err := constraint.Parse(node(t,
		`{"op":"lte","field":"amount","value":{"amount":9007199254740991,"currency":"USD"}}`)); err != nil {
		t.Errorf("an amount below the ceiling was refused: %v", err)
	}
}

// TestMembershipDoesNotDependOnOrder covers a defect the first version had: an
// operand that could not be compared returned at once, so `in [EUR, USD]`
// against a USD purchase failed on the euro while the same two values written
// the other way round matched the dollar and never reached it.
//
// One mandate, two answers, decided by which value the author happened to write
// first. A verifier whose result turns on that is not deterministic in any sense
// worth the name.
func TestMembershipDoesNotDependOnOrder(t *testing.T) {
	t.Parallel()

	s := flight()
	s.Amount = generated.Amount{Amount: 20000, Currency: "USD"}

	const usdFirst = `{"op":"in","field":"amount","value":[
		{"amount":20000,"currency":"USD"},{"amount":100,"currency":"EUR"}]}`
	const eurFirst = `{"op":"in","field":"amount","value":[
		{"amount":100,"currency":"EUR"},{"amount":20000,"currency":"USD"}]}`

	first, second := evaluate(t, usdFirst, s), evaluate(t, eurFirst, s)
	if first.Satisfied != second.Satisfied {
		t.Errorf("the same values in a different order gave %v and %v", first.Satisfied, second.Satisfied)
	}
	if !first.Satisfied {
		t.Errorf("a matching amount was refused: %s", first.Reason)
	}

	// And when nothing matches while something could not be compared, the
	// verifier says so rather than reporting an absence it never established.
	s.Amount = generated.Amount{Amount: 999, Currency: "USD"}
	if got := evaluate(t, eurFirst, s); got.Satisfied {
		t.Error("a non-matching amount was reported as a member")
	}
}

// TestIdentifiersCompareExactly covers the other half of the same review. A
// category is a word two parties are trying to agree on, so folding case stops
// a spelling difference reading as a policy decision. An identifier is a key,
// and folding it decides on the identifier scheme's behalf that "ABC" and "abc"
// name one thing — which most schemes say they do not.
func TestIdentifiersCompareExactly(t *testing.T) {
	t.Parallel()

	s := flight()
	s.Item.ID = "SKU-AbC"

	if got := evaluate(t, `{"op":"eq","field":"item.id","value":"SKU-AbC"}`, s); !got.Satisfied {
		t.Errorf("an identifier did not match itself: %s", got.Reason)
	}
	if got := evaluate(t, `{"op":"eq","field":"item.id","value":"sku-abc"}`, s); got.Satisfied {
		t.Error("two identifiers differing only in case were merged into one")
	}
	// Surrounding space is transport noise, not identity.
	if got := evaluate(t, `{"op":"eq","field":"item.id","value":"  SKU-AbC  "}`, s); !got.Satisfied {
		t.Errorf("surrounding space defeated an identifier match: %s", got.Reason)
	}

	// Labels keep folding, which is what makes an allow-list forgiving.
	s.Item.Category = "Flights"
	if got := evaluate(t, `{"op":"eq","field":"item.category","value":"flights"}`, s); !got.Satisfied {
		t.Errorf("a category did not fold: %s", got.Reason)
	}
}

// TestZeroExpressionDoesNotPanic guards the exported type's zero value. An
// Expression can only be built by Parse, but nothing stops a caller declaring
// one, and a verifier that panics on a request is a verifier that stops
// answering any of them.
func TestZeroExpressionDoesNotPanic(t *testing.T) {
	t.Parallel()

	var zero constraint.Expression
	got := zero.Evaluate(flight())
	if got.Satisfied {
		t.Error("an expression that was never parsed was satisfied")
	}
	if got.Reason == "" {
		t.Error("the refusal carried no reason")
	}
	if s := zero.Render(); s == "" {
		t.Error("an unparsed expression rendered nothing at all")
	}
}

func TestVocabularyIsPublished(t *testing.T) {
	t.Parallel()

	// A verifier that can say what it understands gives a profile or
	// negotiation mechanism something to work with, and costs nothing.
	fields := constraint.FieldNames()
	if len(fields) < 5 {
		t.Errorf("FieldNames() = %v, want the fields every purchase has", fields)
	}
	ops := constraint.OperatorNames()
	for _, want := range []string{"all", "any", "not", "lte", "within", "in", "between"} {
		if !slices.Contains(ops, want) {
			t.Errorf("OperatorNames() is missing %q: %v", want, ops)
		}
	}
}
