package iossim

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsProject(t *testing.T) {
	repo := func(files map[string]string) string {
		root := t.TempDir()
		for name, data := range files {
			path := filepath.Join(root, name)
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			if data == "/" {
				_ = os.MkdirAll(path, 0o755)
				continue
			}
			_ = os.WriteFile(path, []byte(data), 0o644)
		}
		return root
	}
	for name, c := range map[string]struct {
		files map[string]string
		want  bool
	}{
		"an Xcode project":        {map[string]string{"Pawly.xcodeproj/project.pbxproj": "x"}, true},
		"a workspace in apps/ios": {map[string]string{"apps/ios/Pawly.xcworkspace": "/"}, true},
		"an Expo app":             {map[string]string{"apps/mobile/package.json": `{"dependencies":{"expo":"~54.0.0"}}`}, true},
		"a library of RN":         {map[string]string{"package.json": `{"peerDependencies":{"react-native":"*"}}`}, false},
		"a web app":               {map[string]string{"package.json": `{"dependencies":{"react":"19"}}`, "src/main.tsx": ""}, false},
		"pods aren't the app":     {map[string]string{"node_modules/x/X.xcodeproj/p": "x"}, false},
	} {
		if got := IsProject(repo(c.files)); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
