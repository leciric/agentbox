package devicestream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

// Dial opens a stream server's WebSocket over conn, a connection to it.
func Dial(ctx context.Context, conn net.Conn) (*websocket.Conn, error) {
	var once sync.Once
	dial := func(context.Context, string, string) (net.Conn, error) {
		var c net.Conn
		once.Do(func() { c = conn })
		if c == nil {
			return nil, errors.New("the stream's connection is used")
		}
		return c, nil
	}
	ws, res, err := websocket.Dial(ctx, "ws://device/", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: dial}},
	})
	if err != nil {
		_ = conn.Close()
		// The server says why it can't serve, like an emulator that stopped.
		if res != nil && res.Body != nil {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			if msg := strings.TrimSpace(string(body)); msg != "" {
				return nil, errors.New(msg)
			}
		}
		return nil, fmt.Errorf("connecting to the device's stream: %w", err)
	}
	ws.SetReadLimit(maxTag)
	return ws, nil
}

// Relay passes messages between a viewer's WebSocket and a stream server's,
// each as it is, until either side closes.
func Relay(ctx context.Context, viewer, device *websocket.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	pass := func(from, to *websocket.Conn) {
		for {
			typ, data, err := from.Read(ctx)
			if err == nil {
				err = to.Write(ctx, typ, data)
			}
			if err != nil {
				errs <- err
				cancel()
				return
			}
		}
	}
	go pass(viewer, device)
	go pass(device, viewer)
	err := <-errs
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
