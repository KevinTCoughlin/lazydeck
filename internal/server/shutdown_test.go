package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// gateClient blocks Deploy until the job context is cancelled. The pause
// after cancellation makes a shutdown that only signals cancel, and does
// not wait for the job goroutine, visible to the test: Run must not
// return while Deploy is still in that pause. That wait is what reaps the
// uv/ssh/rsync process group before the serve process exits.
type gateClient struct {
	fakeClient
	started chan struct{}
	done    chan struct{}
}

func (g *gateClient) Deploy(ctx context.Context, _, _, _, _ string, _ bool, _ []string) error {
	close(g.started)
	<-ctx.Done()
	time.Sleep(200 * time.Millisecond)
	close(g.done)
	return ctx.Err()
}

func TestRunShutdownWaitsForInFlightJobWithEventStreamOpen(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	gate := &gateClient{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(ctx, gate, &config.Config{Devices: []config.Device{{Name: "deck", Machine: "m"}}}, Options{
			AuditLogPath: auditPath,
		})
	}()
	t.Cleanup(cancel)

	connPath, err := connectionFilePath()
	if err != nil {
		t.Fatal(err)
	}
	info := waitForConnectionFile(t, connPath)

	deployCtx, deployCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer deployCancel()
	body, err := json.Marshal(map[string]string{
		"game_id":   "mygame",
		"directory": "/tmp/build",
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(deployCtx, http.MethodPost, info.BaseURL+"/v1/devices/deck/deployments", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+info.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		msg, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy status = %d, body %s", resp.StatusCode, msg)
	}
	var created struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Job.ID == "" {
		t.Fatal("expected a job id")
	}

	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		t.Fatal("deploy never started")
	}

	// Hold the event stream open. Shutdown must not sit on this handler
	// until the HTTP budget is gone and then skip waiting for the job.
	eventsCtx, eventsCancel := context.WithCancel(context.Background())
	defer eventsCancel()
	eventsReq, err := http.NewRequestWithContext(eventsCtx, http.MethodGet, info.BaseURL+"/v1/jobs/"+created.Job.ID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	eventsReq.Header.Set("Authorization", "Bearer "+info.Token)
	eventsResult := make(chan struct {
		resp *http.Response
		err  error
	}, 1)
	go func() {
		resp, err := http.DefaultClient.Do(eventsReq)
		eventsResult <- struct {
			resp *http.Response
			err  error
		}{resp, err}
	}()
	var eventsResp *http.Response
	select {
	case got := <-eventsResult:
		if got.err != nil {
			t.Fatal(got.err)
		}
		eventsResp = got.resp
	case <-time.After(5 * time.Second):
		t.Fatal("job event stream did not connect")
	}
	defer eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", eventsResp.StatusCode)
	}
	go func() { _, _ = io.Copy(io.Discard, eventsResp.Body) }()

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return; an open job event stream held shutdown past the point where the in-flight job is reaped")
	}
	select {
	case <-gate.done:
	default:
		t.Fatal("Run returned while deploy was still unwinding; shutdown did not wait for the job")
	}
}

func waitForConnectionFile(t *testing.T, path string) ConnectionInfo {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var info ConnectionInfo
			if json.Unmarshal(data, &info) == nil && info.Token != "" && info.BaseURL != "" {
				return info
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection file %s never became readable: %v", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
