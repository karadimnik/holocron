# Holocron design spikes (branch `spikes`)

This branch is `main` plus the feasibility spikes that preceded the Holocron build. It is kept for the record and is not maintained; nothing on `main` depends on it.

| Spike | Question | Code |
|---|---|---|
| SP1, SP1b | ffmpeg arguments on Linux versus Windows; credentials off the command line (RTSP stub) | `spikes/sp1b`, drivers `tests/holocron/sp1-*`, `sp1b-*` |
| SP2 | what a Go program receives when its terminal window is closed | `spikes/sp2`, `tests/holocron/sp2-run.ps1` |
| SP3 | do the children die with the supervisor | `spikes/sp3`, `tests/holocron/sp3-*` |
| SP4 | the "open segment" rule | `spikes/sp4`, `tests/holocron/sp4-*` |
| SP6 | atomic replace of a status-like JSON file with concurrent readers | `spikes/sp6`, `tests/holocron/sp6-*` |
| SP7 | driving the pruner as a dry run | `tests/holocron/sp7-probe.ps1` |
| Spike C | ffprobe of the real cameras' streams (never run) | `tasks/holocron/spikes/spike-c.ps1` |

The write-ups are `tasks/holocron/spikes/holocron-spikes-1.md` (SP1, SP1b, SP4, SP6, SP7) and `holocron-spikes-2.md` (SP2, SP3). The Stage 0 live-view stress tests are NOT spikes of this kind: their testbed and results are on `main` under `tests/stress/live-view/`.
