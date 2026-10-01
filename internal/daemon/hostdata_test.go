package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADaemonRefusesTheHostsDataInTheVM(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	host := filepath.Join(root, "home", "dev") // shared by the host
	shared := func(dir string) bool { return strings.HasPrefix(dir+"/", host+"/") }
	for _, dir := range []string{host, filepath.Join(root, "home", "dev.linux")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	data := filepath.Join(host, ".agentbox") // not made yet
	if err := checkDataDir(data, shared); err == nil || !strings.Contains(err.Error(), "the host's AgentBox data") {
		t.Errorf("checkDataDir(%s) on the shared home = %v, want it refused", data, err)
	}
	own := filepath.Join(root, "home", "dev.linux", ".agentbox")
	if err := checkDataDir(own, shared); err != nil {
		t.Errorf("checkDataDir(%s) on the VM's own disk = %v", own, err)
	}
}
