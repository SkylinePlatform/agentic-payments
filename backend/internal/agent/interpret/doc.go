// Package interpret turns natural language into typed constraints behind the
// IntentInterpreter interface.
//
// The scripted implementation is what CI uses: no test may depend on a live
// model. The user signs the interpreted constraints, not the prompt that
// produced them.
package interpret
