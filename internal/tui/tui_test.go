package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
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

// TestDeleteRequiresConfirmation exercises the lazygit-style "are you sure?"
// gate: entering a gameid must not fire a delete until 'y' is pressed, and
// any other key cancels without deleting.
func TestDeleteRequiresConfirmation(t *testing.T) {
	m := newTestModel(t, 1)
	m = sendKey(m, "x")
	for _, r := range "my-game" {
		m = sendKey(m, string(r))
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.step != promptDeleteConfirm {
		t.Fatalf("expected promptDeleteConfirm after gameid entry, got %v", m.step)
	}
	if cmd != nil {
		t.Fatalf("expected no command fired before confirmation")
	}
	if m.devices[0].busy {
		t.Fatalf("device should not be busy before confirmation")
	}

	// A non-'y' key cancels without deleting.
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	if m.step != promptNone {
		t.Fatalf("expected prompt to close after cancel, got %v", m.step)
	}
	if cmd != nil {
		t.Fatalf("expected no command after cancelling")
	}
	if m.devices[0].busy {
		t.Fatalf("device should not be busy after cancelling delete")
	}
	if len(m.log) == 0 || !strings.Contains(m.log[len(m.log)-1], "cancelled") {
		t.Fatalf("expected a cancellation log entry, got %v", m.log)
	}

	// Re-run and confirm with 'y' this time -> should fire deleteCmd.
	m = sendKey(m, "x")
	for _, r := range "my-game" {
		m = sendKey(m, string(r))
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(Model)
	if m.step != promptNone {
		t.Fatalf("expected prompt closed after confirm, got %v", m.step)
	}
	if cmd == nil {
		t.Fatalf("expected deleteCmd to be returned after confirming")
	}
	if !m.devices[0].busy {
		t.Fatalf("expected device marked busy while delete runs")
	}
	if len(m.log) == 0 || !strings.Contains(m.log[len(m.log)-1], "deleting my-game") {
		t.Fatalf("expected a delete log entry, got %v", m.log)
	}
	if !strings.Contains(m.log[len(m.log)-1], "uv run") {
		t.Fatalf("expected the log entry to show the real underlying command, got %v", m.log[len(m.log)-1])
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

func TestHelpToggle(t *testing.T) {
	m := newTestModel(t, 1)
	m = sendKey(m, "?")
	if m.mode != modeHelp {
		t.Fatalf("expected modeHelp after '?', got %v", m.mode)
	}
	if !strings.Contains(m.View(), "keybindings") {
		t.Fatalf("expected help view to render keybindings, got: %s", m.View())
	}
	// any key dismisses help
	m = sendKey(m, "x")
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after dismissing help, got %v", m.mode)
	}
}

func TestMultiSelectBatchesDeploy(t *testing.T) {
	m := newTestModel(t, 3)
	m = sendKey(m, " ") // select device 0
	m = sendKey(m, "j")
	m = sendKey(m, " ") // select device 1
	if len(m.selected) != 2 {
		t.Fatalf("expected 2 selected devices, got %d", len(m.selected))
	}

	m = sendKey(m, "d")
	if len(m.promptIndices) != 2 {
		t.Fatalf("expected promptIndices to capture both selections, got %v", m.promptIndices)
	}
	if !strings.Contains(m.input.Placeholder, "2 selected") {
		t.Fatalf("expected placeholder to mention batch size, got %q", m.input.Placeholder)
	}

	for _, r := range "batch-game" {
		m = sendKey(m, string(r))
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	for _, r := range "/tmp/build" {
		m = sendKey(m, string(r))
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected a batched deploy command")
	}
	if !m.devices[0].busy || !m.devices[1].busy {
		t.Fatalf("expected both selected devices marked busy, got %+v", m.devices)
	}
	if m.devices[2].busy {
		t.Fatalf("expected unselected device 2 to remain idle")
	}
	if len(m.selected) != 0 {
		t.Fatalf("expected selection cleared after firing batch, got %v", m.selected)
	}
}

func TestAddDeviceWizardDiscoverAndPick(t *testing.T) {
	m := newTestModel(t, 0)
	m = sendKey(m, "a")
	if m.mode != modeWizard || !m.wizard.loading {
		t.Fatalf("expected wizard mode + loading after 'a', got mode=%v loading=%v", m.mode, m.wizard.loading)
	}

	updated, _ := m.Update(discoverResultMsg{
		forWizard: true,
		found: []client.DiscoveredDevice{
			{Name: "found-deck", Address: "10.0.0.5", Port: 32000},
		},
	})
	m = updated.(Model)
	if m.wizard.loading {
		t.Fatalf("expected loading cleared after discover result")
	}
	if len(m.wizard.items) != 1 {
		t.Fatalf("expected 1 discovered item, got %d", len(m.wizard.items))
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.mode != modeNormal {
		t.Fatalf("expected mode back to normal after picking a device")
	}
	if len(m.devices) != 1 || m.devices[0].dev.Name != "found-deck" {
		t.Fatalf("expected the discovered device added to the list, got %+v", m.devices)
	}
	if cmd == nil {
		t.Fatalf("expected a register command fired after adding the device")
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }
