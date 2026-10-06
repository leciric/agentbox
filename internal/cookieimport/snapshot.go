package cookieimport

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// openSnapshot opens a read-only copy of a cookie database. A running browser
// holds a lock and may have unflushed WAL pages, so rather than open the live
// file we ask SQLite to VACUUM INTO a throwaway copy — a consistent snapshot
// that leaves the browser's own file untouched — and read that. The copy goes
// to a temp file removed by cleanup.
func openSnapshot(path string) (*sql.DB, func(), error) {
	tmp, err := os.CreateTemp("", "agentbox-cookies-*.sqlite")
	if err != nil {
		return nil, nil, err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath) // VACUUM INTO needs the target not to exist.
	rm := func() {
		_ = os.Remove(tmpPath)
		_ = os.Remove(tmpPath + "-wal")
		_ = os.Remove(tmpPath + "-shm")
	}

	src, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1&_pragma=busy_timeout(3000)")
	if err != nil {
		rm()
		return nil, nil, err
	}
	// VACUUM INTO wants a plain absolute path, single-quoted.
	abs, err := filepath.Abs(tmpPath)
	if err != nil {
		abs = tmpPath
	}
	_, err = src.Exec("VACUUM INTO '" + abs + "'")
	_ = src.Close()
	if err != nil {
		rm()
		return nil, nil, fmt.Errorf("snapshotting the cookie database: %w", err)
	}

	dst, err := sql.Open("sqlite", "file:"+tmpPath+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		rm()
		return nil, nil, err
	}
	return dst, func() { _ = dst.Close(); rm() }, nil
}
