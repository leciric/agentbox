package desktop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

// serverEvent builds one RECORD data reply carrying a single core event.
func serverEvent(kind, detail byte, x, y int16) []byte {
	reply := make([]byte, 64)
	reply[0] = 1
	reply[1] = recordFromServer
	le.PutUint32(reply[4:], 32/4)
	ev := reply[32:64]
	ev[0] = kind
	ev[1] = detail
	le.PutUint16(ev[20:], uint16(x))
	le.PutUint16(ev[22:], uint16(y))
	return reply
}

// TestLogInputWritesShiftedKeysAndClicksNotScrolls drives logInput against a
// fake X server: a shift held down, a keypress that shift turns into a
// capital, a button click and a scroll-wheel "click" (buttons 4-7), which
// LogInput must not log as a click.
func TestLogInputWritesShiftedKeysAndClicksNotScrolls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, srv := newXServer(t)
	x.idBase, x.record = 0x400, 42
	// Keycode 50 is Shift_L; 38 is "a"/"A" (unshifted/shifted keysyms).
	x.keysyms[50] = []uint32{0xffe1}
	x.keysyms[38] = []uint32{'a', 'A'}

	var buf bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- logInput(ctx, x, &buf) }()

	srv.readRequest(20 + 4 + 24) // CreateContext
	srv.readRequest(8)           // EnableContext

	srv.conn.Write(serverEvent(xKeyPress, 50, 0, 0))   // shift down
	srv.conn.Write(serverEvent(xKeyPress, 38, 0, 0))   // 'A', shift held
	srv.conn.Write(serverEvent(xKeyRelease, 50, 0, 0)) // shift up
	srv.conn.Write(serverEvent(xButtonPress, 1, 10, 20))
	srv.conn.Write(serverEvent(xButtonPress, 4, 0, 0)) // scroll notch: not a click

	// Give the reader a moment to consume the events before tearing down;
	// logInput has no other way to observe "caught up" than reading more.
	time.Sleep(50 * time.Millisecond)
	cancel()
	srv.conn.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("logInput() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("logInput() didn't return after the context was cancelled")
	}

	var events []InputEvent
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		var ev InputEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			t.Fatalf("decoding %q: %v", sc.Text(), err)
		}
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("logged %d events, want 2 (the key and the click, not shift or the scroll): %+v", len(events), events)
	}
	if events[0].Key != "A" || len(events[0].Mods) != 1 || events[0].Mods[0] != "shift" {
		t.Errorf("first event = %+v, want key A with shift held", events[0])
	}
	if events[1].Button != 1 || events[1].X != 10 || events[1].Y != 20 {
		t.Errorf("second event = %+v, want button 1 at (10, 20)", events[1])
	}
}
