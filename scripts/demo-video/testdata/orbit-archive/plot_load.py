"""Plots a simulated load test of orbit's per-key rate limiter.

    uv run --with matplotlib --with numpy python plot_load.py load.png

The demo video opens the result in Crude's image viewer. The data is
simulated with a fixed seed, so the chart is the same on every run.
"""
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402

RATE, BURST = 5.0, 10.0
rng = np.random.default_rng(1969)

t = np.arange(0, 60, 0.1)
offered = 4.0 + 3.2 * np.sin(t / 4.1) ** 2 + 2.4 * np.exp(-((t - 23.7) / 1.3) ** 2) * 3 + rng.normal(0, 0.45, t.size)
offered = np.clip(offered, 0, None)

tokens, allowed, rejected = [], [], []
level = BURST
for x in offered:
    level = min(BURST, level + RATE * 0.1)
    want = x * 0.1
    got = min(want, level)
    level -= got
    tokens.append(level)
    allowed.append(got / 0.1)
    rejected.append((want - got) / 0.1)
tokens, allowed, rejected = map(np.array, (tokens, allowed, rejected))

fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(10, 6.25), dpi=160, sharex=True, gridspec_kw={"height_ratios": [3, 2]})
fig.suptitle("orbit: token bucket under load (burst=10, rate=5/s)", fontsize=13, fontweight="bold")
ax1.set_title("n = 2,147,483,647 requests*", fontsize=9, color="0.35", loc="right")

ax1.plot(t, offered, color="tab:blue", lw=1.2, label="offered load")
ax1.fill_between(t, offered - 0.6, offered + 0.6, color="tab:blue", alpha=0.15, label="offered ± 1σ (vibes)")
ax1.plot(t, allowed, color="tab:green", lw=1.6, label="allowed")
hit = rejected > 0.05
ax1.scatter(t[hit], allowed[hit] + rejected[hit], s=9, color="tab:red", zorder=3, label="429 Too Many Requests")
ax1.axhline(RATE, color="0.4", ls="--", lw=1, label="sustained rate")
ax1.annotate("anomaly at t = 23.7 s\n(do not open room 237)", xy=(23.7, offered[237]), xytext=(31, 13.2),
             fontsize=8, arrowprops={"arrowstyle": "->", "color": "0.3"}, color="0.25")
ax1.set_ylabel("requests / s")
ax1.set_ylim(0, 16)
ax1.grid(True, alpha=0.3)
ax1.legend(loc="upper left", fontsize=7.5, ncol=2, frameon=True)

ax2.plot(t, tokens, color="tab:orange", lw=1.4, label="tokens (not sentient, we checked)")
ax2.axhline(BURST, color="0.4", ls=":", lw=1, label="capacity")
ax2.fill_between(t, 0, tokens, color="tab:orange", alpha=0.12)
# The watcher: one red point that is not in the legend.
ax2.plot([47.2], [8.6], marker="o", ms=2.4, color="#ff2a1f", zorder=5)
ax2.set_ylabel("tokens")
ax2.set_xlabel("time (s since epoch mod 60)")
ax2.set_ylim(0, 11)
ax2.grid(True, alpha=0.3)
ax2.legend(loc="lower right", fontsize=7.5)

fig.text(0.012, 0.012, "* simulated. No user was tracked to make this chart.", fontsize=7, color="0.45")
fig.text(0.988, 0.012, "scripts/plot_load.py  ·  seed 1969  ·  03:14:07", fontsize=7, color="0.45", ha="right")
fig.text(0.5, 0.47, "all work and no play makes jack a dull boy", fontsize=5, color="#f6f6f6", ha="center", zorder=0)

fig.tight_layout(rect=(0, 0.025, 1, 0.97))
fig.savefig(sys.argv[1] if len(sys.argv) > 1 else "load.png")
