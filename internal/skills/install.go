package skills

import (
	"archive/tar"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"agentbox/internal/state"
)

// Installed is one skill as it goes into an agent: its folder's name and files.
type Installed struct {
	Name  string
	Files []state.SkillFile
}

// Manifest is the file, under a HOME, listing the skills AgentBox put there
// last time, one name a line. Installing again removes those first, so a
// skill turned off or deleted leaves every agent, while skills the agent (or
// its repository) made itself are never touched.
const Manifest = ".config/agentbox/skills"

// Tar packs skills as <name>/<path> entries, with the manifest's new content
// as the entry named after it, for InstallScript to unpack inside an agent.
func Tar(list []Installed) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, sk := range list {
		for _, f := range sk.Files {
			mode := int64(f.Mode & 0o777)
			if mode == 0 {
				mode = 0o644
			}
			if err := tw.WriteHeader(&tar.Header{Name: sk.Name + "/" + f.Path, Mode: mode | 0o600, Size: int64(len(f.Content)), Typeflag: tar.TypeReg}); err != nil {
				return nil, err
			}
			if _, err := tw.Write(f.Content); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// InstallScript installs the skills Tar packed, read from stdin, into the HOME
// given as $1, owned by $2 (uid:gid). It runs as root inside an agent. Names
// are ValidateName's, so they are safe as words in it.
const InstallScript = `set -e
home="$1"; owner="$2"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
tar -x -C "$stage" --no-same-owner
manifest="$home/` + Manifest + `"
for rel in ` + ".claude/skills .agents/skills" + `; do
	dir="$home/$rel"
	mkdir -p "$dir"
	if [ -f "$manifest" ]; then
		while read -r old; do if [ -n "$old" ]; then rm -rf "$dir/$old"; fi; done < "$manifest"
	fi
	for sk in "$stage"/*/; do
		[ -d "$sk" ] || continue
		n=$(basename "$sk")
		rm -rf "$dir/$n"
		cp -R "$sk" "$dir/$n"
	done
	chown -R "$owner" "$home/${rel%%/*}"
done
mkdir -p "$(dirname "$manifest")"
(cd "$stage" && ls -1) > "$manifest"
chown "$owner" "$manifest" "$(dirname "$manifest")"
`

// InstallLocal does what InstallScript does, on this machine: for a lead,
// whose HOME is a folder of the daemon's.
func InstallLocal(home string, list []Installed) error {
	manifest := filepath.Join(home, filepath.FromSlash(Manifest))
	var old []string
	if data, err := os.ReadFile(manifest); err == nil {
		old = strings.Fields(string(data))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var names []string
	for _, rel := range InstallDirs {
		dir := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		for _, n := range old {
			if ValidateName(n) == nil {
				if err := os.RemoveAll(filepath.Join(dir, n)); err != nil {
					return err
				}
			}
		}
		for _, sk := range list {
			target := filepath.Join(dir, sk.Name)
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			for _, f := range sk.Files {
				p := filepath.Join(target, filepath.FromSlash(f.Path))
				if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
					return err
				}
				mode := os.FileMode(f.Mode&0o777) | 0o600
				if err := os.WriteFile(p, f.Content, mode); err != nil {
					return err
				}
			}
		}
	}
	for _, sk := range list {
		names = append(names, sk.Name)
	}
	if err := os.MkdirAll(filepath.Dir(manifest), 0o700); err != nil {
		return err
	}
	content := strings.Join(names, "\n")
	if content != "" {
		content += "\n"
	}
	return os.WriteFile(manifest, []byte(content), 0o600)
}
