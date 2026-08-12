package console_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/console"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
)

// TestAnAgentWithAScriptedInterpreterHasAMenu covers the optional-interface
// probe itself, which the route tests reach only through a mock.
func TestAnAgentWithAScriptedInterpreterHasAMenu(t *testing.T) {
	t.Parallel()

	withScript := &console.Agent{Interpreter: interpret.Demo()}
	assert.Equal(t, interpret.Demo().Prompts(), withScript.Examples())

	withModel := &console.Agent{Interpreter: interpreterWithoutPrompts{}}
	assert.Empty(t, withModel.Examples(),
		"an interpreter that publishes no menu must not be talked into one")
}

// interpreterWithoutPrompts is an IntentInterpreter with no Prompts method.
//
// A fixture rather than a generated mock: what is being tested is the absence
// of a method, and mockery cannot express that.
type interpreterWithoutPrompts struct{}

func (interpreterWithoutPrompts) Interpret(context.Context, string, interpret.Shelves) (interpret.Interpretation, error) {
	return interpret.Interpretation{}, nil
}
