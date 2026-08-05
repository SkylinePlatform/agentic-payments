package constraint

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Standard returns a registry holding the vocabulary this repository defines.
//
// It cannot fail: the definitions are compiled in and their types are distinct,
// which NewRegistry checks and TestStandardRegistryBuilds proves. A caller
// building its own vocabulary uses NewRegistry directly.
func Standard() *Registry {
	r, err := NewRegistry(
		priceMaxDefinition(),
		temporalWindowDefinition(),
		itemCategoryDefinition(),
		itemRouteDefinition(),
		merchantCategoryDefinition(),
	)
	if err != nil {
		panic("constraint: the standard vocabulary is malformed: " + err.Error())
	}
	return r
}

// ---------------------------------------------------------------------------
// price.max
// ---------------------------------------------------------------------------

// priceMax caps the amount, in one currency.
type priceMax struct {
	amount   int64
	currency string
}

func priceMaxDefinition() Definition {
	return Definition{
		Type: PriceMax,
		Parse: func(params map[string]any) (Evaluator, error) {
			var p struct {
				Amount   *minorUnits `json:"amount"`
				Currency string      `json:"currency"`
			}
			if err := decodeParams(params, &p); err != nil {
				return nil, err
			}
			switch {
			case p.Amount == nil:
				return nil, errors.New("amount is required")
			case *p.Amount < 0:
				// A negative cap authorises nothing and is more likely a sign
				// error than an intent to forbid all spending, which is what
				// omitting the constraint would say clearly.
				return nil, fmt.Errorf("amount %d is negative", *p.Amount)
			case !validCurrency(p.Currency):
				return nil, fmt.Errorf("currency %q is not an ISO 4217 code", p.Currency)
			}
			return priceMax{amount: int64(*p.Amount), currency: p.Currency}, nil
		},
	}
}

func (p priceMax) Type() Type { return PriceMax }

// Evaluate compares the amount, and refuses outright across currencies.
//
// A cap of 20000 USD says nothing about a charge of 18900 EUR, and this package
// holds no rates and should hold none — converting would make the verifier's
// answer depend on a market feed, which is not something a user consented to
// when they set a limit. Refusing is the conservative answer and the only
// honest one.
//
// The bound is inclusive: "max 20000" permits exactly 20000. That is what the
// word means to the person who set it, and the alternative makes the limit a
// figure that can never be spent.
func (p priceMax) Evaluate(s Subject) Result {
	switch {
	case !strings.EqualFold(s.Amount.Currency, p.currency):
		return Result{Type: PriceMax, Reason: fmt.Sprintf(
			"limit is in %s and the amount is in %s; this verifier does not convert currencies",
			p.currency, s.Amount.Currency)}
	case int64(s.Amount.Amount) > p.amount:
		return Result{Type: PriceMax, Reason: fmt.Sprintf(
			"amount %d %s exceeds the limit of %d %s",
			s.Amount.Amount, s.Amount.Currency, p.amount, p.currency)}
	default:
		return Result{Type: PriceMax, Satisfied: true}
	}
}

// ---------------------------------------------------------------------------
// temporal.window
// ---------------------------------------------------------------------------

// temporalWindow bounds when the authority may be exercised.
type temporalWindow struct {
	notBefore time.Time
	notAfter  time.Time
}

func temporalWindowDefinition() Definition {
	return Definition{
		Type: TemporalWindow,
		Parse: func(params map[string]any) (Evaluator, error) {
			var p struct {
				NotBefore string `json:"not_before"`
				NotAfter  string `json:"not_after"`
			}
			if err := decodeParams(params, &p); err != nil {
				return nil, err
			}
			if p.NotBefore == "" || p.NotAfter == "" {
				return nil, errors.New("not_before and not_after are both required")
			}
			notBefore, err := time.Parse(time.RFC3339, p.NotBefore)
			if err != nil {
				return nil, fmt.Errorf("not_before: %w", err)
			}
			notAfter, err := time.Parse(time.RFC3339, p.NotAfter)
			if err != nil {
				return nil, fmt.Errorf("not_after: %w", err)
			}
			if notAfter.Before(notBefore) {
				// An empty window permits nothing, ever. More likely two
				// values swapped than an intent to authorise nothing.
				return nil, fmt.Errorf("not_after %s is before not_before %s",
					p.NotAfter, p.NotBefore)
			}
			return temporalWindow{notBefore: notBefore, notAfter: notAfter}, nil
		},
	}
}

func (t temporalWindow) Type() Type { return TemporalWindow }

// Evaluate checks the instant against the window, inclusive at both ends.
//
// Inclusive because the window is how a person describes a holiday — "June the
// first to August the thirty-first" includes both days — and because a purchase
// landing exactly on a boundary should not turn on which side of a microsecond
// the clock fell.
//
// The instant comes from the subject, which the verifier fills from its
// injected clock. This package never reads a clock: a constraint that consulted
// the wall time directly would be untestable at its boundaries, which are the
// only interesting part of it.
func (t temporalWindow) Evaluate(s Subject) Result {
	switch {
	case s.At.Before(t.notBefore):
		return Result{Type: TemporalWindow, Reason: fmt.Sprintf(
			"%s is before the window opens at %s",
			s.At.Format(time.RFC3339), t.notBefore.Format(time.RFC3339))}
	case s.At.After(t.notAfter):
		return Result{Type: TemporalWindow, Reason: fmt.Sprintf(
			"%s is after the window closed at %s",
			s.At.Format(time.RFC3339), t.notAfter.Format(time.RFC3339))}
	default:
		return Result{Type: TemporalWindow, Satisfied: true}
	}
}

// ---------------------------------------------------------------------------
// item.category and merchant.category
// ---------------------------------------------------------------------------

// allowList permits a value only if it is one of a stated set. Both category
// constraints are this, differing only in which field they read.
type allowList struct {
	constraintType Type
	allowed        []string
	subject        func(Subject) string
	noun           string
}

func (a allowList) Type() Type { return a.constraintType }

// Evaluate refuses anything outside the list, and refuses an absent value.
//
// An empty subject value is a violation rather than a pass. A purchase whose
// category nobody stated cannot be shown to be inside a list the user approved,
// and treating "unknown" as "allowed" is how an allow-list becomes decorative.
func (a allowList) Evaluate(s Subject) Result {
	got := normalise(a.subject(s))
	switch {
	case got == "":
		return Result{Type: a.constraintType, Reason: fmt.Sprintf(
			"no %s was stated, so it cannot be shown to be one of %s",
			a.noun, strings.Join(a.allowed, ", "))}
	case !slices.Contains(a.allowed, got):
		return Result{Type: a.constraintType, Reason: fmt.Sprintf(
			"%s %q is not one of %s", a.noun, got, strings.Join(a.allowed, ", "))}
	default:
		return Result{Type: a.constraintType, Satisfied: true}
	}
}

func allowListDefinition(t Type, noun string, read func(Subject) string) Definition {
	return Definition{
		Type: t,
		Parse: func(params map[string]any) (Evaluator, error) {
			var p struct {
				Allowed []string `json:"allowed"`
			}
			if err := decodeParams(params, &p); err != nil {
				return nil, err
			}
			allowed := normaliseSet(p.Allowed)
			if len(allowed) == 0 {
				// An empty list permits nothing. Omitting the constraint says
				// that unambiguously; an empty list looks like an oversight,
				// and refusing every purchase is too severe an outcome to
				// reach by accident.
				return nil, errors.New("allowed must list at least one value")
			}
			return allowList{constraintType: t, allowed: allowed, subject: read, noun: noun}, nil
		},
	}
}

func itemCategoryDefinition() Definition {
	return allowListDefinition(ItemCategory, "item category",
		func(s Subject) string { return s.ItemCategory })
}

func merchantCategoryDefinition() Definition {
	return allowListDefinition(MerchantCategory, "merchant category",
		func(s Subject) string { return s.MerchantCategory })
}

// ---------------------------------------------------------------------------
// item.route
// ---------------------------------------------------------------------------

// itemRoute pins a journey to its endpoints.
type itemRoute struct {
	origin      string
	destination string
}

func itemRouteDefinition() Definition {
	return Definition{
		Type: ItemRoute,
		Parse: func(params map[string]any) (Evaluator, error) {
			var p struct {
				Origin      string `json:"origin"`
				Destination string `json:"destination"`
			}
			if err := decodeParams(params, &p); err != nil {
				return nil, err
			}
			origin, destination := normalise(p.Origin), normalise(p.Destination)
			if origin == "" || destination == "" {
				return nil, errors.New("origin and destination are both required")
			}
			if origin == destination {
				return nil, fmt.Errorf("origin and destination are both %q", origin)
			}
			return itemRoute{origin: origin, destination: destination}, nil
		},
	}
}

func (i itemRoute) Type() Type { return ItemRoute }

// Evaluate requires both endpoints to match, in the stated direction.
//
// Direction matters and is not symmetric: a user who approved Belgrade to Palma
// did not thereby approve Palma to Belgrade, which is a different journey on a
// different date at a different price. A constraint that matched either way
// would be quietly widening what was consented to.
func (i itemRoute) Evaluate(s Subject) Result {
	got := Route{Origin: normalise(s.Route.Origin), Destination: normalise(s.Route.Destination)}
	want := Route{Origin: i.origin, Destination: i.destination}

	if got.Origin == "" || got.Destination == "" {
		return Result{Type: ItemRoute, Reason: fmt.Sprintf(
			"no route was stated, so it cannot be shown to be %s", want)}
	}
	if got != want {
		return Result{Type: ItemRoute, Reason: fmt.Sprintf(
			"route %s is not %s", got, want)}
	}
	return Result{Type: ItemRoute, Satisfied: true}
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

// compile-time proof that every evaluator satisfies the interface.
var (
	_ Evaluator = priceMax{}
	_ Evaluator = temporalWindow{}
	_ Evaluator = allowList{}
	_ Evaluator = itemRoute{}
)
