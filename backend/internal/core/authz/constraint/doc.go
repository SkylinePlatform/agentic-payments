// Package constraint is the constraint type registry. Each constraint type has
// a unique identifier, a schema marking which of its fields are selectively
// disclosable, and an evaluation algorithm.
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
// # The vocabulary is ours
//
// AP2 defines constraints as an extension point: it says what defining one
// requires and leaves the vocabulary to the implementer. So this package lives
// in core rather than in adapters/ap2 — when a third protocol arrives, the
// vocabulary should not have to move. AP2 merely transports what is defined
// here.
//
// # An unknown type is rejected, never ignored
//
// Silently skipping a constraint the verifier does not understand converts a
// limit the user set into a limit nobody enforces, and lets the transaction
// proceed while misrepresenting what was approved. That is the worst available
// outcome, and it is why the canonical model leaves the type identifier an open
// string: an unknown type has to be representable in order to be named in a
// rejection receipt, where a parse failure could be neither reported nor told
// apart from a malformed message.
//
// # Selective disclosure is a mandate-level concern
//
// Issue #11 lists a per-type disclosure schema as one of the three things
// defining a constraint requires. In this model that is already answered one
// level up: contracts/authz/*_open.json mark the constraints array with
// x-disclosable-items, so the unit of disclosure is a whole constraint rather
// than a field inside its params.
//
// That is also the only unit that means anything. A price.max disclosed with
// its amount withheld would tell a verifier that a limit exists without saying
// what it is — not a weaker claim but an unusable one, since the verifier's
// only options would be to reject every purchase or to ignore the limit.
package constraint
