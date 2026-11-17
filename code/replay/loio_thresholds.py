"""LOIO sensitivity to the detector's thresholds: the multiplicative factor k
(2, 3, 4) and every absolute floor scaled by 0.5, 1 or 2. Same evaluation as
loio_all.py with a 1-h warm-up; devices grouped by whether their model has
another instance. k=3 with floors x1 reproduces Table 6."""
import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
import replay as R
BASE=list(R.FEATURES)
def strip(devs,key): return {k:v for k,v in devs.items() if k!=key}
def has_twin(d):
    for n,(devs,ev) in data.items():
        for o in devs.values():
            if o.key!=d.key and o.home!='?' and o.cls.get('model')==d.cls.get('model') and o.cls.get('vendor')==d.cls.get('vendor') and o.cls.get('type')==d.cls.get('type'):
                return True
    return False
TW={(n,k):has_twin(d) for n,(devs,ev) in data.items() for k,d in devs.items() if d.home!='?'}
cache={}
for n,(devs,ev) in data.items():
    for key,d in devs.items():
        if d.home=='?': continue
        tr=[(m,strip(data[m][0],key),data[m][1]) for m in data if m!=n]
        cache[(n,key)]=(Signatures(tr,time_of_day=True), n0_estimates(tr))
W=12
for k in [2.0,3.0,4.0]:
    for s in [0.5,1.0,2.0]:
        R.FEATURES[:]=[(f,fl*s) for f,fl in BASE]
        agg={}
        for n,(devs,ev) in data.items():
            for key,d in devs.items():
                if d.home=='?': continue
                sig,est=cache[(n,key)]
                n0=lambda dd,f,est=est: est.get((dd.cls.get('type'),f),{}).get('n0',0.0)
                one={key:d}; e1=[dict(e) for e in ev if e['key']==key]
                g='twin' if TW[(n,key)] else 'single'
                for mode in ['local','fleet','combined']:
                    fl=R.detect(one,W,k=k,fleet=sig,mode=mode,n0=n0); r=score(fl,[dict(e) for e in e1],one,W)
                    for gg in (g,'all'):
                        a=agg.setdefault(f"{gg}|{mode}",[0,0.0,0,0]); a[0]+=r['fp']; a[1]+=r['days']; a[2]+=r['caught']; a[3]+=r['scored']
        for key_ in sorted(agg):
            v=agg[key_]; print(f"k={k:g} floors x{s:<3g} w{W} {key_:16} FP {v[0]:3}/{v[1]:5.1f} = {v[0]/v[1]:.2f}  det {v[2]}/{v[3]}", flush=True)
R.FEATURES[:]=BASE
