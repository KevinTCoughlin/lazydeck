package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(devs []Device) string {
	parts := make([]string, len(devs))
	for i, d := range devs {
		parts[i] = d.Name + "=" + d.Machine + "/" + string(d.Origin)
	}
	return strings.Join(parts, ",")
}

const sharedRegistry = `
[[device]]
name = "lab-deck-1"
machine = "10.0.0.11"

[[device]]
name = "lab-deck-2"
machine = "10.0.0.12"
login = "deck"
`

// clearRegistryEnv keeps a developer's own LAZYDECK_REGISTRY* from leaking
// into these tests.
func clearRegistryEnv(t *testing.T) {
	t.Setenv(EnvRegistry, "")
	t.Setenv(EnvRegistryMode, "")
}

func TestRegistryMergeLocalOverridesSharedByName(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "team", "devices.toml"), sharedRegistry)
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, `registry = "team/devices.toml"

[[device]]
name = "my-deck"
machine = "192.168.1.5"

[[device]]
name = "lab-deck-2"
machine = "10.0.0.99"
`)

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	want := "lab-deck-1=10.0.0.11/shared,lab-deck-2=10.0.0.99/override,my-deck=192.168.1.5/local"
	if got := names(cfg.Devices); got != want {
		t.Fatalf("merged devices = %s, want %s", got, want)
	}
	// The override replaces the whole entry: the shared login doesn't leak in.
	if cfg.Devices[1].Login != "" {
		t.Fatalf("override kept shared login %q", cfg.Devices[1].Login)
	}
	info, ok := cfg.SharedRegistry()
	if !ok || info.Mode != RegistryMerge || info.Shared != 2 || info.Overrides != 1 {
		t.Fatalf("SharedRegistry() = %+v, %v", info, ok)
	}
	if info.Path != filepath.Join(dir, "team", "devices.toml") {
		t.Fatalf("relative registry path resolved to %s", info.Path)
	}
}

func TestRegistryReplaceIgnoresLocalDevices(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.toml")
	writeFile(t, shared, sharedRegistry)
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, `registry = "`+filepath.ToSlash(shared)+`"
registry_mode = "replace"

[[device]]
name = "my-deck"
machine = "192.168.1.5"
`)
	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(cfg.Devices), "lab-deck-1=10.0.0.11/shared,lab-deck-2=10.0.0.12/shared"; got != want {
		t.Fatalf("devices = %s, want %s", got, want)
	}
	info, _ := cfg.SharedRegistry()
	if info.LocalIgnored != 1 || !strings.Contains(info.Summary(), "1 local device(s) ignored") {
		t.Fatalf("info = %+v, summary %q", info, info.Summary())
	}
	if err := AddDevice(local, cfg, Device{Name: "new", Machine: "x"}); err == nil || !strings.Contains(err.Error(), "replaces local devices") {
		t.Fatalf("AddDevice in replace mode = %v, want refusal", err)
	}
}

func TestRegistryPrecedenceFlagOverEnvOverFile(t *testing.T) {
	dir := t.TempDir()
	fileReg := filepath.Join(dir, "file.toml")
	envReg := filepath.Join(dir, "env.toml")
	flagReg := filepath.Join(dir, "flag.toml")
	writeFile(t, fileReg, "[[device]]\nname = \"from-file\"\nmachine = \"f\"\n")
	writeFile(t, envReg, "[[device]]\nname = \"from-env\"\nmachine = \"e\"\n")
	writeFile(t, flagReg, "[[device]]\nname = \"from-flag\"\nmachine = \"g\"\n")
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, `registry = "file.toml"
registry_mode = "merge"
`)

	clearRegistryEnv(t)
	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Devices); got != "from-file=f/shared" {
		t.Fatalf("file only: %s", got)
	}

	t.Setenv(EnvRegistry, envReg)
	t.Setenv(EnvRegistryMode, "replace")
	cfg, err = Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := cfg.SharedRegistry(); names(cfg.Devices) != "from-env=e/shared" || info.Mode != RegistryReplace {
		t.Fatalf("env: %s %+v", names(cfg.Devices), info)
	}

	cfg, err = LoadWithOptions(local, LoadOptions{Registry: flagReg})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(cfg.Devices); got != "from-flag=g/shared" {
		t.Fatalf("flag: %s", got)
	}
}

func TestNoRegistryLeavesDevicesUntouched(t *testing.T) {
	clearRegistryEnv(t)
	local := filepath.Join(t.TempDir(), "devices.toml")
	writeFile(t, local, "[[device]]\nname = \"a\"\nmachine = \"m\"\n")
	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.SharedRegistry(); ok || names(cfg.Devices) != "a=m/" {
		t.Fatalf("devices = %s", names(cfg.Devices))
	}
}

func TestMalformedRegistryIsAnError(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"bad toml":       {"[[device]\nname = ", "parsing shared registry"},
		"unknown key":    {"webhooks = [\"https://example.invalid\"]\n[[device]]\nname = \"a\"\nmachine = \"m\"\n", "unsupported key(s) webhooks"},
		"unknown field":  {"[[device]]\nname = \"a\"\nmachine = \"m\"\nport = 22\n", "unsupported key(s) device.port"},
		"missing name":   {"[[device]]\nmachine = \"m\"\n", "device #1 has no name"},
		"missing addr":   {"[[device]]\nname = \"a\"\n", `device "a" has no machine`},
		"duplicate name": {"[[device]]\nname = \"a\"\nmachine = \"m\"\n[[device]]\nname = \"a\"\nmachine = \"n\"\n", `more than one device named "a"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			clearRegistryEnv(t)
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "shared.toml"), tc.body)
			local := filepath.Join(dir, "devices.toml")
			writeFile(t, local, "registry = \"shared.toml\"\n")
			_, err := Load(local)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestMissingRegistryIsAnError(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, "registry = \"nope.toml\"\n")
	if _, err := Load(local); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Load = %v, want missing-registry error", err)
	}
}

func TestInvalidRegistryModeIsAnError(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shared.toml"), sharedRegistry)
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, "registry = \"shared.toml\"\nregistry_mode = \"union\"\n")
	if _, err := Load(local); err == nil || !strings.Contains(err.Error(), `registry_mode = "union"`) {
		t.Fatalf("Load = %v, want mode error", err)
	}
}

func TestRegistryPointingAtLocalFileIsAnError(t *testing.T) {
	clearRegistryEnv(t)
	local := filepath.Join(t.TempDir(), "devices.toml")
	writeFile(t, local, "registry = \"devices.toml\"\n")
	if _, err := Load(local); err == nil || !strings.Contains(err.Error(), "local devices.toml itself") {
		t.Fatalf("Load = %v, want self-reference error", err)
	}
}

// AddDevice in merge mode writes only local devices to the local file, keeps
// the registry setting, and never touches the shared registry.
func TestAddDeviceWithRegistryWritesLocalOnly(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.toml")
	writeFile(t, shared, sharedRegistry)
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, "registry = \"shared.toml\"\n\n[[device]]\nname = \"mine\"\nmachine = \"m\"\n")

	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if err := AddDevice(local, cfg, Device{Name: "lab-deck-1", Machine: "dup"}); err == nil {
		t.Fatal("AddDevice accepted a name already in the shared registry")
	}
	if err := AddDevice(local, cfg, Device{Name: "new", Machine: "n"}); err != nil {
		t.Fatal(err)
	}
	if got, want := names(cfg.Devices), "lab-deck-1=10.0.0.11/shared,lab-deck-2=10.0.0.12/shared,mine=m/local,new=n/local"; got != want {
		t.Fatalf("in-memory devices = %s, want %s", got, want)
	}

	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "lab-deck") || !strings.Contains(string(data), `registry = "shared.toml"`) {
		t.Fatalf("local file after AddDevice:\n%s", data)
	}
	sharedData, err := os.ReadFile(shared)
	if err != nil {
		t.Fatal(err)
	}
	if string(sharedData) != sharedRegistry {
		t.Fatal("AddDevice modified the shared registry")
	}

	reloaded, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if names(reloaded.Devices) != names(cfg.Devices) {
		t.Fatalf("reloaded = %s, in-memory = %s", names(reloaded.Devices), names(cfg.Devices))
	}
}

// A duplicate local name survives the merge so server.New can still reject
// it, instead of being silently dropped.
func TestRegistryMergeKeepsLocalDuplicates(t *testing.T) {
	clearRegistryEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shared.toml"), sharedRegistry)
	local := filepath.Join(dir, "devices.toml")
	writeFile(t, local, "registry = \"shared.toml\"\n[[device]]\nname = \"lab-deck-1\"\nmachine = \"a\"\n[[device]]\nname = \"lab-deck-1\"\nmachine = \"b\"\n")
	cfg, err := Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := names(cfg.Devices), "lab-deck-1=a/override,lab-deck-2=10.0.0.12/shared,lab-deck-1=b/local"; got != want {
		t.Fatalf("devices = %s, want %s", got, want)
	}
}
