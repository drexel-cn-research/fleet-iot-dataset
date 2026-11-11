"""Leave-one-instance-out fleet replay: each device's signature is built only
from other instances of its class in the other runs (never from itself)."""
import os, json
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
def strip(devs,key): return {k:v for k,v in devs.items() if k!=key}
# a device has a twin if another simulated device in the runs shares its model-level class
def has_twin(d):
    for n,(devs,ev) in data.items():
        for o in devs.values():
            if o.key!=d.key and o.home!='?' and o.cls.get('model')==d.cls.get('model') and o.cls.get('vendor')==d.cls.get('vendor') and o.cls.get('type')==d.cls.get('type'):
                return True
    return False
cache={}
def sig_for(n,key,tod):
    k=(n,key,tod)
    if k not in cache:
        tr=[(m,strip(data[m][0],key),data[m][1]) for m in data if m!=n]
        cache[k]=(Signatures(tr,time_of_day=tod), n0_estimates(tr))
    return cache[k]
out={}
remain_n2=[]
for tod in [True, False]:
    for w in [0,3,6,12,24]:
        if not tod and w not in (0,12): continue
        agg={}
        for n,(devs,ev) in data.items():
            for key,d in devs.items():
                if d.home=='?': continue
                grp='twin' if has_twin(d) else 'single'
                sig,est=sig_for(n,key,tod)
                n0=lambda dd,f,est=est: est.get((dd.cls.get('type'),f),{}).get('n0',0.0)
                one={key:d}
                for mode in ['local','fleet','combined']:
                    if mode=='local' and not tod: continue
                    e1=[dict(e) for e in ev if e['key']==key]
                    fl=detect(one,w,fleet=sig,mode=mode,n0=n0); r=score(fl,e1,one,w)
                    for g in (grp,'all'):
                        a=agg.setdefault(f"{g}|{mode}",[0,0.0,0,0]); a[0]+=r['fp']; a[1]+=r['days']; a[2]+=r['caught']; a[3]+=r['scored']
                    pr=agg.setdefault(f"run|{n}|{mode}",[0,0.0,0,0]); pr[0]+=r['fp']; pr[1]+=r['days']; pr[2]+=r['caught']; pr[3]+=r['scored']
                    if tod and w==12 and n=='N2' and mode=='combined':
                        remain_n2 += [f"{(f.w.start+UTC_OFFSET):%Y-%m-%d %H:%M}|{f.dev.name}|{f.feature}" for f in r['fp_flags']]
        out[f"tod={tod} w={w}"]={k:{'fp':v[0],'days':round(v[1],2),'rate':round(v[0]/v[1],2) if v[1] else None,'caught':v[2],'scored':v[3]} for k,v in agg.items()}
        print(f"== tod={tod} w={w}")
        for k in sorted(agg):
            v=agg[k]; print(f"  {k:22} FP {v[0]:4} / {v[1]:5.1f} = {v[0]/v[1]:.2f}   det {v[2]}/{v[3]}")
json.dump(out,open('loio-results.json','w'),indent=1)
open('run7-fleet-remaining-loio.txt','w').write("\n".join(remain_n2)+"\n")
print("twins:", sorted({d.name for n,(devs,ev) in data.items() for d in devs.values() if d.home!='?' and has_twin(d)}))
