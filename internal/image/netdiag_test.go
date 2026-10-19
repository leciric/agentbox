package image

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestTailWriterKeepsTheEndOfTheOutput(t *testing.T) {
	var log bytes.Buffer
	tw := &tailWriter{w: &log}
	for i := 1; i <= 40; i++ {
		_, _ = fmt.Fprintf(tw, "line %d\n", i)
	}
	err := tw.failure("tools.sh install", errors.New("exit status 1"))
	for _, want := range []string{"tools.sh install", "exit status 1", "line 40", "line 26"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "line 25\n") || strings.HasSuffix(err.Error(), "line 25") {
		t.Errorf("error should keep only the last %d lines: %v", tailLines, err)
	}
	if !strings.Contains(log.String(), "line 1\n") {
		t.Error("the log lost the start of the output")
	}
	if got := (&tailWriter{w: &log}).failure("x", errors.New("boom")).Error(); got != "x: boom" {
		t.Errorf("no output: %q", got)
	}
}

func TestHostNetworkNote(t *testing.T) {
	defer func(f func(context.Context, string) ([]string, error)) { lookupHost = f }(lookupHost)
	lookupHost = func(context.Context, string) ([]string, error) { return []string{"1.2.3.4"}, nil }
	if got := HostNetworkNote(t.Context()); !strings.Contains(got, "does resolve") {
		t.Errorf("host with network: %q", got)
	}
	lookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("no such host") }
	if got := HostNetworkNote(t.Context()); !strings.Contains(got, "can't resolve") {
		t.Errorf("host without network: %q", got)
	}
}
