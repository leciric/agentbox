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

type Rendered = Result<Arc<Image>, String>;

/// Every diagram drawn so far, by its source: a streamed reply re-renders its
/// message often, and each diagram should be laid out once.
static CACHE: Mutex<Option<HashMap<String, Rendered>>> = Mutex::new(None);

pub fn render_svg(src: &str) -> Result<String, String> {
    // The dark preset, on the card's own colour (theme::raised).
    let mut theme = mermaid_rs_renderer::Theme::dark();
    theme.background = "#1a1a1f".into();
    theme.edge_label_background = "#1a1a1f".into();
    let opts = mermaid_rs_renderer::RenderOptions {
        theme,
        ..Default::default()
    };
    mermaid_rs_renderer::render_with_options(src, opts).map_err(|e| e.to_string())
}

fn rendered(src: &str) -> Rendered {
    let mut cache = CACHE.lock().unwrap();
    let cache = cache.get_or_insert_with(HashMap::new);
    if let Some(r) = cache.get(src) {
        return r.clone();
    }
    let r =
        render_svg(src).map(|svg| Arc::new(Image::from_bytes(ImageFormat::Svg, svg.into_bytes())));
    if cache.len() > 256 {
        cache.clear();
    }
    cache.insert(src.to_string(), r.clone());
    r
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
        _ => None,
    })
    .markdown_block_renderer(NODE, |node, _, _| {
        let src = node.as_text().to_string();
        let block = div()
            .my_2()
            .p_3()
            .rounded_lg()
            .bg(theme::raised())
            .border_1()
            .border_color(theme::border());
        match rendered(&src) {
            Ok(image) => block.child(img(image).max_w_full()).into_any_element(),
            Err(_) => block
                .text_xs()
                .font_family("monospace")
                .text_color(theme::muted())
                .child(src)
                .into_any_element(),
        }
    })
    .markdown_block_renderer(HTML, |node, _, _| {
        use std::hash::{Hash as _, Hasher as _};
        let src = node.as_text().to_string();
        let card = div()
            .my_2()
            .rounded_lg()
            .overflow_hidden()
            .border_1()
            .border_color(theme::border())
            .bg(theme::raised());
        if !crate::webview::available() {
            // No webview here (Wayland, no GTK): the page's source instead.
            return card
                .p_3()
                .text_xs()
                .font_family("monospace")
                .text_color(theme::muted())
                .child(src)
                .into_any_element();
        }
        let mut h = std::collections::hash_map::DefaultHasher::new();
        src.hash(&mut h);
        card.child(crate::webview::html_preview(
            format!("html-{:x}", h.finish()),
            src,
            px(260.),
        ))
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
