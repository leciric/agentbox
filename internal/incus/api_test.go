package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/pkg/sftp"
)

// fakeAPI is an Incus REST API on a unix socket, as much of it as the
// operations here use. It answers from instances, and records every request
// that changes something, as "METHOD path body".
type fakeAPI struct {
	mu        sync.Mutex
	instances map[string]*api.InstanceFull
	profiles  map[string]*api.Profile
	failOp    string // an operation whose request path has this suffix fails
	failFast  bool   // and has already failed when it is first looked at
	lastOp    api.Operation
	changes   []string
}

func startFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{instances: map[string]*api.InstanceFull{}, profiles: map[string]*api.Profile{}}
	socket := filepath.Join(t.TempDir(), "incus.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = srv.Serve(l) }()
	t.Setenv("INCUS_SOCKET", socket)
	resetShared()
	t.Cleanup(func() {
		_ = srv.Close()
		resetShared()
	})
	return f
}

// serveSFTP upgrades the request to SFTP, as Incus does for its instances'
// files, and serves this machine's own files over it: the tests use paths in
// a temporary directory as paths inside the instance.
func serveSFTP(w http.ResponseWriter) {
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: sftp\r\nConnection: Upgrade\r\n\r\n")
	if rw.Flush() != nil {
		return
	}
	srv, err := sftp.NewServer(conn)
	if err != nil {
		return
	}
	_ = srv.Serve()
}

func resetShared() {
	shared.Lock()
	shared.server = nil
	shared.Unlock()
}

func (f *fakeAPI) add(inst api.InstanceFull) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if inst.Config == nil {
		inst.Config = map[string]string{}
	}
	if inst.Devices == nil {
		inst.Devices = map[string]map[string]string{}
	}
	f.instances[inst.Name] = &inst
}

func (f *fakeAPI) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.changes...)
}

func reply(w http.ResponseWriter, metadata any) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "sync", "status": "Success", "status_code": 200, "metadata": metadata,
	})
}

func replyError(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "error", "error": msg, "error_code": code})
}

// replyOp answers with a finished operation; /wait gives the same one back.
// f.mu is held.
func (f *fakeAPI) replyOp(w http.ResponseWriter, r *http.Request) {
	op := api.Operation{ID: "op", Class: "task", Status: "Success", StatusCode: api.Success}
	if f.failOp != "" && strings.HasSuffix(r.URL.Path, f.failOp) {
		op.Status, op.StatusCode, op.Err = "Failure", api.Failure, "it went wrong"
	}
	f.lastOp = op
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "async", "status": "Operation created", "status_code": 100,
		"operation": "/1.0/operations/op", "metadata": op,
	})
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/1.0")
	if path == "/instances/agent-01/sftp" {
		serveSFTP(w)
		return
	}
	body, _ := io.ReadAll(r.Body)
	if r.Method != http.MethodGet {
		f.mu.Lock()
		f.changes = append(f.changes, strings.TrimSpace(r.Method+" "+path+" "+strings.TrimSpace(string(body))))
		f.mu.Unlock()
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case path == "":
		reply(w, api.Server{APIExtensions: []string{"container_full", "instance_get_full", "api_filtering", "instance_all_projects", "resources", "storage", "storage_volume_state"}, Environment: api.ServerEnvironment{Server: "incus"}})
	case path == "/operations/op":
		op := f.lastOp // still running, unless it failed fast
		if !f.failFast {
			op.Status, op.StatusCode, op.Err = "Running", api.Running, ""
		}
		reply(w, op)
	case path == "/operations/op/wait":
		reply(w, f.lastOp)
	case path == "/instances" && r.Method == http.MethodGet:
		var all []api.InstanceFull
		for _, name := range []string{"agent-01", "agent-02"} {
			if inst, ok := f.instances[name]; ok {
				all = append(all, *inst)
			}
		}
		reply(w, all)
	case len(parts) >= 2 && parts[0] == "instances":
		inst, ok := f.instances[parts[1]]
		if !ok {
			replyError(w, http.StatusNotFound, "Instance not found")
			return
		}
		switch {
		case len(parts) == 2 && r.Method == http.MethodGet:
			w.Header().Set("ETag", "etag")
			reply(w, inst)
		case len(parts) == 2 && r.Method == http.MethodPut:
			var put api.InstancePut
			_ = json.Unmarshal(body, &put)
			if put.Restore == "" {
				inst.Config, inst.Devices = put.Config, put.Devices
			}
			f.replyOp(w, r)
		case len(parts) == 3 && parts[2] == "snapshots" && r.Method == http.MethodGet:
			if r.URL.Query().Get("recursion") == "1" {
				reply(w, inst.Snapshots)
				return
			}
			var urls []string
			for _, s := range inst.Snapshots {
				urls = append(urls, "/1.0/instances/"+inst.Name+"/snapshots/"+s.Name)
			}
			reply(w, urls)
		default:
			f.replyOp(w, r)
		}
	case len(parts) == 2 && parts[0] == "profiles":
		p, ok := f.profiles[parts[1]]
		switch {
		case !ok:
			replyError(w, http.StatusNotFound, "Profile not found")
		case r.Method == http.MethodGet:
			reply(w, p)
		default:
			_ = json.Unmarshal(body, &p.ProfilePut)
			reply(w, nil)
		}
	case path == "/profiles":
		var post api.ProfilesPost
		_ = json.Unmarshal(body, &post)
		f.profiles[post.Name] = &api.Profile{Name: post.Name}
		reply(w, nil)
	case path == "/storage-pools/default":
		reply(w, api.StoragePool{Name: "default", Driver: "btrfs"})
	case path == "/storage-pools/default/resources":
		reply(w, api.ResourcesStoragePool{Space: api.ResourcesStoragePoolSpace{Used: 3, Total: 10}})
	case path == "/storage-pools/default/volumes/container/agent-01/state":
		reply(w, api.StorageVolumeState{Usage: &api.StorageVolumeStateUsage{Used: 7}})
	default:
		replyError(w, http.StatusNotFound, "not found")
	}
}

func TestAPIReads(t *testing.T) {
	f := startFakeAPI(t)
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f.add(api.InstanceFull{
		Instance: api.Instance{
			Name: "agent-01", Status: "Running", StatusCode: api.Running,
			InstancePut: api.InstancePut{
				Config:  map[string]string{"limits.cpu": "2"},
				Devices: map[string]map[string]string{"worktree": {"type": "disk"}},
			},
			ExpandedConfig: map[string]string{"limits.cpu": "2", "limits.memory": "4GiB"},
		},
		State: &api.InstanceState{
			Network: map[string]api.InstanceStateNetwork{"eth0": {Addresses: []api.InstanceStateNetworkAddress{
				{Family: "inet6", Address: "fd42::1"}, {Family: "inet", Address: "10.0.0.5"},
			}}},
			CPU:       api.InstanceStateCPU{Usage: 5},
			Memory:    api.InstanceStateMemory{Usage: 6},
			Processes: 7,
		},
		Snapshots: []api.InstanceSnapshot{
			{Name: "later", CreatedAt: created.Add(time.Hour)},
			{Name: "ready", CreatedAt: created},
		},
	})
	f.add(api.InstanceFull{Instance: api.Instance{Name: "agent-02", Status: "Stopped"}})
	ctx := context.Background()
	c := Client{}

	all, err := c.Instances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].Name != "agent-01" || all[1].Name != "agent-02" || all[1].State != nil {
		t.Fatalf("Instances = %+v", all)
	}
	inst, err := c.Instance(ctx, "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if inst.IPv4() != "10.0.0.5" || inst.State.CPU.Usage != 5 || inst.State.Memory.Usage != 6 || inst.State.Processes != 7 ||
		inst.ExpandedConfig["limits.memory"] != "4GiB" || inst.Config["limits.cpu"] != "2" {
		t.Fatalf("Instance = %+v", inst)
	}
	if _, err := c.Instance(ctx, "agent-09"); !errors.Is(err, ErrNotFound) || err.Error() != "agent-09: instance not found" {
		t.Fatalf("Instance of a missing one: %v", err)
	}

	if ok, err := c.HasSnapshot(ctx, "agent-01", "ready"); err != nil || !ok {
		t.Fatalf("HasSnapshot(ready) = %v, %v", ok, err)
	}
	if ok, err := c.HasSnapshot(ctx, "agent-01", "gone"); err != nil || ok {
		t.Fatalf("HasSnapshot(gone) = %v, %v", ok, err)
	}
	if ok, err := c.HasSnapshot(ctx, "agent-09", "ready"); err != nil || ok {
		t.Fatalf("HasSnapshot of a missing instance = %v, %v", ok, err)
	}
	snaps, err := c.Snapshots(ctx, "agent-01")
	if err != nil || len(snaps) != 2 || snaps[0].Name != "ready" || !snaps[0].CreatedAt.Equal(created) {
		t.Fatalf("Snapshots = %+v, %v", snaps, err)
	}

	d, err := c.Details(ctx, "agent-01")
	if err != nil || d.Devices["worktree"]["type"] != "disk" || d.Config["limits.cpu"] != "2" || d.ExpandedConfig["limits.memory"] != "4GiB" {
		t.Fatalf("Details = %+v, %v", d, err)
	}
	if _, err := c.Config(ctx, "agent-09"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Config of a missing instance: %v", err)
	}
	if used, total, err := c.PoolSpace(ctx, "default"); err != nil || used != 3 || total != 10 {
		t.Fatalf("PoolSpace = %d, %d, %v", used, total, err)
	}
	if driver, err := c.PoolDriver(ctx, "default"); err != nil || driver != "btrfs" {
		t.Fatalf("PoolDriver = %q, %v", driver, err)
	}
	if used, err := c.VolumeUsage(ctx, "default", "agent-01"); err != nil || used != 7 {
		t.Fatalf("VolumeUsage = %d, %v", used, err)
	}
	if _, err := c.VolumeUsage(ctx, "default", "agent-09"); err == nil ||
		err.Error() != "incus query /1.0/storage-pools/default/volumes/container/agent-09/state: not found" {
		t.Fatalf("VolumeUsage of a missing volume: %v", err)
	}
}

func TestAPIOperations(t *testing.T) {
	f := startFakeAPI(t)
	f.add(api.InstanceFull{Instance: api.Instance{
		Name: "agent-01", Status: "Running", StatusCode: api.Running,
		InstancePut: api.InstancePut{
			Config:  map[string]string{"limits.cpu": "2"},
			Devices: map[string]map[string]string{"kvm": {"type": "unix-char"}},
		},
	}})
	ctx := context.Background()
	c := Client{}

	steps := []struct {
		name string
		do   func() error
		want string // the request it makes, "" for none checked
	}{
		{"start", func() error { return c.Start(ctx, "agent-01") }, `PUT /instances/agent-01/state {"action":"start","timeout":-1,"force":false,"stateful":false}`},
		{"stop", func() error { return c.Stop(ctx, "agent-01") }, `PUT /instances/agent-01/state {"action":"stop","timeout":-1,"force":false,"stateful":false}`},
		{"stop within", func() error { return c.StopWithin(ctx, "agent-01", 30*time.Second) }, `PUT /instances/agent-01/state {"action":"stop","timeout":30,"force":false,"stateful":false}`},
		{"force stop", func() error { return c.ForceStop(ctx, "agent-01") }, `PUT /instances/agent-01/state {"action":"stop","timeout":-1,"force":true,"stateful":false}`},
		{"pause", func() error { return c.Pause(ctx, "agent-01") }, `PUT /instances/agent-01/state {"action":"freeze","timeout":-1,"force":false,"stateful":false}`},
		{"resume", func() error { return c.Resume(ctx, "agent-01") }, `PUT /instances/agent-01/state {"action":"unfreeze","timeout":-1,"force":false,"stateful":false}`},
		{"rename", func() error { return c.Rename(ctx, "agent-01", "agent-01") }, `POST /instances/agent-01 {"name":"agent-01","migration":false,`},
		{"snapshot", func() error { return c.CreateSnapshot(ctx, "agent-01", "s1") }, `POST /instances/agent-01/snapshots {"name":"s1","stateful":false,`},
		{"restore", func() error { return c.RestoreSnapshot(ctx, "agent-01", "s1") }, ""},
		{"delete snapshot", func() error { return c.DeleteSnapshot(ctx, "agent-01", "s1") }, "DELETE /instances/agent-01/snapshots/s1"},
		{"set", func() error { return c.SetConfig(ctx, "agent-01", "limits.memory=4GiB", "raw.idmap=both 1000 1000") }, ""},
		{"unset", func() error { return c.UnsetConfig(ctx, "agent-01", "limits.cpu") }, ""},
		{"add device", func() error { return c.AddDevice(ctx, "agent-01", "worktree", "disk", "source=/w", "path=/w") }, ""},
		{"remove device", func() error { return c.RemoveDevice(ctx, "agent-01", "kvm") }, ""},
	}
	for _, s := range steps {
		before := len(f.recorded())
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		got := f.recorded()[before:]
		if s.want != "" && (len(got) != 1 || !strings.HasPrefix(got[0], s.want)) {
			t.Errorf("%s made %q, want %q", s.name, got, s.want)
		}
	}
	d, err := c.Details(ctx, "agent-01")
	if err != nil {
		t.Fatal(err)
	}
	if d.Config["limits.memory"] != "4GiB" || d.Config["raw.idmap"] != "both 1000 1000" || d.Config["limits.cpu"] != "" {
		t.Errorf("config after set and unset = %v", d.Config)
	}
	if d.Devices["worktree"]["source"] != "/w" || d.Devices["worktree"]["type"] != "disk" || d.Devices["kvm"] != nil {
		t.Errorf("devices after add and remove = %v", d.Devices)
	}

	// The errors the incus command gives, which callers look for.
	for _, tc := range []struct {
		err  error
		want string
	}{
		{c.UnsetConfig(ctx, "agent-01", "limits.cpu"), "incus config unset agent-01 limits.cpu: Can't unset key 'limits.cpu', it's not currently set"},
		{c.AddDevice(ctx, "agent-01", "worktree", "disk"), "incus config device add agent-01 worktree disk: The device already exists"},
		{c.RemoveDevice(ctx, "agent-01", "kvm"), `incus config device remove agent-01 kvm: Device “kvm” doesn't exist`},
		{c.Start(ctx, "agent-09"), "incus start agent-09: Instance not found"},
		{c.Delete(ctx, "agent-09"), "incus delete --force agent-09: Failed checking instance agent-09 exists: Instance not found"},
		{c.DeleteSnapshot(ctx, "agent-09", "s1"), "incus snapshot delete agent-09 s1: Instance not found"},
	} {
		if tc.err == nil || tc.err.Error() != tc.want {
			t.Errorf("got %v, want %q", tc.err, tc.want)
		}
	}

	f.failOp = "/state"
	if err := c.Start(ctx, "agent-01"); err == nil ||
		err.Error() != "incus start agent-01: it went wrong\nTry `incus info --show-log agent-01` for more info" {
		t.Errorf("a failed start: %v", err)
	}
	f.failFast = true
	if err := c.Start(ctx, "agent-01"); err == nil || err.Error() != "incus start agent-01: it went wrong" {
		t.Errorf("a start that failed at once: %v", err)
	}
	f.failFast = false
	if err := c.Delete(ctx, "agent-01"); err == nil ||
		err.Error() != "incus delete --force agent-01: Stopping the instance agent-01 failed: it went wrong" {
		t.Errorf("a failed stop before delete: %v", err)
	}
	f.failOp = ""
	before := len(f.recorded())
	if err := c.Delete(ctx, "agent-01"); err != nil {
		t.Fatal(err)
	}
	if got := f.recorded()[before:]; len(got) != 2 || !strings.HasPrefix(got[0], "PUT /instances/agent-01/state") || got[1] != "DELETE /instances/agent-01" {
		t.Errorf("delete --force of a running instance made %q", got)
	}
}

func TestAPIProfiles(t *testing.T) {
	startFakeAPI(t)
	ctx := context.Background()
	c := Client{}
	if ok, err := c.HasProfile(ctx, "agentbox"); err != nil || ok {
		t.Fatalf("HasProfile before it's made = %v, %v", ok, err)
	}
	if err := c.CreateProfile(ctx, "agentbox"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetProfileConfig(ctx, "agentbox", "security.nesting=true", "linux.kernel_modules=overlay"); err != nil {
		t.Fatal(err)
	}
	if ok, err := c.HasProfile(ctx, "agentbox"); err != nil || !ok {
		t.Fatalf("HasProfile after it's made = %v, %v", ok, err)
	}
	if err := c.SetProfileConfig(ctx, "missing", "a=b"); err == nil || err.Error() != "incus profile set missing a=b: Profile not found" {
		t.Fatalf("SetProfileConfig of a missing profile: %v", err)
	}
}

func TestAPIUnreachable(t *testing.T) {
	t.Setenv("INCUS_SOCKET", filepath.Join(t.TempDir(), "nothing.sock"))
	resetShared()
	t.Cleanup(resetShared)
	c := Client{}
	if _, err := c.Instances(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "incus list --format json: ") {
		t.Fatalf("Instances with no Incus: %v", err)
	}
	// A failed connection isn't kept: Incus answering later is found.
	startFakeAPI(t)
	if _, err := c.Instances(context.Background()); err != nil {
		t.Fatalf("Instances once Incus answers: %v", err)
	}
}

func TestExitCode(t *testing.T) {
	if code, ok := ExitCode(fmt.Errorf("running: %w", &ExitError{Code: 3})); !ok || code != 3 {
		t.Errorf("ExitCode of an ExitError = %d, %v", code, ok)
	}
	if _, ok := ExitCode(errors.New("no")); ok {
		t.Error("ExitCode of another error is ok")
	}
	if got := (&ExitError{Code: 2}).Error(); got != "exit status 2" {
		t.Errorf("ExitError reads %q", got)
	}
}
