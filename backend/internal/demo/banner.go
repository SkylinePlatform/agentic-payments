package demo

import (
	"fmt"
	"io"
	"strings"
)

// Banner writes what came up, what did not, and where to look.
//
// # It never calls the collector a role
//
// ADR 0003 requires that cmd/collector is not presented as a protocol
// participant, and this is the only place the demo describes what anything is,
// so this is where that has to hold. Processes are grouped by Kind and the
// heading says what the group is; the collector's own manifest entry carries
// the sentence saying what it is not, printed verbatim beneath it.
//
// Grouping rather than listing is deliberate. A flat list in startup order puts
// the collector first, above every protocol participant, which reads as billing.
func Banner(out io.Writer, statuses []Status) {
	w := func(format string, args ...any) {
		_, _ = fmt.Fprintf(out, format+"\n", args...)
	}

	w("")
	w("  %s", strings.Repeat("─", 62))
	w("  Agentic Payments — demo stack")
	w("  %s", strings.Repeat("─", 62))

	for _, group := range []struct {
		kind    Kind
		heading string
	}{
		{KindRole, "Protocol participants"},
		{KindInfrastructure, "Demo infrastructure — takes no part in the protocol"},
		{KindUI, "Interface"},
	} {
		members := filterKind(statuses, group.kind)
		if len(members) == 0 {
			continue
		}
		w("")
		w("  %s", group.heading)
		for _, s := range members {
			w("    %-9s %-13s %s", mark(s.State), s.Process.Name, s.Process.Summary)
			if s.Process.Note != "" {
				w("              %-13s %s", "", s.Process.Note)
			}
			if line := trailer(s); line != "" {
				w("              %-13s %s", "", line)
			}
		}
	}

	w("")
	if urls := urlsOf(statuses); len(urls) > 0 {
		w("  Open")
		for _, u := range urls {
			w("    %s", u)
		}
		w("")
	}
	w("  %s", summarise(statuses))
	w("  Ctrl-C stops everything.")
	w("")
}

// tally is which of the summary line's three counters a state feeds.
type tally int

const (
	tallyUp tally = iota
	tallyPending
	tallyFailed
)

// stateDisplay is how each state is marked, explained and counted — the state
// machine written out, rather than implied by three switches that have to
// agree with each other.
//
// It was three: one in mark, one in trailer, one in summarise. The first two
// carried a default and the third did not, so a fifth state would have rendered
// as "[ ?? ]" with no explanation and been counted as neither up, pending nor
// failed — a summary line quietly disagreeing with the list printed above it,
// and nothing anywhere failing to say so. internal/platform/crypto writes its
// key lifecycle out as a table for the same reason, and AGENTS.md asks for it:
// state machines explicit, not implied by chains at each call site.
//
// Adding a state is now one row here and one entry in states; init refuses to
// start without both.
var stateDisplay = map[State]struct {
	// mark is the tag down the left margin.
	mark string

	// trailer explains a state that is not simply up. It takes the whole
	// Status because what needs saying lives in the process and the detail
	// rather than in the state.
	trailer func(Status) string

	// tally is the counter this state feeds. Failed and mislabelled share one:
	// both mean somebody has to go and look.
	tally tally
}{
	StateRunning: {
		mark:    "[ up ]",
		trailer: func(Status) string { return "" },
		tally:   tallyUp,
	},
	StatePending: {
		mark:    "[ -- ]",
		trailer: func(s Status) string { return fmt.Sprintf("not built yet — issue #%s", s.Process.Issue) },
		tally:   tallyPending,
	},
	StateFailed: {
		mark:    "[FAIL]",
		trailer: func(s Status) string { return s.Detail },
		tally:   tallyFailed,
	},
	StateMislabelled: {
		mark:    "[ ?? ]",
		trailer: func(s Status) string { return s.Detail + "; the manifest is out of date" },
		tally:   tallyFailed,
	},
}

// Proof at start-up that every state can be displayed, the same guard
// internal/core/authz/constraint puts on operator phrases. A state added to
// runner.go without a row above would be shown as unknown and counted as
// nothing, which is a demo reporting the wrong total to whoever is watching it.
func init() {
	for _, s := range states {
		if _, ok := stateDisplay[s]; !ok {
			panic("demo: state " + string(s) + " has no display entry; it could not be shown or counted")
		}
	}
}

// mark renders a state as something scannable down the left margin. A state
// with no row reads as unknown — which init makes unreachable for any declared
// one, so this is the fallback for a State somebody minted by hand.
func mark(s State) string {
	d, known := stateDisplay[s]
	if !known {
		return "[ ?? ]"
	}
	return d.mark
}

// trailer is the explanatory line under a process that is not simply up.
func trailer(s Status) string {
	d, known := stateDisplay[s.State]
	if !known {
		return ""
	}
	return d.trailer(s)
}

func filterKind(statuses []Status, k Kind) []Status {
	var out []Status
	for _, s := range statuses {
		if s.Process.Kind == k {
			out = append(out, s)
		}
	}
	return out
}

func urlsOf(statuses []Status) []string {
	var out []string
	for _, s := range statuses {
		if s.State == StateRunning && s.Process.URL != "" {
			out = append(out, s.Process.URL)
		}
	}
	return out
}

// summarise is the one line somebody reads before deciding whether the demo is
// worth looking at.
func summarise(statuses []Status) string {
	counts := make(map[tally]int, 3)
	for _, s := range statuses {
		if d, known := stateDisplay[s.State]; known {
			counts[d.tally]++
		}
	}
	pending, failed := counts[tallyPending], counts[tallyFailed]

	line := fmt.Sprintf("%d up", counts[tallyUp])
	if pending > 0 {
		line += fmt.Sprintf(", %d not built yet", pending)
	}
	if failed > 0 {
		line += fmt.Sprintf(", %d failed", failed)
	}

	// Said plainly, because the alternative is somebody running this against
	// the ten-beat scenario and wondering why it stops after beat 3.
	if pending > 0 {
		line += ". The full scenario needs every participant; until then this" +
			" brings up what exists."
	}
	return line
}
