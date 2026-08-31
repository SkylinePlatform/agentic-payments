package demo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shippedTopology is the manifest `make demo` runs, from this package's
// directory. manifest_test.go holds the same path under its own name because
// it is the external test package and cannot see this one's declarations.
const shippedTopology = "../../../deploy/demo.json"

// The processes whose flags have to be read as a group, and the flags
// themselves. Named here rather than inline because a typo in a string literal
// used once would make this file pass by finding nothing.
const (
	merchantProcess = "merchant"
	agentProcess    = "agent"

	stepFlag    = "-step"
	stepMaxFlag = "-step-max"
	pollFlag    = "-poll"
)

// TestTheMerchantsFirstPriceOutlastsTheStackComingUp used to live here, and
// issue #313 removed it along with the property it checked.
//
// It existed because the manifest started a watch at boot: cmd/agent was given a
// bare -watch, the merchant's schedule had been running since the merchant
// started, and the price that watch took as its baseline was whatever was in
// force by the time the runner reached it six entries later. A baseline already
// at the $210 lost the refusal silently — one attempt, bought at $189, no error
// anywhere — so the test derived a floor from the runner's own constants,
// required -step to clear it, and then required the floor to be exactly zero,
// which is the property the start order was arranged for.
//
// Nothing is started at boot now. The baseline is taken when a person clicks
// Buy, wherever the cycling schedule has got to, so there is no start-up window
// to measure and the refusal is likely rather than guaranteed — deploy/demo.json
// says so, and says what a presenter does about it. Rewriting the test to assert
// something about a watch that no longer exists would have been worse than
// deleting it, and hollowing it out is not available either: internal/suite
// fails a test that asserts nothing.
//
// startupFloor went with it, for the same reason and not as a tidy-up: it was
// that test's only caller.
//
// What survives is below, and it never depended on when a watch started. What did
// *not* survive is an ordering assertion that had nothing to do with the boot
// watch and was only ever a precondition of it —
// TestTheProcessesComeUpInAnOrderThatWorks is where that property now lives, on
// its own terms rather than as somebody else's guard clause.

// TestTheProcessesComeUpInAnOrderThatWorks pins the two orderings
// deploy/demo.json opens by declaring, and which nothing checked.
//
// # Why this exists at all
//
// The file's first paragraph is "**Order matters.**", and it gives two reasons.
// Until issue #313 the only test that read a process's *position* was the
// start-up margin check, which asked whether the merchant came up before the
// watching agent — a precondition of its own arithmetic rather than a claim about
// the topology. That check went with the boot watch, and the manifest was left
// opening with a rule nothing could break.
//
// # The two rules, and why only these two
//
// **The collector is first.** Every role emits into it, and a role that starts
// before it is listening loses whatever it emits in the meantime — there is no
// retry, deliberately, because an event log that pushed back on the roles would
// be the coupling ADR 0003 forbids. That is the one ordering mistake here with a
// silent failure mode: the demonstration comes up, everything reports healthy,
// and the first steps of a transaction are simply missing from the log.
//
// **The frontend is last.** It proxies to the collector and to the agent, and
// there is nothing to look at until they answer. This one is not silent — issue
// #267 is what a frontend probing something that is not up looks like — but it is
// the other half of the sentence the manifest opens with, and a rule stated in
// prose with only half of it checked is the half nobody notices.
//
// Nothing else about the order is asserted, because nothing else about it is
// claimed. The agent sits after the roles so that the console it serves is backed
// by a stack that is up, which `ready` enforces at run time rather than by
// position — and a test pinning every pair would fail on any rearrangement rather
// than on a wrong one.
func TestTheProcessesComeUpInAnOrderThatWorks(t *testing.T) {
	t.Parallel()

	m, err := Load(shippedTopology)
	require.NoError(t, err, "the shipped manifest does not load")
	require.NotEmpty(t, m.Processes, "an empty manifest would make both checks below pass on nothing")

	assert.Equal(t, "collector", m.Processes[0].Name,
		"a role that starts before the collector is listening loses whatever it emits, with no "+
			"retry and no error — the demonstration comes up healthy and the first steps of a "+
			"transaction are missing from the log")
	assert.Equal(t, "frontend", m.Processes[len(m.Processes)-1].Name,
		"the frontend proxies to the collector and to the agent, and there is nothing for it to "+
			"show until both answer")
}

// TestThePollStaysUnderTheShortestPriceHold is the rule deploy/demo.json spends
// a paragraph on and nothing checked.
//
// The watch triggers on the merchant's step changing, so a poll longer than the
// shortest a price can hold steps over the $210 and goes straight from $240 to
// $189. Under a range rather than a fixed duration what -poll has to stay below
// is the floor, -step, and not the pair's average — a run that drew the floor is
// a run like any other. The prose says so; this is what makes it fail.
//
// Whoever starts the watch, this holds: a browser posting to POST /watches gets
// the same re-quote loop the boot watch used to get, so the flag is still worth
// pinning against the merchant's even after #313 took the boot watch away.
func TestThePollStaysUnderTheShortestPriceHold(t *testing.T) {
	t.Parallel()

	m, err := Load(shippedTopology)
	require.NoError(t, err, "the shipped manifest does not load")

	shop, ok := process(m, merchantProcess)
	require.True(t, ok, "no merchant")
	agent, ok := process(m, agentProcess)
	require.True(t, ok, "no agent, so nothing in this demonstration can watch a price at all")

	step, err := durationFlag(shop, stepFlag)
	require.NoError(t, err, "reading the shortest a price holds")
	poll, err := durationFlag(agent, pollFlag)
	require.NoError(t, err, "reading how often the watch re-quotes")

	assert.Less(t, poll, step,
		"a poll at least as long as the shortest hold can land either side of a price without "+
			"ever landing on it, and the price it skips is the one the verifier is meant to refuse")

	stepMax, err := durationFlag(shop, stepMaxFlag)
	require.NoError(t, err, "reading the longest a price holds")
	assert.GreaterOrEqual(t, stepMax, step,
		"a maximum below the minimum is refused by NewDemoService, so this manifest would not "+
			"start the merchant at all")
}

// durationFlag reads the value following name in p's arguments.
//
// It returns an error rather than asserting, so that nothing in this file is a
// helper that fails a test from inside — the shape AGENTS.md warns about, where
// the first caller to reach it from a goroutine gets a silent failure.
func durationFlag(p Process, name string) (time.Duration, error) {
	for i, a := range p.Args {
		if a == name && i+1 < len(p.Args) {
			return time.ParseDuration(p.Args[i+1])
		}
	}
	return 0, errNoSuchFlag{process: p.Name, flag: name}
}

type errNoSuchFlag struct{ process, flag string }

func (e errNoSuchFlag) Error() string {
	return e.process + " does not state " + e.flag +
		", and deploy/demo.json's own rule is that every number in the pacing group is stated there"
}

// process returns the named process from the manifest.
//
// It answered where the process sat in the start order as well, until #313: the
// only caller that asked was the start-up margin check above, and a position
// nothing reads is a position that can drift without anything noticing.
func process(m *Manifest, name string) (Process, bool) {
	for _, p := range m.Processes {
		if p.Name == name {
			return p, true
		}
	}
	return Process{}, false
}
