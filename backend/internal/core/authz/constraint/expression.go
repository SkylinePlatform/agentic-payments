package constraint

import (
	"errors"
	"fmt"
	"slices"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// MaxDepth bounds how deeply a constraint may nest.
//
// A verifier evaluates this on the request path against input it did not write,
// so the recursion has to be bounded by something other than good intentions.
// Eight is far past anything a person would sign — the built scenario is two —
// and shallow enough that a hostile mandate cannot make parsing expensive.
const MaxDepth = 8

// The errors parsing can produce, all of which mean the mandate is unusable
// rather than the purchase refused.
var (
	// ErrUnknownField means no such fact about a purchase is known to this
	// verifier. Rejected, never skipped.
	ErrUnknownField = errors.New("constraint: unknown field")

	// ErrUnknownOperator means this verifier does not implement that operator.
	ErrUnknownOperator = errors.New("constraint: unknown operator")

	// ErrTypeMismatch means the operator or value does not fit the field's
	// type — comparing an amount with a word, ordering two labels.
	ErrTypeMismatch = errors.New("constraint: type mismatch")

	// ErrMalformed means the node's shape is wrong: a group with no children,
	// a leaf with no field, a `not` with three.
	ErrMalformed = errors.New("constraint: malformed node")

	// ErrTooDeep means the expression nests past MaxDepth.
	ErrTooDeep = errors.New("constraint: expression nested too deeply")

	// ErrCurrencyMismatch means a money comparison spanned two currencies.
	// It surfaces at evaluation rather than parsing, because which currency a
	// purchase is in is not known when the mandate is signed.
	ErrCurrencyMismatch = errors.New("constraint: currency mismatch")

	// ErrViolated means a constraint was read and evaluated, and the purchase
	// did not meet it.
	//
	// Evaluate itself never returns this — see its own doc comment on why an
	// unsatisfied Report has to come back as data, with a nil error, rather
	// than as a failure: "this constraint could not be read" and "this
	// purchase does not meet it" are different outcomes, and collapsing them
	// into one error would make Evaluate's return value lie about which one
	// happened. What a caller turning a Report into a rejection receipt needs
	// afterwards is a code for it, and CodeOf could not give one until now —
	// its own doc comment already named constraint_violated as one of three
	// outcomes this package answers, and had no way to produce it. Report.Err
	// is where a Report becomes this error.
	ErrViolated = errors.New("constraint: purchase falls outside the mandate's limits")
)

// CodeOf maps a failure to the canonical error code a verifier puts in its
// rejection receipt and its Problem Details response.
//
// The three outcomes stay distinct all the way out. constraint_type_unknown
// says the verifier could not form a view, because it does not know a field or
// an operator. mandate_malformed says the constraint could not be read at all.
// constraint_violated says it was read, evaluated, and the answer was no. A
// caller told the last when the first is true would believe the user's limits
// had been checked.
func CodeOf(err error) generated.ErrorCode {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnknownField), errors.Is(err, ErrUnknownOperator):
		return generated.ErrorCodeConstraintTypeUnknown
	case errors.Is(err, ErrViolated):
		return generated.ErrorCodeConstraintViolated
	default:
		return generated.ErrorCodeMandateMalformed
	}
}

// Owns reports whether err is one of this package's failures.
//
// It exists because CodeOf is total and cannot answer this. CodeOf's default
// arm is mandate_malformed, which is the right answer for a constraint that
// could not be read and a badly wrong one for an error this package never
// raised — so a caller that wants "is this yours, and if so what is it" has to
// ask in two steps. Rather than each caller keeping its own list of these
// sentinels, which is a copy free to drift in the direction that misses the
// newest one, they ask here.
//
// Every sentinel this package declares is a member — there are no exclusions
// here, unlike authz.Owns, because everything this package raises is a verdict
// about a constraint and CodeOf has an answer for all of it.
//
// TestOwnsCoversEveryFailure spells that list out a second time rather than
// deriving it, and a spelled-out list is enough *here* for the reason
// ap2.adapterCodes gives for its own: the sentinels are one var block in this
// file, immediately above, so a member missing from the loop below is visible
// at a glance. authz.Owns cannot claim that — its members are spread across
// three files plus this package — which is why its test reads the source
// instead. The difference is the layout, not the standard.
func Owns(err error) bool {
	if err == nil {
		return false
	}
	for _, sentinel := range []error{
		ErrUnknownField,
		ErrUnknownOperator,
		ErrTypeMismatch,
		ErrMalformed,
		ErrTooDeep,
		ErrCurrencyMismatch,
		ErrViolated,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

// Expression is a parsed constraint: a leaf comparing one fact, or a group
// combining others.
//
// It is produced by Parse and is the only thing Evaluate accepts, so a
// constraint that could not be read cannot reach evaluation by any path.
type Expression struct {
	op Op

	// Leaf.
	field    Field
	operator operator
	operands []value

	// Group.
	children []Expression
}

// Op returns what this node does.
func (e Expression) Op() Op { return e.op }

// Fields lists the facts this expression reads, deduplicated and sorted.
//
// # Why an expression has to be able to answer this
//
// A holder of a mandate may show a verifier some of its constraints and
// withhold others, and something has to decide which. That decision cannot be
// made from the constraint's text — "the amount is at most 200 USD" and "the
// origin is BEG" are the same shape — so it is made from the facts each one
// reads, against the facts the verifier being presented to can state. A
// verifier that cannot state a fact does not enforce a constraint about it; it
// reports the fact as unstated and refuses, every time, which is a refusal made
// in ignorance rather than a limit being applied.
//
// So this is the question the caller doing that narrowing asks, and it lives
// here because it is a question about a constraint. Nothing in this package
// knows what a presentation is, which verifier exists, or which of them holds
// what — the answer is a list of field names, and what to do with it belongs to
// whoever knows the audience.
//
// # What the list contains
//
// Leaf nodes contribute the field they compare; groups contribute the union of
// their children's. An expression that did not come from Parse contributes
// nothing, on the same grounds Evaluate refuses one: it has no field to read.
//
// Names come back exactly as the constraint wrote them, which means an item
// attribute appears in its addressed form — "item.attr.route.origin", not
// "route.origin". A caller matching these against the registry should note that
// FieldNames covers only the closed part of the vocabulary: Parse admits a
// leaf's field name only if the registry holds it or it carries the
// item-attribute prefix, so a name here that FieldNames does not list is an
// item attribute, and there is no third case.
func (e Expression) Fields() []string {
	read := make(map[string]struct{})
	e.collectFields(read)

	out := make([]string, 0, len(read))
	for name := range read {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func (e Expression) collectFields(into map[string]struct{}) {
	if !e.parsed() {
		return
	}
	if isGroup(e.op) {
		for _, child := range e.children {
			child.collectFields(into)
		}
		return
	}
	into[e.field.Name] = struct{}{}
}

// parsed reports whether this expression came from Parse rather than being
// declared. A group needs children and a leaf needs a field to read; an
// expression with neither is the zero value.
func (e Expression) parsed() bool {
	if isGroup(e.op) {
		return len(e.children) > 0
	}
	return e.field.read != nil
}

// Parse reads one constraint from the canonical model into an expression.
//
// It is exported separately from Evaluate because there is a caller that needs
// to know a constraint is well-formed without having a purchase to judge: the
// Trusted Surface renders the constraints for the user to read before anything
// is signed, and a surface that displayed a constraint nobody could later parse
// would be collecting a signature on a limit that cannot be enforced.
func Parse(c generated.Constraint) (Expression, error) {
	return parse(c, 0)
}

func parse(c generated.Constraint, depth int) (Expression, error) {
	if depth > MaxDepth {
		return Expression{}, fmt.Errorf("%w: past %d levels", ErrTooDeep, MaxDepth)
	}

	op := Op(c.Op)
	if isGroup(op) {
		return parseGroup(c, op, depth)
	}
	return parseLeaf(c, op)
}

func parseGroup(c generated.Constraint, op Op, depth int) (Expression, error) {
	switch {
	case c.Field != nil:
		return Expression{}, fmt.Errorf("%w: %s is a group and takes no field", ErrMalformed, op)
	case c.Value != nil:
		return Expression{}, fmt.Errorf("%w: %s is a group and takes no value", ErrMalformed, op)
	case len(c.Of) == 0:
		// An empty group says nothing. `all` of nothing is vacuously true and
		// would permit every purchase; `any` of nothing is unsatisfiable and
		// would refuse them all. Neither is something a person meant to sign,
		// and both are more likely a constraint that lost its children in
		// transit than an intent.
		return Expression{}, fmt.Errorf("%w: %s has no children", ErrMalformed, op)
	case op == OpNot && len(c.Of) != 1:
		// "not" over several children has two readings — none of them, or not
		// all of them — and picking one silently would make a mandate mean
		// something its author did not choose. Say `not` of an `any` or of an
		// `all` instead, which says which.
		return Expression{}, fmt.Errorf("%w: not takes exactly one child, got %d", ErrMalformed, len(c.Of))
	}

	children := make([]Expression, 0, len(c.Of))
	for _, child := range c.Of {
		parsed, err := parse(child, depth+1)
		if err != nil {
			return Expression{}, err
		}
		children = append(children, parsed)
	}
	return Expression{op: op, children: children}, nil
}

func parseLeaf(c generated.Constraint, op Op) (Expression, error) {
	if c.Field == nil || *c.Field == "" {
		return Expression{}, fmt.Errorf("%w: %s is a comparison and needs a field", ErrMalformed, op)
	}
	if len(c.Of) > 0 {
		return Expression{}, fmt.Errorf("%w: %s is a comparison and takes no children", ErrMalformed, op)
	}

	field, err := lookupField(*c.Field)
	if err != nil {
		return Expression{}, err
	}
	operator, err := lookupOperator(op, field)
	if err != nil {
		return Expression{}, err
	}
	operands, err := parseOperands(operator, field, c.Value)
	if err != nil {
		return Expression{}, err
	}
	return Expression{op: op, field: field, operator: operator, operands: operands}, nil
}

// Evaluate decides whether the subject satisfies the expression.
//
// An Expression that did not come from Parse is refused rather than evaluated.
// The type is exported, so nothing stops a caller declaring one, and its zero
// value has no field to read and no children to combine — reaching either would
// panic, and a verifier that panics on one request stops answering all of them.
// Refusing is also the safe direction: an expression nobody parsed is not one
// the user signed.
func (e Expression) Evaluate(s Subject) Result {
	if !e.parsed() {
		return Result{Reason: "this constraint was never parsed, so nothing about it has been checked"}
	}

	switch e.op {
	case OpAll:
		return e.evaluateAll(s)
	case OpAny:
		return e.evaluateAny(s)
	case OpNot:
		inner := e.children[0].Evaluate(s)
		if inner.Satisfied {
			return Result{Satisfied: false, Reason: "the purchase matches something the mandate excludes: " + inner.describe()}
		}
		return Result{Satisfied: true}
	default:
		return e.evaluateLeaf(s)
	}
}

func (e Expression) evaluateAll(s Subject) Result {
	// Every child is evaluated rather than stopping at the first failure, so a
	// rejection receipt can name everything that was wrong instead of sending
	// the caller round the loop once per violated limit.
	var failures []string
	for _, child := range e.children {
		if r := child.Evaluate(s); !r.Satisfied {
			failures = append(failures, r.Reason)
		}
	}
	if len(failures) == 0 {
		return Result{Satisfied: true}
	}
	return Result{Reason: joinReasons(failures, "; ")}
}

func (e Expression) evaluateAny(s Subject) Result {
	var failures []string
	for _, child := range e.children {
		r := child.Evaluate(s)
		if r.Satisfied {
			return Result{Satisfied: true}
		}
		failures = append(failures, r.Reason)
	}
	return Result{Reason: "none of these held: " + joinReasons(failures, "; ")}
}

func (e Expression) evaluateLeaf(s Subject) Result {
	got, stated := e.field.read(s)
	if !stated {
		// A fact the purchase does not carry cannot be shown to be inside a
		// limit the user approved. Treating unstated as permitted is how a
		// limit stops limiting — and it is the failure an agent would find
		// first, by simply omitting the field.
		return Result{Reason: fmt.Sprintf("%s was not stated, so it cannot be shown to satisfy %q",
			e.field.Noun, e.Render())}
	}

	ok, err := e.apply(got)
	if err != nil {
		return Result{Reason: err.Error()}
	}
	if ok {
		return Result{Satisfied: true}
	}
	return Result{Reason: fmt.Sprintf("%s %s, and the mandate requires that %s",
		e.field.Noun, renderValue(got), e.Render())}
}

// apply runs the comparison. Errors here are evaluation-time facts about the
// purchase — a currency nobody can convert — not defects in the mandate.
func (e Expression) apply(got value) (bool, error) {
	switch e.op {
	case OpEq, OpNeq, OpLt, OpLte, OpGt, OpGte, OpBefore, OpAfter:
		c, err := compare(got, e.operands[0])
		if err != nil {
			return false, err
		}
		switch e.op {
		case OpEq:
			return c == 0, nil
		case OpNeq:
			return c != 0, nil
		case OpLt, OpBefore:
			return c < 0, nil
		case OpLte:
			return c <= 0, nil
		case OpGt, OpAfter:
			return c > 0, nil
		default: // OpGte
			return c >= 0, nil
		}

	case OpIn, OpNin:
		// Every operand is tried before answering, and an incomparable one is
		// remembered rather than returned at once.
		//
		// Returning on the first error made the answer depend on the order of
		// the list: `in [EUR 1.00, USD 200.00]` against a USD purchase failed
		// on the euro, while the same two values the other way round matched
		// the dollar and never reached it. One mandate, two answers, decided
		// by which operand the author happened to write first — and a verifier
		// whose result depends on that is not deterministic in any sense worth
		// the name.
		var incomparable error
		for _, want := range e.operands {
			c, err := compare(got, want)
			if err != nil {
				incomparable = err
				continue
			}
			if c == 0 {
				return e.op == OpIn, nil
			}
		}
		if incomparable != nil {
			// Nothing matched, and at least one operand could not be compared
			// at all — so "no match" is not something this verifier established.
			// Saying so beats reporting an absence it did not verify.
			return false, incomparable
		}
		return e.op == OpNin, nil

	case OpBetween, OpWithin:
		// Inclusive at both ends. That is how a person describes a holiday —
		// "June the first to August the thirty-first" includes both days — and
		// a purchase landing exactly on a boundary should not turn on which
		// side of a microsecond the clock fell.
		low, err := compare(got, e.operands[0])
		if err != nil {
			return false, err
		}
		high, err := compare(got, e.operands[1])
		if err != nil {
			return false, err
		}
		return low >= 0 && high <= 0, nil

	default:
		return false, fmt.Errorf("%w: %s", ErrUnknownOperator, e.op)
	}
}

// Result is what an expression decided.
type Result struct {
	// Satisfied is the answer.
	Satisfied bool

	// Reason says why not, in terms a person reading a rejection receipt can
	// act on. Empty when satisfied.
	Reason string
}

func (r Result) describe() string {
	if r.Reason != "" {
		return r.Reason
	}
	return "it holds"
}

// Report is the outcome of evaluating every constraint on a mandate.
type Report struct {
	Results []Result
}

// Satisfied reports whether every constraint held.
//
// An empty report is satisfied, and that is correct rather than a loophole: a
// mandate carrying no constraints is one where the user placed no limits. What
// must never be satisfied by default is a constraint that could not be read,
// and that is handled before evaluation begins — Evaluate returns an error
// rather than a report.
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

// Err turns an unsatisfied Report into an error wrapping ErrViolated, or
// returns nil when the Report is satisfied.
//
// This is the one place that conversion belongs. Every verifier that
// evaluates a mandate — a chain, a single closed mandate, a search — reaches
// the same moment: a Report that says no, and a rejection receipt that needs
// a reason and a code. Leaving each caller to write that translation is how
// two verifiers end up disagreeing about which violation's Reason a receipt
// names, or forgetting the code entirely; a method on the type that already
// holds both closes that off once.
//
// The reason named is the first violation's, in mandate order — the same
// order Violations returns them in, and the first is what a signer reading
// the receipt reaches first too.
func (r Report) Err() error {
	if r.Satisfied() {
		return nil
	}
	reason := "the purchase did not satisfy every constraint the mandate placed"
	if violations := r.Violations(); len(violations) > 0 && violations[0].Reason != "" {
		reason = violations[0].Reason
	}
	return fmt.Errorf("%w: %s", ErrViolated, reason)
}

// Evaluate parses and evaluates every constraint on a mandate against the
// subject.
//
// It returns an error — and no report — if any constraint cannot be parsed at
// all. A partial answer is the dangerous one: a report saying "satisfied" while
// one constraint was never evaluated is exactly the silent skip this package
// exists to prevent.
//
// # The list is conjunctive, and that is load-bearing
//
// Every constraint on the mandate must hold. The two plausible alternatives are
// both worse: "later overrides earlier" would let a constraint appended to a
// signed list loosen what the user approved, and "any one matches" would let an
// attacker widen authority by adding a permissive twin. Conjunction is the only
// reading where appending to the top-level list cannot increase what the agent
// may do, so a mandate tampered with by addition fails closed.
//
// # What `any` costs, stated plainly
//
// That property holds for the top-level list and stops at the first `any`.
// Inside one, adding a branch widens authority rather than narrowing it. The
// signature over the mandate is then the only thing preventing that, where the
// top level has both the signature and the structure. This is the price of an
// expression language and it is worth paying, but a reader should not be left
// assuming the old property still holds all the way down.
func Evaluate(constraints []generated.Constraint, subject Subject) (Report, error) {
	expressions := make([]Expression, 0, len(constraints))
	for _, c := range constraints {
		e, err := Parse(c)
		if err != nil {
			return Report{}, err
		}
		expressions = append(expressions, e)
	}

	report := Report{Results: make([]Result, 0, len(expressions))}
	for _, e := range expressions {
		report.Results = append(report.Results, e.Evaluate(subject))
	}
	return report, nil
}
