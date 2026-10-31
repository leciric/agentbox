package skills

import (
	"embed"
	"io/fs"
	"path"
	"slices"

	"agentbox/internal/state"
)

// Built-in skills ship with AgentBox, for the Home chat: what it needs to
// know to do a job AgentBox itself hands it, such as Setup asking it to fix a
// failing check (troubleshooting-setup). They aren't in state.db, so the
// skills editor neither lists nor changes them, and a stored skill of the same
// name takes their place.
//
//go:embed builtin
var builtin embed.FS

// Builtin is every built-in skill, with its files.
func Builtin() []Installed {
	dirs, err := fs.ReadDir(builtin, "builtin")
	if err != nil {
		panic(err) // embedded: it is there
	}
	var out []Installed
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		sk := Installed{Name: d.Name()}
		root := path.Join("builtin", d.Name())
		err := fs.WalkDir(builtin, root, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			b, err := builtin.ReadFile(p)
			if err != nil {
				return err
			}
			sk.Files = append(sk.Files, state.SkillFile{Path: p[len(root)+1:], Mode: 0o644, Content: b})
			return nil
		})
		if err != nil {
			panic(err)
		}
		out = append(out, sk)
	}
	return out
}

// WithBuiltin is list with every built-in skill it doesn't have one of the
// same name of.
func WithBuiltin(list []Installed) []Installed {
	for _, b := range Builtin() {
		if !slices.ContainsFunc(list, func(sk Installed) bool { return sk.Name == b.Name }) {
			list = append(list, b)
		}
	}
	return list
}
