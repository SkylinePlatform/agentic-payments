package constraint_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz/constraint"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// The built scenario's numbers, from docs/business/use-cases.md: a cap of
// USD 20000 in minor units, a window of 2026-06-01 to 2026-08-31, and the route
// BEG→PMI.
var (
	windowOpens  = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	windowCloses = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)
	insideWindow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
)

func usd(minor int) generated.Amount {
	return generated.Amount{Amount: minor, Currency: "USD"}
}

// subject returns a purchase that satisfies every constraint in scenario(), so
// a test can change exactly the one thing it is about.
func subject() constraint.Subject {
	return constraint.Subject{
		Amount:           usd(18900),
		At:               insideWindow,
		Route:            constraint.Route{Origin: "BEG", Destination: "PMI"},
		ItemCategory:     "flights",
		MerchantCategory: "airline",
	}
}

func c(t constraint.Type, params map[string]any) generated.Constraint {
	return generated.Constraint{Type: string(t), Params: params}
}

// scenario is the constraint set the user signs in the built scenario.
func scenario() []generated.Constraint {
	return []generated.Constraint{
		c(constraint.PriceMax, map[string]any{"amount": 20000, "currency": "USD"}),
		c(constraint.TemporalWindow, map[string]any{
			"not_before": windowOpens.Format(time.RFC3339),
			"not_after":  windowCloses.Format(time.RFC3339),
		}),
		c(constraint.ItemRoute, map[string]any{"origin": "BEG", "destination": "PMI"}),
	}
}

func evaluate(t *testing.T, cs []generated.Constraint, s constraint.Subject) constraint.Report {
	t.Helper()
	report, err := constraint.Standard().Evaluate(cs, s)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return report
}

// evaluateOne runs a single constraint and returns its result.
func evaluateOne(t *testing.T, con generated.Constraint, s constraint.Subject) constraint.Result {
	t.Helper()
	report := evaluate(t, []generated.Constraint{con}, s)
	if len(report.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(report.Results))
	}
	return report.Results[0]
}

func TestStandardRegistryBuilds(t *testing.T) {
	t.Parallel()

	want := []constraint.Type{
		constraint.ItemCategory,
		constraint.ItemRoute,
		constraint.MerchantCategory,
		constraint.PriceMax,
		constraint.TemporalWindow,
	}
	got := constraint.Standard().Types()
	if len(got) != len(want) {
		t.Fatalf("Types() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Types()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestTheBuiltScenario is beats 5 and 6 as arithmetic. The whole autonomous
// flow turns on the same constraint set answering differently at $210 and
// $189, and it is the verifier that decides — this package — never the agent.
func TestTheBuiltScenario(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		price int
		want  bool
	}{
		{"beat 4 — watched at $240, far above the cap", 24000, false},
		{"beat 5 — a candidate at $210, still above it", 21000, false},
		{"beat 6 — $189, inside what the user approved", 18900, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := subject()
			s.Amount = usd(tc.price)

			report := evaluate(t, scenario(), s)
			if got := report.Satisfied(); got != tc.want {
				t.Errorf("Satisfied() = %v, want %v (violations: %+v)",
					got, tc.want, report.Violations())
			}
		})
	}
}

// TestUnknownTypeIsRejected is the rule that makes every other guarantee here
// mean something. Skipping a constraint the verifier cannot evaluate turns a
// limit the user set into a limit nobody enforces, and lets the purchase
// proceed while misrepresenting what was approved.
func TestUnknownTypeIsRejected(t *testing.T) {
	t.Parallel()

	cs := append(scenario(), c("velocity.perDay", map[string]any{"count": 3}))

	report, err := constraint.Standard().Evaluate(cs, subject())
	if !errors.Is(err, constraint.ErrUnknownType) {
		t.Fatalf("err = %v, want ErrUnknownType", err)
	}
	// No partial answer. A report saying "satisfied" while one constraint was
	// never evaluated is the silent skip in a different costume.
	if len(report.Results) != 0 {
		t.Errorf("a report was returned alongside the error: %+v", report)
	}
	if got := constraint.CodeOf(err); got != generated.ErrorCodeConstraintTypeUnknown {
		t.Errorf("CodeOf = %q, want %q", got, generated.ErrorCodeConstraintTypeUnknown)
	}
}

// TestUnevaluableConstraintDoesNotPass covers the same rule from the other
// side: an unknown type must not be reported as a violation either, because a
// receipt saying "constraint violated" claims the limits were checked.
func TestUnevaluableConstraintDoesNotPass(t *testing.T) {
	t.Parallel()

	_, err := constraint.Standard().Evaluate(
		[]generated.Constraint{c("velocity.perDay", nil)}, subject())
	if constraint.CodeOf(err) == generated.ErrorCodeConstraintViolated {
		t.Error("an unevaluable constraint was reported as violated, which claims it was checked")
	}
}

func TestPriceMax(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		amount  generated.Amount
		want    bool
		wantErr bool
		params  map[string]any
	}{
		{name: "below the cap", amount: usd(18900), want: true},
		// "max 20000" permits exactly 20000. The alternative makes the limit a
		// figure that can never be spent.
		{name: "exactly at the cap", amount: usd(20000), want: true},
		{name: "one minor unit over", amount: usd(20001), want: false},
		{name: "zero", amount: usd(0), want: true},
		// No rates here, and none wanted: converting would make the verifier's
		// answer depend on a market feed the user never consented to.
		{name: "a different currency, under the number", amount: generated.Amount{Amount: 100, Currency: "EUR"}, want: false},
		{name: "a different currency, over the number", amount: generated.Amount{Amount: 99999, Currency: "EUR"}, want: false},

		{name: "no amount", params: map[string]any{"currency": "USD"}, wantErr: true},
		{name: "negative cap", params: map[string]any{"amount": -1, "currency": "USD"}, wantErr: true},
		{name: "no currency", params: map[string]any{"amount": 20000}, wantErr: true},
		{name: "lower-case currency", params: map[string]any{"amount": 20000, "currency": "usd"}, wantErr: true},
		{name: "fractional amount", params: map[string]any{"amount": 200.5, "currency": "USD"}, wantErr: true},
		{name: "amount as a string", params: map[string]any{"amount": "20000", "currency": "USD"}, wantErr: true},
		{name: "unknown parameter", params: map[string]any{"amount": 20000, "currency": "USD", "vat": true}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := tc.params
			if params == nil {
				params = map[string]any{"amount": 20000, "currency": "USD"}
			}
			con := c(constraint.PriceMax, params)

			if tc.wantErr {
				if _, err := constraint.Standard().Parse(con); !errors.Is(err, constraint.ErrInvalidParams) {
					t.Fatalf("Parse = %v, want ErrInvalidParams", err)
				}
				return
			}

			s := subject()
			s.Amount = tc.amount
			if got := evaluateOne(t, con, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v (%s)", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

func TestTemporalWindow(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		at      time.Time
		want    bool
		wantErr bool
		params  map[string]any
	}{
		{name: "inside", at: insideWindow, want: true},
		// Inclusive at both ends: a purchase landing exactly on a boundary
		// should not turn on which side of a microsecond the clock fell.
		{name: "exactly when it opens", at: windowOpens, want: true},
		{name: "exactly when it closes", at: windowCloses, want: true},
		{name: "a nanosecond before it opens", at: windowOpens.Add(-time.Nanosecond), want: false},
		{name: "a nanosecond after it closes", at: windowCloses.Add(time.Nanosecond), want: false},
		{name: "a season early", at: windowOpens.AddDate(0, -3, 0), want: false},
		{name: "the following year", at: windowOpens.AddDate(1, 0, 0), want: false},
		// The same instant in another zone is the same instant.
		{name: "expressed in another zone", at: insideWindow.In(time.FixedZone("UTC+7", 7*3600)), want: true},

		{name: "no not_before", params: map[string]any{"not_after": "2026-08-31T00:00:00Z"}, wantErr: true},
		{name: "no not_after", params: map[string]any{"not_before": "2026-06-01T00:00:00Z"}, wantErr: true},
		{name: "not RFC 3339", params: map[string]any{"not_before": "1 June 2026", "not_after": "2026-08-31T00:00:00Z"}, wantErr: true},
		{name: "window closes before it opens", params: map[string]any{
			"not_before": "2026-08-31T00:00:00Z", "not_after": "2026-06-01T00:00:00Z",
		}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := tc.params
			if params == nil {
				params = map[string]any{
					"not_before": windowOpens.Format(time.RFC3339),
					"not_after":  windowCloses.Format(time.RFC3339),
				}
			}
			con := c(constraint.TemporalWindow, params)

			if tc.wantErr {
				if _, err := constraint.Standard().Parse(con); !errors.Is(err, constraint.ErrInvalidParams) {
					t.Fatalf("Parse = %v, want ErrInvalidParams", err)
				}
				return
			}

			s := subject()
			s.At = tc.at
			if got := evaluateOne(t, con, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v (%s)", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

func TestItemRoute(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		route   constraint.Route
		want    bool
		wantErr bool
		params  map[string]any
	}{
		{name: "the approved route", route: constraint.Route{Origin: "BEG", Destination: "PMI"}, want: true},
		{name: "case does not matter", route: constraint.Route{Origin: "beg", Destination: "pmi"}, want: true},
		// A user who approved Belgrade to Palma did not approve the return: a
		// different journey, on a different date, at a different price.
		{name: "reversed", route: constraint.Route{Origin: "PMI", Destination: "BEG"}, want: false},
		{name: "different destination", route: constraint.Route{Origin: "BEG", Destination: "BCN"}, want: false},
		{name: "different origin", route: constraint.Route{Origin: "ZRH", Destination: "PMI"}, want: false},
		// Unstated is not permitted. It cannot be shown to be the approved
		// route, and treating unknown as allowed is how a limit stops limiting.
		{name: "no route stated", route: constraint.Route{}, want: false},
		{name: "half a route", route: constraint.Route{Origin: "BEG"}, want: false},

		{name: "no origin", params: map[string]any{"destination": "PMI"}, wantErr: true},
		{name: "no destination", params: map[string]any{"origin": "BEG"}, wantErr: true},
		{name: "origin equals destination", params: map[string]any{"origin": "BEG", "destination": "BEG"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := tc.params
			if params == nil {
				params = map[string]any{"origin": "BEG", "destination": "PMI"}
			}
			con := c(constraint.ItemRoute, params)

			if tc.wantErr {
				if _, err := constraint.Standard().Parse(con); !errors.Is(err, constraint.ErrInvalidParams) {
					t.Fatalf("Parse = %v, want ErrInvalidParams", err)
				}
				return
			}

			s := subject()
			s.Route = tc.route
			if got := evaluateOne(t, con, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v (%s)", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

func TestCategoryAllowLists(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name             string
		constraintType   constraint.Type
		value            string
		want             bool
		wantErr          bool
		params           map[string]any
		setItemCategory  bool
		setMerchantCateg bool
	}{
		{name: "item: in the list", constraintType: constraint.ItemCategory, value: "flights", want: true, setItemCategory: true},
		{name: "item: case and space folded", constraintType: constraint.ItemCategory, value: "  Flights ", want: true, setItemCategory: true},
		{name: "item: not in the list", constraintType: constraint.ItemCategory, value: "jewellery", want: false, setItemCategory: true},
		{name: "item: unstated", constraintType: constraint.ItemCategory, value: "", want: false, setItemCategory: true},

		{name: "merchant: in the list", constraintType: constraint.MerchantCategory, value: "airline", want: true, setMerchantCateg: true},
		{name: "merchant: not in the list", constraintType: constraint.MerchantCategory, value: "casino", want: false, setMerchantCateg: true},
		{name: "merchant: unstated", constraintType: constraint.MerchantCategory, value: "", want: false, setMerchantCateg: true},

		// An empty list permits nothing at all. Omitting the constraint says
		// that unambiguously; an empty list looks like an oversight, and
		// refusing every purchase is too severe to reach by accident.
		{name: "empty list", constraintType: constraint.ItemCategory, params: map[string]any{"allowed": []any{}}, wantErr: true},
		{name: "list of blanks", constraintType: constraint.ItemCategory, params: map[string]any{"allowed": []any{"", "  "}}, wantErr: true},
		{name: "no allowed key", constraintType: constraint.ItemCategory, params: map[string]any{}, wantErr: true},
		{name: "allowed is not a list", constraintType: constraint.ItemCategory, params: map[string]any{"allowed": "flights"}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			params := tc.params
			if params == nil {
				switch tc.constraintType {
				case constraint.ItemCategory:
					params = map[string]any{"allowed": []any{"flights", "hotels"}}
				default:
					params = map[string]any{"allowed": []any{"airline", "travel agency"}}
				}
			}
			con := c(tc.constraintType, params)

			if tc.wantErr {
				if _, err := constraint.Standard().Parse(con); !errors.Is(err, constraint.ErrInvalidParams) {
					t.Fatalf("Parse = %v, want ErrInvalidParams", err)
				}
				return
			}

			s := subject()
			if tc.setItemCategory {
				s.ItemCategory = tc.value
			}
			if tc.setMerchantCateg {
				s.MerchantCategory = tc.value
			}
			if got := evaluateOne(t, con, s); got.Satisfied != tc.want {
				t.Errorf("Satisfied = %v, want %v (%s)", got.Satisfied, tc.want, got.Reason)
			}
		})
	}
}

// TestEveryViolationIsReported covers the choice to evaluate all constraints
// rather than stopping at the first failure: a rejection receipt should name
// everything that was wrong, not send the caller round the loop once per
// violated limit.
func TestEveryViolationIsReported(t *testing.T) {
	t.Parallel()

	s := subject()
	s.Amount = usd(99999)                                         // over the cap
	s.At = windowOpens.AddDate(-1, 0, 0)                          // before the window
	s.Route = constraint.Route{Origin: "ZRH", Destination: "AMS"} // wrong route

	report := evaluate(t, scenario(), s)
	if report.Satisfied() {
		t.Fatal("a subject violating every constraint was satisfied")
	}
	if got := len(report.Violations()); got != 3 {
		t.Errorf("%d violations reported, want 3 — evaluation stopped early", got)
	}
	for _, v := range report.Violations() {
		if v.Reason == "" {
			t.Errorf("%s was violated with no reason given", v.Type)
		}
	}
}

// TestRepeatedTypesConjoin pins the reading of a list carrying one type twice.
//
// It is worth a test rather than a comment because the two plausible
// alternatives are both worse, and both would pass every other test here.
// "Later overrides earlier" would let a constraint appended to a signed list
// loosen what the user approved; "any one matches" would let an attacker widen
// authority by adding a permissive twin. Conjunction is the only reading where
// adding a constraint cannot increase what the agent may do.
func TestRepeatedTypesConjoin(t *testing.T) {
	t.Parallel()

	strict := c(constraint.PriceMax, map[string]any{"amount": 5000, "currency": "USD"})
	loose := c(constraint.PriceMax, map[string]any{"amount": 50000, "currency": "USD"})

	for _, tc := range []struct {
		name   string
		order  []generated.Constraint
		amount int
		want   bool
	}{
		{"under both", []generated.Constraint{strict, loose}, 4000, true},
		// Between the two: the stricter has to win, in either order, or a
		// tampered mandate could loosen itself by appending.
		{"between them, strict first", []generated.Constraint{strict, loose}, 20000, false},
		{"between them, loose first", []generated.Constraint{loose, strict}, 20000, false},
		{"over both", []generated.Constraint{strict, loose}, 60000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := subject()
			s.Amount = usd(tc.amount)
			if got := evaluate(t, tc.order, s).Satisfied(); got != tc.want {
				t.Errorf("Satisfied() = %v, want %v", got, tc.want)
			}
		})
	}

	// Two contradictory routes are satisfiable by nothing, and that is the
	// right answer: the user cannot have approved a journey that is two
	// different journeys.
	contradictory := []generated.Constraint{
		c(constraint.ItemRoute, map[string]any{"origin": "BEG", "destination": "PMI"}),
		c(constraint.ItemRoute, map[string]any{"origin": "ZRH", "destination": "AMS"}),
	}
	if evaluate(t, contradictory, subject()).Satisfied() {
		t.Error("a subject satisfied two contradictory routes")
	}
}

// TestNoConstraintsIsSatisfied is a loophole worth pinning deliberately: a
// mandate carrying no constraints is one where the user placed no limits, which
// is different from one whose limits could not be read.
func TestNoConstraintsIsSatisfied(t *testing.T) {
	t.Parallel()

	report := evaluate(t, nil, subject())
	if !report.Satisfied() {
		t.Error("a mandate with no constraints was not satisfied")
	}
	if len(report.Violations()) != 0 {
		t.Error("violations reported for a mandate with none")
	}
}

func TestRegistryRejectsNonsense(t *testing.T) {
	t.Parallel()

	def := func(t constraint.Type) constraint.Definition {
		return constraint.Definition{
			Type:  t,
			Parse: func(map[string]any) (constraint.Evaluator, error) { return nil, nil },
		}
	}

	if _, err := constraint.NewRegistry(def(""), def("a")); err == nil {
		t.Error("a definition with no type was accepted")
	}
	if _, err := constraint.NewRegistry(constraint.Definition{Type: "a"}); err == nil {
		t.Error("a definition with no parser was accepted; parsing it would panic")
	}
	// Two definitions for one type would make which evaluator runs depend on
	// map ordering, which is the least debuggable way for a verifier to be
	// wrong.
	if _, err := constraint.NewRegistry(def("a"), def("a")); !errors.Is(err, constraint.ErrDuplicateType) {
		t.Errorf("err = %v, want ErrDuplicateType", err)
	}
}

// TestEmptyRegistryRejectsEverything is the degenerate case, and it must reject
// rather than wave things through: a verifier that understands no constraint
// types can evaluate nothing, and has to say so.
func TestEmptyRegistryRejectsEverything(t *testing.T) {
	t.Parallel()

	empty, err := constraint.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := empty.Evaluate(scenario(), subject()); !errors.Is(err, constraint.ErrUnknownType) {
		t.Errorf("err = %v, want ErrUnknownType", err)
	}
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	if got := constraint.CodeOf(nil); got != "" {
		t.Errorf("CodeOf(nil) = %q, want empty", got)
	}
	if got := constraint.CodeOf(constraint.ErrUnknownType); got != generated.ErrorCodeConstraintTypeUnknown {
		t.Errorf("CodeOf(ErrUnknownType) = %q", got)
	}
	// Malformed parameters are a broken mandate, not a refused purchase. A
	// caller told "constraint violated" would report to the user that their
	// limit was exceeded, when in fact it could not be read.
	if got := constraint.CodeOf(constraint.ErrInvalidParams); got != generated.ErrorCodeMandateMalformed {
		t.Errorf("CodeOf(ErrInvalidParams) = %q, want %q", got, generated.ErrorCodeMandateMalformed)
	}
}

// TestLargeAmountsSurviveDecoding guards the params decoder. A map that has
// been through encoding/json holds float64 for every number, and an amount past
// 2^53 would come back changed — silently, and as money.
func TestLargeAmountsSurviveDecoding(t *testing.T) {
	t.Parallel()

	const huge = 9007199254740993 // 2^53 + 1, the first integer a float64 cannot hold

	con := c(constraint.PriceMax, map[string]any{
		"amount":   json.Number("9007199254740993"),
		"currency": "USD",
	})
	s := subject()
	s.Amount = generated.Amount{Amount: huge, Currency: "USD"}

	if got := evaluateOne(t, con, s); !got.Satisfied {
		t.Errorf("an amount exactly at the cap was refused: %s", got.Reason)
	}

	s.Amount = generated.Amount{Amount: huge + 1, Currency: "USD"}
	if got := evaluateOne(t, con, s); got.Satisfied {
		t.Error("an amount one minor unit over the cap was allowed; the decoder lost precision")
	}
}
