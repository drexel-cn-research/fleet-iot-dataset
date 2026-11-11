import sys,json,glob
import os; sys.path.insert(0,os.path.dirname(os.path.abspath(__file__)))
import replay, fleet
from replay import Window, Device, ts
from datetime import timedelta
def load(d):
    devs={}
    for f in sorted(glob.glob(d+"/*.json")):
        p=json.load(open(f)); s,e=ts(p["period"]["start"]),ts(p["period"]["end"])
        if s < ts("2026-10-26T23:40:00Z") or s > ts("2026-10-28T10:05:00Z"): continue
        for x in p["devices"]:
            if (x.get("class") or {}).get("vendor")!="TP-Link" or x["ref"][:4] not in ("53c8","b6f8"): continue
            k=x["ref"][:4]; c=x.get("class",{}); cls={q:c.get(q) for q in ("type","vendor","model","firmware")}
            cls["type"]="camera"; cls["vendor"]="TP-Link"
            dv=devs.setdefault(k,Device(k,cls)); dv.home=k
            feats=x.get("envelope",{}).get("features",{}) or {}
            vals={q:float(v.get("max",0)) for q,v in feats.items()}
            vals["new_dest"]=float(x.get("totals",{}).get("new_destinations",0))
            doms=sorted((x.get("destinations") or {}).get("domains",{}) or {})
            rh=sorted("%s@%gs"%(r["dest"],r["period_s"]) for r in (x.get("rhythm") or []))
            dv.windows.append(Window(s,e,vals,doms,rh,cls))
    for v in devs.values(): v.windows.sort(key=lambda w:w.start)
    return devs
devs=load(sys.argv[1])
for wu in (12,0):
  print("== warm-up",wu)
  for tgt,ref in (("53c8","b6f8"),("b6f8","53c8")):
    one={tgt:devs[tgt]}
    sig=fleet.Signatures([("r",{ref:devs[ref]},[])],h_min=1,tau=0.5,time_of_day=False)
    loc=replay.detect(one,warmup=wu)
    for n0 in (144,):
      comb=replay.detect(one,warmup=wu,fleet=sig,mode="combined",n0=lambda d,f:n0)
      fl=replay.detect(one,warmup=wu,fleet=sig,mode="fleet")
      ep=lambda F: sorted({(f.w.start-timedelta(hours=4)).strftime("%m-%d %H:%M") for f in F})
      print(f" {tgt} vs ref {ref}: local {len(ep(loc))} {ep(loc)}\n    combined {len(ep(comb))} {ep(comb)}  [{', '.join(f.feature for f in comb)}]")
