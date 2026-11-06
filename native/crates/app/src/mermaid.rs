//! ```mermaid blocks in chat messages, drawn without a webview: the source is
//! laid out and rendered to SVG by mermaid-rs-renderer (pure Rust), and GPUI
//! draws the SVG as an image. A block that doesn't parse (yet: a reply still
//! streaming in) shows its source instead.
use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use gpui::*;
use gpui_component::text::{MarkdownNode, TextView};

use crate::theme;

const NODE: &str = "mermaid";
const HTML: &str = "html-preview";
/// Every other fenced block, drawn as Electron's CodeBlock.
const CODE: &str = "codeblock";

type Rendered = Result<Arc<Image>, String>;

/// Every diagram drawn so far, by its source: a streamed reply re-renders its
/// message often, and each diagram should be laid out once.
static CACHE: Mutex<Option<HashMap<String, Rendered>>> = Mutex::new(None);

pub fn render_svg(src: &str) -> Result<String, String> {
    // The look's preset (modern on light, dark otherwise), on the card's own
    // colour, in the app's face.
    let mut theme = if theme::look() == theme::Look::Light {
        mermaid_rs_renderer::Theme::modern()
    } else {
        mermaid_rs_renderer::Theme::dark()
    };
    let ground = theme::hex(theme::card());
    theme.background = ground.clone();
    theme.edge_label_background = ground;
    theme.font_family = format!("{}, sans-serif", theme::SANS);
    let opts = mermaid_rs_renderer::RenderOptions {
        theme,
        ..Default::default()
    };
    mermaid_rs_renderer::render_with_options(src, opts).map_err(|e| e.to_string())
}

fn rendered(src: &str) -> Rendered {
    let mut cache = CACHE.lock().unwrap();
    let cache = cache.get_or_insert_with(HashMap::new);
    // Each look draws its own: the key carries it.
    let key = format!("{:?}\n{src}", theme::look());
    if let Some(r) = cache.get(&key) {
        return r.clone();
    }
    let r =
        render_svg(src).map(|svg| Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes())));
    if cache.len() > 256 {
        cache.clear();
    }
    cache.insert(key, r.clone());
    r
}

/// code_block is a fenced block as Electron's CodeBlock (components/chat/
/// CodeBlock.tsx, .chat-codeblock): a well with a border, rounded 0.75rem; a
/// header h-8 with the language at its left over a faint rule; the code at
/// 0.78rem, line height 1.6, in 0.7rem 0.9rem of padding, in Shiki's colours.
pub fn code_block(lang: &str, text: &str) -> AnyElement {
    let lines: Vec<String> = text.trim_end_matches('\n').lines().map(str::to_string).collect();
    let runs = crate::highlight::lang_of_fence(lang).map(|l| crate::highlight::line_runs(l, &lines));
    div()
        .w_full()
        .min_w_0()
        .my(rems(0.65))
        .rounded(px(12.))
        .overflow_hidden()
        .border_1()
        .border_color(theme::line())
        .bg(theme::well())
        .child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .h_8()
                .pl_3()
                .pr_1()
                .border_b_1()
                .border_color(theme::line_faint())
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .font_family(theme::mono())
                        .child(if lang.is_empty() { "text".to_string() } else { lang.to_string() }),
                ),
        )
        .child(
            div()
                .flex()
                .flex_col()
                .px(rems(0.9))
                .py(rems(0.7))
                .overflow_x_hidden()
                .font_family(theme::mono())
                .text_size(rems(0.78))
                .line_height(relative(1.6))
                .text_color(crate::highlight::code_fg())
                .children(lines.into_iter().enumerate().map(|(i, l)| {
                    let l = if l.is_empty() { " ".to_string() } else { l };
                    div()
                        .whitespace_nowrap()
                        .child(crate::highlight::styled(l, runs.as_ref().and_then(|r| r.get(i))))
                })),
        )
        .into_any_element()
}

/// with_mermaid makes a TextView draw ```mermaid fences as diagrams, and
/// ```html preview fences as pages in a webview (webview.rs).
pub fn with_mermaid(view: TextView) -> TextView {
    view.markdown_block_parser(|node, _| match node {
        markdown::mdast::Node::Code(code) if code.lang.as_deref() == Some(NODE) => Some(
            MarkdownNode::new(NODE, ())
                .text(code.value.clone())
                .markdown(code.value.clone()),
        ),
        markdown::mdast::Node::Code(code) if is_preview(code) => Some(
            MarkdownNode::new(HTML, ())
                .text(code.value.clone())
                .markdown(code.value.clone()),
        ),
        markdown::mdast::Node::Code(code) => Some(
            MarkdownNode::new(CODE, code.lang.clone().unwrap_or_default())
                .text(code.value.clone())
                .markdown(code.value.clone()),
        ),
        _ => None,
    })
    .markdown_block_renderer(CODE, |node, _, _| {
        let lang = node.data::<String>().cloned().unwrap_or_default();
        code_block(&lang, node.as_text())
    })
    .markdown_block_renderer(NODE, |node, _, _| {
        let src = node.as_text().to_string();
        let block = div()
            .my_2()
            .p_3()
            .rounded_lg()
            .bg(theme::card())
            .border_1()
            .border_color(theme::line());
        match rendered(&src) {
            Ok(image) => block.child(img(image).max_w_full()).into_any_element(),
            Err(_) => block
                .text_xs()
                .font_family(crate::theme::mono())
                .text_color(theme::muted())
                .child(src)
                .into_any_element(),
        }
    })
    .markdown_block_renderer(HTML, |node, window, _| {
        use std::hash::{Hash as _, Hasher as _};
        let src = node.as_text().to_string();
        let card = div()
            .my_2()
            .rounded_lg()
            .overflow_hidden()
            .border_1()
            .border_color(theme::line())
            .bg(theme::card());
        let mut h = std::collections::hash_map::DefaultHasher::new();
        src.hash(&mut h);
        let id = format!("html-{:x}", h.finish());
        // Wayland (or no child webviews): the page as a picture, drawn offscreen.
        #[cfg(target_os = "linux")]
        if crate::snapshot::wanted(window) && !crate::snapshot::failed(&id) {
            return card
                .child(crate::snapshot::html_snapshot(id, src))
                .into_any_element();
        }
        #[cfg(not(target_os = "linux"))]
        let _ = window;
        if !crate::webview::available() {
            // No webview here (no GTK, or the page failed): its source instead.
            return card
                .child(
                    div()
                        .pb_2()
                        .text_color(theme::faint())
                        .child(agentbox_i18n::t("native.preview.source")),
                )
                .p_3()
                .text_xs()
                .font_family(crate::theme::mono())
                .text_color(theme::muted())
                .child(src)
                .into_any_element();
        }
        card.child(crate::webview::html_preview(id, src, px(260.)))
            .into_any_element()
    })
}

/// is_preview is a ```html preview fence: a page to show, not code.
fn is_preview(code: &markdown::mdast::Code) -> bool {
    code.lang.as_deref() == Some("html")
        && code
            .meta
            .as_deref()
            .is_some_and(|m| m.split_whitespace().any(|w| w == "preview"))
}

#[cfg(test)]
mod tests {
    use super::render_svg;

    #[test]
    fn draws_the_diagrams_chats_use() {
        for src in [
            "flowchart LR\n  app[Desktop app] -->|socket| d[(Daemon)]\n  d --> lead{{Lead}}",
            "sequenceDiagram\n  App->>Daemon: POST\n  Daemon-->>App: SSE",
            "stateDiagram-v2\n  [*] --> running\n  running --> paused",
            "pie title Tokens\n  \"opus\" : 62\n  \"haiku\" : 10",
        ] {
            let svg = render_svg(src).unwrap_or_else(|e| panic!("{src}: {e}"));
            assert!(svg.starts_with("<svg") || svg.contains("<svg"), "{src}");
        }
    }
}
