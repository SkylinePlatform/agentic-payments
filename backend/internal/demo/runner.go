package demo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Timing for startup and shutdown.
const (
	// healthTimeout is how long a process gets to answer its health check
	// before the runner gives up on it and moves on. Generous, because the
	// frontend's first start compiles the app.
	healthTimeout = 30 * time.Second

	// healthInterval is how often the check is retried.
	healthInterval = 150 * time.Millisecond

	// stubGrace is how long a process marked unimplemented is given to exit
	// before the runner concludes it is actually running after all.
	stubGrace = 2 * time.Second

	// shutdownGrace is how long a process gets to stop after SIGTERM before
	// it is killed.
	shutdownGrace = 5 * time.Second
)

// State is what became of a process.
type State string

const (
	// StateRunning is up, and passed its health check if it had one.
	StateRunning State = "running"

	// StatePending is a stub that exited as expected. Not a failure — it is
	// work that has not been done yet, and the manifest names the issue.
	StatePending State = "pending"

	// StateFailed is a process that should have stayed up and did not, or
	// one that never answered its health check.
	StateFailed State = "failed"

	// StateMislabelled is a process marked unimplemented that is running
	// anyway. The manifest has gone stale.
	StateMislabelled State = "mislabelled"
)

// states is every value State takes, in the same spirit as kinds in
// manifest.go.
//
// It exists so that the presentation table in banner.go can be checked against
// it at start-up. A state the banner does not know is one the summary line
// counts as nothing, and a total that silently disagrees with the list above it
// is worse than no total at all.
var states = []State{StateRunning, StatePending, StateFailed, StateMislabelled}

// Status is one process's outcome.
type Status struct {
	Process Process
	State   State
	// Detail explains a failure, empty otherwise.
	Detail string
}

// Runner starts a manifest's processes and supervises them.
type Runner struct {
	manifest *Manifest
	root     string
	out      io.Writer
	// client is used for health checks. It has a short timeout because a
	// check that hangs would eat the whole health budget in one attempt.
	client *http.Client

	// healthTimeout is how long a process gets to become ready. It is a field
	// rather than a constant so a test can shorten it: a test that has to sit
	// through the production budget to watch a health check fail is a test
	// somebody eventually deletes for being slow.
	healthTimeout time.Duration

	// wg covers every goroutine started on a process's behalf: the two pipe
	// readers and the one waiting on the exit.
	wg sync.WaitGroup

	// logMu serialises writes to out, so two processes talking at once do
	// not interleave mid-line.
	logMu sync.Mutex
}

// Option configures a Runner.
type Option func(*Runner)

// WithHealthTimeout sets how long a process gets to answer its health check.
func WithHealthTimeout(d time.Duration) Option {
	return func(r *Runner) { r.healthTimeout = d }
}

// NewRunner returns a runner for m, resolving working directories against root
// and writing everything to out.
func NewRunner(m *Manifest, root string, out io.Writer, opts ...Option) *Runner {
	r := &Runner{
		manifest:      m,
		root:          root,
		out:           out,
		client:        &http.Client{Timeout: 2 * time.Second},
		healthTimeout: healthTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Start brings every process up, in manifest order, waiting on each health
// check before moving to the next. It returns what became of each one.
//
// A process that fails does not tear down the others, and that is true of one
// that exits long afterwards as well — the statuses below are what startup
// settled on, and nothing revises them later. Some of the manifest is still
// stubs, and a supervisor that quit when one child died would be useless for
// exactly the period this exists to cover.
//
// It returns once startup has settled. Call Wait to block until the processes
// stop, which is what leaves room to print a banner in between.
func (r *Runner) Start(ctx context.Context) []Status {
	var mu sync.Mutex
	statuses := make([]Status, len(r.manifest.Processes))

	for i, p := range r.manifest.Processes {
		statuses[i] = Status{Process: p, State: StateFailed, Detail: "did not start"}

		// Something already answering this process's health URL is not this
		// process. Without this check the runner starts a second one, watches
		// it lose the port and exit, then reports the *other* server's health
		// as its own — a green line for a process that is not running. That is
		// what a second `make demo`, or a dev server left over from earlier,
		// looks like, so it is the first thing to rule out rather than a rare
		// case.
		if p.Health != "" && r.healthy(ctx, p.Health) {
			detail := fmt.Sprintf("something is already answering %s — stop it, or change the port", p.Health)
			r.record(&mu, statuses, i, StateFailed, detail)
			r.logf(p, "not started: %s", detail)
			continue
		}

		cmd := exec.CommandContext(ctx, p.Command, p.Args...)
		cmd.Dir = p.Path(r.root)
		isolate(cmd)
		// Cancel with SIGTERM rather than SIGKILL, and give it a moment. A
		// role killed outright would skip whatever shutdown it has, and the
		// collector's is load-bearing: it ends the SSE streams so the frontend
		// is not left holding a socket nobody will ever write to.
		cmd.Cancel = func() error { return terminate(cmd) }
		cmd.WaitDelay = shutdownGrace

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			r.record(&mu, statuses, i, StateFailed, err.Error())
			continue
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			r.record(&mu, statuses, i, StateFailed, err.Error())
			continue
		}

		if err := cmd.Start(); err != nil {
			r.record(&mu, statuses, i, StateFailed, err.Error())
			r.logf(p, "could not start: %v", err)
			continue
		}

		r.wg.Go(func() { r.pipe(p, stdout) })
		r.wg.Go(func() { r.pipe(p, stderr) })

		exited := make(chan error, 1)
		r.wg.Go(func() { exited <- cmd.Wait() })

		state, detail := r.settle(ctx, p, exited)
		r.record(&mu, statuses, i, state, detail)
	}
	return statuses
}

// Wait blocks until every process has stopped and all their output has been
// copied.
//
// The commands were started with CommandContext, so cancelling the context
// passed to Start is what stops them; this only waits for that to finish.
// Waiting for the pipe readers as well is what stops the runner returning
// while a process's dying words are still in flight.
func (r *Runner) Wait() { r.wg.Wait() }

// settle decides what happened to a process shortly after starting it, and
// blocks only as long as that takes.
func (r *Runner) settle(ctx context.Context, p Process, exited <-chan error) (State, string) {
	if !p.Implemented {
		// Expected to exit. Give it a moment to do so; if it is still up
		// after that, the manifest is out of date and says so.
		select {
		case <-exited:
			return StatePending, ""
		case <-time.After(stubGrace):
			return StateMislabelled, "marked not implemented, but it is running"
		case <-ctx.Done():
			return StatePending, ""
		}
	}

	if p.Health == "" {
		// Nothing to wait for. Give it the same grace to fall over on its
		// own, so an immediate crash is reported now rather than looking
		// like a healthy start.
		select {
		case err := <-exited:
			return StateFailed, fmt.Sprintf("exited immediately: %v", err)
		case <-time.After(stubGrace):
			return StateRunning, ""
		case <-ctx.Done():
			return StateRunning, ""
		}
	}

	return r.awaitHealth(ctx, p, exited)
}

// awaitHealth polls p's health URL until it answers, the process dies, or the
// budget runs out.
func (r *Runner) awaitHealth(ctx context.Context, p Process, exited <-chan error) (State, string) {
	deadline, cancel := context.WithTimeout(ctx, r.healthTimeout)
	defer cancel()

	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	for {
		// Death first, and again after a passing check. A health check that
		// succeeds says something is listening; it does not say that this
		// process is the something. Confirming it is still alive is what makes
		// the answer about the process rather than about the port.
		select {
		case err := <-exited:
			return StateFailed, fmt.Sprintf("exited before answering %s: %v", p.Health, err)
		default:
		}

		if r.healthy(deadline, p.Health) {
			select {
			case err := <-exited:
				return StateFailed, fmt.Sprintf("answered %s and then exited: %v", p.Health, err)
			default:
				return StateRunning, ""
			}
		}

		select {
		case err := <-exited:
			return StateFailed, fmt.Sprintf("exited before answering %s: %v", p.Health, err)
		case <-ticker.C:
		case <-deadline.Done():
			if ctx.Err() != nil {
				return StateRunning, ""
			}
			return StateFailed, fmt.Sprintf("did not answer %s within %s", p.Health, r.healthTimeout)
		}
	}
}

// healthy reports whether the URL answers with a non-error status.
func (r *Runner) healthy(ctx context.Context, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode < http.StatusBadRequest
}

// How much of one line the runner forwards, and how much of the pipe it reads at
// a time.
//
// maxLine is inherited from the bufio.Scanner this loop replaced, so nothing that
// was forwarded before is truncated now. It is not a measured worst case in
// #251's sense and cannot be: no line any binary here emits comes near it — the
// widest is a start-up banner at about eighty columns — so what the number bounds
// is a role that has gone wrong, not one that is working. It stays generous for
// that reason: the point of forwarding a broken role's output is to be able to
// read what it said.
//
// **This one truncates where #251's caps refuse**, and that is the difference the
// issue turns on. Those five read a document a verdict is reached on, where a
// short read is a wrong answer. This reads a log line: dropping the tail of one
// costs a screenshot, and refusing would cost every line after it.
const (
	maxLine = 1024 * 1024

	// readerChunk is the Scanner's own starting buffer, kept because it is the
	// size that was already working. It bounds nothing a reader has to reason
	// about — a line longer than it arrives in several pieces and is joined —
	// so the only thing it trades is one more copy against maxLine of resident
	// memory per pipe, and there are two per process.
	readerChunk = 64 * 1024
)

// pipe copies a process's output, one prefixed line at a time.
//
// # An over-long line no longer ends the process's output
//
// Issue #275. This was a bufio.Scanner, whose Err was never read: a line past the
// token limit made Scan return false, the loop exited, and **that role's output
// stopped for the rest of the run** with nothing said. `make demo` kept going and
// the pane simply went quiet — which is the worst thing a runner can do, because
// the role looks idle rather than broken.
//
// A Scanner cannot be resumed after bufio.ErrTooLong, so this reads the pipe
// itself: it forwards the first maxLine bytes of a long line, says how much it
// dropped and which role produced it, and then carries on with the next line.
func (r *Runner) pipe(p Process, from io.Reader) {
	reader := bufio.NewReaderSize(from, readerChunk)
	for {
		line, dropped, err := readLine(reader)

		switch {
		case dropped > 0:
			r.logf(p, "%s… [%d more bytes on this line were dropped; the runner forwards %d per line]",
				line, dropped, maxLine)
		case len(line) > 0:
			r.logf(p, "%s", line)
		}

		if err != nil {
			// EOF is the process finishing. A closed pipe is cmd.Wait having got
			// there first, which is what a Ctrl-C looks like once WaitDelay
			// expires — neither is worth a line, and saying so on every shutdown
			// would train a reader to ignore this message.
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
				r.logf(p, "the runner stopped forwarding this process's output: %v", err)
			}
			return
		}
	}
}

// withoutDelimiter drops the line ending, and exactly the line ending.
//
// bytes.TrimRight over "\r\n" is the obvious spelling and is not the same rule:
// it would eat every trailing carriage return and newline, so a role printing
// `done\r\r` would have both taken. bufio.ScanLines — what this loop replaced —
// drops one \n and then one \r, and a change of behaviour nobody asked for is
// exactly what a replacement of working code must not smuggle in.
func withoutDelimiter(chunk []byte) []byte {
	chunk = bytes.TrimSuffix(chunk, []byte("\n"))
	return bytes.TrimSuffix(chunk, []byte("\r"))
}

// readLine reads one line, keeping at most maxLine bytes of it and reporting how
// many it discarded.
//
// The discarded bytes are consumed rather than left in the reader, which is what
// makes the next call the *next line* instead of the middle of this one.
func readLine(reader *bufio.Reader) (line []byte, dropped int, err error) {
	for {
		// ReadSlice's answer is only valid until the next read, so every branch
		// below copies out of it.
		chunk, read := reader.ReadSlice('\n')

		// The delimiter is not part of the line, and counting it would make the
		// runner report one more dropped byte than a role actually printed.
		// Trimming here rather than on the way out is what keeps that number
		// honest, since the bytes it counts are the ones that did not fit.
		done := !errors.Is(read, bufio.ErrBufferFull)
		if done {
			chunk = withoutDelimiter(chunk)
		}

		switch room := maxLine - len(line); {
		case room >= len(chunk):
			line = append(line, chunk...)
		case room > 0:
			line = append(line, chunk[:room]...)
			dropped += len(chunk) - room
		default:
			dropped += len(chunk)
		}

		// ErrBufferFull is the only error worth going round again for: it means
		// this line is longer than the reader's buffer, not that the pipe is
		// finished.
		if done {
			return line, dropped, read
		}
	}
}

// logf writes one prefixed line.
func (r *Runner) logf(p Process, format string, args ...any) {
	r.logMu.Lock()
	defer r.logMu.Unlock()
	_, _ = fmt.Fprintf(r.out, "%-13s | %s\n", p.Name, fmt.Sprintf(format, args...))
}

func (r *Runner) record(mu *sync.Mutex, into []Status, i int, state State, detail string) {
	mu.Lock()
	defer mu.Unlock()
	into[i].State = state
	into[i].Detail = detail
}
