package trace

import (
	"encoding/json"
	"fmt"
	"os"
)

// rotating is an append-only file that, before a write would take it past
// MaxBytes, is moved to path.1 (path.1 to path.2, and so on, the oldest
// dropped) and started again. A write is never split between two files, so a
// line is whole wherever it is. Only the recorder's goroutine uses it.
type rotating struct {
	Limits
	path string
	f    *os.File
	size int64
}

func (r *rotating) write(b []byte) {
	if r.f == nil && !r.open() {
		return
	}
	if r.size > 0 && r.size+int64(len(b)) > r.MaxBytes {
		r.rotate()
		if r.f == nil && !r.open() {
			return
		}
	}
	// A failed write (a full disk) loses these spans; tracing never fails
	// what it measures.
	n, _ := r.f.Write(b)
	r.size += int64(n)
}

func (r *rotating) open() bool {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return false
	}
	r.f, r.size = f, info.Size()
	return true
}

func (r *rotating) rotate() {
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	if r.MaxFiles <= 0 {
		_ = os.Remove(r.path)
		return
	}
	_ = os.Remove(Backup(r.path, r.MaxFiles))
	for i := r.MaxFiles - 1; i >= 1; i-- {
		_ = os.Rename(Backup(r.path, i), Backup(r.path, i+1))
	}
	_ = os.Rename(r.path, Backup(r.path, 1))
}

func (r *rotating) close() error {
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// Backup is the path of a file's i-th backup: path.1 is the newest.
func Backup(path string, i int) string { return fmt.Sprintf("%s.%d", path, i) }

func marshal(s Span) ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
