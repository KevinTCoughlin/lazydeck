package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
)

func waitTerminal(t *testing.T, job *Job, timeout time.Duration) Snapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		snap := job.Snapshot()
		if snap.Status == Succeeded || snap.Status == Failed || snap.Status == Cancelled {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach a terminal state within %s (last status %s)", job.ID, timeout, snap.Status)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestSubmitSucceeds(t *testing.T) {
	m := NewManager(2)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		report("halfway")
		return nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Status != Succeeded {
		t.Fatalf("status = %s, want succeeded", snap.Status)
	}
	events, wait := job.EventsSince(0)
	if wait != nil {
		t.Fatalf("wait channel should be nil once terminal")
	}
	if len(events) < 3 { // started, progress, completed
		t.Fatalf("events = %#v, want at least 3", events)
	}
}

func TestSubmitFailureDefaultsToUnknownKind(t *testing.T) {
	m := NewManager(1)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		return errors.New("device offline")
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Status != Failed {
		t.Fatalf("status = %s, want failed", snap.Status)
	}
	if snap.Error == nil || snap.Error.Kind != "unknown" {
		t.Fatalf("error = %#v, want kind unknown for a plain error", snap.Error)
	}
}

func TestDeviceBusyRejectsSecondSubmission(t *testing.T) {
	m := NewManager(2)
	started := make(chan struct{})
	release := make(chan struct{})
	_, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	<-started

	if _, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error { return nil }); !errors.Is(err, ErrDeviceBusy) {
		t.Fatalf("second Submit error = %v, want ErrDeviceBusy", err)
	}

	// A different device is unaffected by deck-1's lock.
	other, err := m.Submit("deck-2", "deploy", func(ctx context.Context, report func(string)) error { return nil })
	if err != nil {
		t.Fatalf("Submit for deck-2: %v", err)
	}
	waitTerminal(t, other, time.Second)

	close(release)
}

func TestCancelWhileRunningStopsTheJob(t *testing.T) {
	m := NewManager(1)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	// Give execute a moment to reach the running state before cancelling.
	deadline := time.Now().Add(time.Second)
	for job.Snapshot().Status != Running {
		if time.Now().After(deadline) {
			t.Fatalf("job never reached running")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Status != Cancelled {
		t.Fatalf("status = %s, want cancelled", snap.Status)
	}
}

func TestCancelWhileQueuedNeverRuns(t *testing.T) {
	m := NewManager(1)
	blockerStarted := make(chan struct{})
	blockerRelease := make(chan struct{})
	blocker, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		close(blockerStarted)
		<-blockerRelease
		return nil
	})
	if err != nil {
		t.Fatalf("Submit blocker: %v", err)
	}
	<-blockerStarted // blocker now holds the manager's one global concurrency slot

	// deck-2 shares that slot, so this job queues behind the blocker despite
	// targeting a different device.
	ran := false
	queued, err := m.Submit("deck-2", "deploy", func(ctx context.Context, report func(string)) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("Submit queued: %v", err)
	}
	if _, err := m.Cancel(queued.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	snap := waitTerminal(t, queued, time.Second)
	if snap.Status != Cancelled {
		t.Fatalf("status = %s, want cancelled", snap.Status)
	}

	close(blockerRelease)
	waitTerminal(t, blocker, time.Second)
	if ran {
		t.Fatalf("queued job's run func executed despite being cancelled before its turn")
	}
}

func TestGetUnknownJob(t *testing.T) {
	m := NewManager(1)
	if _, err := m.Get("job_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v, want ErrNotFound", err)
	}
	if _, err := m.Cancel("job_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel error = %v, want ErrNotFound", err)
	}
}

func TestClassifyErrorFromCLIError(t *testing.T) {
	m := NewManager(1)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		return &client.CLIError{Kind: "unreachable", Message: "no route to host"}
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Error == nil || snap.Error.Kind != "unreachable" || snap.Error.Message != "no route to host" {
		t.Fatalf("error = %#v, want kind unreachable / message no route to host", snap.Error)
	}
}
