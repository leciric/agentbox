package customize

import (
	"fmt"
	"strings"

	"agentbox/internal/api"
)

// Guide is every field a chat may set and what it takes, which the
// appearance tool's show returns: there, rather than in the tool's schema,
// because the schema is sent with every call a chat makes, and this only
// when it is about to change the look. TestGuideNamesEveryField keeps it to
// the schema.
func Guide() string {
	var b strings.Builder
	fmt.Fprintf(&b, `Fields (a change is a JSON merge patch: name only what changes, null puts a field back to AgentBox's own, a list replaces the whole list):
- theme.accent: the highlight colour (buttons, links, focus, the logo's gradient), #rrggbb, in every mode.
- theme.radius: corners, %s, %s, %s or %s.
- theme.dark and theme.light: colours worn when the window is dark (Dracula included) or light, each #rrggbb:
  background (the window and the conversation), sidebar (the side columns), composer (where messages are typed),
  popover (menus and dialogs), text, mutedText (secondary text), surface (hover and selection tints), line (borders),
  success, warning, danger, info (status colours). Text and mutedText must read at 4.5:1 on background, sidebar,
  composer and popover, whether set or AgentBox's own: a pale dark.background needs a dark.text too.
- fonts.ui, fonts.mono: CSS font-family lists, like "\"IBM Plex Sans\", sans-serif"; a font the computer lacks falls back to AgentBox's.
- fonts.size: body text in pixels, %d to %d (%d is AgentBox's own); every text size scales with it.
- density: %s, %s (AgentBox's own) or %s: padding and gaps.
- sidebar.top: the order of the rows above the projects: %s.
- sidebar.foot: the order of the icons along the bottom: %s.
  Items a list leaves out follow in AgentBox's order; an item stays in its own place.
- sidebar.hidden: items not shown; %s can't be hidden.
- css: a stylesheet put after the app's (up to %d KiB, nothing loaded by url() or @import). Colours are custom
  properties on :root, like --ab-ink, --ab-chat, --ab-title, --ab-brand-500; the light window is
  :root[data-appearance="light"]. Prefer the fields above: css is for what they can't do.
Changes show in the app at once. The window's mode (follow the desktop's theme, light, dark or dracula) is appearance, set beside changes.`,
		api.RadiusSharp, api.RadiusSubtle, api.RadiusDefault, api.RadiusRound,
		api.FontSizeMin, api.FontSizeMax, api.FontSizeDefault,
		api.DensityCompact, api.DensityComfortable, api.DensitySpacious,
		strings.Join(api.SidebarTopItems, ", "), strings.Join(api.SidebarFootItems, ", "),
		api.SidebarSettings, MaxCSS>>10)
	return b.String()
}
