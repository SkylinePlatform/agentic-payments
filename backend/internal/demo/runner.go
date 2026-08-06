package demo

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
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
// A process that fails does not tear down the others. Seven of the nine are
// stubs today, and a supervisor that quit when one child died would be useless
// for exactly the period this exists to cover.
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

// pipe copies a process's output, one prefixed line at a time.
func (r *Runner) pipe(p Process, from io.Reader) {
	scanner := bufio.NewScanner(from)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		r.logf(p, "%s", scanner.Text())
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
