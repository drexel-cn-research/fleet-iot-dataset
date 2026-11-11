"""Fleet signatures built from other runs, for replay.detect(mode=fleet|combined)."""
import math
from collections import defaultdict
from datetime import timedelta

from replay import FEATURE_NAMES, p95, split_rhythm

UTC_OFFSET = timedelta(hours=-4)  # EDT, for time-of-day buckets


def bucket(w):
    h = (w.start + UTC_OFFSET).hour
    return "night" if h < 6 else ("day" if h < 17 else "evening")


def levels(cls):
    """Class keys from most to least specific (longest match first)."""
    t, v, m = cls.get("type"), cls.get("vendor"), cls.get("model")
    out = []
    if t and v and m:
        out.append(("model", t, v, m))
    if t and v:
        out.append(("vendor", t, v))
    if t:
        out.append(("type", t))
    return out


def injected_windows(dev, events, before=timedelta(minutes=5), after=timedelta(minutes=15)):
    """Windows of dev touched by an injection (excluded when building signatures)."""
    bad = set()
    for e in events:
        if e["key"] != dev.key:
            continue
        for i, w in enumerate(dev.windows):
            if w.end > e["t"] - before and w.start < e["t"] + after:
                bad.add(i)
    return bad


class Signatures:
    def __init__(self, training, h_min=3, tau=0.5, time_of_day=True, min_bucket=24):
        """training: list of (run_name, devs, events)."""
        self.h_min, self.tau, self.tod, self.min_bucket = h_min, tau, time_of_day, min_bucket
        homes = defaultdict(set)
        dom_homes = defaultdict(lambda: defaultdict(set))
        rh = defaultdict(lambda: defaultdict(list))
        rh_homes = defaultdict(lambda: defaultdict(set))
        vals = defaultdict(lambda: defaultdict(lambda: defaultdict(list)))  # key -> feature -> bucket -> values
        for run, devs, events in training:
            for d in devs.values():
                if d.home == "?":
                    continue  # household devices: not part of the fleet
                bad = injected_windows(d, events)
                home = (run, d.home)
                for lk in levels(d.cls):
                    homes[lk].add(home)
                for i, w in enumerate(d.windows):
                    if i in bad:
                        continue
                    for lk in levels(d.cls):
                        for dom in w.domains:
                            dom_homes[lk][dom].add(home)
                        for r in w.rhythms:
                            dest, per = split_rhythm(r)
                            if dest:
                                rh[lk][dest].append(per)
                                rh_homes[lk][dest].add(home)
                        for f in FEATURE_NAMES:
                            if f in w.vals:
                                vals[lk][f][bucket(w)].append(w.vals[f])
                                vals[lk][f]["all"].append(w.vals[f])
        self.homes, self.dom_homes, self.rh, self.rh_homes, self.vals = homes, dom_homes, rh, rh_homes, vals

    def match(self, dev):
        """Longest-match class key with at least h_min homes, or None."""
        for lk in levels(dev.cls):
            if len(self.homes.get(lk, ())) >= self.h_min:
                return lk
        return None

    def supp(self, lk, dom):
        return len(self.dom_homes[lk].get(dom, ())) / len(self.homes[lk])

    def known_domain(self, dev, dom, w):
        lk = self.match(dev)
        return lk is not None and self.supp(lk, dom) >= self.tau

    def rhythm_ranges(self, dev):
        lk = self.match(dev)
        if lk is None:
            return {}
        n = len(self.homes[lk])
        return {dest: (min(p), max(p)) for dest, p in self.rh[lk].items()
                if len(self.rh_homes[lk][dest]) / n >= self.tau}

    def ref(self, dev, f, w):
        lk = self.match(dev)
        if lk is None:
            return None
        v = self.vals[lk].get(f)
        if not v:
            return None
        if self.tod:
            b = v.get(bucket(w), [])
            if len(b) >= self.min_bucket:
                return max(p95(b), sorted(b, reverse=True)[2] if len(b) >= 3 else 0)
        a = v["all"]
        return max(p95(a), sorted(a, reverse=True)[2] if len(a) >= 3 else 0)


def n0_estimates(training, cap=144.0, floor=1.0):
    """n0 = within-device variance / between-device variance (log1p scale), per
    (type, feature), by method of moments, capped. Devices are instances in all
    training runs; between-device variance is that of device means minus the
    sampling share of within-device variance."""
    per = defaultdict(lambda: defaultdict(list))  # (type, f) -> instance -> values
    for run, devs, events in training:
        for d in devs.values():
            if d.home == "?":
                continue
            bad = injected_windows(d, events)
            t = d.cls.get("type")
            for i, w in enumerate(d.windows):
                if i in bad:
                    continue
                for f in FEATURE_NAMES:
                    if f in w.vals:
                        per[(t, f)][(run, d.key)].append(math.log1p(w.vals[f]))
    out = {}
    for k, inst in per.items():
        groups = [g for g in inst.values() if len(g) >= 2]
        if len(groups) < 2:
            continue
        means = [sum(g) / len(g) for g in groups]
        within = sum(sum((x - m) ** 2 for x in g) for g, m in zip(groups, means)) / sum(len(g) - 1 for g in groups)
        gm = sum(means) / len(means)
        nbar = sum(len(g) for g in groups) / len(groups)
        between = max(0.0, sum((m - gm) ** 2 for m in means) / (len(means) - 1) - within / nbar)
        n0 = cap if between <= 0 else min(cap, max(floor, within / between))
        out[k] = {"n0": n0, "within": within, "between": between, "instances": len(groups)}
    return out
