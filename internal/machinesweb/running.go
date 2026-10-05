package machinesweb

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A running serve writes its address to a file, so that `agentbox machines
// mcp`'s view_url can link to it, and start one when there's none.

// RunningFile is that file, in AgentBox's data directory.
func RunningFile(data string) string { return filepath.Join(data, "machines", "serve.json") }

type running struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

// WriteRunning says serve is at url, and returns what removes it again.
func WriteRunning(path, url string) (remove func(), err error) {
	data, _ := json.Marshal(running{URL: url, PID: os.Getpid()})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return func() {
		// Only its own: another serve may have started since.
		if r, err := readRunning(path); err == nil && r.PID == os.Getpid() {
			_ = os.Remove(path)
		}
	}, nil
}

func readRunning(path string) (running, error) {
	var r running
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	return r, err
}

// Running is the address of the serve that wrote path, when it answers.
func Running(ctx context.Context, path string) (string, bool) {
	r, err := readRunning(path)
	if err != nil || !strings.HasPrefix(r.URL, "http://") {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(r.URL, "/")+"/api/machines", nil)
	if err != nil {
		return "", false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	_ = res.Body.Close()
	return r.URL, res.StatusCode == http.StatusOK
}
