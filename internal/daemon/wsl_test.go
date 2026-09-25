package daemon

import (
	"strings"
	"testing"
)

func TestCheckProjectDisk(t *testing.T) {
	for _, tc := range []struct {
		root   string
		wsl    bool
		refuse bool
	}{
		{"/mnt/c/Users/ana/app", true, true},
		{"/mnt/d", true, true},
		{"/home/ana/src/app", true, false},
		{"/mnt/data/app", true, false},
		{"/mnt/c/Users/ana/app", false, false},
	} {
		err := checkProjectDisk(tc.root, tc.wsl)
		if (err != nil) != tc.refuse {
			t.Errorf("checkProjectDisk(%q, %v) = %v", tc.root, tc.wsl, err)
		}
		if err != nil && !strings.Contains(err.Error(), "git clone") {
			t.Errorf("the refusal doesn't say what to do: %v", err)
		}
	}
}
