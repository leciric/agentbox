package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"agentbox/internal/state"
)

// tailMark heads FileTail's answer from inside a machine, after whatever the
// login shell printed first.
const tailMark = "\x01agentbox-tail "

// FileTail reads a piece of a file where a's AI tool runs — in its machine,
// or on this one for a lead, which has none: the limit bytes that end at
// offset before, or the file's last limit bytes when before is 0 or past
// its end. It answers with where the piece starts and the file's size. It is
// how a background command's output file is read (chat.Manager.ReadOutput).
func (m *Manager) FileTail(ctx context.Context, a state.Agent, path string, before, limit int64) ([]byte, int64, int64, error) {
	if limit <= 0 {
		return nil, 0, 0, fmt.Errorf("limit must be positive")
	}
	if a.IsLead() {
		return localTail(path, before, limit)
	}
	script := fmt.Sprintf(`f=%s; s=$(stat -c %%s -- "$f") || exit 3; e=%d; { [ "$e" -gt 0 ] && [ "$e" -lt "$s" ]; } || e=$s; b=$((e-%d)); [ "$b" -lt 0 ] && b=0; printf '\001agentbox-tail %%s %%s\n' "$s" "$b"; tail -c +$((b+1)) -- "$f" | head -c $((e-b))`,
		shellQuote(path), before, limit)
	var out, stderr bytes.Buffer
	if err := m.Exec(ctx, a, script, nil, &out, &stderr); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, 0, 0, fmt.Errorf("%s", msg)
		}
		return nil, 0, 0, err
	}
	return parseTail(out.Bytes())
}

// parseTail splits what FileTail's script printed into the piece and its place.
func parseTail(out []byte) ([]byte, int64, int64, error) {
	i := bytes.Index(out, []byte(tailMark))
	if i < 0 {
		return nil, 0, 0, fmt.Errorf("the machine didn't answer with the file")
	}
	head, data, ok := bytes.Cut(out[i+len(tailMark):], []byte("\n"))
	fields := strings.Fields(string(head))
	if !ok || len(fields) != 2 {
		return nil, 0, 0, fmt.Errorf("the machine didn't answer with the file")
	}
	size, err1 := strconv.ParseInt(fields[0], 10, 64)
	start, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		return nil, 0, 0, fmt.Errorf("the machine didn't answer with the file")
	}
	return data, start, size, nil
}

func localTail(path string, before, limit int64) ([]byte, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, 0, fmt.Errorf("%s isn't a file", path)
	}
	size := info.Size()
	end := size
	if before > 0 && before < size {
		end = before
	}
	start := max(end-limit, 0)
	data := make([]byte, end-start)
	if _, err := f.ReadAt(data, start); err != nil && err != io.EOF {
		return nil, 0, 0, err
	}
	return data, start, size, nil
}
