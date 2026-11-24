// Package hostload reads how loaded the user's own computer is, on the side
// of AgentBox's VM that the user works on, and tells the daemon in the VM.
//
// Each front end reads the host every Interval and puts it to the daemon
// (PUT /v1/host/load). Nothing waits or runs fewer jobs for it (24 Nov
// 2026, the user's decision): until then a host past any threshold was
// "busy", which halved every build's workers, held the VM's growth for half
// a minute after, and showed "Computer busy" in the app, and a host sitting
// at a reserve of a quarter of its memory turned busy every minute, held
// at 7 GiB of its VM. What a report still does:
//
//   - Linux: PSI, the share of the last 10 s some task stalled on memory, the
//     CPU or the disk (/proc/pressure), and MemAvailable against Reserve.
//     Cloud Hypervisor's supervisor reports it, and adds what the VM may
//     grow to now (VMCeiling) and whether the host is below its hard floor,
//     where no machine boots (HoldBoots, chv/memory.go). Its own memory loop
//     sizes the VM.
//   - Mac: the kernel's memory pressure level (kern.memorystatus_vm_pressure_level,
//     what Activity Monitor's graph shows), warn or critical. For Lima's VM
//     `agentbox vm lan`, which runs beside it as long as it does, reports
//     it; for vz's, its supervisor.
//   - Windows: GlobalMemoryStatusEx's available memory against Reserve, and
//     the CPU's busy share from GetSystemTimes; the relay
//     (`agentbox.exe relay`, while the app is open) reports it.
//
// Where the front end doesn't size the VM itself (Lima, WSL), a host short
// of memory has the daemon drop the VM's caches (Reclaim), which free page
// reporting or WSL's autoMemoryReclaim gives back. A front end that stops
// reporting stops all of it: the daemon forgets a report after
// api.HostLoadTTL.
package hostload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"agentbox/internal/api"
)

const (
	// Interval is how often a front end reads and reports.
	Interval = 2 * time.Second
	// MemoryBusy, CPUBusy and IOBusy are the PSI "some avg10" percentages
	// past which the host is over a threshold (Why). Memory as chv's
	// memHostPressure: any real stall on memory means the desktop is
	// waiting on the disk. CPU and IO only when they are high: some tasks
	// waiting for a CPU or the disk is normal on a desktop, a third or half
	// of the time isn't.
	MemoryBusy = 10.0
	CPUBusy    = 50.0
	IOBusy     = 40.0
	// CPUBusyShare is, where there's no PSI (Windows), the share of all
	// cores busy past which the host is over a threshold.
	CPUBusyShare = 90.0
	// ReserveMin is the least a host keeps for itself, its desktop and the
	// app: what a desktop session with a browser and the app needs to stay
	// responsive.
	ReserveMin = int64(4) << 30
	// FloorMin and FloorShare (of its memory) make Floor.
	FloorMin   = int64(2) << 30
	FloorShare = 0.06
)

// Floor is what a host with total bytes of memory can't go under without
// freezing: the larger of FloorMin and FloorShare of it. Below it no agent's
// machine boots and agents are stopped (chv's Shed).
func Floor(total int64) int64 {
	return max(FloorMin, int64(float64(total)*FloorShare))
}

// Reserve is what a host with total bytes of memory keeps for itself, its
// desktop and the app, which the VM's cap leaves it (chv.MaxCap): ReserveMin,
// or the host's Floor where that is more (past 66 GiB), so the cap never
// takes the host under it. It was a quarter of the host (7.6 GiB of 30.5),
// which held back a VM whose host had 7.5 GiB available and its desktop
// calm. What keeps the desktop from stalling moment to moment is the VM's
// memory loop (chv.HostKeep, the host's own memory pressure, the floor), not
// a share fixed in advance.
func Reserve(total int64) int64 {
	return max(ReserveMin, Floor(total))
}

// Reading is one look at the host, before it's judged.
type Reading struct {
	OS string
	// Memory, CPU and IO are PSI some avg10, in percent; -1 when the host
	// has no such figure.
	Memory, CPU, IO float64
	// CPUShare is the share of all cores busy, in percent, where there's
	// no CPU PSI (Windows); -1 otherwise.
	CPUShare float64
	// Level is the Mac's memory pressure level: "normal", "warn" or
	// "critical"; "" elsewhere.
	Level                  string
	MemoryTotal, Available int64
}

// over says why r is past a threshold: "memory", "cpu" or "io", or "".
func (r Reading) over() string {
	switch {
	case r.Memory >= MemoryBusy, r.Level == "warn", r.Level == "critical",
		r.MemoryTotal > 0 && r.Available > 0 && r.Available < Reserve(r.MemoryTotal):
		return "memory"
	case r.CPU >= CPUBusy, r.CPUShare >= CPUBusyShare:
		return "cpu"
	case r.IO >= IOBusy:
		return "io"
	}
	return ""
}

// Judge makes r's report at now: what is over its threshold, and, for
// memory, that the VM's caches should go back to the host.
func Judge(r Reading, now time.Time) api.HostLoad {
	why := r.over()
	return api.HostLoad{
		OS:     r.OS,
		Memory: r.Memory, CPU: r.CPU, IO: r.IO, CPUShare: r.CPUShare,
		Level:       r.Level,
		MemoryTotal: r.MemoryTotal, Available: r.Available,
		Why:     why,
		Reclaim: why == "memory",
		At:      now.UTC(),
	}
}

// Put sends one report to the daemon; a Reporter's way of reaching it.
type Put func(ctx context.Context, load api.HostLoad) error

// Report reads the host every Interval and puts each report with put, until
// ctx ends. reclaim says whether the daemon should drop the VM's caches when
// the host is short of memory: false where the front end sizes the VM's
// memory itself (Cloud Hypervisor).
func Report(ctx context.Context, put Put, reclaim bool) {
	r := newReader()
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		reading, err := r.read()
		if err == nil {
			load := Judge(reading, time.Now())
			load.Reclaim = load.Reclaim && reclaim
			pctx, cancel := context.WithTimeout(ctx, Interval)
			_ = put(pctx, load) // a daemon that's down or busy hears the next one
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Read is one reading of this host, for `agentbox vm watch-host --once` and tests.
func Read() (Reading, error) { return newReader().read() }

// HTTPPut is a Put that reaches the daemon through dial: its unix socket, a
// vsock, or a relay's stream.
func HTTPPut(dial func(ctx context.Context) (net.Conn, error)) Put {
	client := &http.Client{
		Transport: &http.Transport{
			DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
			DisableKeepAlives: true,
		},
	}
	return func(ctx context.Context, load api.HostLoad) error {
		b, err := json.Marshal(load)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://agentbox/v1/host/load", bytes.NewReader(b))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return fmt.Errorf("PUT /v1/host/load: %s", resp.Status)
		}
		return nil
	}
}
