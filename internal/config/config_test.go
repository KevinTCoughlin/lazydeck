package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesStarterAndParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load (starter): %v", err)
	}
	if len(cfg.Devices) != 0 {
		t.Fatalf("expected empty starter config, got %d devices", len(cfg.Devices))
	}

	content := `
[[device]]
name = "steam-machine"
machine = "192.168.1.50"
login = "deck"

[[device]]
name = "steam-deck"
machine = "steamdeck.local"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err = Load(path)
	if err != nil {
		t.Fatalf("Load (populated): %v", err)
	}
	if len(cfg.Devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(cfg.Devices))
	}
	if cfg.Devices[0].Name != "steam-machine" || cfg.Devices[0].Login != "deck" {
		t.Errorf("unexpected first device: %+v", cfg.Devices[0])
	}
	if cfg.Devices[1].Machine != "steamdeck.local" {
		t.Errorf("unexpected second device: %+v", cfg.Devices[1])
	}
}

func TestRefreshIntervalSecondsParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")

	content := `
refresh_interval_seconds = 30

[[device]]
name = "steam-deck"
machine = "steamdeck.local"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RefreshIntervalSeconds != 30 {
		t.Fatalf("expected RefreshIntervalSeconds=30, got %d", cfg.RefreshIntervalSeconds)
	}
}

func TestRefreshIntervalSecondsDefaultsToOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load (starter): %v", err)
	}
	if cfg.RefreshIntervalSeconds != 0 {
		t.Fatalf("expected auto-refresh off by default, got %d", cfg.RefreshIntervalSeconds)
	}
}

func TestSaveAndAddDeviceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")

	cfg := &Config{}
	if err := AddDevice(path, cfg, Device{Name: "deck-1", Machine: "deck1.local"}); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if err := AddDevice(path, cfg, Device{Name: "deck-2", Machine: "192.168.1.60", Login: "deck"}); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(reloaded.Devices) != 2 {
		t.Fatalf("expected 2 devices after round-trip, got %d", len(reloaded.Devices))
	}
	if reloaded.Devices[0].Name != "deck-1" || reloaded.Devices[0].Machine != "deck1.local" {
		t.Errorf("unexpected first device: %+v", reloaded.Devices[0])
	}
	if reloaded.Devices[1].Login != "deck" {
		t.Errorf("unexpected second device: %+v", reloaded.Devices[1])
	}

	// Duplicate name should be rejected.
	if err := AddDevice(path, cfg, Device{Name: "deck-1", Machine: "duplicate"}); err == nil {
		t.Error("expected error adding duplicate device name, got nil")
	}
}
