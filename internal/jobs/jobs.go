// Package jobs implements the async job model used by the local service API
// (see internal/server): every mutating devkit operation (deploy, log sync)
// runs as a queued -> running -> succeeded|failed|cancelled state machine
// instead of blocking the HTTP request for the operation's full duration.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
)

// Status is a job's position in the queued -> running -> terminal lifecycle.
type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

func (s Status) terminal() bool {
	return s == Succeeded || s == Failed || s == Cancelled
}

// Error is a structured job failure, mirroring client.CLIError's coarse
// kinds (auth-failed / unreachable / invalid-input / script-error /
// unknown) plus job-lifecycle kinds like "cancelled" and "internal", so API
// responses can carry a stable machine-readable reason instead of a
// human-readable string an engine plugin would have to pattern-match.
type Error struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// ErrDeviceBusy is returned by Submit when the target device already has a
// queued or running job. #13 calls for "one mutating operation per device";
// rejecting a second submission immediately (rather than silently queueing
// it behind the first) keeps job ordering obvious to the caller and avoids
// an unbounded per-device backlog.
var ErrDeviceBusy = errors.New("device has a job already queued or running")

// ErrNotFound is returned when a job ID is unknown to the Manager.
var ErrNotFound = errors.New("job not found")

// Event is one point-in-time state change of a job, recorded so an SSE
// subscriber that connects mid-run can be replayed from any point and a
// subscriber connecting after completion still sees the full history.
type Event struct {
	Seq     int       `json:"seq"`
	Status  Status    `json:"status"`
	Message string    `json:"message,omitempty"`
	Error   *Error    `json:"error,omitempty"`
	Time    time.Time `json:"time"`
}

// Snapshot is the API-facing view of a Job at one instant.
type Snapshot struct {
	ID          string     `json:"id"`
	DeviceID    string     `json:"device_id"`
	Operation   string     `json:"operation"`
	Status      Status     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	LastMessage string     `json:"last_message,omitempty"`
	Error       *Error     `json:"error,omitempty"`
}

// Job is one submitted operation. All mutable fields are guarded by mu so
// the owning goroutine (running the operation) and readers (HTTP handlers,
// the SSE stream) can touch it concurrently.
type Job struct {
	ID        string
	DeviceID  string
	Operation string
	CreatedAt time.Time

	mu         sync.Mutex
	status     Status
	startedAt  time.Time
	finishedAt time.Time
	lastErr    *Error
	events     []Event
	changed    chan struct{} // closed and replaced on every appended event

	cancel context.CancelFunc
}

// Snapshot returns the job's current state.
func (j *Job) Snapshot() Snapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := Snapshot{
		ID:        j.ID,
		DeviceID:  j.DeviceID,
		Operation: j.Operation,
		Status:    j.status,
		CreatedAt: j.CreatedAt,
		Error:     j.lastErr,
	}
	if !j.startedAt.IsZero() {
		t := j.startedAt
		s.StartedAt = &t
	}
	if !j.finishedAt.IsZero() {
		t := j.finishedAt
		s.FinishedAt = &t
	}
	if n := len(j.events); n > 0 {
		s.LastMessage = j.events[n-1].Message
	}
	return s
}

// EventsSince returns events with Seq > after, plus the wait channel to
// select on for the next change (nil if the job is already terminal and
// every event has been returned, telling the caller not to wait again).
func (j *Job) EventsSince(after int) (events []Event, wait <-chan struct{}) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, e := range j.events {
		if e.Seq > after {
			events = append(events, e)
		}
	}
	if j.status.terminal() {
		// Every event up to and including the terminal one is already in
		// events; there is nothing further to wait for.
		return events, nil
	}
	return events, j.changed
}

func (j *Job) appendEvent(status Status, message string, jobErr *Error) {
	j.mu.Lock()
	seq := len(j.events) + 1
	j.events = append(j.events, Event{Seq: seq, Status: status, Message: message, Error: jobErr, Time: time.Now()})
	j.status = status
	j.lastErr = jobErr
	ch := j.changed
	j.changed = make(chan struct{})
	j.mu.Unlock()
	close(ch)
}

func (j *Job) markStarted() {
	j.mu.Lock()
	j.startedAt = time.Now()
	j.mu.Unlock()
	j.appendEvent(Running, "started", nil)
}

func (j *Job) markFinished(status Status, message string, jobErr *Error) {
	j.mu.Lock()
	j.finishedAt = time.Now()
	j.mu.Unlock()
	j.appendEvent(status, message, jobErr)
}

// Progress records a human-readable progress note without changing status.
// The underlying Python bridge calls (Deploy, SyncLogs) are blocking and
// report no incremental progress of their own, so in practice this MVP only
// ever emits the "started" and terminal events; Progress exists so a future,
// more granular bridge call can report real progress without an API change.
func (j *Job) Progress(message string) {
	j.mu.Lock()
	status := j.status
	j.mu.Unlock()
	j.appendEvent(status, message, nil)
}

// Run is the operation a job executes. It must respect ctx cancellation and
// report progress (optionally) through report.
type Run func(ctx context.Context, report func(message string)) error

// maxRetainedJobs bounds how many jobs a Manager keeps once they've
// finished, so a long-lived `serve` process's job history doesn't grow
// without bound as deploys and log-syncs accumulate. Queued/running jobs
// are never evicted regardless of this cap.
const maxRetainedJobs = 500

// Manager runs submitted jobs with per-device serialization and bounded
// global concurrency, per #13's job model.
type Manager struct {
	maxConcurrent int
	sem           chan struct{}
	wg            sync.WaitGroup // tracks every execute() goroutine, for Shutdown

	mu         sync.Mutex
	jobs       map[string]*Job
	jobOrder   []string          // job IDs in creation order, for retention pruning
	deviceBusy map[string]string // deviceID -> occupying job ID

	// onComplete, if set, is called with every job's final Snapshot once it
	// reaches a terminal state (see execute). It's used to fan out
	// completion notifications (e.g. chat webhooks) without the job
	// execution path knowing about notify.Sender.
	onComplete func(Snapshot)
}

// SetOnComplete registers fn to be called with a job's final Snapshot
// whenever a job finishes (succeeded, failed, or cancelled). fn is called
// synchronously from the job's own goroutine, so it must not block or
// panic; a caller wanting to notify a slow endpoint should do so
// asynchronously inside fn.
func (m *Manager) SetOnComplete(fn func(Snapshot)) {
	m.mu.Lock()
	m.onComplete = fn
	m.mu.Unlock()
}

// NewManager returns a Manager that runs at most maxConcurrent jobs at once
// across all devices (a device-level lock further limits each device to one
// job regardless of this bound). maxConcurrent <= 0 is treated as 1.
func NewManager(maxConcurrent int) *Manager {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Manager{
		maxConcurrent: maxConcurrent,
		sem:           make(chan struct{}, maxConcurrent),
		jobs:          make(map[string]*Job),
		deviceBusy:    make(map[string]string),
	}
}

// Submit creates and starts a job for deviceID running run, or returns
// ErrDeviceBusy if deviceID already has a queued/running job.
func (m *Manager) Submit(deviceID, operation string, run Run) (*Job, error) {
	m.mu.Lock()
	if _, busy := m.deviceBusy[deviceID]; busy {
		m.mu.Unlock()
		return nil, ErrDeviceBusy
	}

	id, err := newID()
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("generating job id: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:        id,
		DeviceID:  deviceID,
		Operation: operation,
		CreatedAt: time.Now(),
		status:    Queued,
		changed:   make(chan struct{}),
		cancel:    cancel,
	}
	m.jobs[id] = job
	m.jobOrder = append(m.jobOrder, id)
	m.deviceBusy[deviceID] = id
	m.pruneLocked()
	m.mu.Unlock()

	m.wg.Add(1)
	go m.execute(ctx, job, run)
	return job, nil
}

// pruneLocked evicts the oldest terminal jobs once more than
// maxRetainedJobs are retained. Must be called with m.mu held.
func (m *Manager) pruneLocked() {
	toRemove := len(m.jobOrder) - maxRetainedJobs
	if toRemove <= 0 {
		return
	}
	kept := make([]string, 0, len(m.jobOrder))
	for _, id := range m.jobOrder {
		if toRemove > 0 {
			if job, ok := m.jobs[id]; ok && job.Snapshot().Status.terminal() {
				delete(m.jobs, id)
				toRemove--
				continue
			}
		}
		kept = append(kept, id)
	}
	m.jobOrder = kept
}

func (m *Manager) execute(ctx context.Context, job *Job, run Run) {
	defer m.wg.Done()
	defer m.releaseDevice(job.DeviceID)

	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		m.finish(job, Cancelled, "cancelled while queued", nil)
		return
	}

	// The semaphore send and ctx.Done() above race when both are ready at
	// once (select among ready cases is pseudo-random), so a job cancelled
	// at that exact instant could still win the slot. Recheck ctx.Err()
	// before doing anything observable.
	if ctx.Err() != nil {
		m.finish(job, Cancelled, "cancelled while queued", nil)
		return
	}

	job.markStarted()
	err := run(ctx, job.Progress)

	// ctx.Err() is authoritative for cancellation: a real client.Client
	// operation cancelled mid-run kills the uv/ssh process group (see
	// internal/client/process_unix.go) and returns a wrapped process error,
	// not context.Canceled, so branching on err's shape alone would
	// misreport a cancelled deploy as failed.
	switch {
	case ctx.Err() != nil:
		m.finish(job, Cancelled, "cancelled", nil)
	case err == nil:
		m.finish(job, Succeeded, "completed", nil)
	default:
		m.finish(job, Failed, "failed", classifyError(err))
	}
}

// finish marks job terminal and invokes the registered onComplete hook, if
// any, with its final Snapshot. It is the single path to a terminal state
// (including both early-cancel returns in execute) so onComplete is called
// consistently for every terminal status, matching SetOnComplete's
// documented behavior. onComplete is called under a recover guard: a panic
// in a notification hook must not take down the whole serve process.
func (m *Manager) finish(job *Job, status Status, message string, jobErr *Error) {
	job.markFinished(status, message, jobErr)

	m.mu.Lock()
	onComplete := m.onComplete
	m.mu.Unlock()
	if onComplete == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("lazydeck: job completion hook panicked: %v", r)
		}
	}()
	onComplete(job.Snapshot())
}

// Shutdown cancels every tracked job (a no-op on ones already terminal)
// and waits for their goroutines to finish, or for ctx to expire first.
// Callers (server.Run on process shutdown) use this so in-flight deploy/
// log-sync subprocess work is cancelled and awaited rather than left
// running detached after the serve process has already exited.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()
	for _, j := range jobs {
		j.cancel()
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) releaseDevice(deviceID string) {
	m.mu.Lock()
	delete(m.deviceBusy, deviceID)
	m.mu.Unlock()
}

// classifyError extracts a stable Kind from run's error when it carries one
// (*client.CLIError does), defaulting to "unknown" so every job failure has
// a machine-readable kind, never just free text a caller would have to
// pattern-match.
//
// Anything that isn't a *client.CLIError (a failed `uv run` invocation, or
// malformed cli.py output) can carry raw subprocess stdout/stderr —
// potentially SSH paths, hostnames, or other process output — so it must
// not be echoed back through the API/SSE verbatim. That case is logged
// locally for an operator to see and replaced with a stable, generic
// message instead.
func classifyError(err error) *Error {
	var cliErr *client.CLIError
	if errors.As(err, &cliErr) {
		return &Error{Kind: cliErr.Kind, Message: cliErr.Message}
	}
	log.Printf("job failed with an unclassified error: %v", err)
	return &Error{Kind: "unknown", Message: "the devkit operation failed unexpectedly; see the lazydeck serve process log for details"}
}

// Get returns the job with the given ID, or ErrNotFound.
func (m *Manager) Get(id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return job, nil
}

// Cancel requests cancellation of the job with the given ID. It is
// idempotent: cancelling an already-terminal job is a no-op.
func (m *Manager) Cancel(id string) (*Job, error) {
	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	job.cancel()
	return job, nil
}

func newID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return "job_" + hex.EncodeToString(buf[:]), nil
}
