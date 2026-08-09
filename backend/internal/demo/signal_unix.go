//go:build unix

// Process-group signalling, which is a Unix concept and named as one here.
//
// The constraint is the point of the file split. syscall.SysProcAttr has no
// Setpgid member outside Unix and syscall.Kill does not exist there at all, so
// before the tag this package simply did not compile for GOOS=windows — and
// nothing said so, because CI is Linux only and a compile error on a platform
// nobody builds is invisible until somebody tries. signal_other.go is the
// fallback, and it is deliberately weaker rather than absent.

package demo

import (
	"errors"
	"os/exec"
	"syscall"
)

// isolate puts a process in its own process group.
//
// Two things follow, and both are wanted. A signal sent to the group reaches
// every descendant, which is the only way to stop `npm run dev`: npm execs a
// shell that execs vite, and a SIGTERM delivered to npm alone leaves the node
// process holding the port. Killing the group is what a supervisor is for, and
// without it `make demo` twice in a row fails on a port that the first run
// appeared to release.
//
// It also detaches the child from the terminal's foreground group, so Ctrl-C
// reaches the runner and nothing else. That makes shutdown something the runner
// performs in a known order rather than a race between every process in the
// manifest, all signalled at once.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate asks a process and everything it started to stop.
//
// SIGTERM rather than SIGKILL, because a killed process skips whatever shutdown
// it has and the collector's is load-bearing: it ends the SSE streams so the
// frontend is not left holding a socket nobody will ever write to again. The
// runner sets WaitDelay, so anything that ignores this is killed shortly after.
//
// The negative PID is the process group, which is the point — see isolate.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		// Already gone between the check and the signal, which is the normal
		// way a stub exits.
		return nil
	}
	return err
}
