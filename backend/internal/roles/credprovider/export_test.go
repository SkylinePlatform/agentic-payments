package credprovider

import "io"

// The seam announced_test.go needs, kept where a deployment cannot reach it.
//
// mint's only failure is its entropy source, and crypto/rand practically never
// refuses — so the one branch in fund that can drop an already-signed receipt
// is unreachable from outside unless something can stand in for that source.
// The obvious way to offer one is an exported field on Service, and that is
// what this file exists instead of: Service is configured by struct literal in
// cmd/credprovider, so an Entropy field would sit beside Signer and Keys, be
// settable by any wiring change, and be guarded by nothing but its own doc
// comment. A predictable source there is a guessable credential.
//
// A file named export_test.go is compiled into this package's test binary and
// into no other build, so the setter below exists for credprovider_test and
// does not exist in the binary anybody deploys. It is the same arrangement
// .mockery.yml already relies on for the doubles it writes beside an interface
// — declared in the package, visible to the external test package, absent from
// the shipped build — and it is what makes "a deployment leaves this alone" a
// fact about the type rather than an instruction somebody has to follow.

// SetEntropy replaces the source mint draws its randomness from.
//
// Nil restores crypto/rand. A test passes a reader that refuses, which is the
// only way to reach the branch where fund has signed a receipt and cannot mint
// the credential that goes with it.
func (s *Service) SetEntropy(r io.Reader) { s.entropy = r }
