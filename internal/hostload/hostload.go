// Package hostload reads how loaded the user's own computer is, on the side
// of AgentBox's VM that the user works on, and tells the daemon in the VM.
//
// A share of the host kept free (chv.HostReserve, the VM's cap) isn't enough
// on its own: what the user's own programs need varies, and a desktop stalls
// on a disk or CPU it shares with a VM long before its memory runs out. So
// each front end reads the host's pressure every Interval and puts it to the
// daemon (PUT /v1/host/load), which holds heavy commands back while the host
// is Busy (the governor's HostBusy) and, where the front end has no memory
// loop of its own, drops the VM's caches so its memory goes back:
//
//   - Linux: PSI, the share of the last 10 s some task stalled on memory, the
//     CPU or the disk (/proc/pressure), and MemAvailable against Reserve.
//     Cloud Hypervisor's supervisor reports it, and its memory loop also
//     stops growing the VM while the host stalls on memory (chv/memory.go).
//   - Mac: the kernel's memory pressure level (kern.memorystatus_vm_pressure_level,
//     what Activity Monitor's graph shows), warn or critical. For Lima's VM
//     `agentbox vm lan`, which runs beside it as long as it does, reports
//     it; for vz's, its supervisor.
//   - Windows: GlobalMemoryStatusEx's available memory against Reserve, and
//     the CPU's busy share from GetSystemTimes; the relay
//     (`agentbox.exe relay`, while the app is open) reports it.
//
// A front end that stops reporting stops holding anything: the daemon forgets
// a report after api.HostLoadTTL.
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
	// past which the host counts as busy. Memory as chv's memHostPressure:
	// any real stall on memory means the desktop is waiting on the disk.
	// CPU and IO only when they are high: some tasks waiting for a CPU or the
	// disk is normal on a desktop, a third or half of the time isn't.
	MemoryBusy = 10.0
	CPUBusy    = 50.0
	IOBusy     = 40.0
	// CPUBusyShare is, where there's no PSI (Windows), the share of all
	// cores busy past which the host counts as busy.
	CPUBusyShare = 90.0
	// CalmAfter is how long every reading must stay under the thresholds
	// before a busy host counts as calm again, so heavy commands don't start
	// in the gap between two bursts of the desktop's own.
	CalmAfter = 10 * time.Second
	// ReserveMin and ReserveShare (of its memory) make Reserve.
	ReserveMin   = int64(4) << 30
	ReserveShare = 0.25
)

// Reserve is what a host with total bytes of memory keeps for itself, its
// desktop and the app: the larger of ReserveMin and ReserveShare of it.
func Reserve(total int64) int64 {
	return max(ReserveMin, int64(float64(total)*ReserveShare))
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

// Judge turns readings into api.HostLoad, with CalmAfter's hysteresis.
type Judge struct {
	busy      bool
	calmSince time.Time
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

// Judge makes r's report at now. A host becomes busy at the first reading
// over a threshold, and calm once readings have stayed under them all for
// CalmAfter.
func (j *Judge) Judge(r Reading, now time.Time) api.HostLoad {
	why := r.over()
	switch {
	case why != "":
		j.busy, j.calmSince = true, time.Time{}
	case j.busy && j.calmSince.IsZero():
		j.calmSince = now
	case j.busy && now.Sub(j.calmSince) >= CalmAfter:
		j.busy, j.calmSince = false, time.Time{}
	}
	return api.HostLoad{
		OS:     r.OS,
		Memory: r.Memory, CPU: r.CPU, IO: r.IO, CPUShare: r.CPUShare,
		Level:       r.Level,
		MemoryTotal: r.MemoryTotal, Available: r.Available,
		Busy:    j.busy,
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
// memory itself (Cloud Hypervisor). onChange, if set, is told each time the
// host turns busy or calm, for the front end's log.
func Report(ctx context.Context, put Put, reclaim bool, onChange func(api.HostLoad)) {
	var j Judge
	r := newReader()
	was := false
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		reading, err := r.read()
		if err == nil {
			load := j.Judge(reading, time.Now())
			load.Reclaim = load.Reclaim && reclaim
			if load.Busy != was && onChange != nil {
				onChange(load)
			}
			was = load.Busy
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
