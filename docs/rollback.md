# Rollback

## When to roll back

Roll back only what is broken. A misbehaving new build is A; wanting the flat archive layout back is B; leaving Holocron altogether is C, which includes B. Run `holocron doctor` first: most problems have a fix on its `warn` and `fail` lines and need no rollback.

Three ways back, from the smallest to the largest:

- [A. A Holocron build that misbehaves](#a-roll-back-a-holocron-build): go back to the previous `holocron.exe`.
- [B. The day-folder archive layout](#b-roll-back-the-archive-layout): put every segment back flat in its camera folder.
- [C. The old PowerShell system](#c-back-to-the-powershell-system): stop Holocron and start the system it replaced, which lives in the original repository `windows-terminal-IPCCTV-dashboard` (branch `master`), not here.

The commands below write `holocron`; on Windows that is the full path of `holocron.exe` in its permanent folder, with `--config <file>` before the command when the configuration is not beside the executable. Every command and output quoted here is in the command registry (`holocron help`) or its source; a step that was never run on a real machine says so.

## A. Roll back a Holocron build

Holocron does not keep old builds itself. Before replacing `holocron.exe`, keep the running one beside it as `holocron.exe.prev` (a convention of this page, not something any command creates). To go back:

1. `holocron down` - stops the host, the supervisor, the relays and the web app in order and proves it: `stopped: 0 survivors after <x>s` and exit 0, or `DID NOT STOP` with the survivors and exit 1. Run it again on exit 1; never start a second copy over survivors.
2. Swap the files: rename `holocron.exe` to something like `holocron.exe.bad` and `holocron.exe.prev` to `holocron.exe`. The autostart entry runs the path `holocron install` recorded, so keeping the file name keeps autostart pointing at the right program (`holocron autostart status` shows what it runs and whether that executable exists).
3. `holocron up --detach` - starts everything in the background and waits for an honest start.
4. `holocron doctor` - ends with `HEALTHY` when all is well; every `warn` and `fail` line carries its fix. `holocron version` confirms which build is running.

The configuration (`holocron.config.json`, `cameras.json`), the secret store and the archive are shared by every build, so nothing else needs to change unless the newer build wrote a key the older one rejects; `holocron config validate` says so, and `holocron config unset <key>` removes it.

## B. Roll back the archive layout

### R2a. Put the archive back flat

Holocron's mover files closed segments into day folders, `<Camera>\YYYY-MM-DD\` (decision D20, key `MoveToDayFolders`). `holocron archive flatten` undoes that: it puts every filed segment back into its camera folder, the flat layout the PowerShell system reads, and removes each day folder it empties; misfiled and foreign files stay and are listed. It is refused, even as a dry run, while the recorder runs, so stop Holocron first.

```
holocron down
holocron archive flatten
holocron archive flatten --now
holocron archive stats
```

The first `flatten` is a dry run that lists per camera what would come back. `--now` applies it; it exits 1 when it is refused or a conflict or error remains, and `--camera NAME` limits it to one camera. Afterwards `archive stats` should show no segments filed in day folders.

To stay flat while running Holocron, `holocron config set MoveToDayFolders false` before the next `holocron up`; otherwise `up` files the segments again. To go forward again later, `holocron archive migrate` (a dry run) and `holocron archive migrate --now` file the flat history back into day folders; it is resumable and safe while the recorder runs.

## C. Back to the PowerShell system

The PowerShell system is a separate program. It was removed from this repository and lives complete in the original repository `windows-terminal-IPCCTV-dashboard`, branch `master`; how to install, start and verify it is that repository's documentation, not this one. The cutover that moved the NVR to Holocron is recorded in [tasks/holocron/cutover-runbook.md](../tasks/holocron/cutover-runbook.md), including what was written down about the old system on that day.

The steps that belong to Holocron, in order:

1. **Stop Holocron and prove it:** `holocron down` (as in A.1). Do not go on while anything survives: the old system and Holocron must never record the same root at once, and a supervisor that finds another one holding the archive exits 20.
2. **Check that nothing is left:** `holocron status` exits 1 and reports the supervisor as not running. `holocron doctor --group system` lists stray `ffmpeg` or MediaMTX processes.
3. **Put the archive back flat:** [R2a](#r2a-put-the-archive-back-flat). The old system lists only the files directly in a camera folder, so filed days would be invisible to it and never pruned by it.
4. **Switch Holocron's autostart off:** `holocron autostart disable`, then `holocron autostart status`. It keeps the entry, so `holocron autostart enable` brings it back; `holocron uninstall` removes exactly what `install` created if you will not return. `autostart status` also lists the old system's tasks, read only; Holocron never changes them ([autostart.md](autostart.md#switching-between-the-powershell-system-and-holocron)).
5. **Start the old system** from its own folder, by its own documentation in the original repository: re-enabling its autostart tasks if it used them, and checking it is healthy by its own means.

What Holocron changed that the old system reads differently:

- **Credentials.** If they were moved with `holocron secret import`, the old `camera_creds.xml` was not deleted by it, so the old system still finds it.
- **Cameras and settings.** Changes made under Holocron (cameras added, recording switched) are in Holocron's `cameras.json` and `holocron.config.json`. Whether and how the old system reads them is that repository's matter (UNVERIFIED from here); carry them over by its own means.
- **The last segment.** `holocron down` kills `ffmpeg`, so the file each camera was writing has its streams but no duration in its header; MKV plays regardless.

Coming back to Holocron afterwards: stop the old system by its own means, then `holocron doctor` (its `system` group fails while the old system's supervisor runs), `holocron up --detach` and `holocron autostart enable`.
