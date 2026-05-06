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
	// Tailscale network checks and DERP relay management (routine mesh networking)
	"netcheck: DetectCaptivePortal",
	"magicsock:",
	"derphttp.Client",
	"health(warnable=",
	// Docker bridge interfaces entering/leaving promiscuous mode (container network lifecycle)
	"entered promiscuous mode",
	"left promiscuous mode",
	// Kubernetes/k3s routine info chatter
	"updated ClusterIP allocator",
	"cidrallocator.go",
	// GIN HTTP access log — suppress successful responses only (4xx/5xx pass through)
	"| 200 |",
	"| 201 |",
	"| 204 |",
	"| 304 |",
	// Cron job execution lines (not failures — failures show at higher priority)
	"cron.service: (root) CMD",
	"cron.service: (CRON) CMD",
	"(root) CMD",   // direct CRON syslog identifier (no service prefix)
	// Snap package scope lifecycle (very chatty, no incident signal)
	"Started snap.",
	"snap.go.go-",
	"scope: Consumed ",
	// Code-server / VS Code routine client reconnections
	"The client has reconnected",
	"[ExtensionHostConnection]",
	"[ManagementConnection]",
	// Grafana routine background workers
	"logger=plugins.update.checker",
	"logger=bleve-backend",
	"logger=infra.usagestats",
	"logger=plugin.finder",
	"logger=grafana.update.checker",
	"logger=sqlstore.transactions",
	// RTC wake alarm scheduling (routine periodic task)
	"rtc-wake-scheduler",
	"rtcwake:",
	"RTC wake alarm set",
	// Firmware update metadata refresh (routine background check)
	"fwupd-refresh",
	// Docker / containerd container lifecycle internals
	// (restarting container is kept — it's signal; shim churn is internal machinery)
	"shim disconnected",
	"cleaning up after shim",
	"cleaning up dead shim",
	"connecting to shim",
	"received task-delete event",
	"sbJoin:",
	// Docker overlay2 mount lifecycle in init.scope
	"var-lib-docker-overlay2-",
	"docker-", // init.scope docker-<id>.scope start/stop lines
	// HTTP access log success responses (non-GIN format, e.g. Python SimpleHTTPServer)
	`HTTP/1.1" 200`,
	`HTTP/1.1" 201`,
	`HTTP/1.1" 204`,
	`HTTP/1.1" 304`,
	// ollama_exporter successful probe results (high volume; failures are kept via [error] prefix)
	"[probe] ",
	"[collector] Running collection",
	// Prometheus TSDB routine maintenance (compaction, GC, block writes, checkpoints)
	"source=compact.go",
	"source=head.go",
	"source=checkpoint.go",
	"msg=\"write block ",
	"msg=\"Head GC ",
	"msg=\"compact blocks\"",
	"msg=\"Deleting obsolete",
	"msg=\"Creating checkpoint\"",
	// snapd internal lifecycle (chatty during snap installs/updates)
	"overlord.go:",
	"daemon.go:",
	"standby.go:",
	"certmgr.go:",
	"backends.go:",
	"backend.go:",
	"snapmgr.go:",
	"patch.go:",
	"cache.go:",
	"api_snaps.go:",
	"store_download.go:",
	"taskrunner.go:",
	"snapd.service: Deactivated",
	"snapd.service: Scheduled restart",
	"tmp-syscheck",
	// systemd-logind hardware button watching (fires on every login/session)
	"Watching system buttons on",
	// dbus service activation (routine system bus operations)
	"[system] Activating via systemd",
	"[system] Successfully activated service",
	// Tailscale network state change dumps
	"control: NetInfo:",
	// snap loop device mounts (capacity changes during snap install/update)
	"detected capacity change from 0 to",
	// AppArmor profile loads/replaces during snap operations
	"apparmor=\"STATUS\" operation=\"profile_",
	// Routine init.scope service starts/stops (Ubuntu background maintenance)
	"Starting apt-news", "Started apt-news", "Finished apt-news", "apt-news.service: Deactivated",
	"Starting esm-cache", "Started esm-cache", "Finished esm-cache", "esm-cache.service: Deactivated",
	"Starting update-notifier", "Finished update-notifier", "update-notifier-download.service: Deactivated",
	"Starting packagekit", "Started packagekit", "Finished packagekit", "packagekit.service: Deactivated",
	"Starting systemd-timedated", "Started systemd-timedated", "Finished systemd-timedated", "timedated.service: Deactivated",
	"Starting systemd-tmpfiles-clean", "Finished systemd-tmpfiles-clean", "systemd-tmpfiles-clean.service: Deactivated",
	"Starting snapd.service", "Started snapd.service", "Finished snapd.service",
	"Mounted snap-",   // snap squashfs mount announcements
	"snap-snapd-",     // snapd snap specific
	"snap-core",       // core snap mounts
	// Syncthing routine NAT-PMP port acquisition attempts
	"Failed to acquire",
	// Grafana Alloy routine stats reporting
	"msg=\"reporting Alloy stats\"",
	"msg=\"usage report sent with success\"",
	"msg=\"series GC completed\"",
	// Launchpadlib cache cleanup (Ubuntu background task)
	"launchpadlib-cache-clean",
	// ModemManager routine hardware probing
	"[base-manager] couldn't check support",
	// User D-Bus session lifecycle
	"Starting dbus.service - D-Bus User",
	"Started dbus.service - D-Bus User",
	"AppArmor D-Bus mediation is enabled",
	"Successfully activated service 'org.freedesktop",
	// Special user nobody warning (known config, appears on systemd rescans)
	"Special user nobody configured, this is not safe",
	// logind routine session tracking (the auth collector handles SSH/sudo signal)
	"New session ",
	"Started session-",
}

// warnNoisePatterns suppress WARN-level events that are routine on most Linux servers.
// Only use patterns here when the event is definitively non-incident (e.g. firewall blocks
// from a broadcast sweep, not from a targeted attack pattern).
var warnNoisePatterns = []string{
	// UFW broadcast/multicast blocks are constant background noise on any UFW-enabled server.
	// Targeted connection blocks (TCP/UDP to specific ports) are NOT suppressed.
	"[UFW BLOCK] IN=",
	// carduka-scraper config permission warnings fire on every systemd daemon-reload/rescan
	"carduka-scraper.timer is marked world-inaccessible",
	"carduka-scraper.service is marked world-inaccessible",
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
