package obs_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/obs"
)

// authorisationRefOpen delimits the frontend's copy of Authorisation's member
// names. An interface body, like MandateRef's, so it closes on a brace.
const (
	authorisationRefOpen  = "interface AuthorisationRef {"
	authorisationRefClose = "}"
)

// TestTheFrontendKnowsEveryAuthorisationField is the fifth pin, and it is
// TestTheFrontendKnowsEveryMandateField's argument applied to the second nested
// object on obs.Event.
//
// The reason it is a separate test rather than one more assertion in a widened
// one is the reason there are five: each of them protects a different failure,
// and this one's is the same shape as the fourth's — obs.Event's own reflection
// stops at `authorisation`, so renaming `json:"signed"` to `json:"rendered"`
// would leave TestTheFrontendKnowsEveryField perfectly green while
// optionalAuthorisation refused every step of every Human Not Present attempt.
//
// The **member names** matter here for one more reason than they did for
// MandateRef, and it is worth being explicit about. `typed` and `signed` are not
// interchangeable words for one thing: the first is the caller's account of what
// the user wrote and the second is the Trusted Surface's own rendering of what
// they signed. A rename that swapped them would put an unsigned string where a
// screen says the user's signature covers it — which is not a label going
// missing, it is the one distinction /authorise/preview exists to protect being
// misdrawn.
//
// Deliberately on the Go side, for the reason its four siblings give: the
// failure belongs to whoever changes the shape, and what they run is
// `make check`.
func TestTheFrontendKnowsEveryAuthorisationField(t *testing.T) {
	raw, err := os.ReadFile(frontendKinds)
	require.NoError(t, err, "the frontend's event module has moved; see TestTheFrontendKnowsEveryKind")
	source := string(raw)

	start := strings.Index(source, authorisationRefOpen)
	require.GreaterOrEqual(t, start, 0,
		"AuthorisationRef is no longer declared as an interface in frontend/src/sse/events.ts, and "+
			"this test can no longer read it — point it at the new shape rather than deleting it")

	rest := source[start+len(authorisationRefOpen):]
	end := strings.Index(rest, authorisationRefClose)
	require.GreaterOrEqual(t, end, 0, "the AuthorisationRef interface body is unclosed")

	declared := make([]string, 0, 3)
	// The same expression MandateRef's own pin uses; both interfaces are one
	// `readonly name:` per member and neither has a reason to be read twice.
	for _, match := range mandateRefMember.FindAllStringSubmatch(rest[:end], -1) {
		declared = append(declared, match[1])
	}
	require.NotEmpty(t, declared,
		"the scan found no members at all, which means AuthorisationRef changed shape rather "+
			"than contents — a version of this test that reported success here would be worse than no test")

	authorisation := reflect.TypeOf(obs.Authorisation{})
	want := make([]string, 0, authorisation.NumField())
	for i := range authorisation.NumField() {
		name, _, _ := strings.Cut(authorisation.Field(i).Tag.Get("json"), ",")
		want = append(want, name)
	}

	assert.Equal(t, want, declared,
		"AuthorisationRef must name every member obs.Authorisation puts on the wire, in Go's "+
			"struct order. A name the two sides disagree about is not a missing label — "+
			"optionalAuthorisation refuses the whole record, so every step of every attempt "+
			"disappears rather than the card")
}
