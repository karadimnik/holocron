# Development notes

Mechanics that matter when you are working on this repository rather than using it. The repository is one Go module (`module holocron`, `go.mod`) with one executable, `cmd/holocron`, and its packages under `internal/`.

## Building

- **Go version**: the one `go.mod` names (`go 1.27`). There are no third-party dependencies, so there is no `go.sum`. On the development machine Go lives in `~/.local/go/bin` inside WSL; `build-holocron.sh` puts that folder on `PATH` itself.
- **`./build-holocron.sh`** (WSL or Linux, from the repository root) is the release build: it fails on any file `gofmt -l cmd internal` lists, then runs `go vet ./...` and `go test -p 1 ./...` (one package at a time, because the web and real-process suites are heavy), and only then cross-compiles `holocron.exe` for `windows/amd64` into the repository root, stamping the version from `git describe --tags --always --dirty` into `main.version` and `internal/web.version`. A build that completes is a build that passed.
- **Other targets**: `GOOS=linux go build ./cmd/holocron` and so on. CI cross-builds `windows/amd64`, `linux/amd64`, `linux/arm64` and `darwin/arm64` ([ci.md](ci.md)).
- **Runtime tools are not built here.** Holocron runs `ffmpeg`, `ffprobe` and MediaMTX as child processes. Defaults (internal/config/config.go): on Windows `C:\ffmpeg\bin\ffmpeg.exe`, `C:\ffmpeg\bin\ffprobe.exe` and `C:\mediamtx\mediamtx.exe` (configs `C:\mediamtx\mediamtx.yml` and `mediamtx-live.yml`); elsewhere `ffmpeg`, `ffprobe` and `mediamtx` from `PATH`. The keys are `FfmpegPath`, `FfprobePath` and `RestreamerExePath` ([internal/config/KEYS.md](../internal/config/KEYS.md)); `holocron doctor` checks ffmpeg and MediaMTX (`setup.ffmpeg`, `setup.mediamtx`).

## Testing

| What | Command |
| --- | --- |
| Everything, as CI runs it | `go test -race -count=1 -p 1 ./...` (about 10 minutes) |
| One package | `go test ./internal/web/...` |
| Formatting | `gofmt -l cmd internal` (must print nothing) |
| Vet for every OS Holocron ships on | `for os in linux windows darwin; do GOOS=$os go vet ./...; done` |
| The documentation checks | `go test -count=1 ./cmd/holocron -run 'Doc|Agent'` |

- **Documentation checks.** `cmd/holocron/doclinks_test.go` (`TestDocLinksResolve`) resolves every relative link and `#anchor` in every Markdown file of the repository and in `llms.txt`. `cmd/holocron/docs_test.go` fails when a `holocron ...` command in the quickstart, the cheat sheet, the agent reference, the README or `AGENTS.md` is not in the command registry, or when the command table of [the agent reference](ai/holocron-for-agents.md) differs from the registry; `HOLOCRON_DOCS_UPDATE=1 go test ./cmd/holocron -run TestAgentDocCommandTable` rewrites that table.
- **Golden files.** `testdata/golden/` holds frozen outputs of the PowerShell implementation Holocron replaced, the behaviour the Go code must reproduce; its [README](../testdata/golden/README.md) records the provenance and how to regenerate one (in the original repository `windows-terminal-IPCCTV-dashboard`). Never edit a golden by hand.
- **Fake time.** The host tests run inside a `testing/synctest` bubble and move the fake clock only once the host and every fake are blocked ([internal/host/README.md](../internal/host/README.md)); time arithmetic is measured in UTC and covered by `testdata/golden/time/dst.json` and the `internal/clock` tests.
- **Stress tests.** Load measurements that are run by hand, not by `go test`, live in `tests/stress/` ([live view](../tests/stress/live-view/README.md)).

## Continuous integration

A GitHub Actions workflow on `main` runs gofmt, vet for three operating systems, the race suite on Linux and Windows (macOS best effort) and the cross-builds. What it covers, how to read it and what it leaves out on purpose: [ci.md](ci.md).

## Documentation

The user guides are [holocron-quickstart.md](holocron-quickstart.md) and [holocron-cheatsheet.md](holocron-cheatsheet.md); the complete reference for AI agents is [ai/holocron-for-agents.md](ai/holocron-for-agents.md), indexed by `llms.txt` and with the repository rules in `AGENTS.md`, both at the repository root. Design documents, decisions and records of past work are under `tasks/holocron/`.
