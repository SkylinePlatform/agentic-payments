package ap2

import (
	"encoding/json"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// KeyBindingPolicy is a verifier's stance on proof of possession.
//
// It exists because the two options structs could not previously express one at
// all, which meant every AP2 verification ran with the same implicit policy:
// no holder key, not required, and therefore a Key Binding JWT that arrives is
// ignored. That is a defensible default for a general SD-JWT library and a poor
// one to have no alternative to, because Human Not Present is the mode where
// the agent's proof of possession is the point — the user signed an open mandate
// naming the agent's key in cnf, and the closed mandate's key binding is what
// ties the presentation to that key rather than to whoever is holding a copy.
//
// The zero value keeps the old behaviour, and says so rather than leaving it to
// be discovered: with HolderKey nil and Required false, a presentation carrying
// a KB-JWT verifies with the proof unexamined. RFC 9901 permits that — a
// Verifier has nothing to conclude from a proof it did not ask for — but it is
// only safe as a decision, which is what naming the type makes it.
//
// Whether the AP2 flows should refuse an unasked-for proof outright is
// answered now, and the answer is not a policy on this type at all. #12
// modelled Human Not Present as a delegation chain rather than as a
// standalone presentation carrying an optional KB-JWT: the agent signs the
// delegating hop with the key the open mandate endorsed in cnf, so
// sdjwt.VerifyChain's own key-binding check already ties the presentation to
// that key, and a KeyBindingPolicy layered on top would ask for proof of the
// same fact twice. That is why CredentialProviderRules.VerifyPayment in
// rules.go leaves this policy at its zero value rather than filling it in —
// there is no agent key in Human Present mode for a KB-JWT to prove
// possession of, so the zero value is what a standalone presentation was
// always going to need. AuthorisePaymentChain (chain.go, and
// CredentialProviderRules' wrapper in rules.go) is where the Human Not
// Present half of the question actually lives: the chain's own key binding
// is the proof, checked there, never here.
type KeyBindingPolicy struct {
	// HolderKey turns the cnf claim of the verified payload into the Verifier
	// for the Key Binding JWT. The argument is the cnf value re-encoded as JSON,
	// typically {"jwk":{...}} — resolving it to a key, and deciding on what
	// grounds to trust it, is the caller's, because this package holds no key
	// material.
	//
	// Required when Required is set. Nil is what makes the whole policy inert.
	HolderKey func(cnf json.RawMessage) (authz.Verifier, error)

	// Required makes a Key Binding JWT mandatory.
	//
	// RFC 9901 §7.3 step 1: this comes from policy and never from whether the
	// presenter supplied one. A verifier that checks key binding only when it is
	// present does not check it.
	Required bool

	// Audience is the value the KB-JWT's aud claim must carry: this verifier's
	// own identifier. Required whenever key binding is checked, and empty is not
	// a way to skip the check — verification fails instead, because "" == ""
	// would pass while proving nothing.
	Audience string

	// Nonce is the value the KB-JWT's nonce claim must carry, as issued to the
	// presenter for this transaction. Required on the same terms as Audience and
	// for the same reason.
	Nonce string

	// MaxAge is how far the KB-JWT's iat may sit from now, in either direction.
	// Zero leaves replay protection to the nonce alone; there is no defensible
	// default, because what counts as fresh belongs to the surrounding flow.
	MaxAge time.Duration
}

// apply writes the policy into the options pkg/sdjwt takes.
//
// The resolver is wrapped rather than passed, because the two layers speak
// different vocabularies — core's authz.Verifier on this side, sdjwt.Verifier on
// the other — and JOSEVerifier is the bridge. It preserves nil, so a caller's
// resolver that returns a nil Verifier with a nil error produces a nil on the
// far side too, and pkg/sdjwt refuses it with ErrKeyBindingInvalid rather than
// panicking on a wrapper around nothing. See jose.go.
func (p KeyBindingPolicy) apply(o *sdjwt.Options) {
	if p.HolderKey != nil {
		o.HolderKey = func(cnf json.RawMessage) (sdjwt.Verifier, error) {
			v, err := p.HolderKey(cnf)
			if err != nil {
				return nil, err
			}
			return JOSEVerifier(v), nil
		}
	}
	o.RequireKeyBinding = p.Required
	o.Audience = p.Audience
	o.Nonce = p.Nonce
	o.MaxKeyBindingAge = p.MaxAge
}
