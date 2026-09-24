// Package android finds what agents need to run Android emulators: an Android
// SDK on the host with the emulator, the platform tools and a system image,
// which agents share read-only. It also recognizes Android projects.
package android

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// DefaultImage is what to install when there's no system image yet.
const DefaultImage = "system-images;android-35;google_apis;x86_64"

// InstallHint is the sdkmanager command that installs what an emulator needs.
var InstallHint = fmt.Sprintf(`sdkmanager "emulator" "platform-tools" %q`, DefaultImage)

// Image is an installed system image.
type Image struct {
	ID  string // as sdkmanager names it, like system-images;android-35;google_apis;x86_64
	API int    // 35 (android-36.1 is 36)
	Tag string // google_apis
	ABI string // x86_64
}

// Dir is the image's directory, relative to the SDK.
func (i Image) Dir() string { return strings.ReplaceAll(i.ID, ";", "/") }

type SDK struct {
	Path   string
	Images []Image // images an x86_64 host can run, best first
}

// Candidates are the places an Android SDK usually is, in order of preference.
func Candidates(getenv func(string) string, home string) []string {
	var dirs []string
	for _, name := range []string{"AGENTBOX_ANDROID_SDK", "ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if dir := getenv(name); dir != "" {
			dirs = append(dirs, dir)
		}
	}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, "Android", "Sdk"))
	}
	return dirs
}

// FindSDK returns the first candidate with an emulator, the platform tools and
// a complete x86_64 system image. Its error says what's missing.
func FindSDK(candidates []string) (SDK, error) {
	var problems []string
	for _, dir := range candidates {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		missing := []string{}
		if !isFile(filepath.Join(dir, "emulator", "emulator")) {
			missing = append(missing, "the emulator")
		}
		if !isFile(filepath.Join(dir, "platform-tools", "adb")) {
			missing = append(missing, "the platform tools")
		}
		images := Images(dir)
		if len(images) == 0 {
			missing = append(missing, "an x86_64 system image")
		}
		if len(missing) == 0 {
			return SDK{Path: dir, Images: images}, nil
		}
		problems = append(problems, fmt.Sprintf("the Android SDK in %s is missing %s", dir, strings.Join(missing, " and ")))
	}
	if len(problems) == 0 {
		return SDK{}, fmt.Errorf("no Android SDK found (looked in %s): install Android Studio or the SDK command-line tools, then run %s, or set AGENTBOX_ANDROID_SDK",
			strings.Join(candidates, ", "), InstallHint)
	}
	return SDK{}, fmt.Errorf("%s: run %s", strings.Join(problems, "; "), InstallHint)
}

// Images lists the complete x86_64 system images in an SDK, best first: the
// newest API, then google_apis (which allows adb root) before Play Store images.
func Images(sdk string) []Image {
	platforms, _ := os.ReadDir(filepath.Join(sdk, "system-images"))
	var images []Image
	for _, platform := range platforms {
		api, ok := apiLevel(platform.Name())
		if !ok {
			continue
		}
		tags, _ := os.ReadDir(filepath.Join(sdk, "system-images", platform.Name()))
		for _, tag := range tags {
			dir := filepath.Join(sdk, "system-images", platform.Name(), tag.Name(), "x86_64")
			// An interrupted download leaves the directory without its disk images.
			if !isFile(filepath.Join(dir, "system.img")) || !isFile(filepath.Join(dir, "kernel-ranchu")) {
				continue
			}
			images = append(images, Image{
				ID:  strings.Join([]string{"system-images", platform.Name(), tag.Name(), "x86_64"}, ";"),
				API: api, Tag: tag.Name(), ABI: "x86_64",
			})
		}
	}
	slices.SortStableFunc(images, func(a, b Image) int {
		if a.API != b.API {
			return b.API - a.API
		}
		if ra, rb := tagRank(a.Tag), tagRank(b.Tag); ra != rb {
			return ra - rb
		}
		return strings.Compare(a.ID, b.ID)
	})
	return images
}

// Find returns the installed image with the given ID, or the best one when id is empty.
func (s SDK) Find(id string) (Image, error) {
	if len(s.Images) == 0 {
		return Image{}, errors.New("no system image installed: run " + InstallHint)
	}
	if id == "" {
		return s.Images[0], nil
	}
	for _, image := range s.Images {
		if image.ID == id {
			return image, nil
		}
	}
	return Image{}, fmt.Errorf("%s isn't installed in %s: run sdkmanager %q", id, s.Path, id)
}

func apiLevel(platform string) (int, bool) {
	version, ok := strings.CutPrefix(platform, "android-")
	if !ok {
		return 0, false
	}
	major, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(major)
	return n, err == nil
}

func tagRank(tag string) int {
	switch tag {
	case "google_apis":
		return 0
	case "default", "aosp_atd", "google_atd":
		return 1
	case "google_apis_playstore":
		return 2
	}
	return 3
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// IsProject reports whether a repository builds an Android app: a Gradle
// module with the Android plugin, an Android manifest, or a React Native or
// Expo app, at the root or in a folder of a monorepo (like apps/mobile).
func IsProject(root string) bool {
	return hasAndroidModule(root, 0)
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "build": true, ".gradle": true, "dist": true, "vendor": true}

func hasAndroidModule(dir string, depth int) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && name == "AndroidManifest.xml" {
			return true
		}
		if !e.IsDir() && (name == "build.gradle" || name == "build.gradle.kts") {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err == nil && strings.Contains(string(data), "android.application") {
				return true
			}
		}
		if !e.IsDir() && name == "package.json" && isReactNativeApp(filepath.Join(dir, name)) {
			return true
		}
	}
	if depth >= 3 {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && !skipDirs[e.Name()] && !strings.HasPrefix(e.Name(), ".") && hasAndroidModule(filepath.Join(dir, e.Name()), depth+1) {
			return true
		}
	}
	return false
}

// isReactNativeApp reports whether a package.json depends on React Native or
// Expo. Libraries list them as dev or peer dependencies, so those don't count.
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
