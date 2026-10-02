package agent

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestWithImageCacheMirror(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		raw     string
		on      bool
		want    []string // registry-mirrors after; nil for none
		changed bool
	}{
		{"no file, on", "", true, []string{ImageCacheMirror}, true},
		{"no file, off", "", false, nil, false},
		{"already first", `{"registry-mirrors":["` + ImageCacheMirror + `"]}`, true, []string{ImageCacheMirror}, false},
		{"put first", `{"registry-mirrors":["https://m.example"]}`, true, []string{ImageCacheMirror, "https://m.example"}, true},
		{"moved to first", `{"registry-mirrors":["https://m.example","` + ImageCacheMirror + `/"]}`, true, []string{ImageCacheMirror, "https://m.example"}, true},
		{"off keeps others", `{"registry-mirrors":["` + ImageCacheMirror + `","https://m.example"]}`, false, []string{"https://m.example"}, true},
		{"off drops the key", `{"registry-mirrors":["` + ImageCacheMirror + `"],"debug":true}`, false, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := withImageCacheMirror([]byte(tc.raw), tc.on)
			if err != nil {
				t.Fatal(err)
			}
			if changed != tc.changed {
				t.Errorf("changed = %t, want %t", changed, tc.changed)
			}
			if !changed {
				return
			}
			var config struct {
				Mirrors []string `json:"registry-mirrors"`
			}
			if err := json.Unmarshal(out, &config); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(config.Mirrors, tc.want) {
				t.Errorf("registry-mirrors = %q, want %q", config.Mirrors, tc.want)
			}
		})
	}
}

func TestWithImageCacheMirrorLeavesAFileThatIsntJSONAlone(t *testing.T) {
	t.Parallel()
	if _, _, err := withImageCacheMirror([]byte("{not json"), true); err == nil {
		t.Error("no error for a daemon.json that isn't JSON")
	}
}
