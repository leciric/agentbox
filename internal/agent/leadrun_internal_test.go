package agent

import (
	"io"
	"strings"
	"testing"
)

func TestTailBuffer(t *testing.T) {
	b := &tailBuffer{max: 10}
	for i := 0; i < 100; i++ {
		io.WriteString(b, "line\n")
	}
	io.WriteString(b, "last ")
	io.WriteString(b, "é")
	// The last 10 bytes start inside a line, so the tail starts at the next.
	if got := b.String(); got != "last é" || b.total != 507 {
		t.Errorf("String() = %q, total %d", got, b.total)
	}
	// A cut inside a character doesn't hand back half of it.
	b = &tailBuffer{max: 3}
	io.WriteString(b, "éé")
	if b.String() != "é" {
		t.Errorf("String() = %q", b.String())
	}
	// Short output comes back whole.
	b = &tailBuffer{max: 10}
	io.WriteString(b, "a\nb")
	if b.String() != "a\nb" {
		t.Errorf("String() = %q", b.String())
	}
}

func TestLimitWriterStopsAtItsMax(t *testing.T) {
	var sink strings.Builder
	w := &limitWriter{w: &sink, max: 8}
	if _, err := w.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("67890")); err == nil || !w.over {
		t.Errorf("a write past the max went through: %v", err)
	}
	if sink.String() != "12345" || w.n != 5 {
		t.Errorf("wrote %q, n = %d", sink.String(), w.n)
	}
}
