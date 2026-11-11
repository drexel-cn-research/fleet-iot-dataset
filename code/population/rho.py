#!/usr/bin/env python3
"""Class-level change rate rho_c(t) from the online drift detector's episodes.

    tools/rho.py --summaries DIR --drift DRIFT.txt [--fleets fleets/pi4.json,fleets/pi5.json]
                 [--homes pi4=B,pi5=A] [--tol 3]

rho_c(t) is the share of class-c devices with an open local episode at window
t, where an episode stays open for tol windows after its last flagged window
(so two devices that change a few minutes apart count together). Classes are
the ones the sensor assigned (summaries), not the simulator's own labels.
Device names come from the fleet files: a simulated MAC is the profile OUI
plus the first bytes of sha256("<pi>/<name>"), and summaries key devices by
the first 9 hex digits of the MAC.

Prints, per class, every stretch where at least one device is open: the
time, rho, which devices (and homes), and whether each device's episode
matches an injection or is unexplained, with the destinations it first
contacted.
"""
import argparse
import glob
import hashlib
import json
import os
import re
from collections import Counter, defaultdict
from datetime import datetime, timedelta

WIN = timedelta(minutes=5)


def load_summaries(d):
    cls = defaultdict(Counter)
    starts = set()
    for f in sorted(glob.glob(os.path.join(d, "*.json"))):
        p = json.load(open(f))
        t = datetime.fromisoformat(p["period"]["start"].replace("Z", "+00:00")).replace(tzinfo=None)
        starts.add(t)
        for x in p["devices"]:
            k = x.get("fingerprints", {}).get("oui")
            if k:
                cls[k][x["class"].get("type", "?")] += 1
    return {k: c.most_common(1)[0][0] for k, c in cls.items()}, min(starts)


def fleet_names(files, keys, homes):
    names, home = {}, {}
    for f in files:
        fl = json.load(open(f))
        for dv in fl["devices"]:
            h = hashlib.sha256(f"{fl['pi']}/{dv['name']}".encode()).hexdigest()[:3].upper()
            m = [k for k in keys if k[6:9] == h]
            if len(m) == 1:
                names[m[0]] = dv["name"]
                home[m[0]] = homes.get(fl["pi"], fl["pi"])
    return names, home


FEATURES = "tx_bytes|rx_bytes|flows|dst_count|lan_peers|new_dest|conns|new_domain|new_rhythm"
FLAG = re.compile(r"^\s+(\w{3} +\d+ \d\d:\d\d)\s+(.+?)\s+(" + FEATURES + r")\s+(.*?)\s+\[(.*)\]\s*$")


def parse_drift(path, names):
    txt = open(path).read()
    first = re.search(r"windows, (\w{3} +\d+ \d\d:\d\d) to", txt).group(1)
    year = datetime.now().year
    first_t = datetime.strptime(f"{year} {first}", "%Y %b %d %H:%M")
    bykey = {v: k for k, v in names.items()}
    flags = []
    for line in txt.split("\nINJECTED")[0].splitlines():
        m = FLAG.match(line)
        if not m:
            continue
        t = datetime.strptime(f"{year} {m.group(1)}", "%Y %b %d %H:%M")
        label = m.group(2).strip()
        key = bykey.get(label) or label.split()[-1]
        n = re.search(r"\((\d+) windows", m.group(4))
        flags.append({"t": t, "key": key, "feature": m.group(3), "why": m.group(4),
                      "windows": int(n.group(1)) if n else 1, "match": m.group(5)})
    return flags, first_t


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--summaries", required=True)
    ap.add_argument("--drift", required=True)
    ap.add_argument("--fleets", default="fleets/pi4.json,fleets/pi5.json")
    ap.add_argument("--homes", default="pi4=B,pi5=A")
    ap.add_argument("--tol", type=int, default=3, help="windows an episode stays open after its last flagged window")
    a = ap.parse_args()

    cls, s_first = load_summaries(a.summaries)
    homes = dict(x.split("=") for x in a.homes.split(","))
    names, home = fleet_names(a.fleets.split(","), cls.keys(), homes)
    flags, d_first = parse_drift(a.drift, names)
    members = defaultdict(list)
    for k, c in cls.items():
        members[c].append(k)

    # open intervals per device
    open_ = defaultdict(list)
    for f in flags:
        open_[f["key"]].append((f["t"], f["t"] + (f["windows"] + a.tol - 1) * WIN, f))

    t0 = min(f["t"] for f in flags) if flags else d_first
    t1 = max(e for v in open_.values() for _, e, _ in v) if flags else d_first
    nm = lambda k: names.get(k, k)
    print(f"tolerance {a.tol} windows ({a.tol * 5} min); classes as assigned by the sensor\n")
    for c in sorted(members):
        ks = members[c]
        print(f"{c}: {len(ks)} devices: " + ", ".join(f"{nm(k)}({home.get(k, '?')})" for k in sorted(ks, key=nm)))
        t, prev = t0, None
        while t <= t1:
            act = {}
            for k in ks:
                for s, e, f in open_.get(k, []):
                    if s <= t < e:
                        act.setdefault(k, []).append(f)
            sig = tuple(sorted(act))
            if sig and sig != prev:
                hs = {home.get(k, "?") for k in act}
                desc = "; ".join(
                    f"{nm(k)}: " + ", ".join(sorted({f['feature'] + ('' if f['match'].startswith('matches') else ' (unexplained)') for f in fs}))
                    + "".join(f" [{f['why'].replace('first contact with ', '')}]" for f in fs if f["feature"] == "new_domain")
                    for k, fs in sorted(act.items(), key=lambda kv: nm(kv[0])))
                print(f"  {t:%H:%M}  rho {len(act)}/{len(ks)} = {len(act) / len(ks):.2f}  homes {','.join(sorted(hs))}  {desc}")
            prev = sig
            t += WIN
        print()


if __name__ == "__main__":
    main()
