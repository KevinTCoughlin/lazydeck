package fixture

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kevintcoughlin/lazydeck/internal/client"
)

func TestDeployRespectsContextCancellation(t *testing.T) {
	c := &Client{DeployDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Deploy(ctx, "m", "l", "game", "/tmp/build", false) }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Deploy() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deploy did not return after context cancellation")
	}
}

func TestDeployReturnsConfiguredErrorAfterDelay(t *testing.T) {
	wantErr := &client.CLIError{Kind: "unreachable", Message: "boom"}
	c := &Client{DeployDelay: 10 * time.Millisecond, DeployErr: wantErr}

	start := time.Now()
	err := c.Deploy(context.Background(), "m", "l", "game", "/tmp/build", false)
	if time.Since(start) < 10*time.Millisecond {
		t.Fatal("Deploy returned before DeployDelay elapsed")
	}
	if err != wantErr {
		t.Fatalf("Deploy() error = %v, want %v", err, wantErr)
	}
}

func TestSyncLogsRespectsContextCancellation(t *testing.T) {
	c := &Client{SyncLogsDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.SyncLogs(ctx, "m", "l", "game", "/tmp/logs") }()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SyncLogs() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SyncLogs did not return after context cancellation")
	}
}

func TestStatusReturnsConfiguredResult(t *testing.T) {
	c := &Client{StatusResult: map[string]any{"state": "online"}}
	status, err := c.Status(context.Background(), "m", "l")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Raw["state"] != "online" {
		t.Fatalf("Status().Raw = %#v, want state=online", status.Raw)
	}
}

func TestStatusReturnsConfiguredError(t *testing.T) {
	wantErr := &client.CLIError{Kind: "auth-failed", Message: "nope"}
	c := &Client{StatusErr: wantErr}
	if _, err := c.Status(context.Background(), "m", "l"); err != wantErr {
		t.Fatalf("Status() error = %v, want %v", err, wantErr)
	}
}

func TestDiscoverReturnsConfiguredResult(t *testing.T) {
	want := []client.DiscoveredDevice{{Name: "deck", Address: "192.0.2.1", Port: 32000}}
	c := &Client{DiscoverResult: want}
	got, err := c.Discover(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "deck" {
		t.Fatalf("Discover() = %#v, want %#v", got, want)
	}
}

func TestNewFromEnvDefaultsToReadyStatus(t *testing.T) {
	c := NewFromEnv()
	if c.StatusResult["state"] != "ready" {
		t.Fatalf("default StatusResult = %#v, want state=ready", c.StatusResult)
	}
	if c.DeployErr != nil || c.SyncLogsErr != nil {
		t.Fatalf("defaults should not configure failures: deploy=%v logs=%v", c.DeployErr, c.SyncLogsErr)
	}
}

func TestNewFromEnvParsesDeployFailAndDelay(t *testing.T) {
	t.Setenv("LAZYDECK_FIXTURE_DEPLOY_FAIL", "true")
	t.Setenv("LAZYDECK_FIXTURE_DEPLOY_DELAY", "50ms")

	c := NewFromEnv()
	if c.DeployDelay != 50*time.Millisecond {
		t.Fatalf("DeployDelay = %v, want 50ms", c.DeployDelay)
	}
	if c.DeployErr == nil {
		t.Fatal("expected DeployErr to be set")
	}
}

func TestNewFromEnvParsesDiscoverJSON(t *testing.T) {
	t.Setenv("LAZYDECK_FIXTURE_DISCOVER", `[{"name":"deck","address":"192.0.2.1","port":32000}]`)

	c := NewFromEnv()
	if len(c.DiscoverResult) != 1 || c.DiscoverResult[0].Name != "deck" {
		t.Fatalf("DiscoverResult = %#v", c.DiscoverResult)
	}
}

func TestNewFromEnvPanicsOnInvalidJSON(t *testing.T) {
	t.Setenv("LAZYDECK_FIXTURE_STATUS", "not-json")

	defer func() {
		if recover() == nil {
			t.Fatal("expected NewFromEnv to panic on invalid JSON")
		}
	}()
	NewFromEnv()
}
