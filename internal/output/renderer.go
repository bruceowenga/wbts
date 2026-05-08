package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"

	"github.com/bruceowenga/wbts/internal/timeline"
	"github.com/bruceowenga/wbts/pkg/event"
)

// Options controls rendering behaviour.
type Options struct {
	NoColor bool
	JSON    bool
	Summary bool // show only incident window summaries
	NoTUI   bool // disable interactive TUI, use plain output
}

var (
	styleDim        = lipgloss.NewStyle().Faint(true)
	styleTimestamp  = lipgloss.NewStyle().Faint(true)
	styleCategory   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	styleCritical   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleError      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleWarn       = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleIncident   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleFirstFault = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
)

// Render writes the full timeline to w, or launches the TUI when stdout is
// a terminal and the user hasn't opted out with --no-tui, --json, --no-color,
// or --summary.
func Render(w io.Writer, tl *timeline.Timeline, opts Options) error {
	if opts.NoColor {
		// Honour the no-color.org convention — lipgloss respects NO_COLOR automatically.
		os.Setenv("NO_COLOR", "1") //nolint:errcheck
	}

	if opts.JSON {
		return renderJSON(w, tl)
	}

	// TUI is now launched from main.go via RunTUI(BuildStreaming(...)).
	// Render is only called for the plain renderer path.

	// Build a set of first-fault event indices for O(1) lookup during render
	firstFaults := make(map[int]bool)
	for _, iw := range tl.IncidentWindows {
		if iw.FirstFaultIndex >= 0 {
			firstFaults[iw.FirstFaultIndex] = true
		}
	}

	if !opts.Summary {
		for i, e := range tl.Events {
			renderEvent(w, e, firstFaults[i])
		}
	}

	if len(tl.IncidentWindows) > 0 {
		fmt.Fprintln(w)
		for _, iw := range tl.IncidentWindows {
			renderIncidentWindow(w, iw)
		}
	} else if len(tl.Events) > 0 && !opts.Summary {
		fmt.Fprintf(w, "\n%s\n", styleDim.Render("no incident windows detected"))
	}

	if len(tl.SkippedSources) > 0 {
		fmt.Fprintln(w)
		for name, err := range tl.SkippedSources {
			fmt.Fprintf(w, "%s\n", styleDim.Render(fmt.Sprintf("! %s unavailable: %s", name, err)))
		}
	}

	if len(tl.Events) == 0 && len(tl.SkippedSources) == 0 {
		fmt.Fprintln(w, styleDim.Render("no events found in the specified time range"))
	}

	return nil
}

func renderEvent(w io.Writer, e event.Event, isFirstFault bool) {
	fmt.Fprintln(w, renderEventLine(e, isFirstFault))
}

// renderEventLine returns the formatted event line string used by both the
// plain renderer (via renderEvent) and the TUI.
func renderEventLine(e event.Event, isFirstFault bool) string {
	ts := styleTimestamp.Render(e.Timestamp.Local().Format("2006-01-02 15:04:05"))
	cat := styleCategory.Render(fmt.Sprintf("[%-7s]", e.Category))
	lvl := renderLevel(e.Level)
	summary := renderSummary(e.Level, e.Summary)

	line := fmt.Sprintf("%s  %s  %s  %s", ts, cat, lvl, summary)
	if isFirstFault {
		line += "  " + styleFirstFault.Render("◄── FIRST FAULT?")
	}
	return line
}

func renderLevel(l event.Level) string {
	switch l {
	case event.Critical:
		return styleCritical.Render("CRIT")
	case event.Error:
		return styleError.Render("ERRO")
	case event.Warn:
		return styleWarn.Render("WARN")
	default:
		return "INFO"
	}
}

func renderSummary(l event.Level, s string) string {
	switch l {
	case event.Critical:
		return styleCritical.Render(s)
	case event.Error:
		return styleError.Render(s)
	case event.Warn:
		return styleWarn.Render(s)
	default:
		return s
	}
}

func renderIncidentWindow(w io.Writer, iw timeline.IncidentWindow) {
	start := iw.Start.Local().Format("15:04:05")
	end := iw.End.Local().Format("15:04:05")
	cats := strings.Join(iw.Categories, ", ")
	msg := fmt.Sprintf("▶ INCIDENT WINDOW: %s–%s (%d events — %s)", start, end, iw.EventCount, cats)
	fmt.Fprintln(w, styleIncident.Render(msg))
}

func renderJSON(w io.Writer, tl *timeline.Timeline) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(tl.Events)
}

// ShouldUseTUI returns true when the TUI should be launched instead of the
// plain renderer. Used by main.go to decide which path to take.
func ShouldUseTUI(w io.Writer, opts Options) bool {
	return !opts.NoTUI && !opts.NoColor && !opts.Summary && !opts.JSON && isTTY(w)
}

// isTTY returns true when w is an *os.File connected to a real terminal.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}
