package interpret

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/platform/transport"
)

// DefaultGeminiModel is the model a Gemini calls when none is named.
//
// **A rolling alias rather than a pinned one, and that is measured rather than
// a preference.** On a free-tier key the pinned aliases answer 429
// RESOURCE_EXHAUSTED with `limit: 0` on every quota — which is *no allocation*
// rather than *used up*, and nothing in that error says so:
//
//	gemini-2.0-flash          RESOURCE_EXHAUSTED
//	gemini-2.0-flash-lite     RESOURCE_EXHAUSTED
//	gemini-2.5-flash-lite     NOT_FOUND
//	gemini-flash-latest       OK
//	gemini-flash-lite-latest  OK
//
// So somebody reaching for a pinned name because it looks more reproducible gets
// a demonstration that cannot start and an error that points at usage they have
// not had. The model name is a field for exactly that reason: naming another one
// is a flag, not a rebuild.
//
// What a rolling alias costs is that the model behind it moves, so two runs
// months apart may read a sentence differently. That is the right trade here —
// the deterministic path is `-interpreter scripted`, which is what `make demo`
// runs and what every golden number in this repository comes from.
const DefaultGeminiModel = "gemini-flash-latest"

// geminiEndpoint is the Generative Language API's base.
const geminiEndpoint = "https://generativelanguage.googleapis.com/v1beta"

// geminiTimeout bounds one call when the caller's context carries no deadline.
//
// An interpretation happens once, before a user signs, with somebody waiting for
// the approval screen — so a request that hangs is a demonstration that appears
// to have frozen. The context still wins when it expires first; this is the
// floor, not the policy.
const geminiTimeout = 60 * time.Second

// maxGeminiResponse bounds what is read back. The answer is a short JSON array,
// and the body's size is chosen by somebody else.
//
// Refused rather than truncated, per issue #251. This site is the one where the
// truncating form was worst: io.ReadAll over an io.LimitReader returns the first
// megabyte with **no error at all**, so the cut document went to geminiAnswer,
// which reported it as "answered something that is not the shape this API
// documents" — a sentence blaming the model for a cut made here, on the one path
// in this repository where the answer really is somebody else's to get wrong.
const maxGeminiResponse = 1 << 20

// Gemini is a Model backed by Google's Generative Language API.
//
// It is the whole of what this repository knows about a provider: one POST, the
// request shape that endpoint documents, and the answer's text pulled out of it.
// Nothing above it — not ModelInterpreter, not internal/agent, not cmd/agent —
// names a Gemini type, which is what makes a second provider a file beside this
// one.
//
// # No SDK
//
// backend/go.mod has one non-test dependency, and the precedent is pkg/httpsig
// and pkg/sdjwt: this repository implements wire formats rather than taking
// dependencies for them. The seam is our own interface anyway, so an SDK would
// buy nothing across it and would put a vendor's types inside the one package an
// LLM is permitted in.
type Gemini struct {
	// key authenticates the call. Held rather than read from the environment:
	// os.Getenv appears nowhere in backend/ and should not start inside a
	// library package, where it would make behaviour depend on who called and
	// tests depend on the order they ran in. cmd/agent reads GEMINI_API_KEY and
	// passes the value.
	key string

	// model is the name in the request path.
	model string

	// endpoint and client are fields rather than the constants above reached
	// directly, so that gemini_internal_test.go can point one at an
	// httptest.Server. That is not a live model and not an external network
	// call, so hard rule 4 is untouched: what the substitution buys is the
	// request shape and every failure branch being assertable without one.
	endpoint string
	client   *http.Client
}

// NewGemini builds the provider.
//
// **It performs no I/O**, which is what lets cmd/agent build one during flag
// handling and fail on a missing key before it has waited thirty seconds for
// four counterparties. The first network call is the first Complete.
//
// An empty key is refused rather than defaulted. `-interpreter gemini` with no
// key must not quietly become the scripted table: an agent asked for a model and
// handed a fixed table produces a screenshot nobody can attribute, and the
// failure would show up as a demo that works suspiciously well.
func NewGemini(apiKey, model string) (*Gemini, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("interpret: a Gemini model needs an API key; " +
			"cmd/agent reads GEMINI_API_KEY and there is no default")
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultGeminiModel
	}
	return &Gemini{
		key:      apiKey,
		model:    model,
		endpoint: geminiEndpoint,
		client:   &http.Client{Timeout: geminiTimeout},
	}, nil
}

// Model reports which model this will call, so that a caller printing what it
// wired can say something true rather than repeating a flag's default.
func (g *Gemini) Model() string { return g.model }

// Complete makes one call and returns the answer's text.
//
// One call. There is no retry here and none above: see ModelInterpreter for the
// argument, which is that a second draw from the same distribution is not a
// correction and a demonstration with a deterministic path a flag away does not
// need one.
func (g *Gemini) Complete(ctx context.Context, instruction, prompt string, schema []byte) ([]byte, error) {
	body, err := json.Marshal(geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: instruction}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: prompt}}}},
		GenerationConfig: geminiGeneration{
			// Zero, because two runs of the same demonstration reading the same
			// sentence differently is a worse property here than a duller
			// reading. It does not make the call deterministic — nothing about
			// this API promises that — and the deterministic path is the
			// scripted interpreter.
			Temperature:        0,
			ResponseMIMEType:   "application/json",
			ResponseJSONSchema: json.RawMessage(schema),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", strings.TrimSuffix(g.endpoint, "/"), g.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// In a header rather than in the query string, which is the other form this
	// API accepts. A key in a URL is a key in every access log, proxy trace and
	// error report between here and there.
	req.Header.Set("x-goog-api-key", g.key)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s: %w", g.model, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(transport.RefusingOver(resp.Body, maxGeminiResponse))
	if err != nil {
		return nil, fmt.Errorf("reading what %s answered: %w", g.model, err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body is quoted because this API puts the diagnosis there and not
		// in the status: a free-tier key with no allocation answers 429 with
		// `limit: 0`, which reads as a quota that has been used up unless the
		// body is in front of you. See DefaultGeminiModel.
		return nil, fmt.Errorf("%s answered %s: %s", g.model, resp.Status, excerpt(raw))
	}

	return geminiAnswer(g.model, raw)
}

// geminiAnswer pulls the text out of a 200, or says why there is none.
//
// Split out from Complete because every branch here is a way the call can
// succeed at the HTTP layer and still have produced nothing usable, and those
// are the ones worth being able to read in a test without a server.
func geminiAnswer(model string, raw []byte) ([]byte, error) {
	var answer geminiResponse
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("%s answered something that is not the shape this API documents: %w; it said: %s",
			model, err, excerpt(raw))
	}

	if len(answer.Candidates) == 0 {
		if answer.PromptFeedback != nil && answer.PromptFeedback.BlockReason != "" {
			return nil, fmt.Errorf("%s refused the prompt: %s", model, answer.PromptFeedback.BlockReason)
		}
		return nil, fmt.Errorf("%s answered with no candidates at all", model)
	}

	candidate := answer.Candidates[0]
	// STOP is the model having finished. Everything else — MAX_TOKENS, SAFETY,
	// RECITATION — is a truncated or withheld answer, and a truncated JSON array
	// would otherwise reach decode as a syntax error with nothing pointing at
	// the cause.
	if candidate.FinishReason != "" && candidate.FinishReason != "STOP" {
		return nil, fmt.Errorf("%s stopped early: %s", model, candidate.FinishReason)
	}

	var text strings.Builder
	for _, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		// A candidate whose parts carry no text at all. It happens when the only
		// part is a thought signature, and an empty string handed to decode
		// would fail as "unexpected end of JSON input", which points nowhere.
		return nil, fmt.Errorf("%s answered a candidate carrying no text", model)
	}
	return []byte(text.String()), nil
}

// The request and response shapes, as the Generative Language API documents
// them. Only the fields this package sends or reads are here: a struct
// mirroring the whole API would be a second, staler copy of somebody else's
// specification.
type (
	geminiRequest struct {
		SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
		Contents          []geminiContent  `json:"contents"`
		GenerationConfig  geminiGeneration `json:"generationConfig"`
	}

	geminiContent struct {
		Role  string       `json:"role,omitempty"`
		Parts []geminiPart `json:"parts"`
	}

	geminiPart struct {
		Text string `json:"text"`
	}

	geminiGeneration struct {
		Temperature float64 `json:"temperature"`

		// ResponseMIMEType and ResponseJSONSchema are what turn this into a
		// structured-output call. responseJsonSchema takes JSON Schema, which is
		// what answerSchema produces and what carries the value union;
		// responseSchema, the older field, takes an OpenAPI subset. Sending the
		// schema on a provider that ignores it costs nothing — the instruction
		// states the shape in prose as well — and sending it on one that refuses
		// it is a 400 at the boundary rather than a wrong answer later.
		ResponseMIMEType   string          `json:"responseMimeType,omitempty"`
		ResponseJSONSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
	}

	geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []geminiPart `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`

		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
)
