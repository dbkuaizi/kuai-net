package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/NimbleMarkets/ntcharts/canvas"
	"github.com/NimbleMarkets/ntcharts/canvas/graph"
	"github.com/NimbleMarkets/ntcharts/linechart"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	clientAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	clientMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	clientError  = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	clientGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	clientBlue   = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	clientYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
)

func clientTitle(title string) string {
	return clientAccent.Render(title) + "  " + clientMuted.Render(version)
}

func (m clientModel) View() string {
	if m.width < 40 || m.height < 20 {
		return ansi.Hardwrap(clientTitle("KuaiNet")+"\n"+m.t("minimumSize")+"\nCtrl+C", max(1, m.width), true)
	}
	var body string
	switch m.screen {
	case screenLanguage:
		body = m.languageView()
	case screenHistory:
		body = m.historyView()
	case screenDetail:
		body = clientTitle(m.t("details")) + "\n\n" + m.detail.View() + "\n" + clientMuted.Render("↑↓ / PgUp / PgDn · Esc "+m.t("back"))
	default:
		body = m.testView()
	}
	if m.modal != nil {
		dialog := m.modalView()
		position := m.popupRect(dialog)
		body = overlayClientAt(body, dialog, m.width-4, position.X, position.Y)
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "  " + ansi.Truncate(line, m.width-4, "") + "  "
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

func (m clientModel) languageView() string {
	var b strings.Builder
	b.WriteString(clientTitle("KuaiNet") + "\n\n" + m.t("chooseLanguage") + " / Choose your language\n\n")
	for i, language := range clientLocales.Languages {
		line := "  ○ " + language.Native
		if i == m.languageCursor {
			line = clientAccent.Render("› ● " + language.Native)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + clientMuted.Render(m.t("helpLanguage")))
	return ansi.Hardwrap(b.String(), m.width-4, true)
}

func (m clientModel) menuLabels() []string {
	return []string{m.t("server"), m.t("testItems"), m.t("connections"), m.t("spec"), m.t("unit"), m.t("language"), m.t("historyTitle"), m.t("startTest")}
}

func (m clientModel) menuValues() []string {
	s := m.state.Settings
	target := s.Address
	if target == "" {
		target = "—"
	} else if u, err := parseTarget(s.Address, s.Port); err == nil {
		target = u.Host
	}
	var tests []string
	for i, key := range []string{"latency", "download", "upload"} {
		if s.Tests[i] {
			tests = append(tests, m.t(key))
		}
	}
	language := s.Language
	for _, l := range clientLocales.Languages {
		if l.Code == language {
			language = l.Native
			break
		}
	}
	return []string{target, strings.Join(tests, " / "), fmt.Sprint(s.Connections), fmt.Sprintf("%d %s", s.Seconds, m.t("seconds")), []string{"Mbps", "MB/s", "KB/s"}[s.Unit], language, fmt.Sprint(len(m.state.History)), "▶"}
}

func (m clientModel) testView() string {
	width := m.testWidth()
	var b strings.Builder
	b.WriteString(clientTitle("KuaiNet") + "  " + clientMuted.Render(m.t("testCard")) + "\n")
	if width >= 70 {
		b.WriteString(clientMuted.Render(ansi.Truncate(m.t("direction"), width, "…")) + "\n")
	}
	b.WriteString(m.formView() + "\n")
	status := m.t(m.status)
	if m.running && m.status == "statusRunning" {
		status += " · " + m.t([]string{"latency", "download", "upload"}[m.phase])
	}
	b.WriteString(clientAccent.Render(ansi.Truncate(status, width, "…")) + "\n")
	if m.running {
		b.WriteString(m.progress(width) + "\n")
	} else if m.record != nil {
		b.WriteString(clientMuted.Render(ansi.Truncate(m.t("resultConfig")+": "+m.record.Target+fmt.Sprintf(" · %d / %ds", m.record.Settings.Connections, m.record.Settings.Seconds), width, "…")) + "\n")
	}
	visualHeight := 7
	if m.height >= 30 {
		visualHeight = 9
	}
	remaining := m.height - 2 - lipgloss.Height(b.String()) - 4
	if width >= 70 && remaining >= 6 {
		visualHeight = min(visualHeight, remaining)
		gaugeWidth := 28
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, m.gaugeView(gaugeWidth, visualHeight), "  ", m.curveView(m.record, width-gaugeWidth-2, visualHeight)) + "\n")
	} else if m.height >= 30 && remaining >= 9 {
		b.WriteString(m.gaugeView(min(width, 32), 5) + "\n" + m.curveView(m.record, width, 4) + "\n")
	}
	b.WriteString(m.resultView(m.record, width))
	if m.err != nil {
		b.WriteString("\n" + clientError.Render(ansi.Truncate(localizedError(m.state.Settings.Language, m.err), width, "…")))
	}
	if m.notice != "" {
		b.WriteString("\n" + clientError.Render(ansi.Truncate(m.notice, width, "…")))
	}
	help := m.t("helpMenu")
	if m.running {
		help = m.t("helpRunning")
	}
	b.WriteString("\n" + clientMuted.Render(ansi.Truncate(help, width, "…")))
	return b.String()
}

func (m clientModel) progress(width int) string {
	fraction := m.live.Elapsed.Seconds() / float64(m.state.Settings.Seconds)
	label := fmt.Sprintf("%.1f / %ds", m.live.Elapsed.Seconds(), m.state.Settings.Seconds)
	if m.phase == phaseLatency {
		fraction = float64(m.live.Probes) / 10
		label = fmt.Sprintf("%d / 10", m.live.Probes)
	}
	n := max(8, min(30, width-22))
	filled := int(max(0, min(1, fraction)) * float64(n))
	text := clientAccent.Render(strings.Repeat("━", filled)) + clientMuted.Render(strings.Repeat("─", n-filled)) + "  " + label
	if m.phase != phaseLatency {
		text += "  " + formatTransfer(m.live.Bytes)
		if width < 70 {
			text += "  " + m.formatSpeed(m.live.Mbps)
		}
	}
	return ansi.Truncate(text, width, "…")
}

func (m clientModel) gaugeValue() (float64, string, lipgloss.Style) {
	if m.running && m.phase == phaseLatency {
		v := 0.0
		if m.record != nil && m.record.Latency != nil {
			v = m.record.Latency.Avg
		}
		return v, fmt.Sprintf("%.2f ms", v), clientYellow
	}
	v := m.live.Mbps
	style := clientGreen
	if m.phase == phaseUpload {
		style = clientBlue
	}
	if !m.running && m.record != nil {
		if m.record.Upload != nil {
			v = m.record.Upload.AvgMbps
			style = clientBlue
		} else if m.record.Download != nil {
			v = m.record.Download.AvgMbps
		} else if m.record.Latency != nil {
			return m.record.Latency.Avg, fmt.Sprintf("%.2f ms", m.record.Latency.Avg), clientYellow
		}
	}
	return v, m.formatSpeed(v), style
}

// Draw a semicircular speedometer with ntcharts' sub-cell Braille canvas.
func (m clientModel) gaugeView(width, height int) string {
	value, label, style := m.gaugeValue()
	scale := 100.0
	for scale < value {
		scale *= 10
	}
	c := canvas.New(width, height)
	base := graph.NewBrailleGrid(width, height, -1.15, 1.15, -0.45, 1.15)
	fill := graph.NewBrailleGrid(width, height, -1.15, 1.15, -0.45, 1.15)
	fraction := max(0, min(1, value/scale))
	for i := 0; i <= 500; i++ {
		angle := math.Pi * (1 - float64(i)/500)
		for _, radius := range []float64{0.98, 1.0} {
			p := canvas.Float64Point{X: math.Cos(angle) * radius, Y: math.Sin(angle) * radius}
			base.Set(base.GridPoint(p))
			if float64(i)/500 <= fraction {
				fill.Set(fill.GridPoint(p))
			}
		}
	}
	graph.DrawBraillePatterns(&c, canvas.Point{}, base.BraillePatterns(), clientMuted)
	graph.DrawBraillePatterns(&c, canvas.Point{}, fill.BraillePatterns(), style)
	x := max(0, (width-ansi.StringWidth(label))/2)
	c.SetStringWithStyle(canvas.Point{X: x, Y: height - 3}, label, style.Bold(true))
	c.SetStringWithStyle(canvas.Point{X: 1, Y: height - 1}, "0", clientMuted)
	scaleValue := m.speedNumber(scale)
	if strings.HasSuffix(label, " ms") {
		scaleValue = scale
	}
	maxLabel := fmt.Sprintf("%.0f", scaleValue)
	c.SetStringWithStyle(canvas.Point{X: max(1, width-len(maxLabel)-1), Y: height - 1}, maxLabel, clientMuted)
	return c.View()
}

func (m clientModel) curveView(record *clientRecord, width, height int) string {
	maxY, maxX := 1.0, float64(m.state.Settings.Seconds)
	if record != nil {
		maxX = float64(record.Settings.Seconds)
		for _, curve := range record.Curves {
			for _, point := range curve {
				maxY = max(maxY, point.Mbps*1.1)
				maxX = max(maxX, point.Elapsed.Seconds())
			}
		}
	}
	chart := linechart.New(width, max(3, height-1), 0, maxX, 0, maxY, linechart.WithXYSteps(2, 2), linechart.WithStyles(clientMuted, clientMuted, clientGreen), linechart.WithYLabelFormatter(func(_ int, v float64) string { return fmt.Sprintf("%.0f", m.speedNumber(v)) }))
	chart.DrawXYAxisAndLabel()
	if record != nil {
		for i, curve := range record.Curves {
			style := clientGreen
			if i == 1 {
				style = clientBlue
			}
			for j := 1; j < len(curve); j++ {
				chart.DrawBrailleLineWithStyle(canvas.Float64Point{X: curve[j-1].Elapsed.Seconds(), Y: curve[j-1].Mbps}, canvas.Float64Point{X: curve[j].Elapsed.Seconds(), Y: curve[j].Mbps}, style)
			}
		}
	}
	legend := clientGreen.Render(m.t("download")) + "  " + clientBlue.Render(m.t("upload")) + "  " + clientMuted.Render([]string{"Mbps", "MB/s", "KB/s"}[m.state.Settings.Unit])
	return ansi.Truncate(legend, width, "…") + "\n" + chart.View()
}

func (m clientModel) resultView(record *clientRecord, width int) string {
	latency := m.t("latency") + "  —"
	if record != nil && record.Latency != nil {
		r := record.Latency
		latency = fmt.Sprintf("%s  %.2f ms  %s %.2f  %s %.2f  %s %.2f", m.t("latency"), r.Avg, m.t("min"), r.Min, m.t("max"), r.Max, m.t("jitter"), r.Jitter)
	}
	lines := []string{clientYellow.Render(ansi.Truncate(latency, width, "…"))}
	for i, key := range []string{"download", "upload"} {
		var r *speedResult
		if record != nil {
			if i == 0 {
				r = record.Download
			} else {
				r = record.Upload
			}
		}
		text := m.t(key) + "  —"
		if r != nil {
			speed := m.t("insufficient")
			if r.Measured {
				speed = m.formatSpeed(r.AvgMbps)
			}
			text = fmt.Sprintf("%s  %s  %s %s  %s %s", m.t(key), speed, m.t("peak"), m.formatSpeed(r.PeakMbps), m.t("transferred"), formatTransfer(r.Bytes))
		}
		style := clientGreen
		if i == 1 {
			style = clientBlue
		}
		lines = append(lines, style.Render(ansi.Truncate(text, width, "…")))
	}
	return strings.Join(lines, "\n")
}

func (m clientModel) modalView() string {
	if m.isDropdown() {
		return m.dropdownView()
	}
	modal := m.modal
	width := min(66, m.width-10)
	labels := m.menuLabels()
	title := m.t("clearHistory")
	if modal.Kind != modalClear {
		title = labels[modal.Kind]
	}
	var b strings.Builder
	b.WriteString(clientAccent.Render(title) + "\n\n")
	help := m.t("helpSelect")
	if modal.Kind == menuServer {
		for i, key := range []string{"server", "port"} {
			mark := "  "
			if modal.Field == i {
				mark = "› "
			}
			b.WriteString(mark + m.t(key) + "\n  " + modal.Inputs[i].View() + "\n")
		}
		b.WriteString("\n" + clientMuted.Render(ansi.Hardwrap(m.t("addressHint"), width, true)) + "\n")
		help = m.t("helpInput")
	} else {
		if modal.Kind == modalClear {
			b.WriteString(m.t("confirmClear") + "\n\n")
		}
		for i, option := range m.modalOptions() {
			mark := "○ "
			if modal.Kind == menuTests {
				if modal.Tests[i] {
					mark = "[✓] "
				} else {
					mark = "[ ] "
				}
				help = m.t("helpMulti")
			} else if i == modal.Cursor {
				mark = "● "
			}
			line := "  " + mark + option
			if i == modal.Cursor {
				line = clientAccent.Render("› " + mark + option)
			}
			b.WriteString(line + "\n")
		}
	}
	if modal.Err != nil {
		b.WriteString("\n" + clientError.Render(ansi.Hardwrap(localizedError(m.state.Settings.Language, modal.Err), width, true)) + "\n")
	}
	b.WriteString("\n" + clientMuted.Render(ansi.Hardwrap(help, width, true)))
	return lipgloss.NewStyle().Width(width+4).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("81")).Padding(1, 2).Render(b.String())
}

// ANSI-aware overlay preserves CJK cell widths and leaves the main screen
// visible around the dialog instead of replacing it with another full page.
func overlayClientAt(background, dialog string, width, x, y int) string {
	back := strings.Split(background, "\n")
	front := strings.Split(dialog, "\n")
	fw := 0
	for _, line := range front {
		fw = max(fw, ansi.StringWidth(line))
	}
	for len(back) < y+len(front) {
		back = append(back, "")
	}
	for i, line := range front {
		original := back[y+i]
		if ansi.StringWidth(original) < width {
			original += strings.Repeat(" ", width-ansi.StringWidth(original))
		}
		back[y+i] = ansi.Truncate(ansi.Cut(original, 0, x)+line+ansi.Cut(original, x+fw, width), width, "")
	}
	return strings.Join(back, "\n")
}

func (m clientModel) historyView() string {
	body := clientTitle(m.t("historyTitle")) + fmt.Sprintf("  %d / %d", len(m.state.History), clientHistoryLimit) + "\n\n"
	if len(m.state.History) == 0 {
		body += m.t("historyEmpty") + "\n"
	} else {
		body += m.history.View() + "\n"
	}
	if m.width >= 76 {
		body += clientMuted.Render("ms / Mbps") + "\n"
	}
	body += "\n" + clientMuted.Render(ansi.Hardwrap(m.t("helpHistory"), m.width-4, true))
	return body
}

func (m clientModel) recordDetail(record clientRecord) string {
	s := record.Settings
	text := record.Time.Local().Format(time.RFC3339) + "\n" + record.Target + "\n" + m.t(record.Status) + "\n\n"
	text += fmt.Sprintf("%s: %d\n%s: %d %s\n", m.t("connections"), s.Connections, m.t("spec"), s.Seconds, m.t("seconds"))
	var tests []string
	for i, key := range []string{"latency", "download", "upload"} {
		if s.Tests[i] {
			tests = append(tests, m.t(key))
		}
	}
	text += m.t("testItems") + ": " + strings.Join(tests, " / ") + "\n\n" + m.resultView(&record, max(30, m.width-8)) + "\n"
	for i, r := range []*speedResult{record.Download, record.Upload} {
		if r == nil {
			continue
		}
		text += fmt.Sprintf("\n%s · %s %.2fs · %.2fs–%.2fs\n", m.t([]string{"download", "upload"}[i]), m.t("duration"), r.Elapsed.Seconds(), r.From.Seconds(), r.To.Seconds())
	}
	if record.Error != "" {
		text += "\n" + record.Error + "\n"
	}
	text += "\n" + m.curveView(&record, max(30, m.width-8), 9)
	return ansi.Hardwrap(text, max(30, m.width-8), true)
}

func (m clientModel) speedNumber(mbps float64) float64 {
	switch m.state.Settings.Unit {
	case 1:
		return mbps / 8
	case 2:
		return mbps * 1000 / 8
	default:
		return mbps
	}
}
func (m clientModel) formatSpeed(mbps float64) string {
	return fmt.Sprintf("%.2f %s", m.speedNumber(mbps), []string{"Mbps", "MB/s", "KB/s"}[m.state.Settings.Unit])
}
func formatTransfer(bytes int64) string {
	v := float64(bytes)
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}
