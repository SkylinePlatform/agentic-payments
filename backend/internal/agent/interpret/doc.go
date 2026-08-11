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
// # One other thing crosses this boundary, and the limit on it is the point
//
// Selective says whether a constraint field describes what to go looking for or
// states a term the purchase has to meet. The fact belongs to the registry and
// this package only forwards it, unaltered: internal/agent needs it to build a
// merchant search and may not import the registry to ask, so it carried two
// string prefixes instead until issue #132.
//
// It is here for Validate's reason rather than merely beside it. Both are the
// verifier's own answer about the constraint vocabulary, reached through the one
// package that already had to hold such an answer so that no second list of
// field names could drift — and the prefixes were exactly that second list.
// Neither infers anything from a sentence, and no model is on the path.
//
// **What may follow it is only more of the same.** This package is not the
// window through which the agent reaches core, and reading Selective as a
// precedent for routing some other fact through here would be the wrong
// reading. Every package that can reach an interpreter is one hard rule 2 has to
// argue about — reach_test.go's mayReach list is where that argument is written
// down, and widening the reasons to import this package, for anything that is
// not the verifier's own answer about the vocabulary, is how a rule about where
// a model may live decays into a formality.
//
// # Two implementations, and the suite that holds both to the same promise
//
// ScriptedInterpreter maps fixed prompts to fixed constraint sets. It is what
// every test uses and what the demo runs — cmd/agent builds interpret.Demo()
// unless it is given -interpreter gemini, or -interpreter auto with
// GEMINI_API_KEY exported — because no test may depend on a live model or on
// an external network call, and because a non-deterministic demo would take
// every golden number in this repository with it.
//
// ModelInterpreter is the other one. It calls a Model, the narrower port inside
// this package over which nothing about a provider crosses, decodes the answer
// with the same decode the scripted one uses, and calls Validate. Gemini is that
// port's one implementation: one POST, no SDK. Adding a second provider is a
// file beside it and a case in cmd/agent's flag — nothing outside this package
// changes, which is the third box on issue #17.
//
// The obligation to call Validate used to be exactly that — an obligation. It
// could not be enforced while ScriptedInterpreter was the only implementation:
// it does call Validate, and for it the call cannot fail, because NewScripted
// validated the same text and decoding is deterministic. So the call was the
// interface's contract written down rather than a check earning its keep, and
// nothing held the next implementation to it.
//
// TestNoInterpreterReturnsSomethingAVerifierCouldNotRead is what closed that. It
// is a suite over implementations, each registering a rig that makes it answer
// arbitrary raw JSON, and the property is that the implementation refuses it
// either at construction or at Interpret and never returns it — two moments,
// because the scripted one refuses at construction and the model-backed one at
// Interpret. The built scenario has to come back deep-equal, so passing by
// refusing everything is not available.
package interpret
