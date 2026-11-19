package iossim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// IsProject reports whether a repository builds an iOS app: an Xcode project
// or workspace, or a React Native or Expo app, at the root or in a folder of
// a monorepo (like apps/mobile).
func IsProject(root string) bool {
	return hasIOSApp(root, 0)
}

var skipDirs = map[string]bool{"node_modules": true, "Pods": true, "build": true, "DerivedData": true, "vendor": true}

func hasIOSApp(dir string, depth int) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() && (strings.HasSuffix(name, ".xcodeproj") || strings.HasSuffix(name, ".xcworkspace")) {
			return true
		}
		if !e.IsDir() && name == "package.json" && isReactNativeApp(filepath.Join(dir, name)) {
			return true
		}
	}
	if depth >= 3 {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !skipDirs[e.Name()] && !strings.HasPrefix(e.Name(), ".") && hasIOSApp(filepath.Join(dir, e.Name()), depth+1) {
			return true
		}
	}
	return false
}

// isReactNativeApp reports whether a package.json depends on React Native or
// Expo, as an app does; libraries list them as dev or peer dependencies.
func isReactNativeApp(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	_, rn := pkg.Dependencies["react-native"]
	_, expo := pkg.Dependencies["expo"]
	return rn || expo
}
