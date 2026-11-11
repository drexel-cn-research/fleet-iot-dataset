// Package supervisor builds the per-device namespaces and macvlan interfaces,
// launches one worker process per device inside its namespace (via
// "ip netns exec", which reliably places all of a process's threads in the
// namespace), restarts workers that exit, and exposes a control HTTP API.
package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"iotsim/internal/device"
	"iotsim/internal/profile"
)

const RunDir = "/run/iotsim"

type Supervisor struct {
	fleet    *profile.Fleet
	fleetArg string // path passed to workers
	devices  []*profile.Device
	self     string // path to this binary

	// Profiles is the extra catalog file, passed through to workers so they
	// resolve the same device types as the supervisor.
	Profiles string

	workers sync.WaitGroup // one per spawn loop

	mu    sync.Mutex
	slots map[string]*slot
	procs map[string]*worker
	log   func(string, ...any)
}

type worker struct {
	name string
	cmd  *exec.Cmd
	sock string
}

func New(fleetPath string, cat profile.Catalog, logf func(string, ...any)) (*Supervisor, error) {
	f, err := profile.LoadFleet(fleetPath)
	if err != nil {
		return nil, err
	}
	devs, err := f.Resolve(cat)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(RunDir, 0o755); err != nil {
		return nil, err
	}
	return &Supervisor{
		fleet: f, fleetArg: fleetPath, devices: devs, self: self,
		slots: map[string]*slot{}, procs: map[string]*worker{}, log: logf,
	}, nil
}

func (s *Supervisor) Fleet() *profile.Fleet { return s.fleet }

// Start builds every device's namespace and launches its worker.
func (s *Supervisor) Start(ctx context.Context) error {
	if launcher("x") == nil {
		return fmt.Errorf("need `ip` (iproute2) or `nsenter` (util-linux) to start workers in their namespaces")
	}
	if _, err := net.InterfaceByName(s.fleet.Parent); err != nil {
		return fmt.Errorf("parent interface %q not found: %w", s.fleet.Parent, err)
	}
	for _, d := range s.devices {
		if err := s.startDevice(ctx, d); err != nil {
			s.log("device %s: %v", d.Name, err)
		}
	}
	return nil
}

func (s *Supervisor) startDevice(ctx context.Context, d *profile.Device) error {
	sl, err := createSlot(s.fleet.Parent, d)
	if err != nil {
		return fmt.Errorf("namespace: %w", err)
	}
	s.mu.Lock()
	s.slots[d.Name] = sl
	s.mu.Unlock()
	s.spawn(ctx, d, sl)
	return nil
}

// spawn launches the worker and keeps it alive until ctx ends.
func (s *Supervisor) spawn(ctx context.Context, d *profile.Device, sl *slot) {
	sock := filepath.Join(RunDir, d.Name+".sock")
	os.Remove(sock)
	w := &worker{name: d.Name, sock: sock}
	s.mu.Lock()
	s.procs[d.Name] = w
	s.mu.Unlock()

	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		backoff := time.Second
		for ctx.Err() == nil {
			args := append(launcher(sl.nsName), s.self, "worker",
				"--fleet", s.fleetArg, "--device", d.Name, "--sock", sock)
			if s.Profiles != "" {
				args = append(args, "--profiles", s.Profiles)
			}
			cmd := exec.CommandContext(ctx, args[0], args[1:]...)
			// On shutdown send SIGTERM (CommandContext's default is SIGKILL),
			// so the worker releases its DHCP lease and removes its socket.
			// Both launchers exec the worker, so the signal reaches it.
			cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
			cmd.WaitDelay = 5 * time.Second
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			w.cmd = cmd
			start := time.Now()
			err := cmd.Run()
			if ctx.Err() != nil {
				return
			}
			s.log("worker %s exited after %s: %v", d.Name, time.Since(start).Round(time.Second), err)
			if time.Since(start) > time.Minute {
				backoff = time.Second
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
		}
	}()
}

// launcher returns the command prefix that runs a program inside the named
// namespace, or nil if neither tool is installed. Both setns then exec, so
// every thread of the worker is in the namespace.
func launcher(ns string) []string {
	if _, err := exec.LookPath("ip"); err == nil {
		return []string{"ip", "netns", "exec", ns}
	}
	if _, err := exec.LookPath("nsenter"); err == nil {
		return []string{"nsenter", "--net=/run/netns/" + ns}
	}
	return nil
}

// Stop tears down all workers and namespaces.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	slots := s.slots
	s.slots = map[string]*slot{}
	s.mu.Unlock()
	// Wait for workers to exit (they get SIGTERM when ctx ends and release
	// their leases) before tearing down the namespaces under them.
	done := make(chan struct{})
	go func() { s.workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		s.log("some workers did not exit in time; tearing down anyway")
	}
	for _, sl := range slots {
		sl.destroy()
		os.Remove(filepath.Join(RunDir, sl.name+".sock"))
	}
}

// ---- control API ----

func (s *Supervisor) worker(name string) *worker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs[name]
}

func (s *Supervisor) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n []string
	for k := range s.procs {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// ask sends a control request to one worker over its unix socket.
func (w *worker) ask(method, path string, body []byte) ([]byte, error) {
	cl := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", w.sock)
			},
		},
	}
	req, _ := http.NewRequest(method, "http://unix"+path, bytes.NewReader(body))
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("worker %s: %s: %s", w.name, resp.Status, out)
	}
	return out, nil
}

// Handler is the supervisor's control API:
//
//	GET  /                      fleet overview + every device status
//	GET  /devices/{name}        one device status
//	POST /devices/{name}/inject {"scenario":"scan","arg":""}
//	POST /inject                broadcast to all: {"scenario":"beacon"}
func (s *Supervisor) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		type overview struct {
			Pi        string           `json:"pi"`
			Parent    string           `json:"parent"`
			Sink      string           `json:"sink"`
			Scenarios []string         `json:"scenarios"`
			Devices   []device.Status  `json:"devices"`
		}
		ov := overview{Pi: s.fleet.Pi, Parent: s.fleet.Parent, Sink: s.fleet.Sink, Scenarios: device.Scenarios}
		for _, name := range s.names() {
			if st, err := s.status(name); err == nil {
				ov.Devices = append(ov.Devices, *st)
			} else {
				ov.Devices = append(ov.Devices, device.Status{Name: name, State: "down"})
			}
		}
		writeJSON(w, ov)
	})

	// The ground-truth log of injections (JSON lines), for scoring detection
	// from another machine (iotsim drift).
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		b, err := os.ReadFile(EventsFile)
		if err != nil && !os.IsNotExist(err) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write(b)
	})

	mux.HandleFunc("GET /devices/{name}", func(w http.ResponseWriter, r *http.Request) {
		st, err := s.status(r.PathValue("name"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, st)
	})

	mux.HandleFunc("POST /devices/{name}/inject", func(w http.ResponseWriter, r *http.Request) {
		s.inject(w, r, r.PathValue("name"))
	})

	mux.HandleFunc("POST /inject", func(w http.ResponseWriter, r *http.Request) {
		var req injectReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.scopeTargets(&req)
		body, _ := json.Marshal(req)
		var errs []string
		for _, name := range s.names() {
			if wk := s.worker(name); wk != nil {
				if _, err := wk.ask("POST", "/inject", body); err != nil {
					errs = append(errs, err.Error())
				} else {
					s.record(name, req)
				}
			}
		}
		writeJSON(w, map[string]any{"injected": req.Scenario, "errors": errs})
	})

	return mux
}

type injectReq struct {
	Scenario string `json:"scenario"`
	Arg      string `json:"arg,omitempty"`
}

func (s *Supervisor) inject(w http.ResponseWriter, r *http.Request, name string) {
	wk := s.worker(name)
	if wk == nil {
		http.Error(w, "no such device", http.StatusNotFound)
		return
	}
	var req injectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.scopeTargets(&req)
	body, _ := json.Marshal(req)
	out, err := wk.ask("POST", "/inject", body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.record(name, req)
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// EventsFile is the ground-truth log of injected behaviors, one JSON object
// per line, for scoring drift detection against what actually happened.
var EventsFile = "/var/lib/iotsim/events.jsonl"

// record appends one ground-truth event. Failures are logged, not fatal.
func (s *Supervisor) record(name string, req injectReq) {
	ev := map[string]string{
		"time":     time.Now().UTC().Format(time.RFC3339),
		"pi":       s.fleet.Pi,
		"device":   name,
		"scenario": req.Scenario,
	}
	for _, d := range s.devices {
		if d.Name == name {
			ev["mac"] = d.MAC.String()
			ev["type"] = d.Profile.Type
		}
	}
	if req.Arg != "" {
		ev["arg"] = req.Arg
	}
	line, _ := json.Marshal(ev)
	os.MkdirAll(filepath.Dir(EventsFile), 0o755)
	f, err := os.OpenFile(EventsFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.log("events: %v", err)
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// scopeTargets keeps the LAN-peer scenario inside the testbed: unless the
// caller named targets, they are the simulated devices on this Pi.
func (s *Supervisor) scopeTargets(req *injectReq) {
	if req.Scenario != "scan" || req.Arg != "" {
		return
	}
	// Prefer another Pi's devices: that traffic crosses the router, while
	// traffic between devices on this Pi never leaves it (macvlan bridges it
	// inside the NIC).
	var ips []string
	for _, peer := range s.fleet.ScanPeers {
		got, err := peerDeviceIPs(peer)
		if err != nil {
			s.log("scan: peer %s: %v", peer, err)
			continue
		}
		ips = append(ips, got...)
	}
	if len(ips) == 0 {
		if len(s.fleet.ScanPeers) > 0 {
			s.log("scan: no peer devices reachable; scanning this Pi's devices (the router will not see it)")
		}
		for _, name := range s.names() {
			if st, err := s.status(name); err == nil && st.IP != "-" && st.IP != "" {
				ips = append(ips, st.IP)
			}
		}
	}
	req.Arg = strings.Join(ips, ",")
}

// peerDeviceIPs asks another Pi's control API for its simulated devices'
// addresses. Only those are ever scanned: never arbitrary LAN hosts.
func peerDeviceIPs(peer string) ([]string, error) {
	if !strings.Contains(peer, "://") {
		peer = "http://" + peer
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(strings.TrimSuffix(peer, "/") + "/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	var ov struct {
		Devices []struct {
			IP string `json:"ip"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&ov); err != nil {
		return nil, err
	}
	var ips []string
	for _, d := range ov.Devices {
		if ip := net.ParseIP(d.IP); ip != nil {
			ips = append(ips, ip.String())
		}
	}
	return ips, nil
}

func (s *Supervisor) status(name string) (*device.Status, error) {
	wk := s.worker(name)
	if wk == nil {
		return nil, fmt.Errorf("no such device")
	}
	out, err := wk.ask("GET", "/status", nil)
	if err != nil {
		return nil, err
	}
	var st device.Status
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}
