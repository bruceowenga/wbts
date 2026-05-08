package output

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bruceowenga/wbts/internal/timeline"
	"github.com/bruceowenga/wbts/pkg/event"
)

// TUI-specific styles (separate from the plain renderer styles).
var (
	tuiCursor    = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	tuiHeader    = lipgloss.NewStyle().Faint(true)
	tuiStatusBar = lipgloss.NewStyle().Faint(true)
	tuiSep       = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	tuiRawLine   = lipgloss.NewStyle().Faint(true)
	tuiHelp      = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("6")).
			Padding(1, 2)
)

// levelFilter controls the minimum event level displayed.
type levelFilter int

const (
	filterAll       levelFilter = iota // Info and above
	filterWarnPlus                     // Warn and above
	filterErrorPlus                    // Error and above
	filterCritOnly                     // Critical only
)

func (f levelFilter) String() string {
	switch f {
	case filterAll:
		return "All"
	case filterWarnPlus:
		return "Warn+"
	case filterErrorPlus:
		return "Error+"
	case filterCritOnly:
		return "Crit"
	default:
		return "?"
	}
}

func (f levelFilter) minLevel() event.Level {
	switch f {
	case filterWarnPlus:
		return event.Warn
	case filterErrorPlus:
		return event.Error
	case filterCritOnly:
		return event.Critical
	default:
		return event.Info
	}
}

// tuiItemKind distinguishes event rows from incident window separator rows.
type tuiItemKind int

const (
	itemEvent     tuiItemKind = iota
	itemSeparator             // incident window header, not cursor-selectable
)

// tuiItem represents one row in the filteredItems list.
type tuiItem struct {
	kind        tuiItemKind
	eventIndex  int // index into tl.Events; -1 for separators
	windowIndex int // index into tl.IncidentWindows; -1 for events
}

// progressMsg wraps a ProgressUpdate received from the streaming channel.
type progressMsg timeline.ProgressUpdate

// tuiModel is the bubbletea model.
type tuiModel struct {
	tl              *timeline.Timeline
	viewport        viewport.Model
	ready           bool
	cursor          int
	expanded        map[int]bool // filteredItems index → raw expanded
	filter          levelFilter
	filteredItems   []tuiItem
	content         string
	width, height   int
	showHelp        bool
	searchQuery     string // active text filter (empty = no filter)
	searchActive    bool   // true while the user is typing a search query
	progressCh      <-chan timeline.ProgressUpdate
	collectorStates []timeline.CollectorState
	loading         bool // true until all collectors have finished
}

func newTUIModel(tl *timeline.Timeline) tuiModel {
	m := tuiModel{
		tl:       tl,
		expanded: make(map[int]bool),
		filter:   filterAll,
	}
	m.rebuildContent()
	for i, item := range m.filteredItems {
		if item.kind == itemEvent {
			m.cursor = i
			break
		}
	}
	return m
}

// newStreamingTUIModel creates a model that populates itself from a progress channel.
func newStreamingTUIModel(ch <-chan timeline.ProgressUpdate) tuiModel {
	m := tuiModel{
		tl:         &timeline.Timeline{SkippedSources: map[string]error{}},
		expanded:   make(map[int]bool),
		filter:     filterAll,
		progressCh: ch,
		loading:    true,
	}
	m.rebuildContent()
	return m
}

// awaitProgress returns a Cmd that reads the next ProgressUpdate from the channel.
func awaitProgress(ch <-chan timeline.ProgressUpdate) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-ch
		if !ok {
			return nil
		}
		return progressMsg(update)
	}
}

// RunTUI launches the bubbletea program using the alternate screen buffer.
// It accepts a streaming progress channel from BuildStreaming, showing events
// as each collector finishes rather than waiting for all to complete.
func RunTUI(ch <-chan timeline.ProgressUpdate) error {
	m := newStreamingTUIModel(ch)
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := p.Run()
	return err
}

// Init implements tea.Model — starts listening on the progress channel.
func (m tuiModel) Init() tea.Cmd {
	if m.progressCh != nil {
		return awaitProgress(m.progressCh)
	}
	return nil
}

// Update implements tea.Model.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case progressMsg:
		m.tl = msg.Timeline
		m.collectorStates = msg.CollectorStates
		m.loading = !msg.Done
		m.rebuildContent()
		if !msg.Done {
			return m, awaitProgress(m.progressCh)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, m.viewportHeight())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = m.viewportHeight()
		}
		m.rebuildContent()
		return m, nil

	case tea.KeyMsg:
		// Search input mode intercepts most keys
		if m.searchActive {
			switch msg.Type {
			case tea.KeyEsc:
				m.searchActive = false
				// First Esc exits input mode but keeps the query
			case tea.KeyEnter:
				m.searchActive = false
			case tea.KeyBackspace, tea.KeyDelete:
				if len(m.searchQuery) > 0 {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
					m.cursor = 0
					m.rebuildContent()
					m.jumpToFirstEvent()
				}
			case tea.KeyRunes:
				m.searchQuery += string(msg.Runes)
				m.cursor = 0
				m.rebuildContent()
				m.jumpToFirstEvent()
			}
			return m, nil
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "esc":
			if m.searchQuery != "" {
				// Clear the query and rebuild
				m.searchQuery = ""
				m.cursor = 0
				m.rebuildContent()
				m.jumpToFirstEvent()
			} else {
				return m, tea.Quit
			}

		case "/":
			m.searchActive = true

		case "j", "down":
			m.moveCursor(1)

		case "k", "up":
			m.moveCursor(-1)

		case "g":
			m.jumpToFirstEvent()

		case "G":
			m.jumpToLastEvent()

		case "e", "enter":
			if m.cursor < len(m.filteredItems) && m.filteredItems[m.cursor].kind == itemEvent {
				m.expanded[m.cursor] = !m.expanded[m.cursor]
				m.rebuildContent()
			}

		case "f":
			m.filter = (m.filter + 1) % (filterCritOnly + 1)
			m.expanded = make(map[int]bool) // clear expansions on filter change
			m.cursor = 0
			m.rebuildContent() // rebuilds filteredItems + content for new filter
			m.jumpToFirstEvent()

		case "n":
			m.jumpToNextSeparator(1)

		case "p":
			m.jumpToNextSeparator(-1)

		case "?":
			m.showHelp = !m.showHelp
		}
	}

	// Pass through to viewport for mouse/scroll events
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

// View implements tea.Model.
func (m tuiModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	if m.showHelp {
		return m.helpView()
	}

	header := m.headerLine()
	status := m.statusLine()

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		m.viewport.View(),
		status,
	)
}

// --- navigation helpers ---

func (m *tuiModel) moveCursor(delta int) {
	if len(m.filteredItems) == 0 {
		return
	}
	next := m.cursor + delta
	for next >= 0 && next < len(m.filteredItems) {
		if m.filteredItems[next].kind != itemSeparator {
			m.cursor = next
			m.rebuildContent() // cursor indicator is baked into content
			m.scrollViewportToCursor()
			return
		}
		next += delta
	}
	// Hit boundary — stay at current position
}

func (m *tuiModel) jumpToFirstEvent() {
	for i, item := range m.filteredItems {
		if item.kind == itemEvent {
			m.cursor = i
			m.rebuildContent()
			m.viewport.GotoTop()
			return
		}
	}
}

func (m *tuiModel) jumpToLastEvent() {
	for i := len(m.filteredItems) - 1; i >= 0; i-- {
		if m.filteredItems[i].kind == itemEvent {
			m.cursor = i
			m.rebuildContent()
			m.viewport.GotoBottom()
			return
		}
	}
}

func (m *tuiModel) jumpToNextSeparator(dir int) {
	if len(m.filteredItems) == 0 {
		return
	}
	start := m.cursor + dir
	for i := 0; i < len(m.filteredItems); i++ {
		idx := (start + i*dir + len(m.filteredItems)*abs(dir)) % len(m.filteredItems)
		if idx < 0 {
			idx += len(m.filteredItems)
		}
		if m.filteredItems[idx].kind == itemSeparator {
			m.cursor = idx
			m.rebuildContent()
			m.scrollViewportToCursor()
			return
		}
	}
}

// matchesSearch returns true if the event matches the current search query.
// Searches Summary and Source fields, case-insensitively.
func (m *tuiModel) matchesSearch(e event.Event) bool {
	if m.searchQuery == "" {
		return true
	}
	q := strings.ToLower(m.searchQuery)
	return strings.Contains(strings.ToLower(e.Summary), q) ||
		strings.Contains(strings.ToLower(e.Source), q)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// scrollViewportToCursor ensures the selected line is visible.
func (m *tuiModel) scrollViewportToCursor() {
	// Approximate line position by counting rendered lines before cursor
	lines := strings.Split(m.content, "\n")
	if m.cursor >= len(lines) {
		return
	}
	// Count lines before cursor index in filteredItems
	linesBefore := 0
	for i := 0; i < m.cursor && i < len(m.filteredItems); i++ {
		linesBefore++ // one line per item base
		if m.filteredItems[i].kind == itemEvent && m.expanded[i] {
			rawLines := len(strings.Split(m.tl.Events[m.filteredItems[i].eventIndex].Raw, "\n"))
			linesBefore += rawLines + 1 // raw lines + blank line
		}
	}
	half := m.viewport.Height / 2
	if linesBefore > half {
		m.viewport.SetYOffset(linesBefore - half)
	} else {
		m.viewport.SetYOffset(0)
	}
}

// --- content building ---

func (m *tuiModel) rebuildContent() {
	m.filteredItems = m.buildFilteredItems()
	m.content = m.renderContent()
	m.viewport.SetContent(m.content)
}

func (m *tuiModel) buildFilteredItems() []tuiItem {
	if m.tl == nil || len(m.tl.Events) == 0 {
		return nil
	}

	minLevel := m.filter.minLevel()

	// Build a set: eventIndex → windowIndex for first-fault markers
	firstFaultToWindow := make(map[int]int)
	for wi, iw := range m.tl.IncidentWindows {
		if iw.FirstFaultIndex >= 0 {
			firstFaultToWindow[iw.FirstFaultIndex] = wi
		}
	}

	var items []tuiItem
	for i, e := range m.tl.Events {
		// Inject separator before the first event of each incident window
		if wi, ok := firstFaultToWindow[i]; ok {
			// Only include separator if at least one event in the window passes the filter
			iw := m.tl.IncidentWindows[wi]
			hasVisible := false
			end := iw.FirstFaultIndex + iw.EventCount
			if end > len(m.tl.Events) {
				end = len(m.tl.Events)
			}
			for j := iw.FirstFaultIndex; j < end; j++ {
				if m.tl.Events[j].Level >= minLevel && m.matchesSearch(m.tl.Events[j]) {
					hasVisible = true
					break
				}
			}
			if hasVisible {
				items = append(items, tuiItem{kind: itemSeparator, eventIndex: -1, windowIndex: wi})
			}
		}

		if e.Level >= minLevel && m.matchesSearch(e) {
			items = append(items, tuiItem{kind: itemEvent, eventIndex: i, windowIndex: -1})
		}
	}
	return items
}

func (m *tuiModel) renderContent() string {
	if len(m.filteredItems) == 0 {
		return styleDim.Render("No events match the current filter. Press f to change.")
	}

	firstFaults := make(map[int]bool)
	for _, iw := range m.tl.IncidentWindows {
		if iw.FirstFaultIndex >= 0 {
			firstFaults[iw.FirstFaultIndex] = true
		}
	}

	var sb strings.Builder
	for i, item := range m.filteredItems {
		selected := (i == m.cursor)

		switch item.kind {
		case itemSeparator:
			iw := m.tl.IncidentWindows[item.windowIndex]
			sep := tuiSeparatorLine(iw, m.width)
			if selected {
				sb.WriteString(tuiCursor.Render("▶ ") + sep)
			} else {
				sb.WriteString("  " + sep)
			}
			sb.WriteByte('\n')

		case itemEvent:
			e := m.tl.Events[item.eventIndex]
			isFirst := firstFaults[item.eventIndex]
			line := renderEventLine(e, isFirst)

			if selected {
				sb.WriteString(tuiCursor.Render("▶ ") + line)
			} else {
				sb.WriteString("  " + line)
			}
			sb.WriteByte('\n')

			// Expanded raw line
			if m.expanded[i] {
				raw := e.Raw
				if raw == "" {
					raw = "(no raw log line available)"
				}
				// Wrap raw line to terminal width minus indent
				indent := "    "
				wrapped := wrapText(raw, m.width-len(indent))
				for _, wl := range strings.Split(wrapped, "\n") {
					sb.WriteString(tuiRawLine.Render(indent + wl))
					sb.WriteByte('\n')
				}
				sb.WriteByte('\n') // blank line after raw
			}
		}
	}
	return sb.String()
}

func tuiSeparatorLine(iw timeline.IncidentWindow, width int) string {
	cats := strings.Join(iw.Categories, ", ")
	inner := fmt.Sprintf(" INCIDENT WINDOW  %s–%s  ·  %d events  ·  %s ",
		iw.Start.Local().Format("15:04:05"),
		iw.End.Local().Format("15:04:05"),
		iw.EventCount,
		cats,
	)
	rule := strings.Repeat("━", max(0, width-4))
	return tuiSep.Render(rule + "\n" + inner + "\n" + rule)
}

// wrapText breaks s at word boundaries to fit within maxWidth.
func wrapText(s string, maxWidth int) string {
	if maxWidth <= 0 || len(s) <= maxWidth {
		return s
	}
	var lines []string
	for len(s) > maxWidth {
		cut := maxWidth
		// Try to cut at a space
		for cut > 0 && s[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = maxWidth // no space found, hard cut
		}
		lines = append(lines, s[:cut])
		s = strings.TrimPrefix(s[cut:], " ")
	}
	lines = append(lines, s)
	return strings.Join(lines, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- header and status ---

func (m tuiModel) headerLine() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("%d events", len(m.tl.Events)))
	if len(m.tl.IncidentWindows) > 0 {
		parts = append(parts, fmt.Sprintf("%d incident windows", len(m.tl.IncidentWindows)))
	}
	if len(m.tl.SkippedSources) > 0 {
		parts = append(parts, fmt.Sprintf("%d unavailable", len(m.tl.SkippedSources)))
	}

	header := "wbts — " + strings.Join(parts, " · ")

	// Show per-collector status while loading
	if m.loading && len(m.collectorStates) > 0 {
		var statuses []string
		for _, s := range m.collectorStates {
			if s.Error != nil {
				statuses = append(statuses, s.Name+" ✗")
			} else if s.Done {
				statuses = append(statuses, s.Name+" ✓")
			} else {
				statuses = append(statuses, s.Name+" ⟳")
			}
		}
		header += "   " + strings.Join(statuses, "  ")
	}

	return tuiHeader.Render(header)
}

func (m tuiModel) statusLine() string {
	if m.showHelp {
		return tuiStatusBar.Render("  ESC close help")
	}

	// Search input mode: show the query with a blinking cursor indicator
	if m.searchActive {
		return tuiStatusBar.Render(fmt.Sprintf("  /%s█", m.searchQuery))
	}

	// Current position: count event items up to cursor
	pos := 0
	total := 0
	for i, item := range m.filteredItems {
		if item.kind == itemEvent {
			total++
			if i <= m.cursor {
				pos = total
			}
		}
	}

	searchInfo := ""
	if m.searchQuery != "" {
		searchInfo = fmt.Sprintf("  ·  /%s", m.searchQuery)
	}

	hints := "/ search  ·  ↑↓ scroll  ·  e expand  ·  n/p incident  ·  f filter  ·  q quit  ·  ? help"
	status := fmt.Sprintf("  %d/%d  ·  Filter: %s%s  ·  %s", pos, total, m.filter, searchInfo, hints)
	return tuiStatusBar.Render(status)
}

func (m tuiModel) viewportHeight() int {
	h := m.height - 2 // 1 header + 1 status bar
	if h < 1 {
		return 1
	}
	return h
}

// --- help overlay ---

const helpText = `wbts keyboard shortcuts

  ↑ / k       scroll up
  ↓ / j       scroll down
  g           jump to top
  G           jump to bottom
  e / Enter   expand / collapse raw log line
  f           cycle filter: All → Warn+ → Error+ → Crit
  /           search (type to filter, Esc to clear)
  n           jump to next incident window
  p           jump to previous incident window
  ?           toggle this help
  q           quit`

func (m tuiModel) helpView() string {
	box := tuiHelp.Render(helpText)
	// Centre in terminal
	lines := strings.Split(box, "\n")
	boxH := len(lines)
	boxW := 0
	for _, l := range lines {
		if len(l) > boxW {
			boxW = len(l)
		}
	}
	topPad := (m.height - boxH) / 2
	leftPad := (m.width - boxW) / 2
	if topPad < 0 {
		topPad = 0
	}
	if leftPad < 0 {
		leftPad = 0
	}
	pad := strings.Repeat(" ", leftPad)
	var sb strings.Builder
	for i := 0; i < topPad; i++ {
		sb.WriteByte('\n')
	}
	for _, l := range lines {
		sb.WriteString(pad + l + "\n")
	}
	sb.WriteString("\n" + m.statusLine())
	return sb.String()
}
