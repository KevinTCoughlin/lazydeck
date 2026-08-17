// Package tui implements the lazydocker-style interactive terminal UI:
// a panel per configured devkit (Steam Machine / Steam Deck / ...) with
// keyboard-driven pair / deploy / status-refresh / log-tail / remote-shell
// actions, each backed by the vendored Python devkit client.
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
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

// promptStep drives the small sequential text-input flows used by the
// deploy / sync-logs / delete keybindings (each may need 1-2 free-form
// values, e.g. a gameid and a local directory).
type promptStep int

const (
	promptNone promptStep = iota
	promptDeployName
	promptDeployDir
	promptLogsName
	promptLogsDir
	promptDeleteName
)

type Model struct {
	cli     *client.Client
	devices []deviceState
	cursor  int
	log     []string // recent action log, most recent last
	width   int
	height  int

	step        promptStep
	input       textinput.Model
	pendingName string // gameid captured in step 1, used in step 2
}

func New(cli *client.Client, cfg *config.Config) Model {
	devices := make([]deviceState, len(cfg.Devices))
	for i, d := range cfg.Devices {
		devices[i] = deviceState{dev: d, statusMsg: "unknown (press s to refresh)"}
	}
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 60
	return Model{cli: cli, devices: devices, input: ti}
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

func deployCmd(cli *client.Client, index int, dev config.Device, name, dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := cli.Deploy(ctx, dev.Machine, dev.Login, name, dir, false)
		return actionDoneMsg{index: index, action: fmt.Sprintf("deploy %s", name), err: err}
	}
}

func syncLogsCmd(cli *client.Client, index int, dev config.Device, name, dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := cli.SyncLogs(ctx, dev.Machine, dev.Login, name, dir)
		return actionDoneMsg{index: index, action: fmt.Sprintf("sync-logs %s", name), err: err}
	}
}

func deleteCmd(cli *client.Client, index int, dev config.Device, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cli.Delete(ctx, dev.Machine, dev.Login, name)
		return actionDoneMsg{index: index, action: fmt.Sprintf("delete %s", name), err: err}
	}
}

type listGamesResultMsg struct {
	index int
	games []any
	err   error
}

func listGamesCmd(cli *client.Client, index int, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		games, err := cli.ListGames(ctx, dev.Machine, dev.Login)
		return listGamesResultMsg{index: index, games: games, err: err}
	}
}

type discoverResultMsg struct {
	found []client.DiscoveredDevice
	err   error
}

func discoverCmd(cli *client.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		found, err := cli.Discover(ctx, 4*time.Second)
		return discoverResultMsg{found: found, err: err}
	}
}

// connInfoMsg carries the resolved SSH login/address/key so we can exec a
// real interactive `ssh` process for the remote-shell keybinding.
type connInfoMsg struct {
	index int
	info  *client.ConnectionInfo
	err   error
}

func connectionInfoCmd(cli *client.Client, index int, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := cli.ConnectionInfo(ctx, dev.Machine, dev.Login)
		return connInfoMsg{index: index, info: info, err: err}
	}
}

type shellExitedMsg struct {
	index int
	err   error
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.step != promptNone {
			return m.updatePrompt(msg)
		}
		return m.updateNormal(msg)

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

	case connInfoMsg:
		m.devices[msg.index].busy = false
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: could not resolve ssh connection: %v", m.devices[msg.index].dev.Name, msg.err))
			return m, nil
		}
		info := msg.info
		sshCmd := exec.Command("ssh",
			"-i", info.KeyPath,
			"-o", "IdentitiesOnly=yes",
			"-o", "StrictHostKeyChecking=accept-new",
			fmt.Sprintf("%s@%s", info.Login, info.Address),
		)
		index := msg.index
		return m, tea.ExecProcess(sshCmd, func(err error) tea.Msg {
			return shellExitedMsg{index: index, err: err}
		})

	case shellExitedMsg:
		name := m.devices[msg.index].dev.Name
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: shell exited: %v", name, msg.err))
		} else {
			m.log = append(m.log, fmt.Sprintf("%s: shell closed", name))
		}
		return m, nil

	case listGamesResultMsg:
		m.devices[msg.index].busy = false
		name := m.devices[msg.index].dev.Name
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: list-games FAILED: %v", name, msg.err))
			return m, nil
		}
		if len(msg.games) == 0 {
			m.log = append(m.log, fmt.Sprintf("%s: no games deployed", name))
			return m, nil
		}
		m.log = append(m.log, fmt.Sprintf("%s: %d game(s):", name, len(msg.games)))
		for _, g := range msg.games {
			m.log = append(m.log, "  - "+fmt.Sprint(g))
		}
		return m, nil

	case discoverResultMsg:
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("discover FAILED: %v", msg.err))
			return m, nil
		}
		if len(msg.found) == 0 {
			m.log = append(m.log, "discover: no devkits announced themselves on the LAN")
			return m, nil
		}
		for _, d := range msg.found {
			m.log = append(m.log, fmt.Sprintf("discover: found %q at %s:%d — add it to devices.toml", d.Name, d.Address, d.Port))
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
	case "d":
		if len(m.devices) == 0 {
			break
		}
		return m.startPrompt(promptDeployName, "gameid to deploy as: "), nil
	case "l":
		if len(m.devices) == 0 {
			break
		}
		return m.startPrompt(promptLogsName, "gameid to fetch logs for: "), nil
	case "x":
		if len(m.devices) == 0 {
			break
		}
		return m.startPrompt(promptDeleteName, "gameid to delete: "), nil
	case "g":
		if len(m.devices) == 0 {
			break
		}
		d := m.devices[m.cursor]
		m.devices[m.cursor].busy = true
		m.log = append(m.log, fmt.Sprintf("listing games on %s ...", d.dev.Name))
		return m, listGamesCmd(m.cli, m.cursor, d.dev)
	case "f":
		m.log = append(m.log, "discovering devkits on the LAN (mDNS, ~4s)...")
		return m, discoverCmd(m.cli)
	case "enter":
		if len(m.devices) == 0 {
			break
		}
		d := m.devices[m.cursor]
		m.devices[m.cursor].busy = true
		m.log = append(m.log, fmt.Sprintf("opening remote shell to %s ...", d.dev.Name))
		return m, connectionInfoCmd(m.cli, m.cursor, d.dev)
	}
	return m, nil
}

func (m Model) startPrompt(step promptStep, placeholder string) Model {
	m.step = step
	m.input.SetValue("")
	m.input.Placeholder = placeholder
	m.input.Focus()
	return m
}

func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.step = promptNone
		m.input.Blur()
		return m, nil
	case "enter":
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return m, nil
		}
		return m.advancePrompt(value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// advancePrompt runs after Enter on a populated prompt: either moves to the
// next field (e.g. gameid -> directory) or fires the underlying action.
func (m Model) advancePrompt(value string) (tea.Model, tea.Cmd) {
	d := m.devices[m.cursor]
	switch m.step {
	case promptDeployName:
		m.pendingName = value
		return m.startPrompt(promptDeployDir, "local build directory to rsync up: "), nil
	case promptDeployDir:
		m.step = promptNone
		m.input.Blur()
		m.devices[m.cursor].busy = true
		m.log = append(m.log, fmt.Sprintf("deploying %s (%s) to %s ...", m.pendingName, value, d.dev.Name))
		return m, deployCmd(m.cli, m.cursor, d.dev, m.pendingName, value)
	case promptLogsName:
		m.pendingName = value
		return m.startPrompt(promptLogsDir, "local directory to save logs into: "), nil
	case promptLogsDir:
		m.step = promptNone
		m.input.Blur()
		m.devices[m.cursor].busy = true
		m.log = append(m.log, fmt.Sprintf("syncing logs for %s from %s ...", m.pendingName, d.dev.Name))
		return m, syncLogsCmd(m.cli, m.cursor, d.dev, m.pendingName, value)
	case promptDeleteName:
		m.step = promptNone
		m.input.Blur()
		m.devices[m.cursor].busy = true
		m.log = append(m.log, fmt.Sprintf("deleting %s from %s ...", value, d.dev.Name))
		return m, deleteCmd(m.cli, m.cursor, d.dev, value)
	}
	m.step = promptNone
	return m, nil
}

// --- view ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
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

	if m.step != promptNone {
		b.WriteString("\n" + promptStyle.Render(m.input.Placeholder) + "\n" + m.input.View() + "\n")
		b.WriteString(helpStyle.Render("enter confirm · esc cancel"))
		return b.String()
	}

	b.WriteString("\n" + helpStyle.Render("↑/↓ select · s refresh · r register · d deploy · l sync-logs · x delete · g games · f find (mDNS) · enter shell · q quit"))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
