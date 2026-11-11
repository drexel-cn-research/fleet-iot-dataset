# Fleet-Informed Supervision: Data and Analysis Code

This repository accompanies the paper *Normal Compared to What? Fleet-Informed Supervision of IoT Devices at the Network Edge* (anonymous submission). It contains the device simulator used to generate the testbed traffic, the behavioral summaries collected by the router sensor in every run, the ground truth for every injected behavioral change, the router performance measurements, and the Python code that produces every number, table, and figure in the evaluation.

Everything needed to reproduce the evaluation from the recorded data is here, and the simulator lets others generate new runs. The router sensor is not included; the summaries it produced are, and the paper's results are computed from them.

## Contents

```
README.md               this file
reproduce.sh            regenerates everything in results/ from data/
requirements.txt        Python packages (only the baseline and figures need any)
docs/
  RUNS.md               every run: purpose, dates, windows, injections, paper name
  SCHEMA.md             every file format: summaries, injections, logs, results
data/
  runs/
    N1/ N2/ Day/ Ctrl/ Pop/     the five reported runs (paper Table 5)
      summaries/                one JSON summary per five-minute window
      injections-pi4.jsonl      ground truth for home B (absent for Ctrl)
      injections-pi5.jsonl      ground truth for home A (absent for Ctrl)
      detector-online.txt       the local detector's output, as logged during the run
    validation/V1 ... V5        the five validation runs used to configure the system
  cameras/summaries/    the two physical cameras (paper Section 6.5)
  homes/pi4.json        simulated devices of home B (Raspberry Pi 4)
  homes/pi5.json        simulated devices of home A (Raspberry Pi 5)
  profiles/catalog.json the 15 simulated device profiles (paper Table 3)
  schedules/            injection schedules, fixed before each run
  perf/                 router CPU, memory and eBPF cost (paper Section 6.4, Table 8)
  figures/              per-window episode lists used by the Figure 4 timeline
code/
  replay/               offline replay: local, fleet and combined supervision
  baseline/             isolation-forest baseline
  population/           population drift rate rho_c(t)
  perf/                 router performance report
  figures/              scripts for Figures 3 and 4 and the Figure 1 illustration
results/                outputs of reproduce.sh, as committed
simulator/              iotsim, the device simulator and local detector (Go), with its own README
```

## Requirements

- **Analysis:** Python 3.10 or later. The replay code uses only the standard library. numpy and scikit-learn are needed for the isolation-forest baseline, and matplotlib for the figures. The committed results were produced on Linux (x86_64) with Python 3.13, numpy 2.5, scikit-learn 1.9 and matplotlib 3.11. Other versions should give the same results, except that the isolation-forest counts may shift slightly with the scikit-learn version.
- **Simulator:** Go 1.24 or later. The binary builds on Linux and macOS. The device and supervisor packages use Linux networking, so the full test suite and running a simulated home (`iotsim run`, which also needs root) require Linux. See `simulator/README.md`.

## Quick start

```
python3 -m pip install -r requirements.txt   # numpy, scikit-learn, matplotlib
./reproduce.sh                                # about 20 minutes; most of it is the isolation-forest baseline
cd simulator && go build ./cmd/iotsim && go test ./...     # on Linux; see simulator/README.md for macOS
```

## Where each result comes from

| Paper result | Script | Output |
|---|---|---|
| Table 6: local, fleet, combined supervision (one-hour warm-up) | `code/replay/loio_all.py` | `results/loio-all.txt`, block `tod=True w=12` (also `results/loio-results.json`) |
| Figure 3: false alarms vs. warm-up | `code/replay/loio_all.py`, then `code/figures/coldstart.py` | `results/loio-all.txt`, `results/figures/fig-coldstart.pdf` |
| No warm-up: 8.31 vs. 0.14 false alarms per device-day | `code/replay/loio_all.py` | `results/loio-all.txt`, block `tod=True w=0` |
| Pre-existing changes: 26/26 detected, no false alarms over 5.5 device-days | `code/replay/loio_preexisting.py` | `results/loio-preexisting.txt` |
| Robustness to tau, h_min, n0 cap, injection-window exclusion | `code/replay/loio_robust.py` | `results/loio-robust.txt` |
| RQ1: n0 per device type, fleet signatures per class | `code/replay/rq1.py` | `results/rq1.txt` |
| Per-run detection and false alarms, warm-up and tau sweeps, time-of-day ablation (run-level holdout, see below) | `code/replay/all.py` | `results/fleet-replay.txt` |
| Isolation-forest baseline (5 to 15 of 36 at a matched false-alarm budget) | `code/baseline/iforest-baseline.py` | `results/iforest-baseline.txt` |
| Table 7: population drift rate | `code/population/rho.py` | `results/population-rho.txt` |
| Router cost in the Population run (Section 6.4) | `code/perf/perf-report.py` | `results/pop-perf.txt` |
| Table 8: router cost under routed TCP load | `code/perf/perf-report.py` | `results/stress-perf.txt` |
| Section 6.5: two physical cameras, 11 to 4 episodes | `code/replay/cam_cross.py` | `results/camera-cross-replay.txt` |
| Figure 4: Night 2 timeline | `code/figures/timeline_v2.py` | `results/figures/fig-timeline.pdf` |

### How the replay works

`code/replay/replay.py` is a library. It loads a run's summaries, runs the local detector, and scores flags against the injections. In local mode it makes the same decisions as the detector did online (compare `detector-online.txt`). Fleet and combined modes add a reference built by `code/replay/fleet.py` from the *other* runs only. `all.py` uses this run-level holdout, in which the same simulated device can still appear in the other runs. The leave-one-instance-out scripts (`loio_*.py`), which produce the paper's numbers, also remove the evaluated device itself from every training run, so a device never contributes to its own fleet expectation and a same-product reference can only come from the device's twin. Device names come from `data/homes/`: a simulated device's MAC is its profile's OUI followed by the first bytes of `sha256("<pi>/<name>")`, and summaries key devices by the first nine hex digits of the MAC.

Paper terms map to the code as follows: twin = device whose model has another simulated instance (the "same product in the fleet" group), single = all others; w12 = one-hour warm-up (12 five-minute windows); w0 = no warm-up.

## Privacy and ethics

- The twenty simulated devices are the only devices kept in the run data. Other devices that appeared on the network during a run were removed from every summary.
- The physical-camera data keeps only the two cameras. The last digits of their MAC prefix are masked (`E0D362000`), the router name is generic, and no packet or video content was ever captured.
- Summaries contain no IP addresses, hostnames, or payloads. Injected traffic stayed inside the testbed: scans targeted only simulated devices, beacons and exfiltration went to a local sink, and new-destination changes used reserved documentation domains and the IANA homepage.

## Citation

Anonymous. *Normal Compared to What? Fleet-Informed Supervision of IoT Devices at the Network Edge.* Under submission, 2026.
