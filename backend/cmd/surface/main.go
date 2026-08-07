// Command surface runs the mock Trusted Surface.
//
// AP2 requires this role to be non-agentic: no LLM, ever, in the thing that
// shows a user what they are about to authorise and takes their signature. A
// surface that could be talked into misdescribing a purchase is a surface whose
// signature means nothing.
//
// It is a separate binary for that reason and no other. This main imports
// nothing that reaches internal/agent, and a test walks the transitive import
// graph to keep it that way — the compiler is what refuses the mistake, rather
// than a reviewer noticing it.
package main

import (
	"flag"
	"net/http"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

func main() {
	addr := flag.String("addr", ":8084", "address to listen on")
	collector := roles.CollectorFlag()
	flag.Parse()

	roles.Main("surface", *addr, *collector, func(role roles.Role) (http.Handler, error) {
		blinder, err := sdjwt.NewBlinder()
		if err != nil {
			return nil, err
		}
		service := &surface.Service{
			Signer:  role.Signer,
			Keys:    role.Keys,
			Clock:   role.Clock,
			Events:  role.Events,
			Blinder: blinder,
		}
		return service.Handler()
	})
}
