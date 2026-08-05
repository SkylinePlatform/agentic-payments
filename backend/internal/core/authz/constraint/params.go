package constraint

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// decodeParams fills target from a constraint's open params.
//
// It re-encodes and decodes rather than reaching into the map, which sounds
// wasteful and buys two things worth more than the allocation. The typed struct
// gets the same field names and the same tolerance the wire format has, because
// it is the same decoder; and numbers survive, because the decoder is put into
// UseNumber mode. A map that has been through encoding/json holds float64 for
// every number, and an amount in minor units large enough to matter would come
// back wrong — the same reason pkg/sdjwt decodes claims that way.
//
// Unknown fields are refused. A constraint carrying a parameter this verifier
// does not understand is one whose meaning may not be what the reader thinks,
// and quietly ignoring the extra field is the same failure as quietly ignoring
// an unknown constraint type, one level down.
func decodeParams(params map[string]any, target any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode params: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return nil
}

// minorUnits reads an integer amount without going through float64.
type minorUnits int64

func (m *minorUnits) UnmarshalJSON(b []byte) error {
	// json.Number accepts a quoted string without complaint — "20000"
	// unmarshals into one and reads back as 20000. The contract says
	// integer, and letting a string through would mean two wire
	// representations of one claim, with the looser one arriving from
	// whichever encoder was sloppiest.
	if len(b) > 0 && b[0] == '"' {
		return fmt.Errorf("amount must be a number, not the string %s", b)
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("amount must be a number: %w", err)
	}
	v, err := n.Int64()
	if err != nil {
		return fmt.Errorf("amount %s is not a whole number of minor units: %w", n, err)
	}
	*m = minorUnits(v)
	return nil
}

// normaliseSet lowercases, trims and sorts a set of labels, and reports whether
// anything survived.
//
// Case folding is a decision rather than tidiness: an allow-list written
// "Flights" by the interpreter and sent "flights" by the merchant would
// otherwise refuse a purchase the user approved, and the failure would look
// like a policy decision rather than a spelling one. It is the ASCII fold,
// which is enough for the identifier-shaped labels these carry and does not
// pretend to handle natural language.
func normaliseSet(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// normalise is normaliseSet's rule for a single value.
func normalise(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// sortTypes orders constraint types for stable output.
func sortTypes(in []Type) {
	slices.SortFunc(in, func(a, b Type) int { return strings.Compare(string(a), string(b)) })
}
