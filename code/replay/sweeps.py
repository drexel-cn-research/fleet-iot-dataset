import os
exec(open(os.path.join(os.path.dirname(os.path.abspath(__file__)),'eval_fleet.py')).read().split('if __name__')[0])
import fleet as F
print("== warm-up sweep (time of day on)")
for w in [0,3,6,12,24]:
    line(f"local w{w}", evaluate('local',w)); line(f"combined w{w}", evaluate('combined',w))
print("== tau sweep, combined w12")
orig=F.Signatures.__init__
for tau in [0.25,0.5,0.75]:
    def init(self,training,h_min=3,tau=tau,time_of_day=True,min_bucket=24): orig(self,training,h_min,tau,time_of_day,min_bucket)
    F.Signatures.__init__=init; Signatures=F.Signatures
    line(f"tau {tau}", evaluate('combined',12))
F.Signatures.__init__=orig
print("== time-of-day ablation")
for w in [0,12]:
    line(f"combined w{w} tod", evaluate('combined',w,tod=True)); line(f"combined w{w} no-tod", evaluate('combined',w,tod=False))
