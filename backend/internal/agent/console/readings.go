package console

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
)

// The interpretations POST /interpret has made and POST /candidates has not yet
// finished with.
//
// # Why the console holds anything at all
//
// It did not, and `docs/specs/2026-08-10-trusted-surface-consent-design.md` says
// so in as many words: *"The console holds nothing — no identifier to hand back,
// no record with a lifetime of its own."* That was right for one request. Issue
// #299 is what one request cost: `POST /proposals` ran a shelves fetch, a model
// call with a 60-second ceiling and a merchant search in sequence, and the
// browser had nothing to draw for the whole of it. Splitting the phases so a
// person can read the interpretation while the search runs means something has to
// survive between two requests, and this is that something.
//
// The spec's own paragraph rejecting a remembered intent is answered rather than
// ignored, because its objection still holds and is narrower than it looks. It
// says a remembered intent *"does not avoid the mandates travelling back through
// the browser… it only adds a third kind of bookkeeping"*, and buys *"a shorter
// final request"*. Nothing here is about the final request: the mandates still
// travel through the browser, `POST /watches` is unchanged, and what this buys is
// the thing that paragraph was not weighing — a screen that says what the agent
// understood before it knows what the shop has.
//
// # Why the interpretation stays here rather than going to the browser
//
// **Because constraints have never come from the browser, and a design where they
// could is one where they eventually do.** Handing the reading out and taking it
// back would make `POST /candidates` a route that accepts a constraint set: not a
// state — the package comment's rule is about states — but something worse in one
// specific way. The limits a person is asked to sign would then be limits this
// agent was *told*, and the agent would be republishing them to the Trusted
// Surface as its own reading of the sentence. Nothing downstream could tell the
// difference, because nothing downstream sees the prompt.
//
// The round trip that costs is one small JSON object. The property it keeps is
// that every constraint on a consent screen was proposed by the party that read
// the sentence.
//
// # Bounded by count and by age, and unguessable
//
// Three properties, each closing a different hole:
//
//   - **maxReadings** stops a page with a button on it from filling this process's
//     memory. `DefaultLimit` makes the same argument about watches.
//   - **readingLifetime** stops a reading from outliving the screen it was made
//     for. A sentence read an hour ago against a catalogue that has since changed
//     is a stale answer, and the honest repair is to read it again.
//   - **The identifier is 16 random bytes**, where a watch's name is six. That is
//     not inconsistency: `newID`'s own comment says a watch name is read off a
//     screen and *"guessing one buys nothing"*. Guessing one of these buys
//     something — a search, and a proposal, against somebody else's reading of
//     somebody else's sentence. So it is sized as a capability rather than as a
//     label. `no-weak-randomness` bans `math/rand` everywhere, which is why both
//     reach for `crypto/rand`.
//
// Eviction is oldest-first and it is silent. A caller whose reading is gone is
// told the same thing whether it expired, was evicted or never existed —
// ErrNoSuchReading — because this store genuinely cannot tell those apart and a
// message that guessed would send somebody looking in the wrong place.

// maxReadings is how many interpretations one console will hold.
//
// Thirty-two, against `DefaultLimit`'s eight watches rather than measured. A
// reading is not a watch: a person types a sentence, reads what came back, tries
// another, and buys one of them — and every row clicked in a product table spends
// the reading again rather than making a new one. Four per watch slot is enough
// for that pattern and small enough that the whole store is a few kilobytes.
const maxReadings = 32

// readingLifetime is how long an interpretation stays available to buy against.
//
// Fifteen minutes, and the number it is chosen against is `openMandateLifetime` —
// the hour a Trusted Surface signature is good for. A reading has to outlive
// somebody reading a product table and outlive nothing else, so it is deliberately
// the shorter of the two: the mandate is what carries authority through time, and
// this is a scratch pad in front of it.
const readingLifetime = 15 * time.Minute

// ErrNoSuchReading means the identifier named an interpretation this console is
// not holding: expired, evicted, or never made.
//
// A sentinel rather than a `generated.ErrorCode`, on this package's own standing
// rule — the one `Service.start`'s `ErrTooManyWatches` arm states. That vocabulary
// is a verifier's account of what is wrong with a mandate, and an agent minting an
// entry in it to describe its own bookkeeping would be the same overreach as
// reporting a verdict. Nothing about a forgotten reading is a fact about a
// mandate; no mandate exists yet.
var ErrNoSuchReading = errors.New(
	"console: this reading is not one this agent still holds — read the sentence again")

// reading is one interpretation and when it was made.
//
// The prompt is here rather than taken from the request that spends it, and that
// is the same property the store exists for one field along: a caller that could
// restate the sentence could put a proposal's `prompt` out of step with the
// constraints it carries, and that field is what the Trusted Surface is later
// handed as the thing the interpretation came from.
type reading struct {
	prompt string
	made   time.Time
	what   interpret.Interpretation
}

// remember files an interpretation and returns the name it can be asked for by.
//
// Expired entries go first, then the oldest, so a console that has been left open
// evicts on age rather than on a bound it never reaches.
func (s *Service) remember(prompt string, what interpret.Interpretation) (string, error) {
	id, err := newReadingID()
	if err != nil {
		return "", err
	}

	now := s.Clock.Now()

	s.readingsMu.Lock()
	defer s.readingsMu.Unlock()

	if s.readings == nil {
		s.readings = make(map[string]reading)
	}
	s.forgetExpired(now)
	for len(s.readings) >= maxReadings && len(s.readingOrder) > 0 {
		oldest := s.readingOrder[0]
		s.readingOrder = s.readingOrder[1:]
		delete(s.readings, oldest)
	}

	s.readings[id] = reading{prompt: prompt, made: now, what: what}
	s.readingOrder = append(s.readingOrder, id)
	return id, nil
}

// recall answers with the interpretation filed under id, or ErrNoSuchReading.
//
// An expired entry is dropped here as well as in remember, so a store nobody is
// writing to still lets go of what it is holding — and so that an identifier
// which has aged out answers the same way whether or not anybody has interpreted
// anything since.
func (s *Service) recall(id string) (reading, error) {
	now := s.Clock.Now()

	s.readingsMu.Lock()
	defer s.readingsMu.Unlock()

	s.forgetExpired(now)
	held, ok := s.readings[id]
	if !ok {
		return reading{}, ErrNoSuchReading
	}
	return held, nil
}

// forgetExpired drops every reading older than readingLifetime. The caller holds
// readingsMu.
func (s *Service) forgetExpired(now time.Time) {
	kept := s.readingOrder[:0]
	for _, id := range s.readingOrder {
		held, ok := s.readings[id]
		if !ok {
			continue
		}
		if now.Sub(held.made) >= readingLifetime {
			delete(s.readings, id)
			continue
		}
		kept = append(kept, id)
	}
	s.readingOrder = kept
}

// newReadingID mints the name one interpretation is asked for by.
//
// Sixteen bytes rather than `newID`'s six, and the difference is what the string
// is for: a watch's name is a label on a row somebody reads off a screen, and this
// is the only thing standing between a caller and an interpretation it did not
// make. Base64url so it survives a URL and a JSON body unaltered, and
// `crypto/rand` because `no-weak-randomness` leaves nothing else.
func newReadingID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("console: naming the reading: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
