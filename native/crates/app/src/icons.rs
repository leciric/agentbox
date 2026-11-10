//! The app's icons: the AI tools' (components/state.tsx AIIcon), and every
//! other icon, Lucide's, the set the Electron app draws with (lucide-react),
//! from gpui-component's catalog (gpui-kit-assets). Only the Lucide icons
//! named here are built in, with gpui-component's own (its inputs', selects'
//! and checkboxes'), as the window's asset source.
//!
//! A Lucide icon takes the size and colour of the text around it, as the
//! glyph it replaces did. `of` takes a Lucide name, or one of the glyphs the
//! app drew before (✕, ▾, ⚙…), so a helper handed either draws the icon.
use std::borrow::Cow;
use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::Arc;

use gpui::*;
use gpui_component::Icon;

/// The lucide shapes (24×24, stroked): Sparkles, Bot, Terminal,
/// MousePointer2, Gem and SquareTerminal.
fn shape(ai: &str) -> &'static str {
    match ai {
        "claude" => {
            r#"<path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z"/><path d="M20 3v4"/><path d="M22 5h-4"/><path d="M4 17v2"/><path d="M5 18H3"/>"#
        }
        "codex" => {
            r#"<path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/><path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/>"#
        }
        "opencode" => {
            r#"<polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/>"#
        }
        "cursor" => {
            r#"<path d="M4.037 4.688a.495.495 0 0 1 .651-.651l16 6.5a.5.5 0 0 1-.063.947l-6.124 1.58a2 2 0 0 0-1.438 1.435l-1.579 6.126a.5.5 0 0 1-.947.063z"/>"#
        }
        "gemini" => {
            r#"<path d="M6 3h12l4 6-10 13L2 9Z"/><path d="M11 3 8 9l4 13 4-13-3-6"/><path d="M2 9h20"/>"#
        }
        _ => {
            r#"<path d="m7 11 2-2-2-2"/><path d="M11 13h4"/><rect width="18" height="18" x="3" y="3" rx="2" ry="2"/>"#
        }
    }
}

thread_local! {
    static CACHE: RefCell<HashMap<(String, u32), Arc<Image>>> = RefCell::new(HashMap::new());
}

/// picture is an icon's SVG in a colour, kept once made.
fn picture(ai: &str, color: Hsla) -> Arc<Image> {
    let rgba = color.to_rgb();
    let hex = (((rgba.r * 255.) as u32) << 16)
        | (((rgba.g * 255.) as u32) << 8)
        | ((rgba.b * 255.) as u32);
    let key = (ai.to_string(), hex);
    CACHE.with(|c| {
        c.borrow_mut()
            .entry(key)
            .or_insert_with(|| {
                let svg = format!(
                    r##"<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#{hex:06x}" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">{}</svg>"##,
                    shape(ai)
                );
                Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes()))
            })
            .clone()
    })
}

/// bold is a Lucide check or x drawn with a heavier stroke than the
/// built-in icons' 2 (lucide-react's strokeWidth), in a colour.
pub fn bold(name: &str, stroke: f32, size: Pixels, color: Hsla) -> AnyElement {
    let path = match name {
        "check" => r#"<path d="M20 6 9 17l-5-5"/>"#,
        _ => r#"<path d="M18 6 6 18"/><path d="m6 6 12 12"/>"#,
    };
    let rgba = color.to_rgb();
    let hex = (((rgba.r * 255.) as u32) << 16)
        | (((rgba.g * 255.) as u32) << 8)
        | ((rgba.b * 255.) as u32);
    let key = (format!("bold:{name}:{stroke}"), hex);
    let image = CACHE.with(|c| {
        c.borrow_mut()
            .entry(key)
            .or_insert_with(|| {
                let svg = format!(
                    r##"<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="#{hex:06x}" stroke-width="{stroke}" stroke-linecap="round" stroke-linejoin="round">{path}</svg>"##
                );
                Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes()))
            })
            .clone()
    });
    img(image)
        .flex_none()
        .size(size)
        .opacity(rgba.a)
        .into_any_element()
}

/// claude_mark is TopBar's ClaudeMark: eight rounded spokes, in a colour.
pub fn claude_mark(size: Pixels, color: Hsla) -> AnyElement {
    let rgba = color.to_rgb();
    let hex = (((rgba.r * 255.) as u32) << 16)
        | (((rgba.g * 255.) as u32) << 8)
        | ((rgba.b * 255.) as u32);
    let image = CACHE.with(|c| {
        c.borrow_mut()
            .entry(("claude-mark".to_string(), hex))
            .or_insert_with(|| {
                let spokes: String = (0..8)
                    .map(|i| format!(r#"<rect x="11" y="2" width="2" height="9" rx="1" transform="rotate({} 12 12)"/>"#, i * 45))
                    .collect();
                let svg = format!(
                    r##"<svg xmlns="http://www.w3.org/2000/svg" width="56" height="56" viewBox="0 0 24 24" fill="#{hex:06x}">{spokes}</svg>"##
                );
                Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes()))
            })
            .clone()
    });
    img(image)
        .flex_none()
        .size(size)
        .opacity(rgba.a)
        .into_any_element()
}

/// ai_icon is an AI tool's icon ("" being Claude Code, as the app takes it).
pub fn ai_icon(ai: &str, size: Pixels, color: Hsla) -> AnyElement {
    let ai = if ai.is_empty() { "claude" } else { ai };
    img(picture(ai, color))
        .flex_none()
        .size(size)
        .into_any_element()
}

/// character is a tool's character at rest, as Electron's AgentCharacter
/// draws it in the idle mood (components/AgentCharacter.tsx): Claude's
/// Clawd, Codex's cloud, OpenCode's frame. None for a tool with none.
fn character(ai: &str) -> Option<String> {
    let light = crate::theme::look() == crate::theme::Look::Light;
    let (frame, inner) = if light {
        ("#3a3535", "#d6d4d3")
    } else {
        ("#e4dede", "#5a5454")
    };
    let body = match ai {
        "claude" => r##"<path fill="#DE8A6E" d="M3 5h18v12.08h-1.49V20H18v-2.92h-1.49V20H15v-2.92H9V20H7.49v-2.92H6V20H4.49v-2.92H3v-3.03H0v-3.1h3z"/><rect x="18.5" y="10.95" width="5.5" height="3.1" fill="#DE8A6E"/><g fill="#3D2B25"><rect x="6.15" y="8.5" width="1.2" height="2.1" rx="0.3"/><rect x="16.66" y="8.5" width="1.2" height="2.1" rx="0.3"/></g>"##.to_string(),
        "codex" => r##"<defs><linearGradient id="g" gradientUnits="userSpaceOnUse" x1="12" x2="12" y1="3" y2="21"><stop stop-color="#C9C2FF"/><stop offset=".5" stop-color="#97B2FF"/><stop offset="1" stop-color="#6670FF"/></linearGradient></defs><rect x="17" y="11.2" width="6.6" height="2.5" rx="1.25" fill="#97B2FF"/><path fill="url(#g)" d="M9.064 3.344a4.578 4.578 0 012.285-.312c1 .115 1.891.54 2.673 1.275.01.01.024.017.037.021a.09.09 0 00.043 0 4.55 4.55 0 013.046.275l.047.022.116.057a4.581 4.581 0 012.188 2.399c.209.51.313 1.041.315 1.595a4.24 4.24 0 01-.134 1.223.123.123 0 00.03.115c.594.607.988 1.33 1.183 2.17.289 1.425-.007 2.71-.887 3.854l-.136.166a4.548 4.548 0 01-2.201 1.388.123.123 0 00-.081.076c-.191.551-.383 1.023-.74 1.494-.9 1.187-2.222 1.846-3.711 1.838-1.187-.006-2.239-.44-3.157-1.302a.107.107 0 00-.105-.024c-.388.125-.78.143-1.204.138a4.441 4.441 0 01-1.945-.466 4.544 4.544 0 01-1.61-1.335c-.152-.202-.303-.392-.414-.617a5.81 5.81 0 01-.37-.961 4.582 4.582 0 01-.014-2.298.124.124 0 00.006-.056.085.085 0 00-.027-.048 4.467 4.467 0 01-1.034-1.651 3.896 3.896 0 01-.251-1.192 5.189 5.189 0 01.141-1.6c.337-1.112.982-1.985 1.933-2.618.212-.141.413-.251.601-.33.215-.089.43-.164.646-.227a.098.098 0 00.065-.066 4.51 4.51 0 01.829-1.615 4.535 4.535 0 011.837-1.388z"/><g stroke="#fff" stroke-width="1" stroke-linecap="round" stroke-linejoin="round" fill="none"><path d="M8.2 10.15l1 1.9-1 1.85"/><path d="M12.9 14.2h2.9"/></g>"##.to_string(),
        "opencode" => format!(r##"<rect x="8" y="10" width="8" height="8" fill="{inner}"/><path d="M16 6H8v12h8V6zm4 16H4V2h16v20z" fill-rule="evenodd" fill="{frame}"/><rect x="17.5" y="12" width="5.8" height="3" fill="{frame}"/><g fill="{frame}"><rect x="9.8" y="12.4" width="1.2" height="1.9" rx="0.3"/><rect x="13" y="12.4" width="1.2" height="1.9" rx="0.3"/></g>"##),
        _ => return None,
    };
    Some(format!(
        r##"<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 24 24">{body}</svg>"##
    ))
}

/// tool_mark is Electron's ToolMark: a tool's character where it has one,
/// else its icon in `color`.
pub fn tool_mark(ai: &str, size: Pixels, color: Hsla) -> AnyElement {
    tool_mark_grey(ai, size, color, false)
}

/// tool_mark_grey is tool_mark, its character in grey when `grey` (CSS's
/// grayscale, as an idle monitor's).
pub fn tool_mark_grey(ai: &str, size: Pixels, color: Hsla, grey: bool) -> AnyElement {
    let ai = if ai.is_empty() { "claude" } else { ai };
    let light = crate::theme::look() == crate::theme::Look::Light;
    let key = (format!("char:{ai}:{light}"), 0);
    let Some(image) = CACHE.with(|c| {
        if let Some(i) = c.borrow().get(&key) {
            return Some(i.clone());
        }
        let svg = character(ai)?;
        let i = Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes()));
        c.borrow_mut().insert(key.clone(), i.clone());
        Some(i)
    }) else {
        return ai_icon(ai, size, color);
    };
    img(image)
        .flex_none()
        .size(size)
        .grayscale(grey)
        .into_any_element()
}

gpui_kit_assets::icon_assets!(
    Lucide,
    [
        Activity,
        AlarmClock,
        ArrowDown,
        ArrowDownLeft,
        ArrowLeft,
        ArrowLeftRight,
        ArrowRight,
        ArrowRightLeft,
        ArrowUp,
        ArrowUpRight,
        // The Office's row (#668's Armchair).
        Armchair,
        Archive,
        Ban,
        Bell,
        Bot,
        Box,
        Brain,
        Briefcase,
        Camera,
        ChartColumn,
        ChartNoAxesGantt,
        Check,
        ChevronDown,
        ChevronLeft,
        ChevronRight,
        ChevronUp,
        ChevronsLeft,
        ChevronsRight,
        ChevronsUpDown,
        Circle,
        CircleAlert,
        CircleArrowUp,
        CircleCheck,
        CircleDot,
        CircleQuestionMark,
        CircleX,
        Clock,
        Copy,
        CornerDownLeft,
        CornerDownRight,
        Coins,
        Cpu,
        Diamond,
        Download,
        Droplet,
        Ellipsis,
        ExternalLink,
        Eye,
        FaceSlightlySmiling,
        EyeOff,
        File,
        FileCode,
        FileDiff,
        FileChartColumn,
        FileText,
        Film,
        FlaskConical,
        Folder,
        FolderGit2,
        FolderOpen,
        FolderPlus,
        FolderTree,
        GitBranch,
        GitMerge,
        GitPullRequest,
        Gauge,
        Globe,
        HardDrive,
        Highlighter,
        RotateCcwClock,
        Hourglass,
        House,
        Image,
        Images,
        Info,
        KeyRound,
        Laptop,
        Languages,
        Layers,
        LayoutGrid,
        ListChecks,
        ListFilter,
        ListTodo,
        LoaderCircle,
        LocateFixed,
        Lock,
        LogOut,
        Maximize2,
        Menu,
        MessageSquare,
        MessageSquarePlus,
        MessagesSquare,
        Minimize2,
        Monitor,
        MonitorPlay,
        Moon,
        MoonStar,
        PanelRight,
        Paperclip,
        Pause,
        Pencil,
        Pin,
        Play,
        Plug,
        Plus,
        Power,
        RefreshCw,
        RotateCcw,
        ScrollText,
        Search,
        Settings,
        Share2,
        ShieldAlert,
        ShieldCheck,
        ShieldX,
        SlidersHorizontal,
        Smartphone,
        Sparkles,
        Square,
        SquareDashed,
        SquarePen,
        SquareTerminal,
        Star,
        Sun,
        StickyNote,
        Tag,
        Trash,
        Terminal,
        TriangleAlert,
        User,
        Users,
        Video,
        Wallet,
        WandSparkles,
        Wrench,
        X,
        ArrowDownUp,
        BellOff,
        BellRing,
        Bug,
        CalendarClock,
        CalendarDays,
        CircleStop,
        Keyboard,
        NotebookPen,
        Stethoscope,
        Zap,
        ZoomIn,
        ZoomOut,
        Clipboard,
        Compass,
        FolderMinus,
        GitFork,
        MessageCircle,
        Split,
        Undo2,
    ]
);

/// Assets is the window's asset source: the icons above, then
/// gpui-component's own.
pub struct Assets;

impl AssetSource for Assets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        if let Ok(Some(found)) = Lucide.load(path) {
            return Ok(Some(found));
        }
        gpui_kit_assets::Assets.load(path).or(Ok(None))
    }

    fn list(&self, path: &str) -> Result<Vec<SharedString>> {
        let mut all = Lucide.list(path)?;
        all.extend(gpui_kit_assets::Assets.list(path).unwrap_or_default());
        Ok(all)
    }
}

/// lucide is the Lucide icon a glyph the app drew stands for, or the name
/// itself when it is one already; None for text that is no icon (a dot, a
/// letter, an emoji).
pub fn lucide(g: &str) -> Option<&str> {
    Some(match g {
        "✕" | "×" => "x",
        "✓" => "check",
        "▾" => "chevron-down",
        "▸" | "›" | "❯" => "chevron-right",
        "‹" => "chevron-left",
        "«" => "chevrons-left",
        "»" => "chevrons-right",
        "⚠" => "triangle-alert",
        "↻" | "⟳" => "refresh-cw",
        "▶" => "play",
        "⏸" => "pause",
        "↗" => "external-link",
        "↙" => "arrow-down-left",
        "✎" => "pencil",
        "⚙" => "settings",
        "⋯" => "ellipsis",
        "←" => "arrow-left",
        "→" => "arrow-right",
        "↑" => "arrow-up",
        "↓" => "arrow-down",
        "⏰" => "alarm-clock",
        "▣" => "monitor",
        "▤" => "file-text",
        "◫" => "images",
        "≡" => "chart-no-axes-gantt",
        "◍" | "◰" => "globe",
        "▯" => "smartphone",
        "⇆" | "⇄" => "arrow-left-right",
        "❐" => "copy",
        "⌛" => "hourglass",
        "◷" => "clock",
        "⚡" => "zap",
        "🔒" => "lock",
        "⎇" => "git-branch",
        "⌂" => "house",
        "🪑" => "armchair",
        "⊕" => "plus",
        "⊞" => "folder-plus",
        "☷" => "layout-grid",
        "⇪" => "download",
        "✦" | "✳" => "sparkles",
        "◌" => "loader-circle",
        "⏻" => "power",
        "⏏" => "log-out",
        "⌖" => "locate-fixed",
        "☾" => "moon-star",
        "☰" => "menu",
        "★" => "star",
        "📌" => "pin",
        "🕘" => "clock",
        "⬚" => "square-dashed",
        "◨" => "panel-right",
        "↳" => "corner-down-right",
        "◆" => "diamond",
        "◎" => "search",
        "◉" => "eye",
        "⌾" => "circle-dot",
        _ if !g.is_empty()
            && g.bytes()
                .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'-') =>
        {
            g
        }
        _ => return None,
    })
}

/// glyph is `g`'s icon when it is one of the glyphs the app drew (never a
/// word, which stays text), or `g` as text.
pub fn glyph(g: impl Into<SharedString>) -> AnyElement {
    let g = g.into();
    if g.is_ascii() {
        return g.into_any_element();
    }
    of(g)
}

/// lead is an icon before a line of text (a branch, a count), as the
/// Electron app sets a lucide icon before its label.
pub fn lead(name: &str, text: impl Into<SharedString>) -> AnyElement {
    gpui_component::h_flex()
        .gap_1()
        .items_center()
        .min_w_0()
        .child(ic(name))
        .child(div().min_w_0().truncate().child(text.into()))
        .into_any_element()
}

/// ic is a Lucide icon by name (`x`, `chevron-down`, `folder-tree`).
pub fn ic(name: &str) -> Icon {
    Icon::empty().path(SharedString::from(format!("icons/{name}.svg")))
}

/// SPINNER is the icon that always turns (Electron's LoaderCircle, which is
/// only ever drawn `animate-spin`).
pub const SPINNER: &str = "loader-circle";

/// spinner is the spinner, turning.
pub fn spinner() -> AnyElement {
    spin(ic(SPINNER))
}

/// spin turns an icon round (crate::motion::spin): a refresh while it fetches.
pub fn spin(icon: Icon) -> AnyElement {
    crate::motion::spin(icon, "spin").into_any_element()
}

/// icon is `icon` (made from `name`, sized and coloured), turning when `name`
/// is the spinner: for helpers handed a name that may be either.
pub fn icon(name: &str, icon: Icon) -> AnyElement {
    if name == SPINNER {
        spin(icon)
    } else {
        icon.into_any_element()
    }
}

/// of is `g`'s icon, or `g` as text when it stands for none.
pub fn of(g: impl Into<SharedString>) -> AnyElement {
    let g = g.into();
    match lucide(&g) {
        Some(name) => icon(name, ic(name)),
        None => g.into_any_element(),
    }
}

#[cfg(test)]
mod tests {
    use super::{Lucide, lucide};
    use gpui::AssetSource as _;

    const GLYPHS: &[&str] = &[
        "✕", "✓", "▾", "▸", "‹", "«", "»", "⚠", "↻", "▶", "⏸", "↗", "↙", "✎", "⚙", "⋯", "←", "→",
        "↑", "↓", "⏰", "▣", "▤", "◫", "≡", "◍", "▯", "⇆", "❐", "⌛", "◷", "⚡", "🔒", "⎇", "⌂",
        "🪑", "⊕", "⊞", "☷", "⇪", "✦", "◌", "⏻", "⏏", "⌖", "☾", "☰", "★", "📌", "🕘", "⬚", "◨",
        "↳", "◆", "◎", "◉", "⌾",
    ];

    /// Every icon the app names is built in: a missing one draws nothing.
    #[test]
    fn every_icon_used_is_built_in() {
        let dir = concat!(env!("CARGO_MANIFEST_DIR"), "/src");
        let mut names = Vec::new();
        fn walk(dir: &std::path::Path, out: &mut Vec<std::path::PathBuf>) {
            for e in std::fs::read_dir(dir).unwrap().flatten() {
                let p = e.path();
                if p.is_dir() {
                    walk(&p, out);
                } else if p.extension().is_some_and(|x| x == "rs") {
                    out.push(p);
                }
            }
        }
        let mut files = Vec::new();
        walk(std::path::Path::new(dir), &mut files);
        for f in files {
            let text = std::fs::read_to_string(&f).unwrap();
            for marker in ["ic(\"", "icons::of(\"", "icons::lead(\""] {
                for part in text.split(marker).skip(1) {
                    let name = part.split('"').next().unwrap();
                    if let Some(l) = lucide(name) {
                        names.push((f.display().to_string(), l.to_string()));
                    }
                }
            }
        }
        // The names views, kinds and sections pick (panel.rs, foot.rs,
        // agent_settings.rs, review.rs, sidebar_actions.rs, views.rs).
        for n in [
            "monitor",
            "square-terminal",
            "folder-tree",
            "images",
            "chart-no-axes-gantt",
            "activity",
            "globe",
            "smartphone",
            "alarm-clock",
            "film",
            "file-text",
            "sticky-note",
            "image",
            "folder-git-2",
            "key-round",
            "wrench",
            "list-checks",
            "briefcase",
            "arrow-left-right",
            "camera",
        ] {
            names.push(("names".into(), n.to_string()));
        }
        // And every glyph `of` maps.
        for g in GLYPHS {
            names.push(("glyphs".into(), lucide(g).unwrap().to_string()));
        }
        let missing: Vec<_> = names
            .iter()
            .filter(|(_, n)| !matches!(Lucide.load(&format!("icons/{n}.svg")), Ok(Some(_))))
            .collect();
        assert!(missing.is_empty(), "icons not built in: {missing:?}");
        assert!(lucide("•").is_none() && lucide("A").is_none());
    }
}
