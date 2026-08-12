package shop

import (
	"context"
	_ "embed"
	"fmt"
	"maps"
)

// dummyJSONSnapshot is what DummyJSON answered on 2026-08-12. See
// data/PROVENANCE.md for the exact request and how to take it again.
//
//go:embed data/dummyjson-products.json
var dummyJSONSnapshot []byte

// Snapshot is a Fetcher that answers from a recording instead of from a shop.
//
// # It is a fixture, and it is not in a _test.go file on purpose
//
// The precedent is interpret.ScriptedInterpreter, and the reason is the same
// one AGENTS.md gives there: a type declared in a _test.go file is reachable
// only from its own package's test binary, and the package that needs this one
// is merchant, one directory up. Everything the merchant does with a live
// catalogue — merging it with the file, validating the mixed set, drawing a
// mark for every fetched offer, searching across both halves — has to be
// testable, and hard rule 4 forbids reaching the shop to do it.
//
// It is also not a mock. It runs the real decoder over real recorded bytes, so
// a change to decodeDummyJSON that broke the shop's actual response shape fails
// here rather than passing against a hand-written struct literal. That is the
// distinction AGENTS.md draws between a double that records a call and one that
// computes an answer, and this is firmly the second.
//
// # Nothing in production may name it
//
// A demonstration that said "live" and served a recording would be the
// screenshot nobody can attribute that -interpreter auto's package doc argues
// about at length, one role along. cmd/merchant's -catalogue-live therefore
// accepts exactly one value, `dummyjson`, and refuses every other string
// including this one — TestOnlyTheRealShopCanBeAskedForALiveCatalogue is what
// says so.
type Snapshot struct {
	products []Product
	name     string
}

// NewSnapshot returns a Fetcher over the recorded DummyJSON response.
//
// It decodes at construction rather than at Fetch, so a recording this package
// can no longer read is a failure a test names immediately instead of one that
// surfaces halfway through building a catalogue.
func NewSnapshot() (*Snapshot, error) {
	products, err := decodeDummyJSON(dummyJSONSnapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: the recorded snapshot in data/: %w", ErrFetch, err)
	}
	return &Snapshot{products: products, name: "a recording of " + DummyJSONHost}, nil
}

// NewFixture returns a Fetcher over products a caller made up.
//
// It exists for the cases a recording cannot reach — a shop quoting the wrong
// currency, a shop whose product collides with an offer deploy/catalogue.json
// ships — each of which is a rule the merchant enforces and none of which
// DummyJSON will ever produce on demand. name is what the merchant would print,
// so a test can assert the startup line names its source.
func NewFixture(name string, products ...Product) *Snapshot {
	return &Snapshot{products: products, name: name}
}

// Name is what the merchant's startup line calls this source.
func (s *Snapshot) Name() string { return s.name }

// Fetch returns the recording. The context is accepted and unused: this reads
// nothing that can be slow, and a Fetcher whose second implementation dropped
// the parameter would not satisfy the interface.
//
// The copy is not ceremony. Attributes is a map, so handing out the stored
// Product would let one caller's edit change what a later Fetch returns —
// the same escape merchant.Offer.copy exists to close.
func (s *Snapshot) Fetch(context.Context) ([]Product, error) {
	out := make([]Product, 0, len(s.products))
	for _, p := range s.products {
		p.Attributes = maps.Clone(p.Attributes)
		out = append(out, p)
	}
	return out, nil
}
