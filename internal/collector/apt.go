package collector

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bruceowenga/wbts/pkg/event"
)

const aptHistoryPath = "/var/log/apt/history.log"

// AptCollector reads package install/upgrade/remove history from apt.
type AptCollector struct{ path string }

func NewAptCollector() *AptCollector { return &AptCollector{path: aptHistoryPath} }

func (a *AptCollector) Name() string { return "apt" }

func (a *AptCollector) Available() error {
	if _, err := os.Stat(a.path); err != nil {
		return fmt.Errorf("apt history not found at %s (non-Debian/Ubuntu system?)", a.path)
	}
	f, err := os.Open(a.path)
	if err != nil {
		return fmt.Errorf("apt history not readable: %w (try: sudo usermod -aG adm $USER)", err)
	}
	f.Close()
	return nil
}

func (a *AptCollector) Collect(ctx context.Context, opts event.Options) (<-chan event.Event, error) {
	r, closers, err := multiFileReader(rotatedPaths(a.path))
	if err != nil {
		return nil, fmt.Errorf("apt: open logs: %w", err)
	}

	ch := make(chan event.Event, 64)
	go func() {
		defer close(ch)
		defer func() {
			for _, c := range closers {
				c.Close()
			}
		}()

		events, err := parseAptHistory(r, opts.Since, opts.Until)
		if err != nil {
			return
		}
		for _, e := range events {
			if opts.Filter.Container != "" {
				continue // apt events are never container-specific
			}
			select {
			case ch <- e:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// parseAptHistory parses the apt history log, returning events in [since, until].
// Exported for testing.
func parseAptHistory(r io.Reader, since, until time.Time) ([]event.Event, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	var events []event.Event
	// Entries are separated by blank lines
	for _, block := range strings.Split(string(content), "\n\n") {
		e, ok := parseAptBlock(block, since, until)
		if ok {
			events = append(events, e)
		}
	}
	return events, nil
}

func parseAptBlock(block string, since, until time.Time) (event.Event, bool) {
	fields := make(map[string]string)
	var currentKey string

	for _, line := range strings.Split(block, "\n") {
		if line == "" {
			continue
		}
		// Continuation lines (leading space) belong to the previous key
		if strings.HasPrefix(line, " ") && currentKey != "" {
			fields[currentKey] += " " + strings.TrimSpace(line)
			continue
		}
		idx := strings.Index(line, ": ")
		if idx < 0 {
			continue
		}
		currentKey = line[:idx]
		fields[currentKey] = line[idx+2:]
	}

	startStr, ok := fields["Start-Date"]
	if !ok {
		return event.Event{}, false
	}
	ts, err := parseAptTimestamp(startStr)
	if err != nil {
		return event.Event{}, false
	}
	if ts.Before(since) || ts.After(until) {
		return event.Event{}, false
	}

	// Determine action type and level
	action, pkgStr := aptAction(fields)
	if action == "" {
		return event.Event{}, false
	}

	lvl := event.Info
	if action == "Upgrade" || action == "Remove" || action == "Purge" {
		lvl = event.Warn
	}

	cmdline := fields["Commandline"]
	summary := buildAptSummary(action, pkgStr, cmdline)

	return event.Event{
		Timestamp: ts,
		Source:    "apt",
		Level:     lvl,
		Category:  event.Package,
		Summary:   summary,
		Raw:       block,
	}, true
}

// aptAction returns the first recognised action and its package string.
func aptAction(fields map[string]string) (string, string) {
	for _, action := range []string{"Upgrade", "Install", "Remove", "Purge"} {
		if v, ok := fields[action]; ok {
			return action, v
		}
	}
	return "", ""
}

func buildAptSummary(action, pkgStr, cmdline string) string {
	pkgs := formatAptPackages(action, pkgStr)
	if cmdline != "" {
		return truncate(fmt.Sprintf("apt %s: %s (via: %s)", strings.ToLower(action), pkgs, cmdline), 140)
	}
	return truncate(fmt.Sprintf("apt %s: %s", strings.ToLower(action), pkgs), 140)
}

// formatAptPackages builds a human-readable summary of the changed packages.
// Limits output to 3 packages and notes the remainder.
func formatAptPackages(action, raw string) string {
	// Split on "), " — each token is "name:arch (versions)"
	// Handle the last token which doesn't have a trailing ")"
	raw = strings.TrimSpace(raw)

	var pkgTokens []string
	for _, tok := range splitAptPackages(raw) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		pkgTokens = append(pkgTokens, tok)
	}

	total := len(pkgTokens)
	if total > 3 {
		pkgTokens = pkgTokens[:3]
	}

	var parts []string
	for _, tok := range pkgTokens {
		parts = append(parts, formatSinglePkg(action, tok))
	}

	result := strings.Join(parts, ", ")
	if total > 3 {
		result += fmt.Sprintf(" ... and %d more", total-3)
	}
	return result
}

// splitAptPackages splits the package list on ", " boundaries between packages.
// Packages look like "name:arch (v1, v2)" — commas inside parens are version separators.
func splitAptPackages(raw string) []string {
	var tokens []string
	depth := 0
	start := 0
	for i, ch := range raw {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				tokens = append(tokens, strings.TrimSpace(raw[start:i]))
				start = i + 1
			}
		}
	}
	tokens = append(tokens, strings.TrimSpace(raw[start:]))
	return tokens
}

func formatSinglePkg(action, tok string) string {
	// tok examples:
	//   "nginx:amd64 (1.18.0-6ubuntu14.3, 1.18.0-6ubuntu14.4)"  ← upgrade
	//   "docker-ce:amd64 (5:24.0.0-1~ubuntu.22.04~jammy, automatic)"  ← install
	//   "old-package:amd64 (1.0.0)"  ← remove
	nameVer := strings.SplitN(tok, " (", 2)
	name := nameVer[0]
	// Strip arch suffix (:amd64, :all, etc.)
	if idx := strings.LastIndex(name, ":"); idx > 0 {
		name = name[:idx]
	}
	if len(nameVer) < 2 {
		return name
	}
	versions := strings.TrimSuffix(nameVer[1], ")")

	switch action {
	case "Upgrade":
		vparts := strings.SplitN(versions, ", ", 2)
		if len(vparts) == 2 {
			return fmt.Sprintf("%s (%s → %s)", name, strings.TrimSpace(vparts[0]), strings.TrimSpace(vparts[1]))
		}
		return name
	case "Install":
		// Strip ", automatic" from version
		ver := strings.TrimSuffix(versions, ", automatic")
		ver = strings.TrimSuffix(ver, ",automatic")
		return fmt.Sprintf("%s (%s)", name, strings.TrimSpace(ver))
	default: // Remove, Purge
		return name
	}
}

// parseAptTimestamp parses apt history timestamps: "2026-05-06  10:30:00" (double space).
func parseAptTimestamp(s string) (time.Time, error) {
	// Normalise double space to single space
	s = strings.Join(strings.Fields(s), " ")
	return time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
}
