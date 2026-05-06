# wbts — what broke the server?

```
curl -fsSL https://raw.githubusercontent.com/bruceowenga/wbts/main/scripts/install.sh | bash
```

```
$ wbts --since 3h

2026-05-05 02:47:13  [KERNEL ]  CRIT  oom-killer: killed process 14821 (node) +1.2G RSS  ◄── FIRST FAULT?
2026-05-05 02:47:13  [DOCKER ]  ERRO  container app_web_1 died (exit 137)
2026-05-05 02:47:15  [SERVICE]  ERRO  app_web_1.service: entered failed state
2026-05-05 02:47:16  [DOCKER ]  WARN  container app_web_1 restarting (attempt 1/3)
2026-05-05 02:47:31  [DOCKER ]  WARN  container app_web_1 restarting (attempt 2/3)
2026-05-05 02:47:58  [DOCKER ]  WARN  container app_web_1 restarting (attempt 3/3)
2026-05-05 02:48:01  [DOCKER ]  ERRO  container app_web_1 stopped (restart limit reached)
2026-05-05 02:56:04  [CRON   ]  ERRO  /etc/cron.d/backup: FAILED (exit 1)

▶ INCIDENT WINDOW: 02:47:13–02:48:01 (7 events — DOCKER, KERNEL, SERVICE)
```

`wbts` correlates logs from journald, dmesg, Docker events, apt, auth, and cron into a
single chronological timeline. Run it after an incident to reconstruct what happened
without manually cross-referencing `journalctl`, `dmesg`, `docker events`, and `auth.log`.

---

## Installation

```bash
# One-liner
curl -fsSL https://raw.githubusercontent.com/bruceowenga/wbts/main/scripts/install.sh | bash

# Or download a release binary from GitHub Releases and put it in your PATH
```

## Usage

```bash
# Last 2 hours
wbts --since 2h

# Specific time range
wbts --since "2026-05-05 02:00" --until "2026-05-05 04:00"

# Filter to events involving a specific container
wbts --since 1h --container app_web_1

# Show only incident window summaries
wbts --since 4h --summary

# JSON output for piping to jq
wbts --since 1h --json | jq '.[] | select(.Level >= 2)'

# Check which log sources are accessible
wbts check-perms
```

## Collectors

| Collector | Source | What it captures |
|---|---|---|
| `journald` | `journalctl` | service starts/stops/crashes, systemd failures |
| `dmesg` | `/dev/kmsg` | OOM kills, kernel panics, disk errors |
| `docker` *(phase 2)* | Docker socket API | container die/OOM, image pulls |
| `apt` *(phase 2)* | `/var/log/apt/history.log` | package installs, upgrades, removals |
| `auth` *(phase 2)* | `/var/log/auth.log` | failed logins, sudo, SSH sessions |
| `cron` *(phase 2)* | journald + syslog | job failures |

## Permissions

`wbts` reads only — it never writes to logs or modifies system state.

```bash
# Check what's accessible with your current user
wbts check-perms

# For full access without sudo, add yourself to these groups:
sudo usermod -aG systemd-journal,adm,docker $USER
```

## Contributing

See [docs/collectors.md](docs/collectors.md) to learn how to write a collector.
The `pkg/event` package is the stable public API — import it to build a third-party collector.

## License

MIT
