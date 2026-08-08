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

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/roles/surface"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

func main() {
	addr := flag.String("addr", ":8084", "address to listen on")
	// The instrument this surface pins into every open Payment Mandate it
	// signs. A flag rather than a field of the request, because the party that
	// gets to say which of the user's cards pays is the party the user is
	// present at — see surface.Service.Instrument.
	instrument := flag.String("instrument", "card-4242",
		"identifier of the payment instrument pinned into open Payment Mandates")
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
			Instrument: generated.PaymentInstrument{
				ID: *instrument,
				// CARD, and there is no flag for it: this project enrols no
				// real instrument and the mock Credential Provider mints
				// against exactly one category, so a second flag would offer a
				// choice nothing downstream can honour.
				Type: "CARD",
				// Description is left unset on purpose. It is what a user reads
				// to tell which instrument they are approving, and this binary
				// knows an identifier and nothing else — "Visa ending 4242"
				// printed beside an arbitrary -instrument would be the surface
				// describing a card it cannot see.
			},
		}
		return service.Handler()
	})
}
