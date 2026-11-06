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

/// ai_icon is an AI tool's icon ("" being Claude Code, as the app takes it).
pub fn ai_icon(ai: &str, size: Pixels, color: Hsla) -> AnyElement {
    let ai = if ai.is_empty() { "claude" } else { ai };
    img(picture(ai, color))
        .flex_none()
        .size(size)
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
        ArrowUp,
        ArrowUpRight,
        Ban,
        Bell,
        Bot,
        Box,
        Brain,
        Briefcase,
        Camera,
        ChartGantt,
        Check,
        ChevronDown,
        ChevronLeft,
        ChevronRight,
        ChevronUp,
        ChevronsLeft,
        ChevronsRight,
        Circle,
        CircleAlert,
        CircleArrowUp,
        CircleCheck,
        CircleDot,
        CircleX,
        Clock,
        Copy,
        CornerDownRight,
        Cpu,
        Diamond,
        Download,
        Ellipsis,
        ExternalLink,
        Eye,
        EyeOff,
        File,
        FileCode,
        FileDiff,
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
        Globe,
        Highlighter,
        Hourglass,
        House,
        Image,
        Images,
        Info,
        KeyRound,
        Layers,
        LayoutGrid,
        ListChecks,
        LoaderCircle,
        LocateFixed,
        Lock,
        LogOut,
        Maximize2,
        Menu,
        MessageSquare,
        Minimize2,
        Monitor,
        Moon,
        MoonStar,
        PanelRight,
        Pause,
        Pencil,
        Pin,
        Play,
        Plug,
        Plus,
        Power,
        RefreshCw,
        RotateCcw,
        Search,
        Settings,
        Share2,
        SlidersHorizontal,
        Smartphone,
        Sparkles,
        Square,
        SquareDashed,
        SquarePen,
        SquareTerminal,
        Star,
        StickyNote,
        Tag,
        Terminal,
        TriangleAlert,
        User,
        Users,
        Wallet,
        WandSparkles,
        Wrench,
        X,
        Zap,
        ZoomIn,
        ZoomOut,
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
        "≡" => "chart-gantt",
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

/// of is `g`'s icon, or `g` as text when it stands for none.
pub fn of(g: impl Into<SharedString>) -> AnyElement {
    let g = g.into();
    match lucide(&g) {
        Some(name) => ic(name).into_any_element(),
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
        "⊕", "⊞", "☷", "⇪", "✦", "◌", "⏻", "⏏", "⌖", "☾", "☰", "★", "📌", "🕘", "⬚", "◨", "↳", "◆",
        "◎", "◉", "⌾",
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
            "chart-gantt",
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
