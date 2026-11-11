package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mkdev(key string, tx []float64, domains [][]string) *ddevice {
	d := &ddevice{key: key, label: key}
	t0 := time.Date(2026, 10, 17, 17, 0, 0, 0, time.UTC)
	for i, v := range tx {
		w := &dwindow{start: t0.Add(time.Duration(i) * 5 * time.Minute), vals: map[string]float64{"tx_bytes": v}}
		w.end = w.start.Add(5 * time.Minute)
		if domains != nil {
			w.domains = domains[i]
		}
		d.windows = append(d.windows, w)
	}
	return d
}

func TestDriftFlagsSpikeOnceAndScores(t *testing.T) {
	mb := float64(1 << 20)
	// 4 quiet windows, a normal clip (new but learned), a repeat clip
	// (not flagged again), then an exfil-sized spike.
	cam := mkdev("085411D7F", []float64{4e3, 5e3, 4e3, 6e3, 3 * mb, 3 * mb, 61 * mb}, nil)
	flags := detect(map[string]*ddevice{cam.key: cam}, 4, 3, 3)
	var got []string
	for _, f := range flags {
		got = append(got, f.w.start.Format("15:04")+" "+f.feature)
	}
	want := []string{"17:20 tx_bytes", "17:30 tx_bytes"} // 17:25 continues the 17:20 episode
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("flags = %v, want %v (the repeat clip at 17:25 must not flag)", got, want)
	}

	ev := &devent{Time: cam.windows[6].start.Add(time.Minute), key: cam.key, Scenario: "exfil"}
	score(flags, []*devent{ev}, map[string]*ddevice{cam.key: cam})
	if len(ev.caught) != 1 || ev.caught[0].w != cam.windows[6] {
		t.Fatalf("exfil not matched to its window")
	}
	if flags[0].event != nil {
		t.Errorf("the normal clip at 17:20 should be unexplained, got matched")
	}
}

func TestDriftNewDomain(t *testing.T) {
	d := mkdev("74C2464DF", []float64{1, 1, 1, 1},
		[][]string{{"amazon.com"}, {"amazon.com"}, {"amazon.com"}, {"amazon.com", "example.com"}})
	flags := detect(map[string]*ddevice{d.key: d}, 3, 3, 3)
	if len(flags) != 1 || flags[0].feature != "new_domain" {
		t.Fatalf("want one new_domain flag, got %d", len(flags))
	}
}

func TestOUIKey(t *testing.T) {
	if k := ouiKey("08:54:11:d7:f1:56"); k != "085411D7F" {
		t.Fatalf("ouiKey = %q", k)
	}
}

func TestDriftLongEpisodeIsOneFlag(t *testing.T) {
	mb := float64(1 << 20)
	// Ten hours idle (so the 95th percentile is idle), then a 2-hour stream:
	// one flag, not 24.
	var tx []float64
	for i := 0; i < 120; i++ {
		tx = append(tx, 10e3)
	}
	for i := 0; i < 24; i++ {
		tx = append(tx, 500*mb)
	}
	d := mkdev("201742942", tx, nil)
	flags := detect(map[string]*ddevice{d.key: d}, 12, 3, 3)
	if len(flags) != 1 {
		t.Fatalf("got %d flags, want 1", len(flags))
	}
}

func TestDriftLearnsRarePeriodicBurst(t *testing.T) {
	// A burst every 24 windows (2h of 5m windows): ~4% of windows, so the
	// 95th percentile never absorbs it. It should flag at most until it has
	// recurred 3 times, then be normal.
	var tx []float64
	for i := 0; i < 24*8; i++ {
		if i%24 == 23 {
			tx = append(tx, 30*float64(1<<20))
		} else {
			tx = append(tx, 10e3)
		}
	}
	d := mkdev("001788350", tx, nil)
	flags := detect(map[string]*ddevice{d.key: d}, 12, 3, 3)
	if len(flags) > 3 {
		t.Fatalf("periodic burst flagged %d times, want at most 3", len(flags))
	}
	for _, f := range flags {
		if f.w.start.After(d.windows[24*3].start) {
			t.Fatalf("still flagging after 3 recurrences: %s", f.w.start.Format("15:04"))
		}
	}
}

func TestDriftNewRhythm(t *testing.T) {
	// A Roku with a normal 5-minute heartbeat whose rounding wobbles between
	// buckets, then a 30 s beacon to the sink.
	d := mkdev("B0A737D62", make([]float64, 20), nil)
	for i, w := range d.windows {
		switch {
		case i == 18:
			w.rhythms = []string{"lan:9000@30s", "roku.com:443@330s"}
		case i%2 == 0:
			w.rhythms = []string{"roku.com:443@300s"}
		default:
			w.rhythms = []string{"roku.com:443@330s"}
		}
	}
	flags := detect(map[string]*ddevice{d.key: d}, 12, 3, 3)
	if len(flags) != 1 || flags[0].feature != "new_rhythm" || flags[0].w != d.windows[18] {
		t.Fatalf("want one new_rhythm flag at window 18, got %d", len(flags))
	}
	if !strings.Contains(flags[0].why, "lan:9000@30s") || strings.Contains(flags[0].why, "roku.com") {
		t.Fatalf("flag text %q", flags[0].why)
	}
}

func TestLoadSummariesReadsRhythmAndConns(t *testing.T) {
	dir := t.TempDir()
	doc := `{"period":{"start":"2026-10-20T10:15:00Z","end":"2026-10-20T10:20:00Z"},"devices":[{"class":{"type":"streaming_device","vendor":"Roku"},"fingerprints":{"oui":"B0A737D62"},"destinations":{"domains":{"roku.com":1}},"rhythm":[{"dest":"lan:9000","period_s":30,"windows":1}],"envelope":{"features":{"conns":{"max":11}}},"totals":{"new_destinations":0}}]}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	devs, n, _, _, err := loadSummaries(dir)
	if err != nil || n != 1 {
		t.Fatalf("load: %v, %d periods", err, n)
	}
	w := devs["B0A737D62"].windows[0]
	if len(w.rhythms) != 1 || w.rhythms[0] != "lan:9000@30s" || w.vals["conns"] != 11 {
		t.Fatalf("rhythms %v conns %v", w.rhythms, w.vals["conns"])
	}
}

func TestDriftStreamWithPartialFirstWindowIsOneEpisode(t *testing.T) {
	mb := float64(1 << 20)
	var tx []float64
	for i := 0; i < 120; i++ {
		tx = append(tx, 20e3)
	}
	tx = append(tx, 151*mb, 541*mb, 542*mb, 540*mb) // a stream starting mid-window
	d := mkdev("201742942", tx, nil)
	flags := detect(map[string]*ddevice{d.key: d}, 12, 3, 3)
	if len(flags) != 1 {
		t.Fatalf("got %d flags, want 1 (partial first window joins the stream)", len(flags))
	}
}

func TestInWarmup(t *testing.T) {
	d := mkdev("085411D7F", make([]float64, 20), nil)
	if !inWarmup(d, d.windows[5].start, 12) || inWarmup(d, d.windows[12].start, 12) {
		t.Fatal("warm-up boundary wrong")
	}
	short := mkdev("085411D7E", make([]float64, 5), nil)
	if !inWarmup(short, short.windows[4].start.Add(time.Hour), 12) {
		t.Fatal("a device that never finished warm-up should count as warming up")
	}
}
