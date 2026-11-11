#!/bin/sh
# Regenerate everything in results/ from data/. Run from the repository root.
set -e
ROOT=$(cd "$(dirname "$0")" && pwd)
D=$ROOT/data/runs
mkdir -p "$ROOT/results/figures"
cd "$ROOT/results"

echo "fleet replay, leave-one-instance-out (Table 6, Figure 3, no warm-up)"
python3 ../code/replay/loio_all.py > loio-all.txt
echo "robustness (tau, h_min, n0 cap, injection-window exclusion)"
python3 ../code/replay/loio_robust.py > loio-robust.txt
echo "changes present from the first window"
python3 ../code/replay/loio_preexisting.py > loio-preexisting.txt
echo "RQ1: n0 per device type and fleet signatures"
python3 ../code/replay/rq1.py > rq1.txt
echo "run-level holdout, sweeps and ablations"
python3 ../code/replay/all.py "$ROOT/data" > fleet-replay.txt

echo "isolation-forest baseline"
python3 ../code/baseline/iforest-baseline.py \
  --run N1=$D/N1/summaries:$D/N1/injections-pi4.jsonl,$D/N1/injections-pi5.jsonl \
  --run N2=$D/N2/summaries:$D/N2/injections-pi4.jsonl,$D/N2/injections-pi5.jsonl \
  --run Day=$D/Day/summaries:$D/Day/injections-pi4.jsonl,$D/Day/injections-pi5.jsonl \
  --clean Ctrl=$D/Ctrl/summaries \
  --drift N1=$D/N1/detector-online.txt --drift Day=$D/Day/detector-online.txt --drift N2=$D/N2/detector-online.txt \
  > iforest-baseline.txt

echo "population drift rate (Table 7)"
python3 ../code/population/rho.py --summaries $D/Pop/summaries --drift $D/Pop/detector-online.txt \
  --fleets "$ROOT/data/homes/pi4.json,$ROOT/data/homes/pi5.json" > population-rho.txt

echo "physical cameras (Section 6.5)"
python3 ../code/replay/cam_cross.py "$ROOT/data/cameras/summaries" > camera-cross-replay.txt

echo "router cost (Section 6.4, Table 8)"
python3 ../code/perf/perf-report.py "$ROOT/data/perf/pop-perf.csv" > pop-perf.txt
while read step a b; do
  echo "-- $step"
  python3 ../code/perf/perf-report.py "$ROOT/data/perf/stress-perf.csv" --since $((a + 60)) --until "$b" | sed -n '/sensor agent (userspace/,$p'
done < "$ROOT/data/perf/stress.steps" > stress-perf.txt

echo "figures"
python3 ../code/figures/coldstart.py figures/fig-coldstart.pdf
python3 ../code/figures/timeline_v2.py "$ROOT/data/figures/N2-episodes.txt" '2026-10-24 17:15' '2026-10-25 05:50' \
  figures/fig-timeline.pdf "$ROOT/data/figures/N2-fleet-remaining.txt"
python3 ../code/figures/drift-panels.py figures/fig-drift.pdf
echo "done"
