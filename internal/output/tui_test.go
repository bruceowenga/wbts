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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
