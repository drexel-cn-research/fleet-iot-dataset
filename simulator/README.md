# iotsim: IoT Device Simulator

`iotsim` is the simulation engine used in the paper's testbed. One Linux host (a Raspberry Pi in our experiments), wired into a router port, presents itself as about ten independent IoT devices. Each device has its own MAC address carrying a real vendor OUI, its own DHCP identity, its own mDNS, SSDP and WS-Discovery announcements, and its own cloud contact pattern. The router therefore sees ordinary devices on the network, not replayed packets.

Bulk traffic (camera clips, video streams) goes to a **sink** you run on another host. Small cloud check-ins go to the real vendor domains, so destinations and autonomous systems look realistic to a network sensor.

## How it works

One wired port carries many devices by giving each device its own **macvlan** interface (a distinct MAC that the parent NIC puts on the wire) inside its own **network namespace**, so each device can run DHCP, bind its own ports and announce itself without colliding with the others. From the router's side these are separate hosts.

```
  sink host (sink + ctl) ──────────────┐
                                       │
   Pi 4  eth0 ──┐                 ┌────┴── router port, home B
     ns:wyze-cam-1  (2C:AA:8E..)  │
     ns:hik-cam-1   (08:54:11..)  │   the router sees ~10 hosts per port
     ns:ecobee      (44:61:32..)  │
     ...          ──┘             │
   Pi 5  eth0 ────────────────────┴── router port, home A
```

The engine is one static Go binary with these modes:

| Command | Where | What |
|---|---|---|
| `iotsim run <fleet.json>` | simulation host (root) | build namespaces and interfaces, run the fleet, serve the control API |
| `iotsim ctl ...` | anywhere | query status, inject scenarios |
| `iotsim sink ...` | sink host | accept uploads, serve streams, log per-device bytes |
| `iotsim profiles` | anywhere | list the device catalog; `--check-dns` checks that every cloud name still resolves |
| `iotsim drift ...` | anywhere | the local detector used in the paper (below) |

`worker` is used internally by `run`; don't call it.

## Build and test

Requires Go 1.24 or later.

```
go build ./cmd/iotsim                                  # this machine
GOOS=linux GOARCH=arm64 go build ./cmd/iotsim          # a 64-bit Raspberry Pi
go test ./...                                          # Linux, no privileges needed
sudo go test ./internal/...                            # Linux, also namespace, macvlan and DHCP paths
```

The device and supervisor packages use Linux networking (netlink, raw sockets, namespaces), so `go test ./...` requires Linux. On macOS, build the binary and test the portable packages:

```
go build ./cmd/iotsim
go test ./cmd/... ./internal/dhcp/... ./internal/disc/... ./internal/profile/...
```

The root-only tests build a veth pair and a throwaway namespace, so they need `CAP_NET_ADMIN`. They skip when not run as root. `run` works only on Linux; on other systems the binary supports `sink`, `ctl`, `profiles` and `drift`.

## Running a home

1. **Prepare the simulation host.** Use a 64-bit Linux system. Keep the host's own traffic off the router under test (manage it over a different interface), and give the wired interface no address of its own so the host does not appear as a device:
   ```
   sudo ip addr flush dev eth0 && sudo ip link set eth0 up
   ```
2. **Start the sink** on another host reachable from the router:
   ```
   ./iotsim sink --listen :9000 --log sink.log
   ```
3. **Write a fleet file** listing the devices for this home and the sink's address. The fleets used in the paper are `../data/homes/pi4.json` (home B) and `../data/homes/pi5.json` (home A). Set `sink` to your sink's address. `speed` multiplies upload and stream rates. `scan_peers` names the other home's control API, so a `scan` targets the other home's simulated devices and crosses the router.
4. **Run the fleet** as root:
   ```
   sudo ./iotsim run fleet.json --control :7070
   ./iotsim ctl --control <host>:7070 status
   ```

Each device obtains a DHCP lease from the router, as a real device would.

## Injecting behavioral changes

```
./iotsim ctl --control <host>:7070 inject exfil hik-cam-1   # one oversized upload to the sink
./iotsim ctl --control <host>:7070 inject newdest echo-dot  # handshakes to never-seen destinations
./iotsim ctl --control <host>:7070 inject scan wyze-cam-1   # probes the other home's simulated devices
./iotsim ctl --control <host>:7070 inject beacon roku       # fixed-interval contacts to the sink
```

These are the four scenarios in the paper's Table 4. All of them stay inside the testbed. `scan` only ever touches simulated devices: with `scan_peers` set it targets the other home's devices, and otherwise it falls back to the host's own devices (which the router will not see, because devices on one host share a NIC). `newdest` contacts IANA's reserved documentation domains (example.com, example.net, example.org) and the IANA homepage. Exfiltration and beacons go only to the sink.

Every injection is logged as ground truth, one JSON line per device, in `/var/lib/iotsim/events.jsonl` (time, device, MAC, profile, scenario). The `injections-*.jsonl` files in `../data/runs/` are these logs.

## The local detector (`iotsim drift`)

`iotsim drift` is the transparent local detector the paper evaluates (Section 4.4). It reads a folder of the sensor's summaries, builds each device's baseline from that device's own earlier windows, and flags a window when a numeric feature exceeds three times the baseline, when the device contacts a domain it never used before, or when it starts a rigid contact rhythm it never kept before. The baseline is the larger of the earlier windows' 95th percentile and the third-largest earlier value (`--recur`), so behavior the device has already shown three times counts as normal. A device needs 12 windows (one hour) before it can be flagged (`--warmup`). Consecutive flagged windows of one behavior are one episode, and the report scores episodes against the injection log.

It reproduces the online detector logs in this repository. For example:

```
TZ=America/New_York ./iotsim drift --summaries ../data/runs/N2/summaries \
  --events ../data/runs/N2/injections-pi4.jsonl,../data/runs/N2/injections-pi5.jsonl
```

This gives the same flags and the same false-alarm rate as `../data/runs/N2/detector-online.txt`. The only difference is that devices without an injection are labeled by vendor and key, because the online run fetched device names from the live control API. The offline replay in `../code/replay/` implements the same decisions in Python and adds the fleet references.

## Device catalog and fidelity

Fifteen profiles ship built in (`internal/profile/catalog.json`, the paper's Table 3): cameras, a doorbell, plugs, a hub, speakers, a streamer, a TV, thermostats and a printer. Each sets a real IEEE OUI for its vendor, the hostname, DHCP options and discovery records that device sends, and its cloud endpoints. Add or override profiles with an extra JSON file of the same shape: `iotsim run fleet.json --profiles extra.json`.

- **Reproduced:** MAC and OUI, DHCP fingerprint, mDNS, SSDP and WS-Discovery, cloud destinations and autonomous systems, traffic shape (idle versus active periods, upload cadence, stream bitrate).
- **Approximated:** TLS JA4. Each ClientHello is shaped per device family (embedded, mbedTLS, modern) so devices do not share one fingerprint, but it will not match a specific firmware exactly (`internal/device/hello.go`).
- **Not reproduced:** application-layer cloud protocols. Cloud contacts are real DNS, TCP and TLS handshakes to the right names followed by small keepalives, without a vendor API conversation.

Profiles follow public captures and vendor documentation. They are approximations, not packet captures of the corresponding devices.

## Layout

```
cmd/iotsim/           run, worker, ctl, sink, profiles, drift
internal/profile/     catalog and fleet types, catalog.json
internal/dhcp/        raw-socket DHCPv4 client (full option control)
internal/disc/        mDNS, SSDP, WS-Discovery
internal/device/      one device: lease, discovery, cloud, traffic, scenarios, control socket
internal/supervisor/  namespaces and macvlan, worker processes, control API
```
