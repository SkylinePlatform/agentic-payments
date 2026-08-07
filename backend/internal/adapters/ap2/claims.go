package ap2

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// AP2's claim names, and the translation between them and the canonical model.
//
// They are here and nowhere else, because this is the boundary they exist at.
// The canonical model calls the merchant's document `checkout` and its
// timestamps `issued_at` and `expires_at`; AP2 calls them `checkout_jwt`, `iat`
// and `exp`, the last two as epoch seconds. Neither naming is more correct —
// contracts/README.md records why the difference is an encoding detail and
// belongs on this side of the line rather than in the schema.
const (
	// Shared by both closed mandates.
	claimIssuedAt  = "iat"
	claimExpiresAt = "exp"

	// Closed Checkout Mandate.
	claimCheckoutJWT  = "checkout_jwt"
	claimCheckoutHash = "checkout_hash"

	// Closed Payment Mandate. Note claimTransactionID: AP2 names the claim
	// `transaction_id` while defining it as the hash of the checkout — the same
	// value the Checkout Mandate calls `checkout_hash`. Two names, one fact, and
	// the canonical model keeps one of them.
	claimTransactionID     = "transaction_id"
	claimPayee             = "payee"
	claimPaymentAmount     = "payment_amount"
	claimPaymentInstrument = "payment_instrument"
	claimExecutionDate     = "execution_date"
	claimRiskData          = "risk_data"

	// Open mandates, both of them. cnf is RFC 7800's confirmation claim; open.go
	// carries the agent's key inside it rather than a reference to one — see
	// encodeCnf and decodeCnf. constraints is AP2's own name for the limits the
	// user approved.
	claimCnf         = "cnf"
	claimConstraints = "constraints"
)

// wireNames maps each canonical type to the AP2 claim its fields travel under.
//
// Keyed by type rather than flat, and that is the whole point: `checkout_hash`
// is the claim `checkout_hash` on a Checkout Mandate and the claim
// `transaction_id` on a Payment Mandate. One canonical name, two wire answers,
// so a single flat table would have to give one of them wrongly.
//
// The job this table does is easy to miss. generated.Disclosable answers in
// canonical names and the Blinder blinds the wire payload, so handing it
// "risk_data" for a mandate whose wire name differed would silently blind
// nothing at all — and the mandate would be issued with the user's device
// signals fully visible, having declared them withholdable. A missing entry is
// therefore a privacy failure that no test of the happy path would catch, which
// is why blindPaths refuses an unmapped path rather than passing it through.
var wireNames = map[string]map[string]string{
	"CheckoutMandate": {
		"checkout":      claimCheckoutJWT,
		"checkout_hash": claimCheckoutHash,
		"issued_at":     claimIssuedAt,
		"expires_at":    claimExpiresAt,
	},
	"PaymentMandate": {
		"checkout_hash":      claimTransactionID,
		"payee":              claimPayee,
		"payment_amount":     claimPaymentAmount,
		"payment_instrument": claimPaymentInstrument,
		"execution_date":     claimExecutionDate,
		"risk_data":          claimRiskData,
		"issued_at":          claimIssuedAt,
		"expires_at":         claimExpiresAt,
	},

	// The two open mandates. Both declare exactly one withholdable path,
	// "constraints[]" — contracts/authz/*_open.json put x-disclosable-items on
	// the constraints array, so disclosure.go's generator names the array
	// element-wise, brackets included, rather than as a bare "constraints". The
	// key here has to be that same string: blindPaths below matches a
	// generated.Disclosable path against this table verbatim, with no
	// suffix-stripping of its own, so a key missing the brackets would make
	// every future IssueOpenCheckout and IssueOpenPayment call fail at
	// blindPaths rather than issue a mandate that discloses too much — the
	// failure is closed, but this is where it is closed correctly instead of by
	// accident. The wire side carries the same suffix for the same reason
	// Blind's own path syntax gives: "constraints[]" tells it to blind each
	// constraint individually, which is the unit doc.go and scenarios.go both
	// describe disclosure as being.
	//
	// The other fields below are not consulted by blindPaths — nothing here is
	// withholdable but the constraints — and are recorded anyway, because this
	// table's job, per the comment above it, is naming the wire claim for every
	// field of the type, not only the ones a caller happens to need today.
	"OpenCheckoutMandate": {
		"agent_key":     claimCnf,
		"constraints[]": claimConstraints + "[]",
		"issued_at":     claimIssuedAt,
		"expires_at":    claimExpiresAt,
	},
	"OpenPaymentMandate": {
		"agent_key":          claimCnf,
		"constraints[]":      claimConstraints + "[]",
		"payee":              claimPayee,
		"payment_amount":     claimPaymentAmount,
		"payment_instrument": claimPaymentInstrument,
		"execution_date":     claimExecutionDate,
		"issued_at":          claimIssuedAt,
		"expires_at":         claimExpiresAt,
	},
}

// blindPaths translates the canonical withholdable paths of a type into the
// wire paths the Blinder takes.
func blindPaths(typeName string) ([]string, error) {
	names, ok := wireNames[typeName]
	if !ok {
		return nil, fmt.Errorf(
			"%w: this adapter has no wire names for %s",
			ErrMandateMalformed, typeName)
	}

	canonical := generated.Disclosable(typeName)
	out := make([]string, 0, len(canonical))
	for _, path := range canonical {
		wire, ok := names[path]
		if !ok {
			return nil, fmt.Errorf(
				"%w: %s declares %q withholdable and this adapter has no wire name for it",
				ErrMandateMalformed, typeName, path)
		}
		out = append(out, wire)
	}
	return out, nil
}

// presentPaths narrows declared withholdable paths to the claims that are
// actually there.
//
// The two lists answer different questions. blindPaths says what this mandate
// type is *allowed* to withhold; this says what this particular mandate *has*.
// An optional claim that was never set has nothing to blind, and the Blinder
// refuses a path it cannot find — so without this, a Payment Mandate carrying
// no risk signals would fail to issue at all.
func presentPaths(claims map[string]any, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, ok := claims[strings.TrimSuffix(path, "[]")]; ok {
			out = append(out, path)
		}
	}
	return out
}

func requireString(claims map[string]any, name string) (string, error) {
	raw, ok := claims[name]
	if !ok {
		return "", fmt.Errorf("%w: no %s claim", ErrMandateMalformed, name)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string, got %T", ErrMandateMalformed, name, raw)
	}
	if s == "" {
		return "", fmt.Errorf("%w: %s is empty", ErrMandateMalformed, name)
	}
	return s, nil
}

// optionalString reads a claim that may legitimately be absent, either because
// AP2 marks it optional or because a presentation withheld it.
func optionalString(claims map[string]any, name string) (*string, error) {
	raw, ok := claims[name]
	if !ok {
		return nil, nil
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("%w: %s must be a string, got %T", ErrMandateMalformed, name, raw)
	}
	return &s, nil
}

// remarshal moves a verified claim into its canonical Go type through JSON.
//
// The claim arrives as the map the JSON decoder produced, and the canonical
// type knows its own shape — including the UnmarshalJSON the generator writes
// to enforce required fields. Going back through JSON is what lets that
// enforcement run, so a payee missing its id is refused here rather than
// carried forward as a zero value that reads like a merchant called "".
func remarshal(claims map[string]any, name string, dst any) error {
	raw, ok := claims[name]
	if !ok {
		return fmt.Errorf("%w: no %s claim", ErrMandateMalformed, name)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("%w: %s could not be re-encoded: %w", ErrMandateMalformed, name, err)
	}
	if err := json.Unmarshal(encoded, dst); err != nil {
		return fmt.Errorf("%w: %s is not a valid %s: %w", ErrMandateMalformed, name, name, err)
	}
	return nil
}

// epochSeconds reads a NumericDate.
//
// pkg/sdjwt decodes with UseNumber, so a JSON number arrives as json.Number
// and not as a float64 — which matters beyond tidiness. A float64 silently
// loses precision above 2^53, and an exp that decodes to a different second
// from the one that was signed is an expiry check answering about a different
// instant. json.Number.Int64 refuses a fractional or oversized value outright,
// so the refusal happens here rather than being carried forward as a wrong
// answer. The other cases are for payloads this package constructs itself,
// which never pass through a JSON decoder.
func epochSeconds(name string, raw any) (int64, error) {
	malformed := func(format string, args ...any) (int64, error) {
		return 0, fmt.Errorf("%w: %s "+format, append([]any{ErrMandateMalformed, name}, args...)...)
	}
	switch v := raw.(type) {
	case json.Number:
		secs, err := v.Int64()
		if err != nil {
			return malformed("is %s, not a whole number of seconds in range", v.String())
		}
		return secs, nil
	case float64:
		secs := int64(v)
		if float64(secs) != v {
			return malformed("is %v, not a whole number of seconds", v)
		}
		return secs, nil
	case int64:
		return v, nil
	case int:
		return int64(v), nil
	default:
		return malformed("must be a number, got %T", raw)
	}
}

// epochTime reads one NumericDate claim into a canonical timestamp, leaving dst
// untouched when the claim is absent. Every timestamp here is optional, on the
// mandates because AP2 marks iat and exp optional and on a receipt because it
// carries no expiry at all.
func epochTime(claims map[string]any, name string, dst **time.Time) error {
	raw, ok := claims[name]
	if !ok {
		return nil
	}
	secs, err := epochSeconds(name, raw)
	if err != nil {
		return err
	}
	t := time.Unix(secs, 0).UTC()
	*dst = &t
	return nil
}

// timestamps reads the iat and exp claims into the canonical fields, which both
// closed mandates carry identically. A receipt reads iat on its own — it has no
// expiry, being a statement about a moment that has already passed.
func timestamps(claims map[string]any, issuedAt, expiresAt **time.Time) error {
	if err := epochTime(claims, claimIssuedAt, issuedAt); err != nil {
		return err
	}
	return epochTime(claims, claimExpiresAt, expiresAt)
}
