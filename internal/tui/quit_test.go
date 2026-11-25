package tui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// Quitting must cancel the command context and tear down the process group,
// not only the direct shell. A background sleep is the grandchild uv/python
// would leave behind (ssh, rsync) if only the parent were killed.
func TestQuitTerminatesCustomCommandProcessGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	// {{"$!"}} is a template string so Expand does not treat $! as a variable.
	script := `sleep 120 & echo {{"$!"}} > '` + pidFile + `'; wait`

	cfg := &config.Config{Devices: []config.Device{{
		Name: "dev", Machine: "deck.local", Login: "deck",
	}}}
	userCfg := &config.UserConfig{CustomCommands: []config.CustomCommand{{
		Key: "p", Name: "sleep", Command: script,
	}}}
	m := NewWithUserConfig(nil, cfg, "", userCfg)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = updated.(Model)
	if cmd == nil || !m.devices[0].busy {
		t.Fatal("expected the custom command to start")
	}
	finished := make(chan tea.Msg, 1)
	go func() { finished <- cmd() }()

	pid := waitForPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	updated, quit := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(Model)
	if m.commandContext().Err() == nil {
		t.Fatal("quit left the command context running")
	}
	if quit == nil {
		t.Fatal("expected a quit command")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("expected QuitMsg")
	}

	// Same call main makes after the Bubble Tea program returns.
	m.Shutdown()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("command did not return after shutdown")
	}
	if !waitForProcessGone(pid, time.Second) {
		t.Fatalf("grandchild pid %d still running after quit", pid)
	}
}

func waitForPID(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if s := strings.TrimSpace(string(data)); s != "" {
				pid, err := strconv.Atoi(s)
				if err == nil && pid > 0 {
					return pid
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for grandchild pid file")
	return 0
}

func waitForProcessGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
