# File Formats

All times are UTC unless stated otherwise. Text logs from the online detector use the testbed's local time (UTC-4).

## Behavioral summary (`data/runs/*/summaries/*.json`, `data/cameras/summaries/*.json`)

One file per five-minute observation window, named `<window end, UTC>_<part>of<parts>.json`. This is the record the router contributes to the fleet (paper Section 3.7). One object per file:

| Field | Meaning |
|---|---|
| `schema` | Format version (1) |
| `router` | The contributing router's identifier |
| `period.start`, `period.end`, `period.seconds` | The observation window |
| `part`, `parts` | Large windows may be split across files; all files here are `1of1` |
| `devices` | One record per device observed in the window (below) |

Each device record:

| Field | Meaning |
|---|---|
| `ref` | Keyed hash of the device's MAC address under a router-specific secret. Stable within one home, not linkable across homes |
| `class.type`, `class.vendor`, `class.model`, `class.firmware` | The device class assigned by the router's identification (Section 4.2). `model` and `firmware` appear only when identified |
| `class.confidence` | Identification confidence, 0 to 1 |
| `tier_observed` | `fixed`, `bounded`, or `open` as observed so far (Section 3.3); `-` before enough history exists |
| `first_seen` | When the router first observed the device |
| `fingerprints.oui` | The first nine hex digits of the MAC address (vendor OUI plus three digits). The replay uses it as the device key. For the simulated devices it maps to a device name (see below); for the physical cameras the last three digits are masked |
| `fingerprints.dhcp_prl` | DHCP parameter request list |
| `fingerprints.ja4` | JA4 TLS client fingerprints seen in the window |
| `destinations.domains` | Registrable domains contacted, learned only from DNS answers to this device, with connection counts |
| `destinations.asns` | Autonomous systems contacted, with counts |
| `destinations.unnamed` | Contacts to addresses with no DNS name |
| `rhythm[]` | Recurring contact with one destination: `dest` (`<domain or lan>:<port>`), `period_s` (seconds between contacts), `windows` (history length behind the estimate) |
| `envelope.window_s`, `envelope.windows` | Window length and number of windows summarized |
| `envelope.features.<f>` | `p50`, `p95`, `p99`, `max` of feature `<f>` over the summarized windows. Features: `tx_bytes`, `rx_bytes`, `flows` (new remote address/port peers), `dst_count` (distinct public destinations), `lan_peers` (distinct local devices contacted), `conns` (TCP connections opened). Paper Table 2 |
| `totals` | Window totals: `tx_bytes`, `rx_bytes`, `flows`, `new_destinations` |

## Injection ground truth (`data/runs/*/injections-pi4.jsonl`, `injections-pi5.jsonl`)

One JSON object per line, written by the simulation engine when it starts a controlled behavioral change.

| Field | Meaning |
|---|---|
| `time` | Start of the change |
| `device` | Simulated device name (see `data/homes/`) |
| `type` | Profile name in `data/profiles/catalog.json` |
| `mac` | The simulated device's MAC address |
| `pi` | Simulation engine: `pi4` = home B, `pi5` = home A |
| `scenario` | `exfil`, `newdest`, `scan`, or `beacon` (paper Table 4) |
| `arg` | For `scan` only: the addresses scanned, all simulated devices in the other home |

## Online detector log (`data/runs/*/detector-online.txt`)

The local detector's report for the run, as produced during the experiment. Header: number of windows, time span (local time), device count, warm-up, and the thresholds `k` (multiplier) and `recur` (third-largest value). `FLAGS` lists one line per flagged episode: local time, device, feature, the observed value against its baseline, and whether it matches an injection. `INJECTED EVENTS` lists each injection and whether it was caught. The last lines give the false-alarm rate. The device count in the header includes devices that were later removed from the summaries (see the README's privacy section); every flag in these logs is on a simulated device.

## Homes (`data/homes/pi4.json`, `pi5.json`)

The simulated devices in each home. `pi` names the engine, `devices[]` gives each device's `name` and its profile `type`. `sink` is the local address that received bulk and injected traffic. A simulated device's MAC is its profile's OUI followed by the first bytes of `sha256("<pi>/<name>")`, which is how `code/replay/replay.py` (`name_devices`) maps summary keys back to device names.

## Device profiles (`data/profiles/catalog.json`)

One entry per simulated product (paper Table 3): `vendor`, `model`, `class`, `oui`, `hostname` template, `tls` stack, `dhcp` options, discovery protocols (`mdns`, `ssdp`, `wsd`), `ntp`, recurring `cloud` contacts (`host`, `port`, `proto`, `every`, `persist`), listening ports, and bulk traffic (`upload` or `stream`), which always goes to the local sink.

## Schedules (`data/schedules/`)

`schedule-v1.txt` is the injection schedule of N1, N2, Day and validation runs V3 to V5: twelve isolated changes. Its header describes 30-minute spacing; the runs used five-minute spacing. `schedule-v2.txt` is the Population run's schedule: isolated, within-home, cross-home and spreading changes. Both were fixed before the runs. Comment lines explain each slot.

## Router performance (`data/perf/`)

`pop-perf.csv` (Population run) and `stress-perf.csv` (routed TCP load) are one-minute samples taken on the router:

| Column | Meaning |
|---|---|
| `ts` | Unix time |
| `pid` | Agent process |
| `cpu_ticks` | Agent CPU time, cumulative clock ticks |
| `rss_kb` | Agent resident memory |
| `bpf_runs`, `bpf_ns` | Cumulative eBPF program runs and run time, from the kernel's BPF statistics |
| `sys_total`, `sys_idle`, `sys_softirq` | Cumulative system CPU counters, all cores |
| `pk_lan2`, `pk_lan3`, `by_lan2`, `by_lan3` | Cumulative packets and bytes on the two monitored ports |
| `mem_avail_kb` | Available memory on the router |

`stress.steps` gives each load step's start and end (Unix time); `stress.log` is the load generator's output. `pop-perf.txt` and `stress.txt` are the reports as produced during the experiment, and `code/perf/perf-report.py` regenerates them (`results/pop-perf.txt`, `results/stress-perf.txt`).

## Figure inputs (`data/figures/`)

`N2-episodes.txt` holds the Night 2 episodes, one per line: `local time|device|feature|windows|scenario or -`. It is extracted from `data/runs/N2/detector-online.txt` by `code/figures/episodes_from_online.py`. `N2-fleet-remaining.txt` lists the false alarms that remain under combined supervision (`local time|device|feature`) and is written by `code/replay/loio_all.py` as `results/run7-fleet-remaining-loio.txt`.

## Results (`results/`)

| File | Format |
|---|---|
| `loio-all.txt` | Blocks `tod=<time-of-day buckets> w=<warm-up windows>`, each with one line per group and mode: `<twin, single, all, or run name>\|<local, fleet, combined>  FP <false-alarm episodes> / <device-days> = <rate>  det <caught>/<injections>` |
| `loio-results.json` | The same numbers keyed by block and group |
| `loio-robust.txt` | `<variant> w<warm-up> <group>\|<mode> FP <n>/<device-days> = <rate> det <caught>/<injections>` |
| `loio-preexisting.txt` | Detection and false alarms when the change is present from the first window |
| `rq1.txt` | Per device type: instances and the median, minimum and capped count of n0 across features; then per fleet class: homes, well-supported and weakly supported domains, and rhythm ranges |
| `fleet-replay.txt` | Run-level holdout results per mode and warm-up, sweeps and ablations |
| `iforest-baseline.txt` | Our detector's false-alarm budget, then each isolation-forest variant at that budget and at its default cut-off |
| `population-rho.txt` | Per class: each stretch with an open episode, rho, the homes and devices involved |
| `camera-cross-replay.txt` | Episodes for each camera with local supervision and with the other camera as its fleet reference |
| `pop-perf.txt`, `stress-perf.txt` | Router performance reports |
| `figures/` | Figures 1, 3 and 4 as PDFs |
