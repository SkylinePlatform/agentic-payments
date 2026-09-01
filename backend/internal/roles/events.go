package roles

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// Where a role's events go, and how a binary is told about it.
//
// ADR 0003 Decision 2 makes emission every role's job and Decision 3 puts the
// receiving end in cmd/collector. This file is the one place the two are joined,
// so that a role binary says where its events go in one line and does not get to
// invent an event shape, a sink or a default of its own.

// DefaultCollector is where a role sends its events unless told otherwise.
//
// The path is spelled out rather than taken from collector.EventsPath, and that
// duplication is the depguard rule named collector-containment working as
// intended: internal/collector is importable only from cmd/collector, so no
// role can name the constant even to read it. One repeated string is the price
// of a package that a dispute path cannot reach, and it is the right price.
//
// 127.0.0.1 rather than localhost, matching deploy/demo.json, because the two
// resolve differently often enough to matter: localhost can be ::1 first, and a
// collector bound to 127.0.0.1 is then simply not there.
const DefaultCollector = "http://127.0.0.1:8085/events"

// CollectorFlag registers the -collector flag every binary that emits shares.
//
// It exists so the five role binaries and the agent describe the same thing the
// same way. Registering the flag has to happen before flag.Parse, which is why
// this returns a pointer rather than Main taking care of it: Main runs after
// parsing, and by then it is too late to declare anything.
func CollectorFlag() *string {
	return flag.String("collector", DefaultCollector,
		"where protocol events are sent; empty means nowhere")
}

// Events builds the emitter a role records protocol-significant moments on.
//
// An empty collector yields a working emitter with the discarding sink rather
// than no emitter at all. A caller that wanted events off should not have to
// hold a different type, and a role running without a collector is the ordinary
// case rather than a broken one — `go run ./cmd/merchant` on its own is exactly
// that.
//
// # It cannot fail its caller, and that is ADR 0003 rather than convenience
//
// It used to return an error, and every caller returned it — so a process that
// could not build its *emitter* refused to start. Issue #273 is where that was
// noticed: the ADR's own consequence is that "a `cmd/collector` outage, a
// dropped event, or a slow SSE consumer must never delay or fail a mandate
// construction, presentation, verification or receipt issuance", and a role that
// will not come up at all is the widest possible instance of failing all four.
// The event log is observability and never evidence; a role whose event log is
// off is unchanged as a protocol participant, which is what the Role type's own
// comment says one file along.
//
// **The defect is reported and not swallowed**, on report, which is the half
// that makes this different from ignoring the error. obs.NewEmitter fails on a
// nil clock, an empty role, a non-positive buffer or a nil sink — every one of
// them a programming defect in the caller rather than anything about the
// collector — so a line naming it is what stops the next person debugging why
// their screenshots are thin. A nil *obs.Emitter records nothing rather than
// panicking, deliberately, so what comes back is usable either way.
//
// report is where that line goes; nil means os.Stderr, so a caller that has
// nowhere in particular to say it cannot accidentally make it silent.
func Events(clk authz.Clock, role, collector string, report io.Writer) *obs.Emitter {
	emitter, err := newEmitter(clk, role, collector)
	if err != nil {
		if report == nil {
			report = os.Stderr
		}
		_, _ = fmt.Fprintf(report, "%s: recording no events: %v\n", role, err)
		return nil
	}
	return emitter
}

// newEmitter is Events without the decision about what a failure means, so that
// the branch above has something to fail on. Unexported because the decision is
// the point: a second exported way in is a second way to reintroduce the return
// Events exists not to have.
func newEmitter(clk authz.Clock, role, collector string) (*obs.Emitter, error) {
	if collector == "" {
		return obs.NewEmitter(clk, role)
	}
	return obs.NewEmitter(clk, role, obs.WithSink(obs.NewHTTPSink(collector)))
}
