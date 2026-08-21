package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fixturesDir locates api/fixtures relative to this source file rather than
// the test binary's working directory, so `go test ./...` from any
// directory finds the same files `just check`/CI would.
func fixturesDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve this test file's path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "api", "fixtures")
}

// TestFixturesMatchResponseShapes decodes every committed fixture into the
// same Go type its handler produces, so a fixture that drifts from the
// actual response shape (a renamed field, a type change) fails CI instead
// of silently going stale relative to the OpenAPI contract it's meant to
// document.
func TestFixturesMatchResponseShapes(t *testing.T) {
	dir := fixturesDir(t)

	cases := []struct {
		file string
		into any
	}{
		{"health.json", &map[string]string{}},
		{"capabilities.json", &capabilitiesResponse{}},
		{"devices_list.json", &struct {
			Devices []deviceResponse `json:"devices"`
		}{}},
		{"deployment_accepted.json", &struct {
			Job jobFixture `json:"job"`
		}{}},
		{"job_succeeded.json", &struct {
			Job jobFixture `json:"job"`
		}{}},
		{"job_failed.json", &struct {
			Job jobFixture `json:"job"`
		}{}},
		{"job_event.json", &eventFixture{}},
		{"error_unauthorized.json", &errorEnvelope{}},
		{"error_not_found.json", &errorEnvelope{}},
		{"error_device_busy.json", &errorEnvelope{}},
		{"error_unsupported.json", &errorEnvelope{}},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatalf("reading fixture: %v", err)
			}
			dec := json.NewDecoder(bytes.NewReader(data))
			dec.DisallowUnknownFields()
			if err := dec.Decode(tc.into); err != nil {
				t.Fatalf("decoding %s into %T: %v", tc.file, tc.into, err)
			}
		})
	}
}

// jobFixture and eventFixture mirror jobs.Snapshot/jobs.Event's JSON shape
// without importing internal/jobs, since this test only needs to assert
// the fixture files decode strictly, not exercise job behavior.
type jobFixture struct {
	ID          string           `json:"id"`
	DeviceID    string           `json:"device_id"`
	Operation   string           `json:"operation"`
	Status      string           `json:"status"`
	CreatedAt   string           `json:"created_at"`
	StartedAt   *string          `json:"started_at,omitempty"`
	FinishedAt  *string          `json:"finished_at,omitempty"`
	LastMessage string           `json:"last_message,omitempty"`
	Error       *jobFixtureError `json:"error,omitempty"`
}

type jobFixtureError struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type eventFixture struct {
	Seq     int              `json:"seq"`
	Status  string           `json:"status"`
	Message string           `json:"message,omitempty"`
	Error   *jobFixtureError `json:"error,omitempty"`
	Time    string           `json:"time"`
}
