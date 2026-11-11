"""Figure: how local and fleet drift combine (stylized, not data)."""
import sys
import numpy as np
import matplotlib; matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.patches
plt.rcParams.update({"font.size": 7, "font.family": "sans-serif", "font.sans-serif": ["Helvetica", "Liberation Sans", "Arial"],
                     "mathtext.fontset": "custom", "mathtext.rm": "Liberation Sans", "mathtext.it": "Liberation Sans:italic", "pdf.fonttype": 42})
rng = np.random.default_rng(3)
T = np.arange(100); WARM = 20; CHANGE = 60
BAND = "#cfe0f3"; DEV = "#222222"; LOC = "#2f7d32"; FLAG = "#c62828"

def trace(level_before, level_after=None):
    y = level_before + rng.normal(0, 0.35, T.size)
    if level_after is not None:
        y[CHANGE:] = level_after + rng.normal(0, 0.35, T.size - CHANGE)
    return y

def local_ref(y):
    r = np.full(T.size, np.nan)
    for t in range(WARM, T.size):
        r[t] = np.sort(y[:t])[-3] + 0.4      # learned from own history, recurring behavior becomes normal
    return r

panels = [
    ("Expected", "local stable, fleet consistent", trace(4.5), (3, 6), None, None),
    ("Shared change", "local drift, fleet consistent ($\\rho_c$ high)", trace(4.5, 8.5), (3, 6), (7, 10), "local"),
    ("Isolated change", "local drift, fleet divergent", trace(4.5, 9.5), (3, 6), None, "both"),
    ("Population mismatch", "local stable, fleet divergent", trace(9.0), (3, 6), None, "fleet"),
]
fig, axes = plt.subplots(2, 2, figsize=(3.45, 2.55), sharex=True, sharey=True)
for ax, (title, sub, y, band, band_after, flag) in zip(axes.flat, panels):
    lo = np.full(T.size, band[0], float); hi = np.full(T.size, band[1], float)
    if band_after:
        lo[CHANGE:], hi[CHANGE:] = band_after
    ax.fill_between(T, lo, hi, color=BAND, lw=0, label="fleet expectation")
    ax.plot(T, y, color=DEV, lw=0.8, label="device")
    ax.plot(T, local_ref(y), color=LOC, lw=1.0, ls="--", label="own reference")
    ax.axvspan(0, WARM, color="#eeeeee", lw=0, zorder=0)
    if flag in ("local", "both"):
        ax.plot(CHANGE + 1, y[CHANGE + 1] + 0.9, marker="v", color=FLAG, ms=4)
    if flag == "fleet":
        ax.annotate("", xy=(4, 6.1), xytext=(4, 8.4), arrowprops=dict(arrowstyle="<->", color=FLAG, lw=0.8))
    if flag == "both":
        ax.annotate("", xy=(92, 6.1), xytext=(92, 8.9), arrowprops=dict(arrowstyle="<->", color=FLAG, lw=0.8))
    ax.set_title(f"{title}\n", fontsize=7.5, fontweight="bold", pad=1, loc="left")
    ax.text(0, 1.02, sub, transform=ax.transAxes, fontsize=6.3, va="bottom", color="#444444")
    ax.set_ylim(1.5, 11.5); ax.set_xlim(0, 99)
    ax.set_xticks([]); ax.set_yticks([])
    for s in ("top", "right"):
        ax.spines[s].set_visible(False)
axes[1, 0].set_xlabel("time", fontsize=6.5, labelpad=1)
axes[1, 1].set_xlabel("time", fontsize=6.5, labelpad=1)
axes[0, 0].set_ylabel("behavior", fontsize=6.5); axes[1, 0].set_ylabel("behavior", fontsize=6.5)
h, l = axes[0, 0].get_legend_handles_labels()
h.append(matplotlib.patches.Patch(color="#eeeeee")); l.append("warm-up")
h.append(plt.Line2D([], [], marker="v", color=FLAG, ls="none", ms=4)); l.append("local flag")
h.append(plt.Line2D([], [], marker="$\\updownarrow$", color=FLAG, ls="none", ms=5, markeredgewidth=0)); l.append("fleet gap")
fig.legend(h, l, loc="lower center", ncol=3, frameon=False, fontsize=6.3, handlelength=1.6, columnspacing=1.0, bbox_to_anchor=(0.5, -0.01))
fig.tight_layout(pad=0.25, h_pad=0.6, w_pad=0.5, rect=(0, 0.12, 1, 1))
fig.savefig(sys.argv[1] if len(sys.argv) > 1 else "fig-drift.pdf")
