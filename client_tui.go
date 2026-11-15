package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

var connectionChoices = []int{1, 2, 4, 8}

const (
	menuServer = iota
	menuTests
	menuConnections
	menuDuration
	menuUnit
	menuLanguage
	menuHistory
	menuStart
	menuCount
)

type clientScreen int

const (
	screenLanguage clientScreen = iota
	screenTest
	screenHistory
	screenDetail
)

type clientModal struct {
	Kind   int
	Cursor int
	Tests  [3]bool
	Inputs [2]textinput.Model
	Field  int
	Err    error
}

const modalClear = 100

type clientEventMsg struct {
	RunID int
	Event testEvent
}

type clientModel struct {
	lifetime          context.Context
	state             clientState
	path              string
	readOnly          bool
	screen            clientScreen
	languageCursor    int
	languageReturn    bool
	focus             int
	width, height     int
	modal             *clientModal
	history           table.Model
	detail            viewport.Model
	notice            string
	err               error
	status            string
	running, stopping bool
	runID             int
	cancel            context.CancelFunc
	events            <-chan testEvent
	record            *clientRecord
	phase             testPhase
	live              testEvent
}

func newClientModel(ctx context.Context) clientModel {
	return clientModel{lifetime: ctx, state: defaultClientState(), width: 88, height: 32, status: "ready"}
}

func runClientTUI() error {
	if !term.IsTerminal(os.Stdin.Fd()) || !term.IsTerminal(os.Stdout.Fd()) {
		return errors.New("client / c requires an interactive terminal (SSH supported)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newClientModel(ctx)
	path, err := clientStatePath()
	if err == nil {
		m.path = path
		m.state, err = loadClientState(path)
	}
	if err != nil {
		m.readOnly = true // Preserve an unreadable file for recovery.
		m.notice = m.t("loadError") + ": " + err.Error()
	}
	for i, language := range clientLocales.Languages {
		if language.Code == m.state.Settings.Language {
			m.languageCursor = i
		}
	}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m clientModel) t(key string) string { return clientText(m.state.Settings.Language, key) }
func (m clientModel) Init() tea.Cmd       { return nil }

func waitClientEvent(ctx context.Context, id int, events <-chan testEvent) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-events:
			return clientEventMsg{id, event}
		case <-ctx.Done():
			return nil
		}
	}
}

func (m *clientModel) persist() {
	if m.readOnly {
		return
	}
	if err := saveClientState(m.path, m.state); err != nil {
		m.notice = m.t("saveError") + ": " + err.Error()
	}
}

func (m clientModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.screen == screenHistory {
			cursor := m.history.Cursor()
			m.buildHistory()
			m.history.SetCursor(cursor)
		}
		if m.screen == screenDetail {
			m.openDetail()
		}
		if m.modal != nil && m.modal.Kind == menuServer {
			for i := range m.modal.Inputs {
				m.modal.Inputs[i].Width = max(8, min(48, m.width-14))
			}
		}
		return m, nil
	case tea.MouseMsg:
		return m.updateMouse(msg)
	case clientEventMsg:
		if msg.RunID != m.runID || !m.running {
			return m, nil
		}
		e := msg.Event
		if e.Final {
			m.running = false
			m.cancel()
			switch {
			case m.stopping || errors.Is(e.Err, context.Canceled):
				m.status = "statusStopped"
			case e.Err != nil:
				m.err, m.status = e.Err, "failed"
			default:
				m.status = "statusDone"
			}
			m.record.Status = m.status
			if e.Err != nil && m.status == "failed" {
				m.record.Error = localizedError(m.state.Settings.Language, e.Err)
			}
			if m.record.Latency != nil || m.record.Download != nil || m.record.Upload != nil {
				m.state.History = append([]clientRecord{*m.record}, m.state.History...)
				m.state.History = m.state.History[:min(clientHistoryLimit, len(m.state.History))]
				m.persist()
			}
			m.stopping = false
			return m, nil
		}
		m.phase, m.live = e.Phase, e
		if !m.stopping {
			m.status = "statusRunning"
		}
		if e.Latency != nil {
			m.record.Latency = e.Latency
		}
		if e.Phase != phaseLatency {
			i := int(e.Phase) - 1
			if e.Speed != nil {
				if i == 0 {
					m.record.Download = e.Speed
				} else {
					m.record.Upload = e.Speed
				}
			} else if e.Elapsed > 0 {
				m.record.Curves[i] = append(m.record.Curves[i], rateSample{e.Elapsed, e.Mbps})
				if len(m.record.Curves[i]) > 600 {
					m.record.Curves[i] = m.record.Curves[i][len(m.record.Curves[i])-600:]
				}
			}
		}
		return m, waitClientEvent(m.lifetime, m.runID, m.events)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		}
		if m.running {
			if msg.String() == "esc" || msg.String() == "enter" || msg.String() == "s" {
				m.stopping, m.status = true, "stopping"
				m.cancel()
			}
			return m, nil
		}
		if m.modal != nil {
			return m.updateModal(msg)
		}
		switch m.screen {
		case screenLanguage:
			return m.updateLanguage(msg)
		case screenHistory:
			switch msg.String() {
			case "esc", "q", "h":
				m.screen = screenTest
				return m, nil
			case "enter":
				if len(m.state.History) > 0 {
					m.openDetail()
				}
				return m, nil
			case "c":
				if len(m.state.History) > 0 {
					m.modal = &clientModal{Kind: modalClear}
				}
				return m, nil
			}
			var cmd tea.Cmd
			m.history, cmd = m.history.Update(msg)
			return m, cmd
		case screenDetail:
			if msg.String() == "esc" || msg.String() == "q" {
				m.screen = screenHistory
				return m, nil
			}
			var cmd tea.Cmd
			m.detail, cmd = m.detail.Update(msg)
			return m, cmd
		default:
			switch msg.String() {
			case "q", "esc":
				return m, tea.Quit
			case "up", "down", "left", "right", "k", "j", "tab", "shift+tab":
				m.moveFormFocus(msg.String())
			case "h":
				m.buildHistory()
				m.screen = screenHistory
			case "l":
				m.focus = menuLanguage
				return m.openSetting()
			case "ctrl+r":
				return m.startTest()
			case "enter", " ", "alt+down":
				return m.openSetting()
			}
			return m, nil
		}
	}
	if m.modal != nil && m.modal.Kind == menuServer {
		var cmd tea.Cmd
		i := m.modal.Field
		m.modal.Inputs[i], cmd = m.modal.Inputs[i].Update(message)
		return m, cmd
	}
	return m, nil
}

func (m clientModel) updateLanguage(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(clientLocales.Languages)
	switch key.String() {
	case "up", "k":
		m.languageCursor = (m.languageCursor + n - 1) % n
	case "down", "j", "tab":
		m.languageCursor = (m.languageCursor + 1) % n
	case "enter":
		m.state.Settings.Language = clientLocales.Languages[m.languageCursor].Code
		m.screen = screenTest
		m.persist()
	case "esc", "q":
		if m.languageReturn {
			m.screen = screenTest
		} else {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m clientModel) openSetting() (tea.Model, tea.Cmd) {
	s := m.state.Settings
	switch m.focus {
	case menuStart:
		return m.startTest()
	case menuHistory:
		m.buildHistory()
		m.screen = screenHistory
		return m, nil
	}
	modal := &clientModal{Kind: m.focus, Tests: s.Tests}
	switch m.focus {
	case menuServer:
		for i := range modal.Inputs {
			input := textinput.New()
			input.Prompt, input.CharLimit, input.Width = "", 256, max(8, min(48, m.width-14))
			modal.Inputs[i] = input
		}
		modal.Inputs[0].Placeholder = "192.168.5.8"
		modal.Inputs[0].SetValue(s.Address)
		modal.Inputs[1].SetValue(s.Port)
		modal.Inputs[1].CharLimit = 5
		m.modal = modal
		return m, modal.Inputs[0].Focus()
	case menuConnections:
		for i, value := range connectionChoices {
			if value == s.Connections {
				modal.Cursor = i
			}
		}
	case menuDuration:
		for i, value := range []int{10, 15, 30} {
			if value == s.Seconds {
				modal.Cursor = i
			}
		}
	case menuUnit:
		modal.Cursor = s.Unit
	case menuLanguage:
		for i, language := range clientLocales.Languages {
			if language.Code == s.Language {
				modal.Cursor = i
			}
		}
	}
	m.modal = modal
	return m, nil
}

func (m clientModel) modalOptions() []string {
	switch m.modal.Kind {
	case menuTests:
		return []string{m.t("latency"), m.t("download"), m.t("upload")}
	case menuConnections:
		return []string{"1", "2", "4", "8"}
	case menuDuration:
		return []string{"10 " + m.t("seconds"), "15 " + m.t("seconds"), "30 " + m.t("seconds")}
	case menuUnit:
		return []string{"Mbps", "MB/s", "KB/s"}
	case menuLanguage:
		var options []string
		for _, language := range clientLocales.Languages {
			options = append(options, language.Native)
		}
		return options
	case modalClear:
		return []string{m.t("cancel"), m.t("confirm")}
	}
	return nil
}

func (m clientModel) updateModal(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	modal := m.modal
	if key.String() == "esc" {
		m.modal = nil
		return m, nil
	}
	if modal.Kind == menuServer {
		switch key.String() {
		case "tab", "shift+tab", "up", "down":
			modal.Inputs[modal.Field].Blur()
			modal.Field = 1 - modal.Field
			return m, modal.Inputs[modal.Field].Focus()
		case "enter":
			address, port := strings.TrimSpace(modal.Inputs[0].Value()), strings.TrimSpace(modal.Inputs[1].Value())
			_, err := parseTarget(address, port)
			if err != nil {
				modal.Err = err
				return m, nil
			}
			m.state.Settings.Address, m.state.Settings.Port = address, port
			m.err = nil
			m.modal = nil
			m.persist()
			return m, nil
		}
		var cmd tea.Cmd
		modal.Inputs[modal.Field], cmd = modal.Inputs[modal.Field].Update(key)
		return m, cmd
	}
	options := m.modalOptions()
	switch key.String() {
	case "up", "k", "shift+tab":
		modal.Cursor = (modal.Cursor + len(options) - 1) % len(options)
	case "down", "j", "tab":
		modal.Cursor = (modal.Cursor + 1) % len(options)
	case " ":
		if modal.Kind == menuTests {
			modal.Tests[modal.Cursor] = !modal.Tests[modal.Cursor]
		}
	case "enter":
		switch modal.Kind {
		case menuTests:
			if !modal.Tests[0] && !modal.Tests[1] && !modal.Tests[2] {
				modal.Err = fault("pickOne")
				return m, nil
			}
			m.state.Settings.Tests = modal.Tests
		case menuConnections:
			m.state.Settings.Connections = connectionChoices[modal.Cursor]
		case menuDuration:
			m.state.Settings.Seconds = []int{10, 15, 30}[modal.Cursor]
		case menuUnit:
			m.state.Settings.Unit = modal.Cursor
		case menuLanguage:
			m.state.Settings.Language = clientLocales.Languages[modal.Cursor].Code
			m.languageCursor = modal.Cursor
		case modalClear:
			if modal.Cursor == 1 {
				m.state.History = nil
				m.buildHistory()
			}
		}
		m.modal = nil
		m.persist()
	}
	return m, nil
}

func (m clientModel) startTest() (tea.Model, tea.Cmd) {
	s := m.state.Settings
	base, err := parseTarget(s.Address, s.Port)
	if err != nil {
		m.err = err
		m.focus = menuServer
		return m.openSetting()
	}
	if !s.Tests[0] && !s.Tests[1] && !s.Tests[2] {
		m.err = fault("pickOne")
		return m, nil
	}
	m.err, m.live = nil, testEvent{}
	m.running, m.stopping, m.screen = true, false, screenTest
	m.status, m.focus, m.phase = "connecting", menuStart, phaseLatency
	m.record = &clientRecord{Time: time.Now(), Target: base.String(), Settings: s}
	m.runID++
	runCtx, cancel := context.WithCancel(m.lifetime)
	m.cancel = cancel
	events := make(chan testEvent, 64)
	m.events = events
	client := newSpeedClient(clientConfig{base, s.Connections, time.Duration(s.Seconds) * time.Second, s.Tests})
	lifetime := m.lifetime
	start := func() tea.Msg {
		client.run(runCtx, func(e testEvent) {
			select {
			case events <- e:
			case <-lifetime.Done():
			}
		})
		return nil
	}
	return m, tea.Batch(start, waitClientEvent(m.lifetime, m.runID, events))
}

func (m *clientModel) buildHistory() {
	width := max(34, m.width-8)
	columns := []table.Column{
		{Title: m.t("colTime"), Width: 14}, {Title: m.t("server"), Width: max(10, width-60)},
		{Title: m.t("latency"), Width: 8}, {Title: m.t("download"), Width: 10}, {Title: m.t("upload"), Width: 10},
	}
	if width < 68 {
		columns = []table.Column{{Title: m.t("colTime"), Width: 14}, {Title: m.t("server"), Width: max(10, width-18)}}
	}
	var rows []table.Row
	for _, r := range m.state.History {
		row := table.Row{r.Time.Local().Format("01-02 15:04:05"), r.Target}
		if len(columns) > 2 {
			latency := "—"
			if r.Latency != nil {
				latency = fmt.Sprintf("%.2f", r.Latency.Avg)
			}
			download, upload := "—", "—"
			if r.Download != nil && r.Download.Measured {
				download = fmt.Sprintf("%.1f", r.Download.AvgMbps)
			}
			if r.Upload != nil && r.Upload.Measured {
				upload = fmt.Sprintf("%.1f", r.Upload.AvgMbps)
			}
			row = append(row, latency, download, upload)
		}
		rows = append(rows, row)
	}
	m.history = table.New(table.WithColumns(columns), table.WithRows(rows), table.WithFocused(true), table.WithHeight(max(4, m.height-9)))
	styles := table.DefaultStyles()
	styles.Selected = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("57")).Bold(true)
	styles.Header = clientAccent.Padding(0, 1)
	m.history.SetStyles(styles)
}

func (m *clientModel) openDetail() {
	i := m.history.Cursor()
	if i < 0 || i >= len(m.state.History) {
		return
	}
	r := m.state.History[i]
	m.detail = viewport.New(max(30, m.width-6), max(6, m.height-6))
	m.detail.SetContent(m.recordDetail(r))
	m.screen = screenDetail
}
