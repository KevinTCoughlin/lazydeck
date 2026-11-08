package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/apikey"
	"github.com/kevintcoughlin/lazydeck/internal/audit"
	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// syncBuffer is a goroutine-safe audit sink: job.finished entries are
// written from job goroutines while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) entries(t *testing.T) []audit.Entry {
	t.Helper()
	var out []audit.Entry
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var e audit.Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, e)
	}
	return out
}

type authFixture struct {
	srv      *Server
	ts       *httptest.Server
	keysPath string
	log      *syncBuffer
}

func newAuthFixture(t *testing.T, fc *fakeClient) *authFixture {
	t.Helper()
	keysPath := filepath.Join(t.TempDir(), "api-keys.json")
	s, err := New(fc, &config.Config{Devices: []config.Device{{Name: "deck", Machine: "deck.local"}}}, testToken)
	if err != nil {
		t.Fatal(err)
	}
	buf := &syncBuffer{}
	s.keys = apikey.NewStore(keysPath)
	s.audit = audit.New(buf)
	s.sessionUser = "owner"
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &authFixture{srv: s, ts: ts, keysPath: keysPath, log: buf}
}

func (f *authFixture) key(t *testing.T, name string, scopes ...apikey.Scope) (string, apikey.Key) {
	t.Helper()
	token, key, err := apikey.Create(f.keysPath, name, scopes, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token, key
}

func findEntry(entries []audit.Entry, action, result string) (audit.Entry, bool) {
	for _, e := range entries {
		if e.Action == action && e.Result == result {
			return e, true
		}
	}
	return audit.Entry{}, false
}

func waitForEntry(t *testing.T, log *syncBuffer, action, result string) audit.Entry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if e, ok := findEntry(log.entries(t), action, result); ok {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %s/%s audit entry; log:\n%s", action, result, log.String())
	return audit.Entry{}
}

func TestReadScopedKeyCanReadButNotDeploy(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	token, key := f.key(t, "dashboard", apikey.ScopeRead)

	resp := doRequest(t, f.ts, http.MethodGet, "/v1/devices", nil, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/devices = %d, want 200", resp.StatusCode)
	}

	resp = doRequest(t, f.ts, http.MethodPost, "/v1/devices/deck/deployments",
		map[string]any{"game_id": "game", "directory": "/tmp/build"}, token)
	body := decodeBody[errorEnvelope](t, resp)
	if resp.StatusCode != http.StatusForbidden || body.Error.Kind != "forbidden" {
		t.Fatalf("deploy with read-only key = %d %+v, want 403 forbidden", resp.StatusCode, body)
	}

	e, ok := findEntry(f.log.entries(t), "deploy", audit.ResultDenied)
	if !ok {
		t.Fatalf("no denied deploy entry; log:\n%s", f.log.String())
	}
	if e.Actor != audit.ActorAPIKey || e.ActorID != key.ID || e.ActorName != "dashboard" || e.Device != "deck" || e.Status != http.StatusForbidden {
		t.Fatalf("denied entry = %+v", e)
	}
	// Reads are not audited.
	if _, ok := findEntry(f.log.entries(t), "devices.list", audit.ResultOK); ok {
		t.Fatal("reads should not be audited")
	}
}

func TestDeployKeyIsAuditedThroughJobCompletion(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	token, key := f.key(t, "ci-build", apikey.ScopeRead, apikey.ScopeDeploy)

	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/deck/deployments",
		map[string]any{"game_id": "game", "directory": "/tmp/build", "argv": []string{"--secret=hunter2"}}, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("deploy = %d, want 202", resp.StatusCode)
	}

	accepted := waitForEntry(t, f.log, "deploy", audit.ResultAccepted)
	if accepted.ActorID != key.ID || accepted.GameID != "game" || accepted.Directory != "/tmp/build" || accepted.JobID == "" || accepted.Device != "deck" {
		t.Fatalf("accepted entry = %+v", accepted)
	}
	finished := waitForEntry(t, f.log, "job.finished", "succeeded")
	if finished.JobID != accepted.JobID || finished.ActorID != key.ID || finished.ActorName != "ci-build" || finished.Operation != "deploy" {
		t.Fatalf("finished entry = %+v", finished)
	}

	raw := f.log.String()
	for _, secret := range []string{token, key.SHA256, testToken, "hunter2"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("audit log contains a secret (%q):\n%s", secret, raw)
		}
	}
}

func TestSessionTokenHasEveryScopeAndIsAttributed(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/deck/pair", nil, testToken)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair with session token = %d", resp.StatusCode)
	}
	e, ok := findEntry(f.log.entries(t), "devices.pair", audit.ResultOK)
	if !ok || e.Actor != audit.ActorSession || e.ActorName != "owner" || e.Device != "deck" {
		t.Fatalf("pair entry = %+v (found %v)", e, ok)
	}
}

func TestRevokedKeyIsRejectedAndAudited(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	token, key := f.key(t, "old-ci", apikey.ScopeRead)
	if err := apikey.Revoke(f.keysPath, key.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	resp := doRequest(t, f.ts, http.MethodGet, "/v1/health", nil, token)
	body := decodeBody[errorEnvelope](t, resp)
	if resp.StatusCode != http.StatusUnauthorized || body.Error.Kind != "unauthorized" {
		t.Fatalf("revoked key = %d %+v", resp.StatusCode, body)
	}
	e, ok := findEntry(f.log.entries(t), "auth", audit.ResultUnauthorized)
	if !ok || e.ActorID != key.ID || e.Reason != "API key revoked" {
		t.Fatalf("auth entry = %+v (found %v)", e, ok)
	}
	if strings.Contains(f.log.String(), token) {
		t.Fatal("audit log contains the presented key")
	}
}

func TestWrongSecretForKnownIDIsRejected(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{})
	_, key := f.key(t, "ci", apikey.ScopeRead)
	forged := "ldk_" + key.ID + "_" + strings.Repeat("a", 64)
	resp := doRequest(t, f.ts, http.MethodGet, "/v1/health", nil, forged)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged key = %d, want 401", resp.StatusCode)
	}
}

func TestKeyCannotCancelAnotherPrincipalsJob(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{deployDelay: 2 * time.Second})
	token, _ := f.key(t, "ci", apikey.ScopeRead, apikey.ScopeDeploy, apikey.ScopeJobsCancel)

	// The owner starts a deploy with the session token...
	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/deck/deployments",
		map[string]any{"game_id": "game", "directory": "/tmp/build"}, testToken)
	accepted := decodeBody[struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}](t, resp)

	// ...which a CI key, even with jobs:cancel, may not cancel.
	resp = doRequest(t, f.ts, http.MethodDelete, "/v1/jobs/"+accepted.Job.ID, nil, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cancel another principal's job = %d, want 403", resp.StatusCode)
	}
	e, ok := findEntry(f.log.entries(t), "jobs.cancel", audit.ResultDenied)
	if !ok || e.JobID != accepted.Job.ID || e.Device != "deck" {
		t.Fatalf("cancel entry = %+v (found %v)", e, ok)
	}

	// The owner can.
	resp = doRequest(t, f.ts, http.MethodDelete, "/v1/jobs/"+accepted.Job.ID, nil, testToken)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("owner cancel = %d, want 202", resp.StatusCode)
	}
}

func TestKeyCanCancelItsOwnJob(t *testing.T) {
	f := newAuthFixture(t, &fakeClient{deployDelay: 2 * time.Second})
	token, _ := f.key(t, "ci", apikey.ScopeRead, apikey.ScopeDeploy, apikey.ScopeJobsCancel)
	resp := doRequest(t, f.ts, http.MethodPost, "/v1/devices/deck/deployments",
		map[string]any{"game_id": "game", "directory": "/tmp/build"}, token)
	accepted := decodeBody[struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}](t, resp)
	resp = doRequest(t, f.ts, http.MethodDelete, "/v1/jobs/"+accepted.Job.ID, nil, token)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel own job = %d, want 202", resp.StatusCode)
	}
	waitForEntry(t, f.log, "job.finished", "cancelled")
}

func TestAPIKeysDisabledWithoutStore(t *testing.T) {
	_, ts := newTestServer(&fakeClient{})
	defer ts.Close()
	resp := doRequest(t, ts, http.MethodGet, "/v1/health", nil, "ldk_000000000000_00")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("key with no store = %d, want 401", resp.StatusCode)
	}
}
