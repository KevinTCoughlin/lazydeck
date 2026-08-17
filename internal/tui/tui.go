// Package tui implements the lazydocker-style interactive terminal UI:
// a panel per configured devkit (Steam Machine / Steam Deck / ...) with
// keyboard-driven pair / deploy / status-refresh / log-tail / remote-shell
// actions, each backed by the vendored Python devkit client.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevintcoughlin/devkit-tui/internal/client"
	"github.com/kevintcoughlin/devkit-tui/internal/config"
)

type deviceState struct {
	dev       config.Device
	statusMsg string // one-line human summary, e.g. "online · SteamOS 3.6 · plasma-wayland"
	busy      bool
	lastErr   error
}

type Model struct {
	cli     *client.Client
	devices []deviceState
	cursor  int
	log     []string // recent action log, most recent last
	width   int
	height  int
	quitErr error
}

func New(cli *client.Client, cfg *config.Config) Model {
	devices := make([]deviceState, len(cfg.Devices))
	for i, d := range cfg.Devices {
		devices[i] = deviceState{dev: d, statusMsg: "unknown (press s to refresh)"}
	}
	return Model{cli: cli, devices: devices}
}

func (m Model) Init() tea.Cmd {
	if len(m.devices) == 0 {
		return nil
	}
	return refreshAllCmd(m.cli, m.devices)
}

// --- messages ---

type statusResultMsg struct {
	index int
	msg   string
	err   error
}

func refreshAllCmd(cli *client.Client, devices []deviceState) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(devices))
	for i := range devices {
		cmds = append(cmds, refreshOneCmd(cli, i, devices[i].dev))
	}
	return tea.Batch(cmds...)
}

func refreshOneCmd(cli *client.Client, index int, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, err := cli.Status(ctx, dev.Machine, dev.Login)
		if err != nil {
			return statusResultMsg{index: index, err: err}
		}
		summary := "online"
		if session, ok := st.Raw["session"].(string); ok && session != "" {
			summary += " · session=" + session
		}
		if osInfo, ok := st.Raw["os"].(string); ok && osInfo != "" {
			summary += " · " + osInfo
		}
		return statusResultMsg{index: index, msg: summary}
	}
}

type actionDoneMsg struct {
	index  int
	action string
	err    error
}

func registerCmd(cli *client.Client, index int, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cli.Register(ctx, dev.Machine)
		return actionDoneMsg{index: index, action: "register", err: err}
	}
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.devices)-1 {
				m.cursor++
			}
		case "s":
			if len(m.devices) == 0 {
				break
			}
			return m, refreshAllCmd(m.cli, m.devices)
		case "r":
			if len(m.devices) == 0 {
				break
			}
			d := m.devices[m.cursor]
			m.devices[m.cursor].busy = true
			m.log = append(m.log, fmt.Sprintf("registering %s ...", d.dev.Name))
			return m, registerCmd(m.cli, m.cursor, d.dev)
		}
		return m, nil

	case statusResultMsg:
		m.devices[msg.index].busy = false
		if msg.err != nil {
			m.devices[msg.index].statusMsg = "offline / unpaired"
			m.devices[msg.index].lastErr = msg.err
		} else {
			m.devices[msg.index].statusMsg = msg.msg
			m.devices[msg.index].lastErr = nil
		}
		return m, nil

	case actionDoneMsg:
		m.devices[msg.index].busy = false
		name := m.devices[msg.index].dev.Name
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: %s FAILED: %v", name, msg.action, msg.err))
		} else {
			m.log = append(m.log, fmt.Sprintf("%s: %s OK", name, msg.action))
			// re-check status after a successful action
			return m, refreshOneCmd(m.cli, msg.index, m.devices[msg.index].dev)
		}
		return m, nil
	}
	return m, nil
}

// --- view ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("devkit-tui") + dimStyle.Render("  — Steam devkit fleet manager") + "\n\n")

	if len(m.devices) == 0 {
		b.WriteString("No devices configured yet.\n")
		b.WriteString(dimStyle.Render("Add a [[device]] block to your devices.toml and restart.") + "\n")
	}

	for i, d := range m.devices {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		state := d.statusMsg
		if d.busy {
			state = "working..."
		}
		line := fmt.Sprintf("%s%-24s %s", cursor, d.dev.Name, state)
		if d.lastErr != nil {
			line += "  " + errStyle.Render("("+truncate(d.lastErr.Error(), 40)+")")
		}
		b.WriteString(style.Render(line) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render(strings.Repeat("─", 60)) + "\n")
	start := 0
	if len(m.log) > 8 {
		start = len(m.log) - 8
	}
	for _, entry := range m.log[start:] {
		b.WriteString(dimStyle.Render(entry) + "\n")
	}

	b.WriteString("\n" + helpStyle.Render("↑/↓ select · s refresh status · r register/pair · q quit"))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
