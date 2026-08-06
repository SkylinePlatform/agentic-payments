//go:build !unix

// The fallback for platforms without process groups, which today means Windows.
//
// It exists so this package compiles everywhere rather than to make `make demo`
// a supported thing on Windows — the demo also needs a Node toolchain, a shell
// and nine ports, and none of that is claimed here either. What the tag buys is
// that a contributor on Windows can build, vet and test the module, including
// the packages that merely import this one.
//
// Both functions below are honest about being weaker than their Unix
// counterparts. Neither pretends to reach a descendant, because the equivalent
// on Windows is a job object rather than a signal, and writing half of one here
// would be worse than saying it is not implemented.

package demo

import (
	"errors"
	"os"
	"os/exec"
)

// isolate does nothing.
//
// Windows has CREATE_NEW_PROCESS_GROUP, but it changes only where Ctrl-C and
// Ctrl-Break are delivered — it does not give terminate a group to signal, which
// is what isolate exists for on Unix. Setting it would look like the Unix
// behaviour and deliver none of it.
func isolate(*exec.Cmd) {}

// terminate kills the process, and only the process.
//
// SIGTERM has no Windows equivalent that a Go program can send to another
// process, so there is no graceful stop to ask for: the collector will not get
// to close its SSE streams, and anything the child itself started keeps running.
// A dead child is still better than an orphan holding a port, which is what
// returning an error and stopping would leave behind.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Already gone is not a failure — it is the normal way a stub exits, the
	// same case ESRCH covers on Unix.
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
