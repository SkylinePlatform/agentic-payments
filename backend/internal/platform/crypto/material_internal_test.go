package crypto

import (
	"testing"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
)

// TestParsedKeysCannotBeWidenedIntoSigners pins the guarantee signingMaterial's
// doc comment claims.
//
// Go interfaces are structural, so this is not something the compiler asserts
// on its own: if ecKey or okpKey ever grows a sign method — most likely by
// someone merging the public and private types back together and making the
// private field nil-able — every key parsed out of a counterparty's JWK Set
// would start satisfying signingMaterial. A type assertion would then succeed
// and panic inside ecdsa.Sign or ed25519.Sign rather than return an error.
//
// The test is cheap and the comment is load-bearing for the next reader, so the
// claim is checked rather than asserted in prose.
func TestParsedKeysCannotBeWidenedIntoSigners(t *testing.T) {
	t.Parallel()

	for _, alg := range []authz.Algorithm{authz.ES256, authz.ES384, authz.ES512, authz.EdDSA} {
		t.Run(string(alg), func(t *testing.T) {
			t.Parallel()

			// A generated key signs.
			generated, err := generate(alg)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}

			// The same key, round-tripped through publication, does not.
			parsed, _, err := parseJWK(publish(generated, "kid"))
			if err != nil {
				t.Fatalf("parseJWK: %v", err)
			}
			if _, widened := parsed.(signingMaterial); widened {
				t.Fatalf("a key parsed from a JWK Set satisfies signingMaterial; "+
					"%T must not have a sign method", parsed)
			}

			// And it still verifies, so the split cost nothing.
			payload := []byte("payload")
			sig, err := generated.sign(payload)
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			if err := parsed.verify(payload, sig); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}
}

// TestSigningKeysCannotBeBuiltWithoutAPrivateKey covers the other half: the
// signing types derive their public half from the private one, so there is no
// path to a signing key with nothing to sign with.
func TestSigningKeysCannotBeBuiltWithoutAPrivateKey(t *testing.T) {
	t.Parallel()

	ec, err := paramsFor(authz.ES256)
	if err != nil {
		t.Fatalf("paramsFor: %v", err)
	}
	if _, err := newECSigningKey(ec, nil); err == nil {
		t.Error("newECSigningKey accepted a nil private key")
	}

	okp, err := paramsFor(authz.EdDSA)
	if err != nil {
		t.Fatalf("paramsFor: %v", err)
	}
	for _, short := range [][]byte{nil, make([]byte, 32), make([]byte, 63)} {
		if _, err := newOKPSigningKey(okp, short); err == nil {
			t.Errorf("newOKPSigningKey accepted a %d-byte private key", len(short))
		}
	}
}
