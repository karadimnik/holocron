# Instructions for AI agents in this repository

Holocron is the live NVR of the Operator's home (one Go executable, branch `main`). This repository contains only Holocron: the earlier PowerShell system was removed from it and lives complete in the repository `windows-terminal-IPCCTV-dashboard` (branch `master`), the rollback target. Read [docs/ai/holocron-for-agents.md](docs/ai/holocron-for-agents.md) before changing anything; [llms.txt](llms.txt) indexes every document. The Operator's guides are [docs/holocron-quickstart.md](docs/holocron-quickstart.md) and [docs/holocron-cheatsheet.md](docs/holocron-cheatsheet.md).

## Hard rules

1. **Never touch the Operator's live system.** It is running and records 24/7. Do not start, stop or kill `holocron`, `ffmpeg` or `mediamtx` processes you did not start; do not touch the archive root (`E:\cameras`), the installed program and its configuration (`C:\Holocron`), the Credential Manager, `%LOCALAPPDATA%\IPCCTV`, the repository `windows-terminal-IPCCTV-dashboard`, scheduled tasks, firewall rules, certificates or Defender. Work in a throwaway root (under `C:\Windows\Temp` or a scratch folder) with `HOLOCRON_SANDBOX_ONLY=1`, and run the binary you just built, not the installed one. Before and after, list the processes and prove you left none behind.
2. **Never open a credential file** (`camera_creds.xml`, `relay_api_creds.xml`, `live_view_secret.txt`, `relay_api_secret.txt`, `secrets.enc`, `secrets.key`, `users.json`, `sessions.json`, any `*.key`) and never print or log a secret. No real password, camera name, LAN address, user or machine name in a document, test or log: use `192.168.1.50` and `Front Door`, and fake credentials.
3. **R1: one executable.** Every action is a subcommand of `holocron`; no script or second program. **R2: every action is logged** through `cli.Action`. A change that breaks either is rejected ([how to add a command](docs/ai/holocron-for-agents.md#12-how-to-add-a-command)).
4. **Commits.** Never commit to `main`: branch `<role>/<task-slug>` from it, one commit per logical change (change, tests and doc line together), each building and passing tests alone. Subject `<area>: <imperative summary>`, body why / what / how verified, trailers `Task: <id>` and `Decision: <id>`. No agent as co-author. Executors never push, merge or tag: the advisor merges and pushes quality-checked work under the Operator's standing approval (releases: rule 7); merges are `--no-ff`, recorded in [tasks/holocron/ledger.md](tasks/holocron/ledger.md), undone with `git revert -m 1`.
5. **Archive layout (D20).** `<root>\<Camera>\` is the spool ffmpeg writes into; the mover files closed segments into `<root>\<Camera>\YYYY-MM-DD\`. Never write, move or delete footage there by hand: use `holocron archive migrate` / `archive flatten`, and list or build archive paths only through `archive.Layout`.
6. **No em dash** in any text; use `-`. `rm`, `cp` and `mv` may be interactive aliases: use `command rm -f`.
7. **Releases.** Only the advisor cuts a release tag, from `main`, after CI is green and `CHANGELOG.md` has the version's section, following [docs/releasing.md](docs/releasing.md); every merge that changes shipped behaviour is released. Executors and sub-agents never create or push a tag or a release. Release notes come only from `CHANGELOG.md`: add your change's line under `## [Unreleased]` in the same commit.
8. **Do not edit** `tests/stress/`, `testdata/` or `.github/` unless the task says so: the first is the stress-test testbed (its PowerShell scripts are deliberate), the second holds the golden and config fixtures the Go tests compare byte for byte, the third is CI. The PowerShell system is not in this repository and must not be recreated here (it lives in `windows-terminal-IPCCTV-dashboard`, branch `master`); Holocron only reads its scheduled tasks, never changes them.

## Run the tests

```bash
export PATH="$HOME/.local/go/bin:$PATH"
test -z "$(gofmt -l cmd internal)"
for os in linux windows darwin; do GOOS=$os go vet ./...; done
go test -race -count=1 -p 1 ./...
```

Documentation: the Go drift guard (`cmd/holocron/docs_test.go`) fails when a document names a command that is not registered or the command table in the agent doc differs from the registry (`HOLOCRON_DOCS_UPDATE=1 go test ./cmd/holocron -run TestAgentDocCommandTable` regenerates it). Every relative link and `#anchor` in the Markdown documents is checked by the Go test `TestDocLinksResolve` (`cmd/holocron/doclinks_test.go`, part of `go test ./cmd/holocron`; run it alone with `go test ./cmd/holocron -run 'Doc|Agent'`). Windows reserves the file names `aux`, `con`, `nul`, `prn`, `com1`, `lpt1`: never create such a file. More: [testing](docs/ai/holocron-for-agents.md#11-testing).
