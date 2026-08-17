package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/devkit-tui/internal/client"
	"github.com/kevintcoughlin/devkit-tui/internal/config"
)

func newTestModel(t *testing.T, n int) Model {
	t.Helper()
	cfg := &config.Config{}
	for i := 0; i < n; i++ {
		cfg.Devices = append(cfg.Devices, config.Device{Name: "dev", Machine: "1.2.3.4"})
	}
	cli, err := client.New()
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return New(cli, cfg)
}

func sendKey(m Model, key string) Model {
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(Model)
}

func TestCursorNavigationBounds(t *testing.T) {
	m := newTestModel(t, 3)

	m = sendKey(m, "j")
	m = sendKey(m, "j")
	if m.cursor != 2 {
		t.Fatalf("expected cursor at 2 after two downs, got %d", m.cursor)
	}
	// one more "down" past the end should be a no-op
	m = sendKey(m, "j")
	if m.cursor != 2 {
		t.Fatalf("expected cursor clamped at 2, got %d", m.cursor)
	}

	m = sendKey(m, "k")
	if m.cursor != 1 {
		t.Fatalf("expected cursor at 1 after one up, got %d", m.cursor)
	}
}

func TestDeployPromptSequence(t *testing.T) {
	m := newTestModel(t, 1)

	m = sendKey(m, "d")
	if m.step != promptDeployName {
		t.Fatalf("expected promptDeployName after 'd', got %v", m.step)
	}

	// type a gameid then confirm
	for _, r := range "my-game" {
		m = sendKey(m, string(r))
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.step != promptDeployDir {
		t.Fatalf("expected promptDeployDir after confirming name, got %v", m.step)
	}
	if m.pendingName != "my-game" {
		t.Fatalf("expected pendingName 'my-game', got %q", m.pendingName)
	}
	if cmd != nil {
		t.Fatalf("expected no command while still collecting fields, got one")
	}

	// type a directory then confirm -> should fire deployCmd and clear the prompt
	for _, r := range "/tmp/build" {
		m = sendKey(m, string(r))
	}
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.step != promptNone {
		t.Fatalf("expected prompt to close after final confirm, got %v", m.step)
	}
	if cmd == nil {
		t.Fatalf("expected deployCmd to be returned after final confirm")
	}
	if !m.devices[0].busy {
		t.Fatalf("expected device marked busy while deploy runs")
	}
	if len(m.log) == 0 || !strings.Contains(m.log[len(m.log)-1], "deploying my-game") {
		t.Fatalf("expected a deploy log entry, got %v", m.log)
	}
}

func TestEscCancelsPrompt(t *testing.T) {
	m := newTestModel(t, 1)
	m = sendKey(m, "x")
	if m.step != promptDeleteName {
		t.Fatalf("expected promptDeleteName after 'x', got %v", m.step)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.step != promptNone {
		t.Fatalf("expected esc to cancel prompt, got step %v", m.step)
	}
}

func TestStatusResultUpdatesDeviceState(t *testing.T) {
	m := newTestModel(t, 2)
	m.devices[0].busy = true

	updated, _ := m.Update(statusResultMsg{index: 0, msg: "online"})
	m = updated.(Model)
	if m.devices[0].busy {
		t.Fatalf("expected busy cleared after status result")
	}
	if m.devices[0].statusMsg != "online" {
		t.Fatalf("expected statusMsg 'online', got %q", m.devices[0].statusMsg)
	}

	updated, _ = m.Update(statusResultMsg{index: 1, err: errFake{}})
	m = updated.(Model)
	if m.devices[1].statusMsg != "offline / unpaired" {
		t.Fatalf("expected offline status on error, got %q", m.devices[1].statusMsg)
	}
	if m.devices[1].lastErr == nil {
		t.Fatalf("expected lastErr to be set")
	}
}

func TestQuitOnQ(t *testing.T) {
	m := newTestModel(t, 1)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatalf("expected a command from 'q'")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg from 'q', got %T", msg)
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }
