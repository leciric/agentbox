package chv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// chClient talks to Cloud Hypervisor over its REST API, on the unix socket
// --api-socket names (vmm/src/api/openapi/cloud-hypervisor.yaml). It needs no
// ch-remote.
type chClient struct {
	http *http.Client
}

func newCHClient(socket string) *chClient {
	return &chClient{http: unixHTTPClient(socket, 30*time.Second)}
}

// unixHTTPClient is an HTTP client whose every request goes to one unix
// socket, whatever the URL's host.
func unixHTTPClient(socket string, timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socket)
			},
			MaxIdleConns:    2,
			IdleConnTimeout: 30 * time.Second,
		},
	}
}

// The VM's states, as Cloud Hypervisor names them (VmState).
const (
	chCreated  = "Created"
	chRunning  = "Running"
	chShutdown = "Shutdown"
	chPaused   = "Paused"
)

// chInfo is the part of GET /vm.info the supervisor reads.
type chInfo struct {
	State  string `json:"state"`
	Config struct {
		Memory struct {
			Size           int64 `json:"size"`
			HotplugSize    int64 `json:"hotplug_size"`
			HotpluggedSize int64 `json:"hotplugged_size"`
		} `json:"memory"`
	} `json:"config"`
	// MemoryActualSize is the guest's memory as it is now: its boot memory
	// and the virtio-mem blocks it has plugged (not what vm.resize asked
	// for, which it gets to in its own time), less the balloon.
	MemoryActualSize int64 `json:"memory_actual_size"`
}

// Requested is what the VM has been asked to have: its boot memory and what
// vm.resize asked for on top (virtio-mem). The guest plugs it in its own time,
// and may not manage to unplug all of a shrink.
func (i chInfo) Requested() int64 {
	return i.Config.Memory.Size + i.Config.Memory.HotpluggedSize
}

func (c *chClient) do(ctx context.Context, method, endpoint string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost/api/v1/"+endpoint, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloud-hypervisor %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("cloud-hypervisor %s: %s", endpoint, msg)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("cloud-hypervisor %s: %w", endpoint, err)
		}
	}
	return nil
}

func (c *chClient) Ping(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "vmm.ping", nil, nil)
}

func (c *chClient) Info(ctx context.Context) (chInfo, error) {
	var i chInfo
	err := c.do(ctx, http.MethodGet, "vm.info", nil, &i)
	return i, err
}

func (c *chClient) Pause(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vm.pause", nil, nil)
}

func (c *chClient) Resume(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vm.resume", nil, nil)
}

// Resize asks for the VM's memory to be bytes in all, by plugging or
// unplugging virtio-mem blocks.
func (c *chClient) Resize(ctx context.Context, bytes int64) error {
	return c.do(ctx, http.MethodPut, "vm.resize", map[string]int64{"desired_ram": bytes}, nil)
}

// PowerButton presses the VM's ACPI power button: the guest shuts down.
func (c *chClient) PowerButton(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vm.power-button", nil, nil)
}

// ShutdownVMM ends Cloud Hypervisor itself.
func (c *chClient) ShutdownVMM(ctx context.Context) error {
	return c.do(ctx, http.MethodPut, "vmm.shutdown", nil, nil)
}
