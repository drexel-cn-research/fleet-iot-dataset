import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'run_fleet.py')).read())
import sys
TOD = '--no-tod' not in sys.argv
def evaluate(mode, warmup, verbose=False, tod=TOD):
    rows={}
    for n,(devs,ev) in data.items():
        tr=[(m,)+data[m] for m in data if m!=n]
        sig=Signatures(tr, time_of_day=tod)
        est=n0_estimates(tr)
        n0=lambda d,f: est.get((d.cls.get('type'),f),{}).get('n0',0.0)
        fl=detect(devs,warmup=warmup,fleet=sig,mode=mode,n0=n0)
        r=score(fl,ev,devs,warmup)
        rows[n]=r
        if verbose:
            for f in r['fp_flags']:
                print(f"   {n:4} FP {(f.w.start+UTC_OFFSET):%H:%M} {f.dev.name:15} {f.feature:10} {f.why[:90]}")
            for e in ev:
                if not e['caught']: print(f"   {n:4} MISS {e['device']} {e['scenario']}")
    return rows
def line(label, rows):
    fp=sum(r['fp'] for r in rows.values()); days=sum(r['days'] for r in rows.values())
    c=sum(r['caught'] for r in rows.values()); s=sum(r['scored'] for r in rows.values())
    per='  '.join(f"{n} {r['caught']}/{r['scored']} {r['fp']}fp {r['fp_rate']:.2f}" for n,r in rows.items())
    print(f"{label:22} caught {c}/{s}  FP {fp} / {days:.1f} d = {fp/days:.2f}   | {per}")
if __name__=='__main__':
    for mode,w in [('local',12),('fleet',12),('combined',12),('combined',0)]:
        line(f"{mode} w{w}", evaluate(mode,w))
