package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/hostos"
)

// In the Cloud Hypervisor VM, the SDK is looked for in the host's home, which
// the VM shares, and not in the VM user's own; one the front end found
// outside the share is named as out of the VM's sight.
func TestFindAndroidSDKInTheLinuxHostsVM(t *testing.T) {
	share, vmHome := t.TempDir(), t.TempDir()
	for _, name := range []string{"AGENTBOX_ANDROID_SDK", "ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", vmHome)
	t.Setenv(hostos.Env, hostos.Linux)
	t.Setenv(hostos.HomeEnv, share)
	sdk := filepath.Join(share, "Android", "Sdk")
	for _, f := range []string{"emulator/emulator", "platform-tools/adb", "system-images/android-34/google_apis/x86_64/system.img", "system-images/android-34/google_apis/x86_64/kernel-ranchu"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(sdk, f)), 0o755)
		_ = os.WriteFile(filepath.Join(sdk, f), nil, 0o755)
	}
	if got, err := findAndroidSDK(); err != nil || got.Path != sdk {
		t.Errorf("the SDK in the shared home: %+v, %v", got, err)
	}
	_ = os.RemoveAll(sdk)
	t.Setenv("AGENTBOX_ANDROID_SDK", "/opt/android-sdk")
	if _, err := findAndroidSDK(); err == nil || !strings.Contains(err.Error(), "/opt/android-sdk is outside your home directory") {
		t.Errorf("an SDK outside the share: %v", err)
	}
}
