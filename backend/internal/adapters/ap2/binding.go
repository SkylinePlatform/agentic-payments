package ap2

import (
	"crypto/subtle"
	"fmt"

	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// checkoutHash computes the value of the checkout_hash claim: the base64url
// digest of the Checkout JWT's compact serialisation.
//
// Three details, each of which is a way to get this wrong:
//
// The input is the JWT string as it travels, not the bytes it decodes to and
// not the checkout object inside it. That is what removes any need to
// canonicalise the merchant's JSON — the merchant's own serialisation is what
// was signed, and it is what is hashed.
//
// The algorithm is not a constant. AP2 requires the same hash the SD-JWT uses,
// which is whatever _sd_alg names, defaulting to sha-256 when the claim is
// absent. Hardcoding sha-256 here would produce a mandate that verifies today
// and stops verifying the first time anybody issues with sha-384 — and it would
// fail as a hash mismatch, which reads as tampering rather than as a bug.
//
// The output carries no algorithm prefix. It is bare base64url, not
// "sha-256:…". The prefixed form appears nowhere in the specification.
func checkoutHash(alg sdjwt.HashAlg, checkoutJWT string) (string, error) {
	return alg.Digest(checkoutJWT)
}

// verifyBinding recomputes the hash of checkout and compares it to claimed.
//
// It never compares the claim against itself. The whole reason this function
// takes the checkout separately is that the claim is the thing being checked:
// a verifier that reads checkout_hash out of the mandate and believes it has
// established nothing an attacker could not have written.
//
// The comparison is constant-time. These are digests of public documents and
// nothing secret is being compared, so this buys no confidentiality — it is
// here because a variable-time compare on a security decision is a habit worth
// not having, and the cost is nil.
func verifyBinding(alg sdjwt.HashAlg, claimed, checkout string) error {
	recomputed, err := checkoutHash(alg, checkout)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(recomputed), []byte(claimed)) != 1 {
		return fmt.Errorf("%w: the checkout presented hashes to %s, the mandate authorises %s",
			ErrCheckoutHashMismatch, abbreviate(recomputed), abbreviate(claimed))
	}
	return nil
}

// abbreviate shortens a digest for an error message. The full value is of no
// use to a human reading a log line, and a rejection receipt carries the error
// code rather than this text.
func abbreviate(digest string) string {
	const enough = 12
	if len(digest) <= enough {
		return digest
	}
	return digest[:enough] + "…"
}
