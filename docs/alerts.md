# The alerts

An unexpected stop or a disk problem raises an alert. An orderly `holocron down` raises none. Every alert goes through one dispatcher that never blocks recording, to every enabled **channel**, and is always written to `holocron.log` as an `ALERT ...` line. The unexpected stop is also recorded in `_state\status.json` as `StopKind` plus `LastUnexpectedStop` (timestamp, reason, pid), carried across restarts so the NVR being back up does not erase the fact that it went down (CONFIRMED: internal/host/README.md, `recordUnexpected` in internal/host/host.go).

## Channels

| Channel | Default | What it does |
| --- | --- | --- |
| `log` | always on | The `ALERT {title} - {message}` line in `holocron.log`. |
| `desktop` | on | Windows: a toast, sent through `powershell.exe` (the one place Holocron uses PowerShell), unelevated, no focus steal, kept in the Action Center. Linux: `notify-send` when it is on PATH. macOS: `osascript`. Otherwise the channel is off. |
| `eventlog` | on | Windows: the Application log, entry text `IPCCTV: {title} - {message}`, event id = alert id. Linux and macOS: syslog tag `holocron`, same text plus `[alert N]`. |
| `webhook` | off | JSON POST to `alerts.webhook.url` (https, or http to a loopback or private address), optional shared secret in a header. |
| `push` | off | An ntfy-style POST to `alerts.push.server`/`alerts.push.topic` (default server `https://ntfy.sh`), reaches a phone running the ntfy app. On ntfy.sh the topic name is the only access control: make it long and random. |

Settings are the `alerts.*` keys in [configuration.md](configuration.md) (`holocron config set alerts.push.topic <topic>`). `holocron alert test [channel]` sends a test through one or every channel and prints ok, off or FAIL for each; `holocron status` and `holocron doctor` list which channels are on. A channel that fails never blocks another: network channels are retried after 1, 5 and 30 s, then the alert is dropped with an ERROR in the log (CONFIRMED: internal/alerts/README.md, internal/alerts/dispatcher.go).

## The ids

| id | Level | Raised by | What it means |
| --- | --- | --- | --- |
| `100` | Error | host | the supervisor stopped unexpectedly and is being restarted (after 5 s) |
| `101` | Warning | host | it is recording again (raised once the restarted supervisor reports `running`) |
| `102` | Error | host | it stopped and the restart budget (5 restarts in 30 minutes) is spent, so it is staying down; also raised at once when the supervisor exits 21 (configuration fault) |
| `103` | Warning | host | the window running `holocron up` (attached mode) was closed, so nothing is recording and nothing will restart it |
| `104` | Error | host | the web app crashed 5 times in 30 minutes and is being restarted (at most one alert per 30 minutes); recording is not affected |
| `110` | Warning | supervisor | one camera has made no file progress for max(`CameraDownAlertMinutes`, one segment + one status tick + 30 s): 15.6 minutes (935 s) with the defaults, whatever `CameraDownAlertMinutes` says below that. The floor is `SegmentSeconds + StatusIntervalSeconds + 30` seconds; a shorter limit would fire on a healthy camera at segment boundaries, where it waits for the next segment's first keyframe and the next status tick. The supervisor logs the effective value once at start when the floor raised it. Repeated every `CameraDownRepeatHours` while the camera stays down |
| `120` | Error | pruner | pruning is disabled: `RootPath` is unsafe or has no valid ownership marker (not claimed as the recorder's own archive), so the disk will fill |
| `121` | Error | pruner | pruning is blocked: free space is below `ReserveGB` and nothing deletable is left, so the disk is filling. Once per episode; recording continues |
| `122` | Error | pruner | the age rule's circuit breaker held a run that would have deleted more than `MaxAgePruneFraction` of the archive. Once per episode; the free-space rule still runs |
| `130` | Error | supervisor | the relay's control API refused the product's login, so which cameras it is serving cannot be seen; every consumer is kept on the relay. Once per episode; `holocron restart` makes the relay reload the login |

CONFIRMED against: internal/alerts/event.go (ids, levels), internal/host/host.go (100 to 103, `MaxRestarts` 5 per 30 min, 5 s delay), internal/host/auxiliary.go and internal/app/web.go (104), internal/supervise/down.go (110, repeat), internal/prune/state.go (120 to 122), internal/supervise/loop.go (130).

Id 103 comes from the console-close duty of the host: closing the terminal (or logging off) of an attached `holocron up` stops the supervisor, MediaMTX and every `ffmpeg`, records `StopKind` `window-closed` and a `LastWindowClose` timestamp in `status.json`, and raises 103 once. The Windows logon task installed with `--window visible` runs exactly that attached form; `--window hidden` runs `holocron up --detach` with no window at all (internal/autostart/logon.go); the other modes are described in [autostart.md](autostart.md).

## Reading them back

Every alert is in the log: `holocron logs --action alert.raise` (supervisor and pruner alerts) and `holocron logs --component host` (host alerts: the `ALERT <id>` line, action `host.alert`).

On Windows the `eventlog` channel writes under the always-registered `Application` source, or under the `IPCCTV` source when that source is registered on the machine. Holocron has no command that registers it (it only checks for it), so the entries normally arrive under `Application`. Reading them back:

```powershell
Get-WinEvent -FilterHashtable @{ LogName = 'Application'; Id = 100,101,102,103,104,110,120,121,122,130 } |
  Select-Object TimeCreated, Id, LevelDisplayName, ProviderName,
    @{ n = 'Text'; e = { if ($_.Message) { $_.Message } else { [string]$_.Properties[0].Value } } } |
  Where-Object { $_.Text -like 'IPCCTV: *' }
```

That filters on the `IPCCTV: ` prefix, not on a provider, **on purpose**: the entries arrive under `Application` unless an `IPCCTV` source is registered, so pinning `ProviderName` could quietly return nothing; the `ProviderName` column tells you which answered. The ids alone are not Holocron's exclusively in a log every application writes to, and the `Properties[0].Value` fallback covers a source with no message resource, which leaves `.Message` empty. With no matching events `Get-WinEvent` reports "No events were found that match the specified selection criteria" - the quiet answer, not a fault.

On Linux and macOS: `journalctl -t holocron` (or the syslog file) shows the entries.
