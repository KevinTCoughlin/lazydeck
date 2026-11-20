package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type recordedRequest struct {
	method      string
	path        string // escaped, as sent on the wire
	contentType string
	body        map[string]any
}

// recordingServer answers every request with reply and records what the
// client sent, so each method's wire shape can be asserted from the test
// goroutine rather than inside the handler.
func recordingServer(t *testing.T, status int, reply string) (*Client, *recordedRequest) {
	t.Helper()
	rec := &recordedRequest{}
	cli, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.method = r.Method
		rec.path = r.URL.EscapedPath()
		rec.contentType = r.Header.Get("Content-Type")
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			_ = json.Unmarshal(data, &rec.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	})
	return cli, rec
}

func TestEveryMethodSpeaksTheWireContract(t *testing.T) {
	ctx := context.Background()
	jobReply := `{"job":{"id":"j1","device_id":"deck 1","operation":"deploy","status":"queued"}}`
	cases := []struct {
		name     string
		reply    string
		call     func(*Client) (any, error)
		method   string
		path     string
		wantBody map[string]any
		check    func(t *testing.T, got any)
	}{
		{
			name:   "capabilities",
			reply:  `{"operations":{"deploy":true}}`,
			call:   func(c *Client) (any, error) { return c.Capabilities(ctx) },
			method: "GET", path: "/v1/capabilities",
			check: func(t *testing.T, got any) {
				if ops, _ := got.(map[string]any)["operations"].(map[string]any); ops["deploy"] != true {
					t.Errorf("capabilities = %v", got)
				}
			},
		},
		{
			name:   "discover",
			reply:  `{"devices":[{"name":"deck"}]}`,
			call:   func(c *Client) (any, error) { return c.Discover(ctx, 2.5) },
			method: "POST", path: "/v1/devices/discover",
			wantBody: map[string]any{"timeout_seconds": 2.5},
			check: func(t *testing.T, got any) {
				if d := got.([]map[string]any); len(d) != 1 || d[0]["name"] != "deck" {
					t.Errorf("devices = %v", got)
				}
			},
		},
		{
			name:   "pair",
			reply:  `{"id":"deck 1","paired":true}`,
			call:   func(c *Client) (any, error) { return c.Pair(ctx, "deck 1") },
			method: "POST", path: "/v1/devices/deck%201/pair",
		},
		{
			name:   "status",
			reply:  `{"id":"a/b","status":{}}`,
			call:   func(c *Client) (any, error) { return c.Status(ctx, "a/b") },
			method: "GET", path: "/v1/devices/a%2Fb/status",
		},
		{
			name:   "games",
			reply:  `{"games":[{"gameid":"g"}]}`,
			call:   func(c *Client) (any, error) { return c.ListGames(ctx, "deck") },
			method: "GET", path: "/v1/devices/deck/games",
			check: func(t *testing.T, got any) {
				if g := got.([]any); len(g) != 1 {
					t.Errorf("games = %v", got)
				}
			},
		},
		{
			name:   "deploy without argv omits it",
			reply:  jobReply,
			call:   func(c *Client) (any, error) { return c.Deploy(ctx, "deck", "game", "/build", true, nil) },
			method: "POST", path: "/v1/devices/deck/deployments",
			wantBody: map[string]any{"game_id": "game", "directory": "/build", "delete_extraneous": true},
		},
		{
			name:  "sync logs",
			reply: jobReply,
			call:  func(c *Client) (any, error) { return c.SyncLogs(ctx, "deck", "game", "/logs") },
			check: func(t *testing.T, got any) {
				if j := got.(Job); j.ID != "j1" || j.Status != "queued" {
					t.Errorf("job = %+v", j)
				}
			},
			method: "POST", path: "/v1/devices/deck/logs/sync",
			wantBody: map[string]any{"game_id": "game", "directory": "/logs"},
		},
		{
			name:   "launch escapes both ids",
			reply:  `{}`,
			call:   func(c *Client) (any, error) { return c.Launch(ctx, "deck", "my game") },
			method: "POST", path: "/v1/devices/deck/games/my%20game/launch",
		},
		{
			name:   "stop",
			reply:  `{}`,
			call:   func(c *Client) (any, error) { return c.Stop(ctx, "deck", "g") },
			method: "POST", path: "/v1/devices/deck/games/g/stop",
		},
		{
			name:   "cancel job",
			reply:  `{"job":{"id":"j/1","status":"cancelled"}}`,
			call:   func(c *Client) (any, error) { return c.CancelJob(ctx, "j/1") },
			method: "DELETE", path: "/v1/jobs/j%2F1",
			check: func(t *testing.T, got any) {
				if j := got.(Job); j.Status != "cancelled" {
					t.Errorf("job = %+v", j)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cli, rec := recordingServer(t, http.StatusOK, c.reply)
			got, err := c.call(cli)
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if rec.method != c.method || rec.path != c.path {
				t.Errorf("sent %s %s, want %s %s", rec.method, rec.path, c.method, c.path)
			}
			if c.wantBody != nil {
				if rec.contentType != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", rec.contentType)
				}
				for k, v := range c.wantBody {
					if rec.body[k] != v {
						t.Errorf("body[%q] = %v, want %v (body %v)", k, rec.body[k], v, rec.body)
					}
				}
				if len(rec.body) != len(c.wantBody) {
					t.Errorf("body = %v, want exactly %v", rec.body, c.wantBody)
				}
			}
			if c.check != nil {
				c.check(t, got)
			}
		})
	}
}

func TestDeploySendsArgv(t *testing.T) {
	cli, rec := recordingServer(t, http.StatusAccepted, `{"job":{"id":"j1","status":"queued"}}`)
	if _, err := cli.Deploy(context.Background(), "deck", "game", "/build", false, []string{"./game", "--fullscreen"}); err != nil {
		t.Fatal(err)
	}
	argv, _ := rec.body["argv"].([]any)
	if len(argv) != 2 || argv[0] != "./game" || argv[1] != "--fullscreen" {
		t.Fatalf("argv = %v", rec.body["argv"])
	}
}

func TestNonEnvelopeErrorKeepsStatusAndBody(t *testing.T) {
	cli, _ := recordingServer(t, http.StatusBadGateway, "upstream proxy error")
	_, err := cli.Capabilities(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadGateway || apiErr.Kind != "unknown" || apiErr.Message != "upstream proxy error" {
		t.Fatalf("APIError = %+v", apiErr)
	}
	if got := apiErr.Error(); got != "unknown: upstream proxy error (http 502)" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestEnvelopeErrorIsDecoded(t *testing.T) {
	cli, _ := recordingServer(t, http.StatusConflict, `{"error":{"kind":"device-busy","message":"a job is running"}}`)
	_, err := cli.Deploy(context.Background(), "deck", "g", "/b", false, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != "device-busy" || apiErr.Status != http.StatusConflict {
		t.Fatalf("err = %#v", err)
	}
}

func TestMalformedSuccessBodyIsAnError(t *testing.T) {
	cli, _ := recordingServer(t, http.StatusOK, `{"devices":`)
	if _, err := cli.ListDevices(context.Background()); err == nil || !strings.Contains(err.Error(), "decoding response") {
		t.Fatalf("err = %v, want a decode error", err)
	}
}

func TestEmptySuccessBodyIsNotAnError(t *testing.T) {
	cli, _ := recordingServer(t, http.StatusNoContent, "")
	got, err := cli.Pair(context.Background(), "deck")
	if err != nil || got != nil {
		t.Fatalf("Pair = %v, %v; want nil, nil", got, err)
	}
}

func TestUnreachableServerNamesTheBaseURL(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	base := ts.URL
	ts.Close() // nothing listens here any more
	_, err := New(base, testToken).Capabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), base) {
		t.Fatalf("err = %v, want it to name %s", err, base)
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("transport failure must not look like an API error: %+v", apiErr)
	}
}

func TestBadBaseURLFailsBeforeSending(t *testing.T) {
	_, err := New("http://bad host", testToken).Capabilities(context.Background())
	if err == nil || !strings.Contains(err.Error(), "building request") {
		t.Fatalf("err = %v, want a request-building error", err)
	}
}
