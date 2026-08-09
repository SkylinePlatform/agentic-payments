package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
)

// TestAfterWatch is the decision in afterWatch's doc comment, as a table.
//
// The two rows that matter are the exhaustion pair. They are the whole of the
// argument: the same outcome is a failure to a caller that gets its shell back
// and is not one to a process that only ever ends on a signal, and a change that
// collapses them is a change to what `make demo` does when a demonstration runs
// out of prices.
func TestAfterWatch(t *testing.T) {
	t.Parallel()

	other := errors.New("the merchant is unreachable")

	for _, tc := range []struct {
		name    string
		err     error
		once    bool
		wantErr error
		// wantSaid is a fragment the reader has to be given, or empty for the
		// cases where there is nothing to say.
		wantSaid string
		why      string
	}{
		{
			name: "bought something", err: nil, once: false, wantErr: nil,
			why: "a completed purchase is the ordinary end of a watch",
		},
		{
			name: "stopped", err: context.Canceled, once: false, wantErr: nil,
			why: "Ctrl-C is somebody stopping the agent, not the agent failing",
		},
		{
			name: "stopped under -once", err: context.Canceled, once: true, wantErr: nil,
			why: "who asked for the shell back does not change what a signal means",
		},
		{
			name: "exhausted, long-running", err: agent.ErrScheduleExhausted, once: false,
			wantErr: nil, wantSaid: "nothing further will be attempted",
			why: "exiting would take the agent out of a stack the banner has already reported as up",
		},
		{
			name: "exhausted under -once", err: agent.ErrScheduleExhausted, once: true,
			wantErr: agent.ErrScheduleExhausted,
			why:     "this caller always gets a status back, so the status is the answer",
		},
		{
			name: "anything else", err: other, once: false, wantErr: other,
			why: "a watch that could not run at all is a failure however the process was started",
		},
		{
			name: "anything else under -once", err: other, once: true, wantErr: other,
			why: "the same, and -once does not soften it either",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var said strings.Builder
			got := afterWatch(&said, tc.err, tc.once)

			if tc.wantErr == nil {
				assert.NoError(t, got, tc.why)
			} else {
				assert.ErrorIs(t, got, tc.wantErr, tc.why)
			}

			if tc.wantSaid == "" {
				assert.Empty(t, said.String(),
					"nothing was said here, so anything printed is noise in a terminal somebody is reading")
				return
			}
			assert.Contains(t, said.String(), tc.wantSaid,
				"a watch that stayed up having stopped attempting anything is indistinguishable"+
					" from one still waiting, unless it says so")
		})
	}
}

// TestOnceAndAddrCannotBothBeGiven is the refusal flagsAgree exists for.
//
// The table's four rows are the whole of the decision, and the two that matter
// are the pair on the last line: -addr on its own is a server, -once on its own
// is a shell prompt after one purchase, and together they are a request nothing
// can satisfy. Refusing at parse time is what keeps a caller from discovering
// which one won by watching a process it expected to still be there.
func TestOnceAndAddrCannotBothBeGiven(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		addr    string
		once    bool
		wantErr bool
		why     string
	}{
		{
			name: "neither", why: "the default is a client that stays up, which is what it always was",
		},
		{
			name: "addr alone", addr: "127.0.0.1:8086",
			why: "a console with no exit path is the ordinary way to serve one",
		},
		{
			name: "once alone", once: true,
			why: "somebody smoke-testing the stack by hand wants the receipts and the shell back",
		},
		{
			name: "both", addr: "127.0.0.1:8086", once: true, wantErr: true,
			why: "a server that exits after its first watch is a server nobody can use",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := flagsAgree(tc.addr, tc.once)
			if !tc.wantErr {
				assert.NoError(t, err, tc.why)
				return
			}
			require.Error(t, err, tc.why)
			assert.Contains(t, err.Error(), "-addr",
				"a refusal that does not name both flags leaves the caller to guess which one to drop")
			assert.Contains(t, err.Error(), "-once")
		})
	}
}

// TestAfterWatchSaysWhatEndedTheWatch pins the first of the two lines rather
// than only the second.
//
// The sentinel's own text is the diagnosis — the merchant has no further price
// to move to — and a message that reported only the consequence would leave
// whoever is watching to guess between a schedule that ran out and a verifier
// that refused for some other reason.
func TestAfterWatchSaysWhatEndedTheWatch(t *testing.T) {
	t.Parallel()

	var said strings.Builder
	wrapped := fmt.Errorf("watching the price: %w", agent.ErrScheduleExhausted)

	assert.NoError(t, afterWatch(&said, wrapped, false), "a wrapped sentinel is still the sentinel")
	assert.Contains(t, said.String(), agent.ErrScheduleExhausted.Error(),
		"the reason is what tells a reader whether the demonstration is over or broken")
}
