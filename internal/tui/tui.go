// Package tui implements the lazydocker-style interactive terminal UI:
// a panel per configured devkit (Steam Machine / Steam Deck / ...) with
// keyboard-driven pair / deploy / status-refresh / log-tail / remote-shell
// actions, each backed by the vendored Python devkit client.
package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	// opID identifies the operation currently occupying this device (0 when
	// idle/never-run). A device accepts at most one manual operation at a
	// time; async completions carry the opID they were started with so a
	// stale or superseded result can never clear a newer operation's busy
	// state or overwrite its status. See Model.beginOp / Model.finishOp.
	opID    uint64
	lastErr error
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

	// refreshInterval, when > 0, drives a background tea.Tick that
	// re-refreshes every device's status on a schedule (see issue #3).
	refreshInterval time.Duration

	// filterEditing is true while the user is actively typing a '/'
	// fuzzy-filter query; filterQuery is the last-applied query (kept
	// after Enter so the filter stays active while browsing).
	filterEditing bool
	filterQuery   string
	filterInput   textinput.Model

	customCmds     []config.CustomCommand
	customCmdIndex map[string]config.CustomCommand

	// opCounter hands out monotonically increasing operation ids so each
	// started device operation is uniquely identifiable (see deviceState.opID).
	opCounter uint64
}

var reservedKeys = map[string]bool{
	"ctrl+c": true, "q": true, "esc": true,
	"up": true, "k": true, "down": true, "j": true,
	"/": true, "s": true, "r": true, " ": true,
	"d": true, "l": true, "x": true, "g": true, "f": true, "a": true,
	"?": true, "enter": true,
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
	return NewWithUserConfig(cli, cfg, path, nil)
}

func NewWithUserConfig(cli *client.Client, cfg *config.Config, path string, userCfg *config.UserConfig) Model {
	devices := make([]deviceState, len(cfg.Devices))
	for i, d := range cfg.Devices {
		devices[i] = deviceState{dev: d, statusMsg: "unknown (press s to refresh)"}
	}
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Width = 60
	fi := textinput.New()
	fi.CharLimit = 128
	fi.Width = 40
	fi.Prompt = "/"
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	var interval time.Duration
	if cfg.RefreshIntervalSeconds > 0 {
		interval = time.Duration(cfg.RefreshIntervalSeconds) * time.Second
	}
	customCmdIndex := make(map[string]config.CustomCommand)
	var customCmds []config.CustomCommand
	if userCfg != nil {
		for _, command := range userCfg.CustomCommands {
			if command.Key == "" || reservedKeys[command.Key] {
				continue
			}
			if _, duplicate := customCmdIndex[command.Key]; duplicate {
				continue
			}
			customCmdIndex[command.Key] = command
			customCmds = append(customCmds, command)
		}
	}
	return Model{
		cli:             cli,
		devices:         devices,
		input:           ti,
		filterInput:     fi,
		selected:        make(map[int]bool),
		spinner:         sp,
		configPath:      path,
		cfg:             cfg,
		refreshInterval: interval,
		customCmds:      customCmds,
		customCmdIndex:  customCmdIndex,
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if len(m.devices) > 0 {
		cmds = append(cmds, func() tea.Msg { return initialRefreshMsg{} })
	}
	if m.refreshInterval > 0 {
		cmds = append(cmds, autoRefreshTickCmd(m.refreshInterval))
	}
	return tea.Batch(cmds...)
}

// autoRefreshTickMsg fires on the configured refresh_interval_seconds
// schedule to periodically re-check every device's status in the
// background, similar to lazygit/lazydocker's auto-refresh (issue #3).
type autoRefreshTickMsg struct{}
type initialRefreshMsg struct{}

func autoRefreshTickCmd(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return autoRefreshTickMsg{} })
}

// --- messages ---

type statusResultMsg struct {
	index int
	opID  uint64
	msg   string
	err   error
}

func refreshOneCmd(cli *client.Client, index int, opID uint64, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		st, err := cli.Status(ctx, dev.Machine, dev.Login)
		if err != nil {
			return statusResultMsg{index: index, opID: opID, err: err}
		}
		summary := "online"
		if session, ok := st.Raw["session"].(string); ok && session != "" {
			summary += " · session=" + session
		}
		if osInfo, ok := st.Raw["os"].(string); ok && osInfo != "" {
			summary += " · " + osInfo
		}
		return statusResultMsg{index: index, opID: opID, msg: summary}
	}
}

type actionDoneMsg struct {
	index  int
	opID   uint64
	action string
	err    error
}

func registerCmd(cli *client.Client, index int, opID uint64, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cli.Register(ctx, dev.Machine)
		return actionDoneMsg{index: index, opID: opID, action: "register", err: err}
	}
}

func deployCmd(cli *client.Client, index int, opID uint64, dev config.Device, name, dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := cli.Deploy(ctx, dev.Machine, dev.Login, name, dir, false)
		return actionDoneMsg{index: index, opID: opID, action: fmt.Sprintf("deploy %s", name), err: err}
	}
}

func syncLogsCmd(cli *client.Client, index int, opID uint64, dev config.Device, name, dir string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		err := cli.SyncLogs(ctx, dev.Machine, dev.Login, name, dir)
		return actionDoneMsg{index: index, opID: opID, action: fmt.Sprintf("sync-logs %s", name), err: err}
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

func deleteCmd(cli *client.Client, index int, opID uint64, dev config.Device, name string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := cli.Delete(ctx, dev.Machine, dev.Login, name)
		return actionDoneMsg{index: index, opID: opID, action: fmt.Sprintf("delete %s", name), err: err}
	}
}

type listGamesResultMsg struct {
	index int
	opID  uint64
	games []any
	err   error
}

func listGamesCmd(cli *client.Client, index int, opID uint64, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		games, err := cli.ListGames(ctx, dev.Machine, dev.Login)
		return listGamesResultMsg{index: index, opID: opID, games: games, err: err}
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
	opID  uint64
	info  *client.ConnectionInfo
	err   error
}

func connectionInfoCmd(cli *client.Client, index int, opID uint64, dev config.Device) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		info, err := cli.ConnectionInfo(ctx, dev.Machine, dev.Login)
		return connInfoMsg{index: index, opID: opID, info: info, err: err}
	}
}

type shellExitedMsg struct {
	index int
	opID  uint64
	err   error
}

type customCmdDoneMsg struct {
	index  int
	opID   uint64
	name   string
	output string
	err    error
}

type tailBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = append([]byte(nil), b.data[len(b.data)-b.limit:]...)
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func customCommandCmd(index int, opID uint64, dev config.Device, custom config.CustomCommand) tea.Cmd {
	return func() tea.Msg {
		expanded, err := custom.Expand(dev)
		if err != nil {
			return customCmdDoneMsg{index: index, opID: opID, name: custom.Name, err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", expanded, "lazydeck", dev.Name, dev.Machine, dev.Login)
		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.WaitDelay = 2 * time.Second
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		output := &tailBuffer{limit: 4096}
		cmd.Stdout = output
		cmd.Stderr = output
		err = cmd.Run()
		return customCmdDoneMsg{index: index, opID: opID, name: custom.Name, output: output.String(), err: err}
	}
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

	case autoRefreshTickMsg:
		var cmd tea.Cmd
		if len(m.devices) > 0 && !m.anyDeviceBusy() {
			cmd = m.beginRefresh()
		}
		return m, tea.Batch(cmd, autoRefreshTickCmd(m.refreshInterval))

	case initialRefreshMsg:
		if len(m.devices) == 0 || m.anyDeviceBusy() {
			return m, nil
		}
		return m, m.beginRefresh()

	case tea.MouseMsg:
		return m.updateMouse(msg)

	case tea.KeyMsg:
		switch m.mode {
		case modeHelp:
			return m.updateHelp(msg)
		case modeWizard:
			return m.updateWizard(msg)
		}
		if m.filterEditing {
			return m.updateFilterInput(msg)
		}
		if m.step != promptNone {
			return m.updatePrompt(msg)
		}
		return m.updateNormal(msg)

	case statusResultMsg:
		if !m.finishOp(msg.index, msg.opID) {
			return m, nil // stale/superseded refresh; ignore
		}
		if msg.err != nil {
			m.devices[msg.index].statusMsg = "offline / unpaired"
			m.devices[msg.index].lastErr = msg.err
		} else {
			m.devices[msg.index].statusMsg = msg.msg
			m.devices[msg.index].lastErr = nil
		}
		return m, nil

	case actionDoneMsg:
		if !m.finishOp(msg.index, msg.opID) {
			return m, nil
		}
		name := m.devices[msg.index].dev.Name
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: %s FAILED: %v", name, msg.action, msg.err))
			return m, nil
		}
		m.log = append(m.log, fmt.Sprintf("%s: %s OK", name, msg.action))
		// Re-check status after a successful action, as a fresh op so it
		// can't be confused with the action that just finished.
		if id, ok := m.beginOp(msg.index); ok {
			return m, refreshOneCmd(m.cli, msg.index, id, m.devices[msg.index].dev)
		}
		return m, nil

	case customCmdDoneMsg:
		if !m.finishOp(msg.index, msg.opID) {
			return m, nil
		}
		name := m.devices[msg.index].dev.Name
		output := strings.TrimSpace(msg.output)
		if msg.err != nil {
			if output != "" {
				m.log = append(m.log, fmt.Sprintf("%s: %s FAILED: %v: %s", name, msg.name, msg.err, truncate(output, 200)))
			} else {
				m.log = append(m.log, fmt.Sprintf("%s: %s FAILED: %v", name, msg.name, msg.err))
			}
		} else if output == "" {
			m.log = append(m.log, fmt.Sprintf("%s: %s OK", name, msg.name))
		} else {
			m.log = append(m.log, fmt.Sprintf("%s: %s: %s", name, msg.name, truncate(output, 200)))
		}
		return m, nil

	case connInfoMsg:
		if msg.index < 0 || msg.index >= len(m.devices) || m.devices[msg.index].opID != msg.opID {
			return m, nil
		}
		if msg.err != nil {
			m.finishOp(msg.index, msg.opID)
			m.log = append(m.log, fmt.Sprintf("%s: could not resolve ssh connection: %v", m.devices[msg.index].dev.Name, msg.err))
			return m, nil
		}
		info := msg.info
		strict := "no"
		if info.StrictHostKeys {
			strict = "accept-new"
		}
		sshCmd := exec.Command("ssh",
			"-i", info.KeyPath,
			"-o", "IdentitiesOnly=yes",
			"-o", "UserKnownHostsFile="+info.KnownHostsPath,
			"-o", "StrictHostKeyChecking="+strict,
			fmt.Sprintf("%s@%s", info.Login, info.Address),
		)
		index, opID := msg.index, msg.opID
		return m, tea.ExecProcess(sshCmd, func(err error) tea.Msg {
			return shellExitedMsg{index: index, opID: opID, err: err}
		})

	case shellExitedMsg:
		if !m.finishOp(msg.index, msg.opID) {
			return m, nil
		}
		name := m.devices[msg.index].dev.Name
		if msg.err != nil {
			m.log = append(m.log, fmt.Sprintf("%s: shell exited: %v", name, msg.err))
		} else {
			m.log = append(m.log, fmt.Sprintf("%s: shell closed", name))
		}
		return m, nil

	case listGamesResultMsg:
		if !m.finishOp(msg.index, msg.opID) {
			return m, nil
		}
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
	case "esc":
		if m.filterQuery != "" {
			m.filterQuery = ""
			m.filterInput.SetValue("")
			m.cursor = 0
		}
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.visibleIndices())-1 {
			m.cursor++
		}
	case "/":
		m.filterEditing = true
		m.filterInput.SetValue(m.filterQuery)
		m.filterInput.CursorEnd()
		m.filterInput.Focus()
		return m, nil
	case "s":
		if len(m.devices) == 0 {
			break
		}
		return m, m.beginRefresh()
	case "r":
		i := m.selectedDeviceIndex()
		if i < 0 {
			break
		}
		id, ok := m.beginOp(i)
		if !ok {
			m.log = append(m.log, fmt.Sprintf("%s is busy; ignoring register", m.devices[i].dev.Name))
			break
		}
		m.log = append(m.log, fmt.Sprintf("registering %s ...", m.devices[i].dev.Name))
		return m, registerCmd(m.cli, i, id, m.devices[i].dev)
	case " ":
		i := m.selectedDeviceIndex()
		if i < 0 {
			break
		}
		if m.selected[i] {
			delete(m.selected, i)
		} else {
			m.selected[i] = true
		}
	case "d":
		if m.selectedDeviceIndex() < 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptDeployName, promptLabel("gameid to deploy as: ", len(m.promptIndices))), nil
	case "l":
		if m.selectedDeviceIndex() < 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptLogsName, promptLabel("gameid to fetch logs for: ", len(m.promptIndices))), nil
	case "x":
		if m.selectedDeviceIndex() < 0 {
			break
		}
		m.promptIndices = m.targetIndices()
		return m.startPrompt(promptDeleteName, promptLabel("gameid to delete: ", len(m.promptIndices))), nil
	case "g":
		i := m.selectedDeviceIndex()
		if i < 0 {
			break
		}
		id, ok := m.beginOp(i)
		if !ok {
			m.log = append(m.log, fmt.Sprintf("%s is busy; ignoring list-games", m.devices[i].dev.Name))
			break
		}
		m.log = append(m.log, fmt.Sprintf("listing games on %s ...", m.devices[i].dev.Name))
		return m, listGamesCmd(m.cli, i, id, m.devices[i].dev)
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
		i := m.selectedDeviceIndex()
		if i < 0 {
			break
		}
		id, ok := m.beginOp(i)
		if !ok {
			m.log = append(m.log, fmt.Sprintf("%s is busy; ignoring shell", m.devices[i].dev.Name))
			break
		}
		m.log = append(m.log, fmt.Sprintf("opening remote shell to %s ...", m.devices[i].dev.Name))
		return m, connectionInfoCmd(m.cli, i, id, m.devices[i].dev)
	default:
		if command, ok := m.customCmdIndex[msg.String()]; ok {
			return m.runCustomCommand(command)
		}
	}
	return m, nil
}

func (m Model) runCustomCommand(command config.CustomCommand) (tea.Model, tea.Cmd) {
	indices := m.targetIndices()
	cmds := make([]tea.Cmd, 0, len(indices))
	for _, i := range indices {
		id, ok := m.beginOp(i)
		if !ok {
			continue
		}
		device := m.devices[i]
		m.log = append(m.log, fmt.Sprintf("running %q on %s ...", command.Name, device.dev.Name))
		cmds = append(cmds, customCommandCmd(i, id, device.dev, command))
	}
	return m, tea.Batch(cmds...)
}

// visibleIndices returns the indices into m.devices that pass the current
// fuzzy filter (see issue #4), in display order. With no filter applied it
// is simply every device index.
func (m Model) visibleIndices() []int {
	if strings.TrimSpace(m.filterQuery) == "" {
		idx := make([]int, len(m.devices))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	idx := make([]int, 0, len(m.devices))
	for i, d := range m.devices {
		if fuzzyMatch(m.filterQuery, d.dev.Name) || fuzzyMatch(m.filterQuery, d.dev.Machine) {
			idx = append(idx, i)
		}
	}
	return idx
}

// selectedDeviceIndex maps m.cursor (a position within the currently
// visible/filtered list) back to an index into m.devices, or -1 if no
// device is visible/selectable.
func (m Model) selectedDeviceIndex() int {
	vis := m.visibleIndices()
	if len(vis) == 0 {
		return -1
	}
	if m.cursor >= len(vis) {
		return vis[len(vis)-1]
	}
	return vis[m.cursor]
}

// fuzzyMatch reports whether every rune of query appears in target, in
// order, case-insensitively (a lightweight fzf-style subsequence match). An
// empty query always matches.
func fuzzyMatch(query, target string) bool {
	query = strings.ToLower(query)
	target = strings.ToLower(target)
	qi := 0
	for _, r := range target {
		if qi == len(query) {
			break
		}
		if r == rune(query[qi]) {
			qi++
		}
	}
	return qi == len(query)
}

// targetIndices returns visible multi-selected device indices, or just the
// cursor's device if no visible device is selected. Filtering must never
// leave an invisible device as the target of a destructive action.
func (m Model) targetIndices() []int {
	idx := make([]int, 0, len(m.selected))
	for _, i := range m.visibleIndices() {
		if m.selected[i] {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return []int{m.selectedDeviceIndex()}
	}
	return idx
}

func (m Model) anyDeviceBusy() bool {
	for _, device := range m.devices {
		if device.busy {
			return true
		}
	}
	return false
}

// beginOp reserves device i for a new operation: it marks the device busy
// under a fresh, unique operation id and returns that id to stamp into the
// async command. It returns (0, false) if i is out of range or the device is
// already running an operation, which is how manual device operations are
// serialized — callers must not start a second concurrent operation on a
// busy device.
func (m *Model) beginOp(i int) (uint64, bool) {
	if i < 0 || i >= len(m.devices) || m.devices[i].busy {
		return 0, false
	}
	m.opCounter++
	m.devices[i].busy = true
	m.devices[i].opID = m.opCounter
	return m.opCounter, true
}

// finishOp clears busy for device i only if the completing operation id still
// matches the device's current operation, so a stale or superseded async
// result can never clear a newer operation's busy state. It returns whether
// the completion was the device's current operation.
func (m *Model) finishOp(i int, opID uint64) bool {
	if i < 0 || i >= len(m.devices) || m.devices[i].opID != opID {
		return false
	}
	m.devices[i].busy = false
	return true
}

func (m *Model) beginRefresh() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.devices))
	for i := range m.devices {
		// Skip devices already running an operation so a fleet refresh never
		// clobbers an in-flight deploy/delete/etc. or its operation id.
		id, ok := m.beginOp(i)
		if !ok {
			continue
		}
		cmds = append(cmds, refreshOneCmd(m.cli, i, id, m.devices[i].dev))
	}
	return tea.Batch(cmds...)
}

// updateFilterInput handles keystrokes while the '/' fuzzy-filter query is
// being actively typed: printable runes/backspace edit the query and
// live-narrow the device list, up/down still move the cursor within the
// narrowed results, enter keeps the filter applied and returns to normal
// browsing, and esc clears the filter entirely.
func (m Model) updateFilterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.filterEditing = false
		m.filterQuery = ""
		m.filterInput.Blur()
		m.cursor = 0
		return m, nil
	case "enter":
		m.filterEditing = false
		m.filterInput.Blur()
		return m, nil
	case "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down":
		if m.cursor < len(m.visibleIndices())-1 {
			m.cursor++
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.filterInput, cmd = m.filterInput.Update(msg)
	m.filterQuery = m.filterInput.Value()
	m.cursor = 0
	return m, cmd
}

// updateMouse handles click-to-select and scroll-to-move mouse events (see
// issue #5): wheel up/down move the cursor like k/j, and a left click on a
// device row selects it. Mouse input is ignored while an overlay (help,
// wizard) or a text-entry prompt/filter is active, to avoid surprising
// interactions with those modal flows.
func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal || m.step != promptNone || m.filterEditing {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.cursor > 0 {
			m.cursor--
		}
	case tea.MouseButtonWheelDown:
		if m.cursor < len(m.visibleIndices())-1 {
			m.cursor++
		}
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		row := msg.Y - m.deviceListTop()
		visible := m.visibleIndices()
		start, end := m.deviceWindow(len(visible))
		if row >= 0 && start+row < end {
			m.cursor = start + row
		}
	}
	return m, nil
}

// deviceListTop returns the row (0-indexed) the first device line is
// rendered on in View, so mouse clicks can be mapped back to a device. It
// must stay in sync with the header lines View() writes before the device
// list.
func (m Model) deviceListTop() int {
	return m.headerHeight() + 2 // panel border + "DEVICES" title
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
			id, ok := m.beginOp(i)
			if !ok {
				m.log = append(m.log, fmt.Sprintf("%s is busy; skipping deploy", m.devices[i].dev.Name))
				continue
			}
			m.log = append(m.log, fmt.Sprintf("deploying %s (%s) to %s ...", m.pendingName, value, m.devices[i].dev.Name))
			cmds = append(cmds, deployCmd(m.cli, i, id, m.devices[i].dev, m.pendingName, value))
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
			id, ok := m.beginOp(i)
			if !ok {
				m.log = append(m.log, fmt.Sprintf("%s is busy; skipping sync-logs", m.devices[i].dev.Name))
				continue
			}
			m.log = append(m.log, fmt.Sprintf("syncing logs for %s from %s ...", m.pendingName, m.devices[i].dev.Name))
			cmds = append(cmds, syncLogsCmd(m.cli, i, id, m.devices[i].dev, m.pendingName, value))
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
		id, ok := m.beginOp(i)
		if !ok {
			m.log = append(m.log, fmt.Sprintf("%s is busy; skipping delete", m.devices[i].dev.Name))
			continue
		}
		d := m.devices[i]
		m.log = append(m.log, fmt.Sprintf("deleting %s from %s (%s) ...", m.pendingName, d.dev.Name, deleteCommandPreview(m.cli, d.dev, m.pendingName)))
		cmds = append(cmds, deleteCmd(m.cli, i, id, d.dev, m.pendingName))
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
		id, _ := m.beginOp(m.cursor)
		return m, registerCmd(m.cli, m.cursor, id, dev)
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

	header := m.headerView()
	footer := m.footerView()
	layout := m.panelLayout(header, footer)
	devicePanel := m.devicePanel(layout.deviceWidth, layout.deviceHeight)
	detailPanel := m.detailPanel(layout.detailWidth, layout.detailHeight)
	logPanel := m.logPanel(layout.logWidth, layout.logHeight)

	var body string
	if layout.wide {
		body = lipgloss.JoinHorizontal(lipgloss.Top, devicePanel, " ", detailPanel, " ", logPanel)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left, devicePanel, detailPanel, logPanel)
	}
	return header + "\n" + body + "\n" + footer
}

type panelGeometry struct {
	wide                                  bool
	deviceWidth, detailWidth, logWidth    int
	deviceHeight, detailHeight, logHeight int
}

func (m Model) panelLayout(header, footer string) panelGeometry {
	width, height := m.width, m.height
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 30
	}
	bodyHeight := max(6, height-lipgloss.Height(header)-lipgloss.Height(footer)-2)
	if width >= 100 {
		deviceWidth := max(28, width*30/100)
		detailWidth := max(28, width*30/100)
		logWidth := max(32, width-deviceWidth-detailWidth-2)
		return panelGeometry{
			wide:        true,
			deviceWidth: deviceWidth, detailWidth: detailWidth, logWidth: logWidth,
			deviceHeight: bodyHeight, detailHeight: bodyHeight, logHeight: bodyHeight,
		}
	}

	usableHeight := max(9, bodyHeight)
	deviceHeight := max(6, usableHeight*40/100)
	detailHeight := max(6, usableHeight*30/100)
	logHeight := max(6, usableHeight-deviceHeight-detailHeight)
	return panelGeometry{
		deviceWidth: width, detailWidth: width, logWidth: width,
		deviceHeight: deviceHeight, detailHeight: detailHeight, logHeight: logHeight,
	}
}

func (m Model) headerView() string {
	header := titleStyle.Render("lazydeck") + dimStyle.Render("  — Steam devkit fleet manager")
	if m.filterEditing {
		return header + "\n" + promptStyle.Render(m.filterInput.View())
	}
	if m.filterQuery != "" {
		return header + "\n" + promptStyle.Render("/"+m.filterQuery) + dimStyle.Render("  (esc to clear)")
	}
	return header
}

func (m Model) headerHeight() int {
	return lipgloss.Height(m.headerView())
}

func (m Model) footerView() string {
	if m.step == promptDeleteConfirm {
		label := fmt.Sprintf("Delete %s from %d device(s)? This cannot be undone.", m.pendingName, len(m.promptIndices))
		return promptStyle.Render(label) + "\n" + helpStyle.Render("y confirm · any other key cancels")
	}
	if m.step != promptNone {
		return promptStyle.Render(m.input.Placeholder) + " " + m.input.View() + "\n" + helpStyle.Render("enter confirm · esc cancel")
	}
	if m.width > 0 && m.width < 100 {
		return helpStyle.Render("? help · ↑/↓ select · / filter · s refresh · q quit")
	}
	return helpStyle.Render("? help · ↑/↓ select · / filter · space multi-select · a add device · s refresh · enter shell · q quit")
}

func renderPanel(title, content string, width, height int) string {
	maxContentLines := max(0, height-3)
	contentLines := strings.Split(content, "\n")
	if len(contentLines) > maxContentLines {
		contentLines = contentLines[:maxContentLines]
		if maxContentLines > 0 {
			contentLines[maxContentLines-1] = dimStyle.Render("…")
		}
		content = strings.Join(contentLines, "\n")
	}
	body := titleStyle.Render(title)
	if content != "" {
		body += "\n" + content
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Width(max(1, width-2)).
		Height(max(1, height-2)).
		Render(body)
}

func (m Model) deviceWindow(total int) (int, int) {
	rows := max(1, m.panelLayout(m.headerView(), m.footerView()).deviceHeight-3)
	if total <= rows {
		return 0, total
	}
	start := max(0, m.cursor-rows+1)
	start = min(start, total-rows)
	return start, start + rows
}

func (m Model) devicePanel(width, height int) string {
	visible := m.visibleIndices()
	if len(m.devices) == 0 {
		return renderPanel("DEVICES", dimStyle.Render("No devices configured.\nPress 'a' to discover one."), width, height)
	}
	if len(visible) == 0 {
		return renderPanel("DEVICES", dimStyle.Render("No matches for "+strconv.Quote(m.filterQuery)), width, height)
	}

	start, end := m.deviceWindow(len(visible))
	title := "DEVICES"
	if start > 0 || end < len(visible) {
		title = fmt.Sprintf("DEVICES  %d-%d/%d", start+1, end, len(visible))
	}
	lines := make([]string, 0, end-start)
	nameWidth := max(8, width-10)
	for row := start; row < end; row++ {
		index := visible[row]
		device := m.devices[index]
		cursor := "  "
		style := lipgloss.NewStyle()
		if row == m.cursor {
			cursor = "> "
			style = selectedStyle
		}
		mark := " "
		if m.selected[index] {
			mark = "*"
		}
		state, stateStyle := m.renderState(device)
		symbol := "[+]"
		if device.busy {
			symbol = "[~]"
		} else if device.lastErr != nil {
			symbol = "[!]"
		} else if strings.HasPrefix(state, "unknown") {
			symbol = "[?]"
		}
		line := fmt.Sprintf("%s%s%-*s %s", cursor, mark, nameWidth, truncate(device.dev.Name, nameWidth), stateStyle.Render(symbol))
		lines = append(lines, style.Render(line))
	}
	return renderPanel(title, strings.Join(lines, "\n"), width, height)
}

func (m Model) detailPanel(width, height int) string {
	index := m.selectedDeviceIndex()
	if index < 0 {
		return renderPanel("DETAIL", dimStyle.Render("No device selected."), width, height)
	}
	device := m.devices[index]
	status, statusStyle := m.renderState(device)
	if device.busy {
		status = m.spinner.View() + " " + status
	}
	valueWidth := max(12, width-12)
	login := device.dev.Login
	if login == "" {
		login = "auto"
	}
	lines := []string{
		dimStyle.Render("Name") + "     " + truncate(device.dev.Name, valueWidth),
		dimStyle.Render("Machine") + "  " + truncate(device.dev.Machine, valueWidth),
		dimStyle.Render("Login") + "    " + truncate(login, valueWidth),
		dimStyle.Render("Status") + "   " + statusStyle.Render(truncate(status, valueWidth)),
	}
	if device.lastErr != nil {
		lines = append(lines, dimStyle.Render("Error")+"    "+errStyle.Render(truncate(device.lastErr.Error(), valueWidth)))
	}
	if len(m.selected) > 0 {
		lines = append(lines, "", dimStyle.Render(fmt.Sprintf("%d device(s) selected for batch actions", len(m.targetIndices()))))
	}
	return renderPanel("DETAIL", strings.Join(lines, "\n"), width, height)
}

func (m Model) logPanel(width, height int) string {
	rows := max(1, height-3)
	start := max(0, len(m.log)-rows)
	if len(m.log) == 0 {
		return renderPanel("ACTIVITY", dimStyle.Render("No activity yet."), width, height)
	}
	lines := make([]string, 0, len(m.log)-start)
	lineWidth := max(12, width-4)
	for _, entry := range m.log[start:] {
		lines = append(lines, dimStyle.Render(truncate(entry, lineWidth)))
	}
	return renderPanel("ACTIVITY", strings.Join(lines, "\n"), width, height)
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
	{"mouse", "click a device to select it, scroll to move the cursor"},
	{"/", "fuzzy-filter the device list by name/machine (esc clears)"},
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
	for _, command := range m.customCmds {
		fmt.Fprintf(&b, "  %-10s %s\n", command.Key, "custom: "+command.Name)
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
