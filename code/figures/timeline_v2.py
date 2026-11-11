"""Run timeline strip chart (v2): one lane per simulated device, grouped by fleet reference.
Input: pipe-separated episodes  time|device|feature|windows|scenario-or-dash
Optional: false alarms that remain with the fleet prior  time|device|feature
Usage: timeline_v2.py episodes.txt start end out.pdf [fleet-remaining.txt]"""
import sys, datetime as dt
import matplotlib
matplotlib.use('Agg')
matplotlib.rcParams.update({'pdf.fonttype': 42, 'ps.fonttype': 42,
    'font.family': 'sans-serif', 'font.sans-serif': ['Helvetica', 'Nimbus Sans', 'Liberation Sans', 'DejaVu Sans'],
    'font.size': 8, 'axes.linewidth': 0.5})
import matplotlib.pyplot as plt
import matplotlib.dates as mdates

src, start, end, out = sys.argv[1:5]
remain_src = sys.argv[5] if len(sys.argv) > 5 else None
P = lambda s: dt.datetime.strptime(s, '%Y-%m-%d %H:%M')
t0, t1 = P(start), P(end)
LANES = ['hik-cam-1','hik-cam-2','wyze-cam-1','wyze-cam-2','ring-doorbell',
         'kasa-plug-1','kasa-plug-2','tuya-plug-1','tuya-plug-2','shelly-plug',
         'hue-bridge','echo-dot','nest-mini-1','nest-mini-2','sonos-one',
         'roku','lg-tv','ecobee','nest-thermostat','hp-printer']
TWINS_ORDER = ['hik-cam-1','hik-cam-2','wyze-cam-1','wyze-cam-2','kasa-plug-1','kasa-plug-2',
               'tuya-plug-1','tuya-plug-2','nest-mini-1','nest-mini-2']
OTHERS = [d for d in LANES if d not in TWINS_ORDER]
# Group lanes: devices with a same-product fleet reference on top, the rest below, one blank row between.
y = {d: len(OTHERS) - 1 - i for i, d in enumerate(OTHERS)}
DIV = len(OTHERS)
y.update({d: DIV + len(TWINS_ORDER) - i for i, d in enumerate(TWINS_ORDER)})
TOP = DIV + len(TWINS_ORDER)
BLUE, ORANGE = '#2A5CAA', '#D97B00'
TWINS = {'hik-cam-1','hik-cam-2','wyze-cam-1','wyze-cam-2','kasa-plug-1','kasa-plug-2','tuya-plug-1','tuya-plug-2','nest-mini-1','nest-mini-2'}
W = dt.timedelta(minutes=5)

eps = []
for line in open(src):
    if line.strip():
        t, dev, feat, n, scen = line.strip().split('|')
        eps.append((P(t), dev, feat, int(n), scen))
remain = []
if remain_src:
    for line in open(remain_src):
        if line.strip():
            t, dev, feat = line.strip().split('|')
            remain.append((P(t), dev, feat))

def stays(t, dev, feat, n):
    return any(d == dev and f == feat and t - W <= rt <= t + W * (n + 1) for rt, d, f in remain)

fig, ax = plt.subplots(figsize=(3.45, 2.45))
ax.axvspan(t0, t0 + dt.timedelta(hours=1), color='#EEEEEE', lw=0, zorder=0)
ax.text(t0 + dt.timedelta(minutes=30), TOP + 0.65, 'warm-up', ha='center', va='bottom', fontsize=6.8, color='#666666')
inj = [e for e in eps if e[4] != '-']
if inj:
    s, f = min(e[0] for e in inj), max(e[0] for e in inj) + W
    ax.axvspan(s, f, color='#E3EAF6', lw=0, zorder=0)
    ax.text(s + (f - s) / 2, TOP + 0.65, 'injections', ha='center', va='bottom', fontsize=6.8, color=BLUE)
for yy in y.values():
    ax.axhline(yy, color='#E6E6E6', lw=0.4, zorder=1)
ax.axhline(DIV, color='0.55', lw=0.6, ls=(0, (3, 2)), zorder=1)
if remain_src:
    ax.text(t1 - dt.timedelta(minutes=10), DIV + 0.12, 'same product in fleet', ha='right', va='bottom',
            fontsize=6.0, style='italic', color='0.35')
    ax.text(t1 - dt.timedelta(minutes=10), DIV - 0.12, 'no same-product fleet reference', ha='right', va='top',
            fontsize=6.0, style='italic', color='0.35')

seen = set()
for t, dev, feat, n, scen in eps:
    if dev in seen and scen != '-':
        continue
    yy = y[dev]
    if scen != '-':
        seen.add(dev)
        ax.plot(t, yy, marker='o', ms=3.8, mfc=BLUE, mec='white', mew=0.5, zorder=4, ls='none')
    elif remain_src is None or stays(t, dev, feat, n):
        ax.plot(t + W * n / 2, yy, marker='X', ms=4.6, mfc=ORANGE, mec=ORANGE, mew=0.5, zorder=3, ls='none')
    else:
        ax.plot(t + W * n / 2, yy, marker='X', ms=4.4, mfc='white', mec=ORANGE, mew=0.7, zorder=3, ls='none')

order = sorted(y, key=lambda d: y[d])
ax.set_yticks([y[d] for d in order])
ax.set_yticklabels(order, fontsize=6.3)
if remain_src:
    for lab in ax.get_yticklabels():
        if lab.get_text() in TWINS: lab.set_fontweight('bold')
ax.set_ylim(-0.7, TOP + 1.5)
ax.set_xlim(t0, t1)
ax.xaxis.set_major_locator(mdates.HourLocator(byhour=range(0, 24, 2)))
ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M'))
ax.tick_params(axis='both', length=2, width=0.5, pad=1.5, labelsize=6.8)
for s in ('top', 'right'): ax.spines[s].set_visible(False)
ax.set_xlabel('local time (5-minute windows)', fontsize=7, labelpad=1.5)
h = [ax.plot([], [], marker='o', ms=3.8, mfc=BLUE, mec='white', ls='none', label='injection detected')[0]]
if remain_src:
    h.append(ax.plot([], [], marker='X', ms=4.6, mfc=ORANGE, mec=ORANGE, mew=0.5, ls='none', label='false alarm, remains')[0])
    h.append(ax.plot([], [], marker='X', ms=4.4, mfc='white', mec=ORANGE, mew=0.7, ls='none', label='removed by fleet prior')[0])
else:
    h.append(ax.plot([], [], marker='X', ms=4.4, mfc=ORANGE, mec='white', ls='none', label='false alarm')[0])
leg = ax.legend(handles=h, loc='upper right', bbox_to_anchor=(1.0, 0.97), ncol=1, frameon=True, fancybox=False, framealpha=1.0, edgecolor='0.6', fontsize=6.5, handletextpad=0.3, borderpad=0.35, labelspacing=0.3)
leg.get_frame().set_linewidth(0.5)
fig.tight_layout(pad=0.2)
fig.savefig(out, dpi=300)
print('saved', out)
