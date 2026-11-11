"""Offline replay of the online drift detector's local detector, extended with fleet references.

Library module: load runs, run the detector (local, fleet, combined), score.
The local mode reproduces the online drift detector exactly (checked against its output).
"""
import glob
import json
import math
import os
from datetime import datetime, timedelta

FEATURES = [  # name, floor
    ("tx_bytes", 1 << 20), ("rx_bytes", 1 << 20), ("flows", 10), ("dst_count", 3),
    ("lan_peers", 3), ("new_dest", 3), ("conns", 8),
]
FEATURE_NAMES = [f for f, _ in FEATURES]
FLOOR = dict(FEATURES)


def ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00")).replace(tzinfo=None)


class Window:
    __slots__ = ("start", "end", "vals", "domains", "rhythms", "cls")

    def __init__(self, start, end, vals, domains, rhythms, cls):
        self.start, self.end, self.vals, self.domains, self.rhythms, self.cls = start, end, vals, domains, rhythms, cls


class Device:
    def __init__(self, key, cls):
        self.key = key
        self.cls = cls  # dict type, vendor, model, firmware (last seen)
        self.windows = []
        self.name = key
        self.home = "?"


def load_run(d):
    devs = {}
    for f in sorted(glob.glob(os.path.join(d, "**", "*.json"), recursive=True)):
        p = json.load(open(f))
        start, end = ts(p["period"]["start"]), ts(p["period"]["end"])
        for x in p["devices"]:
            key = x.get("fingerprints", {}).get("oui")
            if not key:
                continue
            c = x.get("class", {})
            cls = {k: c.get(k) for k in ("type", "vendor", "model", "firmware")}
            dv = devs.get(key)
            if dv is None:
                dv = devs[key] = Device(key, cls)
            dv.cls = cls
            feats = x.get("envelope", {}).get("features", {}) or {}
            vals = {k: float(v.get("max", 0)) for k, v in feats.items()}
            vals["new_dest"] = float(x.get("totals", {}).get("new_destinations", 0))
            doms = sorted((x.get("destinations", {}) or {}).get("domains", {}) or {})
            rh = sorted("%s@%gs" % (r["dest"], r["period_s"]) for r in (x.get("rhythm") or []))
            dv.windows.append(Window(start, end, vals, doms, rh, cls))
    for dv in devs.values():
        dv.windows.sort(key=lambda w: w.start)
    return devs


def name_devices(devs, fleet_files, homes={"pi4": "B", "pi5": "A"}):
    import hashlib
    for f in fleet_files:
        fl = json.load(open(f))
        for x in fl["devices"]:
            h = hashlib.sha256(f"{fl['pi']}/{x['name']}".encode()).hexdigest()[:3].upper()
            m = [k for k in devs if k[6:9] == h]
            if len(m) == 1:
                devs[m[0]].name = x["name"]
                devs[m[0]].home = homes.get(fl["pi"], fl["pi"])


def load_events(files):
    ev = []
    for f in files:
        for line in open(f):
            line = line.strip()
            if not line:
                continue
            e = json.loads(line)
            mac = e["mac"].replace(":", "").replace("-", "").upper()
            ev.append({"t": ts(e["time"]), "key": mac[:9], "device": e["device"], "scenario": e["scenario"]})
    return ev


def p95(v):
    if not v:
        return 0.0
    s = sorted(v)
    i = int(len(s) * 0.95 + 0.999999) - 1
    return s[min(max(i, 0), len(s) - 1)]


def nth_largest(v, n):
    if n <= 0 or len(v) < n:
        return 0.0
    return sorted(v, reverse=True)[n - 1]


def split_rhythm(r):
    i = r.rfind("@")
    if i < 0 or not r.endswith("s"):
        return None, 0
    try:
        p = float(r[i + 1:-1])
    except ValueError:
        return None, 0
    return (r[:i], p) if p > 0 else (None, 0)


def known_rhythm(seen, r, ranges=None):
    dest, per = split_rhythm(r)
    if dest is None:
        return False
    for s in seen:
        d2, p2 = split_rhythm(s)
        if d2 == dest and abs(p2 - per) <= 0.25 * per:
            return True
    if ranges and dest in ranges:  # fleet: a period range per destination
        lo, hi = ranges[dest]
        if lo * 0.75 <= per <= hi * 1.25:
            return True
    return False


class Flag:
    __slots__ = ("dev", "w", "last", "windows", "max", "feature", "value", "base", "why", "event")

    def __init__(self, dev, w, feature, value=0.0, base=0.0, why=""):
        self.dev, self.w, self.last, self.windows, self.max = dev, w, w, 1, value
        self.feature, self.value, self.base, self.why, self.event = feature, value, base, why, None


def detect(devs, warmup=12, k=3.0, recur=3, fleet=None, mode="local", n0=None, skip=None):
    """mode: local (the online drift detector), fleet (class signature only), combined.

    fleet: object with .ref(dev, feature, window) -> fleet reference or None,
           .known_domain(dev, dom, window) -> bool, .rhythm_ranges(dev) -> {dest: (lo, hi)}
    n0:    function (dev, feature) -> pseudo-count for shrinkage (combined mode)
    skip:  number of leading windows to drop per device {key: n} (pre-existing replay)
    """
    out = []
    for key in sorted(devs):
        d = devs[key]
        wins = d.windows[(skip or {}).get(key, 0):]
        prior, seen, rseen, open_ = [], set(), [], {}
        ranges = fleet.rhythm_ranges(d) if fleet and mode != "local" else None
        for w in wins:
            fl, still = [], {}
            n = len(prior)
            if n >= warmup:
                for fname, floor in FEATURES:
                    if fname not in w.vals:
                        continue
                    x = w.vals[fname]
                    vals = [c.vals.get(fname, 0.0) for c in prior]
                    loc = max(p95(vals), nth_largest(vals, recur)) if vals else 0.0
                    if mode == "local":
                        ref = loc
                    else:
                        fr = fleet.ref(d, fname, w)
                        if mode == "fleet":
                            if fr is None:
                                continue
                            ref = fr
                        else:  # combined
                            if fr is None:
                                ref = loc
                            else:
                                m = n0(d, fname)
                                ref = (m * fr + n * loc) / (m + n) if (m + n) > 0 else fr
                    if not (x > ref * k and x - ref >= floor):
                        continue
                    ep = open_.get(fname)
                    if ep is not None and (x <= ep.max * k or (ep.windows == 1 and x <= ep.max * k * k)) and not w.start > ep.last.end:
                        ep.last, ep.windows, ep.max = w, ep.windows + 1, max(ep.max, x)
                        still[fname] = ep
                        continue
                    nf = Flag(d, w, fname, x, ref, f"{x:.0f} vs {ref:.0f}")
                    fl.append(nf)
                    still[fname] = nf
                fresh = [dom for dom in w.domains
                         if dom not in seen
                         and not (mode != "local" and fleet.known_domain(d, dom, w))]
                if fresh:
                    fl.append(Flag(d, w, "new_domain", why="first contact with " + ", ".join(fresh)))
                newr = [r for r in w.rhythms
                        if not known_rhythm(rseen, r, ranges)]
                if newr:
                    fl.append(Flag(d, w, "new_rhythm", why="new rhythm " + ", ".join(newr)))
            rseen.extend(w.rhythms)
            open_ = still
            seen.update(w.domains)
            prior.append(w)
            out.extend(fl)
    out.sort(key=lambda f: f.w.start)
    return out


def score(flags, events, devs, warmup, skip=None):
    for e in events:
        e["caught"] = []
    for f in flags:
        span = f.w.end - f.w.start
        for e in events:
            if e["key"] != f.dev.key:
                continue
            if not e["t"] < f.w.start - span and e["t"] < f.last.end:
                f.event = e
                e["caught"].append(f)
                break
    fp = [f for f in flags if f.event is None]
    days = 0.0
    for key, d in devs.items():
        wins = d.windows[(skip or {}).get(key, 0):]
        for i, w in enumerate(wins):
            if i >= warmup:
                days += (w.end - w.start).total_seconds()
    days /= 86400
    scored = []
    for e in events:
        d = devs.get(e["key"])
        if d is None:
            continue
        wins = d.windows[(skip or {}).get(e["key"], 0):]
        if len(wins) <= warmup or e["t"] < wins[warmup].start:
            continue  # in warm-up: not scored
        scored.append(e)
    caught = sum(1 for e in scored if e["caught"])
    return {"fp": len(fp), "fp_flags": fp, "days": days, "caught": caught, "scored": len(scored),
            "fp_rate": len(fp) / days if days else float("nan")}
