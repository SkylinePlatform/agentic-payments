package demo

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// piping runs pipe over what a process printed and hands back the runner's own
// output, line by line.
//
// assert rather than require inside, on this repository's rule for helpers: a
// helper containing require is unsafe the moment any caller invokes it from a
// goroutine, and nothing about this one says it will not be.
func piping(t *testing.T, printed string) []string {
	t.Helper()

	var out strings.Builder
	runner := NewRunner(&Manifest{}, ".", &out)
	runner.pipe(Process{Name: "merchant"}, strings.NewReader(printed))

	forwarded := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if out.String() == "" {
		return nil
	}
	return forwarded
}

// TestAnOverLongLineDoesNotEndAProcessesOutput is issue #275.
//
// pipe was a bufio.Scanner whose Err was never read. A line past the token limit
// made Scan return false, the loop exited, and **that role's output stopped for
// the rest of the run** — `make demo` carried on and the pane simply went quiet,
// so the role looked idle rather than broken.
//
// The assertion the issue asks for is about the *message*, and this is why: a
// test that only checked the loop kept going would have passed on a runner that
// silently swallowed the long line, which is barely better than stopping. Three
// things have to be true at once — the head of the line is forwarded, the runner
// says what it dropped, and the next line arrives.
func TestAnOverLongLineDoesNotEndAProcessesOutput(t *testing.T) {
	t.Parallel()

	const extra = 4096
	long := strings.Repeat("x", maxLine+extra)
	forwarded := piping(t, "before\n"+long+"\nafter\n")

	require.Len(t, forwarded, 3,
		"the line after the long one is what the defect swallowed: without it the process goes quiet "+
			"for the rest of the run and nothing says so")
	assert.Equal(t, "merchant      | before", forwarded[0])
	assert.Equal(t, "merchant      | after", forwarded[2],
		"forwarding has to resume at the *next* line, not in the middle of the one that was too long")

	assert.Contains(t, forwarded[1], fmt.Sprintf("%d more bytes on this line were dropped", extra),
		"a reader has to be told how much they are not seeing, or the truncated line reads as the "+
			"whole of what the role said")
	assert.Contains(t, forwarded[1], "merchant",
		"and which role produced it — under `make demo` eight panes share one stream")
	assert.Contains(t, forwarded[1], strings.Repeat("x", 64),
		"the part that fits is still the role's output and is still worth forwarding; a runner that "+
			"replaced the line with a complaint would be hiding the evidence")
}

// TestOrdinaryLinesAreForwardedUnchanged is the control on the test above.
//
// Every assertion there is about a line the runner interfered with, so on their
// own they would pass against a pipe that annotated everything. This is what says
// the truncation notice appears only where something was truncated — and it
// covers the two edges the bufio.Scanner used to handle for free: a final line
// with no newline after it, and the \r a Windows-style writer leaves behind.
func TestOrdinaryLinesAreForwardedUnchanged(t *testing.T) {
	t.Parallel()

	forwarded := piping(t, "one\r\ntwo\nthree with no newline after it")

	assert.Equal(t, []string{
		"merchant      | one",
		"merchant      | two",
		"merchant      | three with no newline after it",
	}, forwarded,
		"a role's ordinary output is not the runner's to annotate, and the last line of a process that "+
			"died mid-sentence is the one worth reading")
}

// TestNothingPrintedSaysNothing is the other control, and it is here because the
// loop now has an explicit end rather than a Scan that returns false.
//
// A process that produced no output at all — which is most of them, most of the
// time — must not produce a line about it. An empty read is EOF on the first
// call, and EOF is deliberately not reported: it is the process finishing.
func TestNothingPrintedSaysNothing(t *testing.T) {
	t.Parallel()

	assert.Empty(t, piping(t, ""),
		"the runner would otherwise open every quiet process's pane with a line about the end of its "+
			"output, which is the noise that trains a reader to ignore the message that matters")
}
