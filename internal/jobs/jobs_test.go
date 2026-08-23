package jobs

import (
	"context"
	"errors"
	"fmt"
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

func TestSubmitFailureFromNonCLIErrorIsSanitized(t *testing.T) {
	m := NewManager(1)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		return fmt.Errorf("uv run failed: exit status 1\nstderr: ssh: connect to host 10.0.0.5 user root key /home/alice/.ssh/id_devkit")
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Error == nil {
		t.Fatal("expected an error")
	}
	if snap.Error.Message == "" || snap.Error.Message == "uv run failed: exit status 1\nstderr: ssh: connect to host 10.0.0.5 user root key /home/alice/.ssh/id_devkit" {
		t.Fatalf("raw subprocess error leaked through the API: %q", snap.Error.Message)
	}
}

func TestCancelDuringRunClassifiesAsCancelledRegardlessOfErrShape(t *testing.T) {
	m := NewManager(1)
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		<-ctx.Done()
		// A real client.Client cancellation kills a subprocess and returns
		// a wrapped process error, not context.Canceled itself (see
		// internal/client/process_unix.go) — simulate that shape here.
		return fmt.Errorf("uv run failed: signal: killed")
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for job.Snapshot().Status != Running {
		if time.Now().After(deadline) {
			t.Fatal("job never reached running")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := m.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	snap := waitTerminal(t, job, time.Second)
	if snap.Status != Cancelled {
		t.Fatalf("status = %s, want cancelled even though the run error wasn't context.Canceled", snap.Status)
	}
}

func TestShutdownCancelsRunningJobsAndWaits(t *testing.T) {
	m := NewManager(1)
	started := make(chan struct{})
	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if snap := job.Snapshot(); snap.Status != Cancelled {
		t.Fatalf("status = %s, want cancelled", snap.Status)
	}
}

func TestShutdownTimesOutIfAJobIgnoresCancellation(t *testing.T) {
	m := NewManager(1)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release) // let the goroutine exit after the test observes the timeout
	if _, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
	}
}

func TestPruneEvictsOldestTerminalJobsBeyondCap(t *testing.T) {
	m := NewManager(50)
	total := maxRetainedJobs + 5
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		device := fmt.Sprintf("dev-%d", i)
		job, err := m.Submit(device, "deploy", func(ctx context.Context, report func(string)) error { return nil })
		if err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
		waitTerminal(t, job, time.Second)
		ids = append(ids, job.ID)
	}

	if _, err := m.Get(ids[0]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oldest job should have been pruned once the cap was exceeded, got err=%v", err)
	}
	if _, err := m.Get(ids[len(ids)-1]); err != nil {
		t.Fatalf("newest job should still be retained: %v", err)
	}
}

func TestSetOnCompleteCalledWithFinalSnapshot(t *testing.T) {
	m := NewManager(1)

	got := make(chan Snapshot, 1)
	m.SetOnComplete(func(snap Snapshot) { got <- snap })

	job, err := m.Submit("deck-1", "deploy", func(ctx context.Context, report func(string)) error {
		return errors.New("boom")
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitTerminal(t, job, time.Second)

	select {
	case snap := <-got:
		if snap.Status != Failed {
			t.Fatalf("onComplete snapshot status = %s, want failed", snap.Status)
		}
		if snap.DeviceID != "deck-1" || snap.Operation != "deploy" {
			t.Fatalf("onComplete snapshot = %#v, want deck-1/deploy", snap)
		}
	case <-time.After(time.Second):
		t.Fatal("onComplete was not called within 1s")
	}
}
