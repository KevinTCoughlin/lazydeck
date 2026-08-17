// Package tui implements the lazydocker-style interactive terminal UI:
// a panel per configured devkit (Steam Machine / Steam Deck / ...) with
// keyboard-driven pair / deploy / status-refresh / log-tail / remote-shell
// actions, each backed by the vendored Python devkit client.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/kevintcoughlin/lazydeck/internal/client"
	"github.com/kevintcoughlin/lazydeck/internal/config"
)

// appMode selects which full-screen overlay (if any) intercepts key input
// on top of the normal device-list view.
type appMode int

const (
	modeNormal appMode = iota
	modeHelp
	modeWizard
)

type deviceState struct {
	dev       config.Device
	statusMsg string // one-line human summary, e.g. "online · SteamOS 3.6 · plasma-wayland"
	busy      bool
	lastErr   error
}

// errorKind returns the coarse category of lastErr (see client.CLIError),
// or "" if there is no error or it isn't a categorized CLIError.
func (d deviceState) errorKind() string {
	if d.lastErr == nil {
		return ""
	}
	var cliErr *client.CLIError
	if errors.As(d.lastErr, &cliErr) {
		return cliErr.Kind
	}
	return ""
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
	// promptDeleteConfirm is a single-keypress (not text-input) yes/no
	// gate before an actual delete fires. Modeled on lazygit's
	// default-on confirmation dialogs for destructive actions (e.g.
	// discard changes, drop stash) — deleting a deployed title is the
	// one lazydeck operation with no undo, so it gets an explicit
	// "are you sure" step rather than firing on a single Enter.
	promptDeleteConfirm
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
	// promptIndices is the set of device indices a multi-step prompt (deploy /
	// sync-logs / delete) will act on, captured when the prompt starts so
	// mid-prompt selection changes can't retarget it.
	promptIndices []int

	// selected tracks multi-selected devices (toggled with space) for batch
	// deploy / sync-logs / delete. Empty selection means "just the cursor".
	selected map[int]bool

	spinner spinner.Model

	mode appMode

	// configPath/cfg are kept so the add-device wizard can persist newly
	// discovered devices back to devices.toml.
	configPath string
	cfg        *config.Config
	wizard     wizardState
}

// wizardState drives the "a" (add device) flow: mDNS-discover devices, let
// the user pick one from a list, then persist + register it.
type wizardState struct {
	items   []client.DiscoveredDevice
	cursor  int
	loading bool
	err     error
}

func New(cli *client.Client, cfg *config.Config) Model {
	return NewWithPath(cli, cfg, "")
}

// NewWithPath is like New but also records where cfg was loaded from, so the
// add-device wizard can persist newly discovered devices. Passing an empty
// path disables the wizard's save step (devices are still addable in
// memory for the running session, just not persisted).
func NewWithPath(cli *client.Client, cfg *config.Config, path string) Model {
	devices := make([]deviceState, len(cfg.Devices))
	for i, d := range cfg.Devices {
		devices[i] = deviceState{dev: d, statusMsg: "unknown (press s to refresh)"}
	}
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 60
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		cli:        cli,
		devices:    devices,
		input:      ti,
		selected:   make(map[int]bool),
		spinner:    sp,
		configPath: path,
		cfg:        cfg,
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if len(m.devices) > 0 {
		cmds = append(cmds, refreshAllCmd(m.cli, m.devices))
	}
	return tea.Batch(cmds...)
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

// deleteCommandPreview renders the exact underlying uv/CLI invocation a
// delete against dev will make, so the log line shows real transparency
// instead of just a friendly paraphrase (see client.Client.Preview).
func deleteCommandPreview(cli *client.Client, dev config.Device, name string) string {
	args := []string{"delete", "--machine", dev.Machine, "--name", name}
	if dev.Login != "" {
		args = append(args, "--login", dev.Login)
	}
	return cli.Preview(args...)
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
	found     []client.DiscoveredDevice
	err       error
	forWizard bool
}

func discoverCmd(cli *client.Client, forWizard bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		found, err := cli.Discover(ctx, 4*time.Second)
		return discoverResultMsg{found: found, err: err, forWizard: forWizard}
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

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		switch m.mode {
		case modeHelp:
			return m.updateHelp(msg)
		case modeWizard:
			return m.updateWizard(msg)
		}
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
		if msg.forWizard {
			m.wizard.loading = false
			m.wizard.err = msg.err
			m.wizard.items = msg.found
			m.wizard.cursor = 0
			return m, nil
		}
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
	case " ":
		if len(m.devices) == 0 {
			break
		}
		if m.selected[m.cursor] {
			delete(m.selected, m.cursor)
		} else {
			m.selected[m.cursor] = true
		}
	case "d":
		if len(m.devices) == 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptDeployName, promptLabel("gameid to deploy as: ", len(m.promptIndices))), nil
	case "l":
		if len(m.devices) == 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptLogsName, promptLabel("gameid to fetch logs for: ", len(m.promptIndices))), nil
	case "x":
		if len(m.devices) == 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptDeleteName, promptLabel("gameid to delete: ", len(m.promptIndices))), nil
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
		return m, discoverCmd(m.cli, false)
	case "a":
		m.mode = modeWizard
		m.wizard = wizardState{loading: true}
		return m, discoverCmd(m.cli, true)
	case "?":
		m.mode = modeHelp
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

// targetIndices returns the multi-selected device indices, or just the
// cursor if nothing is selected — used by deploy/sync-logs/delete so those
// keys transparently batch across a selection made with space.
func (m Model) targetIndices() []int {
	if len(m.selected) == 0 {
		return []int{m.cursor}
	}
	idx := make([]int, 0, len(m.selected))
	for i := range m.devices {
		if m.selected[i] {
			idx = append(idx, i)
		}
	}
	return idx
}

func promptLabel(base string, count int) string {
	if count <= 1 {
		return base
	}
	return fmt.Sprintf("%s (applies to %d selected devices) ", strings.TrimRight(base, " "), count)
}

func (m Model) startPrompt(step promptStep, placeholder string) Model {
	m.step = step
	m.input.SetValue("")
	m.input.Placeholder = placeholder
	m.input.Focus()
	return m
}

func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.step == promptDeleteConfirm {
		return m.updateDeleteConfirm(msg)
	}
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
// next field (e.g. gameid -> directory) or fires the underlying action
// across every device in m.promptIndices (usually just the cursor, or the
// multi-selected set if the user pressed space before d/l/x).
func (m Model) advancePrompt(value string) (tea.Model, tea.Cmd) {
	switch m.step {
	case promptDeployName:
		m.pendingName = value
		return m.startPrompt(promptDeployDir, "local build directory to rsync up: "), nil
	case promptDeployDir:
		m.step = promptNone
		m.input.Blur()
		cmds := make([]tea.Cmd, 0, len(m.promptIndices))
		for _, i := range m.promptIndices {
			d := m.devices[i]
			m.devices[i].busy = true
			m.log = append(m.log, fmt.Sprintf("deploying %s (%s) to %s ...", m.pendingName, value, d.dev.Name))
			cmds = append(cmds, deployCmd(m.cli, i, d.dev, m.pendingName, value))
		}
		m.selected = make(map[int]bool)
		return m, tea.Batch(cmds...)
	case promptLogsName:
		m.pendingName = value
		return m.startPrompt(promptLogsDir, "local directory to save logs into: "), nil
	case promptLogsDir:
		m.step = promptNone
		m.input.Blur()
		cmds := make([]tea.Cmd, 0, len(m.promptIndices))
		for _, i := range m.promptIndices {
			d := m.devices[i]
			m.devices[i].busy = true
			m.log = append(m.log, fmt.Sprintf("syncing logs for %s from %s ...", m.pendingName, d.dev.Name))
			cmds = append(cmds, syncLogsCmd(m.cli, i, d.dev, m.pendingName, value))
		}
		m.selected = make(map[int]bool)
		return m, tea.Batch(cmds...)
	case promptDeleteName:
		// Don't fire yet: capture the gameid and gate the actual delete
		// behind an explicit y/n confirmation (see promptDeleteConfirm).
		m.pendingName = value
		m.step = promptDeleteConfirm
		m.input.Blur()
		return m, nil
	}
	m.step = promptNone
	return m, nil
}

// updateDeleteConfirm handles the single-keypress "are you sure?" gate that
// precedes every delete. Only "y"/"Y" confirms; every other key (including
// esc) cancels without deleting anything.
func (m Model) updateDeleteConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.step = promptNone
	if msg.String() != "y" && msg.String() != "Y" {
		m.log = append(m.log, fmt.Sprintf("delete of %s cancelled", m.pendingName))
		m.selected = make(map[int]bool)
		return m, nil
	}
	cmds := make([]tea.Cmd, 0, len(m.promptIndices))
	for _, i := range m.promptIndices {
		d := m.devices[i]
		m.devices[i].busy = true
		m.log = append(m.log, fmt.Sprintf("deleting %s from %s (%s) ...", m.pendingName, d.dev.Name, deleteCommandPreview(m.cli, d.dev, m.pendingName)))
		cmds = append(cmds, deleteCmd(m.cli, i, d.dev, m.pendingName))
	}
	m.selected = make(map[int]bool)
	return m, tea.Batch(cmds...)
}

// updateHelp handles input while the "?" help overlay is shown: any key
// dismisses it back to the normal device list.
func (m Model) updateHelp(_ tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.mode = modeNormal
	return m, nil
}

// updateWizard drives the "a" (add device) flow: navigate the discovered
// devkit list, and on enter persist the chosen one to devices.toml (if a
// config path was provided) then trigger a register against it.
func (m Model) updateWizard(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.mode = modeNormal
		return m, nil
	case "up", "k":
		if m.wizard.cursor > 0 {
			m.wizard.cursor--
		}
		return m, nil
	case "down", "j":
		if m.wizard.cursor < len(m.wizard.items)-1 {
			m.wizard.cursor++
		}
		return m, nil
	case "enter":
		if m.wizard.loading || len(m.wizard.items) == 0 {
			return m, nil
		}
		found := m.wizard.items[m.wizard.cursor]
		dev := config.Device{Name: found.Name, Machine: found.Address}
		if m.configPath != "" {
			if err := config.AddDevice(m.configPath, m.cfg, dev); err != nil {
				m.log = append(m.log, fmt.Sprintf("add device %q FAILED: %v", found.Name, err))
				m.mode = modeNormal
				return m, nil
			}
		} else {
			m.cfg.Devices = append(m.cfg.Devices, dev)
		}
		m.devices = append(m.devices, deviceState{dev: dev, statusMsg: "unknown (press s to refresh)"})
		m.cursor = len(m.devices) - 1
		m.mode = modeNormal
		m.log = append(m.log, fmt.Sprintf("added %q (%s) — registering ...", dev.Name, dev.Machine))
		m.devices[m.cursor].busy = true
		return m, registerCmd(m.cli, m.cursor, dev)
	}
	return m, nil
}

// --- view ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("120"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func (m Model) View() string {
	switch m.mode {
	case modeHelp:
		return m.helpView()
	case modeWizard:
		return m.wizardView()
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("lazydeck") + dimStyle.Render("  — Steam devkit fleet manager") + "\n\n")

	if len(m.devices) == 0 {
		b.WriteString("No devices configured yet.\n")
		b.WriteString(dimStyle.Render("Press 'a' to discover devkits on the LAN, or add a [[device]] block to devices.toml by hand.") + "\n")
	}

	for i, d := range m.devices {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		mark := " "
		if m.selected[i] {
			mark = "*"
		}
		state, stateStyle := m.renderState(d)
		if d.busy {
			state = m.spinner.View() + " " + state
		}
		line := fmt.Sprintf("%s%s%-24s %s", cursor, mark, d.dev.Name, stateStyle.Render(state))
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

	if m.step == promptDeleteConfirm {
		label := fmt.Sprintf("Delete %s from %d device(s)? This cannot be undone.", m.pendingName, len(m.promptIndices))
		b.WriteString("\n" + promptStyle.Render(label) + "\n")
		b.WriteString(helpStyle.Render("y confirm · any other key cancels"))
		return b.String()
	}

	if m.step != promptNone {
		b.WriteString("\n" + promptStyle.Render(m.input.Placeholder) + "\n" + m.input.View() + "\n")
		b.WriteString(helpStyle.Render("enter confirm · esc cancel"))
		return b.String()
	}

	b.WriteString("\n" + helpStyle.Render("? help · ↑/↓ select · space multi-select · a add device · s refresh · enter shell · q quit"))
	return b.String()
}

// renderState returns the device's one-line status text plus the style to
// color it with: green for a healthy "online" summary, red/yellow/orange
// depending on the categorized error kind (see client.CLIError), or dim
// gray while still unknown.
func (m Model) renderState(d deviceState) (string, lipgloss.Style) {
	if d.lastErr == nil {
		if strings.HasPrefix(d.statusMsg, "unknown") {
			return d.statusMsg, dimStyle
		}
		return d.statusMsg, okStyle
	}
	switch d.errorKind() {
	case "auth-failed", "invalid-input":
		return d.statusMsg, warnStyle
	default:
		return d.statusMsg, errStyle
	}
}

var helpKeys = []struct{ key, desc string }{
	{"↑/k, ↓/j", "move cursor"},
	{"space", "toggle multi-select (batches d/l/x across selection)"},
	{"s", "refresh status of all devices"},
	{"r", "register (pair) this workstation's key with the selected device"},
	{"d", "deploy a build (prompts for gameid + local directory)"},
	{"l", "sync logs down for a gameid (prompts for gameid + local directory)"},
	{"x", "delete a deployed title (prompts for gameid, then y/n confirm)"},
	{"g", "list games deployed on the selected device"},
	{"f", "mDNS-discover devkits on the LAN (logs results only)"},
	{"a", "add device wizard: discover + pick + persist + register"},
	{"enter", "open an interactive SSH shell to the selected device"},
	{"?", "toggle this help screen"},
	{"q, ctrl+c", "quit"},
}

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("lazydeck — keybindings") + "\n\n")
	for _, k := range helpKeys {
		fmt.Fprintf(&b, "  %-10s %s\n", k.key, k.desc)
	}
	b.WriteString("\n" + helpStyle.Render("press any key to go back"))
	return b.String()
}

func (m Model) wizardView() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("lazydeck — add device") + "\n\n")
	if m.wizard.loading {
		b.WriteString(m.spinner.View() + " discovering devkits on the LAN (mDNS, ~4s)...\n")
		b.WriteString("\n" + helpStyle.Render("esc cancel"))
		return b.String()
	}
	if m.wizard.err != nil {
		b.WriteString(errStyle.Render("discover failed: "+m.wizard.err.Error()) + "\n")
		b.WriteString("\n" + helpStyle.Render("esc back"))
		return b.String()
	}
	if len(m.wizard.items) == 0 {
		b.WriteString("No devkits announced themselves on the LAN.\n")
		b.WriteString(dimStyle.Render("Make sure the Deck/Steam Machine is on the same network and Developer Mode pairing is enabled.") + "\n")
		b.WriteString("\n" + helpStyle.Render("esc back"))
		return b.String()
	}
	for i, d := range m.wizard.items {
		cursor := "  "
		style := lipgloss.NewStyle()
		if i == m.wizard.cursor {
			cursor = "> "
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%-24s %s:%d", cursor, d.Name, d.Address, d.Port)) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("↑/↓ select · enter add + register · esc cancel"))
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
