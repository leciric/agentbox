package iossim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"agentbox/internal/api"
)

// Client is the daemon's way to the helper, which it finds from the state
// file each time: the helper may have restarted on another port.
type Client struct {
	File string
	HTTP *http.Client
	// host overrides where the helper is: the Mac's own 127.0.0.1 for the
	// front end, which asks it whether it runs.
	host string
}

// NewClient finds the helper from file; "" is a host with no helper, where
// every call is ErrUnavailable.
func NewClient(file string) *Client {
	return &Client{File: file, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) base() (State, string, error) {
	if c == nil || c.File == "" {
		return State{}, "", fmt.Errorf("%w: it runs on a Mac, beside AgentBox's VM", ErrUnavailable)
	}
	st, err := ReadState(c.File)
	if err != nil {
		return State{}, "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	host := DefaultHost
	switch {
	case c.host != "":
		host = c.host
	case st.Host != "":
		host = st.Host
	}
	return st, "http://" + net.JoinHostPort(host, strconv.Itoa(st.Port)), nil
}

func (c *Client) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	st, base, err := c.base()
	if err != nil {
		return nil, err
	}
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+st.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: AgentBox's simulator helper on your Mac doesn't answer (%v): run any agentbox command on the Mac (agentbox vm start) to start it again", ErrUnavailable, err)
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var e api.Error
		if json.Unmarshal(data, &e) != nil || e.Error == "" {
			e.Error = strings.TrimSpace(string(data))
		}
		return nil, &api.StatusError{Code: resp.StatusCode, Message: e.Error}
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func devicePath(udid, action string) (string, error) {
	if !ValidUDID(udid) {
		return "", fmt.Errorf("%q isn't a simulator's UDID", udid)
	}
	return "/v1/devices/" + udid + "/" + action, nil
}

func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	return h, c.do(ctx, http.MethodGet, "/v1/health", nil, &h)
}

// Version is the helper's version, without the Xcode check Health makes:
// what the front end asks every time it runs.
func (c *Client) Version(ctx context.Context) (string, error) {
	var h Health
	return h.Version, c.do(ctx, http.MethodGet, "/v1/version", nil, &h)
}

func (c *Client) Devices(ctx context.Context) ([]api.IOSDevice, error) {
	var devices []api.IOSDevice
	return devices, c.do(ctx, http.MethodGet, "/v1/devices", nil, &devices)
}

func (c *Client) post(ctx context.Context, udid, action string, body, out any) error {
	path, err := devicePath(udid, action)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path, body, out)
}

func (c *Client) Boot(ctx context.Context, udid string) error {
	return c.post(ctx, udid, "boot", nil, nil)
}

func (c *Client) Shutdown(ctx context.Context, udid string) error {
	return c.post(ctx, udid, "shutdown", nil, nil)
}

func (c *Client) Install(ctx context.Context, udid, path string) error {
	return c.post(ctx, udid, "install", api.IOSInstallRequest{Path: path}, nil)
}

func (c *Client) Launch(ctx context.Context, udid string, req api.IOSLaunchRequest) (api.IOSResult, error) {
	var out api.IOSResult
	return out, c.post(ctx, udid, "launch", req, &out)
}

func (c *Client) OpenURL(ctx context.Context, udid, url string) error {
	return c.post(ctx, udid, "openurl", api.IOSOpenURLRequest{URL: url}, nil)
}

func (c *Client) Input(ctx context.Context, udid string, in api.IOSInput) error {
	in.Device = ""
	return c.post(ctx, udid, "input", in, nil)
}

// Screenshot is a PNG of a simulator's screen.
func (c *Client) Screenshot(ctx context.Context, udid string) ([]byte, error) {
	path, err := devicePath(udid, "screenshot")
	if err != nil {
		return nil, err
	}
	resp, err := c.request(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// View opens a simulator's screen, the helper's WebSocket (Server.view).
func (c *Client) View(ctx context.Context, udid string) (*websocket.Conn, error) {
	path, err := devicePath(udid, "view")
	if err != nil {
		return nil, err
	}
	st, base, err := c.base()
	if err != nil {
		return nil, err
	}
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+path, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + st.Token}},
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	conn.SetReadLimit(maxFrame)
	return conn, nil
}
