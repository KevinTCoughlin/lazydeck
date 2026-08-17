// Package config loads the list of devkit targets (Steam Machines, Steam
// Decks, etc.) that devkit-tui manages, from a TOML file the user maintains.
package config

import (
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
}

// DefaultPath returns ~/.config/devkit-tui/devices.toml, creating the parent
// directory if needed.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "devkit-tui")
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

func writeStarter(path string) error {
	const starter = `# devkit-tui device list.
# One [[device]] block per Steam Machine / Steam Deck devkit you deploy to.
# "machine" can be a hostname, IP address, or the mDNS service name shown
# by the official SteamOS Devkit Client during pairing.

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
