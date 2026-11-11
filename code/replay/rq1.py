import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
from statistics import median
tr=[(m,)+data[m] for m in data]
sig=Signatures(tr); est=n0_estimates(tr)
types=sorted({k[0] for k in est})
for t in types:
    v=[est[(t,f)]['n0'] for f in FEATURE_NAMES if (t,f) in est]
    inst=max(est[(t,f)]['instances'] for f in FEATURE_NAMES if (t,f) in est)
    print(f"{t:17} instances {inst:2}  n0 median {median(v):6.1f}  min {min(v):5.1f}  capped {sum(1 for x in v if x>=144)}/{len(v)}")
print()
seen=set()
for n,(devs,ev) in data.items():
    for d in devs.values():
        if d.home=='?': continue
        lk=sig.match(d)
        if lk in seen: continue
        seen.add(lk)
        H=len(sig.homes[lk]); doms=sig.dom_homes[lk]
        core=[x for x in doms if len(doms[x])/H>=0.5]; rare=[x for x in doms if len(doms[x])/H<0.5]
        rr=sig.rhythm_ranges(d)
        print(f"{'/'.join(lk[1:]):40} homes {H:2}  domains supp>=.5: {len(core):2}  <.5: {len(rare):2} {sorted(rare)[:4]}  rhythms {len(rr)} {[(k,round(a),round(b)) for k,(a,b) in rr.items()][:2]}")
