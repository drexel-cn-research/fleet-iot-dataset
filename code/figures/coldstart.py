"""Figure: false alarms vs. warm-up, local-only vs. fleet prior, for devices whose model has
another instance in the fleet and for devices without one (leave-one-instance-out replay,
numbers from tools/fleet-replay/loio_all.py). Color = reference, line style = group."""
import sys
import matplotlib; matplotlib.use('Agg')
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
plt.rcParams.update({'font.size': 8.5, 'font.family': 'sans-serif', 'font.sans-serif': ['Helvetica', 'Liberation Sans', 'Arial'], 'pdf.fonttype': 42})
w = [0, 15, 30, 60, 120]
tw_loc = [8.31, 2.37, 1.26, 0.51, 0.06]; tw_cmb = [0.14, 0.09, 0.10, 0.05, 0.06]
sg_loc = [10.31, 4.85, 3.04, 1.98, 1.59]; sg_cmb = [9.01, 4.90, 3.09, 2.03, 1.65]
fig, ax = plt.subplots(figsize=(3.4, 2.15))
G, B = '#444444', '#1f5fa8'
ax.plot(w, tw_loc, 'o-', color=G, ms=3.4, lw=1.3)
ax.plot(w, tw_cmb, 's-', color=B, ms=3.4, lw=1.3)
ax.plot(w, sg_loc, 'o--', color=G, ms=2.8, lw=1.0, alpha=0.75, dashes=(3, 1.5))
ax.plot(w, sg_cmb, 's--', color=B, ms=2.8, lw=1.0, alpha=0.75, dashes=(3, 1.5))
ax.set_yscale('log'); ax.set_yticks([0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10]); ax.set_yticklabels(['0.05', '0.1', '0.25', '0.5', '1', '2', '5', '10'])
ax.minorticks_off()
ax.set_xticks(w); ax.set_xticklabels([str(x) for x in w])
ax.set_xlabel('warm-up (minutes of local history)'); ax.set_ylabel('false alarms / device-day')
lab = dict(fontsize=7.5, textcoords='offset points')
ax.annotate('0.14', (0, 0.14), xytext=(-4, 6), color=B, **lab)
for x, y in zip(w[1:-1], tw_cmb[1:-1]): ax.annotate(f'{y:.2f}', (x, y), xytext=(-9, -11), color=B, **lab)
ax.annotate('2.37', (15, 2.37), xytext=(-14, -13), color=G, **lab)
for x, y in zip(w[2:-1], tw_loc[2:-1]): ax.annotate(f'{y:.2f}', (x, y), xytext=(4, 3), color=G, **lab)
ax.annotate('8.31', (0, 8.31), xytext=(6, 4), color=G, **lab)
ax.annotate('0.06', (120, 0.06), xytext=(-8, 6), color=G, **lab)
ax.spines[['top', 'right']].set_visible(False)
ax.set_ylim(0.025, 20); ax.set_xlim(-5, 130)
h = [Line2D([], [], color=G, marker='o', ms=3.4, lw=1.3, label='local only'),
     Line2D([], [], color=G, lw=1.2, dashes=(3, 1.5), label='local only\n(no other instance)'),
     Line2D([], [], color=B, marker='s', ms=3.4, lw=1.3, label='fleet prior'),
     Line2D([], [], color=B, lw=1.2, dashes=(3, 1.5), label='fleet prior\n(no other instance)')]
leg = ax.legend(handles=h, frameon=True, fancybox=False, framealpha=1.0, edgecolor='0.6', borderpad=0.3, loc='upper right', bbox_to_anchor=(1.0, 1.0), ncol=2, fontsize=6.8, handlelength=1.4, handletextpad=0.35, labelspacing=0.2, columnspacing=0.5)
leg.get_frame().set_linewidth(0.5)
fig.tight_layout(pad=0.3); fig.savefig(sys.argv[1] if len(sys.argv) > 1 else 'fig-coldstart.pdf', dpi=300)
