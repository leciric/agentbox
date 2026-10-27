package backup

import (
	"database/sql"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The restore script is the one restore path that works on a machine where
// AgentBox is not installed, so it has to be tested as it will be run: a
// backup taken here, then bash with XDG_CONFIG_HOME and XDG_DATA_HOME in its
// environment, and the files it leaves behind.
func TestRestoreScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "restore-agentbox.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the script this test runs is missing: %v", err)
	}

	dir, err := Create(installation(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdgConfig := filepath.Join(root, "config")
	xdgData := filepath.Join(root, "data")
	for _, d := range []string{home, xdgConfig, xdgData} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(xdgConfig, "agentbox")
	agentboxData := filepath.Join(xdgData, "agentbox")

	restore := func(args ...string) (string, error) {
		cmd := exec.Command(bash, append([]string{script}, args...)...)
		// A fresh environment, not this process's with an override: a
		// duplicate variable's first value wins, and the first XDG_CONFIG_HOME
		// here could be the real one.
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + home,
			"XDG_CONFIG_HOME=" + xdgConfig,
			"XDG_DATA_HOME=" + xdgData,
		}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// A daemon's socket is refused, and --force is the way through. This test
	// installs files into a fresh directory, so a socket is all it needs.
	sock := filepath.Join(agentboxData, "run", "agentbox.sock")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	out, err := restore(dir)
	if err == nil {
		t.Fatalf("restored while the daemon's socket was there:\n%s", out)
	}
	if !strings.Contains(out, "daemon is running") {
		t.Errorf("refusal = %q, want it to name the running daemon", out)
	}
	if _, err := os.Stat(filepath.Join(agentboxData, "state.db")); !os.IsNotExist(err) {
		t.Errorf("a refused restore wrote state.db anyway: %v", err)
	}

	out, err = restore(dir, "--force")
	if err != nil {
		t.Fatalf("--force: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Restored ") {
		t.Errorf("output = %q, want it to say what it restored", out)
	}
	// A closed listener unlinks its socket, and the script refuses on a socket
	// that is there whether or not anybody is listening.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if out, err := restore(dir); err != nil {
		t.Fatalf("a second restore, now that the socket is gone: %v\n%s", err, out)
	}

	// The row the backup was taken with is in the restored database, read the
	// way any other client would read it.
	db, err := sql.Open("sqlite", "file:"+filepath.Join(agentboxData, "state.db")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var display string
	if err := db.QueryRow("SELECT display_name FROM projects WHERE name = 'pawly'").Scan(&display); err != nil {
		t.Fatalf("the restored database has no pawly project: %v", err)
	}
	if display != "Pawly" {
		t.Errorf("restored display name = %q, want Pawly", display)
	}

	for _, path := range []string{filepath.Join(config, "secrets.key"), filepath.Join(config, "credentials", "claude", "work.json")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s is mode %o, want 600", path, info.Mode().Perm())
		}
	}
	for _, name := range []string{"state.db-wal", "state.db-shm"} {
		if _, err := os.Stat(filepath.Join(agentboxData, name)); !os.IsNotExist(err) {
			t.Errorf("the restore left %s beside the new database", name)
		}
	}

	// The second restore replaced a database that was already there, so what
	// it replaced is saved beside it.
	saved, err := filepath.Glob(filepath.Join(agentboxData, "agentbox.before-restore-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) == 0 {
		t.Fatal("a restore over an existing database saved nothing")
	}
	if _, err := os.Stat(filepath.Join(saved[0], "state.db")); err != nil {
		t.Errorf("the saved directory has no state.db: %v", err)
	}
}
