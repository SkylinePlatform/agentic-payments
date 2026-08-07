// Package interpret turns a sentence the user typed into the typed constraints
// they will be shown and asked to sign.
//
// It is the only package in this repository where a language model may appear.
// The separation is physical rather than conventional so that the rule is
// checkable by reading one import list: a model reached for anywhere else shows
// up as an import in a package that has no business having one.
//
// # Interpretation happens once, before anything is signed
//
// The sentence is read into constraints, the Trusted Surface renders those
// constraints and takes the user's signature on them, and from that point there
// is no model anywhere in the flow. Beat 4 of the built scenario is the agent
// watching a price for days, and it is an integer comparison.
//
// That ordering is what makes a hallucinating model harmless here rather than
// catastrophic. The model proposes limits, a human approves the limits it
// proposed, and a deterministic verifier enforces them against a purchase the
// model does not get to judge.
//
// # The interpreter decides nothing
//
// What comes out is a description of limits, never an instruction to buy.
// Constraints are evaluated by the verifier and never by the agent —
// internal/core/authz/constraint says why at length. An interpreter that also
// judged purchases would make every other guarantee in this repository
// decorative.
//
// # Objectives are not constraints
//
// "Cheapest", "best", "fastest", "nearest" are not refutable at the point of
// purchase: no merchant can establish what the whole market was offering at an
// instant, and the merchant is in any case the least neutral party to ask. So
// the interpreter's job with such a sentence is to produce a bound that is
// checkable and to leave the searching as agent behaviour that nobody verifies.
// The bound is then a number the user never said, which is the sharpest reason
// the surface shows the interpretation rather than the prompt.
//
// # Every implementation validates before returning
//
// Validate is the contract, and it applies to whatever ends up behind the
// interface. The interpreter is the one component here allowed to be
// non-deterministic, so it is the one whose output must not be taken on trust: a
// constraint naming a field the verifier does not know would be rendered,
// signed, and then rejected as constraint_type_unknown at the moment of
// purchase, having looked like a limit the whole way along.
//
// # Nothing here calls a model yet
//
// ScriptedInterpreter maps fixed prompts to fixed constraint sets. It is what
// every test uses, and what the demo will use once the agent leg of #15 gives
// it a caller, because no test may depend on a live model or on an external
// network call. A model-backed implementation satisfies the same interface,
// calls Validate on what came back, and lives beside it rather than replacing
// it.
//
// One honest note about that last obligation. ScriptedInterpreter does call
// Validate before returning, and for this implementation the call cannot fail:
// NewScripted validated the same text and decoding is deterministic. So the
// call is the interface's contract written down rather than a check that earns
// its keep here — and nothing yet forces a second implementation to make it.
// The place to fix that is when there is a second implementation to hold to it:
// a shared conformance test the model-backed one has to pass, which is #17.
package interpret
