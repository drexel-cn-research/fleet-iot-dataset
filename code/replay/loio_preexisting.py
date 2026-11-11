import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
TW={'hik-cam-1','hik-cam-2','wyze-cam-1','wyze-cam-2','kasa-plug-1','kasa-plug-2','tuya-plug-1','tuya-plug-2','nest-mini-1','nest-mini-2'}
def strip(devs,key): return {k:v for k,v in devs.items() if k!=key}
res={}
for n,(devs,ev) in data.items():
    for e in ev:
        d=devs.get(e['key'])
        if d is None: continue
        grp='twin' if d.name in TW else 'single'
        tr=[(m,strip(data[m][0],d.key),data[m][1]) for m in data if m!=n]; sig=Signatures(tr); est=n0_estimates(tr)
        n0=lambda dd,f: est.get((dd.cls.get('type'),f),{}).get('n0',0.0)
        idx=[i for i,w in enumerate(d.windows) if w.start<=e['t']<w.end][0]
        one={d.key:d}; skip={d.key:idx}; e2=dict(e)
        fl=detect(one,0,fleet=sig,mode='combined',n0=n0,skip=skip); r=score(fl,[e2],one,0,skip=skip)
        a=res.setdefault(grp,[0,0,0,0.0]); a[0]+=bool(e2['caught']); a[1]+=1; a[2]+=r['fp']; a[3]+=r['days']
for g,a in res.items(): print(g, f"caught {a[0]}/{a[1]}, FP {a[2]} over {a[3]:.1f} dd")
