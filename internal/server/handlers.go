package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/jobs"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "api_version": APIVersion})
}

// capabilities tells a client what this server can do without it having to
// probe individual endpoints and infer support from a 404/501. launch and
// stop are hardcoded false: python/cli.py has no launch/stop command for
// the server to call yet (see package doc comment), so advertising them as
// unsupported here is honest rather than routing to code that would always
// fail.
type capabilitiesResponse struct {
	APIVersion string          `json:"api_version"`
	Operations map[string]bool `json:"operations"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, capabilitiesResponse{
		APIVersion: APIVersion,
		Operations: map[string]bool{
			"discover":  true,
			"pair":      true,
			"status":    true,
			"games":     true,
			"deploy":    true,
			"logs_sync": true,
			"launch":    false,
			"stop":      false,
		},
	})
}

type deviceResponse struct {
	ID      string `json:"id"`
	Machine string `json:"machine"`
	Login   string `json:"login,omitempty"`
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices := make([]deviceResponse, 0, len(s.cfg.Devices))
	for _, d := range s.cfg.Devices {
		devices = append(devices, deviceResponse{ID: d.Name, Machine: d.Machine, Login: d.Login})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

type discoverRequest struct {
	TimeoutSeconds float64 `json:"timeout_seconds"`
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	var req discoverRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	timeout := 5 * time.Second
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds * float64(time.Second))
	}

	found, err := s.client.Discover(r.Context(), timeout)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": found})
}

func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceOr404(w, r)
	if !ok {
		return
	}
	if err := s.client.Register(r.Context(), d.Machine); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": d.Name, "paired": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceOr404(w, r)
	if !ok {
		return
	}
	status, err := s.client.Status(r.Context(), d.Machine, d.Login)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": d.Name, "status": status.Raw})
}

func (s *Server) handleGames(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceOr404(w, r)
	if !ok {
		return
	}
	games, err := s.client.ListGames(r.Context(), d.Machine, d.Login)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": games})
}

type deployRequest struct {
	GameID           string `json:"game_id"`
	Directory        string `json:"directory"`
	DeleteExtraneous bool   `json:"delete_extraneous"`
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceOr404(w, r)
	if !ok {
		return
	}
	var req deployRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if req.GameID == "" || req.Directory == "" {
		writeError(w, "invalid-input", "game_id and directory are required")
		return
	}

	job, err := s.jobs.Submit(d.Name, "deploy", func(ctx context.Context, report func(string)) error {
		report("deploying to " + d.Name)
		return s.client.Deploy(ctx, d.Machine, d.Login, req.GameID, req.Directory, req.DeleteExtraneous)
	})
	if err != nil {
		writeSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job.Snapshot()})
}

type logsSyncRequest struct {
	GameID    string `json:"game_id"`
	Directory string `json:"directory"`
}

func (s *Server) handleLogsSync(w http.ResponseWriter, r *http.Request) {
	d, ok := s.deviceOr404(w, r)
	if !ok {
		return
	}
	var req logsSyncRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if req.GameID == "" || req.Directory == "" {
		writeError(w, "invalid-input", "game_id and directory are required")
		return
	}

	job, err := s.jobs.Submit(d.Name, "logs-sync", func(ctx context.Context, report func(string)) error {
		report("syncing logs from " + d.Name)
		return s.client.SyncLogs(ctx, d.Machine, d.Login, req.GameID, req.Directory)
	})
	if err != nil {
		writeSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job.Snapshot()})
}

// handleLaunch and handleStop are capability-gated stubs: /v1/capabilities
// reports launch/stop as unsupported (see handleCapabilities), and these
// return the same "unsupported" error rather than a generic 404, so a
// client that skipped the capability check still gets an unambiguous,
// typed reason. They exist as routes now so the URL shape is stable once a
// real backend lands, instead of #14 or a Unity client guessing it later.
func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	writeErrorStatus(w, http.StatusNotImplemented, "unsupported", "launch is not yet implemented by the LazyDeck backend; check /v1/capabilities")
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	writeErrorStatus(w, http.StatusNotImplemented, "unsupported", "stop is not yet implemented by the LazyDeck backend; check /v1/capabilities")
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, "not-found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job.Snapshot()})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Cancel(r.PathValue("id"))
	if err != nil {
		writeError(w, "not-found", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job.Snapshot()})
}

// handleJobEvents streams a job's event history and subsequent updates as
// Server-Sent Events, chosen over WebSocket because job progress is
// strictly server->client (see package doc); this needs nothing beyond
// net/http on the server and a plain EventSource/HTTPClient stream reader
// on the Godot/Unity side.
func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) {
	job, err := s.jobs.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, "not-found", err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, "internal", "streaming is not supported by this response writer")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	after := 0
	for {
		events, wait := job.EventsSince(after)
		for _, e := range events {
			if err := writeSSEEvent(w, e); err != nil {
				return
			}
			after = e.Seq
		}
		flusher.Flush()

		if wait == nil {
			return // job is terminal and every event has been sent
		}
		select {
		case <-wait:
		case <-r.Context().Done():
			return
		}
	}
}

func writeSSEEvent(w http.ResponseWriter, e jobs.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: job\ndata: %s\n\n", data)
	return err
}

// decodeJSONBody decodes a JSON request body, tolerating an empty body as
// a zero-value v (several requests, like discover, have every field
// optional).
func decodeJSONBody(r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decoding request body: %w", err)
	}
	return nil
}

// writeClientError translates an internal/client error (typically a
// *client.CLIError with a Kind, or a plain error from a failed uv/ssh
// invocation) into the API's typed error envelope.
func writeClientError(w http.ResponseWriter, err error) {
	var cliErr *client.CLIError
	if errors.As(err, &cliErr) {
		writeError(w, cliErr.Kind, cliErr.Message)
		return
	}
	writeError(w, "unknown", err.Error())
}

func writeSubmitError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrDeviceBusy) {
		writeError(w, "device-busy", err.Error())
		return
	}
	writeError(w, "internal", err.Error())
}
