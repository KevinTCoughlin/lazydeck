// Package mcpapi is a thin Go HTTP client for lazydeck's local /v1 API
// (api/openapi.yaml), used by internal/mcp exactly the way the Godot and
// Unity integrations consume the same contract: as an external client
// speaking bearer-authenticated HTTP+SSE, not by importing internal/server
// or internal/client directly. Keeping this a wire-level client (rather
// than an in-process call into the server's handlers) means the MCP tool
// surface can never drift from what any other client of `lazydeck serve`
// sees, and it composes with the connection-file discovery/auto-start
// pattern already established for engine plugins.
package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// APIError is the decoded form of the API's error envelope
// ({"error": {"kind": ..., "message": ...}}), returned by every method
// below instead of a generic HTTP status so callers (and, in turn, MCP tool
// handlers) can surface the same machine-readable Kind the Godot/Unity
// clients already branch on.
type APIError struct {
	Status  int    `json:"-"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s (http %d)", e.Kind, e.Message, e.Status)
}

// Client is a minimal HTTP client for the /v1 API described by
// api/openapi.yaml. It holds no state beyond the base URL, bearer token,
// and an *http.Client, mirroring how little the Godot/Unity clients need.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New builds a Client from a connection file's BaseURL/Token, as read via
// internal/server.ConnectionInfo.
func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Device mirrors the openapi Device schema (a devices.toml entry).
type Device struct {
	ID      string `json:"id"`
	Machine string `json:"machine"`
	Login   string `json:"login,omitempty"`
}

// Job mirrors internal/jobs.Snapshot's JSON shape.
type Job struct {
	ID          string     `json:"id"`
	DeviceID    string     `json:"device_id"`
	Operation   string     `json:"operation"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	LastMessage string     `json:"last_message,omitempty"`
	Error       *JobError  `json:"error,omitempty"`
}

// JobError mirrors internal/jobs.Error's JSON shape.
type JobError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// terminal job statuses, matching internal/jobs.Status's terminal() set.
// Duplicated here (rather than importing internal/jobs) because mcpapi is
// a wire-level client: it only knows the strings the API actually sends.
func isTerminal(status string) bool {
	switch status {
	case "succeeded", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("calling lazydeck serve at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response from %s: %w", path, err)
	}

	if resp.StatusCode >= 300 {
		var envelope struct {
			Error APIError `json:"error"`
		}
		apiErr := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Kind != "" {
			apiErr.Kind = envelope.Error.Kind
			apiErr.Message = envelope.Error.Message
		} else {
			apiErr.Kind = "unknown"
			apiErr.Message = string(data)
		}
		return apiErr
	}

	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}
	return nil
}

// Health calls GET /v1/health.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/v1/health", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Capabilities calls GET /v1/capabilities.
func (c *Client) Capabilities(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListDevices calls GET /v1/devices.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// Discover calls POST /v1/devices/discover.
func (c *Client) Discover(ctx context.Context, timeoutSeconds float64) ([]map[string]any, error) {
	var out struct {
		Devices []map[string]any `json:"devices"`
	}
	body := map[string]any{"timeout_seconds": timeoutSeconds}
	if err := c.do(ctx, http.MethodPost, "/v1/devices/discover", body, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}

// Pair calls POST /v1/devices/{id}/pair.
func (c *Client) Pair(ctx context.Context, deviceID string) (map[string]any, error) {
	var out map[string]any
	path := fmt.Sprintf("/v1/devices/%s/pair", deviceID)
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Status calls GET /v1/devices/{id}/status.
func (c *Client) Status(ctx context.Context, deviceID string) (map[string]any, error) {
	var out map[string]any
	path := fmt.Sprintf("/v1/devices/%s/status", deviceID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListGames calls GET /v1/devices/{id}/games.
func (c *Client) ListGames(ctx context.Context, deviceID string) ([]any, error) {
	var out struct {
		Games []any `json:"games"`
	}
	path := fmt.Sprintf("/v1/devices/%s/games", deviceID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out.Games, nil
}

// Deploy calls POST /v1/devices/{id}/deployments, returning the accepted
// job snapshot (status "queued" or "running"). Use WaitForJob to block
// until it reaches a terminal state.
func (c *Client) Deploy(ctx context.Context, deviceID, gameID, directory string, deleteExtraneous bool) (Job, error) {
	var out struct {
		Job Job `json:"job"`
	}
	body := map[string]any{
		"game_id":           gameID,
		"directory":         directory,
		"delete_extraneous": deleteExtraneous,
	}
	path := fmt.Sprintf("/v1/devices/%s/deployments", deviceID)
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return Job{}, err
	}
	return out.Job, nil
}

// SyncLogs calls POST /v1/devices/{id}/logs/sync, returning the accepted
// job snapshot the same way Deploy does.
func (c *Client) SyncLogs(ctx context.Context, deviceID, gameID, directory string) (Job, error) {
	var out struct {
		Job Job `json:"job"`
	}
	body := map[string]any{"game_id": gameID, "directory": directory}
	path := fmt.Sprintf("/v1/devices/%s/logs/sync", deviceID)
	if err := c.do(ctx, http.MethodPost, path, body, &out); err != nil {
		return Job{}, err
	}
	return out.Job, nil
}

// Launch calls POST /v1/devices/{id}/games/{gameID}/launch. The API
// currently always returns 501 "unsupported" for this route (see
// internal/server/handlers.go); this method exists so the MCP tool surface
// has a stable place to wire it up once/if that changes, without another
// round of API-shape design.
func (c *Client) Launch(ctx context.Context, deviceID, gameID string) (map[string]any, error) {
	var out map[string]any
	path := fmt.Sprintf("/v1/devices/%s/games/%s/launch", deviceID, gameID)
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Stop calls POST /v1/devices/{id}/games/{gameID}/stop. See Launch's
// doc comment: currently always 501.
func (c *Client) Stop(ctx context.Context, deviceID, gameID string) (map[string]any, error) {
	var out map[string]any
	path := fmt.Sprintf("/v1/devices/%s/games/%s/stop", deviceID, gameID)
	if err := c.do(ctx, http.MethodPost, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetJob calls GET /v1/jobs/{id}.
func (c *Client) GetJob(ctx context.Context, jobID string) (Job, error) {
	var out struct {
		Job Job `json:"job"`
	}
	path := fmt.Sprintf("/v1/jobs/%s", jobID)
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Job{}, err
	}
	return out.Job, nil
}

// CancelJob calls DELETE /v1/jobs/{id}.
func (c *Client) CancelJob(ctx context.Context, jobID string) (Job, error) {
	var out struct {
		Job Job `json:"job"`
	}
	path := fmt.Sprintf("/v1/jobs/%s", jobID)
	if err := c.do(ctx, http.MethodDelete, path, nil, &out); err != nil {
		return Job{}, err
	}
	return out.Job, nil
}

// WaitForJob polls GET /v1/jobs/{id} every pollInterval until the job
// reaches a terminal status or ctx is done, whichever comes first. MCP
// tool calls are request/response, not SSE-streamed like the Godot/Unity
// integrations' job progress UI, so a deploy/sync_logs tool call blocks
// here rather than returning a bare job id the caller has no synchronous
// way to await. Callers should derive ctx from a bounded timeout (see
// internal/mcp/tools.go's deployToolTimeout/syncLogsToolTimeout) so a job
// that runs long still returns control to the MCP client with the job id
// intact, inspectable later via get_job.
func (c *Client) WaitForJob(ctx context.Context, jobID string, pollInterval time.Duration) (Job, error) {
	for {
		job, err := c.GetJob(ctx, jobID)
		if err != nil {
			return Job{}, err
		}
		if isTerminal(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
