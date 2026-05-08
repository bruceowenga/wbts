package output

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/bruceowenga/wbts/internal/timeline"
	"github.com/bruceowenga/wbts/pkg/event"
)

// --- helpers ---

func makeTestEvent(t time.Time, lvl event.Level, summary, raw string) event.Event {
	return event.Event{
		Timestamp: t,
		Source:    "test",
		Level:     lvl,
		Category:  event.Service,
		Summary:   summary,
		Raw:       raw,
	}
}

func baseTime() time.Time { return time.Date(2026, 5, 6, 17, 0, 0, 0, time.UTC) }

func newTestModel(events []event.Event, windows []timeline.IncidentWindow) tuiModel {
	tl := &timeline.Timeline{
		Events:          events,
		IncidentWindows: windows,
		SkippedSources:  map[string]error{},
	}
	m := newTUIModel(tl)
	// Simulate a window size message so viewport is ready
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return updated.(tuiModel)
}

func keyMsg(ch rune) tea.Msg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}}
}

func keySpecial(t tea.KeyType) tea.Msg {
	return tea.KeyMsg{Type: t}
}

// --- tests ---

func TestFilterCycle(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Info, "info event", "raw info"),
		makeTestEvent(base.Add(time.Second), event.Warn, "warn event", "raw warn"),
		makeTestEvent(base.Add(2*time.Second), event.Error, "error event", "raw error"),
		makeTestEvent(base.Add(3*time.Second), event.Critical, "crit event", "raw crit"),
	}
	m := newTestModel(events, nil)

	filters := []levelFilter{filterAll, filterWarnPlus, filterErrorPlus, filterCritOnly, filterAll}
	expectedCounts := []int{4, 3, 2, 1, 4} // events visible under each filter

	for i, expected := range filters {
		if m.filter != expected {
			t.Errorf("step %d: filter = %v, want %v", i, m.filter, expected)
		}
		// Count only event items (not separators)
		eventCount := 0
		for _, item := range m.filteredItems {
			if item.kind == itemEvent {
				eventCount++
			}
		}
		if eventCount != expectedCounts[i] {
			t.Errorf("step %d: event count = %d, want %d", i, eventCount, expectedCounts[i])
		}
		// Advance filter
		next, _ := m.Update(keyMsg('f'))
		m = next.(tuiModel)
	}
}

func TestExpandCollapse(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "first event", "raw line for first event"),
		makeTestEvent(base.Add(time.Second), event.Error, "second event", "raw line for second event"),
	}
	m := newTestModel(events, nil)

	// Move to first event (cursor starts at 0)
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}

	// Expand
	next, _ := m.Update(keyMsg('e'))
	m = next.(tuiModel)
	if !m.expanded[0] {
		t.Error("expected expanded[0] = true after 'e'")
	}
	if !strings.Contains(m.content, "raw line for first event") {
		t.Error("expanded content should contain raw line")
	}

	// Collapse
	next, _ = m.Update(keyMsg('e'))
	m = next.(tuiModel)
	if m.expanded[0] {
		t.Error("expected expanded[0] = false after second 'e'")
	}
}

func TestCursorSkipsSeparators(t *testing.T) {
	base := baseTime()
	// Create 3 errors close together to trigger an incident window
	events := []event.Event{
		makeTestEvent(base, event.Error, "error 1", ""),
		makeTestEvent(base.Add(5*time.Second), event.Error, "error 2", ""),
		makeTestEvent(base.Add(10*time.Second), event.Error, "error 3", ""),
	}
	windows := []timeline.IncidentWindow{
		{
			Start: base, End: base.Add(10 * time.Second),
			EventCount: 3, Categories: []string{"SERVICE"},
			FirstFaultIndex: 0, // separator inserted before events[0]
		},
	}
	m := newTestModel(events, windows)

	// filteredItems should be: [separator, event0, event1, event2]
	if len(m.filteredItems) < 2 {
		t.Fatalf("expected at least 2 items (separator + events), got %d", len(m.filteredItems))
	}

	// Cursor should start on first event (skip leading separator if any)
	if m.filteredItems[m.cursor].kind == itemSeparator {
		t.Error("cursor should never rest on a separator at init")
	}

	// Move down — cursor should skip any separator it encounters
	current := m.cursor
	next, _ := m.Update(keySpecial(tea.KeyDown))
	m = next.(tuiModel)
	if m.filteredItems[m.cursor].kind == itemSeparator {
		t.Errorf("cursor landed on separator at index %d after ↓ from %d", m.cursor, current)
	}
}

func TestJumpToIncidentWindow(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "e1", ""),
		makeTestEvent(base.Add(5*time.Second), event.Error, "e2", ""),
		makeTestEvent(base.Add(10*time.Second), event.Error, "e3", ""),
		makeTestEvent(base.Add(5*time.Minute), event.Error, "e4", ""),
		makeTestEvent(base.Add(5*time.Minute+5*time.Second), event.Error, "e5", ""),
		makeTestEvent(base.Add(5*time.Minute+10*time.Second), event.Error, "e6", ""),
	}
	windows := []timeline.IncidentWindow{
		{Start: base, End: base.Add(10 * time.Second), EventCount: 3, Categories: []string{"SERVICE"}, FirstFaultIndex: 0},
		{Start: base.Add(5 * time.Minute), End: base.Add(5*time.Minute + 10*time.Second), EventCount: 3, Categories: []string{"SERVICE"}, FirstFaultIndex: 3},
	}
	m := newTestModel(events, windows)

	// Count separators
	var sepCount int
	for _, item := range m.filteredItems {
		if item.kind == itemSeparator {
			sepCount++
		}
	}
	if sepCount != 2 {
		t.Fatalf("expected 2 separators, got %d", sepCount)
	}

	// Jump to first window with 'n'
	next, _ := m.Update(keyMsg('n'))
	m = next.(tuiModel)
	if m.filteredItems[m.cursor].kind != itemSeparator {
		t.Errorf("after 'n', cursor should be on a separator, got kind=%v at index %d", m.filteredItems[m.cursor].kind, m.cursor)
	}
	firstSepIdx := m.cursor

	// Jump to second window with 'n'
	next, _ = m.Update(keyMsg('n'))
	m = next.(tuiModel)
	if m.filteredItems[m.cursor].kind != itemSeparator {
		t.Error("after second 'n', cursor should be on a separator")
	}
	secondSepIdx := m.cursor
	if secondSepIdx == firstSepIdx {
		t.Error("second 'n' should move to a different separator")
	}

	// Wrap: third 'n' wraps back to first separator
	next, _ = m.Update(keyMsg('n'))
	m = next.(tuiModel)
	if m.cursor != firstSepIdx {
		t.Errorf("third 'n' should wrap to first separator (%d), got %d", firstSepIdx, m.cursor)
	}

	// 'p' should go back to second separator
	next, _ = m.Update(keyMsg('p'))
	m = next.(tuiModel)
	if m.cursor != secondSepIdx {
		t.Errorf("'p' should go to second separator (%d), got %d", secondSepIdx, m.cursor)
	}
}

func TestFilterHidesInfoEvents(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Info, "info 1", ""),
		makeTestEvent(base.Add(time.Second), event.Info, "info 2", ""),
		makeTestEvent(base.Add(2*time.Second), event.Info, "info 3", ""),
		makeTestEvent(base.Add(3*time.Second), event.Error, "error 1", ""),
		makeTestEvent(base.Add(4*time.Second), event.Error, "error 2", ""),
	}
	m := newTestModel(events, nil)

	// Advance to Error+ filter (two 'f' presses: All→Warn+→Error+)
	next, _ := m.Update(keyMsg('f'))
	m = next.(tuiModel)
	next, _ = m.Update(keyMsg('f'))
	m = next.(tuiModel)

	if m.filter != filterErrorPlus {
		t.Fatalf("filter = %v, want filterErrorPlus", m.filter)
	}

	var eventCount int
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 2 {
		t.Errorf("Error+ filter: event count = %d, want 2 (only errors)", eventCount)
	}
	if strings.Contains(m.content, "info 1") {
		t.Error("Info events should not appear in Error+ filtered content")
	}
}

func TestFilterPreservesIncidentSeparators(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "error 1", ""),
		makeTestEvent(base.Add(5*time.Second), event.Error, "error 2", ""),
		makeTestEvent(base.Add(10*time.Second), event.Error, "error 3", ""),
	}
	windows := []timeline.IncidentWindow{
		{Start: base, End: base.Add(10 * time.Second), EventCount: 3, Categories: []string{"SERVICE"}, FirstFaultIndex: 0},
	}
	m := newTestModel(events, windows)

	// Switch to Error+ filter — separators should survive since errors remain
	next, _ := m.Update(keyMsg('f'))
	m = next.(tuiModel) // Warn+
	next, _ = m.Update(keyMsg('f'))
	m = next.(tuiModel) // Error+

	var sepCount int
	for _, item := range m.filteredItems {
		if item.kind == itemSeparator {
			sepCount++
		}
	}
	if sepCount != 1 {
		t.Errorf("Error+ filter: separator count = %d, want 1", sepCount)
	}
}

func TestBuildContentNoEvents(t *testing.T) {
	m := newTestModel(nil, nil)
	// Should not panic and content should have a placeholder
	if m.content == "" {
		t.Error("content should not be empty even with no events")
	}
	if !strings.Contains(m.content, "no events") && !strings.Contains(m.content, "No events") {
		t.Errorf("empty timeline content should contain 'no events' placeholder, got: %q", m.content[:min(len(m.content), 100)])
	}
}

func TestCursorBoundsCheck(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "e1", ""),
		makeTestEvent(base.Add(time.Second), event.Warn, "e2", ""),
		makeTestEvent(base.Add(2*time.Second), event.Info, "e3", ""),
	}
	m := newTestModel(events, nil)

	// Jump to bottom
	next, _ := m.Update(keyMsg('G'))
	m = next.(tuiModel)
	if m.filteredItems[m.cursor].kind == itemSeparator {
		t.Error("G should not land on a separator")
	}
	bottomCursor := m.cursor

	// Another down should not go out of bounds
	next, _ = m.Update(keySpecial(tea.KeyDown))
	m = next.(tuiModel)
	if m.cursor != bottomCursor {
		t.Errorf("↓ at bottom should not move cursor: was %d, now %d", bottomCursor, m.cursor)
	}

	// Jump to top
	next, _ = m.Update(keyMsg('g'))
	m = next.(tuiModel)
	if m.cursor != 0 {
		t.Errorf("g should move cursor to 0, got %d", m.cursor)
	}
	// Another up should stay at 0
	next, _ = m.Update(keySpecial(tea.KeyUp))
	m = next.(tuiModel)
	if m.cursor != 0 {
		t.Errorf("↑ at top should not move cursor, got %d", m.cursor)
	}
}

func TestQuitReturnsCmd(t *testing.T) {
	m := newTestModel(nil, nil)
	_, cmd := m.Update(keyMsg('q'))
	if cmd == nil {
		t.Fatal("'q' should return a non-nil cmd")
	}
	// Execute the cmd and verify it produces a tea.QuitMsg
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("'q' cmd should produce tea.QuitMsg, got %T", msg)
	}
}

func TestSearchActivation(t *testing.T) {
	m := newTestModel([]event.Event{
		makeTestEvent(baseTime(), event.Error, "nginx error", ""),
	}, nil)

	if m.searchActive {
		t.Error("searchActive should be false at init")
	}

	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	if !m.searchActive {
		t.Error("'/' should activate search mode")
	}
}

func TestSearchFiltersEvents(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "nginx upstream error", ""),
		makeTestEvent(base.Add(time.Second), event.Error, "k3s housekeeping failed", ""),
		makeTestEvent(base.Add(2*time.Second), event.Warn, "cloudflared tunnel error", ""),
	}
	m := newTestModel(events, nil)

	// Activate search and type "nginx"
	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "nginx" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}

	if m.searchQuery != "nginx" {
		t.Fatalf("searchQuery = %q, want 'nginx'", m.searchQuery)
	}

	// Only the nginx event should be visible
	eventCount := 0
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 1 {
		t.Errorf("search for 'nginx': got %d events, want 1", eventCount)
	}
	if !strings.Contains(m.content, "nginx") {
		t.Error("content should contain the matching event")
	}
	if strings.Contains(m.content, "k3s") {
		t.Error("content should not contain non-matching event")
	}
}

func TestSearchCaseInsensitive(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "NGINX upstream timeout", ""),
		makeTestEvent(base.Add(time.Second), event.Error, "k3s error", ""),
	}
	m := newTestModel(events, nil)

	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "nginx" { // lowercase search, uppercase summary
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}

	eventCount := 0
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 1 {
		t.Errorf("case-insensitive search failed: got %d events, want 1", eventCount)
	}
}

func TestSearchEscClearsQuery(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "nginx error", ""),
		makeTestEvent(base.Add(time.Second), event.Error, "k3s error", ""),
	}
	m := newTestModel(events, nil)

	// Activate search and type a query
	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "nginx" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}
	if m.searchQuery != "nginx" {
		t.Fatal("precondition: searchQuery should be 'nginx'")
	}

	// Esc should exit search mode but keep the query
	next, _ = m.Update(keySpecial(tea.KeyEsc))
	m = next.(tuiModel)
	if m.searchActive {
		t.Error("Esc should deactivate search mode")
	}
	if m.searchQuery != "nginx" {
		t.Error("first Esc should keep query (just exit input mode)")
	}

	// Second Esc should clear the query
	next, _ = m.Update(keySpecial(tea.KeyEsc))
	m = next.(tuiModel)
	if m.searchQuery != "" {
		t.Errorf("second Esc should clear query, got %q", m.searchQuery)
	}

	// All events should be visible again
	eventCount := 0
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 2 {
		t.Errorf("after clearing search, got %d events, want 2", eventCount)
	}
}

func TestSearchBackspace(t *testing.T) {
	m := newTestModel([]event.Event{
		makeTestEvent(baseTime(), event.Error, "nginx error", ""),
	}, nil)

	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "ngin" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}
	if m.searchQuery != "ngin" {
		t.Fatalf("precondition: searchQuery = %q, want 'ngin'", m.searchQuery)
	}

	next, _ = m.Update(keySpecial(tea.KeyBackspace))
	m = next.(tuiModel)
	if m.searchQuery != "ngi" {
		t.Errorf("backspace: searchQuery = %q, want 'ngi'", m.searchQuery)
	}
}

func TestSearchCombinedWithLevelFilter(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Info, "nginx info", ""),
		makeTestEvent(base.Add(time.Second), event.Error, "nginx error", ""),
		makeTestEvent(base.Add(2*time.Second), event.Error, "k3s error", ""),
	}
	m := newTestModel(events, nil)

	// Set level filter to Error+
	next, _ := m.Update(keyMsg('f')) // Warn+
	m = next.(tuiModel)
	next, _ = m.Update(keyMsg('f')) // Error+
	m = next.(tuiModel)

	// Now search for nginx
	next, _ = m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "nginx" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}

	// Should see only: nginx error (Info nginx is filtered by level; k3s filtered by search)
	eventCount := 0
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 1 {
		t.Errorf("combined filter: got %d events, want 1 (nginx error only)", eventCount)
	}
}

func TestSearchSourceField(t *testing.T) {
	base := baseTime()
	// Events with different sources
	e1 := makeTestEvent(base, event.Error, "connection timeout", "")
	e1.Source = "nginx"
	e2 := makeTestEvent(base.Add(time.Second), event.Error, "connection timeout", "")
	e2.Source = "postgres"

	m := newTestModel([]event.Event{e1, e2}, nil)

	next, _ := m.Update(keyMsg('/'))
	m = next.(tuiModel)
	for _, ch := range "nginx" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = next.(tuiModel)
	}

	// Search should match on Source field too
	eventCount := 0
	for _, item := range m.filteredItems {
		if item.kind == itemEvent {
			eventCount++
		}
	}
	if eventCount != 1 {
		t.Errorf("source search: got %d events, want 1 (nginx source only)", eventCount)
	}
}

func TestServiceColorConsistency(t *testing.T) {
	// Same service name must always return the same color
	c1 := tuiServiceColor("cloudflared")
	c2 := tuiServiceColor("cloudflared")
	if c1 != c2 {
		t.Errorf("tuiServiceColor is not deterministic: %v != %v", c1, c2)
	}

	// Different service names should return colors from the palette
	colors := map[string]bool{}
	services := []string{"nginx", "postgres", "redis", "docker", "k3s", "cloudflared", "grafana", "prometheus"}
	for _, svc := range services {
		colors[string(tuiServiceColor(svc))] = true
	}
	// Should use multiple colors (not all the same)
	if len(colors) < 2 {
		t.Error("all services got the same color — hash is not working")
	}
}

func TestExtractServiceName(t *testing.T) {
	cases := []struct {
		summary string
		want    string
	}{
		{"cloudflared.service: ERR failed", "cloudflared"},
		{"k3s.service: E0506 housekeeping", "k3s"},
		{"docker.service: level=error msg=...", "docker"},
		{"session-182.scope: odin : COMMAND=/bin/bash", "session-182"},
		{"no service prefix here", ""},
	}
	for _, c := range cases {
		got := extractServiceName(c.summary)
		if got != c.want {
			t.Errorf("extractServiceName(%q) = %q, want %q", c.summary, got, c.want)
		}
	}
}

func TestDetailPopupOpens(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "nginx error", "full raw log line for nginx"),
		makeTestEvent(base.Add(time.Second), event.Error, "k3s error", "full raw log line for k3s"),
	}
	m := newTestModel(events, nil)

	if m.detailOpen {
		t.Error("detailOpen should be false at init")
	}

	// Enter should open the detail popup
	next, _ := m.Update(keySpecial(tea.KeyEnter))
	m = next.(tuiModel)
	if !m.detailOpen {
		t.Error("Enter should open the detail popup")
	}
}

func TestDetailPopupClosesOnEsc(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "nginx error", "raw log"),
	}
	m := newTestModel(events, nil)

	next, _ := m.Update(keySpecial(tea.KeyEnter))
	m = next.(tuiModel)
	if !m.detailOpen {
		t.Fatal("precondition: detail should be open")
	}

	next, _ = m.Update(keySpecial(tea.KeyEsc))
	m = next.(tuiModel)
	if m.detailOpen {
		t.Error("Esc should close the detail popup")
	}
}

func TestDetailPopupNavigatesEvents(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "event 0", "raw 0"),
		makeTestEvent(base.Add(time.Second), event.Error, "event 1", "raw 1"),
		makeTestEvent(base.Add(2*time.Second), event.Error, "event 2", "raw 2"),
	}
	m := newTestModel(events, nil)

	// Open detail on event 0
	next, _ := m.Update(keySpecial(tea.KeyEnter))
	m = next.(tuiModel)
	if m.detailIdx != 0 {
		t.Fatalf("detailIdx = %d, want 0", m.detailIdx)
	}

	// ] moves to next event
	next, _ = m.Update(keyMsg(']'))
	m = next.(tuiModel)
	if m.detailIdx != 1 {
		t.Errorf("after ]: detailIdx = %d, want 1", m.detailIdx)
	}

	// [ moves back
	next, _ = m.Update(keyMsg('['))
	m = next.(tuiModel)
	if m.detailIdx != 0 {
		t.Errorf("after [: detailIdx = %d, want 0", m.detailIdx)
	}
}

func TestDetailPopupWrapsNavigation(t *testing.T) {
	base := baseTime()
	events := []event.Event{
		makeTestEvent(base, event.Error, "e0", "raw 0"),
		makeTestEvent(base.Add(time.Second), event.Error, "e1", "raw 1"),
	}
	m := newTestModel(events, nil)

	next, _ := m.Update(keySpecial(tea.KeyEnter))
	m = next.(tuiModel)

	// ] from last event wraps to first
	next, _ = m.Update(keyMsg(']'))
	m = next.(tuiModel) // now at idx 1
	next, _ = m.Update(keyMsg(']'))
	m = next.(tuiModel) // should wrap to 0
	if m.detailIdx != 0 {
		t.Errorf("wrap forward: detailIdx = %d, want 0", m.detailIdx)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
