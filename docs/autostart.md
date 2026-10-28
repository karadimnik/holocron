# Autostart (`holocron install`)

This page is about the autostart entry of `holocron`. The old PowerShell system is a separate program that
may still be on the machine; it has its own three logon tasks, `IPCCTV-Recorder`, `IPCCTV-Dashboard` and
`IPCCTV-Ui`. The two systems must never both start at logon. `holocron` never changes those tasks (it only
reads them); the old system lives in the original repository `windows-terminal-IPCCTV-dashboard`, branch
`master`.

Installing the autostart is a `holocron` command like every other (rule R1): there is no script.

```
holocron install [--mode logon|service|systemd|launchd] [--window visible|hidden] [--dry-run] [--allow-temp]
                 [--i-know-old-tasks-are-enabled] [--yes] [--account A]
holocron autostart status [--json]
holocron autostart disable [--dry-run]
holocron autostart enable [--dry-run] [--i-know-old-tasks-are-enabled]
holocron uninstall [--dry-run] [--force]
```

## What it creates

| OS | Mode (default first) | Entry | It runs |
|---|---|---|---|
| Windows | `logon` | Scheduled Task `Holocron` for the current user, trigger "at log on of" that user, limited token, no admin needed | `--window visible` (default): `C:\Holocron\holocron.exe --config C:\Holocron\holocron.config.json up` (attached, in a console window on your desktop); `--window hidden`: `C:\Windows\System32\conhost.exe --headless C:\Holocron\holocron.exe --config ... up --detach`; both in the configuration's folder |
| Windows | `service` (opt-in, admin) | Windows service `Holocron` ("Holocron NVR"), automatic (delayed) start, LocalSystem unless `--account` | `"C:\Holocron\holocron.exe" --config ... service` |
| Linux | `systemd` | user unit `~/.config/systemd/user/holocron.service` (`$XDG_CONFIG_HOME` honoured), `WantedBy=default.target` | `holocron --config ... host --service` |
| macOS | `launchd` | agent `~/Library/LaunchAgents/com.holocron.nvr.plist`, `RunAtLoad`, `KeepAlive` with `SuccessfulExit=false` | `holocron --config ... host --service`, output to `~/Library/Logs/com.holocron.nvr.log` |

Every entry passes `--config` with the absolute path of the configuration `install` was run with
(and `--root` when `install` was given one), so it does not depend on the working folder.

- **Logon task settings:** no execution time limit, not stopped or held back on battery, a second start
  is ignored (`IgnoreNew`; the instance lock protects as well), started late when the logon was missed,
  priority 5 (normal; the Task Scheduler's default 7 is below normal and `ffmpeg` would inherit it). It
  runs only while you are logged on (interactive token), which is what puts the console on your desktop.
  The task itself is listed in the Task Scheduler window (its "hidden" setting is off; that setting only
  hides the task in the list, never a console window).
- **The console window (`--window`, Windows logon task only):**
  - `visible` (default): at logon the attached `holocron up` opens in a console window. That window IS
    the system, as with `holocron up` typed by hand: **closing it stops everything in order**. That is the
    "window closed" stop (`StopKind` `window-closed`, one alert 103), not an outage: nothing restarts it
    until the next logon, `holocron up`, or `holocron up --detach`. Ctrl+C in it is an ordinary stop.
  - `hidden`: `conhost.exe --headless` runs `holocron up --detach`, so there is no window to close by
    accident; `holocron down` stops it.
  - A first `install` is `visible`; a later `install` without `--window` keeps the recorded choice, and
    `--window` with the other value is an update (reported, recorded in `install.json`, shown by
    `autostart status` and its `--json` key `autostart.window`). Other modes refuse `--window` (exit 2).
- **Who restarts what:** the host inside `holocron` restarts the supervisor in place (5 times in
  30 minutes); the OS entry only starts `holocron` and restarts it as a last resort: systemd
  `Restart=on-failure` after 10 s but never after exit 20 (another supervisor holds the archive) or 21
  (configuration fault); launchd after a non-zero exit, at most every 10 s; the Windows service 60 s
  after a failure, three times, the count reset after a day. The logon task does not restart anything.
- **Why `host --service` and not `up` under systemd and launchd:** they stop a service with SIGTERM.
  Attached `up` reads SIGTERM as a closed window (alert 103); the host in service mode reads it as an
  ordered, requested stop, the same as `holocron down`.

`install` starts nothing now: the entry starts `holocron` at the next logon or boot. To start it now,
`holocron up --detach` (or `systemctl --user start holocron.service`, `sc start Holocron`).

## Install, per OS

Put `holocron` where it will stay first. `install` refuses an executable under a temporary folder or in
the `go run` build cache (an entry pointing at it breaks when it is cleaned up) unless `--allow-temp`.

```
holocron install --dry-run     # every command and the generated task XML / unit / plist; changes nothing
holocron install               # create it, read it back to verify, record it
holocron autostart status      # installed, enabled, what it runs, whether that executable exists
```

**Windows (logon).** No elevation. `install` first reads the old system's tasks
`IPCCTV-Recorder`, `IPCCTV-Dashboard` and `IPCCTV-Ui` (read only) and refuses while any of them is
enabled, because both systems would start at logon and fight for the ports and the cameras; it prints
the commands to disable them (they are yours to run) or goes on with
`--i-know-old-tasks-are-enabled`. `autostart status` lists the three tasks read only, and `autostart enable`
makes the same check as `install`. The task is created with `schtasks /Create /XML` from a task XML that
`holocron` generates and checks, read back with `schtasks /Query /XML` and compared. Check it yourself
with `schtasks /Query /TN Holocron /V /FO LIST`.

**Linux (systemd).** A user unit, never a system unit, never `sudo`: `systemctl --user daemon-reload`
and `systemctl --user enable holocron.service`. It starts when your systemd user manager starts, which
is at your login. Starting at boot without logging in needs lingering, which `holocron` does not switch
on by itself; `install` prints the command, `loginctl enable-linger <you>`. Check with
`systemctl --user status holocron.service` and `journalctl --user -u holocron.service`.

**macOS (launchd), not tested on a Mac.** The plist is written and switched on with
`launchctl enable gui/<uid>/com.holocron.nvr`; launchd loads it at your next login
(`launchctl bootstrap gui/<uid> ~/Library/LaunchAgents/com.holocron.nvr.plist` loads it now).

**A second `install`** reports `already installed, unchanged`, or replaces the entry and lists what
changed (for example the executable after you moved it); it never makes a second entry. A step that
fails undoes what that run did: an entry it created is removed, one it replaced is put back.

## Switch it off and on

`holocron autostart disable` switches the entry off and keeps it (task disabled, `systemctl --user
disable`, `launchctl disable`, service start type disabled); `holocron autostart enable` switches it on
again (and checks the old tasks first, like `install`). Neither starts or stops a running `holocron`:
`holocron down` and `holocron up --detach` do that.

`holocron status` and `holocron doctor` show the autostart line; `doctor` warns when the executable the
entry runs no longer exists (the binary was moved or deleted: run `install` again from the new place)
and, on Windows, lists the old tasks' state.

## Switching between the PowerShell system and `holocron`

Both directions are reversible and leave every file in place. The old system is a separate program: stop
and start it by its own means, as its own documentation in the original repository says. `holocron` does
not do that for you. Run the commands below in Windows PowerShell.

From the old system to `holocron`:

```powershell
# 1. stop the old system by its own means (recorder, dashboard and its web UI)
schtasks /Change /TN IPCCTV-Recorder /DISABLE     # the old system no longer starts at logon
schtasks /Change /TN IPCCTV-Dashboard /DISABLE
schtasks /Change /TN IPCCTV-Ui /DISABLE
C:\Holocron\holocron.exe install                  # or: autostart enable, when it is installed already
C:\Holocron\holocron.exe up --detach              # start it now instead of at the next logon
```

The `holocron` web app listens on `127.0.0.1:8787` by default (`web.listen`). If the old system's web UI is
still running it may hold that port: stop it by its own means first or give `holocron` another `web.listen`.
`holocron doctor` warns when the port is held by another program.

Back from `holocron` to the old system (the full procedure is [rollback.md](rollback.md)):

```powershell
C:\Holocron\holocron.exe down                     # stop everything, proven by the census
C:\Holocron\holocron.exe autostart disable        # keep the entry, switched off
C:\Holocron\holocron.exe archive flatten --now    # the old system reads only the flat layout (dry run first, without --now)
schtasks /Change /TN IPCCTV-Recorder /ENABLE
schtasks /Change /TN IPCCTV-Dashboard /ENABLE
schtasks /Change /TN IPCCTV-Ui /ENABLE
# 2. start the old system by its own means (original repository, branch master)
```

## Windows service mode

`holocron install --mode service` is opt-in and needs an elevated (Run as administrator) prompt, as do
`uninstall` and `autostart enable|disable` of the service. It says the following before it asks, and asks
for `yes` (`--yes` skips the question; `--dry-run` asks nothing and changes nothing):

- the service starts at boot, before anyone logs on, as LocalSystem (or the `--account`: only accounts
  without a password, `NT AUTHORITY\LocalService`, `NT AUTHORITY\NetworkService`, `NT SERVICE\<name>`);
- that account has no Credential Manager and its own profile, so the camera login must be in the
  encrypted file store: `secrets.backend` `file` with `secrets.file` and `secrets.keyFile` set to absolute
  paths that account can read (the defaults are in the installing user's profile). `install` refuses
  otherwise and says how to fix it. `HOLOCRON_SECRET_KEY`, if you use it instead of the key file, must be
  in the service's environment;
- the archive root, the configuration and the web app's folders (`web.authDir`, `web.tlsDir`, whose
  defaults are in your profile) must be usable by that account;
- there is no console: the service manager's Stop and the system shutdown are an ordered stop, the same
  as `holocron down`.

`uninstall` marks the service for deletion; a running service goes when it stops, and `uninstall` then
says to stop it and run `uninstall` again. Real service install is not tested: the tests run against a
fake service manager (no elevation was taken).

## Uninstall

`holocron uninstall` removes exactly what `install` created, as recorded in
`<config folder>\_state\install.json` (mode, entry name, executable, command, time, version), verifies it
is gone and deletes the record. It does not stop a running `holocron` (`holocron down` does). An entry
with holocron's own name but no record (for example after the record was deleted) is refused unless
`--force`; nothing else is ever removed. `--dry-run` prints every command.

## Known issue: Microsoft Defender

Documented-only (record: `internal/autostart/README.md`, "Verified for real"; not reproducible from code).
On 2026-10-21 Microsoft Defender flagged an unsigned test build of `holocron.exe` that had just created a
(disabled) logon task as `Behavior:Win32/Persistence.A!ml` and blocked the file; that build ran from
`C:\Windows\Temp`. The same day an unsigned build from a permanent folder under the user profile created a
disabled test task with `--window visible`, replaced it with `--window hidden` (`conhost.exe --headless`),
switched it on and off, reinstalled it unchanged and uninstalled it: Defender raised nothing (8 detections
before, 8 after) and the binary kept running. So the temporary folder, not `conhost --headless`, is the
likely trigger; install from the folder `holocron.exe` will live in. This is one machine and one day, not a
guarantee; a signed binary is the durable fix (UNVERIFIED: no signed build was tried).

## For tests

The hidden `--name` flag and the variable `HOLOCRON_INSTALL_NAME` change the entry name; only names with
`-TEST-` in them are accepted, so a test can never create, switch or remove the real `Holocron`,
`holocron.service` or `com.holocron.nvr`. The hidden `--test-disabled` (test names only) creates the
entry switched off; a logon task made with it also has its logon trigger off, so `autostart enable` of a
test task still cannot fire.
