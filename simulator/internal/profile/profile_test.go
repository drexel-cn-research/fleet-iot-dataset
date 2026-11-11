package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinCatalogValid(t *testing.T) {
	c, err := LoadCatalog("")
	if err != nil {
		t.Fatalf("built-in catalog: %v", err)
	}
	if len(c) < 10 {
		t.Fatalf("expected a full catalog, got %d types", len(c))
	}
	for _, name := range c.Names() {
		p := c[name]
		if p.Class == "" || p.Vendor == "" || p.Model == "" {
			t.Errorf("%s: missing class/vendor/model", name)
		}
	}
}

func TestResolveDeterministicMAC(t *testing.T) {
	c, _ := LoadCatalog("")
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.json")
	os.WriteFile(fp, []byte(`{"pi":"pi4","sink":"10.0.0.2:9000","devices":[
		{"name":"hik-cam-1","type":"hikvision-cam"},
		{"name":"hik-cam-2","type":"hikvision-cam"}]}`), 0o644)
	f, err := LoadFleet(fp)
	if err != nil {
		t.Fatal(err)
	}
	a, err := f.Resolve(c)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.Resolve(c)
	if a[0].MAC.String() != b[0].MAC.String() {
		t.Errorf("MAC not stable across resolves")
	}
	if a[0].MAC.String() == a[1].MAC.String() {
		t.Errorf("two instances share a MAC: %s", a[0].MAC)
	}
	// MAC must carry the vendor OUI (085411 for Hikvision).
	if got := a[0].MAC.String()[:8]; got != "08:54:11" {
		t.Errorf("MAC OUI = %s, want 08:54:11", got)
	}
}

func TestExpandTemplates(t *testing.T) {
	c, _ := LoadCatalog("")
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.json")
	os.WriteFile(fp, []byte(`{"pi":"pi5","sink":"10.0.0.2:9000","devices":[{"name":"hue-bridge","type":"hue-bridge"}]}`), 0o644)
	f, _ := LoadFleet(fp)
	d, _ := f.Resolve(c)
	got := d[0].Expand("id={mac6l} serial={serial} u={uuid}")
	if len(got) < 20 || got == "id={mac6l} serial={serial} u={uuid}" {
		t.Errorf("expand did not substitute: %q", got)
	}
}

func TestInHours(t *testing.T) {
	cases := []struct {
		win string
		h   int
		in  bool
	}{
		{"", 3, true},
		{"7-23", 8, true},
		{"7-23", 23, false},
		{"7-23", 2, false},
		{"22-6", 23, true},
		{"22-6", 3, true},
		{"22-6", 12, false},
	}
	for _, c := range cases {
		if got := InHours(c.win, c.h); got != c.in {
			t.Errorf("InHours(%q,%d)=%v want %v", c.win, c.h, got, c.in)
		}
	}
}

func TestUnknownTypeRejected(t *testing.T) {
	c, _ := LoadCatalog("")
	dir := t.TempDir()
	fp := filepath.Join(dir, "f.json")
	os.WriteFile(fp, []byte(`{"pi":"x","sink":"10.0.0.2:9000","devices":[{"name":"d","type":"nope"}]}`), 0o644)
	f, _ := LoadFleet(fp)
	if _, err := f.Resolve(c); err == nil {
		t.Fatal("expected error for unknown type")
	}
}

func TestHoursSpan(t *testing.T) {
	for w, want := range map[string]int{"18-23": 5, "19-24": 5, "8-22": 14, "22-6": 8, "": 24, "junk": 24} {
		if got := HoursSpan(w); got != want {
			t.Errorf("HoursSpan(%q) = %d, want %d", w, got, want)
		}
	}
}
