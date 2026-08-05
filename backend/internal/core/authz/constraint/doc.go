// Package constraint is the limits a user places on what an agent may do, and
// the deterministic gate that decides whether a purchase falls inside them.
//
// A constraint is an expression tree. A leaf compares one fact about the
// purchase — amount, time, quantity, what is being bought, who from — against a
// value. A group combines the nodes beneath it with all, any or not.
//
// Constraints are evaluated by the verifier. Never by the agent.
//
// # Why that sentence is the important one
//
// The agent may propose anything — that is what a language model does. What
// makes the design safe is not that the agent behaves, but that a deterministic
// gate on the other side decides, and that the gate reads the limits the *user*
// signed rather than anything the agent said about them. An agent able to wave
// through its own mistake would make every other guarantee in this repository
// decorative.
//
// Nothing here calls a model, and nothing here can: this package imports the
// standard library and the canonical model, and core-isolation in
// .golangci.yml stops it importing anything else in the module.
//
// # Generality comes from a matrix, not from a list of types
//
// A closed registry of fields, each with a declared value kind, and a table of
// operators valid for each kind. Adding a fact about a purchase is one entry in
// field.go, after which every operator that fits its kind works on it — rather
// than a new named constraint type needing its own parser, evaluator and
// renderer.
//
// Facts that belong to one kind of purchase rather than to purchases in general
// are item attributes, addressed as item.attr.<name>. A flight's route, a
// concert's venue, a bicycle's frame size. Core does not know what a flight is
// and should not have to.
//
// # An unknown field or operator is rejected, never ignored
//
// Silently skipping a constraint the verifier does not understand converts a
// limit the user set into a limit nobody enforces, and lets the transaction
// proceed while misrepresenting what was approved. That is the worst available
// outcome, and it is why the canonical model leaves op and field open strings:
// an unknown one has to be representable in order to be named in a rejection
// receipt, where a parse failure could be neither reported nor told apart from
// a malformed message.
//
// Three outcomes stay distinct all the way out, and CodeOf is where they are
// mapped. A field or operator nobody knows is constraint_type_unknown — the
// verifier could not form a view. A constraint that could not be read at all is
// mandate_malformed. A constraint read, evaluated and failed is
// constraint_violated. Collapsing the first into the last would tell a user
// their limit was exceeded when in fact nobody could read it.
//
// # The top-level list is conjunctive, and what `any` costs
//
// Every constraint on a mandate must hold. The two plausible alternatives are
// both worse: "later overrides earlier" would let a constraint appended to a
// signed list loosen what the user approved, and "any one matches" would let an
// attacker widen authority by adding a permissive twin. Conjunction is the only
// reading where appending to the top-level list cannot increase what the agent
// may do, so a mandate tampered with by addition fails closed.
//
// That property stops at the first `any`. Inside one, adding a branch widens
// authority rather than narrowing it, so the signature over the mandate is the
// only thing preventing it — where the top level has both the signature and the
// structure. This is the price of an expression language and it is worth
// paying, but a reader should not be left assuming the old property still holds
// all the way down.
//
// # Every node must say itself, and that is a test
//
// Render produces one sentence per node, and a node type that cannot be said
// clearly does not enter the model. That is not presentation polish: beat 3 of
// the built scenario is the Trusted Surface showing the user the interpretation
// and taking their signature on it, and a constraint the surface cannot state
// plainly is one the user cannot meaningfully approve.
//
// # What a constraint cannot be
//
// A constraint is refutable at the point of purchase: a merchant can check that
// an amount is at most some figure. No merchant can check that a purchase was
// the *cheapest available anywhere* — that needs the whole market at an instant,
// which no single verifier has, and the merchant is the least neutral party to
// ask. The same is true of best, fastest, nearest, highest rated.
//
// So an objective is not a constraint and cannot become one. What the
// interpreter does with "buy the cheapest" is turn it into a bound that is
// checkable, plus search behaviour that nobody verifies — and the Trusted
// Surface must show the bound, because a surface displaying "buy the cheapest"
// would collect a signature on something no verifier can enforce. This is the
// stated edge of the model rather than a gap in it.
//
// # Selective disclosure is a mandate-level concern
//
// contracts/authz/*_open.json mark the constraints array with
// x-disclosable-items, so the unit of disclosure is a whole top-level
// constraint and never a subtree. Withholding one branch of an `any` would
// leave the remaining branch looking mandatory, which misrepresents what the
// user approved rather than merely telling the verifier less.
package constraint
