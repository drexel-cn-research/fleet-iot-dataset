package main

// iotsim drift: a simple, transparent drift check over the sensor summaries,
// scored against the simulator's ground-truth injection log.
//
// For each device it builds a baseline from that device's own earlier
// windows (skipping windows it already flagged, so one spike doesn't
// poison the baseline), then flags a window when a feature far exceeds
// anything in the baseline, or when the device talks to a domain it never
// used before. Flags are matched to injected events, so the output says
// what was caught, what was missed, and what fired with no injection
// behind it.
//
// This is a stand-in until the signature-based detector (wave 2) exists,
// and a scoring harness for it afterwards.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// feature is one number per device per window, with the minimum absolute
// rise that can count as drift (so tiny baselines don't flag noise).
type feature struct {
	name   string
	floor  float64
	isByte bool
}

var driftFeatures = []feature{
	{"tx_bytes", 1 << 20, true},
	{"rx_bytes", 1 << 20, true},
	{"flows", 10, false},
	{"dst_count", 3, false},
	{"lan_peers", 3, false},
	{"new_dest", 3, false},
	{"conns", 8, false}, // TCP connections opened (the sensor with connection counts)
}

type dwindow struct {
	start, end time.Time
	vals       map[string]float64
	domains    []string
	rhythms    []string // "destination:port@period", rigid contact rhythms in the window
	class      string
}

type ddevice struct {
	key     string // OUI query from the summary (first 9 hex digits of the MAC)
	label   string
	windows []*dwindow
}

type dflag struct {
	dev     *ddevice
	w       *dwindow // first window of the episode
	last    *dwindow // last window of the episode (== w for one window)
	windows int      // windows in the episode
	max     float64  // largest value in the episode
	feature string
	value   float64
	base    float64
	why     string
	event   *devent
}

type devent struct {
	Time     time.Time `json:"-"`
	TimeStr  string    `json:"time"`
	Device   string    `json:"device"`
	MAC      string    `json:"mac"`
	Type     string    `json:"type"`
	Scenario string    `json:"scenario"`
	Pi       string    `json:"pi"`
	key      string
	caught   []*dflag
}

func cmdDrift(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("drift", flag.ExitOnError)
	dir := fs.String("summaries", "", "folder of the sensor summary JSON files (required)")
	control := fs.String("control", "", "comma-separated Pi control addresses (host:7070) for device names and the injection log")
	eventsFile := fs.String("events", "", "local events.jsonl file(s), comma-separated, instead of or as well as --control")
	warmup := fs.Int("warmup", 12, "windows of baseline a device needs before it can be flagged (12 x 5m = 1h)")
	recur := fs.Int("recur", 3, "a level the device has reached this many times before is normal for it")
	k := fs.Float64("k", 3, "flag a feature above k times the device's baseline maximum")
	fs.Parse(args)
	if *dir == "" {
		return fmt.Errorf("usage: iotsim drift --summaries DIR [--control host:7070,...] [--events FILE]")
	}

	devs, nwin, first, last, err := loadSummaries(*dir)
	if err != nil {
		return err
	}
	if nwin == 0 {
		return fmt.Errorf("no summary files in %s", *dir)
	}

	names := map[string]string{} // OUI query -> simulated device name
	var events []*devent
	for _, c := range splitList(*control) {
		base := c
		if !strings.HasPrefix(base, "http") {
			base = "http://" + base
		}
		if body, err := get(ctx, base+"/"); err == nil {
			var ov struct {
				Devices []struct{ Name, MAC string } `json:"devices"`
			}
			if json.Unmarshal(body, &ov) == nil {
				for _, d := range ov.Devices {
					names[ouiKey(d.MAC)] = d.Name
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", c, err)
		}
		if body, err := get(ctx, base+"/events"); err == nil {
			events = append(events, parseEvents(body)...)
		} else {
			fmt.Fprintf(os.Stderr, "warning: %s/events: %v (redeploy the Pi to get this endpoint)\n", c, err)
		}
	}
	for _, f := range splitList(*eventsFile) {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		events = append(events, parseEvents(b)...)
	}
	for _, e := range events {
		if e.key != "" && names[e.key] == "" {
			names[e.key] = e.Device
		}
	}
	for _, d := range devs {
		if n := names[d.key]; n != "" {
			d.label = n
		}
	}

	flags := detect(devs, *warmup, *k, *recur)
	score(flags, events, devs)

	// ---- report ----
	fmt.Printf("%d windows, %s to %s, %d devices, warm-up %d windows, k=%.1f, recur=%d\n\n",
		nwin, first.Local().Format("Jan 2 15:04"), last.Local().Format("Jan 2 15:04"), len(devs), *warmup, *k, *recur)

	fmt.Println("FLAGS (consecutive windows of one behavior are one episode)")
	if len(flags) == 0 {
		fmt.Println("  none")
	}
	for _, f := range flags {
		match := "no injection (unexplained)"
		if f.event != nil {
			match = "matches " + f.event.Scenario + " at " + f.event.Time.Local().Format("15:04:05")
		}
		dur := ""
		if f.windows > 1 {
			dur = fmt.Sprintf(" (%d windows, to %s, peak %s)", f.windows, f.last.end.Local().Format("15:04"), fmtVal(featureByName(f.feature), f.max))
		}
		fmt.Printf("  %s  %-16s %-10s %s%s   [%s]\n", f.w.start.Local().Format("Jan 2 15:04"), f.dev.label, f.feature, f.why, dur, match)
	}

	if len(events) > 0 {
		fmt.Println("\nINJECTED EVENTS")
		caught, warming := 0, 0
		for _, e := range events {
			res := "MISSED"
			if len(e.caught) > 0 {
				caught++
				var fe []string
				for _, f := range e.caught {
					fe = append(fe, f.feature)
				}
				res = "caught (" + strings.Join(uniq(fe), ", ") + ")"
			} else if !inRange(e.Time, first, last) {
				res = "outside the summaries' time range"
			} else if d := devByKey(devs, e.key); d != nil && inWarmup(d, e.Time, *warmup) {
				res = "not scored (device still warming up)"
				warming++
			}
			fmt.Printf("  %s  %-16s %-8s %s\n", e.Time.Local().Format("Jan 2 15:04:05"), e.Device, e.Scenario, res)
		}
		unexplained := 0
		for _, f := range flags {
			if f.event == nil {
				unexplained++
			}
		}
		scored := len(events) - warming
		fmt.Printf("\n%d of %d scored injections caught", caught, scored)
		if warming > 0 {
			fmt.Printf(" (%d more during warm-up, not scored)", warming)
		}
		fmt.Printf("; %d flag(s) with no injection behind them\n", unexplained)
	} else {
		fmt.Println("\n(no injection log: pass --control or --events to score the flags)")
	}
	unexplained, days := 0, deviceDays(devs, *warmup)
	for _, f := range flags {
		if f.event == nil {
			unexplained++
		}
	}
	if days > 0 {
		fmt.Printf("false positives: %d unexplained episode(s) over %.1f supervised device-days = %.2f per device-day\n",
			unexplained, days, float64(unexplained)/days)
	}
	return nil
}

func loadSummaries(dir string) (map[string]*ddevice, int, time.Time, time.Time, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	type part struct {
		Period struct {
			Start time.Time `json:"start"`
			End   time.Time `json:"end"`
		} `json:"period"`
		Devices []struct {
			Class struct {
				Type   string `json:"type"`
				Vendor string `json:"vendor"`
			} `json:"class"`
			Fingerprints struct {
				OUI string `json:"oui"`
			} `json:"fingerprints"`
			Destinations struct {
				Domains map[string]any `json:"domains"`
			} `json:"destinations"`
			Rhythm []struct {
				Dest    string  `json:"dest"`
				PeriodS float64 `json:"period_s"`
			} `json:"rhythm"`
			Envelope struct {
				Features map[string]struct {
					Max float64 `json:"max"`
				} `json:"features"`
			} `json:"envelope"`
			Totals struct {
				NewDestinations float64 `json:"new_destinations"`
			} `json:"totals"`
		} `json:"devices"`
	}
	devs := map[string]*ddevice{}
	periods := map[time.Time]bool{}
	var first, last time.Time
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, first, last, err
		}
		var p part
		if err := json.Unmarshal(b, &p); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", filepath.Base(f), err)
			continue
		}
		periods[p.Period.Start] = true
		if first.IsZero() || p.Period.Start.Before(first) {
			first = p.Period.Start
		}
		if p.Period.End.After(last) {
			last = p.Period.End
		}
		for _, d := range p.Devices {
			key := d.Fingerprints.OUI
			if key == "" {
				continue // randomized MAC: no stable key
			}
			dv := devs[key]
			if dv == nil {
				dv = &ddevice{key: key, label: strings.TrimSpace(d.Class.Vendor + " " + key)}
				devs[key] = dv
			}
			w := &dwindow{start: p.Period.Start, end: p.Period.End, vals: map[string]float64{}, class: d.Class.Type}
			for name, v := range d.Envelope.Features {
				w.vals[name] = v.Max
			}
			w.vals["new_dest"] = d.Totals.NewDestinations
			for dom := range d.Destinations.Domains {
				w.domains = append(w.domains, dom)
			}
			sort.Strings(w.domains)
			for _, r := range d.Rhythm {
				w.rhythms = append(w.rhythms, fmt.Sprintf("%s@%gs", r.Dest, r.PeriodS))
			}
			sort.Strings(w.rhythms)
			dv.windows = append(dv.windows, w)
		}
	}
	for _, d := range devs {
		sort.Slice(d.windows, func(i, j int) bool { return d.windows[i].start.Before(d.windows[j].start) })
	}
	return devs, len(periods), first, last, nil
}

// detect walks each device's windows in time order and compares each window
// with a reference built from all of that device's earlier windows: the
// larger of their 95th percentile and the recur-th largest value. Every window
// joins the baseline afterward, flagged or not, so a new behavior is learned
// once it recurs: the percentile alone never learns an event that happens in
// fewer than 5% of windows (a hub's two-hourly burst), but recur does after a
// few repeats. With a short history the percentile equals the maximum.
//
// Consecutive flagged windows of one feature at a similar level (within k of
// the episode's peak) are one episode, so a three-hour stream is one flag,
// not thirty-five. A much larger value starts a new episode, so an exfil
// right after a normal clip is still its own flag.
func detect(devs map[string]*ddevice, warmup int, k float64, recur int) []*dflag {
	var out []*dflag
	for _, d := range sortedDevs(devs) {
		var prior []*dwindow
		seen := map[string]bool{}
		var rhythmsSeen []string
		open := map[string]*dflag{} // feature -> episode flagged in the previous window
		for _, w := range d.windows {
			var fl []*dflag
			still := map[string]*dflag{}
			if len(prior) >= warmup {
				for _, f := range driftFeatures {
					x, ok := w.vals[f.name]
					if !ok {
						continue
					}
					vals := make([]float64, 0, len(prior))
					for _, c := range prior {
						vals = append(vals, c.vals[f.name])
					}
					ref := max(p95(vals), nthLargest(vals, recur))
					if !(x > ref*k && x-ref >= f.floor) {
						continue
					}
					if ep := open[f.name]; ep != nil && continues(ep, x, k) && !w.start.After(ep.last.end) {
						ep.last, ep.windows, ep.max = w, ep.windows+1, max(ep.max, x)
						still[f.name] = ep
						continue
					}
					nf := &dflag{dev: d, w: w, last: w, windows: 1, max: x, feature: f.name, value: x, base: ref,
						why: fmt.Sprintf("%s vs baseline %s", fmtVal(f, x), fmtVal(f, ref))}
					fl = append(fl, nf)
					still[f.name] = nf
				}
				var fresh []string
				for _, dom := range w.domains {
					if !seen[dom] {
						fresh = append(fresh, dom)
					}
				}
				if len(fresh) > 0 {
					fl = append(fl, &dflag{dev: d, w: w, last: w, windows: 1, feature: "new_domain",
						why: "first contact with " + strings.Join(fresh, ", ")})
				}
				var newRhythm []string
				for _, r := range w.rhythms {
					if !knownRhythm(rhythmsSeen, r) {
						newRhythm = append(newRhythm, r)
					}
				}
				if len(newRhythm) > 0 {
					fl = append(fl, &dflag{dev: d, w: w, last: w, windows: 1, feature: "new_rhythm",
						why: "new rigid contact rhythm " + strings.Join(newRhythm, ", ")})
				}
			}
			rhythmsSeen = append(rhythmsSeen, w.rhythms...)
			open = still
			for _, dom := range w.domains {
				seen[dom] = true
			}
			prior = append(prior, w)
			out = append(out, fl...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].w.start.Before(out[j].w.start) })
	return out
}

// knownRhythm reports whether the device has already kept rhythm r
// ("dest:port@30s"): the same destination and port at a period within 25%,
// so a heartbeat whose rounding wobbles between buckets is not new.
func knownRhythm(seen []string, r string) bool {
	dest, per, ok := splitRhythm(r)
	if !ok {
		return false
	}
	for _, s := range seen {
		d2, p2, ok := splitRhythm(s)
		if ok && d2 == dest && math.Abs(p2-per) <= 0.25*per {
			return true
		}
	}
	return false
}

func splitRhythm(r string) (string, float64, bool) {
	i := strings.LastIndex(r, "@")
	if i < 0 || !strings.HasSuffix(r, "s") {
		return "", 0, false
	}
	p, err := strconv.ParseFloat(r[i+1:len(r)-1], 64)
	if err != nil || p <= 0 {
		return "", 0, false
	}
	return r[:i], p, true
}

// continues reports whether x, flagged in the window right after an open
// episode, is the same behavior: within k of the episode's peak, or, right
// after the episode's first window, within k*k. A session that starts
// mid-window shows a partial first window (a 151 MB first window of a stream
// that then runs 541 MB per window), so the second window may jump more than
// k; a jump beyond k*k (a 3 MB clip, then a 61 MB exfil) is still new.
func continues(ep *dflag, x, k float64) bool {
	if x <= ep.max*k {
		return true
	}
	return ep.windows == 1 && x <= ep.max*k*k
}

// inWarmup reports whether t falls before the device could be flagged (an
// injection then is not a miss: nothing could have caught it).
func inWarmup(d *ddevice, t time.Time, warmup int) bool {
	end := warmupEnd(d, warmup)
	return end.IsZero() || t.Before(end)
}

// warmupEnd is when a device's warm-up ends: the start of its first
// window that can be flagged. Zero if it never got there.
func warmupEnd(d *ddevice, warmup int) time.Time {
	if len(d.windows) <= warmup {
		return time.Time{}
	}
	return d.windows[warmup].start
}

// nthLargest is the n-th largest value (0 when there are fewer than n).
func nthLargest(v []float64, n int) float64 {
	if n <= 0 || len(v) < n {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Sort(sort.Reverse(sort.Float64Slice(s)))
	return s[n-1]
}

// deviceDays is the supervised time: each device's windows after warm-up.
func deviceDays(devs map[string]*ddevice, warmup int) float64 {
	var secs float64
	for _, d := range devs {
		for i, w := range d.windows {
			if i >= warmup {
				secs += w.end.Sub(w.start).Seconds()
			}
		}
	}
	return secs / 86400
}

func featureByName(name string) feature {
	for _, f := range driftFeatures {
		if f.name == name {
			return f
		}
	}
	return feature{name: name}
}

// score links each flag to an injection on the same device whose time falls
// in the flagged episode or the window before it (effects can spill over a
// window boundary).
func score(flags []*dflag, events []*devent, devs map[string]*ddevice) {
	for _, f := range flags {
		span := f.w.end.Sub(f.w.start)
		for _, e := range events {
			if e.key != f.dev.key {
				continue
			}
			if !e.Time.Before(f.w.start.Add(-span)) && e.Time.Before(f.last.end) {
				f.event = e
				e.caught = append(e.caught, f)
				break
			}
		}
	}
}

func parseEvents(b []byte) []*devent {
	var out []*devent
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var e devent
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, e.TimeStr)
		if err != nil {
			continue
		}
		e.Time = t
		e.key = ouiKey(e.MAC)
		out = append(out, &e)
	}
	return out
}

// ouiKey turns a MAC into the 9-hex-digit query the sensor puts in
// summaries ("08:54:11:d7:f1:56" -> "085411D7F").
func ouiKey(mac string) string {
	h := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(mac))
	if len(h) < 9 {
		return ""
	}
	return h[:9]
}

// p95 is the nearest-rank 95th percentile (the maximum for fewer than 20
// values).
func p95(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	i := int(float64(len(s))*0.95+0.999999) - 1
	return s[min(max(i, 0), len(s)-1)]
}

func fmtVal(f feature, v float64) string {
	if f.isByte {
		return hbytes(uint64(v))
	}
	return fmt.Sprintf("%.0f", v)
}

func sortedDevs(m map[string]*ddevice) []*ddevice {
	var out []*ddevice
	for _, d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].label < out[j].label })
	return out
}

func devByKey(m map[string]*ddevice, key string) *ddevice { return m[key] }

func inRange(t, a, b time.Time) bool { return !t.Before(a) && t.Before(b) }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
