package authz_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/authz"
	"github.com/SkylinePlatform/agentic-payments/backend/internal/core/generated"
)

// TestEveryTransitionOfTheRejectionReceiptRule is the machine, one row per cell.
//
// All nine, refusals included, because the refusals are the rule: the three
// permitted transitions on their own would say what an agent may do and leave
// what it may not do to be inferred.
func TestEveryTransitionOfTheRejectionReceiptRule(t *testing.T) {
	t.Parallel()

	// mentions is the per-cell half of a refusal message — the sentence the
	// table authors, not the sentinel's own text.
	//
	// It earns its place on the four cells that share ErrNoPresentationOutstanding,
	// where this sentence is the only thing telling a reader which of them
	// refused and a swapped pair would go unnoticed by every ErrorIs here. The
	// other two cells each own their sentinel — ErrOpenMandateOutstanding and
	// ErrMandateSpent appear once apiece — so there it pins the wording rather
	// than the identification. Three sentinels appear in the table in all;
	// ErrUnknownTransition is the package's fourth and belongs to no cell,
	// because a missing entry is what produces it.
	//
	// The sentinel's own wording is deliberately left free, and that asymmetry
	// is the rule rather than an omission: pin what a consumer will depend on
	// byte-for-byte (the state names a screen will render), pin identity for
	// what code branches on (the sentinels, which ErrorIs already covers more
	// strongly than any string compare), and leave diagnostic prose editable.
	// Pinning it too would make every wording improvement a test edit and buy
	// nothing no assertion here already has.
	for _, tc := range []struct {
		name     string
		from     authz.MandateState
		event    authz.MandateEvent
		want     authz.MandateState
		fails    error
		mentions string
	}{
		{
			name: "a mandate with nothing outstanding may be attempted",
			from: authz.StateReady, event: authz.EventAttempted,
			want: authz.StateAwaitingReceipt,
		},
		{
			name: "a rejection for a mandate that was never attempted answers nothing",
			from: authz.StateReady, event: authz.EventRejected,
			fails: authz.ErrNoPresentationOutstanding, mentions: "no attempt outstanding, so nothing is here for a rejection",
		},
		{
			name: "an acceptance for a mandate that was never attempted answers nothing",
			from: authz.StateReady, event: authz.EventAccepted,
			fails: authz.ErrNoPresentationOutstanding, mentions: "no attempt outstanding, so nothing is here for an acceptance",
		},
		{
			name: "attempting again before the receipt arrives is the rule's whole point",
			from: authz.StateAwaitingReceipt, event: authz.EventAttempted,
			fails: authz.ErrOpenMandateOutstanding, mentions: "rejection receipt for it has to arrive before another attempt may begin",
		},
		{
			name: "a rejection receipt is what licenses the next attempt",
			from: authz.StateAwaitingReceipt, event: authz.EventRejected,
			want: authz.StateReady,
		},
		{
			name: "an accepted attempt spends the mandate",
			from: authz.StateAwaitingReceipt, event: authz.EventAccepted,
			want: authz.StateSpent,
		},
		{
			name: "a spent mandate may not be attempted, because no rejection is coming for it",
			from: authz.StateSpent, event: authz.EventAttempted,
			fails: authz.ErrMandateSpent, mentions: "its attempt was accepted, and only a rejection licenses another",
		},
		{
			name: "a rejection for a spent mandate answers nothing",
			from: authz.StateSpent, event: authz.EventRejected,
			fails: authz.ErrNoPresentationOutstanding, mentions: "spent, so nothing is outstanding for a rejection",
		},
		{
			name: "an acceptance for a spent mandate answers nothing",
			from: authz.StateSpent, event: authz.EventAccepted,
			fails: authz.ErrNoPresentationOutstanding, mentions: "spent, so nothing is outstanding for an acceptance",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := tc.from.Next(tc.event)

			if tc.fails != nil {
				// Before the Contains below, because Contains against an empty
				// string passes for any message at all — so a refusal row that
				// forgot its mentions would assert nothing while reading like a
				// row that asserts something. The three permitted rows have no
				// mentions legitimately and return above this branch, which is
				// exactly what makes the omission invisible in the literal: a
				// row without the field looks normal. The recurrence-aware
				// outcome this branch's commit describes would add three rows to
				// this table, and this line is what stops them landing silent.
				require.NotEmpty(t, tc.mentions,
					"a refusal row with no sentence to match asserts nothing, and reopens the swap hazard the column exists to close")
				require.ErrorIs(t, err, tc.fails,
					"the refusal has to name a reason a caller can act on, and these are not interchangeable: one is worth waiting out and one never becomes attemptable")
				assert.Contains(t, err.Error(), tc.mentions,
					"the sentinel says which rule refused and this sentence says which cell did, which is the half a reader needs to find the transition they got wrong")
				assert.Equal(t, tc.from, got,
					"a refused event has to leave the mandate where it was, or a caller storing the result without reading the error would have applied a transition the machine refused")
				return
			}

			require.NoError(t, err, "the rule permits this and refusing it would stall an honest agent")
			assert.Equal(t, tc.want, got, "the mandate ended up somewhere the rule does not put it")
		})
	}
}

// TestOneAuthorisationCannotBeSpentAgainstTwoCheckouts is the attack the rule
// exists to stop, and it is issue #13's reason for being. An agent holding one
// open mandate — one thing the user approved — begins a second purchase attempt
// while the first is unanswered, which is how a single authorisation reaches two
// different checkouts. The second attempt never leaves the agent.
//
// **Be exact about what is being reproduced, because the machine is blunter
// than the story.** MandateState holds no checkout identity and no attempt
// identity, so there are not two candidates here: there is one value stepped
// twice. What the machine refuses is a second attempt while one is outstanding,
// and it cannot tell a deliberate double-spend from an honest retry after a
// lost response — it refuses both identically, which is the conservative
// direction and is why StateAwaitingReceipt has no timeout out of it. So the
// attack is blocked in the sense that the second attempt is refused, and not in
// the stronger sense of being recognised as an attack.
//
// The refusal is the agent's own, not a verifier's, and that is the property
// rather than a shortcoming of the test: a verifier is shown one presentation
// with no record of the other, so there is no place but here for this to be
// caught.
func TestOneAuthorisationCannotBeSpentAgainstTwoCheckouts(t *testing.T) {
	t.Parallel()

	// The user approved one purchase. No attempt has been made yet, which is
	// what the zero value means.
	var mandate authz.MandateState
	require.Equal(t, authz.StateReady, mandate,
		"a mandate a caller has no record of has to start ready, or a tracker would have to remember to initialise every entry")

	// The first candidate: the agent begins an attempt against it.
	mandate, err := mandate.Next(authz.EventAttempted)
	require.NoError(t, err, "the first attempt on an unspent mandate is the one the rule permits")
	require.Equal(t, authz.StateAwaitingReceipt, mandate, "the attempt is outstanding until a receipt answers it")

	// The second candidate, against the same authorisation, while the first is
	// still unanswered. This is the attack.
	after, err := mandate.Next(authz.EventAttempted)
	assert.ErrorIs(t, err, authz.ErrOpenMandateOutstanding,
		"one user authorisation was spendable against two checkouts at once, which is the whole of what this rule prevents")
	assert.NotErrorIs(t, err, authz.ErrMandateSpent,
		"this refusal is worth waiting out and a spent mandate's is not, so an agent that could not tell the two apart would abandon a purchase the user approved")
	assert.Equal(t, authz.StateAwaitingReceipt, after,
		"the refused attempt must not consume the outstanding one, or the receipt that does arrive would have nothing to answer")
	assert.Equal(t, generated.ErrorCodeOpenMandateOutstanding, authz.CodeOf(err),
		"the refusal has to be nameable in the same vocabulary a receipt uses, since a caller rendering it has only the canonical codes")
}

// TestTheBuiltScenarioAttemptsTwiceAndThenStops walks beats 5 and 6 of
// docs/business/use-cases.md: the candidate at $210 is rejected by the
// merchant, and only then may the agent attempt $189.
//
// It is the counterpart to the attack test and matters as much. The rule makes
// attempts sequential rather than impossible, and a machine that refused the
// retry would have stopped the demo's own headline flow while looking like it
// was enforcing something.
func TestTheBuiltScenarioAttemptsTwiceAndThenStops(t *testing.T) {
	t.Parallel()

	mandate := authz.StateReady

	// Beat 5: the candidate at $210, which the merchant refuses because it is
	// over the $200 cap.
	mandate, err := mandate.Next(authz.EventAttempted)
	require.NoError(t, err, "the first attempt is permitted")
	mandate, err = mandate.Next(authz.EventRejected)
	require.NoError(t, err, "a rejection receipt has to be applicable, or the agent could never retry")
	require.Equal(t, authz.StateReady, mandate,
		"a rejected mandate returns to ready: the rejection receipt is exactly what the specification requires before the next attempt")

	// Beat 6: the price falls to $189 and the purchase goes through.
	mandate, err = mandate.Next(authz.EventAttempted)
	require.NoError(t, err, "the retry the rejection receipt licensed was refused")
	mandate, err = mandate.Next(authz.EventAccepted)
	require.NoError(t, err, "a successful receipt has to be applicable")
	require.Equal(t, authz.StateSpent, mandate, "an accepted purchase spends the authorisation it was made under")

	// And that is the end of it: single use, so a third candidate cannot reuse
	// what the user approved once.
	after, err := mandate.Next(authz.EventAttempted)
	assert.ErrorIs(t, err, authz.ErrMandateSpent,
		"a mandate that has already bought something stayed attemptable, which is one authorisation buying twice")
	assert.NotErrorIs(t, err, authz.ErrOpenMandateOutstanding,
		"a retry loop reading this as merely outstanding would wait for a rejection receipt that is never coming, which is why the two refusals are separate sentinels")
	assert.Equal(t, authz.StateSpent, after, "spent is terminal")
	assert.Equal(t, generated.ErrorCodeOpenMandateOutstanding, authz.CodeOf(err),
		"both readings of one rule share the code that names it, so a reader of a rendered refusal is not told there are two rules")
}

// TestAStateTheMachineDoesNotDefineRefusesEverything covers the value that
// arrives from a cast or from decoding something another process stored.
//
// Refusing is the only safe answer: the alternative to refusing a state that
// cannot be read is permitting an attempt against one.
func TestAStateTheMachineDoesNotDefineRefusesEverything(t *testing.T) {
	t.Parallel()

	unknown := authz.MandateState(7)

	for _, event := range []authz.MandateEvent{authz.EventAttempted, authz.EventRejected, authz.EventAccepted} {
		t.Run(event.String(), func(t *testing.T) {
			t.Parallel()

			got, err := unknown.Next(event)
			require.ErrorIs(t, err, authz.ErrUnknownTransition,
				"an unreadable state has to refuse rather than fall through to whatever the zero value would permit")
			assert.NotErrorIs(t, err, authz.ErrNoPresentationOutstanding,
				"these say different things: one means the caller's bookkeeping is out of step, this one means the value it is holding is not a state at all, and only the second is a reason to stop trusting the value")
			assert.Equal(t, unknown, got, "refusing must not silently repair the state into one the machine does define")
		})
	}

	_, err := authz.StateReady.Next(authz.MandateEvent(9))
	assert.ErrorIs(t, err, authz.ErrUnknownTransition, "an event the machine does not define is refused on the same reasoning")
}

// TestTheStateNamesAreWhatAConsumerShows pins the spellings because they are
// read by people rather than only by code. The middle one is the one that
// matters: the rule makes attempts sequential, and a mandate waiting for its
// receipt is waiting, not stalled.
func TestTheStateNamesAreWhatAConsumerShows(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ready", authz.StateReady.String())
	assert.Equal(t, "awaiting_receipt", authz.StateAwaitingReceipt.String(),
		"this name is what a screen renders, and it has to read as waiting for an answer rather than as a failure")
	assert.Equal(t, "spent", authz.StateSpent.String())
	assert.Equal(t, "mandate_state(7)", authz.MandateState(7).String(),
		"a state outside the machine has to be printable, since it is the value a refusal message has to name")

	assert.Equal(t, "attempted", authz.EventAttempted.String())
	assert.Equal(t, "rejected", authz.EventRejected.String())
	assert.Equal(t, "accepted", authz.EventAccepted.String())
	assert.Equal(t, "mandate_event(9)", authz.MandateEvent(9).String())

	// The negative side, pinned separately because it is a different guard and
	// what it prevents is not cosmetic. These types index an array, so without
	// the low bound a negative value fails in two ways at once, both verified
	// by deleting it: called directly it panics with "index out of range [-1]",
	// and reached through Next's own fmt.Errorf it is recovered by fmt into
	// "%!s(PANIC=String method: runtime error: index out of range [-1])" — so
	// the refusal survives as a message that no longer says which pair was
	// refused. evidence.Step's spellings are pinned the same way.
	// The boundary itself, which the values above step straight over: 7 and 9
	// are four and six past the end, so a guard reading > rather than >= is
	// wrong only at exactly len(names) and nothing here would touch it. That
	// value is the reachable one — MandateState(3) is the next iota after
	// StateSpent, which is precisely what "decoding a stored state something
	// else wrote" produces when a newer build added a state — and it panics with
	// "index out of range [3] with length 3". evidence.Step has the same gap at
	// its own high end; that is worth knowing and is not this branch's to fix.
	assert.Equal(t, "mandate_state(3)", authz.MandateState(3).String(),
		"the first value past the last state is the one a newer build's stored state arrives as, so it is the one the guard has to hold at")
	assert.Equal(t, "mandate_event(3)", authz.MandateEvent(3).String(),
		"the same boundary on the event side, reachable the same way")

	assert.Equal(t, "mandate_state(-1)", authz.MandateState(-1).String(),
		"a negative state has to render rather than panic, because Next formats whatever it was handed")
	assert.Equal(t, "mandate_event(-1)", authz.MandateEvent(-1).String(),
		"a negative event has to render rather than panic, for the same reason")

	_, err := authz.MandateState(-1).Next(authz.MandateEvent(-1))
	require.ErrorIs(t, err, authz.ErrUnknownTransition)
	assert.Contains(t, err.Error(), "mandate_state(-1) on mandate_event(-1)",
		"the refusal names the pair it was handed, which is the whole of what a caller has to go on here")
}

// TestARefusalThatIsNotTheRuleHasNoCanonicalCode pins the empty code, not
// merely "some other code".
//
// A receipt applied where nothing is outstanding, and a state value the machine
// does not define, are both a caller's bug rather than a verdict about a
// mandate, and contracts/evidence/error_code.json has no code for either. The
// arm that answers them exists so they do not reach the default, which answers
// mandate_malformed — a 400 telling a counterparty their mandate is bad because
// this caller's bookkeeping is. Asserting only that the code is *not*
// open_mandate_outstanding would pass for mandate_malformed too, which is the
// answer being ruled out.
func TestARefusalThatIsNotTheRuleHasNoCanonicalCode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"a receipt with nothing to answer", func() error {
			_, err := authz.StateReady.Next(authz.EventAccepted)
			return err
		}},
		{"a state the machine does not define", func() error {
			_, err := authz.MandateState(7).Next(authz.EventAttempted)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, generated.ErrorCode(""), authz.CodeOf(err),
				"a caller's own bug has no canonical code, and answering one would put a named protocol violation against a counterparty who did nothing")
		})
	}
}
