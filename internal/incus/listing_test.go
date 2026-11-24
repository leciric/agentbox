package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lxc/incus/v7/shared/api"
)

// liveListing is a listing shaped like the user's VM on 23 Nov 2026: 26
// instances, 4 running, each with ~2.4 KB of config, ~1.2 KB of devices and
// one snapshot of ~6.8 KB, about 458 KB of JSON in all.
func liveListing() []api.InstanceFull {
	config := func(i int) map[string]string {
		c := map[string]string{
			"image.description": "AgentBox base", "image.os": "debian", "image.release": "trixie",
			"limits.cpu.allowance": "800%", "limits.memory": "24GiB", "limits.memory.swap": "false",
			"limits.processes": "8192", "security.nesting": "true", "security.syscalls.intercept.mknod": "true",
			"security.syscalls.intercept.setxattr": "true", "raw.idmap": "both 1000 1000",
			"volatile.base_image":             strings.Repeat("ab", 32),
			"volatile.cloud-init.instance-id": "4f1c2a9e-6d3b-4b1e-9a77-0c1d2e3f4a5b",
			"volatile.eth0.host_name":         fmt.Sprintf("veth%08x", i), "volatile.eth0.hwaddr": "10:66:6a:12:34:56",
			"volatile.idmap.base": "0", "volatile.idmap.current": strings.Repeat(`[{"Isuid":true,"Isgid":false,"Hostid":1000000,"Nsid":0,"Maprange":1000000000}]`, 2),
			"volatile.idmap.next":       strings.Repeat(`[{"Isuid":true,"Isgid":false,"Hostid":1000000,"Nsid":0,"Maprange":1000000000}]`, 2),
			"volatile.last_state.idmap": "[]", "volatile.last_state.power": "RUNNING",
			"volatile.uuid": "7e3d1f0a-2b4c-4d5e-8f60-718293a4b5c6", "volatile.uuid.generation": "7e3d1f0a-2b4c-4d5e-8f60-718293a4b5c6",
			"user.agentbox.project": fmt.Sprintf("project-%02d", i%9), "user.agentbox.agent": fmt.Sprintf("agent-%02d", i),
			"user.agentbox.branch": fmt.Sprintf("agentbox/feat-some-long-branch-name-%02d", i),
			"environment.TMPDIR":   "/t", "environment.LANG": "C.UTF-8",
			"user.agentbox.note": strings.Repeat("x", 2000),
		}
		return c
	}
	devices := map[string]map[string]string{
		"eth0":     {"type": "nic", "network": "agentbox0", "name": "eth0", "ipv4.address": "10.8.8.42"},
		"root":     {"type": "disk", "path": "/", "pool": "agentbox", "size": "60GiB"},
		"worktree": {"type": "disk", "source": "/var/lib/agentbox/worktrees/project/agent-01", "path": "/var/lib/agentbox/worktrees/project/agent-01", "shift": "true"},
		"git":      {"type": "disk", "source": "/home/user/src/project/.git", "path": "/home/user/src/project/.git", "shift": "true"},
		"cache":    {"type": "disk", "source": "/var/cache/agentbox/packages", "path": "/var/cache/agentbox/packages", "shift": "true"},
		"socket":   {"type": "proxy", "listen": "unix:/run/agentbox.sock", "connect": "unix:/home/user/.local/share/agentbox/run/agent.sock", "bind": "instance", "uid": "1000", "gid": "1000", "mode": "0660"},
		"kvm":      {"type": "unix-char", "source": "/dev/kvm", "path": "/dev/kvm"},
	}
	created := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var out []api.InstanceFull
	for i := range 26 {
		cfg := config(i)
		inst := api.InstanceFull{
			Instance: api.Instance{
				Name: fmt.Sprintf("ab-project-agent-%02d", i), Status: "Stopped", StatusCode: api.Stopped,
				Type: "container", CreatedAt: created, LastUsedAt: created,
				Location: "none", Project: "default",
				InstancePut:     api.InstancePut{Architecture: "x86_64", Config: cfg, Devices: devices, Profiles: []string{"default", "agentbox"}, Ephemeral: false, Stateful: false},
				ExpandedConfig:  cfg,
				ExpandedDevices: devices,
			},
			Snapshots: []api.InstanceSnapshot{{
				Name: "ready", CreatedAt: created, LastUsedAt: created, Architecture: "x86_64", Size: 1 << 30,
				InstanceSnapshotPut: api.InstanceSnapshotPut{ExpiresAt: created},
				Config:              cfg, Devices: devices, ExpandedConfig: cfg, ExpandedDevices: devices,
				Profiles: []string{"default", "agentbox"},
			}},
			Backups: []api.InstanceBackup{},
		}
		if i%7 == 0 {
			inst.Status, inst.StatusCode = "Running", api.Running
			inst.State = &api.InstanceState{
				Status: "Running", StatusCode: api.Running, Pid: 12345, Processes: 140,
				CPU:    api.InstanceStateCPU{Usage: 123456789000},
				Memory: api.InstanceStateMemory{Usage: 3 << 30, UsagePeak: 5 << 30, Total: 24 << 30},
				Disk:   map[string]api.InstanceStateDisk{"root": {Usage: 9 << 30, Total: 60 << 30}},
				Network: map[string]api.InstanceStateNetwork{
					"eth0": {Addresses: []api.InstanceStateNetworkAddress{
						{Family: "inet", Address: "10.8.8.42", Netmask: "24", Scope: "global"},
						{Family: "inet6", Address: "fd42:1:2:3:216:3eff:fe12:3456", Netmask: "64", Scope: "global"},
						{Family: "inet6", Address: "fe80::216:3eff:fe12:3456", Netmask: "64", Scope: "link"},
					}, Counters: api.InstanceStateNetworkCounters{BytesReceived: 123456789, BytesSent: 98765432, PacketsReceived: 123456, PacketsSent: 65432},
						Hwaddr: "10:66:6a:12:34:56", HostName: "veth1234", Mtu: 1500, State: "up", Type: "broadcast"},
					"lo": {Addresses: []api.InstanceStateNetworkAddress{{Family: "inet", Address: "127.0.0.1", Netmask: "8", Scope: "local"}, {Family: "inet6", Address: "::1", Netmask: "128", Scope: "local"}},
						Mtu: 65536, State: "up", Type: "loopback"},
					"docker0": {Addresses: []api.InstanceStateNetworkAddress{{Family: "inet", Address: "172.17.0.1", Netmask: "16", Scope: "global"}},
						Hwaddr: "02:42:ac:11:00:01", Mtu: 1500, State: "down", Type: "broadcast"},
				},
			}
		} else {
			inst.State = &api.InstanceState{Status: "Stopped", StatusCode: api.Stopped}
		}
		out = append(out, inst)
	}
	return out
}

// serveListing serves body as Incus's answer to every listing, and the
// server's own description at /1.0.
func serveListing(tb testing.TB, listing []api.InstanceFull) int {
	tb.Helper()
	body, err := json.Marshal(map[string]any{"type": "sync", "status": "Success", "status_code": 200, "metadata": listing})
	if err != nil {
		tb.Fatal(err)
	}
	server, _ := json.Marshal(map[string]any{"type": "sync", "status": "Success", "status_code": 200,
		"metadata": api.Server{APIExtensions: []string{"container_full", "instance_get_full", "api_filtering"}, Environment: api.ServerEnvironment{Server: "incus"}}})
	socket := filepath.Join(tb.TempDir(), "incus.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		tb.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/1.0" {
			_, _ = w.Write(server)
			return
		}
		_, _ = w.Write(body)
	})}
	go func() { _ = srv.Serve(l) }()
	tb.Setenv("INCUS_SOCKET", socket)
	resetShared()
	tb.Cleanup(func() {
		_ = srv.Close()
		resetShared()
	})
	return len(body)
}

// BenchmarkInstances is what one listing costs the daemon, which takes one
// every couple of seconds while the app is open (watch, the agents' list,
// the pressure loop).
func BenchmarkInstances(b *testing.B) {
	size := serveListing(b, liveListing())
	b.Logf("listing: %d KB", size/1024)
	c := Client{}
	ctx := context.Background()
	if _, err := c.Instances(ctx); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := c.Instances(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
