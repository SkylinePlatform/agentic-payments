package constraint

import (
	"errors"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// Type identifies a constraint type. It is a defined string rather than an
// enumeration for the reason the canonical model gives: an unknown type must be
// representable so that it can be rejected explicitly and named in a receipt.
type Type string

// The vocabulary. Adding one means a Definition and a place in Standard.
const (
	// PriceMax caps what may be spent.
	PriceMax Type = "price.max"

	// TemporalWindow bounds when the authority may be used.
	TemporalWindow Type = "temporal.window"

	// ItemCategory limits what may be bought.
	ItemCategory Type = "item.category"

	// ItemRoute pins a journey to its endpoints.
	ItemRoute Type = "item.route"

	// MerchantCategory limits who may be bought from.
	MerchantCategory Type = "merchant.category"
)

// Subject is the transaction being authorised, as the evaluators need to see
// it.
//
// # Why a struct and not a map
//
// It is deliberately closed. The vocabulary is ours, so a constraint type that
// needs a fact this struct does not carry is a change to what the domain knows
// about a purchase — and that should be a visible edit here rather than a new
// string key appearing in a map somewhere. The cost is that adding a constraint
// type can mean adding a field; the benefit is that the compiler finds every
// evaluator when a fact changes shape.
//
// # Why it is not the AP2 checkout
//
// The adapter fills this in from whatever the wire format carried. Evaluators
// see a protocol-neutral description of the purchase, which is what lets the
// vocabulary stay put when a second protocol arrives — and what stops core
// becoming AP2-shaped, which no depguard rule would catch.
type Subject struct {
	// Amount is what will be charged, in minor units.
	Amount generated.Amount

	// At is when the authority is being exercised, from the injected clock.
	At time.Time

	// Route is the journey being bought, if the purchase is a journey.
	Route Route

	// ItemCategory is what is being bought, in whatever vocabulary the
	// merchant and the user share.
	ItemCategory string

	// MerchantCategory is who it is being bought from — an MCC, or a
	// scheme the two sides agree on.
	MerchantCategory string
}

// Route is a journey between two points, identified the way the parties
// identify them — IATA codes in the built scenario.
type Route struct {
	Origin      string
	Destination string
}

// String renders the route the way the documentation writes it.
func (r Route) String() string { return r.Origin + "→" + r.Destination }

// Result is what one constraint decided.
type Result struct {
	// Type is the constraint that produced this.
	Type Type

	// Satisfied is the answer.
	Satisfied bool

	// Reason says why not, in terms a person reading a rejection receipt can
	// act on. Empty when satisfied.
	Reason string
}

// Evaluator is a constraint whose parameters have been validated, ready to
// decide about a subject.
//
// Parsing and evaluating are separate steps because they fail for different
// reasons and at different times: a constraint whose params are malformed is a
// broken mandate, and one whose params are fine but whose subject falls outside
// them is a refused purchase. Collapsing them would report the first as the
// second and tell the user their limit was exceeded when in fact it could not
// be read.
type Evaluator interface {
	Type() Type
	Evaluate(Subject) Result
}

// Definition is what defining a constraint type requires: the identifier, and
// the algorithm — supplied here as a parser that yields the evaluator.
type Definition struct {
	Type Type

	// Parse validates params and returns the evaluator for them. It is given
	// the raw params from the canonical model, which are open by
	// construction: the registry, not the schema, decides what is valid for a
	// given type.
	Parse func(params map[string]any) (Evaluator, error)
}

// The errors evaluation can produce before it gets as far as deciding.
var (
	// ErrUnknownType means no evaluator is registered for the constraint's
	// type. It is a rejection, never a skip — see the package documentation.
	ErrUnknownType = errors.New("constraint: unknown type")

	// ErrInvalidParams means the type is known and its parameters are not
	// usable. The mandate is malformed rather than unsatisfied.
	ErrInvalidParams = errors.New("constraint: invalid parameters")

	// ErrDuplicateType means a registry was built with one type defined
	// twice, which would make which evaluator runs depend on ordering.
	ErrDuplicateType = errors.New("constraint: type defined twice")
)

// CodeOf maps an evaluation failure to the canonical error code a verifier
// puts in its rejection receipt and its Problem Details response.
//
// The two codes are not interchangeable. constraint_type_unknown says the
// verifier could not form a view; constraint_violated says it formed one and
// the answer was no. A caller told the second when the first is true would
// think the user's limits had been checked.
func CodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnknownType):
		return generated.ErrorCodeConstraintTypeUnknown
	default:
		return generated.ErrorCodeMandateMalformed
	}
}

// Registry maps a constraint type to its definition.
//
// It is immutable after construction, so a verifier's vocabulary cannot change
// under a request in flight — two evaluations of one mandate must not be able
// to disagree because something registered a type in between.
type Registry struct {
	definitions map[Type]Definition
}

// NewRegistry returns a registry holding exactly the definitions given.
func NewRegistry(definitions ...Definition) (*Registry, error) {
	out := make(map[Type]Definition, len(definitions))
	for _, d := range definitions {
		switch {
		case d.Type == "":
			return nil, fmt.Errorf("%w: a definition with no type", ErrInvalidParams)
		case d.Parse == nil:
			return nil, fmt.Errorf("%w: %s has no parser", ErrInvalidParams, d.Type)
		}
		if _, seen := out[d.Type]; seen {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateType, d.Type)
		}
		out[d.Type] = d
	}
	return &Registry{definitions: out}, nil
}

// Types lists what the registry understands, sorted, for diagnostics and for a
// verifier that wants to publish its vocabulary.
func (r *Registry) Types() []Type {
	out := make([]Type, 0, len(r.definitions))
	for t := range r.definitions {
		out = append(out, t)
	}
	sortTypes(out)
	return out
}

// Parse validates one constraint from the canonical model.
//
// It returns ErrUnknownType for a type the registry does not hold, which the
// caller must treat as a rejection.
//
// It is exported separately from Evaluate because there is a caller that needs
// to know a constraint is well-formed without having a purchase to judge: the
// Trusted Surface renders the constraints for the user to read before anything
// is signed, and a surface that displayed a constraint nobody could later parse
// would be collecting a signature on a limit that cannot be enforced.
func (r *Registry) Parse(c generated.Constraint) (Evaluator, error) {
	d, ok := r.definitions[Type(c.Type)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownType, c.Type)
	}
	e, err := d.Parse(c.Params)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidParams, c.Type, err)
	}
	return e, nil
}

// Report is the outcome of evaluating every constraint on a mandate.
type Report struct {
	Results []Result
}

// Satisfied reports whether every constraint held.
//
// An empty report is satisfied, and that is correct rather than a loophole: a
// mandate carrying no constraints is one where the user placed no limits. What
// must never be satisfied by default is a constraint that could not be
// evaluated, and that is handled before evaluation begins — Evaluate returns an
// error rather than a report.
func (r Report) Satisfied() bool {
	for _, res := range r.Results {
		if !res.Satisfied {
			return false
		}
	}
	return true
}

// Violations returns the constraints that were not satisfied, in the order they
// appeared on the mandate.
func (r Report) Violations() []Result {
	var out []Result
	for _, res := range r.Results {
		if !res.Satisfied {
			out = append(out, res)
		}
	}
	return out
}

// Evaluate parses and evaluates every constraint against the subject.
//
// It evaluates all of them rather than stopping at the first failure, so a
// rejection receipt can name everything that was wrong instead of sending the
// caller round the loop once per violated limit.
//
// It returns an error — and no report — if any constraint cannot be parsed at
// all. A partial answer is the dangerous one: a report saying "satisfied" while
// one constraint was never evaluated is exactly the silent skip this package
// exists to prevent.
//
// # Every constraint must hold, including repeats of one type
//
// The list is conjunctive. A mandate carrying price.max twice is satisfied only
// by an amount under both, so the stricter wins — which is the safe direction
// and the useful one. A mandate carrying item.route twice with different routes
// is satisfiable by nothing, and is refused for every purchase.
//
// Worth stating because the two plausible alternative readings are both worse.
// "Later overrides earlier" would let a constraint appended to a signed list
// loosen what the user approved. "Any one matches" would let an attacker widen
// authority by adding a permissive twin of a strict constraint. Conjunction is
// the only reading where adding a constraint cannot increase what the agent may
// do — so a mandate that has been tampered with by addition fails closed.
func (r *Registry) Evaluate(constraints []generated.Constraint, subject Subject) (Report, error) {
	evaluators := make([]Evaluator, 0, len(constraints))
	for _, c := range constraints {
		e, err := r.Parse(c)
		if err != nil {
			return Report{}, err
		}
		evaluators = append(evaluators, e)
	}

	report := Report{Results: make([]Result, 0, len(evaluators))}
	for _, e := range evaluators {
		report.Results = append(report.Results, e.Evaluate(subject))
	}
	return report, nil
}
