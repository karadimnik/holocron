# Configuration reference

Holocron reads two files that sit side by side: `holocron.config.json` (this page) and `cameras.json` (the camera list, see [the end of this page](#the-camera-list)). `holocron.config.json` overlays the built-in defaults; any key you leave out keeps its default, and a missing or empty file means all defaults. The default place is the platform's configuration folder (Windows: the folder of `holocron.exe`; Linux: `$XDG_CONFIG_HOME/holocron` or `~/.config/holocron`; macOS: `~/Library/Application Support/holocron`), and `--config FILE` names another one; `cameras.json` is always looked for in the same folder (CONFIRMED: internal/osx/env.go `DefaultDirs`, internal/app/paths.go `Resolve`). The complete key list with every validation rule is [internal/config/KEYS.md](../internal/config/KEYS.md); this page explains what the keys do.

Rules that apply to every key (CONFIRMED: KEYS.md, internal/config/parse.go):

* Key names are case-insensitive; two spellings of one key are an error.
* Types are strict: a JSON string is never accepted for a number or a boolean. `null` is accepted only for `MaxRetentionDays`.
* An unknown key is a warning, except a key starting with `Live`, which is an error (a misspelled `LiveViewEnabled` would otherwise leave live view off silently).
* A few keys that only the old viewer read (the `Health*`, `Grid*`, `VideoAspect*` keys, `RelayReadyPollMs`, `ViewerPromoteCheckSeconds`) are accepted and ignored without a warning, so an old file still loads.

**Changing a key.** `holocron config get <key>`, `config set <key> <value>`, `config unset <key>` (back to the default), `config show` (the effective configuration, secrets masked) and `config validate` read and edit the file in place: validated first, written atomically, every other key and the file's byte-order mark (if it has one) kept. `config set` accepts only the editable keys, marked **editable** below; the others you change by hand. A running supervisor keeps the values it started with and logs once that the file changed: restart it (`holocron restart`) to apply a key. The exceptions are `MoveToDayFolders` (applied at the next reconcile) and `LiveViewEnabled` (`holocron live enable|disable` applies it at once) (CONFIRMED: internal/supervise/loop.go `reconcile`, internal/app/configkeys.go).

**Four keys are also editable from the web app's Settings screen** - `SegmentSeconds`, `ReserveGB`, `PruneHysteresisGB` and `MaxRetentionDays`, marked **UI** below. The Admin tab edits every editable key (CONFIRMED: internal/web/settings.go `editableConfigKeys`, internal/app/configkeys.go `editableRules`). Values written by the web app go through the same code as `holocron config set`.

## Paths and recording

| Key | Default | Meaning |
| --- | --- | --- |
| `RootPath` | `E:\cameras` (Linux `/srv/cameras`, macOS `~/Movies/cameras`) | Where footage, `_state` and `_logs` live. Footage is `<Camera>\` (ffmpeg writes here: the open segment and the newest closed one) plus `<Camera>\YYYY-MM-DD\` (filed days; see [how it works](how-it-works.md#recording)). Must not contain `%`. `--root DIR` overrides it for one command. |
| `FfmpegPath` / `FfprobePath` | `C:\ffmpeg\bin\ffmpeg.exe` / `...\ffprobe.exe` (elsewhere `ffmpeg` / `ffprobe` from PATH) | The recording tools. ffmpeg 5.0 or newer is required; an older or missing one stops the supervisor with exit 21 (CONFIRMED: internal/ffmpeg/version.go, internal/supervise/README.md). |
| `SegmentSeconds` **editable, UI** | `900` | Segment length in seconds, aligned to the clock. |
| `Container` | `mkv` | Which entry of `ContainerProfiles` to use. `fmp4` re-encodes audio to AAC (`-c:a aac -b:a 64k`). |
| `ContainerProfiles` | `mkv` and `fmp4` | The profiles themselves: per profile an `Extension`, a `SegmentFormat`, `SegmentFormatOptions`, and `AudioArgs` / `ExtraArgs` added to the ffmpeg command line. The file's map **replaces** the defaults, so a file that sets it must list every profile it uses. The extension decides what the pruner may delete: `.` plus 1 to 8 letters or digits. |
| `RtspTransport` | `tcp` | Transport for the recorder's inputs: `tcp`, `udp`, `udp_multicast`, `http` or `https`. |
| `TimeoutMicroseconds` | `10000000` | ffmpeg's socket timeout (`-timeout`). |

## Retention

| Key | Default | Meaning |
| --- | --- | --- |
| `ReserveGB` **editable, UI** | `20` | Free space the pruner defends. |
| `PruneHysteresisGB` **editable, UI** | `5` | How much extra it frees once it starts, so it does not thrash. |
| `PruneIntervalSeconds` | `300` | How often it looks (and once at start). |
| `MaxRetentionDays` **editable, UI** | `null` | Optional age cap, on top of the space rule. `null` (or `holocron config unset`) switches it off; a number must be greater than 0. |
| `MaxAgePruneFraction` **editable** | `0.5` | Circuit breaker on `MaxRetentionDays`. A run that would delete more than this share of the archive by age - by segment count or by bytes - deletes nothing by age, logs one `PRUNE AGE RULE HELD` line with both shares and alerts once (id 122), so a mistyped retention cannot empty the archive. Lower `MaxRetentionDays` in steps to shorten retention a long way. Must be from `0.01` to `1.0`; `1.0` switches the brake off. Never holds back the free-space rule: a disk below `ReserveGB` keeps pruning. |
| `MoveToDayFolders` | `true` | The mover (decision D20): every closed segment is filed from the camera folder into `<camera>\<YYYY-MM-DD>\` (the date in its name); the camera folder keeps the segment being written and the newest closed one. `false` stops filing at the next reconcile (no restart); filed days stay and are still shown and pruned. To put the archive back flat (a rollback step), use `holocron archive flatten` ([rollback](rollback.md#r2a-put-the-archive-back-flat)). See [how it works](how-it-works.md#the-mover). |
| `MoveIntervalSeconds` | `60` | How often the mover looks. At least `10`. |
| `MoveMarginSeconds` | `120` | A segment written less than this long ago is never filed. At least `30`. The default of `holocron archive migrate --margin`. |
| `MoveMaxPerCycle` | `200` | Files filed per camera per cycle, so a first run over a large flat archive spreads its disk work (also the size of each cycle of `holocron archive migrate --now`). At least `1`. |

Retention is the free-space watermark (`ReserveGB`), not a number of days, and the day-folder layout does not change it: the pruner counts the camera folder and every day folder together. The four `Move*` keys are the day-folder mover's ([KEYS.md](../internal/config/KEYS.md)).

## Supervisor

Every key here is a tuning constant rather than a setting: the defaults are the values this system was measured on, and a wrong one will degrade a working supervisor rather than configure it. Only the three marked **editable** can be set with `holocron config set`.

| Key | Default | Meaning |
| --- | --- | --- |
| `ReconcileSeconds` | `5` | How quickly `holocron cam enable` or an edited address is picked up. |
| `StatusIntervalSeconds` | `5` | How often `status.json` is rewritten (the heartbeat). The supervisor counts as not running when the heartbeat is older than max(3 x this, 15 s) (CONFIRMED: internal/status/verdict.go). |
| `ArchiveScanSeconds` | `60` | How often the whole archive is measured for the disk figures. Separate because it touches every file. |
| `StopTimeoutSeconds` **editable** | `30` | How long the supervisor's ordered stop (`holocron down`) waits for its children. |
| `BackoffStartSeconds` / `BackoffMaxSeconds` | `3` / `60` | Restart backoff for a failing child (3, 6, 12, 24, 48, 60 s). |
| `HealthyResetSeconds` | `300` | How long a child must stay up before its backoff resets. |
| `StallFactor` | `2.5` | Multiplied by `SegmentSeconds` for the stall watchdog. Must be greater than 1. |
| `CameraDownAlertMinutes` **editable** | `15` | How long a camera may make no file progress before it raises an alert (id 110), at least `SegmentSeconds + StatusIntervalSeconds + 30` seconds - 935 s (15.6 min) with the defaults - because a healthy camera is a whole segment past its last progress at every boundary, plus the keyframe wait and a status tick. A lower value is raised to that and logged once at start. |
| `CameraDownRepeatHours` **editable** | `12` | How long before the same camera alerts again while still down. |
| `LogMaxBytes` | `2097152` (2 MB) | Log rotation threshold of `holocron.log` (`audit.log` rotates at four times this). |
| `LogRepeatSuppressSeconds` | `60` | Read and validated (a number of 0 or more); its effect on the log is UNVERIFIED (no production use found outside internal/config). |
| `StatusEventCount` | `20` | How many recent log lines `status.json` publishes under `Events`. |

## Relay

The paths and addresses here are configuration; `RelayStartupWaitSeconds`, `RelayReadyWaitSeconds` and `StartStaggerMs` are tuning constants, set from measurement, and a wrong one will degrade a working relay rather than configure it.

| Key | Default | Meaning |
| --- | --- | --- |
| `RestreamerEnabled` | `true` | Set false to run on direct camera inputs only. |
| `RelayExternal` | `false` | Record from a relay Holocron does not run, at `RelayBase`: no MediaMTX is started, stopped or configured and no relay or live login is read; live view is not started. Needs `RestreamerEnabled` false. This exists for the side-by-side shadow run ([shadow.md](shadow.md)). |
| `RestreamerExePath` | `C:\mediamtx\mediamtx.exe` (elsewhere `mediamtx` from PATH) | The binary. |
| `RestreamerConfigPath` | `C:\mediamtx\mediamtx.yml` (elsewhere `mediamtx.yml` in the configuration folder) | The generated config, rewritten by the supervisor at every relay start when it changed. Keep it on a local drive: MediaMTX watches it with `ReadDirectoryChanges`, which the WSL file share does not implement, and exits at once if it cannot (documented-only: the header comment of the generated file, measured on MediaMTX v1.21.2). |
| `RelayBase` | `rtsp://127.0.0.1:8554` | Where consumers read. Loopback only; the generated config allows anonymous reading precisely because nothing off the machine can reach it. |
| `RelayApiBase` | `http://127.0.0.1:9997` | The control API the readiness probe uses. |
| `RelayProbeTimeoutMs` | `1500` | Budget for one probe. A value below 100 is raised to 100 with a warning. |
| `RelayStartupWaitSeconds` | `20` | How long the supervisor waits for the relay's control API to answer before starting any recorder. The API only, not every path: one offline camera's path can never become ready and would otherwise hold up the healthy ones. |
| `RelayReadyWaitSeconds` | `15` | How long **one** consumer waits for **its own** relay path before giving up and taking a direct camera slot. Spent only after the relay has just started; a later attempt decides on the spot, so a relay that died is left behind immediately. A value below 0 is raised to 0. |
| `StartStaggerMs` | `300` | Delay between consecutive recorder starts, so six of them do not burst onto the relay, the control API and the cameras in the same instant. |

The polling interval of the readiness wait is fixed at 500 ms (internal/relay/README.md).

## Live view

Off by default. With `LiveViewEnabled` false nothing here is read, started or written, and `status.json` and the relay's generated config are exactly what they would be without it. It is the server half of watching the cameras in a browser (the Live tab of the web app, see [ui.md](ui.md)): a **second** MediaMTX, dedicated to WebRTC, supervised exactly like the relay and stopped with it. The recording relay never speaks WebRTC, because MediaMTX's WebRTC port answers any website's cross-origin request when its origins are the default and has no `Host` check, and a user allowed to read RTSP may read WebRTC too. The design and the reasoning behind each generated key are in [tasks/holocron/live-1a-design.md](../tasks/holocron/live-1a-design.md). `holocron live enable|disable|status` switches it and shows its state.

| Key | Default | Meaning |
| --- | --- | --- |
| `LiveViewEnabled` **editable** | `false` | Starts the live instance under the supervisor, once the relay is running. It needs `RestreamerEnabled` true and `RelayExternal` false; otherwise the supervisor logs why and does not start it. A failed start logs one redacted line and recording carries on. |
| `LiveExePath` | `""` | The MediaMTX binary. Empty means `RestreamerExePath`. |
| `LiveConfigPath` | `C:\mediamtx\mediamtx-live.yml` (elsewhere in the configuration folder) | The generated config, rewritten by the supervisor at every start. A network path (starting `\\`) is refused at load. |
| `LiveWhepAddress` | `127.0.0.1:8889` | Where WebRTC signaling listens. Loopback only: any other host is refused at load. |
| `LiveUdpAddress` **editable** | `127.0.0.1:8189` | The WebRTC media port (UDP). `127.0.0.1:<port>` (loopback only), `0.0.0.0:<port>` or one private IPv4 of this PC such as `192.168.1.50:8189`. Never a name, IPv6 or a public address. |
| `LiveAdvertiseHosts` **editable** | `[]` | Private IPv4 literals (at most 4) browsers are told to send media to, in addition to `127.0.0.1`, e.g. `["192.168.1.50"]`. Empty (the default) keeps the generated live config as it was. A non-loopback `LiveUdpAddress` needs at least one entry and a non-empty list needs a non-loopback `LiveUdpAddress`; with a single bound address the list may name only that address. |
| `LiveSubStream` **editable** | `stream2` | The camera stream the live grid pulls, on demand. 1 to 32 letters, digits, `_` or `-`. |
| `LiveCloseAfterSeconds` **editable** | `10` | How long after the last viewer leaves the camera's session is closed. 1 to 3600. |
| `LiveSecretPath` | `""` | The login's secret. Empty means `live_view_secret.txt` in the auth folder (Windows `%LOCALAPPDATA%\IPCCTV\camui\auth`; Linux `$XDG_STATE_HOME/holocron/auth` or `~/.local/state/holocron/auth`; macOS `~/Library/Application Support/holocron/auth`: internal/osx/env.go): a local per-user folder. `%NAME%` is replaced from the environment, and the supervisor and the web app resolve it by the same rule. |

A malformed value, or any key starting with `Live` that is not in this table, is refused when the configuration loads, whether or not live view is on.

- **Paths.** For each camera `<name>sub` pulls the camera's `LiveSubStream` only while someone watches (its second camera session), and `<name>main` pulls the recording relay's own loopback path, which adds no camera session. A camera whose recorder `Quality` already is `LiveSubStream` gets only `<name>main`. A live name that equals a relay path is an error (CONFIRMED: internal/relay/names.go `LivePaths`).
- **LAN media (off by default).** Signaling stays on loopback (`LiveWhepAddress` is always `127.0.0.1`; browsers reach it through the web app's proxy, never directly). Only the media can be offered on a LAN address. In MediaMTX v1.21.2 the host candidates in the answer come from `webrtcAdditionalHosts` (here `127.0.0.1` plus `LiveAdvertiseHosts`), each paired with the port of the UDP listener, because `webrtcIPsFromInterfaces` is off (CONFIRMED for the generated keys: internal/relay/liveconfig.go; the candidate behaviour of MediaMTX is documented-only). The listener binds `LiveUdpAddress`; an advertised address is reachable only if that bind covers it, hence `0.0.0.0` (all interfaces) or the one LAN address. Example for a PC at `192.168.1.50`: `"LiveUdpAddress": "0.0.0.0:8189", "LiveAdvertiseHosts": ["192.168.1.50"]`, then `holocron restart`. A browser on this PC still works through the loopback candidate; one that ignores loopback candidates uses the LAN one (that Firefox does is a hypothesis recorded in [ui.md](ui.md): UNVERIFIED here). Listening beyond loopback means a Windows Firewall rule is needed for OTHER devices (see [ui.md](ui.md)). Nothing is created for you.
- **The Live tab's hidden-window grace period (not a `holocron.config.json` key).** How long the page keeps its streams after its window is hidden is a web-app setting: `liveHiddenGraceSeconds` in the JSON file given to `holocron web -config FILE`, or the environment variable `CAMUI_LIVE_HIDDEN_GRACE`, which wins. The web app that `holocron up` starts is launched without a `-config` file (internal/app/web.go), so under `up` only the environment variable applies, and the web child inherits the environment of the `holocron up` process (CONFIRMED: internal/host/spawner.go, `Env` nil means this process's environment). Restart to change it. Values are whole seconds, capped at 86400 (CONFIRMED: internal/web/config.go):

  | `liveHiddenGraceSeconds` | Meaning |
  |---|---|
  | `300` (default) | A minimized or covered window, or another tab, keeps its streams for 5 minutes, then frees them. Coming back inside that time shows the pictures again with no reconnect. |
  | `60` | Frees after one minute: the cameras' second session and the CPU are released sooner. |
  | `0` | Frees at once; every return reconnects. |
  | `-1` | Never frees: the wall keeps decoding while hidden (the CPU figures are measurements in [ui.md](ui.md)). |

  Example file for `holocron web -config FILE`: `{ "liveHiddenGraceSeconds": 60 }`. Details: [ui.md](ui.md).
- **Login.** One user, `camlive`, with `read` only; there is no anonymous user. The secret is 32 random bytes (64 hex characters) in a plain ASCII file the supervisor creates when it is missing, restricted to the current OS user, so no other account can read it; the web app reads the same file. The generated config holds only its SHA-256 (`pass: sha256:...`). The secret is on no command line and in no log or `status.json`. A web page cannot learn it, and the web app adds the login server-side.
- **No cross-origin permission.** `webrtcAllowOrigins` is empty, so MediaMTX sends no `Access-Control-Allow-Origin`, and a browser POST that is not same-origin is refused even with a correct login. The browser therefore reaches the live instance only through a same-origin proxy such as the web app. Every other MediaMTX server (RTSP, RTMP, HLS, SRT, API, metrics, pprof, playback) is off (CONFIRMED: internal/relay/liveconfig.go).
- **Status.** While live view is on, `status.json` has a `Live` section (`Enabled`, `Alive`, `Pid`, `LastPid`, `StartedAt`, `RestartsTotal`, `Paths` and `Cameras` as names only, `LastError`, `LastErrorAt`) and `holocron status` / `holocron live status` show it. With it off the section does not exist (CONFIRMED: internal/status/types.go). Its log is `liveview.log` in the archive's `_logs` folder. Cameras added to `cameras.json` while it runs appear at its next start (the live config is rendered at each start: internal/supervise/mtx.go `launch`).
- **Camera sessions.** The cameras allow few simultaneous RTSP sessions (the code treats it as two: internal/supervise/README.md, `holocron doctor`; the number itself is a property of the cameras and UNVERIFIED in code). The recorder holds one through the relay; live view may hold the second, only while a `<name>sub` viewer is connected. While anyone watches a sub-stream, the phone app may have no free session on that camera.

## Logging, alerts, secrets and the web app

These four groups are objects in `holocron.config.json`; their sub-keys are camelCase and an unknown sub-key is a warning. All of them are editable with `holocron config set` (for example `holocron config set logging.levels.web debug`), except the web addresses and folders, which you set by hand.

| Key | Default | Meaning |
| --- | --- | --- |
| `logging.level` | `info` | The default log level: error, warn, info, debug or trace. `HOLOCRON_LOG`, `-v` and `-vv` override it for one run; `holocron log level` changes it in a running supervisor. |
| `logging.levels` | `{}` | Per-component levels, e.g. `holocron config set logging.levels.supervisor debug`. |
| `alerts.*` | see KEYS.md | Channels and delivery of the alerts: `queueSize`, `timeoutSeconds`, `minRepeatSeconds`, `retryBackoffSeconds`, `webhook.*`, `push.*`, `desktop.enabled`, `eventlog.enabled`. What each channel does: [alerts.md](alerts.md). |
| `secrets.backend` | `auto` | Where the camera login is stored: `auto` (Windows: the Credential Manager when it answers, else the file; Linux and macOS: the file), `keyring`, `file` or `env` (`HOLOCRON_CAMERA_USER` and `HOLOCRON_CAMERA_PASSWORD`) (CONFIRMED: internal/secrets/open.go, internal/app/credentials.go). Set the login with `holocron secret set camera.user` and `camera.password`. |
| `secrets.file`, `secrets.keyFile` | `""` | The file backend's encrypted store and its owner-only key file; empty means `secrets.enc` and `secrets.key` in the auth folder (the key may come from `HOLOCRON_SECRET_KEY` instead). Changing one does not move a stored login. |
| `web.enabled` | `true` | `holocron up` starts the web app (false: recorder only). |
| `web.listen` | `127.0.0.1:8787` | The web app's loopback listener. A non-loopback address needs `CAMUI_TOKEN` of 16 or more characters. |
| `web.tlsListen` | `""` | Empty (off) or `IPv4:port`: the LAN HTTPS listener with login. The web app requires a private address and at least one user. |
| `web.tlsDir`, `web.authDir` | `""` | Folders of the local CA and certificate, and of `users.json` and `sessions.json`; empty means the web app's default folders (never inside the repository). |

## The camera list

`cameras.json` is a separate file with its own schema - see [KEYS.md](../internal/config/KEYS.md#camerasjson). Edit it with `holocron cam add|set|remove|enable|disable` (validated, written atomically); the web app's Settings screen edits it too: add, remove, rename, change the IP, the stream and the record flag. The supervisor picks up a change within `ReconcileSeconds`.
