# Runs

Every run used the same router, sensor and twenty simulated devices: fifteen profiles, five of them instantiated twice (paper Table 3). Home A is simulated by a Raspberry Pi 5 (`pi5`) and home B by a Raspberry Pi 4 (`pi4`). Each home connects to its own router port and subnet. Windows are five minutes long.

## Reported runs (paper Table 5)

The configuration was frozen after the validation runs. No code, profiles, schedule, topology or scoring changed between these runs.

| Directory | Paper name | Start (UTC) | End (UTC) | Windows | Injections | Purpose |
|---|---|---|---|---|---|---|
| `runs/N1` | Night 1 | 2026-10-21 23:30 | 2026-10-22 10:40 | 135 | 12 (4 exfil, 3 newdest, 3 scan, 2 beacon) | Detection and false alarms |
| `runs/Day` | Day | 2026-10-23 11:15 | 2026-10-23 18:15 | 85 | 12, same mix | Time-of-day sensitivity |
| `runs/Ctrl` | Control | 2026-10-23 23:49 | 2026-10-24 12:20 | 151 | none | False alarms alone |
| `runs/N2` | Night 2 | 2026-10-24 21:18 | 2026-10-25 09:50 | 151 | 12, same mix | Detection and false alarms (Figure 4) |
| `runs/Pop` | Population | 2026-10-25 10:07 | 2026-10-25 18:55 | 106 | 10 (3 beacon, 7 newdest) | Population drift (Table 7), router cost |

N1, Day and N2 follow `schedules/schedule-v1.txt`: twelve isolated changes on different devices, one every five minutes, starting about four hours into the run. One member of each twin pair stays unchanged. Pop follows `schedules/schedule-v2.txt`: beacons on cameras (isolated, then cross-home), and new destinations on speakers (isolated, then within one home) and on plugs (isolated, then spreading across homes), with conditions twenty minutes apart.

The Day directory also holds one earlier window, ending 2026-10-22 10:40, kept as recorded. The run itself starts at 11:15 on 2026-10-23.

## Validation runs (not reported)

These runs were used to configure features, detector parameters and the testbed before the configuration was frozen. Their results are not reported in the paper. They are included for completeness.

| Directory | Start (UTC) | End (UTC) | Windows | Injections | Notes |
|---|---|---|---|---|---|
| `runs/validation/V1` | 2026-10-17 17:25 | 2026-10-18 18:10 | 179 | not logged | Destination novelty at IP level, broken streams, a ten-hour overnight gap, one manual exfiltration |
| `runs/validation/V2` | 2026-10-18 18:11 | 2026-10-19 11:10 | 204 | none | No-injection baseline; domain-level novelty; stream pacing fixed partway through |
| `runs/validation/V3` | 2026-10-19 13:05 | 2026-10-20 11:20 | 147 | 12 | First run with the full injection schedule, five-minute spacing |
| `runs/validation/V4` | 2026-10-20 19:49 | 2026-10-21 01:35 | 70 | 13 | Five-minute spacing; scans not visible to the sensor because both homes shared a hardware switch |
| `runs/validation/V5` | 2026-10-21 02:22 | 2026-10-21 09:40 | 88 | 14 | Two-home routed topology, as in the reported runs; includes a Wi-Fi outage on a non-simulated host |

## Physical cameras (paper Section 6.5)

`cameras/summaries/` holds 420 windows from 2026-10-26 23:20 to 2026-10-28 10:15 UTC for two TP-Link Tapo C216 cameras of the same model, one indoors (`ref` starting `53c8`) and one outdoors (`b6f8`), observed passively on the authors' home network. The analysis window is 2026-10-26 23:40 to 2026-10-28 10:05 UTC. Only the two cameras are kept. Events during the run, for interpreting the timeline:

| Time (UTC) | Event |
|---|---|
| 2026-10-26 23:20 | Sensor restarted |
| 2026-10-26 23:39 | Both cameras power-cycled; analysis starts |
| 2026-10-27 09:49 | Brief live view of both cameras from the app on the local network |
| 2026-10-27 10:00 | Both cameras briefly contact several local peers |
| 2026-10-27 10:32 to 10:34 | Motion events (no visible network change) |
| 2026-10-27 12:22 | Remote live view of the indoor camera, about one minute |
| 2026-10-27 13:55 | Remote live view of the outdoor camera, about one minute |
| 2026-10-28 06:22 | The upstream router's scheduled firmware check makes all devices reassociate |

`code/replay/cam_cross.py` reproduces the paper's numbers: 11 local false-alarm episodes over 2.8 device-days, and 4 when each camera uses the other as its only fleet reference (`h_min` relaxed to 1, no time-of-day buckets, one-hour warm-up). See `results/camera-cross-replay.txt`.
