package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Heroes are the offers a scripted sentence goes looking for, and the four this
// program copies through untouched.
//
// They are the engine of the demonstration rather than stock. The flight opens
// at $240.00 against a cap of $200.00 and steps to $189.00, which *is* the Human
// Not Present beat — a purchase refused at $210.00 and completed at $189.00 —
// and the bicycle is the same shape one vertical over. The concert and the
// ladders sit inside their cap the whole way and are bought at once, because
// issue #198 read their sentences as instructions rather than as conditions.
//
// Two of the four are named character for character by the constraint sets in
// backend/internal/agent/interpret/scenarios.go, which approve one specific
// object rather than a class of object.
//
// **They are named here and not restated.** This program copies each one out of
// the file it is rewriting, as the raw JSON it found — so a re-run cannot move a
// price, a title or even a key order, and there is no second copy of the four
// numbers every diagram of a real transaction reuses. If one of these
// identifiers ever left the file, the rewrite refuses rather than dropping the
// beat quietly.
var Heroes = []string{
	"route:BEG-PMI",
	"gtin:05012345678900",
	"event:vlado-georgijev-2026-11-14",
	"gtin:05014477390221",
}

// file is deploy/catalogue.json, in exactly the shape
// merchant.CatalogueFile parses.
//
// A second statement of that struct, for the reason the package comment gives:
// Go's internal rule puts the real one out of reach, and a generator that
// imported the loader would be able to bless its own output.
//
// Comment and Offers are both json.RawMessage because both are text this
// program is a custodian of rather than an author of. The comment is prose a
// person owns; the offers include the four heroes, which have to come back out
// exactly as they went in.
type file struct {
	Comment  json.RawMessage `json:"$comment,omitempty"`
	Currency string          `json:"currency"`
	Merchant struct {
		Category string `json:"category"`
	} `json:"merchant"`
	Offers []json.RawMessage `json:"offers"`
}

// entry is one derived thing for sale. The field order is the order it is
// written in, which is the order the file already used.
type entry struct {
	ID          string            `json:"id"`
	Category    string            `json:"category"`
	Attributes  map[string]string `json:"attributes"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	ImageURL    string            `json:"image_url"`
	Retailer    string            `json:"retailer"`
	Prices      []int             `json:"prices"`
	Scenario    scenario          `json:"scenario"`
}

// scenario is the offer declaring what it is for: the bound the prompt going
// looking for it places on the price, and what that prompt is then supposed to
// do. The loader's Found type is the authority on the three spellings.
type scenario struct {
	Cap   int    `json:"cap"`
	Found string `json:"found"`
}

// The three values Found may take, as this program spells them.
const (
	foundAlways         = "always"
	foundAtTheLastPrice = "at-the-last-price"
	foundNever          = "never"
)

func readCatalogue(path string) (*file, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// rewrite returns the file with every derived offer replaced and the heroes kept
// exactly as they were.
//
// The heroes come first and in the order [Heroes] names them, which is the order
// the file already had. Ordering inside the file is presentational — the
// merchant sorts by identifier when it builds a catalogue — but a diff is not,
// and keeping the four that matter at the top is what stops a re-run's diff
// burying them under sixty rows of stock.
func (f *file) rewrite(derived []entry) (*file, error) {
	byID := make(map[string]json.RawMessage, len(f.Offers))
	for _, raw := range f.Offers {
		var named struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &named); err != nil {
			return nil, fmt.Errorf("the file lists an offer this program cannot read: %w", err)
		}
		byID[named.ID] = raw
	}

	out := *f
	out.Offers = make([]json.RawMessage, 0, len(Heroes)+len(derived))
	for _, id := range Heroes {
		hero, listed := byID[id]
		if !listed {
			// Refused rather than skipped, on the reasoning Validate uses for a
			// claim it does not understand: a hero that has silently left the
			// file is a beat of the demonstration that has silently left with
			// it, and writing the file anyway would replace a loud failure here
			// with a quiet one in front of whoever was about to take a
			// screenshot.
			return nil, fmt.Errorf("the file no longer lists %s, which is one of the four offers "+
				"a scripted sentence goes looking for; rewriting it would drop that beat", id)
		}
		out.Offers = append(out.Offers, hero)
	}

	for _, o := range derived {
		if slices.Contains(Heroes, o.ID) {
			return nil, fmt.Errorf("derived an offer under %s, which is a hero's identifier", o.ID)
		}
		encoded, err := json.Marshal(o)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", o.ID, err)
		}
		out.Offers = append(out.Offers, encoded)
	}
	return &out, nil
}

// render is the file as it will be written.
//
// Split out from writing it so that a test can compare two renderings without
// either of them touching the tree, which is what determinism is asserted
// against.
func render(f *file) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The prose block names `item.attr.<name>` and
	// `GET /checkout?from=BEG&to=PMI`, and the encoder's default would turn both
	// into < and & — a whole-comment diff on a run that changed
	// nothing about it.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return collapsePrices(buf.Bytes()), nil
}

func writeCatalogue(path string, f *file) error {
	rendered, err := render(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// priceArray matches one offer's price sequence, however the encoder indented
// it.
var priceArray = regexp.MustCompile(`(?s)"prices": \[[^]]*]`)

// collapsePrices puts each price sequence back on one line.
//
// json.Indent gives every array element a line of its own, which for a
// sixty-offer catalogue is four hundred lines carrying one number each. The
// sequence is the one field of an offer that is read as a *sequence* — the whole
// argument for it is that 24000 gives way to 21000 and then to 18900 — so it is
// the one worth keeping on a line, and it is how the file was written before it
// was generated.
func collapsePrices(b []byte) []byte {
	return priceArray.ReplaceAllFunc(b, func(match []byte) []byte {
		inner := match[bytes.IndexByte(match, '[')+1 : len(match)-1]
		numbers := strings.FieldsFunc(string(inner), func(r rune) bool {
			return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
		})
		return fmt.Appendf(nil, `"prices": [%s]`, strings.Join(numbers, ", "))
	})
}
