package timeline

import (
	"strings"

	"github.com/bruceowenga/wbts/pkg/event"
)

// infoNoisePatterns suppress INFO-level events matching routine system activity.
var infoNoisePatterns = []string{
	// systemd routine lifecycle
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
	// Prometheus / metrics scrapes
	"GET /metrics",
	"POST /metrics",
	"/metrics HTTP/1.1",
	// Cloudflared routine connection messages
	"Z INF ",
	// DNS noise (tailscale, container resolvers)
	"dns: resolver: forward: no upstream",
	"SERVFAIL",
	"[RATELIMIT]",
	// Docker internal DNS resolver retries
	"[resolver] failed to query external DNS",
	// Grafana routine internal operations (structured log lines at info level)
	"logger=dashboard-service",
	"logger=cleanup",
	"logger=ngalert",
	// k3s / etcd routine maintenance
	"msg=\"COMPACT ",
	"COMPACT compactRev=",
	"COMPACT deleted",
	// sysstat routine accounting
	"sysstat-collect",
	// Tailscale network checks
	"netcheck: DetectCaptivePortal",
	// Kubernetes/k3s routine info chatter
	"updated ClusterIP allocator",
	"cidrallocator.go",
}

// warnNoisePatterns suppress WARN-level events that are routine on most Linux servers.
// Only use patterns here when the event is definitively non-incident (e.g. firewall blocks
// from a broadcast sweep, not from a targeted attack pattern).
var warnNoisePatterns = []string{
	// UFW broadcast/multicast blocks are constant background noise on any UFW-enabled server.
	// Targeted connection blocks (TCP/UDP to specific ports) are NOT suppressed.
	"[UFW BLOCK] IN=",
}

// isNoise returns true if the event should be suppressed based on its level and summary.
// ERROR and CRITICAL events are never suppressed.
func isNoise(level event.Level, summary string) bool {
	switch level {
	case event.Critical, event.Error:
		return false
	case event.Warn:
		for _, p := range warnNoisePatterns {
			if strings.Contains(summary, p) {
				return true
			}
		}
		return false
	default: // Info
		for _, p := range infoNoisePatterns {
			if strings.Contains(summary, p) {
				return true
			}
		}
		return false
	}
}
