package constraint

import "github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"

// Narrowing returns what one constraint narrows a merchant's catalogue search
// to: nothing, itself, or the part of itself a catalogue can answer.
//
// Selective answers this for a field. This answers it for a node, which is the
// same fact one level up and is issue #203. Until it, nobody asked the question
// of a node at all: internal/agent's discovery half tested c.Field for nil and
// went round the loop, so a group — all, any, not — carried op and of, never a
// field, and was dropped whole with nothing logged and nothing failing.
//
// # A query is not a mandate, and the direction is what makes an answer honest
//
// Nothing here can weaken a limit. Every constraint the user signed stays on the
// mandate and is evaluated by the verifier at the moment of purchase, whatever
// this returns; what a query decides is only what the agent goes looking for.
// The property that makes one honest is one-directional:
//
//	the constraint must imply the query, never the other way round
//
// A query the constraint does not imply excludes offers the user approved, so
// discovery never shows them the thing they asked for. Among the queries the
// constraint does imply, the strongest is the one to send: a weaker one comes
// back with offers the sentence did not describe, and the agent takes the first.
// So this returns the strongest implied query it can construct — and the three
// node kinds differ over exactly one thing, which is whether they are monotone
// in their children.
//
// # What each node kind contributes
//
//   - A **leaf** contributes itself when its field is selective, and nothing
//     otherwise. That is Selective, unchanged: a bound on the price, the time or
//     the quantity is a term the purchase has to meet, and a search carrying it
//     returns nothing at all while the price is still too high — which is the
//     one case a watch exists for.
//
//   - **all** contributes each child's narrowing, concatenated. all(a,b) implies
//     a, so dropping a conjunct only weakens the question; and the query is
//     itself a conjunctive list, so the children join it rather than being
//     rewrapped. Nesting flattens, because all is associative and no verifier
//     can tell the two apart.
//
//   - **any** contributes the disjunction of its children's narrowings, and only
//     when every child narrows to something. A branch that narrows to nothing
//     has widened to "any offer at all", and any(anything, X) is anything — so
//     the group has nothing left in it a catalogue can answer. Sending the other
//     branches alone would ask a *narrower* question than the user's sentence,
//     which is the one direction that is not safe.
//
//   - **not** contributes itself, whole and unchanged, and only when every fact
//     beneath it is one a catalogue can answer. Negation is antitone: from
//     a ⟹ a′ it follows ¬a′ ⟹ ¬a, which is the wrong way round, so a weakened
//     child cannot be negated. A flight costing $300 satisfies
//     "not (a flight under $200)" and would be excluded by "not a flight".
//
// **not is also the arm whose silent drop costs the most**, and it is worth
// stating plainly because it is not the same cost as the others. Dropping a
// selector widens a search; dropping a negation hands the agent precisely what
// the user excluded — `not(item.category eq "flights")` dropped leaves the first
// flight in the catalogue as candidate zero.
//
// # What cannot be read narrows nothing
//
// The node is parsed first, and a node that does not parse contributes nothing.
// That is Selective's own answer for an unknown name — "putting it in a query
// asks a merchant a question in a vocabulary neither party shares" — applied to
// a whole node rather than to a name, and it covers the shapes a name cannot: a
// group carrying a field, an empty group, a not with two children, a comparison
// its field's kind refuses.
//
// It is also what lets everything below read c.Of and *c.Field without checking
// either. Parse settles the shape of every node in the tree and bounds the
// recursion at MaxDepth, so this function inherits that bound rather than
// keeping a second one that could disagree with it.
//
// # Where it stops, deliberately
//
// A not over a group that mixes a selector with a term contributes nothing,
// where pushing the negation down by De Morgan would sometimes find one:
// ¬(a ∨ b) is ¬a ∧ ¬b, and the first conjunct may be answerable on its own. That
// is a rewriting into normal form, which is a larger thing to audit than the
// search it improves, and the cost of stopping short is a wider query rather
// than a wrong one. It is written here rather than left for a reader to notice.
//
// # The contrast with selective disclosure, which runs the other way
//
// contracts/authz/constraint.json refuses to let a holder withhold one branch of
// an any from a verifier, because the remaining branch then looks mandatory —
// that is a *strengthening*, and it misrepresents what the user approved.
// Everything here is a weakening, which is why an any may travel with its
// branches narrowed and may not travel with one of them removed. Same
// monotonicity, opposite direction, and the two must not be read as one rule.
func Narrowing(c generated.Constraint) []generated.Constraint {
	if _, err := Parse(c); err != nil {
		return nil
	}
	return narrowing(c)
}

// narrowing is Narrowing over a node Parse has already accepted, so a group has
// children, a not has exactly one, and a leaf has a field.
func narrowing(c generated.Constraint) []generated.Constraint {
	switch Op(c.Op) {
	case OpAll:
		var out []generated.Constraint
		for _, child := range c.Of {
			out = append(out, narrowing(child)...)
		}
		return out

	case OpAny:
		branches := make([]generated.Constraint, 0, len(c.Of))
		for _, child := range c.Of {
			narrowed := narrowing(child)
			if len(narrowed) == 0 {
				return nil
			}
			branches = append(branches, conjunction(narrowed))
		}
		return []generated.Constraint{{Op: string(OpAny), Of: branches}}

	case OpNot:
		if !whollySelective(c) {
			return nil
		}
		return []generated.Constraint{c}

	default:
		if !Selective(*c.Field) {
			return nil
		}
		return []generated.Constraint{c}
	}
}

// conjunction turns one child's narrowing back into a single node, for the one
// caller that needs a node rather than a list: a branch of an any.
//
// The list a narrowing comes back as is conjunctive — that is what lets all's
// children join the query's own list — so the node is an all of them, and one
// part needs no group around it at all.
func conjunction(parts []generated.Constraint) generated.Constraint {
	if len(parts) == 1 {
		return parts[0]
	}
	return generated.Constraint{Op: string(OpAll), Of: parts}
}

// whollySelective reports whether every fact a node reads is one a catalogue can
// answer, which is the condition a not has to meet to travel at all.
//
// It asks the parsed expression rather than walking the raw node a second time.
// Fields is already the published answer to "which facts does this read", and a
// second walk here would be a copy of it free to drift — over the item-attribute
// family first, which no listing enumerates.
func whollySelective(c generated.Constraint) bool {
	e, err := Parse(c)
	if err != nil {
		return false
	}
	for _, name := range e.Fields() {
		if !Selective(name) {
			return false
		}
	}
	return true
}
