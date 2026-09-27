package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentbox/internal/api"
	"agentbox/internal/image"
)

// The base the app shows says what the image has that it doesn't: organic's,
// saved from image 2026.09.21.1, misses Mesa and an older Claude Code. The
// base it replaced isn't compared: it is only there to go back to.
func TestBaseAPISaysWhatTheImageHasThatTheBaseDoesnt(t *testing.T) {
	t.Parallel()
	var specs, older []string
	for _, tool := range image.ToolsFor(image.Components{}) {
		specs = append(specs, tool.Spec)
		if tool.Name() == "claude" {
			older = append(older, "claude@0.0.1")
		} else {
			older = append(older, tool.Spec)
		}
	}
	config := func(version string, tools []string) string {
		return fmt.Sprintf(`{"config":{"user.agentbox.image-version":%q,"user.agentbox.tools-version":"0123456789ab","user.agentbox.tools":%q,"user.agentbox.saved-from":"hello-stack/agent-02","user.agentbox.saved-at":"2026-09-21T10:00:00Z"},"devices":{}}`,
			version, strings.Join(tools, " "))
	}
	root := t.TempDir()
	d := startTestDaemon(t, root, fmt.Sprintf(`case "$1" in
  list) echo '[{"name":"ab-hello-stack-base","status":"Stopped"}]' ;;
  query)
    case "$2" in
      */ab-hello-stack-base/snapshots|*/agentbox-base/snapshots) echo "[\"$2/ready\"]" ;;
      */snapshots) echo "Error: Instance not found" >&2; exit 1 ;;
      */ab-hello-stack-base) echo '%s' ;;
      */agentbox-base) echo '%s' ;;
      *) echo '{"config": {}, "devices": {}}' ;;
    esac ;;
esac
exit 0
`, config("2026.09.21.1", older), config(image.Version, specs)))
	ctx := context.Background()
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: d.fixtureRepo(t, "hello-stack")}); err != nil {
		t.Fatal(err)
	}

	base, ok, err := d.client.Base(ctx, "hello-stack")
	if err != nil || !ok {
		t.Fatalf("Base() = %+v, %v, %v", base, ok, err)
	}
	if base.Image != "2026.09.21.1" || base.Tools != "0123456789ab" {
		t.Errorf("Base() recorded image %q, tools %q", base.Image, base.Tools)
	}
	b := base.Behind
	if b == nil {
		t.Fatal("Base().Behind is nil for a base from an older image")
	}
	if b.ImageFrom != "2026.09.21.1" || b.ImageTo != image.Version {
		t.Errorf("image %q → %q", b.ImageFrom, b.ImageTo)
	}
	if len(b.Changes) == 0 || !strings.Contains(b.Changes[len(b.Changes)-1].What, "Mesa") {
		t.Errorf("changes = %+v, want Mesa among them", b.Changes)
	}
	if len(b.Tools) != 1 || b.Tools[0].Name != "claude" || b.Tools[0].From != "0.0.1" || b.Tools[0].To == "" {
		t.Errorf("tools = %+v, want claude alone moving on", b.Tools)
	}
}

func TestCatchUpNote(t *testing.T) {
	if note := catchUpNote("", nil, ""); note != "" {
		t.Errorf("nothing to catch up still says %q", note)
	}
	done := catchUpNote("image 2026.09.21.1 → 2026.09.27.1", nil, "apt output")
	if !strings.Contains(done, "caught this machine up") || !strings.Contains(done, "2026.09.27.1") || strings.Contains(done, "apt output") {
		t.Errorf("success note = %q", done)
	}
	failed := catchUpNote("claude 0.0.1 → 2.1.280", errors.New("tools.sh install: exit status 1"), "\nE: mise failed to fetch claude\n")
	for _, want := range []string{"failed: tools.sh install: exit status 1", "E: mise failed to fetch claude", "still show as behind"} {
		if !strings.Contains(failed, want) {
			t.Errorf("failure note is missing %q:\n%s", want, failed)
		}
	}
}

func TestTailWriterKeepsTheLastLines(t *testing.T) {
	w := &tailWriter{max: 16}
	for i := range 10 {
		_, _ = fmt.Fprintf(w, "line %d\n", i)
	}
	if got := w.String(); got != "line 8\nline 9\n" {
		t.Errorf("tail = %q", got)
	}
	var none *tailWriter
	if none.String() != "" {
		t.Error("no tail isn't empty")
	}
}
