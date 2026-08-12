package transport_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// The cap these tests use is small on purpose. What is being checked is the
// arithmetic at the boundary — one byte under, exactly on, one byte over — and a
// megabyte would prove the same three things more slowly.
const limit = 200

// filling is a body of exactly n bytes.
func filling(n int) string { return strings.Repeat("x", n) }

// object is a single JSON object of exactly n bytes whose last byte is the
// closing brace.
//
// The shape matters more than the size: a document missing its final byte cannot
// parse, so a truncating reader and a refusing one are distinguishable by which
// error arrives rather than by whether one arrives at all. n must be at least as
// long as the shell, which every caller below satisfies by construction.
func object(n int) string {
	const shell = `{"pad":"","tail":1}`
	return `{"pad":"` + filling(n-len(shell)) + `","tail":1}`
}

// readings is the two ways a body arrives: all at once, and a byte at a time.
//
// The second is not padding. The accounting has to be cumulative across reads,
// and an implementation that only compared one Read's length against the whole
// limit would pass every single-shot test in this file and refuse nothing on a
// real socket, where a 400 KiB body arrives in dozens of pieces.
var readings = []struct {
	name string
	wrap func(string) io.Reader
}{
	{name: "all at once", wrap: func(s string) io.Reader { return strings.NewReader(s) }},
	{name: "a byte at a time", wrap: func(s string) io.Reader {
		return iotest.OneByteReader(strings.NewReader(s))
	}},
}

// TestABodyThatFitsIsReadWhole is the half that must not change.
//
// A limit that refused what it used to accept would be a different defect with
// the same shape, and the row that matters is the one *exactly* on the cap: the
// limit is what may be read, not what may not, so a body of precisely that many
// bytes has to come back entire and with no error.
func TestABodyThatFitsIsReadWhole(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 1, limit - 1, limit} {
		for _, r := range readings {
			t.Run(fmt.Sprintf("%d bytes, %s", size, r.name), func(t *testing.T) {
				t.Parallel()

				body := filling(size)
				got, err := io.ReadAll(transport.RefusingOver(r.wrap(body), limit))

				require.NoError(t, err,
					"a %d-byte body inside a %d-byte limit is exactly what the limit permits; "+
						"refusing it would break every caller that reads a normal answer", size, limit)
				assert.Equal(t, body, string(got),
					"the whole body has to arrive, or the reader is still shortening documents and "+
						"has only changed which ones")
			})
		}
	}
}

// TestABodyOverTheCapIsRefusedRatherThanShortened is the refusal itself.
//
// Two claims, and the second is the one io.LimitReader fails. The error has to
// arrive — io.ReadAll over io.LimitReader returns nil for a body that did not
// fit — and it has to be identifiable as *this* fault, because a caller deciding
// whether to widen a cap or go and look at the peer needs to know which of the
// two happened.
func TestABodyOverTheCapIsRefusedRatherThanShortened(t *testing.T) {
	t.Parallel()

	for _, size := range []int{limit + 1, limit + 40, 4 * limit} {
		for _, r := range readings {
			t.Run(fmt.Sprintf("%d bytes, %s", size, r.name), func(t *testing.T) {
				t.Parallel()

				got, err := io.ReadAll(transport.RefusingOver(r.wrap(filling(size)), limit))

				require.ErrorIs(t, err, transport.ErrTooLarge,
					"a %d-byte body over a %d-byte limit has to be refused; io.LimitReader answers "+
						"this case with the first %d bytes and no error at all, which is a truncated "+
						"document handed on as a whole one", size, limit, limit)
				assert.Contains(t, err.Error(), fmt.Sprintf("%d bytes", limit),
					"the error has to name the limit that was hit, because the reader is the only "+
						"party that knows what the number was")
				assert.LessOrEqual(t, len(got), limit,
					"nothing past the limit may reach the caller — a caller handed the whole body "+
						"beside the error is one that can parse it and never look at the error")
			})
		}
	}
}

// TestADecoderSeesTheRefusalRatherThanABrokenDocument is the case the agent, the
// merchant and the shop all actually hit: a JSON decoder over a capped body.
//
// The mutation this is written against is the whole change — swap
// transport.RefusingOver for io.LimitReader and the "one over" arm still fails,
// but with io.ErrUnexpectedEOF, which says the counterparty sent a document that
// ended in the middle. It did not. This side cut it. Asserting only that an
// error arrived would pass under that mutation, which is why the two assertions
// below are about *which* error it is.
func TestADecoderSeesTheRefusalRatherThanABrokenDocument(t *testing.T) {
	t.Parallel()

	decode := func(t *testing.T, body string) error {
		t.Helper()

		var out struct {
			Pad  string `json:"pad"`
			Tail int    `json:"tail"`
		}
		return json.NewDecoder(
			transport.RefusingOver(strings.NewReader(body), limit)).Decode(&out)
	}

	t.Run("exactly on the cap", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, decode(t, object(limit)),
			"a document whose last byte is the last byte the limit allows is a document that "+
				"fits, and a decoder that failed here would refuse every answer that happens "+
				"to land on the boundary")
	})

	t.Run("one byte over the cap", func(t *testing.T) {
		t.Parallel()

		err := decode(t, object(limit+1))

		require.ErrorIs(t, err, transport.ErrTooLarge,
			"the document did not fit, and that is the fault to report")
		assert.NotErrorIs(t, err, io.ErrUnexpectedEOF,
			"unexpected EOF is what io.LimitReader produces here, and it blames the sender's "+
				"JSON for a cut this side made — nobody reading it widens a cap, they go and "+
				"look at the peer")
	})
}

// TestReadingOnAfterARefusalKeepsRefusing is why the refusal is sticky.
//
// A reader that answered the call after a refusal with EOF would let a caller
// that swallowed one error carry on and reach the end of a body it had just been
// told it could not have.
func TestReadingOnAfterARefusalKeepsRefusing(t *testing.T) {
	t.Parallel()

	r := transport.RefusingOver(strings.NewReader(filling(limit+10)), limit)

	_, err := io.ReadAll(r)
	require.ErrorIs(t, err, transport.ErrTooLarge, "the first read has to refuse, or there is nothing to read on from")

	buf := make([]byte, 16)
	n, err := r.Read(buf)
	assert.Zero(t, n, "a refused reader has no bytes left to give")
	assert.ErrorIs(t, err, transport.ErrTooLarge, "the second read has to refuse for the same reason as the first")
}

// TestALimitAtEitherEndOfInt64IsNotAPanic is the two ends of the range, and both
// rows exist because both used to panic inside Read with a message about slice
// arithmetic in a package the caller never named.
//
// Nothing in this module passes either value. What makes them worth pinning is
// that neither is an absurd thing for a future call site to pass: math.MaxInt64
// is the idiomatic "no limit", and io.LimitReader takes it happily, so a site
// migrating from one to the other would arrive with it in hand.
func TestALimitAtEitherEndOfInt64IsNotAPanic(t *testing.T) {
	t.Parallel()

	t.Run("less than nothing", func(t *testing.T) {
		t.Parallel()

		_, err := io.ReadAll(transport.RefusingOver(strings.NewReader("x"), -1))
		assert.ErrorIs(t, err, transport.ErrTooLarge,
			"a limit of less than nothing permits nothing, and it has to say so rather than panic")
	})

	t.Run("everything", func(t *testing.T) {
		t.Parallel()

		body := filling(4 * limit)
		got, err := io.ReadAll(transport.RefusingOver(strings.NewReader(body), math.MaxInt64))

		require.NoError(t, err, "a limit no body can reach refuses nothing, and computing it must not overflow")
		assert.Equal(t, body, string(got), "the whole body arrives, because the limit is larger than it")
	})
}
