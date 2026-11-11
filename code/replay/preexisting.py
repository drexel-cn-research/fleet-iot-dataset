import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
from collections import Counter
res=Counter(); tot=Counter(); fpw=0; days=0
for n,(devs,ev) in data.items():
    tr=[(m,)+data[m] for m in data if m!=n]; sig=Signatures(tr); est=n0_estimates(tr)
    n0=lambda d,f: est.get((d.cls.get('type'),f),{}).get('n0',0.0)
    for e in ev:
        d=devs.get(e['key']);
        if d is None: continue
        idx=[i for i,w in enumerate(d.windows) if w.start<=e['t']<w.end][0]
        one={d.key:d}; skip={d.key:idx}
        for mode,w in [('local',12),('combined',0)]:
            fl=detect(one,warmup=w,fleet=sig,mode=mode,n0=n0,skip=skip)
            e2=dict(e); r=score(fl,[e2],one,w,skip=skip)
            hit=bool(e2['caught']); res[(mode,e['scenario'])]+=hit; tot[(mode,e['scenario'])]+=1
            if mode=='combined':
                fpw+=r['fp']; days+=r['days']
for k in sorted(tot): print(k, f"{res[k]}/{tot[k]}")
print("combined w0, false alarms on those device histories:", fpw, "over", round(days,1), "device-days")
