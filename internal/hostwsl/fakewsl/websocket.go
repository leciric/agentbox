package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net/http"
)

// echo is the smallest websocket server that will do: it upgrades, and sends
// every frame it gets back, unmasked, until the client closes.
func echo(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		http.Error(w, "not a websocket", http.StatusBadRequest)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n")
	rw.Flush()
	for {
		op, payload, err := readFrame(rw.Reader)
		if err != nil || op == 0x8 {
			writeFrame(rw.Writer, 0x8, nil)
			rw.Flush()
			return
		}
		writeFrame(rw.Writer, op, payload)
		rw.Flush()
	}
}

func readFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	var mask [4]byte
	if h[1]&0x80 != 0 {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return h[0] & 0x0f, payload, nil
}

func writeFrame(w *bufio.Writer, op byte, payload []byte) {
	w.WriteByte(0x80 | op)
	switch n := len(payload); {
	case n < 126:
		w.WriteByte(byte(n))
	case n < 1<<16:
		w.WriteByte(126)
		binary.Write(w, binary.BigEndian, uint16(n))
	default:
		w.WriteByte(127)
		binary.Write(w, binary.BigEndian, uint64(n))
	}
	w.Write(payload)
}
