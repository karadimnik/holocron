# The web app

Holocron's web app is a browser page served by the `holocron` program itself: Dashboard, Settings, Footage, Live, and for the LAN a Phone tab and an Admin tab. This document describes what the code does (`internal/web`, `cmd/holocron`). Behaviour that was read in code but not run on a machine says so; anything not established is marked `UNVERIFIED`, and `documented-only` marks text taken from a repository note rather than from the code.

## How it works

- **One binary, nothing to deploy.** The page, its scripts and style sheet are embedded in `holocron` (`internal/web/main.go`, `//go:embed static`). The web app runs as the **web role**.
- **It owns no state.** Status is a read of `<root>/_state/status.json`; control actions go to the supervisor's control API; Settings saves use the same edit helpers as `holocron cam` and `holocron config set`; the Footage index comes from the file names in the archive.
- **No camera credential reaches the browser.** `GET /api/status` is passed through a redaction: keys `password`, `username` and `RelayBase` are dropped, and anything shaped like a URL or an IPv4 address in a string becomes `[redacted]` (`internal/web/api.go`, `Redact`, `ScrubText`). Live view sends the browser no relay address, login or path name (see [Live view](#live-view)). The only camera addresses that cross the API are the ones the Settings tab shows for editing.
- **A web page you visit cannot drive it.** Every request passes the guard in `internal/web/guard.go` before any handler: the `Host` header must be an allowed name (against DNS rebinding); on `/api/` a `Sec-Fetch-Site` other than `same-origin` or `none`, or an `Origin` that is not the address the request went to, is refused with 403; a state-changing request with a body must be `application/json` (else 415). The one exception is `POST /api/live/{camera}/whep`, which carries `application/sdp`. A request with neither `Origin` nor `Sec-Fetch-Site` (a script, `curl`) passes these checks.
- **Every change is a logged action.** Mutating routes run as `cli.Action`s named `web.control`, `web.record`, `web.cameras.put`, `web.config.put`, `web.acknowledge`, `web.login`, `web.logout`, `web.setup`, `web.live.create`, `web.live.delete` and `web.admin.*` (`internal/web/actions.go`, `admin_api.go`). Read the log with `holocron logs --component web`.

## Starting it

| How | What happens |
|---|---|
| `holocron up` | Starts the host, supervisor, relay, live relay and the web app. The host runs the web app as a managed child: `holocron <global flags> web --stop-file <root>/_state/web.stop` (`internal/app/web.go`). If the child exits, the host restarts it and raises an alert ("the web app stopped unexpectedly and is being restarted"). `--detach` waits for an honest start and then prints `web app: http://<web.listen>/ is answering` (`cmd/holocron/nvr.go`). |
| `holocron up --only host` | Everything except the web app. |
| `web.enabled` false in `holocron.config.json` | `up` starts no web app (`internal/config/KEYS.md`). |
| `holocron web` | Runs the web app alone, in the foreground (diagnostics). Defaults come from `holocron.config.json` (`web.listen`, `web.tlsListen`, `web.tlsDir`, `web.authDir`); flags override them. A port already in use fails with `bind <address>`. |
| `holocron open [camera]` | Opens `http://127.0.0.1:8787/#live` (or `#live/<camera>`) in the default browser (`rundll32` on Windows, `open` on macOS, `xdg-open` elsewhere). The address is `web.listen`, with an empty or wildcard host replaced by `127.0.0.1`. `--print` only prints it. It refuses when `web.enabled` is false or nothing answers on `web.listen`. It never uses the HTTPS address. |

Flags of `holocron web` (`internal/web/main.go`): `-listen`, `-tls-listen`, `-tls-dir`, `-auth-dir`, `-tls-readonly true|false`, `-config FILE` (a JSON file), and the one-shot jobs `-print-ca`, `-set-password USER`, `-unlock USER`, `-version`. The one-shot jobs have commands of their own (`holocron tls ca`, `holocron user add`, `holocron user passwd`, `holocron user unlock`); prefer those.

Keys that only the `-config FILE` of `holocron web` or the environment can set (they are not `holocron.config.json` keys, whose unknown `web.*` entries are ignored with a warning, `internal/config/web.go`):

| Setting | Config file key | Environment | Flag |
|---|---|---|---|
| Sign-in token for a non-loopback `web.listen` | `token` | `CAMUI_TOKEN` | none |
| View-only HTTPS listener (default true) | `tlsReadOnly` | `CAMUI_TLS_READONLY` | `-tls-readonly` |
| Extra names on the server certificate (at most 4) | `tlsHosts` | none | none |
| Extra allowed `Host` names | `allowedHosts` | none | none |
| Seconds a hidden Live page keeps its streams | `liveHiddenGraceSeconds` | `CAMUI_LIVE_HIDDEN_GRACE` | none |

`holocron up` starts the web child without `-config`, so under `up` only the environment variables apply. The child inherits the environment of the host process (`internal/osx/scope_unix.go`, `scope_windows.go`); whether the autostart entry created by `holocron install` carries your variable is `UNVERIFIED`.

## Addresses and ports

| Listener | Setting | Default | Scheme | Sign-in |
|---|---|---|---|---|
| Loopback | `web.listen` | `127.0.0.1:8787` | HTTP | none: whoever reaches it is the owner and an admin |
| LAN (phones, a second PC) | `web.tlsListen` | off (empty) | HTTPS | required |

Both are changed in `holocron.config.json` and applied with `holocron restart --detach`.

**Loopback guard.** The web app refuses to start on a `web.listen` that is not exclusively loopback unless a token of at least 16 characters is configured (`internal/web/config.go`, `Validate`). With a token every request needs it: as the `camui_token` cookie, an `Authorization: Bearer` header, or once as `?token=`, which is moved into a cookie (`internal/web/token.go`). For other devices prefer the HTTPS listener below, which has a real sign-in; the token mode has no users or roles.

**HTTPS listener.** A second listener, off by default; the loopback one is not affected. It needs a private IPv4 address of this PC and at least one user. See [HTTPS on the LAN: a phone or a second PC](#https-on-the-lan-a-phone-or-a-second-pc).

`holocron status` shows the web role (pid, listen address, whether the HTTPS listener is on, whether a login exists). `holocron doctor --group web` checks the process, `GET /api/status` on the loopback address, the TLS handshake and certificate expiry, the local CA, that a user exists, and ERROR records of the last hour.

## First run: the setup page and signing in

- **Loopback needs no sign-in.** Open `http://127.0.0.1:8787/` and everything works as an admin.
- **First user.** While no user exists, `http://127.0.0.1:8787/setup` (loopback only) shows "Create the first user": a user name, the password twice, **Create user**. The user is an admin. Once any user exists, `/setup` and `POST /api/auth/setup` answer 404, and the page says "A user already exists." On the HTTPS listener these routes never exist (`internal/web/auth_service.go`).
- **Or from the command line**: `holocron user add anna` reads the password from standard input (typed without echo when stdin is a terminal). A user name is 1 to 64 characters from `A-Z a-z 0-9 . _ -` (case-insensitive); the password is 12 to 128 characters and must not equal the name; there are no other rules.
- **The LAN listener will not start with no user.** With `web.tlsListen` set and no user the web app refuses to start (`ValidateTLSUsers`). Create the user first, then set `web.tlsListen`.
- **Sign-in page** (HTTPS listener): **Username**, **Password**, **Sign in**. Without a session, a browser page request is redirected (303) to `/login?next=<path>` (the target is kept only if it is a same-origin path); an API call gets 401 `{"error":"authentication required"}`. After sign-in the header shows the user name and **Sign out**.
- **Sessions.** Cookie `camui_session` (`Secure; HttpOnly; SameSite=Strict; Path=/`), 30 days. A session ends after 7 days without use or 30 days after sign-in, at **Sign out**, and when that user's password is changed or the user is removed. The server keeps only the SHA-256 of the token, in `sessions.json` (`internal/web/auth_sessions.go`, `auth_service.go`). Passwords are stored as PBKDF2-HMAC-SHA256 with 600,000 iterations and a random salt (`auth_password.go`).
- **Files.** `users.json`, `sessions.json` and `auth.log` live in `web.authDir`; empty means `%LOCALAPPDATA%\IPCCTV\camui\auth` on Windows, otherwise the user cache folder `IPCCTV/camui/auth` (`internal/web/config.go`, `defaultAuthDir`). Never put them in the repository.

### Lockout and `user unlock`

Two independent protections on the sign-in (`internal/web/auth_throttle.go`, `auth_service.go`):

- **Per client address** (the TCP peer; forwarding headers are never trusted): 10 attempts a minute with a burst of 5, then 429 with `Retry-After`.
- **Per user name**, existing or not: the first 4 consecutive failures cost nothing; the 5th locks the name for 30 s, and each further failure doubles it up to 15 minutes. While locked, even the correct password is refused (429 with `Retry-After`). A success resets the count; the count is forgotten after 24 hours of quiet. The counters are in the web app's memory, so restarting it clears them. A device already signed in is not affected.
- An unknown name and a wrong password answer identically: `Invalid username or password`.
- `auth.log` in the auth folder records time, peer address, attempted name and result (`ok`, `fail`, `throttled`, `locked`), plus sign-outs and unlock requests; never a password, hash or token. It is cut at 1 MiB to `auth.log.1`.

To lift a lockout at once: `holocron user unlock anna`. It drops a marker file in `<authDir>/unlock/`; the running web app forgets the failures of that name at the next sign-in attempt for it (`auth_cli.go`, `auth_service.go`). The Admin tab shows a "Locked out" column and an unlock action for the same purpose; the column is only meaningful while the LAN listener runs (`lockoutsKnown` in `GET /api/admin/users`).

## Roles: admins and viewers

Every user is an **admin** or a **viewer**: `holocron user add <name> --role viewer`, `holocron user role <name> admin|viewer`, or the Admin tab. A record with no role counts as admin (`effectiveRole`, `auth_store.go`).

One table decides who may call what: `routeAccess` in `internal/web/access.go` gives every registered route a class (public, viewer or admin). The LAN session gate asks the mux which route a request will really run, so path tricks are judged as their target; an unclassified route is refused for everybody (403).

| Class | Who | Routes |
|---|---|---|
| public | nobody signed in | `GET /login`, `/login.js`, `/style.css`, `POST /api/auth/login`, `GET /api/auth/me` |
| viewer | any signed-in user | the page and its assets (`GET /`), `GET /api/status`, `/api/footage`, `/api/clip`, `/api/live`, `POST /api/live/{camera}/whep`, `DELETE /api/live/session/{id}`, `GET /api/tls/ca.crt`, `/api/tls/info`, `POST /api/auth/logout` |
| admin | admins only | everything else: `POST /api/control`, `/api/cameras/{name}/record`, `/api/acknowledge`, `GET` and `PUT /api/cameras`, `/api/config`, `GET /admin.js`, every `/api/admin/*` route, and (loopback only) `GET /setup`, `/setup.js`, `POST /api/auth/setup` |

- **A viewer watches**: Dashboard, Footage, Live and the Phone tab. A viewer's refused call gets 403 `forbidden: admins only` and a WARN audit record (`access.denied`, action `web.access`). The page hides Settings and Admin for a viewer and shows a note; the server decides regardless.
- **Order of checks on the LAN listener**: no session 401, then role 403, then view-only 403, then the Host/Origin guard (`tlsserver.go`).
- **There is always an admin.** The last admin cannot be demoted (`user role`, Admin tab). `user remove` refuses the last admin while other users remain; from the command line the very last user may go (with `--force` while `web.tlsListen` is set); from the Admin tab the last admin is never removable (`admin.go`).
- **Changing a role or password ends that user's sessions** (an admin changing their own in the Admin tab stays signed in).
- **The loopback listener has no roles**: `GET /api/auth/me` there answers `loginRequired: false, role: admin`.

**View-only HTTPS (default).** `tlsReadOnly` defaults to true: even an admin on the HTTPS listener can only watch. It is an allow-list (`readOnlyAllowed`, `tlsserver.go`): a few GETs (status, live, cameras, config, footage, clip, `/api/tls/*`, `/api/auth/me`), the Live WHEP calls, and sign-in and sign-out. Everything else, including every `/api/admin/*` route, is refused with 403 `read-only: this address can only watch`. The page then shows "This address is view-only", makes Settings inert and hides the Admin tab. `-tls-readonly false` (or `CAMUI_TLS_READONLY=false`) gives a signed-in LAN admin full control; a viewer is still refused every change. There is no `holocron.config.json` key for it.

## The tabs

The header shows the tabs, a status chip with "updated N s ago" (the page polls `GET /api/status` every 5 s), and on the HTTPS listener the user name and **Sign out**. The address fragment selects a tab: `#dash`, `#set`, `#foot`, `#live` (`#live/<camera>` for one camera), `#phone`, `#admin`; an unknown fragment shows the Dashboard. Viewers do not get Settings or Admin; Phone exists only on the HTTPS listener; Admin is hidden on a view-only listener (`detectAccess` in `app.js`).

### Dashboard

The Dashboard shows state, not commands. From the top:

- **Status summary** (`#summary`, a polite live region: `role="status"`, rewritten only when its words change, so a screen reader hears each change once). One verdict, an icon plus a word ("All good", "Needs attention", "Not recording", "No status", "No connection"; colour is never the only cue), and one sentence, built only from `GET /api/status`: `supervisor.running` and `reason`, each camera's `Record` and `Alive`, `Disk.FreeGB` against the reserve, and `Disk.ProjectedDaysRemaining`. Examples: "Recording 6 of 6 cameras. Nothing needs attention.", "2 of 6 cameras are not recording." (each camera's name is a link that scrolls to its row and focuses its Record switch), "Holocron is not recording." (supervisor stopped, or the status file claims running but its process is gone), the disk at its reserve or under 4 days of headroom (with a link to Admin > Health for admins), "This page cannot reach the web app." (no answer, and no stop was asked for). Switched-off cameras are mentioned, not counted as a problem.
- **Persistent notices** for the last unexpected stop and the last window close, kept after the recorder is back until an admin presses **Dismiss** (`POST /api/acknowledge`). The dismissal is keyed to the event's timestamp and stored beside `status.json` in `camui-acknowledged.json`, so a later stop shows again.
- **"Holocron is not running"** notice, only while `supervisor.running` is false. That is the case where this page is served by `holocron web` on its own: under `holocron up` the page stops with the rest, so it never offers a Start there. The notice gives `holocron up --detach`; an admin also gets one **Start Holocron** button (`POST /api/control` `{"scope":"all","action":"start"}`: runs `holocron up --detach` with the web role's own global flags; 409 "the supervisor is already running" when the control endpoint answers; measured 2026-10-28 on a Linux sandbox: 0.52 to 0.54 s). While it runs the button reads "Starting... Ns" and a progress line says what is running and how long it usually takes.
- **Tiles**: Footage headroom (projected days), Free space, Archive (GB and segments).
- **Cameras table** (compact rows below 760 px): camera (with segment count and GB, or "No footage since ..." or the last error), status chip (Recording, Recovering, Camera offline, Not recording, Stopped), input (Relay or Direct), time since the last closed segment, restarts in the last hour, and a **Record** switch (`role="switch"`, On or Off in words).
- **Recent events** and **Services** (two rows: `holocron supervisor` and `holocron web`, plus the supervisor's own verdict).
- **The full health check is in the Admin tab** (first section, see [The Admin tab](#the-admin-tab)); from a terminal it is `holocron doctor`.

**Record switch** (admins): `POST /api/cameras/{name}/record` with `{"enabled": true|false}`, through the supervisor; 409 naming `holocron cam enable|disable` when the supervisor is not running. Switching off asks nothing; a toast at the bottom of the window says "Recording switched off for <camera>" and offers **Undo** for 8 s (the countdown pauses while the pointer or the focus is on it), which switches it back on through the same endpoint; the switch itself is always the way back. Switching on needs nothing. Results are announced through a polite live region; one action at a time (409 "another action is still running").

Reloading the configuration and stopping Holocron are in **Admin > System**, not on the Dashboard (see [The Admin tab](#the-admin-tab)).

### Design principles

The Dashboard and Admin > System follow these, each from a primary source (fetched 2026-10-28):

- **Status first, at a glance**: the top answers "is Holocron recording, and does anything need me?" without scrolling or clicking ([NN/g, 10 usability heuristics: visibility of system status](https://www.nngroup.com/articles/ten-usability-heuristics/); [NN/g, dashboards: at-a-glance information](https://www.nngroup.com/articles/dashboards-preattentive/)).
- **Show state, not commands; no control that cannot work**: Start appears only where it can run, Stop only where it belongs ([NN/g, visibility of system status](https://www.nngroup.com/articles/visibility-system-status/); restart lives in settings in [Home Assistant](https://www.home-assistant.io/common-tasks/general/restarting/) and [Frigate](https://docs.frigate.video/configuration/config/)).
- **Routine actions without confirmation, with undo**: the Record switch and Reload ([NN/g, confirmation dialogs](https://www.nngroup.com/articles/confirmation-dialog/); [Apple HIG, alerts](https://developer.apple.com/design/human-interface-guidelines/alerts); [Material components, snackbar with an undo action](https://github.com/material-components/material-components-android/blob/master/docs/components/Snackbar.md)).
- **A typed confirmation only for the disruptive action, away from routine use**: Stop in a Danger zone, a dialog that says what stops and how to start again, STOP typed ([NN/g, confirmation dialogs](https://www.nngroup.com/articles/confirmation-dialog/); [Primer, confirmation dialog](https://primer.style/product/components/confirmation-dialog/guidelines)).
- **Dialog focus**: initial focus on Cancel, Tab stays inside, Escape cancels, focus returns to the opener ([WAI-ARIA APG, modal dialog](https://www.w3.org/WAI/ARIA/apg/patterns/dialog-modal/); [Primer, confirmation dialog accessibility](https://primer.style/product/components/confirmation-dialog/accessibility)).
- **Explain what happened**: "Holocron has stopped" instead of "Server unreachable" ([NN/g, visibility of system status](https://www.nngroup.com/articles/visibility-system-status/)).
- **Accessible status**: live regions for the summary and results ([WCAG 2.2, 4.1.3 Status Messages](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html)); colour never alone ([WCAG 2.2, 1.4.1 Use of Color](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html)); the Undo countdown pauses ([WCAG 2.2, 2.2.1 Timing Adjustable](https://www.w3.org/WAI/WCAG22/Understanding/timing-adjustable.html)); 44 px targets on a phone ([WCAG 2.2, 2.5.8 Target Size](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html)); text contrast at least 4.5:1.

### Settings

Admins only. Reads `GET /api/cameras` and `GET /api/config`; saves with `PUT /api/cameras` and `PUT /api/config`. Both go through the same helpers as `holocron cam` and `holocron config set`: validated first, written atomically, order and fields the page does not show are kept, and a problem answers 422 with the fields at fault and writes nothing. Afterwards the running supervisor is asked to reload through the control API and the page says whether it did; a request that changes nothing writes nothing (`cmd/holocron/settings.go`).

- **Cameras** (`cameras.json`, schema in [`internal/config/KEYS.md`](../internal/config/KEYS.md#camerasjson)): rows of **Name**, **Address** (host or `host:port`), **Stream** (blank means `stream1`) and **Record**, with **Add camera**, **Save cameras**, **Discard**. The page also requires: a name of at most 48 characters, none of `< > : " / \ | ? *`, no control characters, no trailing dot, not a reserved Windows device name (`CON`, `NUL`, `COM1` ...), no two names that would share an archive folder; a stream of letters, digits, `.`, `_`, `-` (1 to 64). No credentials are in this file or on this page.
- **Recording** (`holocron.config.json`): the four keys `SegmentSeconds` (60 to 3600, whole seconds; the page offers 5, 15, 30, 60 minutes), `ReserveGB` (1 to 100000), `PruneHysteresisGB` (0 to 10000), `MaxRetentionDays` (1 to 365; blank removes the age limit). These ranges are narrower than `holocron config set` accepts. **Archive location** is shown, never edited.
- **Everything else** in `holocron.config.json` is reachable from the Admin tab's "Settings keys" (the `holocron config set` allow-list) or with `holocron config set`. Keys: [`internal/config/KEYS.md`](../internal/config/KEYS.md), prose in [configuration.md](configuration.md).

The message after a save is one of: "Nothing changed; nothing was written.", "Saved. The recorder is not running; the change applies at its next start.", "Saved. The running recorder has reloaded.", or "Saved. The running recorder did not take a reload (...); it picks the change up on its own within Ns."

### Footage

Pick a camera, then a date, then a segment.

- **Camera list**: every camera folder in the archive, in `cameras.json` order, with its segment count; a folder whose camera left `cameras.json` is listed last and marked "no longer in the camera list". Folders beginning with `_` are the recorder's own and are not listed.
- **Dates** are newest first ("Today", "Yesterday"). **Segments** are newest first: start time, length, size, and a "recording now" chip on the newest one when it was written in the last 90 s. A banner lists gaps of 5 s or more in the day (up to 4, then "N more gaps"). **Refresh** re-reads; the tab also re-reads on every visit.
- **The index is parsed from file names**, `<Camera>_YYYYMMDD_HHMMSS+ZZZZ.mkv` (or `.mp4`), in the camera folder and its day folders; a file in a day folder counts only under the date in its name. A segment's length is its last-write time minus its start; nothing opens a segment to list it (`internal/web/footage.go`). Layout: see [how-it-works.md](how-it-works.md).
- **Playing a clip**: `GET /api/clip?camera=<name>&segment=<file name>`. ffmpeg remuxes the segment on request to fragmented MP4 with the video copied and the camera audio re-encoded to AAC 64k (`-c:v copy -c:a aac -b:a 64k`), streamed as it is produced, so nothing is converted in advance or stored twice. The response has no length and `Accept-Ranges: none`. The segment being recorded plays up to what is written. The ffmpeg used is `FfmpegPath` of `holocron.config.json`, else `ffmpeg` on the PATH. A remux is cut off after 10 minutes. Failures: 404 for an unknown camera or segment, 502 when ffmpeg cannot start or produces nothing.
- **Safety**: the camera and segment names from the browser are matched against what the archive listing contains and never joined onto a path; the result must resolve inside the archive root and be a regular file (`resolveSegment`).

### Phone

Only on the HTTPS listener, for every signed-in user (a viewer has no Settings, so this is where the CA lives for them; an admin also finds the same panel under Settings). It shows the SHA-256 fingerprint of the local CA, a download link (`api/tls/ca.crt`) and install steps. See [HTTPS on the LAN](#https-on-the-lan-a-phone-or-a-second-pc).

### Admin

Admins only, and not on a view-only listener. See [The Admin tab](#the-admin-tab).

## Live view

The **Live** tab shows the cameras as a grid of tiles. It needs `LiveViewEnabled` set to `true` in `holocron.config.json` (default false) and the live relay running, which starts with the recorder (`holocron up`). Otherwise the tab says why: "Live view is off" (it says to run `holocron live enable`, which sets `LiveViewEnabled` in `holocron.config.json`), "Live view is not running", or "No cameras". `holocron open` jumps straight to it.

**What it shows** (`internal/web/static/live.js`):

- Each tile plays the camera's sub stream (`LiveSubStream`, default `stream2`) with the name, a state chip (Connecting, Reconnecting, Live, Offline, Error, Paused) and an **Unmute** button. Sound is off until you unmute that tile.
- The grid fills the window. **Fit** (default) never crops; **Fill** divides the whole area into cells and crops the picture to them. The choice is kept per browser in `localStorage` under `camui.live.fit`. **Fullscreen** puts the grid alone on the screen.
- A click on a tile opens a modal for that camera: the tile's own picture at once, then the **main** stream (chip "HD loading...", then "HD"; "HD unavailable" with **Retry HD** if it cannot be had), **Unmute/Mute**, **Fullscreen**, **Copy diagnostics**, **Close**. At most one main session exists at a time.
- Deep links: `#live`, `#live/<camera name>` (percent-encoded).
- **Hidden means keep, then free.** Minimising the window or choosing another tab does not close the streams at once; they stay connected for `hiddenGraceSeconds` (default 300; 0 frees at once; -1 never; capped at 86400; see the settings table above for where to set it), then everything is closed and reopens when the page is shown again.
- **Recovery without a reload**: a lost picture reconnects after 1, 2, 4, 8, 15, then 30 s; a connected picture whose frames stand still for 8 s is rebuilt; the page makes at most 20 automatic reconnects a minute; the camera list is re-read every 10 s (5 s while something is wrong, 30 s while hidden). Constants: `live.js` lines 81 to 100.

**How it works.** The browser talks only to the web app. `POST /api/live/{camera}/whep?stream=sub|main` carries the browser's WebRTC offer (`Content-Type: application/sdp`, at most 64 KiB). The web app looks the camera's path up in `status.json`, forwards the offer to the live relay at `LiveWhepAddress` (a loopback `host:port`, default `127.0.0.1:8889`) with a Basic login (user `camlive`, the secret read at each use from the live secret file, 64 hex characters, `LiveSecretPath` or `live_view_secret.txt` in the auth folder) and returns the answer with `201` and `Location: /api/live/session/<id>`, an id the web app minted. The picture then flows from the relay to the browser directly over UDP (`LiveUdpAddress`, default `127.0.0.1:8189`). No browser header is forwarded, no proxy from the environment is used, redirects are not followed.

| Route | Who | Behaviour |
|---|---|---|
| `GET /api/live` | viewer | `enabled`, `alive`, `hiddenGraceSeconds`, and per camera `name`, `sub`, `main`, `camAlive`, `restarts`, `lastError`. Names only; `cameras` is empty when live view is off. |
| `POST /api/live/{camera}/whep?stream=sub\|main` | viewer | 201 with the answer SDP. Errors are JSON with generic text: 400 bad `stream` or empty offer, 404 unknown camera or stream not offered, 413 offer too large, 415 not `application/sdp`, 429 too many sessions, 502 "live view is misconfigured" (the relay refused the login, the secret file is missing or not 64 hex characters, or `LiveWhepAddress` is not loopback), 503 "live view is off" or "unavailable", 504 the relay did not answer in 15 s. |
| `DELETE /api/live/session/{id}` | viewer | Closes that session. An id the web app did not mint, or older than an hour, is 404. |

At most 24 live sessions at a time and 6 per camera; more is 429.

## Live view on the LAN: media, firewall and stuck tiles

**A tile that is not playing says why**, in plain words: the signalling answer, the ICE and connection state, how many candidates the browser and the server offered and of what class (loopback, private, ...), and "No media after 15 s". Only counts and classes are shown, never an address. **Copy diagnostics** puts one `live-diag/3` line on the clipboard to paste into a bug report. From a terminal, `holocron doctor --group live` checks the live side.

**Media on a LAN address.** The page and signalling stay on the web app's addresses, but the video travels over UDP between the relay and the browser. For a browser on another device, `LiveUdpAddress` (default `127.0.0.1:8189`) must be `0.0.0.0:<port>` or this PC's private IPv4 and port, and `LiveAdvertiseHosts` must list the private IPv4 address(es) the browser should use (at most 4; rules in [`internal/config/KEYS.md`](../internal/config/KEYS.md); restart needed). Prose: [configuration.md](configuration.md).

**Firefox** (documented-only; the code agrees in one place): the tile hint in `live.js` says that when the server offers only loopback addresses "some browsers (Firefox) refuse" and tells you to set `LiveAdvertiseHosts`. The repository notes record that Firefox did not connect to a loopback-only media address and played once the settings above were set (`tasks/holocron/todo.md`, items P5 and P13; `tests/stress/live-view/README.md`). Not re-run for this document.

**Firewall** (Holocron applies none; documented-only, `UNVERIFIED` on any machine in this review). Windows filters by program. For other devices to reach the media port, allow the program that serves live view (`LiveExePath`, the relay's MediaMTX program when empty) inbound on UDP for your `LiveUdpAddress` port, Private network only. From an elevated PowerShell, adjusting the program path and port:

```powershell
New-NetFirewallRule -DisplayName 'Holocron live view media (UDP 8189)' -Direction Inbound -Action Allow -Protocol UDP -LocalPort 8189 -Program 'C:\mediamtx\mediamtx.exe' -Profile Private -RemoteAddress LocalSubnet
Remove-NetFirewallRule -DisplayName 'Holocron live view media (UDP 8189)'   # to take it away again
```

The rule only helps on a network Windows calls Private (`Get-NetConnectionProfile`). Without a rule, Windows asks the first time a program listens beyond loopback.

## HTTPS on the LAN: a phone or a second PC

An optional second listener serves the whole web app over HTTPS, **behind a sign-in**. It is off by default. There is no plain-HTTP listener on the LAN address.

**Set it up:**

1. Create a user: `holocron user add anna` (password on standard input), or the setup page. Viewers: `holocron user add bob --role viewer`.
2. In `holocron.config.json` set `"web": { "tlsListen": "192.168.1.50:8443" }`. The host must be a private IPv4 address of this PC (`10.x`, `172.16-31.x`, `192.168.x`); `0.0.0.0`, names, IPv6, loopback and public addresses are refused when the web app starts. Reserve the address on the router so it stays put.
3. `holocron restart --detach`, then `holocron tls ca` to get the CA and its fingerprint.
4. Allow the TCP port in the firewall (below), and install the CA on each device.

- `web.tlsDir` holds the CA and the server certificate with their keys (`ca.crt`, `ca.key`, `leaf.crt`, `leaf.key`); empty means `%LOCALAPPDATA%\IPCCTV\tls` on Windows, otherwise the user cache folder. Keep it private and out of the repository.
- TLS 1.2 or newer, HTTP/2 and HTTP/1.1.
- The `Host` the browser uses must be one of the certificate's names: the `web.tlsListen` address, `127.0.0.1` and `localhost` (plus `tlsHosts`, config-file only); another name gets 403 "host not allowed". Open the page by the IP address you configured.
- The view-only default and the roles are described above.

### Signing in on the LAN: users, sessions and brute-force protection

See [First run](#first-run-the-setup-page-and-signing-in) for the sign-in page, sessions and files, [Lockout and `user unlock`](#lockout-and-user-unlock) for the limits, and [Roles: admins and viewers](#roles-admins-and-viewers) for what each user may do.

Commands (all take `--auth-dir`, default `web.authDir`):

| Command | Does |
|---|---|
| `holocron user add <name> [--role admin\|viewer]` | Adds a user (default admin); refuses an existing name. |
| `holocron user passwd <name>` | New password from standard input; ends that user's sessions. |
| `holocron user role <name> admin\|viewer` | Changes the role; ends that user's sessions; the last admin cannot be demoted. |
| `holocron user remove <name> [--force]` | Removes the user and its sessions. |
| `holocron user list [--json]` | Names, role, creation time; never hashes. |
| `holocron user unlock <name>` | Lifts a sign-in lockout in a running web app. |

### The certificate authority

The first start with `web.tlsListen` set (or `holocron tls ca`) creates a local CA (ECDSA P-256, valid 10 years) and the server certificate it signs (`internal/web/tlsca.go`).

- **The CA is name-constrained**: it can vouch only for `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `127.0.0.0/8` and the names `localhost`, `.lan`, `.local`, `.home.arpa`, `.internal`. A leaked CA key cannot vouch for a public website.
- **The server certificate lasts 390 days** and is replaced when it is within 30 days of expiry or when the names it must cover change; devices keep trusting the CA. A running listener renews it too. An expired CA is a start error that tells you to delete the TLS folder.
- Only the CA **certificate** is ever served (`GET /api/tls/ca.crt`, to a signed-in user, as `ipcctv-local-ca.crt`; 404 on the loopback listener). `GET /api/tls/info` gives the fingerprint, subject, expiry and the read-only flag, on the HTTPS listener only.
- Key files are written through a temporary file restricted to the owner (on Windows with `icacls`, `fileperm_windows.go`).

| Command | Does |
|---|---|
| `holocron tls ca [--json]` | Prints the CA file, name, expiry, SHA-256 fingerprint and install steps; creates the CA when absent. |
| `holocron tls print [--out FILE]` | Writes the CA certificate (never a key) to standard output or a file; fails when there is no CA yet. |
| `holocron tls rotate --yes` | Deletes the four certificate and key files (anything else in the folder is left alone). A fresh pair is made at the next web start and every device must reinstall the CA. |

**Install the CA once per device, in this order.** The download needs a session: (1) open `https://192.168.1.50:8443/` on the device and click through the one certificate warning, (2) sign in, (3) open the **Phone** tab and download the certificate, (4) check that the fingerprint the device shows equals the one on the page or from `holocron tls ca`, (5) install it, (6) reopen the page: no warning any more.

- **iPhone:** open the file, install it under Settings > General > VPN & Device Management, then turn it on under Settings > General > About > Certificate Trust Settings.
- **Android:** Settings > Security > Install a certificate > CA certificate.
- **Windows:** double-click the file > Install > Trusted Root Certification Authorities.

(Steps as printed by `holocron tls ca`; not walked through on a device for this document.)

**Rotate or revoke.** `holocron tls rotate --yes`, then `holocron restart --detach`; `holocron tls ca` shows the new fingerprint; remove the old CA from each device and install the new one. The Admin tab does the same after you type `ROTATE`; until the web app restarts the LAN listener keeps the old certificate. To cut one person off, change their password (`holocron user passwd`) or remove them (`holocron user remove`); there is no per-device list.

**Windows Firewall** (Holocron applies none; documented-only, `UNVERIFIED`; Windows filters by program and `holocron.exe` is the program that listens). From an elevated PowerShell, adjusting the path and port:

```powershell
New-NetFirewallRule -DisplayName 'Holocron web HTTPS (TCP 8443)' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8443 -Program '<path to holocron.exe>' -Profile Private -RemoteAddress LocalSubnet
Remove-NetFirewallRule -DisplayName 'Holocron web HTTPS (TCP 8443)'   # to take it away again
```

The video needs the media settings and UDP rule from [Live view on the LAN](#live-view-on-the-lan-media-firewall-and-stuck-tiles).

**Threat note.** The sign-in and HTTPS keep other devices out and the traffic private on the LAN. With the view-only default, a lost phone or a guessed password can watch but cannot stop recording or reconfigure anything; with `tlsReadOnly` off, a signed-in admin has full control. Anyone who can read `web.tlsDir` can read the private keys; anyone who can read `web.authDir` can read the password hashes. Both folders are restricted to the owner; do not copy them. Exposing the web app to the internet is out of scope: the project plan puts a private overlay (WireGuard or Tailscale) before any such step (`tasks/holocron/todo.md`, P50). See [limitations.md](limitations.md).

## The Admin tab

Admins only; its markup and code are in `admin.js`, which only an admin receives (`GET /admin.js` is an admin route; the page everyone gets holds an empty placeholder). Not shown on a view-only HTTPS listener. Each section calls the same helper as the matching `holocron` command, as a `web.admin.*` action; changes and the reads that matter for security (user list, CA, camera-login status, autostart status) go to the audit stream. Passwords and the camera login are typed into password fields, sent once in a POST body and never read back.

| Section | Does | Command it shares |
|---|---|---|
| Health (first) | A badge (HEALTHY, DEGRADED, BROKEN, NOT RUNNING), the time of the run, **Refresh**, "auto refresh every 30 s" (off by default), and a block per group (setup, recording, connections, live, web, storage, system) that is open when it holds a warn or fail; each check with its level, detail and, for warn and fail, the fix command with a Copy button. | `holocron doctor` |
| Users | List (name, role, created, locked out), add, set a password, make admin or viewer, unlock, remove. | `holocron user add`, `passwd`, `role`, `remove`, `unlock` |
| Camera login | Stores the camera user name and password in the secrets store; shows only "present" or "not set". Importing the old `camera_creds.xml` stays on the command line. | `holocron secret set`, `holocron secret import` |
| Settings keys | Every key of the `config set` allow-list with value, default, rule and whether it needs a restart; edit (validated first) and back to the default. | `holocron config set`, `holocron config unset` |
| Certificate authority | Fingerprint, download of the CA certificate, install steps, rotate after typing `ROTATE`. | `holocron tls ca`, `tls print`, `tls rotate` |
| Maintenance | A preview of the pruner (deletes nothing), the archive table, log levels (running and configured) with a run-time change, optionally saved. | `holocron prune --dry-run`, `holocron archive stats`, `holocron log level` |
| Autostart | Status and enable or disable; a checkbox to enable despite an enabled PowerShell-system task. Creating or removing the entry stays on the command line. | `holocron autostart status`, `autostart enable`, `autostart disable` |
| System (last) | **Reload configuration** (`POST /api/admin/reload`, no confirmation): the supervisor re-reads `cameras.json` and `holocron.config.json` now; live view on or off applies at once, other changed settings still need a restart; 409 when the supervisor is not running. A **Danger zone** with **Stop Holocron...**: a dialog lists what stops (every camera's ffmpeg recorder, live view and the relay, and under `holocron up` the host and this page), that nothing is recorded until Holocron is started again, and how (`holocron up --detach`, signing in again when autostart is on, or the service manager); **Stop Holocron** enables only once `STOP` is typed (any case, spaces trimmed); the focus starts on Cancel and Escape cancels. It sends `POST /api/control` `{"scope":"all","action":"stop"}` (`POST /v1/stop`: cameras, then live view, then relay). When the server then stops answering, the page shows **Holocron has stopped** (when, why the page is gone, how to start it again, **Check again**; it reloads by itself when the server answers) instead of "This page cannot reach the web app". | the control API's `POST /v1/reload`; `holocron down` |

**Health.** `GET /api/admin/health[?group=recording,live]` answers `{"ok":true,"health":<the document of holocron doctor --json>,"checkedAt":<time>}`; an unknown group is 400 and the message names the valid ones. It runs the same check table as `holocron doctor` inside the web role; the page gives up after 20 s (`admin.js`). Per `internal/web/README.md` (documented-only) concurrent requests share one run and a run younger than 5 s is reused. It is a read-only poll: not audited, its records are DEBUG. The `environment` line describes the web app's own environment, not your terminal.

**On the command line only, on purpose:** `holocron secret import`, `holocron prune --now --yes`, `holocron install` and `holocron uninstall`, and `holocron up`, `down`, `restart` beyond the Dashboard's standalone Start and the Admin tab's Stop.

## Routes

`routeAccess` in `internal/web/access.go` is the authority; this table is a summary. "Public", "viewer", "admin" are as in [Roles](#roles-admins-and-viewers). On a view-only HTTPS listener only the allow-list described there passes (no change of state, no `/api/admin/*`).

| Verb and path | Who | Purpose |
|---|---|---|
| `GET /`, `/style.css` | viewer, public | The page and its style sheet |
| `GET /login`, `/login.js`, `POST /api/auth/login`, `GET /api/auth/me` | public | Sign-in page and calls (LAN) |
| `POST /api/auth/logout` | viewer | Sign out |
| `GET /setup`, `/setup.js`, `POST /api/auth/setup` | admin, loopback only, while no user exists | First user |
| `GET /api/status` | viewer | Redacted status for the Dashboard |
| `POST /api/control` | admin | Start or stop (`scope` all, recorder, dashboard; `action` start, stop) |
| `POST /api/cameras/{name}/record` | admin | Record on or off |
| `POST /api/acknowledge` | admin | Dismiss a persistent notice |
| `POST /api/admin/reload` | admin | Reload the configuration (Admin > System; `web.admin.reload`, audited) |
| `GET`, `PUT /api/cameras` | admin | Read or replace the camera list |
| `GET`, `PUT /api/config` | admin | Read or write the four Settings keys |
| `GET /api/footage`, `/api/clip` | viewer | Footage index; one clip as fragmented MP4 |
| `GET /api/live`, `POST /api/live/{camera}/whep`, `DELETE /api/live/session/{id}` | viewer | Live view |
| `GET /api/tls/ca.crt`, `/api/tls/info` | viewer, HTTPS listener only | CA certificate and its fingerprint |
| `GET /admin.js` | admin | The Admin tab's code |
| `/api/admin/config` (GET), `/config/set`, `/config/unset` (POST) | admin | Settings keys |
| `/api/admin/users` (GET), `/users/add`, `/users/password`, `/users/role`, `/users/remove`, `/users/unlock` (POST) | admin | Users |
| `/api/admin/camera-login` (GET, POST) | admin | Camera login |
| `/api/admin/tls` (GET), `/tls/ca.crt` (GET), `/tls/rotate` (POST) | admin | Certificate authority |
| `/api/admin/prune`, `/archive`, `/log`, `/autostart`, `/health` (GET); `/log/level`, `/autostart` (POST) | admin | Maintenance, autostart, health |
