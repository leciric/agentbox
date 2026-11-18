package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// VMBoot is what the VM's supervisor on the host knows of this boot, which
// it writes to boot.json among the VM's disks (hostos.VMDisks) before each:
// when it started, and, when the VMs before it ended without being stopped
// (a crash loop), Safe with Why, and when they did. The daemon reads it as it
// starts (ReadVMBoot): on Safe it carries nothing on by itself.
//
// The supervisor's side is agent-829's (fix-vm-memory-crash-loop); this is
// the same type, which whichever of the two lands second keeps one copy of.
type VMBoot struct {
	Started time.Time `json:"started"`
	// Safe says this boot is after a crash: nothing resumes by itself.
	Safe bool `json:"safe,omitempty"`
	// Why is the supervisor's account of it, for the user.
	Why string `json:"why,omitempty"`
	// Crashes are when the VMs before this one ended unexpectedly.
	Crashes []time.Time `json:"crashes,omitempty"`
}

// vmBootFile is VMBoot's file in the VM's disks directory.
const vmBootFile = "boot.json"

// ReadVMBoot reads dir's boot.json. With no dir or no file it is the zero
// VMBoot, not Safe, and no error.
func ReadVMBoot(dir string) (VMBoot, error) {
	var b VMBoot
	if dir == "" {
		return b, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, vmBootFile))
	if errors.Is(err, fs.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	return b, json.Unmarshal(data, &b)
}
