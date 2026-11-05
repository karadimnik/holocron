# Holocron - checklist

Companion to `plan.md` (design, decisions, the Status board and the gap analysis). Reconciled 2026-11-05 against `CHANGELOG.md`,
`ledger.md` (merges to `1ab7a72`, releases to v1.5.0) and the code on `main`. The first version of this file was written on
2026-10-17 on the branch `portable-nvr`; the P and S numbers are kept so older notes still resolve. The original project's list is
`tasks/ipcctv-recording/todo.md` in the repository `windows-terminal-IPCCTV-dashboard`, branch `master` (no longer in this
repository); this file never edits it. Tick an item only with evidence (a commit, a check run, an Operator confirmation).

Legend: [x] done; [~] done in part; [ ] open; [-] obsolete, dropped; (A) added during the work, not in the first plan; (Op) needs
the Operator's own action or decision. Commit ids before 2026-10-28 refer to the former history (branch `portable-nvr`) and are the
task record in `ledger.md`; released versions are in the Releases table there.

## Operator-reported evidence (2026-11-05)

Stated by the Operator; not something CI or a test in this repository proves.

- Real-network `holocron cam discover` verified (R26, R27; shipped in v1.5.0). Before, only a dry run from WSL2 on the real LAN was recorded.
- The macOS verification pass is done.
- The real-hardware checks are done: the Footage tab on a phone, two viewers against the cameras' 2-session limit, real-camera CPU numbers, and the gap while the relay restarts.
- Holocron is installed and runs flawlessly on a Raspberry Pi 4 with 2 GB of RAM. This is the only run on Linux/ARM and is Operator-reported: CI builds `linux/arm64` but runs its tests on amd64, and no test here ran on that board. Which autostart mechanism the Pi uses was not stated.

## Done

- [x] **P1** Stage 0 spikes: WebRTC in Chrome and Edge, hardware decode, G.711 audio, the MediaMTX cross-origin finding (`tests/stress/live-view/`).
- [x] **P2** Stage 1b (original repo, `master`): mpv password off the command line.
- [x] **P3** Stage 1A: dedicated live-view MediaMTX, login, on-demand sub-streams (`15e4bb3`).
- [x] **P4** Stage 1B: Live tab, same-origin WHEP proxy, session caps, scoped guard exception (`b8749e3`).
- [x] **P5** Stage 2a: tile diagnostics, `LiveAdvertiseHosts`, LAN `LiveUdpAddress`; Firefox verified on the real system (`87871d0`). Firefox on a Mac with a LAN media address: confirmed (`301d029`).
- [x] **P6** Stage 2b: HTTPS listener, local CA with name constraints (`97fd3fe`). The shared token was replaced by the login (P14).
- [x] **P7** Stage 2b.1: HTTPS listener view-only by default (`1e05dd8`).
- [x] **P8** (A) Stage 2c prep: demo `-Tls`; a real phone confirmed by the Operator, 2026-10-17 (`e15dbb4`).
- [x] **P9** (A) Live grid that fills the window, camera modal, fullscreen (`3fa1979`).
- [x] **P10** (A) Live robustness: hidden-window grace, stall detection, recovery (`629bb5e`).
- [x] **P11** (A) Fit / Fill toggle (`436b363`).
- [x] **P12** (A) Alert 110 floor in the PowerShell system (original repository, `master`); that system was replaced on 22 Oct 2026.
- [x] **P13** (A) Docs: grace settings, Firefox needs a non-loopback media address, phone-test steps (`6890b24`).
- [x] **P14** (B) LAN login instead of the token: users, PBKDF2 passwords, sessions, per-address throttle and per-username lockout, `auth.log` (`59237bd`, merged; the branch `portable-nvr-login` is history). Roles and the Admin tab: P33.
- [x] **P21** Footage tab on a phone over the LAN (GAP L6): closed by the Operator, 2026-11-05.
- [x] **P22** Two viewers against the cameras' 2-session limit (L8): closed by the Operator, 2026-11-05.
- [x] **P24** Real-camera CPU numbers: closed by the Operator, 2026-11-05. (The real sub-streams' H.264 profile is in the Stage 0 Spike C table in `plan.md`: three of six cameras are High, and live view plays them.)
- [x] **P30** (L1) Install and autostart: `holocron install` / `uninstall` / autostart as logon task, systemd user unit, launchd agent or Windows service (`3aff33c`, `0ad7f7f`); the Operator's machine runs the installed Holocron since the cutover of 22 Oct 2026 ([cutover-plan.md](cutover-plan.md)). The real Windows service and the launchd agent are not individually evidenced (see `docs/limitations.md`).
- [x] **P35** (L7) Alerts to the phone: webhook and push channels (`06d45fb`, W10; [docs/alerts.md](../../docs/alerts.md)).
- [x] **P40** (OS9) `status.json` as a versioned schema, tested with golden files (`29dd4ef`, W0).
- [x] **P41** (OS3, OS4, D12) Portable camera-secret store: env, encrypted file, OS keyring, `camera_creds.xml` importer (`a94e307`, W11); the recorder reads through the relay (`62278b5`, W6).
- [x] **P42** Stage 4: Go recorder (ffmpeg `e433dbe`, pruner `aa05daf`, supervisor loop `7f16cf1`), shadow run beside the PowerShell recorder (`0e80374`, `ebe5fe5`; result in `docs/shadow.md` and the shadow runbook), cutover on 22 Oct 2026 ([cutover-plan.md](cutover-plan.md)).
- [x] **P43** Stage 5: Go supervisor in attached and service modes (D7): child lifecycle `d1dee7e`, supervisor `7f16cf1`, process lifetime and host `f237a52`.
- [x] **P44** (OS5) Alert channels: webhook, push, desktop notification, event log (`06d45fb`).
- [x] **P45** (OS6) Install and autostart per OS (`3aff33c`, `0ad7f7f`).
- [x] **P46** (OS8) The `camui` control path is gone with the PowerShell system: the web app is part of the `holocron` module and controls through the supervisor's API (`18749a2`, `7878a89`).
- [x] **P47** (OS7) The web app carries the health information: Dashboard (R12 `1d3e191`), doctor in the Admin tab (W19, W20). The mpv grid went with the PowerShell system (`589a72a`).
- [x] **P48** (OS11, OS12, D1, D11) Own repository (D21, `7e2b7e7`), CI on Linux, Windows and macOS (`55d0dbb`, `ccd36bd`), release builds (`5002a66`); v1.0.1 to v1.5.0 published.
- [x] **W9d** (Operator, 2026-10-21) Admin area and the `viewer` role: every management function in the web app, server-side role checks (`22943e7`). Its follow-up, the Phone tab for viewers outside Settings, shipped in W16 (`eeae710`).
- [x] **S1** Single-instance lock: the host takes an instance lock (`f237a52`, W8; `internal/host`); the doctor names a second supervisor.

## Obsolete, dropped

- [-] **P20** A clean run of the FULL PowerShell harness: the harness was removed (`589a72a`); the Go suite runs in CI on every push.
- [-] **P26** The 24-hour tab soak in Stage 1's acceptance: it belonged to the PowerShell-era `camui`; the Operator's daily use since 22 Oct 2026 replaced it. No explicit drop decision is recorded.
- [-] **P28** A `camui` that outlived its closed window: `camui` no longer exists (`7878a89`).

## Open

Each has its evidence for being open.

- [ ] **P23** iPhone and iPad: certificate steps and live video were never run on iOS or iPadOS, whether iOS accepts the name-constrained CA is unverified (`docs/limitations.md` 13, `docs/ui.md`). Android is tested (CHANGELOG 1.3.2). (Op, devices)
- [ ] **P25** A real PC sleep and resume with the Live wall open: not in the Operator's closed list, the figure on record (about 12 s in simulation) comes from the PowerShell era. Uncertain whether Holocron's web app behaves the same; left open.
- [ ] **P27** Housekeeping on the Operator's machine: remove the old phone-test firewall rule `IPCCTV camui HTTPS (TCP 8443)`, clean the old temp and demo folders. Cannot be checked from this repository; left open. (Op)
- [ ] **P31** (L2) Detect a change of the LAN address and regenerate the certificate and the advertised host: no such code found in `internal/web` or `internal/supervise` (only the operator-set `LiveAdvertiseHosts` and `web.remoteHost`).
- [ ] **P32** (L3) "Connect a device" page with a QR code: the Phone tab and `holocron tls ca` give steps, no QR code found in the code.
- [~] **P33** (L4) User management: add, remove, set password, unlock, roles and a password change in the UI exist (W9c `fec055e`, W9d `22943e7`; changing a password or role ends that user's other sessions). Open: a per-device session list with revoke and last seen (no such route found).
- [ ] **P34** (L5, D9) Control from the LAN with a separate credential, or stay view-only. The LAN listener is view-only by default (`tlsReadOnly`). Decision not recorded. (Op decision)
- [ ] **P36** (L9, D8) Hide camera addresses from LAN devices: no such mode found. Decision not recorded. (Op decision)
- [~] **P50** (Stage 3) The internet: opt-in remote access over HTTPS with its own constrained CA and opt-in live video from outside (R13 `0750082`, R14, R15; v1.3.0), an Android phone tested including live video over the internet (CHANGELOG 1.3.2). Not evidenced: the dedicated security review the plan made a gate, and the choice of a private overlay (WireGuard or Tailscale) instead of a port forward; the certificate for the public name is the open decision D25.
- [~] **S2** Continuous census of expected processes with an alert and a health chip: not built. What exists: `holocron down` counts the host and its children by pid, start time and image (never by name), and `holocron doctor --group system` lists stray and orphan processes and a second supervisor. No continuous check, no alert id, no chip.
- [ ] **S3** Reap policy for provable orphans, off by default: `down` stops only what it can prove is Holocron's; no separate opt-in reap setting found.
- [ ] **S4** Live-session ledger and an OS-level check that no camera session lingers: not found in the code.
- [~] **S5** Leak assertion in tests and in every agent task: the repository rule (AGENTS.md rule 1) asks agents to list processes before and after; no automated leak assertion found in the Go tests.
- [~] **S6** S1 is carried into the Go supervisor (above); S2 and S3 are the partial state above.
- [ ] **Real Windows service and launchd** The Windows service install and the launchd agent are not individually evidenced in the ledger; the Operator reports a macOS verification pass but did not say which parts. `docs/limitations.md` 11 keeps them as untested.
