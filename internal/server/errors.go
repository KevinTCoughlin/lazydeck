package server

import (
	"encoding/json"
	"net/http"
)

// apiError is the JSON body of every non-2xx /v1/ response, giving engine
// plugins a stable machine-readable Kind instead of a message they'd have
// to pattern-match (mirrors client.CLIError's kinds, plus API-level kinds
// like "unauthorized" and "not-found").
type apiError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

// statusForKind maps a Kind to the HTTP status engine plugins should treat
// as authoritative; the JSON body's Kind is still the field to branch on
// since several kinds share a status.
func statusForKind(kind string) int {
	switch kind {
	case "unauthorized":
		return http.StatusUnauthorized
	case "invalid-input", "invalid-request":
		return http.StatusBadRequest
	case "not-found":
		return http.StatusNotFound
	case "device-busy":
		return http.StatusConflict
	case "unreachable", "auth-failed":
		return http.StatusBadGateway // failure talking to the devkit, not to this API
	case "script-error":
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, kind, message string) {
	writeErrorStatus(w, statusForKind(kind), kind, message)
}

func writeErrorStatus(w http.ResponseWriter, status int, kind, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: apiError{Kind: kind, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
