# Changelog

All notable changes to Holocron are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html). How a release is made: [docs/releasing.md](docs/releasing.md).

## Versioning policy

A version is `MAJOR.MINOR.PATCH`, tagged `vMAJOR.MINOR.PATCH`. The public interface that the version number speaks for is:

- **The command line**: the `holocron` subcommands, their flags and arguments, and their exit codes ([cmd/holocron/README.md](cmd/holocron/README.md)).
- **The configuration**: the keys of `holocron.config.json` and `cameras.json`, their types, defaults and rules ([internal/config/KEYS.md](internal/config/KEYS.md)).
- **`status.json`**: its `Schema` number (today 1, `internal/status/status.schema.json`) and the fields that schema defines.
- **The archive layout**: where segments are written and filed, `<root>/<Camera>/` and `<root>/<Camera>/YYYY-MM-DD/` (decision D20), and the segment file names.
- **The HTTP API**: the routes of the web app (`/api/...`) and the supervisor's control API ([internal/web/README.md](internal/web/README.md), [internal/ctlapi/README.md](internal/ctlapi/README.md)).
- **The release archives**: their names and what each one contains.

**MAJOR** for a change that can break a working installation or a client of the above: a removed or renamed command, flag or configuration key, a changed default that changes what is recorded or deleted, a new `status.json` `Schema` number, a change to the archive layout that needs `holocron archive migrate` or a similar step, a removed or incompatibly changed HTTP route, or a renamed or restructured release archive. **MINOR** for an addition that leaves every existing use working: a new command, flag, key, field or route. **PATCH** for a fix that changes no interface. A version with a suffix (`v1.1.0-rc.1`) is a pre-release. Go packages under `internal/` are not a public interface.

## [Unreleased]

### Fixed

- Web app: signing in from a phone failed with "Invalid username or password" when the keyboard appended a space after the suggested user name; white space around a user name sent by the sign-in, setup or Admin form is now ignored (in the lookup, the per-name lockout and `auth.log`), the password is still taken exactly as typed ([docs/ui.md](docs/ui.md#first-run-the-setup-page-and-signing-in)).

## [1.3.0] - 2026-10-29

### Added

- Remote access, off by default: `web.remoteHost` lets the HTTPS login listener answer to one public DNS name; requests from public peer addresses are viewer-only, with a stricter sign-in limit, shorter sessions, HSTS, a `web.remote` doctor check and a `holocron status` line ([docs/ui.md](docs/ui.md#reaching-holocron-from-outside-the-home-network)).
- A trusted certificate for the remote name: with `web.remoteHost` set, a second local CA constrained to exactly that name serves its own server certificate by SNI (the LAN certificate is unchanged), installable only from the home network via the Phone tab or `holocron tls ca --remote`, checked by `holocron doctor`; remote sessions show "viewing only" ([docs/ui.md](docs/ui.md#reaching-holocron-from-outside-the-home-network)).
- Live video from outside the home network, off by default: `LiveAdvertiseRemoteHost` offers the `web.remoteHost` name to browsers for video (resolved at each session, so a new public IP needs no restart) once the router forwards the media UDP port; a `live.remote` doctor check (`--deep` resolves the name), and live tiles that say why a session from outside gets no video ([docs/ui.md](docs/ui.md#live-video-from-outside-the-home-network)).
- Terminal: `holocron status` and `holocron doctor` name the running version on their first line (`holocron v1.1.2 status - root E:\cameras`, `holocron v1.1.2 (windows/amd64)`), and `holocron doctor --json` has a top-level `version`; `holocron help` already did.
- Web app: every tab's footer shows `Holocron <version>` to signed-in users and on the loopback page (never on the sign-in or setup page), Admin > System has an "About this Holocron" block (version, platform, Go version, recording since), and a page left open across an upgrade says "Holocron was updated to <new>. Reload this page (Ctrl+F5) to use it." with a Reload button; `GET /api/status` gains an additive `app` object.

## [1.1.2] - 2026-10-29

### Changed

- Web app: a camera's last error on the Dashboard is put into words, with the time and whether it is handled ("At 12:03 the camera stream ended after 4 h 40 min. Restarted automatically; recording resumed." instead of "ffmpeg exited with code 0 after 16827s"), as a calm note while the camera records and an error while it does not; the original text stays in the tooltip and under Details, and a note older than 24 h moves under Details.
- Web app: a camera that restarted once or twice in the last hour keeps its green "Recording" chip (the camera note tells what happened); the amber chip is now "Unstable" and appears at 3 restarts in the hour, the level where `holocron doctor` warns. A camera switched off on purpose no longer shows a red "Not recording now." note.

## [1.1.1] - 2026-10-29

### Fixed

- `holocron doctor`: the `recording.segment` check no longer warns at every sunrise and sunset, when a camera switches between its infrared night and colour day profiles and its bitrate changes 3 to 4 times; it now judges the newest closed segment against the camera's closed segments of the last 24 h (a warning below half the lowest rate, a stalled or black stream, or above 1.5 times the peak rate, a runaway) instead of the median of 2 h, which is still shown; an empty closed segment warns ("no data") and is never part of the baseline. The JSON data adds `peakRatio`, `lowestRatio` and `peakCompared`.

## [1.1.0] - 2026-10-29

### Changed

- Web app: the Dashboard leads with a status summary ("Recording 6 of 6 cameras. Nothing needs attention." or what needs attention, with links to the camera or to Admin > Health) and no longer has a Start or Stop control; while Holocron is not running a notice gives `holocron up --detach`, and an admin gets one Start Holocron button.
- Web app: switching a camera's recording off no longer asks; a toast offers Undo for 8 s instead.
- Web app: Stop Holocron moved to Admin > System, in a Danger zone, behind a dialog that says what stops and how to start it again and needs STOP typed; after a stop the page says "Holocron has stopped" instead of "Server unreachable".
- Web app: on a phone the tab bar scrolls instead of clipping the Admin tab, buttons and switches are at least 44 px tall, and busy and primary buttons meet AA contrast.
- Web app: the page, sign-in and first-user pages are named Holocron (they said "IPCCTV control"), and the Stop advice names the logon task, the systemd unit and the launchd agent.
- Web app: the Start and Stop buttons for the recorder and the viewers, which could never work (the viewers no longer exist and the recorder cannot be stopped apart from the rest), were removed together with the control row.

### Added

- Web app: Admin > System > Reload configuration (`POST /api/admin/reload`, admin only, audited as `web.admin.reload`).

### Fixed

- Web app: Start pressed within 5 s of a Stop was refused with "the supervisor is already running"; a start or a stop now makes the next request check the supervisor again.

## [1.0.1] - 2026-10-28

The first release built and published by the release pipeline. The program is the same as 1.0.0: the tag `v1.0.0` was made before the pipeline existed and has no downloadable files, so this version is the first one with archives, checksums, SBOMs and build attestations. Everything new is in the release process.

### Added

- A release process: pushing a `vX.Y.Z` tag of a `main` commit builds the release with GoReleaser (`.goreleaser.yaml`, `.github/workflows/release.yml`) and publishes a GitHub Release with archives for windows/amd64, linux/amd64, linux/arm64 and darwin/arm64, `checksums.txt` (SHA-256), an SPDX SBOM per archive and a build provenance attestation; the notes are this file's section for the version. Builds use `-trimpath` and the commit time, so the same commit builds the same bytes.
- CI job `release-check`, which runs `goreleaser check` and a snapshot release on every push to `main`.
- Dependabot version updates for the pinned GitHub Actions (`.github/dependabot.yml`).
- A repository ruleset to import once, `.github/rulesets/protect-release-tags.json`, that stops a `v*` tag from being moved or deleted.
- This changelog and the release runbook, [docs/releasing.md](docs/releasing.md).

## [1.0.0] - 2026-10-28

The first version of Holocron in its own repository: a Go network video recorder in one executable, `holocron` (`holocron.exe` on Windows), live on Windows since 22 Oct 2026. Earlier history is in the original repository `windows-terminal-IPCCTV-dashboard` (branch `portable-nvr`).

### Added

- Recording with audio: each camera recorded by `ffmpeg` as a stream copy into fixed-length segments (15 minutes by default), with no re-encoding.
- Self-healing: a supervisor restarts a recorder that exits or stalls, with backoff, and cameras are read through a relay (MediaMTX) so the recorder and the live view share one camera session.
- A web app: Dashboard, Live (WebRTC), Footage, Settings and an Admin tab, with sign-in and the roles admin and viewer; an optional HTTPS listener with its own local certificate authority for phones and the LAN.
- Retention by free space: the pruner deletes the oldest footage to keep the `ReserveGB` watermark.
- The archive layout of decision D20: new segments land in each camera's folder (the spool) and closed ones are filed into `<Camera>/YYYY-MM-DD/`; `holocron archive migrate` and `holocron archive flatten` move an archive between the flat and the day-folder layout.
- Alerts through the desktop, the Windows event log, the log file, a push channel and a webhook, and `holocron alert test`.
- `holocron doctor`, a read-only health check with a fix for every problem it finds.
- Builds for Windows (first-class and live), Linux and macOS, cross-compiled and tested in CI.
- Licence: [PolyForm Noncommercial License 1.0.0](LICENSE) (decision D22).

[Unreleased]: https://github.com/karadimnik/holocron/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/karadimnik/holocron/compare/v1.1.2...v1.3.0
[1.1.2]: https://github.com/karadimnik/holocron/compare/v1.1.1...v1.1.2
[1.1.1]: https://github.com/karadimnik/holocron/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/karadimnik/holocron/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/karadimnik/holocron/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/karadimnik/holocron/releases/tag/v1.0.0
