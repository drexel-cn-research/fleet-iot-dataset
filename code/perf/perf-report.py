#!/usr/bin/env python3
"""Summarize a router-perf.sh CSV: the sensor agent CPU, memory and per-packet BPF cost.

    tools/perf-report.py run8-perf.csv [--clk-tck 100] [--cpus 4] [--since HH:MM] [--until HH:MM]

Rates are computed between consecutive samples with the same pid (a
restart resets the cumulative counters). Reports median, 95th percentile
and max over the run, plus totals.
"""
import argparse
import csv
import statistics


def pct(xs, p):
    if not xs:
        return float("nan")
    xs = sorted(xs)
    k = min(len(xs) - 1, max(0, round(p / 100 * (len(xs) - 1))))
    return xs[k]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("csv")
    ap.add_argument("--clk-tck", type=int, default=100)
    ap.add_argument("--cpus", type=int, default=4)
    ap.add_argument("--since", help="drop samples before this time: unix seconds, or HH:MM (local, same day as the last sample)")
    ap.add_argument("--until", help="drop samples after this time (same formats as --since)")
    a = ap.parse_args()

    rows = [{k: int(v) for k, v in r.items()} for r in csv.DictReader(open(a.csv))]
    def cutoff(v):
        if ":" in v:
            import datetime
            last = datetime.datetime.fromtimestamp(rows[-1]["ts"])
            h, m = map(int, v.split(":"))
            return int(last.replace(hour=h, minute=m, second=0).timestamp())
        return int(v)

    if a.since:
        cut = cutoff(a.since)
        rows = [r for r in rows if r["ts"] >= cut]
    if a.until:
        cut = cutoff(a.until)
        rows = [r for r in rows if r["ts"] <= cut]
    cpu, sys_busy, softirq, ns_per_run, pps, mbps, rss = [], [], [], [], [], [], []
    tot_runs = tot_ns = tot_ticks = tot_secs = tot_pk = tot_by = 0
    for p, q in zip(rows, rows[1:]):
        dt = q["ts"] - p["ts"]
        if dt <= 0:
            continue
        rss.append(q["rss_kb"])
        dj = q["sys_total"] - p["sys_total"]
        if dj > 0:
            sys_busy.append(100 * (1 - (q["sys_idle"] - p["sys_idle"]) / dj))
            softirq.append(100 * (q["sys_softirq"] - p["sys_softirq"]) / dj)
        dpk = (q["pk_lan2"] + q["pk_lan3"]) - (p["pk_lan2"] + p["pk_lan3"])
        dby = (q["by_lan2"] + q["by_lan3"]) - (p["by_lan2"] + p["by_lan3"])
        if dpk >= 0:
            pps.append(dpk / dt)
            mbps.append(8 * dby / dt / 1e6)
            tot_pk += dpk
            tot_by += dby
        if p["pid"] != q["pid"] or q["pid"] == 0:
            continue
        dticks = q["cpu_ticks"] - p["cpu_ticks"]
        # percent of one core; divide by --cpus for share of the whole router
        cpu.append(100 * dticks / a.clk_tck / dt)
        druns, dns = q["bpf_runs"] - p["bpf_runs"], q["bpf_ns"] - p["bpf_ns"]
        if druns > 0:
            ns_per_run.append(dns / druns)
        tot_runs += druns
        tot_ns += dns
        tot_ticks += dticks
        tot_secs += dt

    def line(name, xs, unit, fmt="{:.2f}"):
        if not xs:
            print(f"  {name:34} (no data)")
            return
        print(f"  {name:34} median {fmt.format(statistics.median(xs))}  p95 {fmt.format(pct(xs, 95))}"
              f"  max {fmt.format(max(xs))} {unit}")

    hours = (rows[-1]["ts"] - rows[0]["ts"]) / 3600 if len(rows) > 1 else 0
    print(f"{len(rows)} samples over {hours:.1f} h")
    print("sensor agent (userspace)")
    line("CPU (% of one core)", cpu, "%")
    line("resident memory", [r / 1024 for r in rss], "MiB")
    print("sensor (in-kernel eBPF programs)")
    line("cost per program run", ns_per_run, "ns", "{:.0f}")
    if tot_runs:
        print(f"  {'overall':34} {tot_ns / tot_runs:.0f} ns per run over {tot_runs:,} runs"
              f" ({100 * tot_ns / 1e9 / max(tot_secs, 1):.4f}% of one core)")
    print("router as a whole (all processes on the router)")
    line("CPU busy (all cores)", sys_busy, "%")
    line("softirq (all cores)", softirq, "%")
    print("traffic on the monitored ports (lan2 + lan3)")
    line("packets per second", pps, "pkt/s", "{:.0f}")
    line("throughput", mbps, "Mbit/s")
    if tot_secs:
        print(f"  total {tot_pk:,} packets, {tot_by / 1e9:.2f} GB; agent used "
              f"{tot_ticks / a.clk_tck:.0f} CPU-s over {tot_secs / 3600:.1f} h "
              f"({100 * tot_ticks / a.clk_tck / tot_secs / a.cpus:.3f}% of the router's {a.cpus} cores)")


if __name__ == "__main__":
    main()
