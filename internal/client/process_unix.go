//go:build unix

package client

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// cancelWaitDelay bounds how long cmd.Wait blocks after cancellation before
// the runtime force-closes the child's I/O, so a wedged descendant holding a
// pipe open can never hang a lazydeck operation indefinitely.
const cancelWaitDelay = 10 * time.Second

// configureCancellation makes cmd start in its own process group and, on
// context cancellation, SIGKILLs the entire group rather than just the direct
// child. lazydeck launches `uv`, which execs Python, which shells out to
// `ssh`/`rsync`; without a process-group kill those grandchildren would be
// orphaned and keep running (holding SSH sessions / partial rsync transfers
// open) after a timeout or user cancel. Modeled on the same pattern already
// used for custom commands in the TUI.
func configureCancellation(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.WaitDelay = cancelWaitDelay
	cmd.Cancel = func() error {
		return killProcessGroup(cmd)
	}
}

// killProcessGroup sends SIGKILL to the child's whole process group. A
// negative PID targets the group (see kill(2)); ESRCH means everything has
// already exited, which we normalize to os.ErrProcessDone so exec treats the
// cancellation as a clean stop.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
