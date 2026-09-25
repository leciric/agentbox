package android

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeImage(t *testing.T, sdk, platform, tag string) {
	dir := filepath.Join(sdk, "system-images", platform, tag, "x86_64")
	touch(t, filepath.Join(dir, "system.img"), "")
	touch(t, filepath.Join(dir, "kernel-ranchu"), "")
}

func TestFindSDKPicksACompleteSDKAndOrdersImages(t *testing.T) {
	empty := t.TempDir()
	sdk := t.TempDir()
	touch(t, filepath.Join(sdk, "emulator", "emulator"), "")
	touch(t, filepath.Join(sdk, "platform-tools", "adb"), "")
	fakeImage(t, sdk, "android-35", "google_apis")
	fakeImage(t, sdk, "android-36.1", "google_apis_playstore")
	fakeImage(t, sdk, "android-36", "google_apis")
	// Incomplete downloads and ARM images don't count.
	_ = os.MkdirAll(filepath.Join(sdk, "system-images", "android-37.0", "google_apis_playstore", "x86_64"), 0o755)
	touch(t, filepath.Join(sdk, "system-images", "android-34", "google_apis", "arm64-v8a", "system.img"), "")

	got, err := FindSDK([]string{filepath.Join(empty, "missing"), empty, sdk})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != sdk {
		t.Errorf("Path = %s, want %s", got.Path, sdk)
	}
	var ids []string
	for _, image := range got.Images {
		ids = append(ids, image.ID)
	}
	want := "system-images;android-36;google_apis;x86_64 system-images;android-36.1;google_apis_playstore;x86_64 system-images;android-35;google_apis;x86_64"
	if strings.Join(ids, " ") != want {
		t.Errorf("Images = %v, want %s", ids, want)
	}
	if image, _ := got.Find(""); image.API != 36 || image.Dir() != "system-images/android-36/google_apis/x86_64" {
		t.Errorf("Find(\"\") = %+v", image)
	}
	if _, err := got.Find("system-images;android-30;default;x86_64"); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("Find(missing) = %v", err)
	}
}

func TestFindSDKSaysWhatIsMissing(t *testing.T) {
	sdk := t.TempDir()
	touch(t, filepath.Join(sdk, "platform-tools", "adb"), "")
	_, err := FindSDK([]string{sdk})
	want := "the Android SDK in " + sdk + " is missing the emulator and an x86_64 system image: run sdkmanager"
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("FindSDK = %v, want it to start with %q", err, want)
	}
	if _, err := FindSDK([]string{filepath.Join(sdk, "nope")}); err == nil || !strings.Contains(err.Error(), "no Android SDK found") {
		t.Errorf("FindSDK(nothing) = %v", err)
	}
}

func TestCandidates(t *testing.T) {
	env := map[string]string{"ANDROID_HOME": "/sdk/a", "AGENTBOX_ANDROID_SDK": "/sdk/agentbox"}
	got := Candidates(func(k string) string { return env[k] }, "/home/u")
	if strings.Join(got, " ") != "/sdk/agentbox /sdk/a /home/u/Android/Sdk" {
		t.Errorf("Candidates = %v", got)
	}
}

func TestIsProject(t *testing.T) {
	cases := map[string]map[string]string{
		"gradle app":   {"android/app/build.gradle.kts": `plugins { alias(libs.plugins.android.application) }`},
		"groovy app":   {"app/build.gradle": `apply plugin: 'com.android.application'`},
		"manifest":     {"app/src/main/AndroidManifest.xml": `<manifest package="dev.example"/>`},
		"expo":         {"package.json": `{"dependencies": {"expo": "~53.0.0"}}`},
		"react native": {"package.json": `{"dependencies": {"react-native": "0.80.0"}}`},
		"expo monorepo": {
			"package.json":             `{"private": true, "devDependencies": {"turbo": "^2.5.0"}}`,
			"apps/api/package.json":    `{"dependencies": {"@nestjs/core": "^11.0.0"}}`,
			"apps/mobile/package.json": `{"dependencies": {"expo": "~54.0.33", "react-native": "0.81.5"}}`,
		},
	}
	for name, files := range cases {
		root := t.TempDir()
		for path, content := range files {
			touch(t, filepath.Join(root, path), content)
		}
		if !IsProject(root) {
			t.Errorf("%s: IsProject = false", name)
		}
	}
	web := t.TempDir()
	touch(t, filepath.Join(web, "package.json"), `{"dependencies": {"react": "19.0.0"}}`)
	touch(t, filepath.Join(web, "node_modules", "some-lib", "android", "build.gradle"), `apply plugin: 'com.android.application'`)
	if IsProject(web) {
		t.Error("a web app with an Android library in node_modules: IsProject = true")
	}
	lib := t.TempDir()
	touch(t, filepath.Join(lib, "packages", "ui", "package.json"), `{"peerDependencies": {"react-native": "*"}, "devDependencies": {"react-native": "0.81.5"}}`)
	if IsProject(lib) {
		t.Error("a React Native library (react-native only as a peer and dev dependency): IsProject = true")
	}
}
