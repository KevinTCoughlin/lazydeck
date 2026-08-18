//go:build unix

package client

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestConfigureCancellationKillsProcessGroup guards finding #3: cancelling a
// run must tear down the whole process tree (uv -> python -> ssh/rsync), not
// just the direct child. We model that tree with a shell that backgrounds a
// long-lived grandchild; if only the shell were killed, the grandchild would
// survive. With process-group cancellation it must be reaped too.
func TestConfigureCancellationKillsProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Background a sleep (the "grandchild"), record its PID, then wait on it.
	// Killing only the shell would orphan the sleep; a process-group kill
	// takes it down too.
	script := "sleep 120 & echo $! > " + pidFile + "; wait"
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	configureCancellation(cmd)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected configureCancellation to request a new process group")
	}
	if cmd.Cancel == nil {
		t.Fatal("expected configureCancellation to install a Cancel hook")
	}
	if cmd.WaitDelay <= 0 {
		t.Fatal("expected a bounded WaitDelay backstop")
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	grandchildPID := waitForPID(t, pidFile)

	cancel()
	_ = cmd.Wait() // returns once the group is torn down / WaitDelay elapses

	if !waitForProcessGone(grandchildPID, 5*time.Second) {
		// Clean up the leak so we don't strand a process, then fail.
		_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
		t.Fatalf("grandchild pid %d survived cancellation (process group not killed)", grandchildPID)
	}
}

func waitForPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if s := strings.TrimSpace(string(data)); s != "" {
				pid, err := strconv.Atoi(s)
				if err == nil && pid > 0 {
					return pid
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for grandchild pid file")
	return 0
}

// waitForProcessGone reports whether pid is no longer signalable (i.e. dead)
// within the timeout. syscall.Kill(pid, 0) probes existence without signaling.
func waitForProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true // ESRCH: process no longer exists
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
