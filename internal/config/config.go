// Package config loads the list of devkit targets (Steam Machines, Steam
// Decks, etc.) that lazydeck manages, from a TOML file the user maintains.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Device is one paired (or pairable) devkit target.
type Device struct {
	Name    string `toml:"name"`    // display name, e.g. "steam-machine-livingroom"
	Machine string `toml:"machine"` // hostname / IP / mDNS service name passed to the client
	Login   string `toml:"login"`   // optional remote username override
}

type Config struct {
	Devices []Device `toml:"device"`

	// RefreshIntervalSeconds, when > 0, enables periodic background
	// refresh of every device's status on that interval. 0 (the
	// default) disables auto-refresh; status is only refreshed on
	// startup and on the manual 's' keybinding.
	RefreshIntervalSeconds int `toml:"refresh_interval_seconds"`
}

// DefaultPath returns ~/.config/lazydeck/devices.toml, creating the parent
// directory if needed.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "lazydeck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "devices.toml"), nil
}

// Load reads the config at path, creating a starter file with inline
// documentation if it does not exist yet.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeStarter(path); err != nil {
			return nil, err
		}
	}

	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes cfg to path as TOML, overwriting any existing contents. It is
// used by the TUI's device-discovery wizard to persist newly added devices
// without requiring the user to hand-edit devices.toml.
func Save(path string, cfg *Config) error {
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// AddDevice appends d to cfg.Devices and persists the result to path. It
// returns an error if a device with the same Name already exists.
func AddDevice(path string, cfg *Config, d Device) error {
	for _, existing := range cfg.Devices {
		if existing.Name == d.Name {
			return fmt.Errorf("device %q already exists", d.Name)
		}
	}
	cfg.Devices = append(cfg.Devices, d)
	return Save(path, cfg)
}

func writeStarter(path string) error {
	const starter = `# lazydeck device list.
# One [[device]] block per Steam Machine / Steam Deck devkit you deploy to.
# "machine" can be a hostname, IP address, or the mDNS service name shown
# by the official SteamOS Devkit Client during pairing.

# Uncomment to auto-refresh every device's status on an interval (seconds).
# Off by default; status still refreshes on startup and on the 's' key.
# refresh_interval_seconds = 30

# [[device]]
# name = "steam-machine"
# machine = "192.168.1.50"
# login = "deck"

# [[device]]
# name = "steam-deck"
# machine = "steamdeck.local"
`
	return os.WriteFile(path, []byte(starter), 0o644)
}
