# Holocron

Holocron turns your PC into a 24/7 home security recorder. One small program records every IP camera with sound, heals itself when a stream drops, files footage by camera and day, frees space on its own, and alerts you when something is wrong. Watch live or replay clips from any browser or phone. Free for noncommercial use.

## What it offers

* **Recording with audio.** Each camera is recorded by `ffmpeg` as a stream copy of its video and audio into fixed-length segments (15 minutes by default), with no re-encoding.
* **Self-healing.** A supervisor restarts a recorder that exits or stalls, with backoff, and reads the cameras through a relay (MediaMTX) so the recorder and the live view share one camera session.
* **A web app.** Dashboard, Live (WebRTC, sub-streams in the grid and the main stream on focus), Footage (browse by camera and day, play clips), Settings, and for admins an Admin tab with the health panel and user management. Sign-in with two roles: admin and viewer.
* **Phones and the LAN over HTTPS.** An optional HTTPS listener with its own local certificate authority (`holocron tls ca` shows how to install it on a device), and a Phone tab.
* **Retention by free space.** The pruner deletes the oldest footage to keep a free-space watermark (`ReserveGB`), so the archive holds what fits on the disk.
* **A tidy archive.** New segments land in each camera's folder (the spool); closed ones are filed into one folder per day, `<Camera>/YYYY-MM-DD/`.
* **Alerts** through the desktop, the Windows event log, the log file, a push channel (ntfy-style) and a webhook; `holocron alert test` proves each channel.
* **`holocron doctor`**, a read-only health check that groups its findings (setup, recording, connections, live, web, storage, system) and gives a fix for every problem.
* **One executable** for every role and command, `holocron` (`holocron.exe` on Windows). Windows first and live there since 22 Oct 2026; Linux and macOS builds are cross-compiled and tested in CI.

## Quick start

1. Put `holocron.exe` in a permanent folder (`./build-holocron.sh` in WSL builds it; see [docs/development.md](docs/development.md)).
2. Beside it, `holocron.config.json` (`{ "RootPath": "E:\\cameras" }` is enough) and `cameras.json` (`Name`, `IP`, `Quality`, `Record`).
3. `holocron secret set camera.user` and `holocron secret set camera.password`, then `holocron doctor`.
4. `holocron up --detach`, then `holocron open` for the web app and `holocron install` to start at logon.

The same four steps with the result of each, and Linux and macOS, are in [the quickstart](docs/holocron-quickstart.md).

## Documentation

* **Guides** - [quickstart](docs/holocron-quickstart.md) and the [cheat sheet](docs/holocron-cheatsheet.md) (commands by intent, files, ports, exit codes, alerts, the likeliest problems).
* **Operating** - [the web app](docs/ui.md), [autostart](docs/autostart.md), [alerts](docs/alerts.md), [configuration](docs/configuration.md) (every key: [internal/config/KEYS.md](internal/config/KEYS.md)), [troubleshooting](docs/troubleshooting.md), [rollback](docs/rollback.md), [known limitations](docs/limitations.md), [comparing two archives](docs/shadow.md).
* **How it works** - [docs/how-it-works.md](docs/how-it-works.md).
* **Development** - [building, testing and the documentation checks](docs/development.md), [continuous integration](docs/ci.md).
* **For AI agents** - [docs/ai/holocron-for-agents.md](docs/ai/holocron-for-agents.md), [AGENTS.md](AGENTS.md) and [llms.txt](llms.txt).
* **Design, decisions and records** - `tasks/holocron/`, with the records of the cutover from the PowerShell system ([runbook](tasks/holocron/cutover-runbook.md), [plan and record](tasks/holocron/cutover-plan.md)) and the shadow run before it ([runbook](tasks/holocron/shadow-runbook.md)).

## Where it came from

Holocron replaced a PowerShell camera wall and recorder on 22 Oct 2026. That system is not in this repository; it lives complete in the original repository `windows-terminal-IPCCTV-dashboard` (branch `master`); Holocron was built there, on the branch `portable-nvr`, until 28 Oct 2026, and this repository starts at the tag `v1.0.0`. The PowerShell system is the target of the last step of [the rollback](docs/rollback.md). If it is still installed on a machine, `holocron doctor` reports it in its `system` group and `holocron install` leaves its tasks alone.

## Stress tests

Before the Holocron live view was built, its design was load-tested on 2026-10-15: how much CPU and GPU does a browser (Chrome 155, Edge 155) or mpv need to show six cameras at once, at which resolutions, with hardware decode on and off, and what a hidden tab costs. The testbed is a sandbox MediaMTX v1.21.2 on loopback fed by six looping `ffmpeg -c copy` publishers (synthetic 20 fps H.264 clips at 2560x1440, 1280x720 and 640x360), a test page with six WebRTC tiles, and an out-of-process sampler; each cell is 30 s warm-up plus 180 s sampled, one run per cell. Machine: i5-6600K (4 cores), 32 GB, GTX 1070 Ti plus Intel HD 530, Windows, measured while the Operator's normal desktop was running. CPU below is the browser's total in % of ONE core (400 = the whole PC).

* **Six 2560x1440 streams, hardware decode on** - smooth: Chrome 64.7% CPU, Edge 67.4%, GPU video decode 41.9% / 38.9%, 20 fps on every tile.
* **The same, hardware decode off** - unusable: Chrome 150.1% CPU, 14.71% frames dropped, 142.2 s of frozen video in 3 min; Edge 185.6% CPU, 2.78% dropped. The whole PC hit 100%.
* **Six 640x360 sub-streams** - smooth with hardware decode on or off, 52.9% to 59.3% CPU (Edge with hardware on was marginal, 47 freezes, read as noise); mpv with the dashboard's flags used 38.3%.
* **Five sub-streams plus one 2560x1440 focused** - smooth in all four cells: 54.1% and 54.7% CPU with hardware decode, 74.9% and 72.9% without. This is why the grid shows sub-streams and the main stream only on focus.
* **A hidden (minimized) window still decodes, throttled** - six 1440p tiles went from 64.5% to 20.6% CPU in Chrome (67.1% to 23.3% in Edge) and from about 20 fps to 5.2-6.0 fps per tile in Chrome (4.9-9.9 in Edge).
* **mpv, six 2560x1440 streams** - 140.6% CPU, 0 dropped frames.

Caveats: the single-tile runs (`A-*`) went over ICE-over-TCP by accident and are transport-limited, not representative of UDP viewing; Firefox never connected to the loopback sandbox, so there are no Firefox numbers; the PC was already at 74.9% CPU with no browser running, so absolute figures are pessimistic and only comparisons between cells are safe. Not measured: LAN, Wi-Fi or phone viewers and real cameras. Everything - hardware and software, what ran, every result table, the raw data and how to run it again - is in [tests/stress/live-view/README.md](tests/stress/live-view/README.md).

## Spikes

The feasibility spikes SP1 to SP7 (ffmpeg arguments on Linux versus Windows, credentials off the command line, closing the terminal window, children dying with the supervisor, the open-segment rule, atomic status-file replacement, driving the pruner as a dry run) that preceded the build are kept on the branch `spikes`, no longer on `main`: [reports](https://github.com/karadimnik/holocron/tree/spikes/tasks/holocron/spikes) and [programs](https://github.com/karadimnik/holocron/tree/spikes/spikes).

## License

[PolyForm Noncommercial License 1.0.0](LICENSE) (SPDX: `PolyForm-Noncommercial-1.0.0`). You may use, study, change and share Holocron for any noncommercial purpose - at home, for research, in a school, a charity or a public-interest organisation. Commercial use is not permitted without the author's separate written permission. This is a source-available licence, not an OSI open-source one. Copies obtained before 2026-10-28 from the earlier repository `windows-terminal-IPCCTV-dashboard` stay under the licence they were given under there.
