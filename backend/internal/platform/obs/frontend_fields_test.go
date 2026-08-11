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

// frontendFields delimits the frontend's own copy of obs.Event's field names.
// Same file frontendKinds reads, same reason: a field the frontend does not
// know about is one ProtocolEvent cannot carry and parseRecord silently drops,
// which for issue #174's amount is a price that never reaches the screen it
// was added for.
const (
	protocolEventFieldsOpen  = "PROTOCOL_EVENT_FIELDS = ["
	protocolEventFieldsClose = "]"
)

// eventJSONFields reads obs.Event's own field order and json tag names by
// reflection, so this test fails the moment a field is added or renamed in
// Go without anybody having to remember to update a second list here.
func eventJSONFields() []string {
	t := reflect.TypeOf(obs.Event{})
	fields := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		fields = append(fields, name)
	}
	return fields
}

// TestTheFrontendKnowsEveryField is TestTheFrontendKnowsEveryKind's sibling,
// for the fields of one event rather than the six kinds. AGENTS.md names the
// same defect for both: "a field the two languages disagree about is the
// defect this repository keeps finding." A kind missing from the frontend's
// list makes a whole event invisible; a field missing here makes one fact
// about a visible event invisible instead — issue #174's amount, dropped
// silently by parseRecord's "unrecognised fields are dropped" rule, with
// nothing failing anywhere near the change that caused it.
//
// Deliberately on the Go side, for TestTheFrontendKnowsEveryKind's reason: the
// failure belongs to whoever adds or renames a field, and what they run is
// `make check`.
func TestTheFrontendKnowsEveryField(t *testing.T) {
	source, err := os.ReadFile(frontendKinds)
	require.NoError(t, err, "the frontend's event module has moved; see TestTheFrontendKnowsEveryKind")

	start := strings.Index(string(source), protocolEventFieldsOpen)
	require.GreaterOrEqual(t, start, 0,
		"PROTOCOL_EVENT_FIELDS is no longer declared as an array literal in frontend/src/sse/events.ts")

	rest := string(source)[start+len(protocolEventFieldsOpen):]
	end := strings.Index(rest, protocolEventFieldsClose)
	require.GreaterOrEqual(t, end, 0, "the PROTOCOL_EVENT_FIELDS array literal is unclosed")

	parts := strings.Split(rest[:end], `"`)
	declared := make([]string, 0, len(parts)/2)
	for i := 1; i < len(parts); i += 2 {
		declared = append(declared, parts[i])
	}
	require.NotEmpty(t, declared, "the scan found no strings at all; the array's shape changed")

	assert.Equal(t, eventJSONFields(), declared,
		"frontend/src/sse/events.ts's PROTOCOL_EVENT_FIELDS must name every field obs.Event carries, "+
			"in Go's struct order. A field missing there is one ProtocolEvent cannot hold and "+
			"parseRecord silently drops on the way in")
}
