package transport

import (
	"errors"
	"fmt"
	"io"
)

// ErrTooLarge means a body did not fit the limit the reader was given.
//
// A sentinel, because "the counterparty sent more than we agreed to read" and
// "the counterparty sent something that does not parse" are different faults
// with different owners and different fixes. io.LimitReader collapses the first
// into the second, and a caller holding only an error text cannot tell them
// apart — which is what this exists to undo.
var ErrTooLarge = errors.New("transport: the body is longer than the reader accepts")

// RefusingOver returns a reader over r that yields at most limit bytes and then
// fails with ErrTooLarge, instead of reporting the body as finished there.
//
// # What io.LimitReader does instead, measured
//
// io.LimitReader reports EOF at the limit, so a body that does not fit becomes a
// shorter body that does — and what happens next is decided by whatever parses
// it rather than by the code that set the limit. Both outcomes were observed
// against a 200-byte cap and a 201-byte body:
//
//	io.ReadAll(io.LimitReader(r, 200))          200 bytes, err == nil
//	json.NewDecoder(io.LimitReader(r, 200))     "unexpected EOF"
//
// The first is a truncated document handed on as though it were the whole one:
// no error anywhere, and whether anybody notices depends entirely on whether the
// next parser happens to care. The second is an error, but it is an error about
// the *sender's* JSON — the counterparty sent a well-formed document, this side
// cut it, and the diagnosis names the document. Nobody reading "unexpected EOF"
// goes and widens a cap; they go and look at the peer.
//
// # Why it reads one byte past the limit, and never hands it on
//
// A read confined to limit bytes cannot tell a body that ends there from one
// that goes on, so the extra byte is the only thing that makes the overrun
// observable at all. It is counted and dropped rather than delivered, because a
// caller handed the whole document *and* an error is a caller that will not see
// the error: json.Decoder keeps a read error aside and surfaces it only if it
// runs out of document, so delivering the byte past the limit would let the
// value complete and the refusal vanish. That is this file's own defect wearing
// the opposite mask, and TestADecoderSeesTheRefusalRatherThanABrokenDocument is
// what holds it shut.
//
// A body of exactly limit bytes is therefore read whole and never refused, which
// is the boundary worth stating: the limit is what may be read, not what may not.
//
// The refusal fires when something *asks* for a byte past the limit, and at a
// json.Decoder that is not quite the same as "the body was longer". A decoder
// stops as soon as its top-level value closes, so a document that ends exactly on
// the limit followed by trailing bytes is accepted, error unraised — which is the
// case roles.OK produces on every answer, since json.NewEncoder appends a
// newline. That tolerance is one byte of whitespace and it is correct: nothing the
// caller needed was dropped. What it means for a test is that the fixture has to
// carry the newline the real handler writes, or the row exercises a framing
// production never sends — see TestASearchAnswerOverTheCapIsRefusedRatherThanShortened,
// which has a row each way for exactly that reason. At an io.ReadAll site there is
// no such tolerance: one byte past the limit is refused, full stop.
//
// # Why this is not a RoundTripper
//
// A round-tripper wrapping every response body would mean no call site could
// forget — the argument internal/agent's own comment makes for Correlating, and
// the stronger shape in general. It is not the shape here because the limit is a
// different number at each site and the site is where that number is known and
// justified: 2 MiB for a public shop's whole catalogue, 1 MiB for one role's
// answer to one question. A transport capping every body at one number would be
// either too small for the shop or too large to be a limit for anything else,
// and the comment recording why *this* number is *this* big would have nowhere
// to live.
func RefusingOver(r io.Reader, limit int64) io.Reader {
	if limit < 0 {
		// Clamped rather than trusted. The alternative is a negative slice bound
		// inside Read, which panics with a message about slice arithmetic in a
		// package the caller never named.
		limit = 0
	}
	return &refusing{r: r, limit: limit, room: limit}
}

// refusing is the reader RefusingOver returns.
type refusing struct {
	r     io.Reader
	limit int64 // the cap, kept so the error can name it
	room  int64 // bytes that may still be handed to the caller
	over  bool  // whether the limit has already been passed
}

func (f *refusing) Read(p []byte) (int, error) {
	// Sticky. Once refused, every subsequent read refuses too — a reader that
	// answered the next call with EOF would let a caller that ignored one error
	// carry on and reach the end of a body it was told it could not have.
	if f.over {
		return 0, f.tooLarge()
	}

	// Written as a subtraction on the left rather than an addition on the right,
	// which is not a style choice: f.room+1 overflows for a limit near
	// math.MaxInt64 — the idiomatic "no limit" value, which io.LimitReader accepts
	// happily — and the negative bound that comes out of it panics inside a
	// purchase, about slice arithmetic, in a package the caller never named. This
	// form only computes room+1 once it is known to be smaller than len(p), and it
	// is the same comparison for every value that does not overflow.
	if int64(len(p))-1 > f.room {
		p = p[:f.room+1]
	}
	n, err := f.r.Read(p)
	if int64(n) > f.room {
		// The bytes past the limit are dropped, per the doc comment above.
		n = int(f.room)
		f.room, f.over = 0, true
		return n, f.tooLarge()
	}
	f.room -= int64(n)
	return n, err
}

func (f *refusing) tooLarge() error {
	return fmt.Errorf("%w: the limit is %d bytes", ErrTooLarge, f.limit)
}
