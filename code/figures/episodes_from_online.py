"""Extract flagged episodes from an online detector log (detector-online.txt)
into the pipe-separated input of timeline_v2.py:

    time|device|feature|windows|scenario-or-dash

    python3 episodes_from_online.py ../../data/runs/N2/detector-online.txt YEAR > N2-episodes.txt

Times are the log's local times (UTC-4 for these runs)."""
import re, sys
from datetime import datetime
src, year = sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else "2026"
seen, out = set(), []
for line in open(src):
    m = re.match(r"\s+(\w{3} \d+ \d\d:\d\d)\s+(\S+)\s+(\S+)\s+(.*)", line)
    if not m or "[" not in line:
        continue
    t = datetime.strptime(f"{year} {m.group(1)}", "%Y %b %d %H:%M").strftime("%Y-%m-%d %H:%M")
    w = re.search(r"\((\d+) windows", line)
    s = re.search(r"\[matches (\w+) at", line)
    rec = f"{t}|{m.group(2)}|{m.group(3)}|{w.group(1) if w else 1}|{s.group(1) if s else '-'}"
    if rec not in seen:
        seen.add(rec); out.append(rec)
print("\n".join(out))
