package main

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func clickClient(m clientModel, x, y int) (clientModel, tea.Cmd) {
	next, cmd := m.Update(tea.MouseMsg{X: x + 2, Y: y + 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return next.(clientModel), cmd
}

func TestFormIsCompactAndAligned(t *testing.T) {
	m := newClientModel(context.Background())
	m.screen, m.width = screenTest, 220
	m.state.Settings.Address = "192.168.5.8"
	layout := m.formLayout()
	if layout.Width > 92 || m.testWidth() > 112 {
		t.Fatal("wide terminals must not stretch the form or graph")
	}
	view := ansi.Strip(m.formView())
	if !strings.Contains(view, "192.168.5.8:8080") || !strings.Contains(view, "▾") || !strings.Contains(view, "[ ▶") {
		t.Fatalf("form needs visible fields, dropdown arrows and a start button:\n%s", view)
	}
	lines := strings.Split(m.View(), "\n")
	for kind, field := range layout.Fields {
		line := ansi.Cut(lines[field.Y+1], field.X+2, field.X+2+field.Width)
		if !strings.HasPrefix(ansi.Strip(line), "[") || !strings.HasSuffix(ansi.Strip(line), "]") {
			t.Errorf("field %d is not aligned with its click target: %q", kind, line)
		}
	}
}

func TestDropdownAnchoredAndMouseSelects(t *testing.T) {
	m := newClientModel(context.Background())
	m.screen, m.width, m.height = screenTest, 110, 36
	field := m.formLayout().Fields[menuConnections]
	m, _ = clickClient(m, field.X+2, field.Y)
	if !m.isDropdown() || m.modal.Kind != menuConnections {
		t.Fatal("click on a select field did not open its dropdown")
	}
	popup := m.popupRect(m.modalView())
	if popup.X != field.X || popup.Y != field.Y+1 {
		t.Fatalf("dropdown is not anchored below the field: field=%+v popup=%+v", field, popup)
	}
	m, _ = clickClient(m, popup.X+3, popup.Y+1+3)
	if m.modal != nil || m.state.Settings.Connections != 8 {
		t.Fatal("single-select click did not commit selected value")
	}

	field = m.formLayout().Fields[menuTests]
	m, _ = clickClient(m, field.X+2, field.Y)
	popup = m.popupRect(m.modalView())
	m, _ = clickClient(m, popup.X+3, popup.Y+2)
	if m.modal == nil || m.modal.Tests[1] || !m.state.Settings.Tests[1] {
		t.Fatal("multi-select should edit only the pending value until confirmed")
	}
	m, _ = clickClient(m, popup.X+3, popup.Y+1+3)
	if m.modal != nil || m.state.Settings.Tests[1] {
		t.Fatal("multi-select confirm button did not commit")
	}
}

func TestDropdownWheelKeyboardAndOutsideCancel(t *testing.T) {
	m := newClientModel(context.Background())
	m.screen, m.width, m.height = screenTest, 100, 36
	m.focus = menuUnit
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	if m.modal == nil {
		t.Fatal("Alt+Down did not open the dropdown")
	}
	popup := m.popupRect(m.modalView())
	next, _ := m.Update(tea.MouseMsg{X: popup.X + 4, Y: popup.Y + 3, Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress})
	m = next.(clientModel)
	if m.modal.Cursor != 1 || m.state.Settings.Unit != 0 {
		t.Fatal("wheel should move highlight without committing")
	}
	m, _ = clickClient(m, 0, 0)
	if m.modal != nil || m.state.Settings.Unit != 0 {
		t.Fatal("outside click must cancel pending selection")
	}

	m.focus = menuConnections
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != menuDuration {
		t.Fatal("Right should focus the adjacent field on the same row")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.focus != menuLanguage {
		t.Fatal("Down should preserve the column")
	}
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != menuStart {
		t.Fatal("Tab order should proceed to the primary action")
	}
}

func TestLanguageDropdownCancelsAndCommits(t *testing.T) {
	m := newClientModel(context.Background())
	m.screen, m.focus = screenTest, menuLanguage
	m, _ = press(m, enter())
	if m.screen != screenTest || m.modal == nil || len(m.modalOptions()) != 10 {
		t.Fatal("language setting should open an in-place dropdown")
	}
	m.modal.Cursor = 2
	m, _ = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.state.Settings.Language != "zh" {
		t.Fatal("cancel switched language")
	}
	m, _ = press(m, enter())
	m.modal.Cursor = 2
	m, _ = press(m, enter())
	if m.state.Settings.Language != "en" || m.modal != nil {
		t.Fatal("language dropdown did not commit")
	}
}

func TestDropdownStaysInsideTerminalAfterResize(t *testing.T) {
	for _, language := range clientLocales.Languages {
		for _, size := range [][2]int{{40, 20}, {40, 24}, {60, 30}, {80, 24}, {110, 36}, {220, 40}} {
			m := newClientModel(context.Background())
			m.screen, m.width, m.height = screenTest, size[0], size[1]
			m.state.Settings.Language = language.Code
			for _, kind := range []int{menuTests, menuConnections, menuDuration, menuUnit, menuLanguage} {
				m.focus = kind
				next, _ := m.openSetting()
				m = next.(clientModel)
				r := m.popupRect(m.modalView())
				if r.X < 0 || r.Y < 0 || r.X+r.Width > m.width-4 || r.Y+r.Height > m.height-2 {
					t.Fatalf("%s %v kind %d dropdown out of bounds: %+v", language.Code, size, kind, r)
				}
				for _, line := range strings.Split(m.View(), "\n") {
					if ansi.StringWidth(line) > m.width {
						t.Fatal("dropdown overflowed screen width")
					}
				}
				if strings.Count(m.View(), "\n")+1 > m.height {
					t.Fatalf("%s %v dropdown overflowed height", language.Code, size)
				}
			}
		}
	}
}
