# Golden files

Pinned behaviour of the PowerShell recorder and supervisor, the specification the Go
`holocron` executable must reproduce (tasks/holocron/os-independent-lld.md, W0a and W0b).
Every file is a frozen output of the PowerShell product's own functions, called with FAKE inputs
(cameras `Camera One` ..., documentation addresses `192.0.2.x`, user `fakeuser`, password
`fakepass`). Nothing real is read. Every file that carries a fake credential also carries the
marker `SYNTHETIC` so a leak scan can tell it from a real one.

`.gitattributes` marks this folder `-text`: the files are compared byte for byte (the MediaMTX
configs are CRLF, as the product writes them).

| Path | What | Produced by (generator in `tests/holocron/` of the original repository) | Go test |
|---|---|---|---|
| `status/*.json` | status.json documents written by `Write-CamStatusFile` and the two patch functions: running (full, with live, first write, winter offset), stopped (requested, interrupted, unexpected), patched (unexpected, window closed; minimal and over a running file), after a restart (finding F1), unknown keys | `status-docs.ps1` `New-HolocronStatusDocuments` (-Deterministic: fixed clock, free space and pid) | `internal/status/conformance_test.go` |
| `status/negative/*.json` | the same documents broken in exactly one way each (missing field, wrong type, bad enum, bad timestamp, ...) | `New-HolocronNegativeStatusDocuments` | same, with the expected violation per file |
| `ffmpeg/*.json` | the exact ffmpeg argument array (before quoting) and the quoted command line for every variant: mkv and fmp4, relay and direct input, 900 s and 7 s segments, a space in the archive root, udp transport, a profile with AudioArgs/ExtraArgs/SegmentFormatOptions; `quoting.json` is the `Get-CamQuotedArgument` table | `golden-ffmpeg.ps1` | `internal/golden` |
| `relay/*.yml`, `live/*.yml`, `relay-live-inputs.json` | generated MediaMTX configs (relay: default, live off, live on - the three are identical; custom addresses. live: default, sub stream and close time, LAN) and the fake inputs behind them | `golden-relay.ps1` | `internal/golden` |
| `names/safe-names.json` | `Get-CamSafeName` / `Get-CamRelayPathName` over 61 awkward camera names | `golden-names.ps1` | `internal/golden` |
| `names/segments.json` | segment name pattern per container, names for sample instants, accept/reject lists of `Test-CamSegmentName` (with `trap` rows for oddities the product has) | `golden-names.ps1` | `internal/golden` |
| `time/dst.json` | instants around 2026-10-25 04:00->03:00 and 2027-03-28 03:00->04:00 in `GTB Standard Time` (Europe/Bucharest): the `Format-CamTimestamp` text, read-back, offsets, segment suffix, log stamp, interval and heartbeat results | `golden-time.ps1` | `internal/golden` (re-derived with Go's own zone database) |

## Provenance

These files are frozen outputs of the original PowerShell implementation, produced 2026-10-17 to 2026-10-20 in the repository `windows-terminal-IPCCTV-dashboard` (branch `portable-nvr`, generator commits `3297a4a` to `ae25c70`) and compared by the Go tests of the Holocron differential suite. The generators (`tests/holocron/regen-goldens.ps1`, `golden-*.ps1`, `status-docs.ps1`, `prune-diff.ps1`) and the PowerShell product they call were removed from this repository on 2026-10-28; they stay in its git history (for example `git show ae25c70:tests/holocron/regen-goldens.ps1`) and in `windows-terminal-IPCCTV-dashboard`.

To regenerate a golden, check out that history, run the generator under Windows PowerShell 5.1 in the zone `GTB Standard Time` from a local folder, review the diff, and commit the new files here together with the Go change that needs them. Without that, treat the goldens as read-only: the Go tests compare them byte for byte.
