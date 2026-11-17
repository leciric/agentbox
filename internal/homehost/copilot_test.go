package homehost

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// npm's `copilot` is a Node loader that waits on its platform package's
// binary: the Home chat runs the binary itself, so stopping the chat stops
// Copilot. Any other install is the binary already, and is run as found.
func TestCopilotNativeSkipsNpmsLoader(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm installs a .cmd shim on Windows, which is run as found")
	}
	dir := t.TempDir()
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	loader := filepath.Join(dir, "lib", "node_modules", "@github", "copilot", "npm-loader.js")
	native := filepath.Join(dir, "lib", "node_modules", "@github", "copilot", "node_modules", "@github", "copilot-"+runtime.GOOS+"-"+arch, "copilot")
	for _, f := range []string{loader, native} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "bin", "copilot")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loader, bin); err != nil {
		t.Fatal(err)
	}
	if got := copilotNative(bin); got != native {
		t.Errorf("copilotNative(npm's loader) = %s, want %s", got, native)
	}
	if got := copilotNative(native); got != native {
		t.Errorf("copilotNative(the binary) = %s", got)
	}
	if err := os.Remove(native); err != nil {
		t.Fatal(err)
	}
	if got := copilotNative(bin); got != bin {
		t.Errorf("copilotNative(a loader with no binary beside it) = %s, want it kept", got)
	}
}
