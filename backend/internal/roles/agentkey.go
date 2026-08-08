package roles

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/crypto"
)

// The two directions an agent key travels, and the only two this project needs.
//
// An open mandate binds to the agent it authorises by carrying that agent's
// public key — the canonical model calls it agent_key and AP2 puts it in the
// RFC 7800 cnf claim. Putting a key into one and reading a key back out of one
// are separate problems with separate callers: an agent asking its own key
// store what to be endorsed as, and a verifier asking what the cnf it has just
// read endorses.
//
// Neither function names a crypto/ecdsa, crypto/ed25519 or crypto/rsa type, and
// neither needs to. generated.PublicKey's JSON tags are JWK member names —
// kty, kid and alg from RFC 7517 §4, and crv, x, y, n and e from RFC 7518 §6 —
// so both directions are decoding rather than translation, and the key material
// itself stays where depguard's key-material-containment rule keeps it: inside
// internal/platform/crypto, behind an authz.Verifier that can check a signature
// and cannot yield a key.
//
// The tags are a subset of the members RFC 7517 defines, not all of them. What
// that costs is set out on PublicKey below.

// PublicKey reads the single key a publisher publishes, in the canonical
// model's own form.
//
// This is what an agent hands a Trusted Surface to be endorsed by: the surface
// writes it into the open mandate's agent_key and the adapter encodes it as
// cnf, so what the user signs is a copy of the key rather than a name for one.
// That is why this returns a generated.PublicKey where Peer.Only — the same
// question asked of a counterparty over HTTP — returns an authz.Verifier. A
// relying party wants something that can check a signature; a party being
// endorsed has to hand over the key itself, because the endorsement travels to
// verifiers that have never met the agent.
//
// It refuses a set that is not exactly one key, on the reasoning Peer.Only
// already sets out: a mock role signs with one key, so "this role's key" is
// unambiguous and a caller has no kid to choose with. Picking one of several
// would be worse here than there. Peer.Only picking wrong produces a signature
// that does not verify, which points at the key; this picking wrong produces a
// mandate endorsing a key the agent will not sign with, which fails an hour
// later at a verifier that can say nothing more useful than that the delegation
// was signed by an unendorsed key.
//
// Its caller is the agent: cmd/agent reads its own key set through this before
// asking the Trusted Surface to authorise a watch, and AgentKey below reads the
// same key back out of cnf at each verifier. That round trip is the whole
// contract between the two functions, and it is exercised end to end by
// internal/agent's Human Not Present tests and by the ones in this package,
// which endorse a key the test's agent then really signs a delegation with.
func PublicKey(ctx context.Context, keys authz.KeySetPublisher) (generated.PublicKey, error) {
	var zero generated.PublicKey
	if keys == nil {
		return zero, errors.New("roles: no key set to read a public key from")
	}

	document, err := keys.JWKS(ctx)
	if err != nil {
		return zero, fmt.Errorf("publishing the key set: %w", err)
	}

	// generated.PublicKey models the material and not the usage rules, so this
	// decode drops "use" — the one member beyond its fields that
	// crypto.publish writes, always as "sig". encoding/json drops what a
	// struct has no field for.
	//
	// That is a real gap rather than a tidy omission, and worth stating where
	// somebody reasoning from this code will meet it. crypto.ParseJWKS treats
	// "use" as a refusal criterion, not a hint: it skips a key published for
	// anything but signing. So a P-256 key marked "use":"enc" is refused by
	// AgentKey when it arrives in a cnf directly, and accepted through
	// PublicKey → cnf → AgentKey, because by then the member is gone. Nothing
	// in this repository publishes such a key — crypto.publish always writes
	// "sig" — so the two verdicts cannot disagree in practice today. Closing it
	// properly means the canonical model carrying the usage rules, which is a
	// contracts/ change and not this function's to make.
	//
	// It does not carry "key_ops": crypto.publish never emits one.
	var set struct {
		Keys []generated.PublicKey `json:"keys"`
	}
	if err := json.Unmarshal(document, &set); err != nil {
		return zero, fmt.Errorf("reading the published key set: %w", err)
	}

	switch len(set.Keys) {
	case 1:
		return set.Keys[0], nil
	case 0:
		return zero, errors.New("roles: this key set publishes no keys, so there is nothing to endorse")
	default:
		return zero, fmt.Errorf(
			"roles: this key set publishes %d keys and an open mandate endorses exactly one",
			len(set.Keys))
	}
}

// AgentKey resolves the key an open mandate's cnf claim endorses.
//
// It is PublicKey's inverse and the verifying side's half of the delegation:
// the key a closed mandate's delegating hop is checked against is the one the
// user's own signature already covers. Its shape is fixed by the fields it is
// written for — ap2.MerchantRules.AgentKey and
// ap2.CredentialProviderRules.AgentKey, which cmd/merchant, cmd/credprovider
// and cmd/mpp all set to this function — which is why it takes no context and
// cannot grow one. See the call to Resolve below for why that costs nothing.
//
// cnf arrives as pkg/sdjwt hands it over: the JSON encoding of the whole
// confirmation claim, {"jwk": {...}}, taken from the *processed* payload, so a
// cnf that travelled as a disclosure reads the same as one in the clear. The
// jwk member is lifted out and re-wrapped as a one-entry JWK Set because
// crypto.ParseJWKS is the only parser in this module that turns published key
// material into something that can verify — and it is the parser worth reaching
// for rather than a shorter one, because it is the one that refuses a point off
// its curve, a coordinate of the wrong width, an alg that contradicts the key
// type, and a "d" member. An agent key arriving with private material in it is
// a key leak at whoever minted it, and this is a place it can be caught.
//
// What this deliberately does not check is whether the key names enough
// material to endorse anybody. ap2.wrapAgentKey runs authz.UsableKey on the
// same cnf before calling this, and reports the failure as
// authz.ErrAgentKeyMismatch — the mandate is well formed and the key is the
// problem, a distinction only that layer can draw.
func AgentKey(cnf json.RawMessage) (authz.Verifier, error) {
	var confirmation struct {
		JWK json.RawMessage `json:"jwk"`
	}
	if err := json.Unmarshal(cnf, &confirmation); err != nil {
		return nil, fmt.Errorf("roles: reading the cnf claim: %w", err)
	}
	if len(confirmation.JWK) == 0 {
		return nil, errors.New("roles: cnf carries no jwk member, so it endorses no key")
	}

	document, err := json.Marshal(struct {
		Keys []json.RawMessage `json:"keys"`
	}{Keys: []json.RawMessage{confirmation.JWK}})
	if err != nil {
		return nil, fmt.Errorf("roles: re-wrapping the endorsed key as a key set: %w", err)
	}

	set, err := crypto.ParseJWKS(document)
	if err != nil {
		return nil, fmt.Errorf("roles: reading the key cnf endorses: %w", err)
	}

	// The document has exactly one entry, so this is 1 or 0 and nothing else.
	// Zero means ParseJWKS skipped the entry rather than failing on it, which
	// it does for a key type or curve this implementation cannot verify with,
	// and for a key published for something other than signing.
	refs := set.Keys()
	if len(refs) == 0 {
		return nil, fmt.Errorf(
			"%w: cnf endorses a key this implementation cannot verify a signature with",
			authz.ErrUnsupportedAlgorithm)
	}

	// authz.KeyResolver takes a context because the port is also implemented
	// over a remote directory. This implementation is a map parsed three lines
	// ago and reads the context only to return early when it is already
	// cancelled, so there is no work here for a caller's deadline to cut short
	// — which is what lets the signature above leave the context out.
	return set.Resolve(context.Background(), refs[0])
}
