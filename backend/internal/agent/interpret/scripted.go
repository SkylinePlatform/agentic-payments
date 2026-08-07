package interpret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// ErrNoScript means this interpreter has no entry for the prompt it was given.
//
// The alternative is the worst thing this package could do: answering an
// unrecognised sentence with an empty constraint set. Silence reads as success
// at every call site, and the mandate it produced would authorise everything.
// So an unscripted prompt is a refusal.
var ErrNoScript = errors.New("interpret: no script for this prompt")

// Script is one prompt and the constraints it stands for.
//
// The constraints are written as JSON rather than as Go literals, for two
// reasons. It is the form they arrive in inside a signed mandate, so a scenario
// here can be set beside the fixtures in internal/core/authz and compared line
// by line. And it is the form a model would answer in, which keeps both
// implementations of IntentInterpreter going through the same decoding rather
// than the scripted one having a private route into the type.
type Script struct {
	// Prompt is the sentence a user types.
	Prompt string

	// Constraints is a JSON array of constraint nodes — the same vocabulary
	// documented in internal/core/authz/constraint.
	Constraints string
}

// ScriptedInterpreter answers from a fixed table and never calls a model.
//
// # Why this is not a generated mock, and not in a _test.go file
//
// Two separate reasons, and the second is the one that settles it.
//
// It computes rather than records. backend/.mockery.yml takes the doubles a test
// asserts *about* — that a collaborator was called, how often, with what — and
// this is not one of those: it produces a specific interpretation, and swapping
// it for a mock returning canned values would delete what its tests prove, in
// the same way it would for pkg/sdjwt's hmacKey or for clock.Fake.
//
// And it is not only a test double. It is what the demo runs: no model is
// configured there, and beat 2 has to happen for beats 3 to 10 to exist at all.
// A type declared in a _test.go file is reachable only from its own package's
// test binary, so cmd/agent and internal/agent could not name one. It lives in
// the package proper because non-test code depends on it.
//
// A value is safe to share between goroutines once built: nothing mutates it
// after NewScripted returns, and Interpret hands back a freshly decoded tree
// rather than a view onto the script.
type ScriptedInterpreter struct {
	// scripts in the order they were given, which is the order Prompts reports.
	scripts []Script

	// byPrompt maps a normalised prompt to its index in scripts.
	byPrompt map[string]int
}

// NewScripted builds an interpreter from a script.
//
// Every entry is decoded and validated here, so a script the verifier could not
// read stops the program that wires it rather than the demo three minutes later.
// That is an early warning and not the guarantee: the guarantee is that
// Interpret validates the tree it is about to return, which is what a caller
// holding the interface gets whichever implementation is behind it.
func NewScripted(scripts ...Script) (*ScriptedInterpreter, error) {
	s := &ScriptedInterpreter{
		scripts:  make([]Script, 0, len(scripts)),
		byPrompt: make(map[string]int, len(scripts)),
	}

	for _, script := range scripts {
		key := normalise(script.Prompt)
		if key == "" {
			return nil, errors.New("interpret: a script entry has no prompt")
		}
		if existing, clash := s.byPrompt[key]; clash {
			// Two entries that match the same way is not a harmless duplicate.
			// Which one answered would depend on which was written last, a
			// reader of the script would have no way of telling that the other
			// is dead, and this is the one component whose entire purpose is to
			// be deterministic. Aliases are supported — they are two entries
			// with two prompts — so nothing legitimate is being refused here.
			return nil, fmt.Errorf("interpret: %q and %q are the same prompt once matched",
				s.scripts[existing].Prompt, script.Prompt)
		}

		constraints, err := decode(script.Constraints)
		if err != nil {
			return nil, fmt.Errorf("interpret: the script for %q: %w", script.Prompt, err)
		}
		if err := Validate(constraints); err != nil {
			return nil, fmt.Errorf("interpret: the script for %q: %w", script.Prompt, err)
		}

		s.byPrompt[key] = len(s.scripts)
		s.scripts = append(s.scripts, script)
	}

	return s, nil
}

// Interpret answers from the script.
//
// The context is ignored, and that is this implementation's whole character:
// there is nothing to cancel because nothing leaves the process. An
// implementation that called a model would use it, and a test that used *that*
// would depend on a live model, which hard rule 4 forbids.
func (s *ScriptedInterpreter) Interpret(_ context.Context, prompt string) ([]generated.Constraint, error) {
	i, ok := s.byPrompt[normalise(prompt)]
	if !ok {
		return nil, fmt.Errorf("%w: %q; this interpreter is scripted for %d prompts, which Prompts lists",
			ErrNoScript, prompt, len(s.scripts))
	}

	// Decoded on every call rather than once at construction, so that what goes
	// back is a tree nobody else holds. A constraint carries an open value —
	// decoded JSON, so maps and slices behind a shared header — and a caller
	// handed the script's own copy could edit the interpretation the next caller
	// receives. The demo has one agent and would never notice; a scripted
	// interpreter shared between requests would notice exactly once, as a
	// mandate nobody wrote.
	constraints, err := decode(s.scripts[i].Constraints)
	if err != nil {
		// Unreachable: NewScripted decoded this same text. Reported rather than
		// swallowed, because the only other option is returning no constraints
		// and no error, and that reads as "the user placed no limits" at every
		// call site.
		return nil, fmt.Errorf("interpret: the script for %q: %w", s.scripts[i].Prompt, err)
	}

	// The interface's contract, not a repeat of the constructor's check: this is
	// the tree about to be returned, and the promise belongs to
	// IntentInterpreter rather than to one way of building one.
	if err := Validate(constraints); err != nil {
		return nil, err
	}
	return constraints, nil
}

// Prompts lists the sentences this interpreter answers, as they were written.
//
// In script order rather than sorted, because the order carries information the
// alphabet does not: the built scenario is first and the variations follow it. A
// caller printing this is showing somebody a menu.
func (s *ScriptedInterpreter) Prompts() []string {
	out := make([]string, 0, len(s.scripts))
	for _, script := range s.scripts {
		out = append(out, script.Prompt)
	}
	return out
}

// normalise is how a prompt is matched: case folded, with every run of
// whitespace collapsed to a single space.
//
// The line stops there deliberately. Anything looser — substring matching,
// keyword scoring, edit distance — is a language model with none of the honesty
// and none of the capability, and it fails in the direction that matters: "buy a
// flight to Palma" and "do not buy a flight to Palma" share every keyword, so a
// matcher that guessed would hand the second sentence the first one's mandate. A
// prompt this interpreter has no answer for is refused instead, which is a
// visible failure rather than a silently wrong authorisation.
func normalise(prompt string) string {
	return strings.Join(strings.Fields(strings.ToLower(prompt)), " ")
}

// decode reads a script's constraints.
//
// A plain Unmarshal, deliberately. Decoder options such as DisallowUnknownFields
// would be inert here for the reason recorded beside parseMoney in
// internal/core/authz/constraint: the generated Constraint has its own
// UnmarshalJSON which re-runs a plain json.Unmarshal internally, and a custom
// unmarshaller takes over the whole value. Validate is what actually catches a
// script written against a model that has moved on.
func decode(raw string) ([]generated.Constraint, error) {
	var constraints []generated.Constraint
	if err := json.Unmarshal([]byte(raw), &constraints); err != nil {
		return nil, fmt.Errorf("is not a JSON array of constraints: %w", err)
	}
	return constraints, nil
}
