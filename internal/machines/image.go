package machines

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"

	"agentbox/internal/agent"
	"agentbox/internal/image"
)

//go:embed image/Dockerfile image/entrypoint.sh image/chromium.conf
var imageFiles embed.FS

// ImageRepo is the machine image's name; its tag is its build context's hash,
// so a new AgentBox with a changed desktop builds a new one.
const ImageRepo = "agentbox-machine"

// buildContext is the machine image's build context, by file name. browser.sh
// and the wallpaper are the agents' own, so a machine's desktop is theirs.
func buildContext() map[string][]byte {
	files := map[string][]byte{}
	for _, name := range []string{"Dockerfile", "entrypoint.sh", "chromium.conf"} {
		data, err := imageFiles.ReadFile("image/" + name)
		if err != nil {
			panic(err) // embedded above
		}
		files[name] = data
	}
	files["browser.sh"] = agent.BrowserScript()
	files["wallpaper.png"], _ = agent.Wallpaper()
	return files
}

// playwrightMCP is the Playwright MCP server the image installs: the agents'
// own pin (internal/image/tools.txt), without mise's npm: prefix.
func playwrightMCP() string {
	spec := image.Pin("npm:@playwright/mcp")
	return spec[len("npm:"):]
}

// ImageTag is the image this AgentBox builds.
func ImageTag() string {
	files := buildContext()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n + "\x00"))
		h.Write(files[n])
		h.Write([]byte{0})
	}
	h.Write([]byte(playwrightMCP()))
	return ImageRepo + ":" + hex.EncodeToString(h.Sum(nil))[:12]
}

// WriteContext writes the build context into dir, for a build by hand or
// one that publishes the image: `docker build --build-arg PLAYWRIGHT_MCP=… dir`.
func WriteContext(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, data := range buildContext() {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// BuildArgs are the build's --build-arg values.
func BuildArgs() map[string]string {
	return map[string]string{"PLAYWRIGHT_MCP": playwrightMCP()}
}
