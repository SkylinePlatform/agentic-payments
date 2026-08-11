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
	watchProcess    = "agent-watch"

	stepFlag    = "-step"
	stepMaxFlag = "-step-max"
	pollFlag    = "-poll"
)

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

// processAt returns the named process and where it sits in the start order.
func processAt(m *Manifest, name string) (Process, int, bool) {
	for i, p := range m.Processes {
		if p.Name == name {
			return p, i, true
		}
	}
	return Process{}, 0, false
}

// startupFloor is the shortest time the runner can possibly take to get from
// one process starting to the next one it is asked about starting.
//
// It is a floor and not an estimate, derived from settle: a process marked
// unimplemented is waited on only until it exits, and awaitHealth checks before
// its first tick, so neither can be given a positive lower bound here. What can
// is an implemented process with no health check, which settle has nothing to
// wait on and so sleeps out stubGrace in full every time.
func startupFloor(m *Manifest, after, before int) time.Duration {
	var d time.Duration
	for _, p := range m.Processes[after+1 : before] {
		if p.Implemented && p.Health == "" {
			d += stubGrace
		}
	}
	return d
}

// TestTheMerchantsFirstPriceOutlastsTheStackComingUp is the second way beat 5
// can be lost, and the one no test in internal/roles/merchant can see.
//
// The known way is a poll longer than a step, which deploy/demo.json documents
// and merchant's TestJitteredScheduleObservesEveryPriceInOrder excludes. This
// is the other one. The merchant's schedule starts when the *merchant* starts —
// NewDemoService reads start from the clock at construction — but agent-watch
// is six entries further down the manifest, and its baseline quote is whatever
// price is in force by the time it gets there. agent.Watch takes that baseline
// as `last` and attempts only on a step it has not already seen, so a baseline
// that is already the $210 means the refusal never happens: one attempt, bought
// at $189, no error anywhere.
//
// Under the fixed 30s step this had 27 seconds of slack and nobody had to think
// about it. Issue #158's 3s floor left roughly half a second — 2.45 to 2.49
// seconds of merchant clock across eight runs — nearly all of it the flat
// stubGrace the runner spends on agent-buy, which stays up and has no health
// check to wait on. Moving agent-watch above agent-buy took that term out of the
// window entirely: 0.45 to 0.46 seconds measured the same way, against the same
// 3s floor, the difference being that grace to within a few milliseconds.
//
// # Two assertions, because after that reorder the first one alone proves nothing
//
// startupFloor is now zero for the shipped manifest, so requiring -step to
// exceed it is a statement that three seconds is more than nothing. That is the
// honest consequence of the fix rather than a hole: what buys the margin is no
// longer a number clearing another number, it is the *absence* of anything
// between the merchant and the watch that the runner is provably slow for. So
// the floor being zero is asserted as the property in its own right. Putting
// agent-buy back above agent-watch, or adding any other process there that stays
// up without a health check, fails on that second assertion — which is the
// regression this test now exists to catch, the first one having become a
// backstop for a manifest that reintroduces one *and* shortens -step.
func TestTheMerchantsFirstPriceOutlastsTheStackComingUp(t *testing.T) {
	t.Parallel()

	m, err := Load(shippedTopology)
	require.NoError(t, err, "the shipped manifest does not load")

	shop, shopAt, ok := processAt(m, merchantProcess)
	require.True(t, ok, "the demonstration has no merchant, so there is no schedule to pace anything against")
	_, watchAt, ok := processAt(m, watchProcess)
	require.True(t, ok, "the demonstration has no watching agent, so beat 5 cannot happen at all")
	require.Less(t, shopAt, watchAt,
		"the watch starts before the merchant it quotes, which is a topology this check cannot reason about")

	step, err := durationFlag(shop, stepFlag)
	require.NoError(t, err, "reading the shortest a price holds")

	floor := startupFloor(m, shopAt, watchAt)
	assert.Greater(t, step, floor,
		"the first price can expire before the watch has seen it: every process between the "+
			"merchant and agent-watch that stays up without a health check costs the runner a "+
			"full stubGrace, and the merchant's schedule is already running throughout. A "+
			"baseline taken at the $210 is a run with no refusal in it — beat 5 gone, silently, "+
			"which is exactly what issue #158 asked for a test about")

	assert.Zero(t, floor,
		"something between the merchant and agent-watch now makes the runner wait on a timer "+
			"rather than on a health check, and the watch's baseline moves later by that much. "+
			"agent-buy is the one that used to sit here and cost two seconds of a three second "+
			"floor; if it or anything like it has moved back above the watch, the margin this "+
			"order was chosen for is gone whether or not -step still clears the bound above")
}

// TestThePollStaysUnderTheShortestPriceHold is the rule deploy/demo.json spends
// a paragraph on and nothing checked.
//
// The watch triggers on the merchant's step changing, so a poll longer than the
// shortest a price can hold steps over the $210 and goes straight from $240 to
// $189. Under a range rather than a fixed duration what -poll has to stay below
// is the floor, -step, and not the pair's average — a run that drew the floor is
// a run like any other. The prose says so; this is what makes it fail.
func TestThePollStaysUnderTheShortestPriceHold(t *testing.T) {
	t.Parallel()

	m, err := Load(shippedTopology)
	require.NoError(t, err, "the shipped manifest does not load")

	shop, _, ok := processAt(m, merchantProcess)
	require.True(t, ok, "no merchant")
	watch, _, ok := processAt(m, watchProcess)
	require.True(t, ok, "no watching agent")

	step, err := durationFlag(shop, stepFlag)
	require.NoError(t, err, "reading the shortest a price holds")
	poll, err := durationFlag(watch, pollFlag)
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
