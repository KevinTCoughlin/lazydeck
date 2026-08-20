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

func TestLoadUserConfigCreatesStarterAndParses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")

	cfg, err := LoadUserConfig(path)
	if err != nil {
		t.Fatalf("LoadUserConfig (starter): %v", err)
	}
	if len(cfg.CustomCommands) != 0 {
		t.Fatalf("expected empty starter user config, got %d custom commands", len(cfg.CustomCommands))
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected starter file to be written: %v", err)
	}

	content := `
customCommands:
  - key: "p"
    name: "ping device"
    command: "ping -c 3 {{.Machine}}"
  - key: "u"
    name: "uptime"
    command: "ssh {{.Login}}@{{.Machine}} uptime"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err = LoadUserConfig(path)
	if err != nil {
		t.Fatalf("LoadUserConfig (populated): %v", err)
	}
	if len(cfg.CustomCommands) != 2 {
		t.Fatalf("expected 2 custom commands, got %d", len(cfg.CustomCommands))
	}
	if cfg.CustomCommands[0].Key != "p" || cfg.CustomCommands[0].Command != "ping -c 3 {{.Machine}}" {
		t.Errorf("unexpected first custom command: %+v", cfg.CustomCommands[0])
	}
	if cfg.CustomCommands[1].Name != "uptime" {
		t.Errorf("unexpected second custom command: %+v", cfg.CustomCommands[1])
	}
}

func TestCustomCommandExpand(t *testing.T) {
	c := CustomCommand{Key: "p", Name: "ping", Command: "ping -c 3 {{.Machine}} # {{.Name}} {{.Login}}"}
	d := Device{Name: "steam-deck", Machine: "steamdeck.local", Login: "deck"}

	got, err := c.Expand(d)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	want := "ping -c 3 ${2} # ${1} ${3}"
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}
}

func TestCustomCommandExpandUsesPositionalParameters(t *testing.T) {
	c := CustomCommand{Key: "p", Name: "ping", Command: "echo {{.Name}}"}
	d := Device{Name: "evil'; rm -rf /; echo 'pwned"}

	got, err := c.Expand(d)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	want := `echo ${1}`
	if got != want {
		t.Errorf("Expand() = %q, want %q", got, want)
	}

}

func TestLoadUserConfigMissingUnwritableStarterIsOptional(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.yml")
	cfg, err := LoadUserConfig(path)
	if err != nil {
		t.Fatalf("optional missing config should not fail startup: %v", err)
	}
	if len(cfg.CustomCommands) != 0 {
		t.Fatalf("expected empty optional config, got %+v", cfg)
	}
}

func TestCustomCommandExpandInvalidTemplate(t *testing.T) {
	c := CustomCommand{Key: "p", Name: "bad", Command: "echo {{.Nope"}
	if _, err := c.Expand(Device{}); err == nil {
		t.Fatal("expected error for invalid template, got nil")
	}
}

// TestSaveIsAtomicNoTempLeftovers guards finding #6: a successful Save must
// leave only the target file behind, never a stray temp file.
func TestSaveIsAtomicNoTempLeftovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")
	cfg := &Config{Devices: []Device{{Name: "a", Machine: "1.2.3.4"}}}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "devices.toml" {
			t.Fatalf("unexpected leftover file after atomic save: %q", e.Name())
		}
	}
}

// TestAddDeviceDoesNotMutateOnSaveFailure guards finding #6: the in-memory
// config must only change after the write succeeds, so a failed persist never
// leaves cfg holding a device that isn't on disk.
func TestAddDeviceDoesNotMutateOnSaveFailure(t *testing.T) {
	dir := t.TempDir()
	// A regular file where a directory is expected makes MkdirAll/CreateTemp
	// fail deterministically, standing in for any write failure.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "devices.toml")

	cfg := &Config{Devices: []Device{{Name: "existing", Machine: "1.2.3.4"}}}
	err := AddDevice(path, cfg, Device{Name: "new", Machine: "5.6.7.8"})
	if err == nil {
		t.Fatal("expected AddDevice to fail when the config dir can't be created")
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].Name != "existing" {
		t.Fatalf("in-memory config was mutated despite save failure: %+v", cfg.Devices)
	}
}

// TestAddDevicePreservesRefreshInterval verifies staging a copy for the atomic
// write doesn't drop non-device config fields.
func TestAddDevicePreservesRefreshInterval(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")
	cfg := &Config{RefreshIntervalSeconds: 45}
	if err := AddDevice(path, cfg, Device{Name: "a", Machine: "1.2.3.4"}); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if cfg.RefreshIntervalSeconds != 45 {
		t.Fatalf("in-memory refresh interval changed: %d", cfg.RefreshIntervalSeconds)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.RefreshIntervalSeconds != 45 {
		t.Fatalf("expected refresh interval preserved on disk, got %d", reloaded.RefreshIntervalSeconds)
	}
	if len(reloaded.Devices) != 1 {
		t.Fatalf("expected 1 device persisted, got %d", len(reloaded.Devices))
	}
}

// TestSaveOverwriteReplacesContent verifies the rename-based save fully
// replaces prior contents (no partial/append leftovers).
func TestSaveOverwriteReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")
	if err := Save(path, &Config{Devices: []Device{
		{Name: "one", Machine: "1.1.1.1"},
		{Name: "two", Machine: "2.2.2.2"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, &Config{Devices: []Device{{Name: "only", Machine: "3.3.3.3"}}}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Devices) != 1 || reloaded.Devices[0].Name != "only" {
		t.Fatalf("expected content fully replaced, got %+v", reloaded.Devices)
	}
}
