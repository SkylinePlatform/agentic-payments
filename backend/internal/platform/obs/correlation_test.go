package obs_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

func TestNewCorrelationIDIsEightLegibleCharacters(t *testing.T) {
	t.Parallel()

	// Fixed entropy, so the exact ID is asserted rather than its shape. This is
	// what the io.Reader parameter is for.
	id, err := obs.NewCorrelationID(bytes.NewReader([]byte{0xe9, 0xa4, 0x31, 0xdc, 0xa7, 0x9f}))
	if err != nil {
		t.Fatalf("NewCorrelationID: %v", err)
	}
	assert.Equal(t, "6aQx3Kef", id)
	// The length is the whole point of choosing six bytes: ADR 0003 rejected
	// traceparent on legibility, and a value that grew would give that up.
	assert.Equal(t, 8, len(id), "the ID has to read in a screenshot")
	if !obs.ValidCorrelationID(id) {
		t.Error("a minted ID did not pass our own validator")
	}
}

func TestNewCorrelationIDIsRandom(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 100)
	for range 100 {
		id, err := obs.NewCorrelationID(nil)
		if err != nil {
			t.Fatalf("NewCorrelationID: %v", err)
		}
		if seen[id] {
			t.Fatalf("%q was minted twice in 100 draws", id)
		}
		seen[id] = true
	}
}

func TestNewCorrelationIDReportsAFailedReader(t *testing.T) {
	t.Parallel()

	// Short read: fewer bytes than idBytes.
	if _, err := obs.NewCorrelationID(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("a truncated entropy source was accepted")
	}
	if _, err := obs.NewCorrelationID(failingReader{}); !errors.Is(err, errReader) {
		t.Errorf("err = %v, want it to wrap %v", err, errReader)
	}
}

var errReader = errors.New("no entropy today")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errReader }

func TestValidCorrelationID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		id   string
		want bool
	}{
		{"minted shape", "7aQx-3Kf", true},
		{"underscore", "7aQx_3Kf", true},
		{"foreign but harmless", "order-12345", true},
		{"single character", "a", true},
		{"at the length bound", strings.Repeat("a", 64), true},

		{"empty", "", false},
		{"past the length bound", strings.Repeat("a", 65), false},
		// The reason this validator exists at all. An adopted ID is written
		// into an SSE frame, where a blank line ends the event: a caller that
		// could smuggle one in could forge the next event in the stream the
		// frontend reads.
		{"newline", "abc\ndata: forged", false},
		{"carriage return", "abc\rdef", false},
		{"space", "abc def", false},
		{"colon", "abc:def", false},
		{"non-ascii", "abcé", false},
		{"nul", "abc\x00def", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := obs.ValidCorrelationID(tc.id); got != tc.want {
				t.Errorf("ValidCorrelationID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestContextCarriesTheID(t *testing.T) {
	t.Parallel()

	if got := obs.CorrelationID(context.Background()); got != "" {
		t.Errorf("a bare context reported %q, want empty", got)
	}

	ctx := obs.WithCorrelationID(context.Background(), "7aQx-3Kf")
	if got := obs.CorrelationID(ctx); got != "7aQx-3Kf" {
		t.Errorf("CorrelationID = %q, want %q", got, "7aQx-3Kf")
	}
}

func TestEnsureCorrelationID(t *testing.T) {
	t.Parallel()

	// Absent: mints one.
	ctx, id, err := obs.EnsureCorrelationID(context.Background(), nil)
	if err != nil {
		t.Fatalf("EnsureCorrelationID: %v", err)
	}
	if !obs.ValidCorrelationID(id) {
		t.Errorf("minted %q, which is not valid", id)
	}
	if obs.CorrelationID(ctx) != id {
		t.Error("the returned context does not carry the returned ID")
	}

	// Present: leaves it alone. This is the rule that no hop regenerates.
	again, id2, err := obs.EnsureCorrelationID(ctx, nil)
	if err != nil {
		t.Fatalf("EnsureCorrelationID: %v", err)
	}
	if id2 != id {
		t.Errorf("an existing ID was regenerated: %q became %q", id, id2)
	}
	if obs.CorrelationID(again) != id {
		t.Error("the context lost its ID")
	}
}
