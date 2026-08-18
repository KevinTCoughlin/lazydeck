// Package config loads the list of devkit targets (Steam Machines, Steam
// Decks, etc.) that lazydeck manages, from a TOML file the user maintains.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
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

// CustomCommand binds a key to an arbitrary shell command run against the
// selected device(s), lazygit-style, without requiring the user to fork
// lazydeck. Command may reference {{.Name}}, {{.Machine}}, and {{.Login}}
// (see Expand), which are filled in from the Device the command targets.
type CustomCommand struct {
	Key     string `yaml:"key"`     // e.g. "p", matched against tea.KeyMsg.String()
	Name    string `yaml:"name"`    // short human label shown in the log and help screen
	Command string `yaml:"command"` // shell command template, run via `sh -c`
}

// Expand replaces device placeholders with positional shell parameters.
// customCommandCmd passes the actual values as separate sh arguments, so
// device names discovered over mDNS can never become shell syntax.
func (c CustomCommand) Expand(_ Device) (string, error) {
	tmpl, err := template.New("command").Parse(c.Command)
	if err != nil {
		return "", fmt.Errorf("parsing custom command %q template: %w", c.Key, err)
	}
	var buf bytes.Buffer
	data := struct{ Name, Machine, Login string }{
		Name:    "${1}",
		Machine: "${2}",
		Login:   "${3}",
	}
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("expanding custom command %q: %w", c.Key, err)
	}
	return buf.String(), nil
}

// UserConfig holds user-customizable settings (currently: custom
// keybindings/commands) loaded from config.yml. It composes with, rather
// than replaces, the device list in devices.toml.
type UserConfig struct {
	CustomCommands []CustomCommand `yaml:"customCommands"`
}

// UserConfigPath returns ~/.config/lazydeck/config.yml.
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "lazydeck")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yml"), nil
}

// LoadUserConfig reads the user config at path. Starter creation is
// best-effort because custom commands are optional and must not prevent
// startup from an otherwise readable, immutable configuration directory.
func LoadUserConfig(path string) (*UserConfig, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeUserConfigStarter(path); err != nil {
			return &UserConfig{}, nil
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg UserConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &cfg, nil
}

func writeUserConfigStarter(path string) error {
	const starter = `# lazydeck user config: custom keybindings/commands.
# Composes with devices.toml — this file does not replace it.
#
# customCommands lets you bind a key to an arbitrary shell command run
# against the currently selected device(s) (or all multi-selected devices),
# without forking lazydeck. Commands are run via "sh -c" and may reference:
#   {{.Name}}    - the device's configured name
#   {{.Machine}} - the device's hostname/IP/mDNS name
#   {{.Login}}   - the device's configured login (may be empty)
#
# customCommands:
#   - key: "p"
#     name: "ping device"
#     command: "ping -c 3 {{.Machine}}"
#   - key: "u"
#     name: "uptime"
#     command: "ssh {{.Login}}@{{.Machine}} uptime"
`
	return os.WriteFile(path, []byte(starter), 0o644)
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
