// Package emulator talks to a running Android emulator over its gRPC endpoint
// (EmulatorController, emulator_controller.proto in the SDK's emulator/lib):
// the screen as raw images, sent only when it changes, and touches, keys,
// text and rotation into it.
//
// The few messages it needs are encoded by hand with protowire, and gRPC is
// HTTP/2 without TLS (h2c) to 127.0.0.1, which net/http speaks, so it needs
// neither grpc-go nor generated code.
package emulator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Endpoint is where a running emulator's gRPC server listens, and the token
// it wants.
type Endpoint struct {
	Port  int
	Token string
	AVD   string // the AVD's id, like "agentbox"
	PID   int
}

// RunningDirs are where emulators write a pid_<pid>.ini file describing
// themselves while they run, in the order the emulator itself prefers.
func RunningDirs(getenv func(string) string, uid int) []string {
	var dirs []string
	if dir := getenv("XDG_RUNTIME_DIR"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "avd", "running"))
	}
	dirs = append(dirs, fmt.Sprintf("/run/user/%d/avd/running", uid))
	if dir := getenv("ANDROID_EMULATOR_HOME"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "avd", "running"))
	}
	if home := getenv("HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, ".android", "avd", "running"))
	}
	return dirs
}

// Find finds the gRPC endpoint of the running emulator of AVD avd ("" for
// any), from the files in dirs whose emulator process is alive.
func Find(dirs []string, avd string) (Endpoint, error) {
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "pid_*.ini"))
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			ep := ParseDiscovery(data)
			pid, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(file), "pid_"), ".ini"))
			ep.PID = pid
			if ep.Port == 0 || (avd != "" && ep.AVD != avd) || !alive(pid) {
				continue
			}
			return ep, nil
		}
	}
	return Endpoint{}, errors.New("no running emulator has a gRPC endpoint")
}

// ParseDiscovery reads an emulator's pid_<pid>.ini.
func ParseDiscovery(data []byte) Endpoint {
	var ep Endpoint
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "grpc.port":
			ep.Port, _ = strconv.Atoi(strings.TrimSpace(value))
		case "grpc.token":
			ep.Token = strings.TrimSpace(value)
		case "avd.id":
			ep.AVD = strings.TrimSpace(value)
		}
	}
	return ep
}

// Client calls one emulator's EmulatorController.
type Client struct {
	ep   Endpoint
	http *http.Client
}

const service = "/android.emulation.control.EmulatorController/"

func New(ep Endpoint) *Client {
	var protocols http.Protocols
	protocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{
		Protocols: &protocols,
		// A frame is a few megabytes: big frames keep it from crawling.
		HTTP2: &http.HTTP2Config{MaxReadFrameSize: 1 << 20},
	}
	return &Client{ep: ep, http: &http.Client{Transport: transport}}
}

func (c *Client) Close() {
	c.http.CloseIdleConnections()
}

// call starts method with one request message and returns the response body,
// for reading messages from.
func (c *Client) call(ctx context.Context, method string, msg []byte) (*response, error) {
	body := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(body[1:5], uint32(len(msg)))
	copy(body[5:], msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d%s%s", c.ep.Port, service, method), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	if c.ep.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.ep.Token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("emulator %s: %w", method, err)
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		return nil, fmt.Errorf("emulator %s: HTTP %d", method, res.StatusCode)
	}
	// A call that fails at once answers with its status in the headers.
	if err := grpcStatus(method, res.Header); err != nil {
		_ = res.Body.Close()
		return nil, err
	}
	return &response{method: method, res: res, r: bufio.NewReaderSize(res.Body, 1<<20)}, nil
}

// unary calls method and returns its one response message.
func (c *Client) unary(ctx context.Context, method string, msg []byte) ([]byte, error) {
	res, err := c.call(ctx, method, msg)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	out, err := res.Next()
	if err == io.EOF {
		return nil, fmt.Errorf("emulator %s: no response", method)
	}
	if err != nil {
		return nil, err
	}
	// Reading on to the end gives the call's status.
	if _, err := res.Next(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("emulator %s: more than one response", method)
		}
		return nil, err
	}
	return out, nil
}

type response struct {
	method string
	res    *http.Response
	r      *bufio.Reader
}

// maxMessage bounds one message: a 4K screen in RGBA is 33 MB.
const maxMessage = 64 << 20

// Next reads the next message, or io.EOF once the call has ended well.
func (r *response) Next() ([]byte, error) {
	var head [5]byte
	if _, err := io.ReadFull(r.r, head[:]); err != nil {
		if err == io.EOF {
			if err := grpcStatus(r.method, r.res.Trailer); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		return nil, fmt.Errorf("emulator %s: %w", r.method, err)
	}
	if head[0] != 0 {
		return nil, fmt.Errorf("emulator %s: compressed messages aren't supported", r.method)
	}
	n := binary.BigEndian.Uint32(head[1:])
	if n > maxMessage {
		return nil, fmt.Errorf("emulator %s: a %d-byte message is too big", r.method, n)
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(r.r, msg); err != nil {
		return nil, fmt.Errorf("emulator %s: %w", r.method, err)
	}
	return msg, nil
}

func (r *response) Close() { _ = r.res.Body.Close() }

func grpcStatus(method string, h http.Header) error {
	code := h.Get("Grpc-Status")
	if code == "" || code == "0" {
		return nil
	}
	msg := h.Get("Grpc-Message")
	if msg == "" {
		msg = "status " + code
	}
	if code == "16" || code == "7" {
		return fmt.Errorf("emulator %s: not allowed (%s)", method, msg)
	}
	return fmt.Errorf("emulator %s: %s", method, msg)
}
