package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"strings"

	"agentbox/internal/state"
)

// The desktop's wallpaper travels with the binary rather than with the base
// image: browser.sh is already pushed to the agent on every `browser start`
// (prepareBrowser), so shipping the picture the same way means a new look
// reaches every existing agent at once, and changing it never costs an image
// version. Baking it in would also mean rendering an SVG during the image
// build, which is a renderer the image doesn't otherwise need.
//
// wallpaper.png is rendered from wallpaper.svg next to it:
//
//	chromium --headless --window-size=1440,900 --screenshot=wallpaper.png <the svg>

//go:embed wallpaper.png
var wallpaper []byte

// wallpaperPath is where browser.sh looks for it. It is the same path an image
// could have baked it into, so nothing has to change if that ever happens.
const wallpaperPath = "/usr/local/share/agentbox/wallpaper.png"

// installWallpaper writes the wallpaper into the agent, unless the one there is
// already this one: it is half a megabyte, and `browser start` runs often.
// A failure isn't fatal — browser.sh falls back to a solid colour.
func (m *Manager) installWallpaper(ctx context.Context, a state.Agent) {
	sum := sha256.Sum256(wallpaper)
	want := hex.EncodeToString(sum[:])
	out, err := m.Incus.Run(ctx, "exec", a.Instance, "--",
		"sh", "-c", "sha256sum "+wallpaperPath+" 2>/dev/null | cut -d' ' -f1")
	if err == nil && strings.TrimSpace(out) == want {
		return
	}
	if err := m.Incus.WriteFile(ctx, a.Instance, wallpaperPath, wallpaper, 0, 0, 0o644); err != nil {
		m.logf("The desktop wallpaper wasn't installed: %v", err)
	}
}
