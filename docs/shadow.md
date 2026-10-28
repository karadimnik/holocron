# Comparing two archives: `holocron archive compare`

`holocron archive compare` proves that two archives of the same cameras hold the same recordings: the segment names, clock boundaries, sizes, ffprobe durations, stream layout, audio and gaps. It was built for the shadow run, in which Holocron recorded a copy beside the PowerShell system before the cutover (the procedure and its result are a record: [tasks/holocron/shadow-runbook.md](../tasks/holocron/shadow-runbook.md)); it works on any two roots, for example an archive and a restored copy of it. The code is `internal/shadow` and `cmd/holocron/comparecmd.go`.

It only reads. It lists both roots, stats the files and runs `ffprobe` on closed segments; it never writes, renames or deletes anything, and it creates no log file and no folder under either root.

## Usage

```
holocron archive compare --old ROOT --new ROOT [--camera NAME ...] [--since TIME] [--json] [--ffprobe PATH]
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--old`, `--new` | (required) | the two archive roots |
| `--camera NAME` | every camera folder of `--new` | repeatable |
| `--since TIME` | the start of the overlap | RFC 3339, `"2006-01-02 15:04"`, or a duration such as `36h ago` |
| `--ffprobe PATH` | `FfprobePath` of `--config`, then `PATH` | the ffprobe to run |
| `--audio SPEC` | `pcm_alaw:8000:1` | expected `codec:rate:channels`, or `any` (the two sides must still agree) |
| `--segment-seconds N` | `SegmentSeconds` of `--config`, else 900 | segment length |
| `--start-tolerance D` | `5s` | starts this close are a pair; also the allowed clock-boundary jitter |
| `--duration-tolerance D` | `2s` | ffprobe durations of a pair may differ by this much |
| `--gap-tolerance D` | `2s` | a gap between segments longer than this is reported |
| `--json` | off | the report as JSON, schema 1 |

The last four are accepted by the command (`comparecmd.go`) but not shown in `holocron help`'s one-line usage.

Exit codes: `0` equal within the tolerances, `1` differences (the report is printed), `2` usage (a missing or equal root, a bad value, no ffprobe).

## What it compares, per camera

- **Closed segments only.** A file written in the last 90 seconds is open and skipped; so is the newest segment of a folder until it is one segment length and a minute old.
- **The window** is the overlap of the two archives, from the later first start to the earlier last end; `--since` moves the start later.
- **Pairs**: segments whose starts differ by at most the start tolerance. For each pair: the size ratio new/old (outside 0.9 to 1.1 is a difference), the ffprobe durations, the time zone suffix of the names, the video codec and size, and the audio.
- **Partials** (a side's first file, or the part after a restart) are reported, not counted as missing; a pair cut short by the end of the window is `partial_end` and is not compared for size or duration.
- **Layouts.** Each root may be flat or filed into day folders (`<Camera>\YYYY-MM-DD\`, decision D20); segments are found in both.
- **Gaps** longer than the gap tolerance on each side, and segments present on one side only.

## The JSON report (`--json`, schema 1)

Top level: `schema`, `old`, `new`, `checked_at`, `params` (the settings used), `equal`, `differences` (the count) and `cameras[]`. Per camera: `camera`, `equal`, `window`, `old` and `new` (per-side counts and gaps), `pairs[]`, `partials[]`, `unmatched_old[]`, `unmatched_new[]`, `size_ratio`, `differences[]` and `notes[]`. The field names are the `json` tags of `internal/shadow/compare.go`, which is the authority.
