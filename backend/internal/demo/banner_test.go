package demo_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/demo"
)

// status builds one outcome for the banner to render.
func status(name string, state demo.State, detail string) demo.Status {
	return demo.Status{
		Process: demo.Process{
			Name:        name,
			Kind:        demo.KindRole,
			Summary:     "a protocol participant",
			Issue:       "16",
			Implemented: true,
			Dir:         "backend",
			Command:     "bin/" + name,
		},
		State:  state,
		Detail: detail,
	}
}

// TestBannerCountsEveryState is the regression the display table exists for.
//
// The summary line used to be a switch with no default, sitting beside two that
// had one. A state none of the three knew about therefore printed as "[ ?? ]"
// with no explanation and was counted as neither up, pending nor failed — so
// the total quietly disagreed with the list printed directly above it, which is
// the one line somebody reads before deciding the demo is working.
func TestBannerCountsEveryState(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	demo.Banner(&out, []demo.Status{
		status("agent", demo.StateRunning, ""),
		status("mpp", demo.StatePending, ""),
		status("merchant", demo.StateFailed, "exited immediately"),
		status("surface", demo.StateMislabelled, "marked not implemented, but it is running"),
	})

	assert.Contains(t, out.String(), "1 up, 1 not built yet, 2 failed",
		"every state has to land in exactly one counter, or the total contradicts the list above it")
}

// TestBannerExplainsEveryStateThatIsNotUp checks the other half of the table.
// A state that is not simply up owes the reader a reason, and a running one
// owes them nothing — a trailer there would be noise on the common case.
func TestBannerExplainsEveryStateThatIsNotUp(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		state demo.State
		want  string
	}{
		{"pending names the issue", demo.StatePending, "not built yet — issue #16"},
		{"failed gives the detail", demo.StateFailed, "exited immediately"},
		{"mislabelled blames the manifest", demo.StateMislabelled, "exited immediately; the manifest is out of date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			demo.Banner(&out, []demo.Status{status("agent", tc.state, "exited immediately")})
			assert.Contains(t, out.String(), tc.want,
				"a state that is not up has to say why, or the reader is left guessing")
		})
	}
}
