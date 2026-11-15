package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type clientRect struct{ X, Y, Width, Height int }

func (r clientRect) contains(x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

type clientFormField struct {
	clientRect
	LabelX, LabelWidth int
}

type clientFormLayout struct {
	Width, Height, Top int
	Fields             [menuCount]clientFormField
}

func (m clientModel) testWidth() int { return min(112, m.width-4) }

// Rendering and hit testing use the same cell coordinates (before the outer
// two-column/one-row margin), including when the terminal is resized.
func (m clientModel) formLayout() clientFormLayout {
	layout := clientFormLayout{Width: min(92, m.testWidth()), Top: 1}
	if m.testWidth() >= 70 {
		layout.Top = 2
	}
	inner := layout.Width - 4
	labelWidth := 0
	for _, label := range m.menuLabels()[:menuLanguage+1] {
		labelWidth = max(labelWidth, ansi.StringWidth(label))
	}
	labelWidth = min(labelWidth, 16)
	place := func(kind, row, x, width int, button bool) {
		field := clientFormField{clientRect: clientRect{X: x, Y: layout.Top + 1 + row, Width: width, Height: 1}}
		if !button {
			field.LabelX, field.LabelWidth = x, min(labelWidth, max(6, width/2-3))
			field.X += field.LabelWidth + 1
			field.Width -= field.LabelWidth + 1
		}
		layout.Fields[kind] = field
	}
	place(menuServer, 0, 2, inner, false)
	place(menuTests, 1, 2, inner, false)
	if layout.Width >= 64 {
		column := (inner - 2) / 2
		place(menuConnections, 2, 2, column, false)
		place(menuDuration, 2, 2+column+2, inner-column-2, false)
		place(menuUnit, 3, 2, column, false)
		place(menuLanguage, 3, 2+column+2, inner-column-2, false)
		place(menuStart, 4, 2, column, true)
		place(menuHistory, 4, 2+column+2, inner-column-2, true)
		layout.Height = 7
	} else {
		for i, kind := range []int{menuConnections, menuDuration, menuUnit, menuLanguage} {
			place(kind, i+2, 2, inner, false)
		}
		column := (inner - 2) / 2
		place(menuStart, 6, 2, column, true)
		place(menuHistory, 6, 2+column+2, inner-column-2, true)
		layout.Height = 9
	}
	return layout
}

func padClientCells(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func replaceClientCells(line, text string, x, width int) string {
	return ansi.Cut(line, 0, x) + padClientCells(text, width) + ansi.Cut(line, x+width, ansi.StringWidth(line))
}

func (m clientModel) formView() string {
	layout := m.formLayout()
	rows := make([]string, layout.Height)
	border := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	rows[0] = border.Render("╭" + strings.Repeat("─", layout.Width-2) + "╮")
	for row := 1; row < layout.Height-1; row++ {
		rows[row] = border.Render("│") + strings.Repeat(" ", layout.Width-2) + border.Render("│")
	}
	rows[layout.Height-1] = border.Render("╰" + strings.Repeat("─", layout.Width-2) + "╯")
	labels, values := m.menuLabels(), m.menuValues()
	for kind, field := range layout.Fields {
		row := field.Y - layout.Top
		focused := kind == m.focus
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("235"))
		if focused {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("24")).Bold(true)
		}
		if m.running && kind != menuStart {
			style = clientMuted
		}
		var text string
		if kind == menuStart || kind == menuHistory {
			label := labels[kind]
			if kind == menuStart {
				if m.running {
					label = "■ " + m.t("stopTest")
				} else {
					label = "▶ " + label
				}
			} else {
				label += " · " + values[kind]
			}
			text = "[ " + padClientCells(label, field.Width-4) + " ]"
		} else {
			label := clientMuted.Render(padClientCells(labels[kind], field.LabelWidth))
			rows[row] = replaceClientCells(rows[row], label, field.LabelX, field.LabelWidth)
			arrow := "▾"
			if kind == menuServer {
				arrow = "…"
			} else if m.modal != nil && m.modal.Kind == kind {
				arrow = "▴"
			}
			text = "[ " + padClientCells(values[kind], field.Width-6) + " " + arrow + " ]"
		}
		rows[row] = replaceClientCells(rows[row], style.Render(text), field.X, field.Width)
	}
	return strings.Join(rows, "\n")
}

func (m clientModel) isDropdown() bool {
	return m.modal != nil && m.modal.Kind >= menuTests && m.modal.Kind <= menuLanguage
}

func (m clientModel) selectedOption() int {
	s := m.state.Settings
	switch m.modal.Kind {
	case menuConnections:
		for i, value := range connectionChoices {
			if value == s.Connections {
				return i
			}
		}
	case menuDuration:
		for i, value := range []int{10, 15, 30} {
			if value == s.Seconds {
				return i
			}
		}
	case menuUnit:
		return s.Unit
	case menuLanguage:
		for i, value := range clientLocales.Languages {
			if value.Code == s.Language {
				return i
			}
		}
	}
	return -1
}

func (m clientModel) dropdownView() string {
	field := m.formLayout().Fields[m.modal.Kind]
	help := m.t("helpSelect")
	if m.modal.Kind == menuTests {
		help = m.t("helpMulti")
	}
	width := max(field.Width, min(56, ansi.StringWidth(help)+4))
	for _, option := range m.modalOptions() {
		width = max(width, ansi.StringWidth(option)+8)
	}
	width = min(width, m.testWidth())
	content := width - 4
	border := lipgloss.NewStyle().Foreground(lipgloss.Color("81"))
	plain := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("235"))
	selected := lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("57")).Bold(true)
	rows := []string{border.Render("╭" + strings.Repeat("─", width-2) + "╮")}
	add := func(text string, style lipgloss.Style) {
		rows = append(rows, border.Render("│")+style.Render(" "+padClientCells(text, content)+" ")+border.Render("│"))
	}
	for i, option := range m.modalOptions() {
		mark := "○ "
		if m.modal.Kind == menuTests {
			mark = "[ ] "
			if m.modal.Tests[i] {
				mark = "[✓] "
			}
		} else if i == m.selectedOption() {
			mark = "● "
		}
		prefix, style := "  ", plain
		if i == m.modal.Cursor {
			prefix, style = "› ", selected
		}
		add(prefix+mark+option, style)
	}
	if m.modal.Kind == menuTests {
		add("[ "+m.t("confirm")+" · Enter ]", selected)
	}
	if m.modal.Err != nil {
		add(localizedError(m.state.Settings.Language, m.modal.Err), clientError.Background(lipgloss.Color("235")))
	}
	for _, line := range strings.Split(ansi.Hardwrap(help, content, true), "\n") {
		add(line, plain)
	}
	rows = append(rows, border.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	return strings.Join(rows, "\n")
}

func (m clientModel) popupRect(dialog string) clientRect {
	r := clientRect{Width: lipgloss.Width(dialog), Height: lipgloss.Height(dialog)}
	width, height := m.width-4, m.height-2
	if m.isDropdown() {
		width = m.testWidth()
		field := m.formLayout().Fields[m.modal.Kind]
		r.X = min(field.X, max(0, width-r.Width))
		r.Y = field.Y + field.Height
		if r.Y+r.Height > height {
			if field.Y >= r.Height {
				r.Y = field.Y - r.Height
			} else {
				r.Y = max(0, height-r.Height)
			}
		}
	} else {
		r.X, r.Y = max(0, (width-r.Width)/2), max(0, (height-r.Height)/2)
	}
	return r
}

func (m *clientModel) moveFormFocus(key string) {
	order := []int{menuServer, menuTests, menuConnections, menuDuration, menuUnit, menuLanguage, menuStart, menuHistory}
	if key == "tab" || key == "shift+tab" {
		for i, kind := range order {
			if kind == m.focus {
				step := 1
				if key == "shift+tab" {
					step = -1
				}
				m.focus = order[(i+step+len(order))%len(order)]
				return
			}
		}
	}
	layout := m.formLayout()
	current := layout.Fields[m.focus]
	best, score := -1, int(^uint(0)>>1)
	for kind, candidate := range layout.Fields {
		dx, dy := candidate.X-current.X, candidate.Y-current.Y
		if (key == "up" || key == "k") && dy >= 0 || (key == "down" || key == "j") && dy <= 0 {
			continue
		}
		if key == "left" && (dy != 0 || dx >= 0) || key == "right" && (dy != 0 || dx <= 0) {
			continue
		}
		s := absClient(dy)*1000 + absClient(dx)
		if s < score {
			best, score = kind, s
		}
	}
	if best >= 0 {
		m.focus = best
	}
}

func absClient(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (m clientModel) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x, y := msg.X-2, msg.Y-1
	wheel := msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown
	click := msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress
	if !wheel && !click {
		return m, nil
	}
	if m.width < 40 || m.height < 20 {
		return m, nil
	}
	if m.modal != nil {
		popup := m.popupRect(m.modalView())
		if !popup.contains(x, y) {
			if click {
				m.modal = nil
			}
			return m, nil
		}
		if m.isDropdown() {
			if wheel {
				key := tea.KeyDown
				if msg.Button == tea.MouseButtonWheelUp {
					key = tea.KeyUp
				}
				return m.updateModal(tea.KeyMsg{Type: key})
			}
			row := y - popup.Y - 1
			if row >= 0 && row < len(m.modalOptions()) {
				m.modal.Cursor = row
				if m.modal.Kind == menuTests {
					return m.updateModal(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
				}
				return m.updateModal(tea.KeyMsg{Type: tea.KeyEnter})
			}
			if m.modal.Kind == menuTests && row == len(m.modalOptions()) {
				return m.updateModal(tea.KeyMsg{Type: tea.KeyEnter})
			}
		} else if click && m.modal.Kind == menuServer {
			field := (y - popup.Y - 4) / 2
			if y-popup.Y >= 4 && y-popup.Y <= 7 {
				m.modal.Inputs[m.modal.Field].Blur()
				m.modal.Field = field
				return m, m.modal.Inputs[field].Focus()
			}
		}
		return m, nil
	}
	if m.screen == screenLanguage {
		if wheel {
			key := tea.KeyDown
			if msg.Button == tea.MouseButtonWheelUp {
				key = tea.KeyUp
			}
			return m.updateLanguage(tea.KeyMsg{Type: key})
		}
		if row := y - 4; click && row >= 0 && row < len(clientLocales.Languages) {
			m.languageCursor = row
			return m.updateLanguage(tea.KeyMsg{Type: tea.KeyEnter})
		}
		return m, nil
	}
	if m.screen == screenHistory && wheel {
		if msg.Button == tea.MouseButtonWheelUp {
			m.history.MoveUp(1)
		} else {
			m.history.MoveDown(1)
		}
		return m, nil
	}
	if m.screen == screenDetail {
		var cmd tea.Cmd
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	if m.screen != screenTest || !click {
		return m, nil
	}
	for kind, field := range m.formLayout().Fields {
		if field.contains(x, y) {
			if m.running {
				if kind == menuStart {
					m.stopping, m.status = true, "stopping"
					m.cancel()
				}
				return m, nil
			}
			m.focus = kind
			return m.openSetting()
		}
	}
	return m, nil
}
