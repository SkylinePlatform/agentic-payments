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
// the collector first, above seven roles, which reads as billing.
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

// mark renders a state as something scannable down the left margin.
func mark(s State) string {
	switch s {
	case StateRunning:
		return "[ up ]"
	case StatePending:
		return "[ -- ]"
	case StateFailed:
		return "[FAIL]"
	case StateMislabelled:
		return "[ ?? ]"
	default:
		return "[ ?? ]"
	}
}

// trailer is the explanatory line under a process that is not simply up.
func trailer(s Status) string {
	switch s.State {
	case StatePending:
		return fmt.Sprintf("not built yet — issue #%s", s.Process.Issue)
	case StateFailed:
		return s.Detail
	case StateMislabelled:
		return s.Detail + "; the manifest is out of date"
	case StateRunning:
		return ""
	default:
		return ""
	}
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
	var up, pending, failed int
	for _, s := range statuses {
		switch s.State {
		case StateRunning:
			up++
		case StatePending:
			pending++
		case StateFailed, StateMislabelled:
			failed++
		}
	}

	line := fmt.Sprintf("%d up", up)
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
