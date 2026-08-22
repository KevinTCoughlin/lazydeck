package mcpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

const testToken = "test-token"

// newTestServer returns an httptest.Server that mimics the subset of the
// /v1 wire contract (api/openapi.yaml) exercised by these tests, and a
// Client already pointed at it. It is a hand-rolled fake rather than a
// reuse of internal/server (whose fakeClient/newTestServer helpers are
// unexported test-only code in a different package) since mcpapi is
// deliberately a wire-level client: these tests only need to control what
// bytes come back over HTTP, not internal/server's Go-level dependencies.
func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"kind": "unauthorized", "message": "missing or invalid bearer token"},
			})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(ts.Close)
	return New(ts.URL, testToken), ts
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("encoding fake response: %v", err)
	}
}

func TestHealth(t *testing.T) {
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/health" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"status": "ok", "api_version": "v1"})
	})

	out, err := cli.Health(t.Context())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if out["status"] != "ok" {
		t.Errorf("status = %v, want ok", out["status"])
	}
}

// TestPathEscaping locks in that device/game/job identifiers containing
// characters like spaces and slashes (e.g. a devices.toml entry named
// "Steam Deck (Galileo)") are escaped into the request path rather than
// splitting it into extra segments or producing an invalid request.
func TestPathEscaping(t *testing.T) {
	const rawDeviceID = "Steam Deck / Galileo"
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/devices/" + url.PathEscape(rawDeviceID) + "/status"
		if r.URL.EscapedPath() != wantPath {
			t.Fatalf("EscapedPath = %q, want %q", r.URL.EscapedPath(), wantPath)
		}
		if r.URL.Path != "/v1/devices/"+rawDeviceID+"/status" {
			t.Fatalf("decoded Path = %q, want the raw device id round-tripped back out", r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"status": map[string]any{"state": "ready"}})
	})

	if _, err := cli.Status(t.Context(), rawDeviceID); err != nil {
		t.Fatalf("Status: %v", err)
	}
}

func TestListDevices(t *testing.T) {
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/devices" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"devices": []Device{{ID: "deck-1", Machine: "10.0.0.5", Login: "deck"}},
		})
	})

	devices, err := cli.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != "deck-1" {
		t.Errorf("devices = %+v, want one device with id deck-1", devices)
	}
}

func TestUnauthorized(t *testing.T) {
	cli := New("http://unused", "wrong-token")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"kind": "unauthorized", "message": "missing or invalid bearer token"},
		})
	}))
	t.Cleanup(ts.Close)
	cli.BaseURL = ts.URL

	_, err := cli.Health(t.Context())
	if err == nil {
		t.Fatal("expected an error for a bad token, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err = %T(%v), want *APIError", err, err)
	}
	if apiErr.Kind != "unauthorized" || apiErr.Status != http.StatusUnauthorized {
		t.Errorf("apiErr = %+v, want kind=unauthorized status=401", apiErr)
	}
}

func TestDeployAndGetJob(t *testing.T) {
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/devices/deck-1/deployments":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["game_id"] != "mygame" {
				t.Errorf("game_id = %v, want mygame", body["game_id"])
			}
			writeJSON(t, w, http.StatusAccepted, map[string]any{
				"job": Job{ID: "job-1", DeviceID: "deck-1", Operation: "deploy", Status: "queued"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/jobs/job-1":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"job": Job{ID: "job-1", DeviceID: "deck-1", Operation: "deploy", Status: "succeeded"},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	job, err := cli.Deploy(t.Context(), "deck-1", "mygame", "/abs/path", false)
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if job.ID != "job-1" || job.Status != "queued" {
		t.Fatalf("job = %+v, want id=job-1 status=queued", job)
	}

	final, err := cli.GetJob(t.Context(), job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if final.Status != "succeeded" {
		t.Errorf("final.Status = %q, want succeeded", final.Status)
	}
}

func TestWaitForJobTerminal(t *testing.T) {
	calls := 0
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		status := "running"
		if calls >= 3 {
			status = "succeeded"
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"job": Job{ID: "job-1", Status: status},
		})
	})

	job, err := cli.WaitForJob(t.Context(), "job-1", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForJob: %v", err)
	}
	if job.Status != "succeeded" {
		t.Errorf("job.Status = %q, want succeeded", job.Status)
	}
	if calls < 3 {
		t.Errorf("calls = %d, want at least 3 polls before terminal", calls)
	}
}

func TestWaitForJobTimeout(t *testing.T) {
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"job": Job{ID: "job-1", Status: "running"}})
	})

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()

	job, err := cli.WaitForJob(ctx, "job-1", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if job.Status != "running" {
		t.Errorf("job.Status = %q, want running (last known snapshot)", job.Status)
	}
}
