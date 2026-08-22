//go:build unix

package server

import (
	"os"
	"syscall"
)

// lockExclusiveNonblocking takes an exclusive advisory lock on f via
// flock(2), returning immediately with an error if another process already
// holds it instead of blocking. The lock is tied to the open file
// descriptor, not the path, so it is automatically released by the OS when
// f is closed or this process exits or is killed — no PID-liveness
// heuristic needed to detect a stale lock left by a crashed instance.
func lockExclusiveNonblocking(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
