package hub

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestHubImportsOnlyHubapi keeps the hub a program of its own (D44): of the
// rest of AgentBox's module it imports only the protocol, hubapi. That
// internal/ can't import the hub, Go's internal/ rule sees to.
func TestHubImportsOnlyHubapi(t *testing.T) {
	root := filepath.Join("..", "..") // hub/
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if (p == "agentbox" || strings.HasPrefix(p, "agentbox/")) && p != "agentbox/hubapi" && !strings.HasPrefix(p, "agentbox/hub/") {
				t.Errorf("%s imports %s: the hub imports only agentbox/hubapi of the rest of AgentBox", path, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
