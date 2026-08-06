package ap2

import (
	"context"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// The two ports say the same thing in two vocabularies, and this is where they
// meet.
//
// authz.Signer answers Key() with a KeyRef, because core describes a key as a
// domain fact: an identifier and the algorithm it is bound to. sdjwt.Signer
// answers Algorithm() and KeyID() separately, because JOSE writes them into a
// protected header as two members. Neither shape is wrong and neither belongs
// in the other package — core must not know what a JOSE header is, and
// pkg/sdjwt implements a public standard and must not know this project's
// domain types. So the translation lives here, in the adapter, which is the
// only layer allowed to know both.
//
// internal/platform/crypto's own test wrote the verifier half inline and left a
// note saying the production version arrives with this issue. This is it, and
// the test can now use it instead of its own copy.

// A wrapper must not turn a nil interface into a non-nil one.
//
// That is the rule the three constructors below exist to keep, and it is worth
// stating separately from what they adapt. A struct wrapping a nil interface is
// itself non-nil, so `joseSigner{nil}` sails past every `if signer == nil` in
// pkg/sdjwt and panics on the first method call instead — a nil dereference
// several frames inside a package that had a check for exactly this and was
// denied the chance to run it.
//
// The guards in IssueCheckout, VerifyCheckout, IssuePayment and VerifyPayment
// were written because of that, and they are still there because ErrMisconfigured
// names the culprit better than "no signer" does. What they are no longer is
// load-bearing: nothing in this package constructs a bridge except through these
// functions, so an entry point added tomorrow without a guard produces an error
// from pkg/sdjwt rather than a panic. internal/platform/crypto's material.go
// records the same class of bug one interface further down, and refuses it the
// same way — by never letting the nil be wrapped.

// JOSESigner adapts an authz.Signer for the JOSE layer.
//
// Exported because the roles sign AP2 artefacts this package does not construct
// for them. The merchant's Checkout JWT is the case: its contents are the
// merchant's business — AP2 only ever hashes the compact serialisation — but it
// is a compact JWS signed by a key the rest of the protocol has to verify, so
// the bridge between the two vocabularies is still this package's to provide.
//
// It was test-only until a role needed one. Growing an exported API for a test
// would have been the wrong order; this is the right one.
//
// A nil Signer produces a nil sdjwt.Signer rather than a wrapper around nothing.
func JOSESigner(s authz.Signer) sdjwt.Signer {
	if s == nil {
		return nil
	}
	return joseSigner{s}
}

// JOSEVerifier is the verifying half, for the same reason, and preserves nil for
// the same reason again.
func JOSEVerifier(v authz.Verifier) sdjwt.Verifier {
	if v == nil {
		return nil
	}
	return joseVerifier{v}
}

// joseClockOf is the third of the trio. It is unexported because no role holds
// an sdjwt.Clock, only this package does.
func joseClockOf(c authz.Clock) sdjwt.Clock {
	if c == nil {
		return nil
	}
	return joseClock{c}
}

// joseSigner adapts an authz.Signer to the sdjwt.Signer pkg/sdjwt takes.
type joseSigner struct{ inner authz.Signer }

func (s joseSigner) Algorithm() string { return string(s.inner.Key().Algorithm) }
func (s joseSigner) KeyID() string     { return s.inner.Key().KeyID }

func (s joseSigner) Sign(ctx context.Context, signingInput []byte) ([]byte, error) {
	return s.inner.Sign(ctx, signingInput)
}

// joseVerifier adapts an authz.Verifier to the sdjwt.Verifier.
//
// It deliberately does not carry the key id. pkg/sdjwt compares the header's
// alg against Algorithm and refuses a mismatch, and the key was already chosen
// by whoever resolved this Verifier — reading kid out of the header to pick a
// key is the other half of the algorithm-confusion bug, and an adapter that
// does not expose kid cannot be talked into it.
type joseVerifier struct{ inner authz.Verifier }

func (v joseVerifier) Algorithm() string { return string(v.inner.Key().Algorithm) }

func (v joseVerifier) Verify(signingInput, signature []byte) error {
	return v.inner.Verify(signingInput, signature)
}

// joseClock adapts an authz.Clock to the sdjwt.Clock.
//
// The two interfaces are structurally identical, so a concrete clock satisfies
// both without help. This exists for the case where a caller holds only the
// authz.Clock interface value, which does not implicitly convert.
type joseClock struct{ inner authz.Clock }

func (c joseClock) Now() time.Time { return c.inner.Now() }

// Compile-time proof that the bridges are complete. A missing method here is a
// build failure rather than a runtime surprise inside pkg/sdjwt.
var (
	_ sdjwt.Signer   = joseSigner{}
	_ sdjwt.Verifier = joseVerifier{}
	_ sdjwt.Clock    = joseClock{}
)
