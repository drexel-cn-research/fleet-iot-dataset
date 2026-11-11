#!/usr/bin/env python3
"""Isolation-forest baseline on the same 5-minute window features as the online drift detector.

    tools/iforest-baseline.py --run run6=DIR:EVENTS[,EVENTS] --run run7=... \
        [--clean run7b=DIR] [--drift run6=FILE ...] [--warmup 12]

DIR holds one run's summary JSON files; EVENTS are the Pis' injection logs.
--clean names an injection-free run used only by the "prior run" variant.
--drift gives the online drift detector's output for a run, so our detector is counted in
the same unit (device episodes) for the comparison.

Variants (all score only windows after each device's warm-up, as drift does):
  device     one forest per device, refit on all of its earlier windows
  class      one forest per device class, refit on all earlier windows of
             every device of that class in the run
  prior-run  one forest per device, trained on that device's windows in the
             clean run plus its own earlier windows (the generous variant)

Each variant is run on two feature sets:
  volume     tx, rx, flows, dst_count, lan_peers, conns (log1p)
  +novelty   volume plus new destinations, first-contact domains and new
             rhythms, counted per window (gives the forest our set signals)

Unit of comparison: a device episode, i.e. consecutive alarmed windows of one
device. An injection is caught if its device alarms in the window containing
the injection or the next one. Alarms within 15 minutes after an injection on
the injected device are explained; every other device episode is a false
alarm. Thresholds: one score threshold per variant, shared by all runs,
chosen so the false alarms per device-day match our detector's.
"""
import argparse
import glob
import json
import os
import re
from collections import defaultdict
from datetime import datetime, timedelta, timezone

import numpy as np
from sklearn.ensemble import IsolationForest

VOLUME = ["tx_bytes", "rx_bytes", "flows", "dst_count", "lan_peers", "conns"]
NOVELTY = ["new_dest", "new_domains", "new_rhythms"]
WIN = timedelta(minutes=5)
REFIT = 6  # refit every 6 windows (30 min) on everything before the window scored


def ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00"))


def load_run(d):
    """devices: oui -> {'cls': type, 'w': [(start, {feature: value})]}"""
    devs = {}
    for f in sorted(glob.glob(os.path.join(d, "**", "*.json"), recursive=True)):
        p = json.load(open(f))
        start = ts(p["period"]["start"])
        for x in p["devices"]:
            key = x.get("fingerprints", {}).get("oui")
            if not key:
                continue
            dv = devs.setdefault(key, {"cls": x["class"].get("type", "?"), "label": (x["class"].get("vendor", "") + " " + key).strip(), "w": []})
            feats = x.get("envelope", {}).get("features", {})
            v = {k: float(feats.get(k, {}).get("max", 0)) for k in VOLUME}
            v["new_dest"] = float(x.get("totals", {}).get("new_destinations", 0))
            v["_domains"] = set(x.get("destinations", {}).get("domains", {}) or {})
            v["_rhythms"] = {"%s@%gs" % (r["dest"], r["period_s"]) for r in x.get("rhythm", []) or []}
            dv["w"].append((start, v))
    for dv in devs.values():
        dv["w"].sort(key=lambda t: t[0])
        seen_d, seen_r = set(), set()
        for _, v in dv["w"]:
            v["new_domains"] = float(len(v["_domains"] - seen_d))
            v["new_rhythms"] = float(len(v["_rhythms"] - seen_r))
            seen_d |= v["_domains"]
            seen_r |= v["_rhythms"]
    return devs


def ouikey(mac):
    return mac.replace(":", "").replace("-", "").upper()[:9]


def load_events(files):
    ev = []
    for f in files:
        for line in open(f):
            line = line.strip()
            if line:
                e = json.loads(line)
                ev.append({"t": ts(e["time"]), "key": ouikey(e["mac"]), "device": e["device"], "scenario": e["scenario"]})
    return sorted(ev, key=lambda e: e["t"])


def vec(v, fs):
    return np.log1p(np.array([v[k] for k in fs], dtype=float))


def forest(X):
    return IsolationForest(n_estimators=100, random_state=0).fit(X)


def scores(devs, fs, variant, warmup, clean=None):
    """(key, start) -> anomaly score (higher = more anomalous), supervised windows only."""
    out = {}
    if variant == "class":
        by_cls = defaultdict(list)
        for key, dv in devs.items():
            for i, (t, v) in enumerate(dv["w"]):
                by_cls[dv["cls"]].append((t, key, i, v))
        for cls, rows in by_cls.items():
            rows.sort(key=lambda r: r[0])
            times = sorted({r[0] for r in rows})
            m = None
            for j, t in enumerate(times):
                cur = [r for r in rows if r[0] == t and r[2] >= warmup]
                if not cur:
                    continue
                if m is None or j % REFIT == 0:
                    m = forest(np.array([vec(r[3], fs) for r in rows if r[0] < t]))
                s = -m.score_samples(np.array([vec(r[3], fs) for r in cur]))
                for r, x in zip(cur, s):
                    out[(r[1], r[0])] = x
        return out
    for key, dv in devs.items():
        base = []
        if variant == "prior-run":
            if clean is None or key not in clean:
                continue
            base = [vec(v, fs) for _, v in clean[key]["w"]]
        m = None
        for i in range(warmup, len(dv["w"])):
            if m is None or (i - warmup) % REFIT == 0:
                m = forest(np.array(base + [vec(v, fs) for _, v in dv["w"][:i]]))
            t, v = dv["w"][i]
            out[(key, t)] = -m.score_samples(vec(v, fs)[None, :])[0]
    return out


def evaluate(devs, events, sc, thr, warmup):
    alarms = defaultdict(list)
    for (key, t), s in sc.items():
        if s >= thr:
            alarms[key].append(t)
    caught = []
    explained = set()
    for e in events:
        t0 = e["t"] - (e["t"] - datetime(1970, 1, 1, tzinfo=timezone.utc)) % WIN  # window containing it
        hit = any(t0 <= a <= t0 + WIN for a in alarms.get(e["key"], []))
        caught.append((e, hit))
        for a in alarms.get(e["key"], []):
            if t0 <= a <= t0 + 3 * WIN:
                explained.add((e["key"], a))
    fp = 0
    fp_list = []
    for key, ts_ in alarms.items():
        prev = None
        for a in sorted(ts_):
            if (key, a) in explained:
                prev = None
                continue
            if prev is None or a - prev > WIN:
                fp += 1
                fp_list.append((a, key))
            prev = a
    days = sum(max(0, len(dv["w"]) - warmup) for dv in devs.values()) * 300 / 86400
    return caught, fp, days, fp_list


FLAG = re.compile(r"^\s+(\w{3} +\d+ \d\d:\d\d)\s+(\S+)\s+(\S+)\s.*?(?:\((\d+) windows)?.*\[(no injection|matches)")


def drift_device_episodes(path):
    """Our detector's false alarms in device episodes, from the online drift detector output."""
    txt = open(path).read()
    days = float(re.search(r"over ([\d.]+) supervised device-days", txt).group(1))
    m = re.search(r"(\d+) windows, (\w{3} +\d+ \d\d:\d\d)", txt)
    year = 2026
    spans = defaultdict(list)
    for line in txt.splitlines():
        f = FLAG.match(line)
        if not f or f.group(5) != "no injection":
            continue
        t = datetime.strptime(f"{year} {f.group(1)}", "%Y %b %d %H:%M")
        n = int(f.group(4) or 1)
        spans[f.group(2)].append((t, t + n * WIN))
    eps = 0
    for dev, ss in spans.items():
        ss.sort()
        end = None
        for a, b in ss:
            if end is None or a > end:
                eps += 1
                end = b
            else:
                end = max(end, b)
    flags = len(re.findall(r"\[no injection", txt))
    return eps, flags, days


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--run", action="append", required=True, help="name=DIR:EVENTS[,EVENTS]")
    ap.add_argument("--clean", help="name=DIR, injection-free run for the prior-run variant")
    ap.add_argument("--drift", action="append", default=[], help="name=FILE, the online drift detector output")
    ap.add_argument("--warmup", type=int, default=12)
    a = ap.parse_args()

    runs = {}
    for r in a.run:
        name, rest = r.split("=", 1)
        d, ev = (rest.split(":", 1) + [""])[:2]
        runs[name] = (load_run(d), load_events([x for x in ev.split(",") if x]))
    clean = load_run(a.clean.split("=", 1)[1]) if a.clean else None
    labels = {}
    for _, (_, ev) in runs.items():
        for e in ev:
            labels[e["key"]] = e["device"]

    # our detector, in the same unit
    ours_eps = ours_days = 0
    print("OUR DETECTOR (from the online drift detector)")
    for r in a.drift:
        name, path = r.split("=", 1)
        eps, flags, days = drift_device_episodes(path)
        ours_eps += eps
        ours_days += days
        print(f"  {name:6} {flags} flag episodes, {eps} device episodes over {days:.1f} device-days = {eps / days:.2f}/device-day")
    target = ours_eps / ours_days if ours_days else 1.0
    print(f"  pooled: {target:.2f} false-alarm device episodes per device-day (the budget below)\n")

    for fsname, fs in [("volume", VOLUME), ("+novelty", VOLUME + NOVELTY)]:
        for variant in ["device", "class", "prior-run"]:
            if variant == "prior-run" and clean is None:
                continue
            sc = {name: scores(devs, fs, variant, a.warmup, clean) for name, (devs, _) in runs.items()}
            allsc = sorted({s for d in sc.values() for s in d.values()}, reverse=True)

            def at(thr):
                tot_fp = tot_days = 0
                res = {}
                for name, (devs, ev) in runs.items():
                    caught, fp, days, fpl = evaluate(devs, ev, sc[name], thr, a.warmup)
                    res[name] = (caught, fp, days, fpl)
                    tot_fp += fp
                    tot_days += days
                return res, tot_fp / tot_days if tot_days else 0

            # highest-recall threshold whose pooled FP rate stays within the budget
            lo, hi = 0, len(allsc) - 1           # allsc descending: FP rises with the index
            best = allsc[0] + 1
            while lo <= hi:
                mid = (lo + hi) // 2
                if at(allsc[mid])[1] <= target:
                    best = allsc[mid]
                    lo = mid + 1
                else:
                    hi = mid - 1
            res, rate = at(best)
            per = defaultdict(lambda: [0, 0])
            tot = hit = 0
            for name, (caught, _, _, _) in res.items():
                for e, h in caught:
                    per[e["scenario"]][0] += h
                    per[e["scenario"]][1] += 1
                    tot += 1
                    hit += h
            print(f"ISOLATION FOREST  variant={variant:9} features={fsname}")
            print(f"  at matched budget: {hit}/{tot} injections caught, {rate:.2f} false-alarm episodes per device-day")
            print("  by scenario: " + ", ".join(f"{k} {v[0]}/{v[1]}" for k, v in sorted(per.items())))
            print("  by run:      " + ", ".join(f"{n} {sum(h for _, h in c)}/{len(c)} fp {fp}" for n, (c, fp, d, _) in res.items()))
            missed = [f"{n}:{e['device']}({e['scenario']})" for n, (c, _, _, _) in res.items() for e, h in c if not h]
            if missed:
                print("  missed:      " + ", ".join(missed))
            # sklearn's default cut-off (contamination='auto' -> score offset 0.5)
            res2, rate2 = at(0.5)
            hit2 = sum(h for c, _, _, _ in res2.values() for _, h in c)
            print(f"  default cut-off (score > 0.5): {hit2}/{tot} caught, {rate2:.2f} false alarms per device-day")
            # a few points of the trade-off curve
            pts = []
            for q in [0.999, 0.995, 0.99, 0.98, 0.95]:
                thr = float(np.quantile(allsc, q))
                r3, rt3 = at(thr)
                h3 = sum(h for c, _, _, _ in r3.values() for _, h in c)
                pts.append(f"{rt3:.1f}/day:{h3}/{tot}")
            print("  curve (FP per device-day : caught): " + "  ".join(pts) + "\n")


if __name__ == "__main__":
    main()
