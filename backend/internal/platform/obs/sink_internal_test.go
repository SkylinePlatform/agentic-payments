package obs

// The sink's transport — issue #341.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestASinkDoesNotShareTheProcessTransport is the deterministic half of #341,
// and it exists because the behavioural half is a race.
//
// TestASinkSurvivesAnotherPackageClosingItsIdleConnections drives real sends
// while something tears connections down, which is what a sink actually meets —
// and it passed fifty times locally, twenty-five at GOMAXPROCS=1, before failing
// on the first four-core CI runner. A test that can only lose the race
// sometimes cannot be the thing that stops the sharing coming back.
//
// What can is the property the fix turns on: `CloseIdleConnections` reaches the
// pool it is called on and no other, so a sink holding its own is out of reach of
// every `httptest.Server.Close` in the process. Asserting the transport is not
// the shared one is asserting exactly that, and it fails every time rather than
// one run in fifty.
//
// It is in the internal test package because the client is unexported, and it
// should stay that way: a call site holding a *HTTPSink has no business
// reconfiguring the pool underneath it.
func TestASinkDoesNotShareTheProcessTransport(t *testing.T) {
	t.Parallel()

	sink := NewHTTPSink("http://127.0.0.1:1")
	require.NotNil(t, sink.client.Transport,
		"a nil Transport is http.DefaultTransport, which is the sharing this issue removed — and it "+
			"is the value the field had for as long as nobody looked")
	assert.NotSame(t, http.DefaultTransport, sink.client.Transport,
		"every httptest.Server.Close in the process calls CloseIdleConnections on the default "+
			"transport, so sharing it means unrelated packages dropping this sink's deliveries — "+
			"measured once on a CI runner, at send 75 of 200")

	other := NewHTTPSink("http://127.0.0.1:1")
	assert.NotSame(t, sink.client.Transport, other.client.Transport,
		"and two sinks do not share with each other either: one is the ordinary case, but a pool "+
			"shared between two is the same coupling with a smaller blast radius")
}
