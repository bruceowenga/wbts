package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/bruceowenga/wbts/internal/timeline"
	"github.com/bruceowenga/wbts/pkg/event"
)

// Options controls rendering behaviour.
type Options struct {
	NoColor bool
	JSON    bool
	Summary bool // show only incident window summaries
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

// Render writes the full timeline to w.
func Render(w io.Writer, tl *timeline.Timeline, opts Options) error {
	if opts.NoColor {
		// Honour the no-color.org convention — lipgloss respects NO_COLOR automatically.
		os.Setenv("NO_COLOR", "1") //nolint:errcheck
	}

	if opts.JSON {
		return renderJSON(w, tl)
	}

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
	ts := styleTimestamp.Render(e.Timestamp.Local().Format("2006-01-02 15:04:05"))
	cat := styleCategory.Render(fmt.Sprintf("[%-7s]", e.Category))
	lvl := renderLevel(e.Level)
	summary := renderSummary(e.Level, e.Summary)

	line := fmt.Sprintf("%s  %s  %s  %s", ts, cat, lvl, summary)
	if isFirstFault {
		line += "  " + styleFirstFault.Render("◄── FIRST FAULT?")
	}
	fmt.Fprintln(w, line)
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
