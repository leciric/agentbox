//! The AI tools' icons (components/state.tsx AIIcon): the lucide icons the
//! Electron app draws for each tool, as SVG pictures stroked in a colour.
use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::Arc;

use gpui::*;

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
