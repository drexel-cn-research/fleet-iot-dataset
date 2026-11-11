package device

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
)

// ServeControl runs the worker's control socket: a unix-domain HTTP server
// the supervisor talks to.
//
//	GET  /status           this device's Status
//	POST /inject           {"scenario":"scan","arg":""}
func (d *Device) ServeControl(ctx context.Context, sock string) error {
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	os.Chmod(sock, 0o660)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		st := d.Status()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	})
	mux.HandleFunc("POST /inject", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Scenario string `json:"scenario"`
			Arg      string `json:"arg"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := d.Inject(req.Scenario, req.Arg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"injected": req.Scenario, "device": d.Name})
	})

	srv := &http.Server{Handler: mux}
	go func() {
		<-ctx.Done()
		srv.Close()
		os.Remove(sock)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
