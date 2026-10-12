package image

import (
	"slices"
	"strings"
	"testing"
)

// Every tool tools.txt pins for every image is either published, with its
// license on record, or left out: a new tool has to be one or the other
// before anything publishes it.
func TestEveryToolIsPublishedOrNot(t *testing.T) {
	for _, tool := range ToolsFor(Components{}) {
		_, licensed := toolLicenses[tool.Name()]
		left := slices.Contains(unpublished, tool.Name())
		if licensed == left {
			t.Errorf("%s should be in exactly one of toolLicenses (published) and unpublished", tool.Name())
		}
	}
	for _, name := range unpublished {
		Pin(name) // panics for one tools.txt doesn't pin
	}
	for name := range toolLicenses {
		Pin(name)
	}
}

func TestPublishedToolsLeaveOutClaudeCode(t *testing.T) {
	got := specsOf(PublishedTools())
	if slices.Contains(got, Pin("claude")) || slices.Contains(got, Pin("npm:@cursor/sdk")) || !slices.Contains(got, Pin("go")) {
		t.Errorf("PublishedTools() = %v", got)
	}
}

func TestParseSums(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("B", 64)
	got := parseSums([]byte(a + "  image.json\n" + b + " *agentbox-base.tar.gz\nnot a line\n\n" + "abc  short\n"))
	if len(got) != 2 || got["image.json"] != a || got["agentbox-base.tar.gz"] != strings.ToLower(b) {
		t.Errorf("parseSums() = %v", got)
	}
}

func TestParseDpkgStatus(t *testing.T) {
	status := `Package: zlib1g
Status: install ok installed
Architecture: amd64
Source: zlib (1:1.3.dfsg+really1.3.1-1)
Version: 1:1.3.dfsg+really1.3.1-1+b1
Description: compression library
 a continuation line: with a colon

Package: removed
Status: deinstall ok config-files
Version: 1.0

Package: docker-ce
Status: install ok installed
Architecture: amd64
Source: docker-ce
Version: 5:28.0.0-1~debian.13~trixie
`
	got := parseDpkgStatus([]byte(status))
	want := []debPackage{
		{"docker-ce", "5:28.0.0-1~debian.13~trixie", "amd64", "docker-ce", "5:28.0.0-1~debian.13~trixie"},
		{"zlib1g", "1:1.3.dfsg+really1.3.1-1+b1", "amd64", "zlib", "1:1.3.dfsg+really1.3.1-1"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("parseDpkgStatus() = %+v, want %+v", got, want)
	}
	notice := string(sourcesNotice(Published{Version: Version, Arch: "amd64", File: "agentbox-base.tar.gz"}, PublishedTools(), got))
	for _, line := range []string{
		"- zlib 1:1.3.dfsg+really1.3.1-1: https://snapshot.debian.org/package/zlib/1:1.3.dfsg+really1.3.1-1/",
		"- docker-ce 5:28.0.0-1~debian.13~trixie: Docker's package (Apache-2.0), source at https://github.com/moby/moby",
		"- " + Pin("gh") + " (MIT): https://github.com/cli/cli",
	} {
		if !strings.Contains(notice, line+"\n") {
			t.Errorf("SOURCES.md has no line %q:\n%s", line, notice)
		}
	}
}
