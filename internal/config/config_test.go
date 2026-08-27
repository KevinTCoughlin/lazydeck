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

func TestAddDevicePreservesWebhooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.toml")
	cfg := &Config{Webhooks: []string{"https://hooks.slack.com/services/T/B/X"}}
	if err := AddDevice(path, cfg, Device{Name: "a", Machine: "1.2.3.4"}); err != nil {
		t.Fatalf("AddDevice: %v", err)
	}
	if len(cfg.Webhooks) != 1 {
		t.Fatalf("in-memory webhooks dropped: %#v", cfg.Webhooks)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Webhooks) != 1 || reloaded.Webhooks[0] != "https://hooks.slack.com/services/T/B/X" {
		t.Fatalf("expected webhooks preserved on disk, got %#v", reloaded.Webhooks)
	}
}

// TestDefaultPathHonorsXDGConfigHome guards the documented ~/.config
// location: with XDG_CONFIG_HOME set, DefaultPath must use it rather than
// whatever OS-native directory os.UserConfigDir() would otherwise resolve
// to (e.g. "~/Library/Application Support" on macOS).
func TestDefaultPathHonorsXDGConfigHome(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	// migrateLegacyPath calls os.UserConfigDir(), which reads $HOME
	// directly; isolate it to an empty temp dir so this test can never
	// read (and delete) the real user's actual legacy config file.
	t.Setenv("HOME", t.TempDir())

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(xdg, "lazydeck", "devices.toml")
	if path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}
}

// TestDefaultPathFallsBackToDotConfig guards the documented ~/.config
// location on platforms/environments with no XDG_CONFIG_HOME set.
func TestDefaultPathFallsBackToDotConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join(home, ".config", "lazydeck", "devices.toml")
	if path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}
}

// TestDefaultPathMigratesLegacyOSNativeConfig guards against silently
// orphaning a real, populated devices.toml that a prior lazydeck version
// wrote to the OS-native directory os.UserConfigDir() resolves to (e.g.
// "~/Library/Application Support/lazydeck" on macOS), which never matched
// the ~/.config path this project has always documented. The legacy file's
// content must be moved to the documented path, and the legacy file
// removed, rather than shadowed by a fresh blank starter.
func TestDefaultPathMigratesLegacyOSNativeConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	legacyDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}
	legacyDir = filepath.Join(legacyDir, "lazydeck")
	newDir := filepath.Join(home, ".config", "lazydeck")
	if legacyDir == newDir {
		t.Skip("this OS's UserConfigDir already matches the documented ~/.config path; nothing to migrate")
	}

	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "devices.toml")
	const content = `[[device]]
name = "steam-deck"
machine = "192.168.1.137"
`
	if err := os.WriteFile(legacyPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join(newDir, "devices.toml"); path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading migrated file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("migrated content = %q, want %q", got, content)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy file removed after migration, stat err = %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load migrated path: %v", err)
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].Machine != "192.168.1.137" {
		t.Fatalf("unexpected devices after migration: %+v", cfg.Devices)
	}
}

// TestDefaultPathDoesNotOverwriteExistingDocumentedConfig guards against
// migration clobbering a file the user already has at the documented
// ~/.config path (e.g. from following the README before this fix).
func TestDefaultPathDoesNotOverwriteExistingDocumentedConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	newDir := filepath.Join(home, ".config", "lazydeck")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(newDir, "devices.toml")
	const existing = `[[device]]
name = "already-here"
machine = "10.0.0.1"
`
	if err := os.WriteFile(newPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	legacyDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}
	legacyDir = filepath.Join(legacyDir, "lazydeck")
	if legacyDir != newDir {
		if err := os.MkdirAll(legacyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacyDir, "devices.toml"), []byte("[[device]]\nname = \"legacy\"\nmachine = \"legacy.local\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("expected existing documented config left untouched, got %q", got)
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
