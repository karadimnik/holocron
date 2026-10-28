# Repository cleanup - final record (28 Oct 2026)

Rule (Operator, 2026-10-28): the Holocron repository contains only Go and Holocron-related artifacts. This file is the record of what was removed, what moved to the branch `spikes`, what stays and why. It was an audit with open decisions; the decisions are made and carried out, so it is now a record.

## Decisions of 2026-10-28

1. **Stress tests are kept.** `tests/stress/` (the Stage 0 live-view testbed, its raw data and report) stays in the repository on purpose, including its PowerShell scripts.
2. **Spikes live on a branch.** The design spikes (Go programs sp1b, sp2, sp3, sp4, sp6, their runners and the two SP reports) moved to the branch `spikes`.
3. **The PowerShell system is retired from this repository** (decision D23, [os-independent-hld.md](os-independent-hld.md)). It stays complete in the repository `windows-terminal-IPCCTV-dashboard`, branch `master`; rollback to it uses that repository, not this one.
4. **History is not rewritten.** The Operator will re-initialise the repository; until then every removed file is still reachable in git history, and the original repository holds the PowerShell system.

## What was removed

Done in task R5 (stress tests and spikes), task B3a (the PowerShell-era web app in Go) and task B3b-1 (the PowerShell system).

| Artifact | Size | What it was for | Why it could go |
|---|---|---|---|
| `cam.ps1`, `cam.recorder.ps1`, `cam.common.ps1`, `cam.health.ps1`, `mpv.cam.autolayout.autorestart.ps1` | 585 KB | The PowerShell recorder, supervisor, health strip and viewer that Holocron replaced on 2026-10-22 | Retired (D23); no Go test or build step reads them any more (the facts the tests need are in `testdata/`, task B1) |
| `mediamtx.template.yml`, `recorder.config.example.json` (repository root) | 6 KB | The PowerShell system's relay template and config example | The Go module embeds its own template (`internal/relay/mediamtx.template.yml`) and reads fixtures from `testdata/config/` |
| `tests/cam.tests.ps1`, `tests/cam.testlib.ps1` | 852 KB | The PowerShell test harness, including the `doclinks` and `docswitches` doc checks | Replaced: the link check is the Go test `TestDocLinksResolve`; `docswitches` compared documents with `cam.ps1` parameters and has no subject left |
| `tests/holocron/*.ps1` (9 generators) | 103 KB | Regenerated the 231 goldens from the PowerShell implementation | That implementation is gone from here. The goldens stay, frozen; their provenance and how to regenerate are in `testdata/golden/README.md` |
| `tests/live/` (5 files) | 37 KB | Browser test page and server of the PowerShell-era live view | The web app is Go (`internal/web`) |
| `ui/` (3 shims) | 2 KB | Build and apply shims for the nested UI module | The nested module is gone (B3a); `holocron web` is the only entry point |
| `tasks/ipcctv-recording/` (`todo.md`, `relay-api-auth.md`) | 111 KB | The old project's todo and relay design notes | Belongs to the original repository; the Holocron documents that cited it now name that repository and branch `master` |
| `spikes/`, `tests/holocron/sp*`, `tasks/holocron/spikes/*.md` | ~170 KB | Design spikes SP1 to SP7 | Moved to the branch `spikes` |
| `reference/phaseC-mediamtx.yml`, `docs/dashboard.png`, `tests/holocron/.gitkeep` | 386 KB | Reference relay config, screenshot of the PowerShell dashboard, empty-folder placeholder | Removed |

Tracked files: 853 before task B3b-1, 825 after.

The branch `spikes` was created from the commit before the move, so it holds every moved file. `tests/stress/live-view/spike-c.ps1` (Spike C, a probe of the real cameras' streams, never run) lives on that branch; it needs the original PowerShell repository's camera list.

## What stays, and why

| Path | Why |
|---|---|
| `cmd/`, `internal/`, `go.mod`, `build-holocron.sh` | The Go program and its build |
| `testdata/` (95 files, 1.9 MB) | Golden files (frozen outputs of the PowerShell implementation) and config fixtures; the Go differential tests compare against them byte for byte. Untouched by the cleanup |
| `tests/stress/` (186 files, 1.9 MB) | The stress-test testbed and its measurements (decision 1); its PowerShell scripts are deliberate |
| `.github/`, `LICENSE`, `.gitignore`, `.gitattributes` | CI, licence, ignore and line-ending rules (the `.ps1` rule serves `tests/stress`; the secret and key rules are unchanged) |
| `docs/`, `README.md`, `AGENTS.md`, `llms.txt` | The documents. `AGENTS.md`, `llms.txt` and `docs/ai/holocron-for-agents.md` are the AI-facing contract and were corrected in B3b-1 |
| `tasks/holocron/` | Plans, designs, specs, ledger and the cutover record - the history of decisions |

## Still open: legacy documents (task B3b-2)

Closed by task B3c: see [Documents](#documents-task-b3c).

Task B3b-1 only made the repository consistent: it builds, all Go tests pass and no relative link points at a deleted file. These documents still describe or mention the PowerShell system as if it were present and are rewritten or deleted in B3b-2: `README.md` (legacy sections), `docs/install.md`, `docs/reference.md`, `docs/go-toolchain.md`, `docs/limitations.md`, `docs/development.md`, `docs/ui.md`, `docs/how-it-works.md`, `docs/configuration.md`, `docs/troubleshooting.md`, `docs/cutover.md`, `docs/rollback.md`, `docs/shadow.md`, `docs/autostart.md`, `docs/alerts.md`, `docs/holocron-cheatsheet.md`. Go comments and README files in packages that mention `cam.common.ps1` and similar as the origin of a rule are historic and stay.

## Reproducing a removed file

Every file removed here is in git history of this repository (`git log --diff-filter=D -- <path>`, then `git show <commit>^:<path>`) and, for the PowerShell system, in `windows-terminal-IPCCTV-dashboard`, branch `master`.

## Documents (task B3c)

Task B3c made every document describe Holocron only (decision D23). Mentions of the PowerShell system that remain describe it as another program that may still be on the machine (doctor's `system` group, `install` leaving its tasks alone, `secret import`, exit 20) or as the rollback target in `windows-terminal-IPCCTV-dashboard`, branch `master`.

| Document | Disposition |
|---|---|
| `docs/install.md`, `docs/reference.md`, `docs/go-toolchain.md` | Deleted (PowerShell install, script reference, UI toolchain). What was true of Holocron builds moved to `docs/development.md` "Building"; links repointed |
| `docs/cutover.md` | Moved to `tasks/holocron/cutover-runbook.md` with a "Historical" banner; body unchanged except relative links fixed for the new folder |
| `docs/shadow.md` | Moved to `tasks/holocron/shadow-runbook.md` with a "Historical" banner; a new short `docs/shadow.md` documents `holocron archive compare` |
| `README.md` | Rewritten: the opening paragraph, what it offers, quick start, documentation index; stress tests, spikes and licence kept; every PowerShell section deleted |
| `docs/limitations.md` | Rewritten: only limits sourced in code or measurements, each with its source |
| `docs/rollback.md` | Rewritten for Holocron: a previous build, the archive layout (`archive flatten`, anchor `#r2a-put-the-archive-back-flat` kept), and the Holocron side of going back to the PowerShell system |
| `docs/ui.md` | Rewritten as the guide to Holocron's web app from `internal/web` (camui.exe, token-era and PowerShell content removed) |
| `docs/development.md` | Rewritten: building, testing, documentation checks, golden files, fake time, stress tests |
| `docs/how-it-works.md`, `configuration.md`, `troubleshooting.md`, `alerts.md`, `autostart.md`, `ci.md`, `holocron-cheatsheet.md`, `holocron-quickstart.md` | PowerShell content removed or reworded, remaining claims checked against code |
| `AGENTS.md`, `llms.txt`, `docs/ai/holocron-for-agents.md` | Updated to the final document set; structure kept |

Not covered by B3c (a later task, with the Go strings and comments): the package READMEs under `internal/` and `cmd/holocron/README.md`, `internal/config/KEYS.md` and `internal/status/KNOWN-DIFFERENCES.md`, which describe the Go code's parity with the PowerShell origin; user-visible strings in Go and in `internal/web/static/` that still name `cam.ps1`, `camui.exe` or `recorder.config.json`.
