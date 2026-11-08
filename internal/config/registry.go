package config

// Shared device registry (issue #48).
//
// A team keeps one devices file somewhere everyone can read: a network
// share, a git checkout, or a config-management-provisioned path. Each
// engineer's own devices.toml points at it with `registry = "<path>"`
// (or LAZYDECK_REGISTRY, or `serve --registry`), and lazydeck merges the
// two at load time.
//
// The shared registry is read-only as far as lazydeck is concerned:
// nothing here ever writes to it, so concurrent edits are the team's
// version-control or file-share problem, not a lock-file protocol every
// lazydeck instance has to get right over NFS/SMB. The TUI's "add device"
// wizard writes to the local devices.toml only.
//
// Precedence:
//
//   - Registry path: `serve --registry` flag, then LAZYDECK_REGISTRY, then
//     the `registry` key in devices.toml. An empty value at a higher level
//     falls through to the next one.
//   - Mode: LAZYDECK_REGISTRY_MODE, then `registry_mode` in devices.toml,
//     then "merge".
//   - Devices in "merge" mode: shared entries in registry order, with a
//     local entry of the same name replacing the shared one wholesale (no
//     field-by-field merge), followed by local-only entries in file order.
//   - Devices in "replace" mode: shared entries only. Local [[device]]
//     entries are ignored and counted in RegistryInfo.LocalIgnored.
//
// Everything other than the device list (refresh interval, webhooks) still
// comes from the local file. The registry may contain only [[device]]
// tables; any other key is an error, so nobody assumes a shared
// `webhooks` list takes effect when it doesn't.
//
// A configured registry that is missing or malformed is a load error, not
// a warning: silently falling back to the local list would show a partial
// fleet (or deploy to the wrong devkit) without anyone noticing that the
// share is unmounted.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Origin says where a Device entry came from when a shared registry is in
// use. It is empty when no registry is configured.
type Origin string

const (
	// OriginShared is an entry taken from the shared registry.
	OriginShared Origin = "shared"
	// OriginLocal is an entry only in the local devices.toml.
	OriginLocal Origin = "local"
	// OriginOverride is a local entry that replaced a shared entry with
	// the same name.
	OriginOverride Origin = "override"
)

// Registry modes.
const (
	RegistryMerge   = "merge"
	RegistryReplace = "replace"
)

// Environment variables that select a shared registry.
const (
	EnvRegistry     = "LAZYDECK_REGISTRY"
	EnvRegistryMode = "LAZYDECK_REGISTRY_MODE"
)

// LoadOptions adjusts LoadWithOptions.
type LoadOptions struct {
	// Registry overrides LAZYDECK_REGISTRY and devices.toml's `registry`
	// key (e.g. from `serve --registry`). Empty means no override.
	Registry string
}

type sharedState struct {
	path         string
	mode         string
	local        []Device
	shared       int
	overrides    int
	localIgnored int
}

// RegistryInfo describes the shared registry merged into a Config.
type RegistryInfo struct {
	Path         string // absolute path of the registry file
	Mode         string // RegistryMerge or RegistryReplace
	Shared       int    // devices read from the registry
	Overrides    int    // shared devices replaced by a local entry (merge)
	LocalIgnored int    // local devices ignored (replace)
}

// SharedRegistry reports the shared registry merged into cfg, if any.
func (c *Config) SharedRegistry() (RegistryInfo, bool) {
	if c.shared == nil {
		return RegistryInfo{}, false
	}
	return RegistryInfo{
		Path:         c.shared.path,
		Mode:         c.shared.mode,
		Shared:       c.shared.shared,
		Overrides:    c.shared.overrides,
		LocalIgnored: c.shared.localIgnored,
	}, true
}

// Summary is a one-line description of the merge for logs and banners.
func (r RegistryInfo) Summary() string {
	s := fmt.Sprintf("shared registry %s (%s mode): %d shared device(s)", r.Path, r.Mode, r.Shared)
	switch {
	case r.Mode == RegistryMerge && r.Overrides > 0:
		s += fmt.Sprintf(", %d overridden locally", r.Overrides)
	case r.Mode == RegistryReplace && r.LocalIgnored > 0:
		s += fmt.Sprintf(", %d local device(s) ignored", r.LocalIgnored)
	}
	return s
}

// LoadWithOptions reads the local devices.toml at path (creating a starter
// file if it doesn't exist) and, if a shared registry is configured,
// merges it in according to the precedence rules in this file's header.
func LoadWithOptions(path string, opts LoadOptions) (*Config, error) {
	cfg, err := loadLocal(path)
	if err != nil {
		return nil, err
	}

	regPath, fromFile := opts.Registry, false
	if regPath == "" {
		regPath = os.Getenv(EnvRegistry)
	}
	if regPath == "" {
		regPath, fromFile = cfg.Registry, true
	}
	if regPath == "" {
		return cfg, nil
	}
	regPath, err = resolveRegistryPath(regPath, path, fromFile)
	if err != nil {
		return nil, err
	}
	if same, _ := sameFile(regPath, path); same {
		return nil, fmt.Errorf("shared registry %s is the local devices.toml itself", regPath)
	}

	mode := os.Getenv(EnvRegistryMode)
	source := EnvRegistryMode
	if mode == "" {
		mode, source = cfg.RegistryMode, "registry_mode"
	}
	if mode == "" {
		mode = RegistryMerge
	}
	if mode != RegistryMerge && mode != RegistryReplace {
		return nil, fmt.Errorf("%s = %q: want %q or %q", source, mode, RegistryMerge, RegistryReplace)
	}

	shared, err := loadRegistry(regPath)
	if err != nil {
		return nil, err
	}
	merged, state := mergeDevices(shared, cfg.Devices, mode)
	state.path = regPath
	cfg.Devices = merged
	cfg.shared = state
	return cfg, nil
}

// resolveRegistryPath expands a leading "~/" and, for a path written in
// devices.toml, resolves a relative path against that file's directory
// (so a checked-in devices.toml can say registry = "../team/devices.toml").
// A relative path from the flag or environment resolves against the
// working directory, as a shell user would expect.
func resolveRegistryPath(p, localPath string, fromFile bool) (string, error) {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expanding shared registry path %q: %w", p, err)
		}
		p = filepath.Join(home, rest)
	}
	if fromFile && !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(localPath), p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolving shared registry path %q: %w", p, err)
	}
	return abs, nil
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// registryFile is the shared registry's schema: [[device]] tables only.
type registryFile struct {
	Devices []Device `toml:"device"`
}

// loadRegistry reads and validates a shared registry. Unlike the local
// file it is never created: a missing registry is an error.
func loadRegistry(path string) ([]Device, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("shared registry %s does not exist (is the share mounted or the checkout present?)", path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading shared registry: %w", err)
	}
	var f registryFile
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, fmt.Errorf("parsing shared registry %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("shared registry %s: unsupported key(s) %s (a registry holds only [[device]] tables with name, machine and login; other settings belong in your local devices.toml)", path, strings.Join(keys, ", "))
	}
	seen := make(map[string]bool, len(f.Devices))
	for i, d := range f.Devices {
		if strings.TrimSpace(d.Name) == "" {
			return nil, fmt.Errorf("shared registry %s: device #%d has no name", path, i+1)
		}
		if strings.TrimSpace(d.Machine) == "" {
			return nil, fmt.Errorf("shared registry %s: device %q has no machine", path, d.Name)
		}
		if seen[d.Name] {
			return nil, fmt.Errorf("shared registry %s: more than one device named %q", path, d.Name)
		}
		seen[d.Name] = true
	}
	return f.Devices, nil
}

// mergeDevices applies the registry precedence rules. It returns the merged
// list and the bookkeeping Save and RegistryInfo need.
func mergeDevices(shared, local []Device, mode string) ([]Device, *sharedState) {
	state := &sharedState{mode: mode, shared: len(shared)}
	for _, d := range local {
		d.Origin = OriginLocal
		state.local = append(state.local, d)
	}

	if mode == RegistryReplace {
		state.localIgnored = len(local)
		out := make([]Device, len(shared))
		for i, d := range shared {
			d.Origin = OriginShared
			out[i] = d
		}
		return out, state
	}

	localByName := make(map[string]Device, len(local))
	for _, d := range state.local {
		if _, dup := localByName[d.Name]; !dup {
			localByName[d.Name] = d
		}
	}
	out := make([]Device, 0, len(shared)+len(local))
	overridden := make(map[string]bool)
	for _, d := range shared {
		if l, ok := localByName[d.Name]; ok {
			l.Origin = OriginOverride
			out = append(out, l)
			overridden[d.Name] = true
			continue
		}
		d.Origin = OriginShared
		out = append(out, d)
	}
	state.overrides = len(overridden)
	// Skip only the first local entry of each overriding name (it is
	// already in place above). A second local entry with the same name is
	// kept so callers that reject duplicate names still see it.
	placed := make(map[string]bool, len(overridden))
	for _, d := range state.local {
		if overridden[d.Name] && !placed[d.Name] {
			placed[d.Name] = true
			continue
		}
		out = append(out, d)
	}
	return out, state
}
