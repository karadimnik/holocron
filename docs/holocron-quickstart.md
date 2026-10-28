# Holocron quickstart

Four steps from nothing to a recording NVR. Each step has a block for Linux and macOS (bash or zsh) and one for Windows (Windows PowerShell 5.1, not elevated): run the one for your system. A line starting `#` under a command is the result to expect. You need ffmpeg and ffprobe 5.0 or later, MediaMTX and cameras that answer RTSP ([requirements](../README.md#requirements)). Replace the placeholder camera (`Front Door`, `192.168.1.50`) and archive folder with yours.

Windows is in production. Linux is built and tested in CI; its systemd user unit was exercised in WSL2 only. macOS is tested in CI only (report-only), and its launchd agent is untested on a Mac ([supported platforms](../README.md#supported-platforms)).

## 1. Get the program, into a permanent folder

Download and verify the archive for your system as in [Download a release](../README.md#download-a-release), or [build from source](../README.md#build-from-source). Then, from the folder you unpacked it in:

```bash
mkdir -p ~/.local/bin && install -m 0755 holocron ~/.local/bin/holocron    # put ~/.local/bin on PATH if it is not
holocron version
# holocron <version> (status.json schema 1, linux/amd64, go<n>)            macOS: darwin/arm64
```

```powershell
New-Item -ItemType Directory -Force C:\Holocron | Out-Null
Copy-Item .\holocron\holocron.exe C:\Holocron\        # after a build: .\holocron.exe
Set-Alias holocron C:\Holocron\holocron.exe           # this window only; elsewhere use C:\Holocron\holocron.exe
holocron version
# holocron <version> (status.json schema 1, windows/amd64, go<n>)
```

`<version>` is the release tag, or `dev` for a plain `go build`. Not in Downloads or a temporary folder: `holocron install` refuses an executable under a temporary folder, and Downloads is easily cleaned out. Gatekeeper may refuse the unsigned macOS binary and SmartScreen may warn about `holocron.exe` ([release limitations](releasing.md#limitations)).

## 2. The two configuration files

The configuration folder is `~/.config/holocron` on Linux, `~/Library/Application Support/holocron` on macOS and the folder of `holocron.exe` on Windows.

```bash
CONF="${XDG_CONFIG_HOME:-$HOME/.config}/holocron"     # macOS: CONF="$HOME/Library/Application Support/holocron"
mkdir -p "$CONF"
printf '{ "RootPath": "%s/cameras" }\n' "$HOME" > "$CONF/holocron.config.json"
printf '[ { "Name": "Front Door", "IP": "192.168.1.50", "Quality": "stream1", "Record": true } ]\n' > "$CONF/cameras.json"
holocron config validate
# ok   /home/<you>/.config/holocron/holocron.config.json
# ok   /home/<you>/.config/holocron/cameras.json (1 camera(s))
```

```powershell
Set-Content C:\Holocron\holocron.config.json '{ "RootPath": "C:\\cameras" }'
Set-Content C:\Holocron\cameras.json '[ { "Name": "Front Door", "IP": "192.168.1.50", "Quality": "stream1", "Record": true } ]'
holocron config validate
# ok   C:\Holocron\holocron.config.json
# ok   C:\Holocron\cameras.json (1 camera(s))
```

`RootPath` is the archive folder; every key has a default ([KEYS.md](../internal/config/KEYS.md)). ffmpeg, ffprobe and MediaMTX are found in `C:\ffmpeg\bin` and `C:\mediamtx\mediamtx.exe` on Windows and on `PATH` elsewhere; add `FfmpegPath`, `FfprobePath` and `RestreamerExePath` only if they are not there. One entry per camera: `Name`, `IP` (or `host:port`), `Quality` (`stream1` main, `stream2` sub) and `Record` (`true` to record).

## 3. The camera login, then the check

```bash
holocron secret set camera.user                       # type the value, Enter; nothing is shown
# stored camera.user (file store)
holocron secret set camera.password
# stored camera.password (file store)
holocron doctor
# no line starts with "fail"; the last line is NOT RUNNING (<n> warning(s)); exit code 0
```

```powershell
holocron secret set camera.user                       # type the value, Enter; nothing is shown
# stored camera.user (keyring store)
holocron secret set camera.password
# stored camera.password (keyring store)
holocron doctor
# no line starts with "fail"; the last line is NOT RUNNING (<n> warning(s)); exit code 0
```

Windows keeps the login in the Credential Manager (the `keyring store`), Linux and macOS in an encrypted file with an owner-only key (the `file store`; [configuration](configuration.md)). A `fail` line names its own fix; `warn` lines about a root not claimed yet, a missing status file or nothing running are normal before the first start. On Windows, if the old PowerShell system's login is still in its `camera_creds.xml`, `holocron secret import --from C:\old\camera_creds.xml` prints `imported camera.user and camera.password` and leaves the old file untouched.

## 4. Start, look, start at login

```bash
holocron up --detach
# RECORDING: supervisor pid <n> is running after <x>s; 1 camera(s) set to record (holocron status shows each)
holocron open
# opens http://127.0.0.1:8787/#live in the browser (holocron open --print only prints the address)
holocron install
# installed and verified: systemd user unit holocron, enabled, ..., runs ...     macOS: launchd agent
```

```powershell
holocron up --detach
# RECORDING: supervisor pid <n> is running after <x>s; 1 camera(s) set to record (holocron status shows each)
holocron open
# opens http://127.0.0.1:8787/#live in the browser (holocron open --print only prints the address)
holocron install
# installed and verified: logon task Holocron, enabled, console window visible, runs ...
```

`holocron install` starts nothing now; `holocron install --dry-run` prints what it would create and changes nothing. On Windows, with the visible window (default), closing that window at logon stops everything; `holocron install --window hidden` has no window. Stop it any time with `holocron down` (`stopped: 0 survivors after <x>s`). Autostart in depth: [autostart.md](autostart.md).

## Then

- **Phone or second PC over HTTPS**: add `"web": { "tlsListen": "192.168.1.50:8443" }` to the config, `holocron user add anna` (password on standard input, 12 characters or more), `holocron tls ca` (the certificate to install on the device), allow the port in the firewall, `holocron restart --detach`. Details: [ui.md](ui.md). Viewers: `holocron user add bob --role viewer`.
- **Every command, file, port, exit code and alert on one page**: [holocron-cheatsheet.md](holocron-cheatsheet.md); everything else starts at the [README](../README.md); the complete reference for AI agents: [ai/holocron-for-agents.md](ai/holocron-for-agents.md).
- **Problems and limits**: [troubleshooting.md](troubleshooting.md), [limitations.md](limitations.md).
- **The old PowerShell system** (Windows): the move to Holocron is history (record: [cutover-runbook.md](../tasks/holocron/cutover-runbook.md)). The way back is [rollback.md](rollback.md); the old system itself is in the original repository `windows-terminal-IPCCTV-dashboard`, branch `master` (going back includes `holocron archive flatten --now`: Holocron files closed segments into day folders that the old system does not read).
