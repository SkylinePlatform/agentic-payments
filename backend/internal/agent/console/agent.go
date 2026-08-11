package console

import (
	"context"
	"errors"
	"time"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/agent/interpret"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
	"github.com/SkylinePlatform/agentic-payments/backend/pkg/sdjwt"
)

// Agent is the Watcher this console runs in the process: the real client, the
// real key, the real watch loop.
//
// It exists so that cmd/agent stays what AGENTS.md says a main should be —
// flags, an identity, a service and a Run — while the composition it would
// otherwise hold sits somewhere it can be tested without a process. There is no
// decision in it: every field is configuration or a collaborator, and the four
// methods are the Watcher this console drives — see that interface's own doc
// comment for why Propose, Examples, Authorise and Watch are one port.
//
// **It is not where the model is.** Interpreter is the only thing here an LLM
// may ever sit behind, it is called once per authorisation before the user
// signs, and nothing in Watch reaches it — which is hard rule 2 and is the same
// property cmd/agent's own doc comment claims for the command line.
type Agent struct {
	// Client is how the agent talks to its counterparties. Required.
	Client *agent.Client

	// Interpreter turns a prompt into constraints. Required. cmd/agent
	// -interpreter scripted wires interpret.Demo(), a scripted table, and
	// -interpreter gemini wires a model behind this same interface; the demo
	// asks for -interpreter auto, which is the first of those unless
	// GEMINI_API_KEY is exported.
	Interpreter interpret.IntentInterpreter

	// AgentKey is the public half of the key this agent signs delegations with.
	// It ends up in both open mandates' cnf claim. Required.
	AgentKey generated.PublicKey

	// Signer holds the private half — the one both open mandates endorse.
	// Required.
	Signer authz.Signer

	// Blinder decides what may be withheld from which verifier. Required.
	Blinder *sdjwt.Blinder

	// Clock stamps every closed mandate and every key binding. Required.
	Clock authz.Clock

	// Interval is how often each watch re-quotes. Zero means agent.DefaultPoll.
	Interval time.Duration

	// Merchant is the payee written into every closed Payment Mandate, and
	// CredProviderID and ProcessorID are the audiences of the other two payment
	// chains. All three are each verifier's own identifier, as it sets Audience
	// on its rules.
	Merchant       generated.Merchant
	CredProviderID string
	ProcessorID    string
}

// Propose runs the discovery half and stops before the signature.
func (a *Agent) Propose(ctx context.Context, prompt, item string) (agent.Proposal, error) {
	if a.Client == nil {
		return agent.Proposal{}, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.Propose(ctx, agent.Intent{
		Prompt: prompt, Item: item, Interpreter: a.Interpreter, AgentKey: a.AgentKey,
	})
}

// Examples asks the interpreter for its menu, if it has one.
//
// An optional-interface probe rather than a method on IntentInterpreter, because
// widening that interface would oblige every implementation to have a menu when
// a model-backed one has none — and the honest answer there is nothing, not an
// invented list. ScriptedInterpreter.Prompts is already documented as the thing
// "a caller printing this is showing somebody a menu"; this is that caller.
func (a *Agent) Examples() []string {
	menu, ok := a.Interpreter.(interface{ Prompts() []string })
	if !ok {
		return nil
	}
	return menu.Prompts()
}

// Authorise runs the discovery half.
//
// item goes through unread. Whether the offer a caller picked is one the prompt
// describes is a question about a constraint, and the agent does not answer
// those — agent.Intent.Item argues that where the field lives.
func (a *Agent) Authorise(ctx context.Context, prompt, item string) (agent.Authorisation, error) {
	if a.Client == nil {
		return agent.Authorisation{}, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.Authorise(ctx, agent.Intent{
		Prompt:      prompt,
		Item:        item,
		Interpreter: a.Interpreter,
		AgentKey:    a.AgentKey,
	})
}

// Watch spends an authorisation, reporting each attempt to p.
//
// A fresh agent.Watch per call, which is what makes several watches at once
// legitimate: each one keeps its own Tracker in a local, so two authorisations
// are two lifecycle machines and neither can see the other. agent.Tracker's "not
// safe to share" is about one tracker rather than one agent.
func (a *Agent) Watch(
	ctx context.Context, auth agent.Authorisation, quantity int, p agent.Progress,
) (agent.Watched, error) {
	w := &agent.Watch{
		Client:         a.Client,
		Authorisation:  auth,
		Signer:         a.Signer,
		Blinder:        a.Blinder,
		Clock:          a.Clock,
		Interval:       a.Interval,
		Quantity:       quantity,
		Merchant:       a.Merchant,
		CredProviderID: a.CredProviderID,
		ProcessorID:    a.ProcessorID,
		Progress:       p,
	}
	return w.Run(ctx)
}

// Compile-time proof that the composition above is the port the console drives.
var _ Watcher = (*Agent)(nil)
