// Package constraint is the constraint type registry. Each constraint type has
// a unique identifier, a schema marking which of its fields are selectively
// disclosable, and an evaluation algorithm.
//
// Constraints are evaluated by the verifier. Never by the agent.
package constraint
