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

	// Interpreter turns a prompt into constraints. Required. The demo wires
	// interpret.Demo(), a scripted table; cmd/agent -interpreter gemini wires a
	// model behind this same interface.
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

// Interpret runs the reading and stops there — Propose's slow half.
//
// The only method on this type that reaches an interpreter, which is what the
// paragraph above claims for the whole of it: Propose delegates to
// agent.Client.Propose, which delegates to agent.Client.Interpret, so there is
// exactly one call site for a model however a caller arrives. ProposeFrom below
// reaches none at all.
func (a *Agent) Interpret(ctx context.Context, prompt string) (interpret.Interpretation, error) {
	if a.Client == nil {
		return interpret.Interpretation{},
			errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.Interpret(ctx, agent.Intent{
		Prompt: prompt, Interpreter: a.Interpreter, AgentKey: a.AgentKey,
	})
}

// ProposeFrom settles on an offer from a reading already made.
//
// No interpreter is handed to the Intent below, and that absence is the point
// rather than an economy: agent.Client.ProposeFrom does not read a sentence, so a
// field for one here would suggest some path through this call could. Hard rule 2
// is a property of the graph, and the graph is narrower on this method than on
// any other.
func (a *Agent) ProposeFrom(
	ctx context.Context, prompt, item string, reading interpret.Interpretation,
) (agent.Proposal, error) {
	if a.Client == nil {
		return agent.Proposal{}, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.ProposeFrom(ctx, agent.Intent{
		Prompt: prompt, Item: item, AgentKey: a.AgentKey,
	}, reading)
}

// ProposeStated settles on a chosen offer under a stated limit.
//
// The agent key is this console's, exactly as on the read path: it is the key
// both open mandates will endorse in cnf, and it is not something a caller gets
// to name. Everything else came from a person looking at a table.
func (a *Agent) ProposeStated(
	ctx context.Context, item string, limit generated.Amount, quantity int,
) (agent.Proposal, error) {
	if a.Client == nil {
		return agent.Proposal{}, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.ProposeStated(ctx, agent.Intent{
		Item: item, AgentKey: a.AgentKey,
	}, limit, quantity)
}

// Catalogue asks the merchant for everything it sells.
//
// No interpreter and no key, on Describe's reasoning read one question wider: a
// shop window is a read of the shop's own catalogue, and there is nothing about
// it for a user to approve because nothing about it is a purchase. What it must
// not be mistaken for is a search — see agent.Client.Catalogue, which carries
// the argument for why the two are different questions.
func (a *Agent) Catalogue(ctx context.Context) ([]agent.Offer, error) {
	if a.Client == nil {
		return nil, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.Catalogue(ctx)
}

// Describe asks the merchant to say what one offer is.
//
// No interpreter and no key: naming a thing is a read of the shop's own
// catalogue, and there is nothing about it for a user to approve. That is why
// it sits beside Propose rather than inside it — see agent.Client.Describe.
func (a *Agent) Describe(ctx context.Context, item string) (agent.Offer, error) {
	if a.Client == nil {
		return agent.Offer{}, errors.New("console: this agent has no client to reach its counterparties with")
	}
	return a.Client.Describe(ctx, item)
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
