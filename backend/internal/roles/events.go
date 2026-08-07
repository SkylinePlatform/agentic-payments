package roles

import (
	"flag"

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
func Events(clk authz.Clock, role, collector string) (*obs.Emitter, error) {
	if collector == "" {
		return obs.NewEmitter(clk, role)
	}
	return obs.NewEmitter(clk, role, obs.WithSink(obs.NewHTTPSink(collector)))
}
