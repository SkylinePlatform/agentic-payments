// Package generated holds the canonical model, compiled from the JSON Schema
// in contracts/.
//
// Every other file here is generated and none of them is committed — see
// AGENTS.md, "Generated code is not hand-edited". This file is the exception:
// it is hand-written, tracked, and its only job is to make the absence of the
// others say so.
//
// Without it the directory does not exist in a fresh clone, so what fails is
// module resolution — "no required module provides package .../core/generated;
// to add it: go get ..." — reported against
// internal/core/authz/constraint/expression.go, a hand-written file, and
// offering a `go get` that cannot work. gopls then loses the build list for the
// whole module and reports the standard library as unimportable, which reads
// like a broken toolchain or a missing go.work rather than a tree nobody has
// generated into yet.
//
// With it the package always exists, the module still resolves, and what fails
// is the declaration below — in this file, whose comment is the command to run.
package generated

// The build breaks here, on purpose, when this package has not been generated.
//
//   - ErrorCode is declared by model.go, written by `make generate-go`.
//   - Disclosable is declared by disclosure.go, written by
//     `make generate-disclosure`.
//
// Two references rather than one, because either file can be present without
// the other — `make generate-go` on its own leaves half a package — and a
// package that compiles with half its symbols pushes the failure back out to
// the packages that import it, which is the failure this file exists to stop.
//
// Both symbols already exist for their own reasons. Nothing was added to a
// generator so that hand-written code could point at it: a generator emitting a
// symbol whose only consumer is a file it does not know about is a coupling in
// the direction generators are not supposed to have.
//
// `make setup` writes both on a fresh clone; `make check` writes both before it
// lints anything.
var (
	_ ErrorCode
	_ = Disclosable
)
