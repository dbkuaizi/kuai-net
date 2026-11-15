package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func press(m clientModel, key tea.KeyMsg) (clientModel, tea.Cmd) {
	next, cmd := m.Update(key)
	return next.(clientModel), cmd
}
func key(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func enter() tea.KeyMsg       { return tea.KeyMsg{Type: tea.KeyEnter} }

func TestLanguageFirstAndTransactionalDialogs(t *testing.T) {
	m := newClientModel(context.Background())
	if m.screen != screenLanguage || !strings.Contains(m.View(), "English") || !strings.Contains(m.View(), "简体中文") {
		t.Fatal("must start at language list")
	}
	m.languageCursor = 2
	m, _ = press(m, enter())
	if m.screen != screenTest || m.state.Settings.Language != "en" {
		t.Fatal("language selection was not applied")
	}
	m.focus = menuConnections
	m, _ = press(m, enter())
	for _, option := range []string{"1", "2", "4", "8"} {
		if !strings.Contains(ansi.Strip(m.modalView()), option) {
			t.Fatal("dialog omitted option", option)
		}
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.state.Settings.Connections != 4 || m.modal != nil {
		t.Fatal("cancel must preserve previous configuration")
	}
	m, _ = press(m, enter())
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, enter())
	if m.state.Settings.Connections != 8 || m.focus != menuConnections {
		t.Fatal("confirm must commit and restore focus")
	}
	m.focus = menuTests
	m, _ = press(m, enter())
	for i := 0; i < 3; i++ {
		m, _ = press(m, key(" "))
		m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	m, _ = press(m, enter())
	if m.modal == nil || m.modal.Err == nil || !m.state.Settings.Tests[0] {
		t.Fatal("empty plan must not commit")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	m.focus = menuServer
	m, _ = press(m, enter())
	m, _ = press(m, key("192.168.5.8"))
	m, _ = press(m, enter())
	if m.modal != nil || m.state.Settings.Address != "192.168.5.8" {
		t.Fatal("server input dialog failed")
	}
}

func TestClientStateRoundTripAndHistoryLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kuainet", "client.json")
	state := defaultClientState()
	state.Settings.Address = "192.168.5.8"
	state.Settings.Language = "ja"
	for i := 0; i < 65; i++ {
		state.History = append(state.History, clientRecord{Time: time.Now(), Target: "http://x:8080", Status: "statusDone"})
	}
	if err := saveClientState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadClientState(path)
	if err != nil || loaded.Settings.Address != state.Settings.Address || loaded.Settings.Language != "ja" || len(loaded.History) != 60 {
		t.Fatalf("bad state round trip: %+v %v", loaded, err)
	}
	m := newClientModel(context.Background())
	m.state = loaded
	m.path = path
	m.screen = screenHistory
	m.buildHistory()
	m, _ = press(m, key("c"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if len(m.state.History) != 60 {
		t.Fatal("cancelled clear erased history")
	}
	m, _ = press(m, key("c"))
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = press(m, enter())
	loaded, err = loadClientState(path)
	if err != nil || len(loaded.History) != 0 {
		t.Fatal("confirmed clear did not persist")
	}
}

func TestTUIEventLoopStartStopRepeatAndPersist(t *testing.T) {
	started := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/ping" {
			_, _ = io.WriteString(w, "ok")
			return
		}
		started <- struct{}{}
		w.Header().Set("Content-Type", "application/octet-stream")
		for {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
				if _, err := w.Write(make([]byte, 8192)); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newClientModel(ctx)
	m.state.Settings.Address = srv.URL
	m.state.Settings.Connections = 1
	m.state.Settings.Tests = [3]bool{true, true, false}
	m.path = filepath.Join(t.TempDir(), "client.json")
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	var output bytes.Buffer
	p := tea.NewProgram(m, tea.WithInput(input), tea.WithOutput(&output), tea.WithoutSignalHandler())
	type result struct {
		model tea.Model
		err   error
	}
	done := make(chan result, 1)
	go func() { final, err := p.Run(); done <- result{final, err} }()
	defer p.Kill()
	p.Send(tea.WindowSizeMsg{Width: 100, Height: 36})
	p.Send(enter())
	p.Send(tea.KeyMsg{Type: tea.KeyCtrlR})
	for run := 0; run < 2; run++ {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("download did not start")
		}
		time.Sleep(250 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyEsc})
		if run == 0 {
			time.Sleep(200 * time.Millisecond)
			p.Send(tea.KeyMsg{Type: tea.KeyCtrlR})
		}
	}
	time.Sleep(200 * time.Millisecond)
	p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case final := <-done:
		if final.err != nil {
			t.Fatal(final.err)
		}
		m = final.model.(clientModel)
		if m.running || m.runID != 2 || len(m.state.History) != 2 || m.record.Download == nil || !m.record.Download.Stopped {
			t.Fatalf("incomplete repeated stop: %+v", m.record)
		}
		loaded, err := loadClientState(m.path)
		if err != nil || len(loaded.History) != 2 {
			t.Fatal("history was not persisted", err)
		}
		if !strings.Contains(ansi.Strip(output.String()), "KuaiNet") {
			t.Fatal("TUI did not render")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TUI did not quit")
	}
}

func TestTUISelectedStagesComplete(t *testing.T) {
	srv := newTestServer()
	defer srv.Close()
	m := newClientModel(context.Background())
	m.screen = screenTest
	m.state.Settings.Address = srv.URL
	m.state.Settings.Tests = [3]bool{true, false, false}
	next, batch := m.startTest()
	m = next.(clientModel)
	commands := batch().(tea.BatchMsg)
	go commands[0]()
	cmd := commands[1]
	for m.running {
		next, cmd = m.Update(cmd())
		m = next.(clientModel)
	}
	if m.err != nil || m.record.Latency == nil || m.record.Latency.Samples != 10 || len(m.state.History) != 1 {
		t.Fatal("latency-only plan incomplete")
	}
	m.buildHistory()
	m.screen = screenHistory
	m, _ = press(m, enter())
	if m.screen != screenDetail || !strings.Contains(ansi.Strip(m.detail.View()), srv.URL) {
		t.Fatal("history detail did not open")
	}
}

func TestTUILayoutFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 24}, {60, 30}, {80, 24}, {100, 36}} {
		m := newClientModel(context.Background())
		m.width, m.height = size[0], size[1]
		for _, screen := range []clientScreen{screenLanguage, screenTest, screenHistory} {
			m.screen = screen
			if screen == screenHistory {
				m.buildHistory()
			}
			for _, modal := range []bool{false, true} {
				m.modal = nil
				if modal && screen == screenTest {
					m.modal = &clientModal{Kind: menuConnections, Cursor: 2}
				}
				view := m.View()
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Errorf("%v screen %d overflow: %q", size, screen, ansi.Strip(line))
					}
				}
				if lines := strings.Count(view, "\n") + 1; lines > m.height {
					t.Errorf("%v screen %d height=%d", size, screen, lines)
				}
			}
		}
	}
}

func TestTUIRunningAndDialogsAcrossLanguages(t *testing.T) {
	for _, language := range clientLocales.Languages {
		for _, size := range [][2]int{{40, 24}, {60, 30}, {80, 24}, {110, 36}} {
			m := newClientModel(context.Background())
			m.state.Settings.Language = language.Code
			m.screen, m.width, m.height = screenTest, size[0], size[1]
			m.running, m.phase = true, phaseDownload
			m.record = &clientRecord{Target: "http://192.168.5.8:8080", Settings: m.state.Settings,
				Download: &speedResult{Measured: true, AvgMbps: 940, PeakMbps: 950, Bytes: 1000000000}}
			assertFits := func() {
				t.Helper()
				view := m.View()
				for _, line := range strings.Split(view, "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatalf("%s %v width overflow", language.Code, size)
					}
				}
				if rows := strings.Count(view, "\n") + 1; rows > m.height {
					t.Errorf("%s %v height overflow: %d", language.Code, size, rows)
				}
			}
			assertFits()
			m.running = false
			for _, focus := range []int{menuServer, menuTests, menuConnections, menuDuration, menuUnit} {
				m.focus = focus
				next, _ := m.openSetting()
				m = next.(clientModel)
				assertFits()
			}
		}
	}
}

func TestClientLocaleCoverage(t *testing.T) {
	for _, language := range clientLocales.Languages {
		for key := range clientLocales.Texts["en"] {
			if clientLocales.Texts[language.Code][key] == "" {
				t.Errorf("%s missing %s", language.Code, key)
			}
		}
	}
}

func TestClientCommandAliases(t *testing.T) {
	for _, alias := range []string{"client", "c"} {
		if err := runCommand([]string{alias, "unexpected"}); err == nil || !strings.Contains(err.Error(), "TUI") {
			t.Errorf("alias %s: %v", alias, err)
		}
	}
}
