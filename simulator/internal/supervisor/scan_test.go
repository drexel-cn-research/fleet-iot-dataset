package supervisor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iotsim/internal/profile"
)

func TestScanTargetsPeerPi(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"pi":"pi5","devices":[{"name":"roku","ip":"192.168.50.66"},{"name":"tv","ip":"192.168.50.80"},{"name":"down","ip":"-"}]}`))
	}))
	defer peer.Close()

	s := &Supervisor{fleet: &profile.Fleet{Pi: "pi4", ScanPeers: []string{strings.TrimPrefix(peer.URL, "http://")}},
		log: func(string, ...any) {}}
	req := injectReq{Scenario: "scan"}
	s.scopeTargets(&req)
	if req.Arg != "192.168.50.66,192.168.50.80" {
		t.Fatalf("targets = %q, want the peer Pi's two leased devices", req.Arg)
	}

	// Other scenarios and explicit targets are left alone.
	ex := injectReq{Scenario: "scan", Arg: "10.0.0.9"}
	s.scopeTargets(&ex)
	if ex.Arg != "10.0.0.9" {
		t.Fatalf("explicit targets rewritten to %q", ex.Arg)
	}
	other := injectReq{Scenario: "exfil"}
	s.scopeTargets(&other)
	if other.Arg != "" {
		t.Fatalf("exfil got targets %q", other.Arg)
	}
}

func TestScanFallsBackToLocalWhenPeerDown(t *testing.T) {
	var logged []string
	s := &Supervisor{fleet: &profile.Fleet{Pi: "pi4", ScanPeers: []string{"127.0.0.1:1"}},
		slots: map[string]*slot{}, procs: map[string]*worker{},
		log: func(f string, a ...any) { logged = append(logged, f) }}
	req := injectReq{Scenario: "scan"}
	s.scopeTargets(&req)
	if req.Arg != "" { // no local devices in this test
		t.Fatalf("targets = %q", req.Arg)
	}
	if len(logged) < 2 {
		t.Fatalf("expected a peer error and a fallback warning, got %v", logged)
	}
}
