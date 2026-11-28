package api

// Customization is how the user has changed the desktop app's look and layout:
// colours on top of the appearance (Theme), fonts, density, the sidebar's
// items, and a stylesheet of their own. It is one file per user, kept by the
// daemon (internal/customize), served on /v1/customization, and worn by the
// app's renderer as it changes (desktop lib/customization.ts). The app's
// Settings resets it; the lead and the Home chat read and change it with their
// appearance tool, so asking a chat to "make it compact" changes the app, not
// its code.
//
// Every field's zero value is AgentBox's own look, so an empty Customization
// is the default and a field left out of a change stays as it was.
type Customization struct {
	// Version is the file's format, CustomizationVersion.
	Version int         `json:"version"`
	Theme   CustomTheme `json:"theme"`
	Fonts   CustomFonts `json:"fonts"`
	// Density is DensityCompact, DensityComfortable (the default, also "") or
	// DensitySpacious: how much room the app's padding and gaps take.
	Density string        `json:"density,omitempty"`
	Sidebar CustomSidebar `json:"sidebar"`
	// CSS is a stylesheet of the user's own, put after the app's.
	CSS string `json:"css,omitempty"`
}

// CustomizationVersion is the format Customization is written in.
const CustomizationVersion = 1

// CustomTheme is colours worn over the appearance setting's.
type CustomTheme struct {
	// Accent is the one highlight colour ("#rrggbb"), worn in every mode,
	// over the followed theme's: buttons, links, focus, the brand's gradient.
	Accent string `json:"accent,omitempty"`
	// Radius is how round corners are: RadiusSharp, RadiusSubtle,
	// RadiusDefault (also "") or RadiusRound.
	Radius string `json:"radius,omitempty"`
	// Dark and Light are the colours worn when the window is dark (Dracula
	// included) and light.
	Dark  CustomColors `json:"dark"`
	Light CustomColors `json:"light"`
}

// CustomColors are the roles a colour can be given, each "#rrggbb", or ""
// for the mode's own. A role is a few of the stylesheet's properties at once
// (desktop lib/customization.ts colorRoles), so a change can't leave half of
// one behind.
type CustomColors struct {
	Background string `json:"background,omitempty"`
	Sidebar    string `json:"sidebar,omitempty"`
	Composer   string `json:"composer,omitempty"`
	Popover    string `json:"popover,omitempty"`
	Text       string `json:"text,omitempty"`
	MutedText  string `json:"mutedText,omitempty"`
	Surface    string `json:"surface,omitempty"`
	Line       string `json:"line,omitempty"`
	Success    string `json:"success,omitempty"`
	Warning    string `json:"warning,omitempty"`
	Danger     string `json:"danger,omitempty"`
	Info       string `json:"info,omitempty"`
}

// CustomFonts are the app's fonts.
type CustomFonts struct {
	// UI and Mono are CSS font-family lists, like `"IBM Plex Sans", sans-serif`,
	// put before AgentBox's own so a font this computer lacks falls back.
	UI   string `json:"ui,omitempty"`
	Mono string `json:"mono,omitempty"`
	// Size is the body text's size in pixels, FontSizeMin to FontSizeMax;
	// every other size scales with it. 0 is FontSizeDefault.
	Size int `json:"size,omitempty"`
}

// CustomSidebar orders and hides the sidebar's fixed items. Top is the rows
// above the projects (SidebarTopItems), Foot the icons along its bottom
// (SidebarFootItems), each listing items of its own place in the order
// wanted: what a list leaves out follows, in AgentBox's order.
type CustomSidebar struct {
	Top    []string `json:"top,omitempty"`
	Foot   []string `json:"foot,omitempty"`
	Hidden []string `json:"hidden,omitempty"`
}

// CustomizationStatus is what /v1/customization answers and EventCustomization
// carries: the customization worn, and where it is kept.
type CustomizationStatus struct {
	Customization Customization `json:"customization"`
	// Path is the file, which the user may edit by hand.
	Path string `json:"path"`
	// Problem says why the file isn't being worn — it was edited by hand into
	// something that isn't valid — and Customization is then the default.
	Problem string `json:"problem,omitempty"`
}

// EventCustomization says the customization changed; its data is a
// CustomizationStatus.
const EventCustomization = "customization"

const (
	DensityCompact     = "compact"
	DensityComfortable = "comfortable"
	DensitySpacious    = "spacious"

	RadiusSharp   = "sharp"
	RadiusSubtle  = "subtle"
	RadiusDefault = "default"
	RadiusRound   = "round"

	FontSizeMin     = 12
	FontSizeDefault = 14
	FontSizeMax     = 18

	// SidebarSettings is the one item that can't be hidden: Settings is where
	// the customization is reset from.
	SidebarSettings = "settings"
)

// SidebarTopItems and SidebarFootItems are the sidebar's fixed items, in
// AgentBox's order: a new task, Home, the Office (when it is on) and the
// filters above the projects; Settings, Work, Media, Jobs, Running and the
// update check below them.
var (
	SidebarTopItems  = []string{"new-task", "home", "office", "filters"}
	SidebarFootItems = []string{SidebarSettings, "work", "media", "jobs", "running", "update"}
)
