// Package agent is the shopping agent — the one component in this system
// allowed to be non-deterministic.
//
// It assembles carts and proposes constraints. It does not verify anything, and
// it never signs as the user: under Human Not Present it signs closed mandates
// with its **own** key, which is a delegation the user's open mandate endorsed
// in cnf rather than a signature standing in for theirs. The difference is what
// a verifier checks — it resolves the agent's key out of the mandate the user
// signed, and refuses anything signed by another.
package agent
