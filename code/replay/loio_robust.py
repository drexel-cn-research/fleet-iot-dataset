"""LOIO robustness: injection-window exclusion on/off, tau, h_min, n0 cap.
Same evaluation as loio_all.py (1-h warm-up and no warm-up), devices grouped
by whether their model has another instance."""
import os, sys, json
import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
def strip(devs,key): return {k:v for k,v in devs.items() if k!=key}
def has_twin(d):
    for n,(devs,ev) in data.items():
        for o in devs.values():
            if o.key!=d.key and o.home!='?' and o.cls.get('model')==d.cls.get('model') and o.cls.get('vendor')==d.cls.get('vendor') and o.cls.get('type')==d.cls.get('type'):
                return True
    return False
TW={(n,k):has_twin(d) for n,(devs,ev) in data.items() for k,d in devs.items() if d.home!='?'}
def run(label, exclude=True, tau=0.5, h_min=3, cap=144.0, warmups=(12,0)):
    res={}
    for w in warmups:
        agg={}
        for n,(devs,ev) in data.items():
            for key,d in devs.items():
                if d.home=='?': continue
                tr=[(m,strip(data[m][0],key),data[m][1] if exclude else []) for m in data if m!=n]
                sig=Signatures(tr,h_min=h_min,tau=tau,time_of_day=True); est=n0_estimates(tr,cap=cap)
                n0=lambda dd,f,est=est: est.get((dd.cls.get('type'),f),{}).get('n0',0.0)
                one={key:d}; e1=[dict(e) for e in ev if e['key']==key]
                g='twin' if TW[(n,key)] else 'single'
                for mode in (['local','combined'] if label=='baseline' else ['combined']):
                    fl=detect(one,w,fleet=sig,mode=mode,n0=n0); r=score(fl,[dict(e) for e in e1],one,w)
                    for gg in (g,'all'):
                        a=agg.setdefault(f"{gg}|{mode}",[0,0.0,0,0]); a[0]+=r['fp']; a[1]+=r['days']; a[2]+=r['caught']; a[3]+=r['scored']
        for k in sorted(agg):
            v=agg[k]; print(f"{label:16} w{w:<2} {k:16} FP {v[0]:3}/{v[1]:5.1f} = {v[0]/v[1]:.2f}  det {v[2]}/{v[3]}", flush=True)
        res[w]=agg
    return res
V=[('baseline',{}),('no-exclusion',{'exclude':False}),('tau=0.25',{'tau':0.25}),('tau=0.75',{'tau':0.75}),
   ('h_min=2',{'h_min':2}),('h_min=4',{'h_min':4}),('cap=72',{'cap':72.0}),('cap=288',{'cap':288.0})]
only=sys.argv[1:]
for lab,kw in V:
    if only and lab not in only: continue
    run(lab,**kw)
