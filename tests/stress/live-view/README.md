# Live-view stress tests (Stage 0)

The load tests that decided how the Holocron live view should be built: how much CPU and GPU does a browser (Chrome, Edge) or mpv use to show six camera streams, at which resolutions, with hardware decode on or off, and what happens to a hidden tab. They were run once, on 2026-10-15, before any live-view code existed, and they stay in the repository as the evidence behind the "sub-stream in the grid, main stream on focus" tile policy (decision D3 in [tasks/holocron/plan.md](../../../tasks/holocron/plan.md)).

This file is the single entry point: it says what the testbed is, what was run, where each result is, and how to run it again. The measured findings and their reasoning are in [stage0-report.md](stage0-report.md); every result table is in [stage0-tables.md](stage0-tables.md).

The names "Spike A", "Spike B" and "Spike C" below are the Stage 0 test groups. They are not the design spikes SP1 to SP7, which live on the branch `spikes` (see the root [README](../../../README.md)).

## What was tested

| Group | What it measured | Cells |
|---|---|---|
| Spike B | Six simultaneous WebRTC tiles in a test page, 30 s warm-up then 180 s sampled, in four layouts (see below), Chrome 155 and Edge 155, hardware decode on and off; the same six streams in six mpv windows as baseline | 16 browser cells + 2 mpv cells |
| Hidden tab | Six 2560x1440 tiles, hardware decode on; the window is minimized 60 s into a 130 s sample | 2 (Chrome, Edge) |
| Noise floor | The rig and the sampler only, no browser, 60 s | 1 (`B-idle`) |
| Decoder identification (`I2-*`) | One tile (640x360 or 2560x1440), 10 s, a fake audio capture held so Chromium exposes `getStats().decoderImplementation` | 8 |
| Spike A | One 2560x1440 tile per browser: a clean 180 s run, then a 60 s probe run (audio unmuted, burned-in barcode latency probe) | 5 (`A-*`, includes one 20 s Firefox attempt) |
| Spike C | Probe of the real cameras' stream1/stream2 with ffprobe (resolution, codec, fps, bit rate, B-frames, audio) | written, NOT run against real cameras |

The four Spike B layouts: (i) six 2560x1440 "main" streams, (ii) six 640x360 "sub-stream" stand-ins, (iii) six 1280x720, (iv) five 640x360 plus one 2560x1440 focused (3x3 grid, the main tile spans 2x2). Cell names in `data/`: `B-main6-*`, `B-sub6-*`, `B-hd6-*`, `B-focus-*`, `B-main6-<browser>-hidden`, `B-idle`, `B-<layout>-mpv`, `A-<browser>-clean|probe`, `I2-<browser>-hw<on|off>-<m1|s1>`.

## Known limitations and caveats (preserved from the report)

- Spike A (`A-*`) is TRANSPORT-LIMITED: it ran over ICE-over-TCP by accident and is NOT representative of real UDP viewing. Its latency, first-frame and CPU figures must not be quoted as UDP results. The `I2-*` decoder identification does not depend on transport and stands, but the `I2-*` CPU figures are not valid (they also ran over ICE-TCP). The UDP rerun (`A2-*`) did NOT run and its partial data was discarded.
- The 16 Spike B browser cells, the hidden-tab runs and the mpv baseline ran over UDP (without a capture permission, with the TCP listener off).
- Firefox never connected to the loopback-only sandbox (ICE failed), so there are NO Firefox numbers; one 20 s attempt is recorded as `A-firefox-clean`.
- The numbers were taken on a busy PC (the operator's normal desktop was running): the machine was at 75% CPU with no browser at all, and 85-100% in every cell. Absolute CPU figures include contention and are pessimistic; comparisons between cells are the safer reading. The sampler itself costs about 46% of one core.
- Single run per cell, no repeats: differences of a few points between cells are inside the noise. Edge with hardware decode on at 640x360 is marked marginal (47 freezes); its hardware-off twin was clean, read as contention, not repeated.
- The clips are synthetic (`testsrc2` plus temporal noise, 20 fps assumed): lower texture than a real camera scene, so decode cost may be understated.
- Latency is not measured to the millisecond: the barcode estimate has an uncertainty of about 0.1-0.5 s, loopback only, no camera or network in it.
- Not measured: Firefox; any LAN, Wi-Fi or phone viewer; real cameras; the real relay path; tab memory over 24 h.
- Launching an installed Firefox for the tests applied a pending update to the operator's real Firefox (finding C in the report). Check for a staged update first, or use a portable copy.
- Hardware decode ran on the GTX 1070 Ti only; the HD 530 showed 0 in every cell.

## Testbed

Hardware and software, as stated in [stage0-report.md](stage0-report.md) and the scripts:

- Machine: the operator's PC, i5-6600K (4 cores / 4 threads), 32 GB RAM, NVIDIA GTX 1070 Ti plus Intel HD 530, Windows (exact version not recorded in the report). Everything was measured on this PC while the operator's normal desktop was running.
- Browsers: Chrome 155, Edge 155, Firefox 157.0.1 (updated to 158.0 during the tests, see the report). Hardware decode off is `--disable-accelerated-video-decode` (Chromium) or `media.hardware-video-decoding.enabled=false` (Firefox).
- Player baseline: mpv with the flags of the existing dashboard (software decode, no `--hwdec`), plus `--ontop` and `--input-ipc-server`.
- Relay: a sandbox MediaMTX v1.21.2 on loopback only (WHEP `127.0.0.1:18889`, UDP `18189`, RTSP `18554`, API `18997`, collector `18080`), so the operator's real ports (8554, 9997) are untouched.
- Publishers: `ffmpeg -re -stream_loop -1 -c copy -f rtsp` of three pre-encoded 30 s looping clips (`make-clips.ps1`): libx264 Main, no B-frames, GOP 40, 20 fps, yuv420p, G.711 A-law mono 8 kHz audio; 2560x1440 at 1.5 Mbps, 1280x720 at 1.0 Mbps, 640x360 at 0.5 Mbps. Paths `m1..m6`, `h1..h6`, `s1..s6`.
- Fixed install paths the scripts expect (see `common.ps1`, `measure.ps1`, `mpv-baseline.ps1`): `C:\mediamtx\mediamtx.exe`, `C:\ffmpeg\bin\ffmpeg.exe` and `ffprobe.exe`, `C:\mpv\mpv.exe`, Chrome and Edge and Firefox in their default Program Files folders. Windows PowerShell 5.1. Python 3 for `analyze.py` and `make-tables.py`.
- Runtime folder: `%TEMP%\stage0` (clips, MediaMTX config and log, `state.json` with every started pid, raw data). Never a UNC path.

How a cell is run (`measure.ps1`): a browser is launched with a throwaway `--user-data-dir`, on the test page served by the collector, the window is forced to 0,0,1920,1080 and TOPMOST, the display is held awake, and the process is sampled FROM OUTSIDE (CPU per process and busiest thread every ~2 s, GPU engine utilization by adapter every ~3 s, system CPU), while the page posts `getStats` every 2 s to the collector. Test-browser processes are found by the unique profile path and the descendants of the launched pid, never by image name, and are killed by pid at the end.

Verdict rule (applied by `analyze.py`): smooth = every tile at least 19 fps decoded, at most 1% frames dropped by the decoder, at most 2% dropped at render, worst tile frozen at most 0.5% of the window; marginal = at least 15 fps, at most 5% dropped, freezes at most 3%; otherwise unusable.

## Files

| File | What it does |
|---|---|
| `common.ps1` | Shared settings (ports, paths, clip sets, 20 fps), pid state file, process-tree helpers, window and screenshot helpers, `Keep-Awake` |
| `rig-up.ps1` | Starts sandbox MediaMTX (config written to `%TEMP%\stage0\mtx`) and the collector; records every pid in `state.json`; refuses to start if a port is in use |
| `rig-down.ps1` | Stops everything started, by pid, after checking the pid still has the expected image name |
| `make-clips.ps1` | Encodes the three looping clips once, with a burned-in frame counter and a 10-bit frame-number barcode for the latency probe |
| `pub.ps1` | Starts or stops the `-c copy` publishers of one clip set (`-Action start` or `stop`, `-Set m`, `h` or `s`, `-Count N`) and records publisher start times |
| `collector.ps1` | Local HTTP server: serves the test page, publisher start times (`/t0.json`), and appends every POSTed `getStats` line to `<run>.jsonl` |
| `page/index.html` | The test page: WHEP tiles in `grid` (3x2), `focus` (3x3, first tile spans 2x2) or `single` layout, stats upload, optional barcode probe and unmute |
| `sampler.ps1` | Out-of-process CPU, thread, memory, system and GPU-engine sampler (GPU engines mapped to adapters by LUID) |
| `measure.ps1` | One browser cell: launch, warm up, sample, kill by pid |
| `mpv-baseline.ps1` | The mpv baseline: six mpv windows tiled 3x2 on 1920x1080 (640x540 panes), dashboard flags |
| `run-matrix.ps1` | The whole Spike B matrix and mpv baseline in one process: noise floor, decoder runs, four layouts x browsers x hardware decode, mpv, hidden-tab runs |
| `run-spike-a.ps1` | Spike A: one 2560x1440 tile per browser, clean run then probe run |
| `run-extra.ps1` | Decoder-implementation runs (`I2-*`) and a Spike A rerun without capture (`A2-*`); only the `I2-*` part was executed |
| `spike-c.ps1` (on branch `spikes`) | Spike C: ffprobe of the real cameras' stream1 and stream2. It was never run and needs the PowerShell system's camera list, so it lives on the branch [`spikes`](https://github.com/karadimnik/holocron/blob/spikes/tasks/holocron/spikes/spike-c.ps1) instead of here |
| `analyze.py` | `analyze.py <datadir> [run ...]` turns each cell's raw files into `<run>.summary.json` and prints a markdown row |
| `make-tables.py` | `make-tables.py <datadir> > tables.md` builds the report tables from the summaries (output is [stage0-tables.md](stage0-tables.md)) |
| `data/` | Result data: per cell `<run>.meta.json`, `<run>.summary.json`, and gzipped raw page stats (`.jsonl.gz`), CPU samples (`.samples.jsonl.gz`) and GPU samples (`.gpu.jsonl.gz`); 34 cells, plus the screenshot `A-chrome-clean.png` |
| [stage0-report.md](stage0-report.md) | The findings, the recommendation, method, assumptions and limits |
| [stage0-tables.md](stage0-tables.md) | Every result table, generated from `data/` |

## Reproduce

All from a Windows PowerShell 5.1 prompt in this folder, on a Windows PC with the install paths above. The runs put browser windows on screen and hold the display awake for about 80 minutes; do not use the PC meanwhile.

1. `.\make-clips.ps1` once (writes the clips under `%TEMP%\stage0\clips`).
2. `.\rig-up.ps1` starts MediaMTX and the collector. Caveat: the committed `rig-up.ps1` has the ICE-over-TCP listener (`webrtcLocalTCPAddress`) switched ON, which the report's Spike A and `I2-*` runs needed. For UDP cells like the 16 Spike B cells, comment that line out before starting the rig (the report states those cells ran with the TCP listener off).
3. `.\run-matrix.ps1` (add `-SkipHidden` to skip the hidden-tab runs) runs the Spike B matrix; results land in `%TEMP%\stage0\data`.
4. `.\run-spike-a.ps1`, `.\run-extra.ps1` for the single-tile groups (see the transport caveat above).
5. `.\rig-down.ps1` stops everything by pid.
6. `python analyze.py %TEMP%\stage0\data` then `python make-tables.py %TEMP%\stage0\data > tables.md` to rebuild the tables; to audit the committed results instead, run both against `data\` (copy it first, the scripts write summaries next to the data).

Re-running will not reproduce the numbers exactly: single runs on a busy PC, see the limitations above.
