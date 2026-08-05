package ap2

import (
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// JOSESignerFor exposes the signer bridge to this package's black-box tests.
//
// The bridge stays unexported in the package proper: within ap2 the checkout,
// payment and receipt paths share it directly, and nothing outside needs to
// build an SD-JWT that this package would not build itself. A test does — it
// has to mint the malformed mandates a hostile issuer could send, which the
// exported constructors correctly refuse to produce.
func JOSESignerFor(s authz.Signer) sdjwt.Signer { return joseSigner{s} }

// JOSEVerifierFor is the same, for the verifying half.
func JOSEVerifierFor(v authz.Verifier) sdjwt.Verifier { return joseVerifier{v} }
