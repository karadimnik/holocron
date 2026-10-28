# Holocron

[![CI](https://github.com/karadimnik/holocron/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/karadimnik/holocron/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/karadimnik/holocron)](https://github.com/karadimnik/holocron/releases/latest)
[![License: PolyForm Noncommercial 1.0.0](https://img.shields.io/badge/license-PolyForm%20Noncommercial%201.0.0-blue)](LICENSE)
[![Go version from go.mod](https://img.shields.io/github/go-mod/go-version/karadimnik/holocron)](go.mod)

Holocron turns your PC into a 24/7 home security recorder. One small program records every IP camera with sound, heals itself when a stream drops, files footage by camera and day, frees space on its own, and alerts you when something is wrong. Watch live or replay clips from any browser or phone. Free for noncommercial use.

[Features](#features) - [Platforms](#supported-platforms) - [Install](#install) - [Quick start](#quick-start) - [Documentation](#documentation) - [Releases](#releases-and-versioning) - [License](#license)

## Features

* **Recording with audio** - `ffmpeg` stream-copies each camera's video and audio into fixed-length segments (15 minutes by default), with no re-encoding.
* **Self-healing** - a supervisor restarts a recorder that exits or stalls, with backoff; a relay (MediaMTX) holds one session per camera, shared by the recorder and the live view.
* **A web app** - Dashboard, Live (WebRTC: sub-streams in the grid, the main stream on focus), Footage by camera and day, Settings, and an Admin tab with the health panel and users. Two roles: admin and viewer. Phones and other PCs connect over an optional HTTPS listener with its own local certificate authority (`holocron tls ca`).
* **Retention by free space** - the pruner deletes the oldest footage to keep a free-space watermark (`ReserveGB`).
* **A tidy archive** - closed segments are filed into one folder per camera and day, `<Camera>/YYYY-MM-DD/`.
* **Alerts** - desktop, the system log (Windows event log, syslog), the log file, a push channel (ntfy-style) and a webhook; `holocron alert test` proves each one.
* **`holocron doctor`** - a read-only health check, grouped (setup, recording, connections, live, web, storage, system), with a fix for every problem.
* **One executable** for every role and command: `holocron` (`holocron.exe` on Windows).

## Supported platforms

| OS | Release archives | Status | Start at login (`holocron install`) |
| --- | --- | --- | --- |
| Windows | amd64 | **In production**: records 24/7 since 22 Oct 2026. Tested in CI on `windows-latest`. | logon task (default); Windows service mode tested only against a fake service manager |
| Linux | amd64, arm64 | Built and tested in CI on `ubuntu-latest`, including the real-ffmpeg and real-process suites. Not yet run in production. | systemd user unit, exercised in WSL2 only |
| macOS | arm64 (Apple silicon) | Built and tested in CI on `macos-latest`, report-only (that job cannot fail a run). Not used outside CI. | launchd agent, untested on a Mac |

The arm64 Linux build is cross-compiled; its tests run on amd64. Every known limit, with its source: [docs/limitations.md](docs/limitations.md); CI: [docs/ci.md](docs/ci.md); autostart per OS: [docs/autostart.md](docs/autostart.md).

## Requirements

| What | Version | Notes |
| --- | --- | --- |
| `ffmpeg` and `ffprobe` | 5.0 or later (older is refused) | Windows default `C:\ffmpeg\bin\`, elsewhere from `PATH`; `FfmpegPath` and `FfprobePath` override |
| [MediaMTX](https://github.com/bluenviron/mediamtx) | no minimum enforced; measured on v1.21.2 | Windows default `C:\mediamtx\mediamtx.exe`, elsewhere `mediamtx` from `PATH`; `RestreamerExePath` overrides |
| IP cameras | RTSP | one entry per camera in `cameras.json` |
| A browser | WebRTC | for the live view; Firefox needs a LAN media address ([limitations](docs/limitations.md)) |
| Go | the version in [go.mod](go.mod) (1.27) | only to build from source |

## Install

### Download a release

Get the archive for your platform from the [latest release](https://github.com/karadimnik/holocron/releases/latest). `<version>` is the tag without its `v` (tag `v1.0.2`, file `holocron_1.0.2_linux_amd64.tar.gz`).

| Platform | Archive |
| --- | --- |
| Windows x64 | `holocron_<version>_windows_amd64.zip` |
| Linux x64 | `holocron_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `holocron_<version>_linux_arm64.tar.gz` |
| macOS Apple silicon | `holocron_<version>_darwin_arm64.tar.gz` |

Each archive holds the executable, `LICENSE`, `README.md` and `docs/holocron-quickstart.md`, and has an SPDX SBOM beside it (`<archive>.sbom.json`); `checksums.txt` lists the SHA-256 of every file. Download, verify and unpack:

```bash
# Linux and macOS
VERSION=1.0.2                                   # the version you want
FILE=holocron_${VERSION}_linux_amd64.tar.gz     # or linux_arm64, darwin_arm64
BASE=https://github.com/karadimnik/holocron/releases/download/v$VERSION
curl -fsSL -O "$BASE/$FILE" -O "$BASE/checksums.txt"
grep "  $FILE\$" checksums.txt | sha256sum -c    # macOS: shasum -a 256 -c
tar -xzf "$FILE"
./holocron version
```

```powershell
# Windows PowerShell
$Version = "1.0.2"
$File = "holocron_${Version}_windows_amd64.zip"
$Base = "https://github.com/karadimnik/holocron/releases/download/v$Version"
Invoke-WebRequest "$Base/$File" -OutFile $File
Invoke-WebRequest "$Base/checksums.txt" -OutFile checksums.txt
$Want = (Get-Content checksums.txt | Where-Object { $_.EndsWith("  $File") }).Split(" ")[0]
if ((Get-FileHash $File -Algorithm SHA256).Hash -ne $Want) { throw "checksum mismatch: $File" }
Expand-Archive $File -DestinationPath .\holocron
```

The stronger check is the build provenance attestation, made by the release workflow and verified with the GitHub CLI: `gh attestation verify <archive> --repo karadimnik/holocron` ([details](docs/releasing.md#verify-a-download)). The Windows and macOS binaries are not code-signed, so SmartScreen may warn when `holocron.exe` is first run from a download and Gatekeeper may refuse to open the macOS binary ([release limitations](docs/releasing.md#limitations)).

### Build from source

```bash
git clone https://github.com/karadimnik/holocron.git && cd holocron
go build ./cmd/holocron                             # this OS; no third-party dependencies
GOOS=linux GOARCH=arm64 go build ./cmd/holocron     # cross-compile any released target
./build-holocron.sh                                 # developer build: gofmt, vet and tests, then holocron.exe (windows/amd64)
```

More: [building and testing](docs/development.md).

## Quick start

Four steps: a permanent folder, two configuration files, the camera login, then start. The configuration folder is `~/.config/holocron` on Linux, `~/Library/Application Support/holocron` on macOS and the folder of `holocron.exe` on Windows. Replace the placeholder camera (`Front Door`, `192.168.1.50`) and archive folder with yours.

```bash
# Linux and macOS, from the folder you unpacked
mkdir -p ~/.local/bin && install -m 0755 holocron ~/.local/bin/holocron    # put ~/.local/bin on PATH if it is not
CONF="${XDG_CONFIG_HOME:-$HOME/.config}/holocron"                         # macOS: CONF="$HOME/Library/Application Support/holocron"
mkdir -p "$CONF"
printf '{ "RootPath": "%s/cameras" }\n' "$HOME" > "$CONF/holocron.config.json"
printf '[ { "Name": "Front Door", "IP": "192.168.1.50", "Quality": "stream1", "Record": true } ]\n' > "$CONF/cameras.json"
holocron config validate
holocron secret set camera.user                    # type the value, Enter; nothing is shown
holocron secret set camera.password
holocron doctor
holocron up --detach
holocron open
holocron install                                   # systemd user unit; launchd agent on macOS
```

```powershell
# Windows PowerShell, from the folder you unpacked
New-Item -ItemType Directory -Force C:\Holocron | Out-Null
Copy-Item .\holocron\holocron.exe C:\Holocron\
Set-Alias holocron C:\Holocron\holocron.exe          # this window only
Set-Content C:\Holocron\holocron.config.json '{ "RootPath": "C:\\cameras" }'
Set-Content C:\Holocron\cameras.json '[ { "Name": "Front Door", "IP": "192.168.1.50", "Quality": "stream1", "Record": true } ]'
holocron config validate
holocron secret set camera.user                    # type the value, Enter; nothing is shown
holocron secret set camera.password
holocron doctor
holocron up --detach
holocron open
holocron install                                   # logon task Holocron
```

`holocron doctor` must report no `fail` line before the first start; `holocron open` shows the Live grid at `http://127.0.0.1:8787/`; `holocron install` refuses an executable under a temporary folder. The walk-through with the expected output of each step is [the quickstart](docs/holocron-quickstart.md).

## Common commands

| Command | What it does |
| --- | --- |
| `holocron status` | roles, pids, cameras, disk, prune, live view and alerts; exit 0 when running |
| `holocron doctor` | the health check: ok, warn and fail lines with a fix for each problem |
| `holocron logs -f` | read and follow `holocron.log` (filter with `--component`, `--level`, `--since`) |
| `holocron cam add --name "Front Door" --ip 192.168.1.50 --record` | add a camera (validated; the login is a secret, never an argument) |
| `holocron archive stats` | per camera: segments, size, oldest and newest; GB per day, free space, projected days |
| `holocron alert test` | send a test alert through every channel |
| `holocron down` / `holocron up --detach` | stop everything and prove it; start again in the background |

Every command, file, port, exit code and alert: [the cheat sheet](docs/holocron-cheatsheet.md).

## Documentation

* **Guides** - [quickstart](docs/holocron-quickstart.md) and [cheat sheet](docs/holocron-cheatsheet.md).
* **Operating** - [the web app](docs/ui.md), [autostart](docs/autostart.md), [alerts](docs/alerts.md), [configuration](docs/configuration.md) (every key: [KEYS.md](internal/config/KEYS.md)), [troubleshooting](docs/troubleshooting.md), [rollback](docs/rollback.md), [known limitations](docs/limitations.md), [comparing two archives](docs/shadow.md).
* **Development** - [how it works](docs/how-it-works.md), [building, testing and the documentation checks](docs/development.md), [continuous integration](docs/ci.md), [releasing](docs/releasing.md).
* **For AI agents** - [docs/ai/holocron-for-agents.md](docs/ai/holocron-for-agents.md), [AGENTS.md](AGENTS.md) and [llms.txt](llms.txt).
* **Design, decisions and records** - `tasks/holocron/`, including the cutover from the PowerShell system ([runbook](tasks/holocron/cutover-runbook.md), [plan and record](tasks/holocron/cutover-plan.md)) and the shadow run before it ([runbook](tasks/holocron/shadow-runbook.md)).

## Releases and versioning

Holocron follows [Semantic Versioning](https://semver.org/) and lists the changes of each version in [CHANGELOG.md](CHANGELOG.md); every merge to `main` that changes shipped behaviour is released. Published binaries come only from the release workflow (`.github/workflows/release.yml`): pushing a tag `vX.Y.Z` on `main` runs it, and it builds the four archives, their SBOMs, `checksums.txt` and the attestations, and publishes the release with the version's changelog section as its notes. The process and how to verify a download: [docs/releasing.md](docs/releasing.md).

## Security

On the command line, passwords and secrets are read from standard input only, never from an argument, and the camera login is stored in the Windows Credential Manager or an encrypted file with an owner-only key ([configuration](docs/configuration.md)). The web app listens on loopback (`127.0.0.1:8787`) by default; other devices reach it only through the optional HTTPS listener, which requires a sign-in, or a non-loopback address that the web app refuses without an access token ([the web app](docs/ui.md)).

## Stress tests

The live view's design was load-tested on 2026-10-15 (six WebRTC tiles from a loopback MediaMTX, Chrome and Edge 155, i5-6600K with a GTX 1070 Ti): six 2560x1440 streams were smooth with hardware decode (Chrome 64.7% of one core) and unusable without it (150.1%, 14.71% frames dropped); six 640x360 sub-streams were smooth either way (52.9% to 59.3%), and so were five sub-streams plus one 1440p stream on focus, which is why the grid shows sub-streams. Only comparisons between cells are safe; LAN, phone and Firefox viewers were not measured. Setup, every result and the raw data: [tests/stress/live-view/README.md](tests/stress/live-view/README.md).

## Where it came from

Holocron replaced a PowerShell camera wall and recorder on 22 Oct 2026; that system lives complete in the repository `windows-terminal-IPCCTV-dashboard` (branch `master`), where Holocron was built on the branch `portable-nvr` until this repository started at the tag `v1.0.0`. The PowerShell system is the target of the last step of [the rollback](docs/rollback.md). The feasibility spikes SP1 to SP7 that preceded the build are on the branch `spikes`: [reports](https://github.com/karadimnik/holocron/tree/spikes/tasks/holocron/spikes) and [programs](https://github.com/karadimnik/holocron/tree/spikes/spikes).

## License

[PolyForm Noncommercial License 1.0.0](LICENSE) (SPDX: `PolyForm-Noncommercial-1.0.0`), a source-available licence, not an OSI open-source one. You may use, study, change and share Holocron for any noncommercial purpose - at home, for research, in a school, a charity or a public-interest organisation. Commercial use needs the author's separate written permission. Copies obtained before 2026-10-28 from the earlier repository `windows-terminal-IPCCTV-dashboard` stay under the licence they were given under there.
