package timeline

import "strings"

// noisePatterns are routine INFO-level messages that add no signal to an incident timeline.
// These are only applied to Info-level events — Warn/Error/Critical always pass through.
var noisePatterns = []string{
	"systemd-timesyncd",
	"Time has been changed",
	"Started Daily",
	"Started Weekly",
	"Started Monthly",
	"apt-daily",
	"apt-daily-upgrade",
	"Started man-db",
	"Reached target",
	"Stopped target",
	"Listening on",
	"Mounting ",
	"Unmounting ",
	"Finished Cleanup",
	"Finished Rotate log",
	"Started Rotate log",
	"logrotate",
	"pam_unix",
	"Reloading",
	"Reloaded",
	"Starting Session",
	"Started Session",
	"Removed slice",
	"Created slice",
}

// isNoise returns true if an Info-level event matches a known routine pattern.
// Never call this for events at Warn level or above.
func isNoise(summary string) bool {
	for _, p := range noisePatterns {
		if strings.Contains(summary, p) {
			return true
		}
	}
	return false
}
