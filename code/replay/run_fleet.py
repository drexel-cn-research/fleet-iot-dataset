import os, sys, glob
HERE = os.path.dirname(os.path.abspath(__file__)); sys.path.insert(0, HERE)
from replay import *
from fleet import *
# REPLAY_DATA points at the dataset's data/ directory (default: ../../data relative to this file).
D = os.environ.get('REPLAY_DATA', os.path.join(HERE, '..', '..', 'data'))
FL = [f'{D}/homes/pi4.json', f'{D}/homes/pi5.json']
RUNS = ['N1', 'N2', 'Day', 'Ctrl', 'Pop']   # the five reported runs (Table 5)
data = {}
for n in RUNS:
    devs = load_run(f'{D}/runs/{n}/summaries'); name_devices(devs, FL)
    data[n] = (devs, load_events(sorted(glob.glob(f'{D}/runs/{n}/injections-*.jsonl'))))
