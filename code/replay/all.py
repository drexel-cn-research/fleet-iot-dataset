"""Fleet replay: every replay result for the paper in one run.

    python3 code/replay/all.py [DATA_DIR] > results/fleet-replay.txt

DATA_DIR is the dataset's data/ directory (default: ../../data).
"""
import os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
if len(sys.argv) > 1: os.environ["REPLAY_DATA"] = sys.argv[1]
for part in ["eval_fleet.py", "sweeps.py", "preexisting.py", "rq1.py"]:
    print(f"\n######## {part}\n", flush=True)
    src = open(os.path.join(HERE, part)).read()
    if part == "eval_fleet.py":
        src = src.replace("if __name__=='__main__':", "if True:")
    exec(compile(src, part, "exec"), {"__name__": "replay_part", "__file__": os.path.join(HERE, part)})
