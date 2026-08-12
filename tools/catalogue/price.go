package main

import (
	"crypto/sha256"
	"encoding/binary"
)

// band is what a shelf charges, in the file's currency and its minor unit.
//
// low ends in 900 in every shelf, and every price is low plus a whole number of
// thousands, so a derived price always reads as $x.99 — which is what a shop
// looks like and what a screenshot of one has to survive.
type band struct {
	low  int
	high int
}

// price is what an offer costs over time, and what it claims a search for it
// will do.
//
// # Why this is derived from the identifier rather than drawn
//
// A catalogue that filled differently each run would make issue #158's sentence
// — "the refusal at $210 happens on every run, and a test says so rather than a
// reader hoping" — unwritable one shelf over: every derived offer states a
// `scenario` block, and the merchant suite drives each one's own prompt against
// its own prices. A drawn price would make that assertion pass or fail by luck.
//
// So the only entropy here is the offer's identifier, which is a Wikidata
// Q-number or a route and does not change between runs. Two runs of this program
// over the same snapshot produce the same file, byte for byte, which is what
// makes re-running it a no-op rather than a diff.
//
// # The three shapes, and why the proportions are what they are
//
// Most of the shop is [foundAlways]: a price inside the bound its own prompt
// would name, which is what a search returning a list worth sorting is made of.
// A quarter is [foundAtTheLastPrice], so that a screen taken at the opening
// prices and one taken after the schedule has run are not the same screen. A
// seventh is [foundNever] — scenery, the thing a search is meant *not* to
// return, which is what tells a reader the list was filtered rather than merely
// short. Nothing the demonstration shipped before this claimed that third value
// at all.
func price(id string, b band) ([]int, scenario) {
	steps := (b.high-b.low)/1000 + 1
	first := b.low + draw(id, "price", steps)*1000

	// Between a twentieth and a fifth off, to the nearest thousand, and never
	// nothing: a Human Not Present watch attempts only on a step change — see
	// agent.Watch — so an offer whose second price equalled its first would be
	// an offer no watch could ever act on. Issue #192 is what that cost the
	// concert and the ladders before they each acquired a second figure.
	reduction := (first * (5 + draw(id, "cut", 16)) / 100 / 1000) * 1000
	if reduction < 1000 {
		reduction = 1000
	}
	last := first - reduction

	prices := []int{first, last}
	switch bucket := draw(id, "scenario", 100); {
	case bucket < 15:
		// Below every price it steps through, so no search for it ever returns
		// it. Rounded down to a hundred, and comfortably clear of the last
		// price: a cap a rounding error away from a price is a scenario nobody
		// can read off the file.
		return prices, scenario{Cap: floor100(last - atLeast(last/10, 1000)), Found: foundNever}
	case bucket < 40:
		// Between the two, so the opening price is refused and the last one is
		// not. Strictly between, which the arithmetic guarantees: the reduction
		// above is at least a thousand, so the midpoint is at least five hundred
		// clear of the last price and strictly under the first.
		return prices, scenario{Cap: floor100(last + (first-last)/2), Found: foundAtTheLastPrice}
	default:
		// Above every price it steps through. A tenth of headroom rather than a
		// hair, so that the bound reads as a bound somebody chose rather than as
		// the price with a digit changed.
		return prices, scenario{Cap: ceil1000(first + atLeast(first/10, 1000)), Found: foundAlways}
	}
}

// draw is the deterministic stand-in for a random choice: a value in [0, n) that
// depends only on the offer's identifier and on what is being decided.
//
// purpose is mixed in so that two decisions about one offer do not move
// together — without it every offer's price and every offer's scenario would be
// the same fraction of their respective ranges, and the shop would be one row
// repeated.
//
// SHA-256 rather than math/rand, and not only because AGENTS.md bans the latter
// everywhere: a hash has no state to seed, so the answer for one offer cannot
// depend on how many offers were derived before it. Insert a shelf at the top of
// the list and nothing below it moves.
func draw(id, purpose string, n int) int {
	sum := sha256.Sum256([]byte(purpose + "\x00" + id))
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(n))
}

// atLeast is a floor on a proportion, so that a cheap offer still gets a
// visible gap rather than a rounding error.
func atLeast(value, floor int) int {
	if value < floor {
		return floor
	}
	return value
}

func floor100(v int) int { return v / 100 * 100 }
func ceil1000(v int) int { return (v + 999) / 1000 * 1000 }
