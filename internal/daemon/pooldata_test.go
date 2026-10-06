package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newPoolData(t *testing.T) poolData {
	t.Helper()
	root := t.TempDir()
	p := poolData{
		data: filepath.Join(root, "data"), mount: filepath.Join(root, "pool"),
		copyDir: cpDir, logf: func(string, ...any) {},
	}
	for _, d := range []string{p.data, p.mount} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMoveDataToPoolCopiesDataAndStartsCachesAfresh(t *testing.T) {
	p := newPoolData(t)
	write(t, filepath.Join(p.data, "projects", "web", "notes.md"), "notes")
	write(t, filepath.Join(p.data, "tools", "bin", "claude"), "#!/bin/sh")
	if err := os.Symlink("claude", filepath.Join(p.data, "tools", "bin", "link")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(p.data, "package-cache", "go", "mod"), "big")
	write(t, filepath.Join(p.data, "state.db"), "state")

	aside, err := p.move(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range poolItems {
		src := filepath.Join(p.data, it.name)
		if target, err := os.Readlink(src); err != nil || target != filepath.Join(p.mount, it.name) {
			t.Errorf("%s links to %q (%v)", it.name, target, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(p.data, "projects", "web", "notes.md")); err != nil || string(b) != "notes" {
		t.Errorf("notes through the link: %q, %v", b, err)
	}
	if target, err := os.Readlink(filepath.Join(p.mount, "tools", "bin", "link")); err != nil || target != "claude" {
		t.Errorf("a symlink in tools copied as %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(p.mount, "package-cache", "go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the package cache was copied: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(p.data, "state.db")); err != nil || !info.Mode().IsRegular() {
		t.Errorf("state.db moved: %v", err)
	}
	// What was there before is left aside, for dropMovedData.
	if len(aside) != 3 {
		t.Errorf("aside = %v, want package-cache, tools and projects", aside)
	}
	for _, dir := range aside {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}

	// A second start finds them moved, and only the old copies to drop.
	again, err := p.move(context.Background())
	if err != nil || len(again) != 3 {
		t.Errorf("second move: %v, %v", again, err)
	}
	for _, dir := range again {
		_ = os.RemoveAll(dir)
	}
	if again, err := p.move(context.Background()); err != nil || len(again) != 0 {
		t.Errorf("third move: %v, %v", again, err)
	}
}

func TestMoveDataToPoolCarriesOnFromAnInterruptedStart(t *testing.T) {
	p := newPoolData(t)
	// Renamed aside, but no symlink yet: the copy on the pool is whole.
	write(t, filepath.Join(p.data, ".projects.moved", "web", "notes.md"), "old")
	write(t, filepath.Join(p.mount, "projects", "web", "notes.md"), "notes")
	// A copy cut short, beside data that never moved.
	write(t, filepath.Join(p.mount, "tools.moving", "half"), "x")
	write(t, filepath.Join(p.data, "tools", "whole"), "x")

	aside, err := p.move(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(p.data, "projects", "web", "notes.md")); err != nil || string(b) != "notes" {
		t.Errorf("projects: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(p.data, "tools", "whole")); err != nil {
		t.Errorf("tools: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.data, "tools", "half")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the cut-short copy is used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.mount, "tools.moving")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("tools.moving left: %v", err)
	}
	if len(aside) != 2 {
		t.Errorf("aside = %v", aside)
	}
}

func TestMoveDataToPoolLeavesADirectoryThatCantBeCopied(t *testing.T) {
	p := newPoolData(t)
	write(t, filepath.Join(p.data, "projects", "notes.md"), "notes")
	p.copyDir = func(context.Context, string, string) error { return errors.New("no space left on device") }
	if _, err := p.move(context.Background()); err == nil {
		t.Fatal("no error")
	}
	info, err := os.Lstat(filepath.Join(p.data, "projects"))
	if err != nil || !info.IsDir() {
		t.Errorf("projects isn't where it was: %v", err)
	}
}

func TestFitCacheMax(t *testing.T) {
	const gib = int64(1 << 30)
	for _, c := range []struct {
		name                    string
		chosen, total, floor    int64
		wantMax, wantFitDefault int64
	}{
		{"the default on a big disk", 0, 320 * gib, 10 * gib, 20 * gib, 20 * gib},
		{"the default on the VM's system disk", 0, 20 * gib, 1 * gib, 2560 << 20, 2560 << 20},
		{"the default on a tiny disk", 0, 4 * gib, gib / 5, gib, gib},
		{"a choice that fits", 50 * gib, 320 * gib, 10 * gib, 50 * gib, 20 * gib},
		{"a choice bigger than the disk", 500 * gib, 100 * gib, 10 * gib, 90 * gib, 100 * gib / 8},
		{"a disk that can't be measured", 0, 0, 0, 20 * gib, 20 * gib},
	} {
		maxBytes, def := fitCacheMax(c.chosen, 20*gib, c.total, c.floor)
		if maxBytes != c.wantMax || def != c.wantFitDefault {
			t.Errorf("%s: fitCacheMax = %d, %d; want %d, %d", c.name, maxBytes, def, c.wantMax, c.wantFitDefault)
		}
	}
}
