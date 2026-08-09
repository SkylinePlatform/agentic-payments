package interpret

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An in-package test, because what is being asserted is the request this
// provider builds and the failure branches it turns an answer into, and both are
// unexported by design — nothing above the Model port is allowed to know that a
// generateContent call exists.
//
// **The server is a local httptest.Server, not the API.** Hard rule 4 forbids a
// test that depends on a live LLM or an external network call, and this depends
// on neither: no key, no quota, no DNS. What it cannot prove is the mirror image
// — that no test ever *does* reach the network. Nothing can prove that. What is
// checkable, and is checked, is that NewGemini performs no I/O and that Model is
// the only way ModelInterpreter obtains bytes.

// TestTheRequestCarriesWhatTheAPIDocuments pins the four things a wrong one
// would break silently: the key is in a header rather than the URL, the model
// name is in the path, the instruction and the prompt go in different places,
// and the schema is sent as JSON rather than as a quoted string.
//
// The last is the one worth the test. answerSchema produces bytes; a field typed
// string rather than json.RawMessage would marshal them as "{\"type\":…}", the
// API would reject it, and the failure would arrive as a 400 whose body nobody
// reads.
func TestTheRequestCarriesWhatTheAPIDocuments(t *testing.T) {
	t.Parallel()

	var (
		gotPath string
		gotKey  string
		gotBody map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		assert.Empty(t, r.URL.Query().Get("key"),
			"a key in a URL is a key in every access log and proxy trace between here and there")
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody), "the request this package built is not JSON")

		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[]"}]},"finishReason":"STOP"}]}`))
	}))
	defer server.Close()

	g, err := NewGemini("test-key", "some-model")
	require.NoError(t, err)
	g.endpoint = server.URL
	g.client = server.Client()

	answer, err := g.Complete(t.Context(), "the instruction", "the prompt", []byte(`{"type":"array"}`))
	require.NoError(t, err)
	assert.Equal(t, "[]", string(answer), "the answer's text is what the port returns, unaltered")

	assert.Equal(t, "/models/some-model:generateContent", gotPath,
		"the model is named in the path, so a wrong one is a 404 rather than a different model answering")
	assert.Equal(t, "test-key", gotKey)

	assert.Contains(t, marshalled(t, gotBody["systemInstruction"]), "the instruction",
		"the vocabulary belongs in the system instruction, where it is not mistaken for something the user typed")
	assert.Contains(t, marshalled(t, gotBody["contents"]), "the prompt")

	config, ok := gotBody["generationConfig"].(map[string]any)
	require.True(t, ok, "the request carries no generationConfig, so structured output was never asked for")
	assert.Equal(t, "application/json", config["responseMimeType"])
	assert.Equal(t, map[string]any{"type": "array"}, config["responseJsonSchema"],
		"a schema sent as a quoted string is a 400 whose cause is three layers from the message")
}

// TestGeminiSaysWhyThereIsNoAnswer covers every way a call can succeed at the
// HTTP layer and still have produced nothing usable.
//
// Each of these otherwise reaches decode as a JSON syntax error, which points at
// this package rather than at the model — and the two that matter most are the
// truncation cases: a MAX_TOKENS answer is valid-looking JSON with the end
// missing, so "unexpected end of input" is exactly the wrong diagnosis.
func TestGeminiSaysWhyThereIsNoAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		says string
		why  string
	}{
		{
			name: "the answer was cut off", says: "MAX_TOKENS",
			raw: `{"candidates":[{"content":{"parts":[{"text":"[{\"op\":"}]},"finishReason":"MAX_TOKENS"}]}`,
			why: "truncated JSON reported as a syntax error sends the reader to the decoder",
		},
		{
			name: "the prompt was refused", says: "SAFETY",
			raw:  `{"promptFeedback":{"blockReason":"SAFETY"}}`,
			why:  "a blocked prompt is a fact about the sentence, not a defect in this package",
		},
		{
			name: "nothing came back at all", says: "no candidates",
			raw:  `{"candidates":[]}`,
			why:  "an empty candidate list is the one case with nothing else to report",
		},
		{
			name: "a candidate with no text", says: "no text",
			raw:  `{"candidates":[{"content":{"parts":[{}]},"finishReason":"STOP"}]}`,
			why:  "the parts of a candidate carry more than text, and a part carrying none is not an answer",
		},
		{
			name: "not the documented shape", says: "not the shape",
			raw:  `<html>502 Bad Gateway</html>`,
			why:  "a proxy answering 200 with HTML is a real failure mode and reads as a model defect",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := geminiAnswer("some-model", []byte(tc.raw))
			require.Error(t, err, tc.why)
			assert.Contains(t, err.Error(), tc.says, tc.why)
		})
	}
}

// TestARefusedCallQuotesTheBody is about the 429 the free tier answers.
//
// `limit: 0` on every quota means no allocation rather than used up, and nothing
// in the status line says which. An error carrying only "429 Too Many Requests"
// sends whoever reads it looking for traffic they have not had — see
// DefaultGeminiModel for the measurement this comes from.
func TestARefusedCallQuotesTheBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"limit: 0"}}`))
	}))
	defer server.Close()

	g, err := NewGemini("test-key", "")
	require.NoError(t, err)
	g.endpoint = server.URL
	g.client = server.Client()

	_, err = g.Complete(t.Context(), "instruction", "prompt", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
	assert.Contains(t, err.Error(), "limit: 0",
		"the diagnosis is in the body, and an error without it points at the wrong cause")
}

// TestNewGeminiRefusesToStartWithoutAKey is decision 5's other half, at the
// place that can enforce it.
//
// An agent asked for a model and quietly handed the scripted table produces a
// screenshot nobody can attribute, and the failure would look like the demo
// working suspiciously well. cmd/agent's flag handling is where the refusal
// surfaces; this is where it is made.
func TestNewGeminiRefusesToStartWithoutAKey(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"", "   "} {
		_, err := NewGemini(key, "")
		assert.Error(t, err, "a model-backed interpreter with no key must not fall back to a fixed table")
	}
}

// TestTheDefaultModelIsARollingAlias is the measurement in DefaultGeminiModel's
// comment, held to by a test so that the comment cannot quietly become a
// pinned name.
//
// The pinned aliases answer 429 with `limit: 0` on a free-tier key — no
// allocation rather than used up — so somebody reaching for one because it looks
// more reproducible gets a demonstration that cannot start.
func TestTheDefaultModelIsARollingAlias(t *testing.T) {
	t.Parallel()

	g, err := NewGemini("test-key", "")
	require.NoError(t, err)
	assert.Equal(t, DefaultGeminiModel, g.Model(), "an unnamed model has to resolve to the documented default")
	assert.True(t, strings.HasSuffix(DefaultGeminiModel, "-latest"),
		"the free tier has no allocation on the pinned aliases, and the error for that names usage rather than allocation")
}

func marshalled(t *testing.T, v any) string {
	t.Helper()

	out, err := json.Marshal(v)
	assert.NoError(t, err, "re-encoding what the server received should not be able to fail")
	return string(out)
}
