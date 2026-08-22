package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/jobs"
)

// Operation-specific job deadlines, matching the TUI's own constants
// (internal/tui/tui.go) rather than internal/client's 60s fallback timeout,
// which only exists for callers that don't set a deadline of their own.
const (
	deployTimeout   = 10 * time.Minute
	logsSyncTimeout = 2 * time.Minute
)

// maxDiscoverTimeoutSeconds bounds a caller-supplied discover timeout so a
// request can't ask this process to hang around browsing mDNS indefinitely
// (or hand a negative/absurdly large value down to time.Duration, which a
// bare float64->Duration conversion would accept without complaint).
const maxDiscoverTimeoutSeconds = 300

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
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > maxDiscoverTimeoutSeconds {
		writeError(w, "invalid-input", fmt.Sprintf("timeout_seconds must be between 0 and %d", maxDiscoverTimeoutSeconds))
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

// gameIDPattern matches the game_id values this API accepts: it must start
// with an alphanumeric character (never '-', which cli.py's argparse could
// mistake for a flag) and stay within a sane length, ruling out control
// characters, whitespace, path separators, and the pathological inputs a
// naive non-empty check would let through all the way to an accepted 202
// before the async job later reports invalid-input.
var gameIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validateGameID(id string) error {
	if !gameIDPattern.MatchString(id) {
		return errors.New("game_id must start with a letter or digit and contain only letters, digits, '.', '_', or '-' (max 128 characters)")
	}
	return nil
}

// validateDirectory requires an absolute path. internal/client's run()
// invokes cli.py with cmd.Dir set to the bundled Python runtime's directory
// (see internal/client/client.go), not this process's working directory or
// the caller's, so a relative path here would resolve somewhere the API
// caller never intended.
func validateDirectory(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("directory must be an absolute path")
	}
	return nil
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
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if err := validateGameID(req.GameID); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if err := validateDirectory(req.Directory); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}

	job, err := s.jobs.Submit(d.Name, "deploy", func(ctx context.Context, report func(string)) error {
		ctx, cancel := context.WithTimeout(ctx, deployTimeout)
		defer cancel()
		report("deploying to " + d.Name)
		return s.client.Deploy(ctx, d.Machine, d.Login, req.GameID, req.Directory, req.DeleteExtraneous)
	})
	if err != nil {
		writeSubmitError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job.Snapshot()})
}

// logsSyncRequest's GameID is accepted for forward compatibility but
// currently unused: the vendored devkit_client.sync_logs (see
// python/vendor/devkit_client/__init__.py) always pulls the complete
// ~/.local/share/Steam/logs and minidump directories regardless of any
// game name, so this operation cannot filter by title yet. It is therefore
// optional here rather than required, unlike deploy's game_id.
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
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}
	if req.GameID != "" {
		if err := validateGameID(req.GameID); err != nil {
			writeError(w, "invalid-input", err.Error())
			return
		}
	}
	if err := validateDirectory(req.Directory); err != nil {
		writeError(w, "invalid-input", err.Error())
		return
	}

	job, err := s.jobs.Submit(d.Name, "logs-sync", func(ctx context.Context, report func(string)) error {
		ctx, cancel := context.WithTimeout(ctx, logsSyncTimeout)
		defer cancel()
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

// maxRequestBodyBytes bounds every request body this API decodes. These
// requests are all small, fixed-shape JSON objects; there is no legitimate
// case for a multi-megabyte body, so this is generous headroom against an
// authenticated-but-misbehaving local client rather than a tight limit.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// decodeJSONBody decodes a single JSON value from the request body,
// tolerating an empty body as a zero-value v (several requests, like
// discover, have every field optional). It bounds the body size and
// rejects anything beyond that one JSON value — concatenated JSON or other
// trailing non-whitespace data — so a request can't be accepted on the
// strength of a valid-looking prefix while carrying unexamined extra data.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) error {
	if r.ContentLength == 0 {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decoding request body: %w", err)
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON value")
	}
	return nil
}

// writeClientError translates an internal/client error into the API's
// typed error envelope. A *client.CLIError's Message is already curated by
// cli.py for the "error" field of its JSON envelope, so it's safe to pass
// through; anything else (a failed `uv run` invocation, malformed cli.py
// output) can carry raw subprocess stdout/stderr — potentially SSH paths,
// hostnames, or other process output — so that case is logged locally and
// replaced with a stable, generic message instead of being echoed back.
func writeClientError(w http.ResponseWriter, err error) {
	var cliErr *client.CLIError
	if errors.As(err, &cliErr) {
		writeError(w, cliErr.Kind, cliErr.Message)
		return
	}
	log.Printf("devkit operation failed with an unclassified error: %v", err)
	writeError(w, "unknown", "the devkit operation failed unexpectedly; see the lazydeck serve process log for details")
}

func writeSubmitError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrDeviceBusy) {
		writeError(w, "device-busy", err.Error())
		return
	}
	writeError(w, "internal", err.Error())
}
