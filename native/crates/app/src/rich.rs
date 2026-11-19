//! A chat's rich blocks, as RichBlock.tsx's DrawnBlock draws them: a
//! ```mermaid diagram, an ```svg picture or an ```html preview page, in a
//! well under a header that names it, with its code a click away, Open
//! large (large.rs) and Copy. A click on a picture opens it large too
//! (Electron's cursor-zoom-in button).
use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::hash::{Hash as _, Hasher as _};
use std::sync::{Arc, Mutex};

use agentbox_i18n::t;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::h_flex;

use crate::theme;

/// The most a picture is drawn in the chat (lib/rich.ts PICTURE_MAX): a
/// diagram fits the column, and is opened large to be read.
const PICTURE_MAX: Pixels = px(360.);

#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub enum Kind {
    Mermaid,
    Svg,
    Html,
}

impl Kind {
    fn key(self) -> &'static str {
        match self {
            Kind::Mermaid => "mermaid",
            Kind::Svg => "svg",
            Kind::Html => "html",
        }
    }

    /// label is what the block is: Diagram, Picture or Page.
    pub fn label(self) -> String {
        t(&format!("chat.rich.{}", self.key()))
    }

    /// of is the drawn block a fence's language and meta make, as
    /// mermaid::with_mermaid reads them: ```mermaid, ```svg (any case) and
    /// ```html preview. A plain ```html block stays code.
    pub fn of(lang: &str, meta: &str) -> Option<Kind> {
        match lang {
            "mermaid" => Some(Kind::Mermaid),
            l if l.eq_ignore_ascii_case("svg") => Some(Kind::Svg),
            "html" if meta.split_whitespace().any(|w| w == "preview") => Some(Kind::Html),
            _ => None,
        }
    }
}

/// An open fence: a drawn block a message ends inside of, its opening fence
/// arrived and its closing one not yet (lib/rich.ts openFence).
#[derive(Debug, PartialEq)]
pub struct OpenFence<'a> {
    pub kind: Kind,
    /// Where the fence starts: the text before it is whole.
    pub at: usize,
    /// The fence's language, and what of the block has arrived.
    pub lang: &'a str,
    pub code: &'a str,
}

impl OpenFence<'_> {
    pub fn lines(&self) -> usize {
        if self.code.is_empty() {
            0
        } else {
            self.code.split('\n').count()
        }
    }
}

/// open_fence is the drawn block `text` ends inside of, if any. A streaming
/// reply's block is drawn once it is whole, not on every chunk: until its
/// closing fence arrives it is generating(), or its code if the reply ended
/// without one. Other fences, and drawn blocks already closed, are None.
pub fn open_fence(text: &str) -> Option<OpenFence<'_>> {
    // The open fence: its character, its length, where it starts, its info.
    let mut fence: Option<(u8, usize, usize, &str)> = None;
    let mut at = 0;
    for line in text.split('\n') {
        let start = at;
        at += line.len() + 1;
        let body = line.trim_end_matches('\r');
        let indent = body.len() - body.trim_start_matches(' ').len();
        if indent > 3 {
            continue;
        }
        let rest = &body[indent..];
        let Some(&c) = rest
            .as_bytes()
            .first()
            .filter(|c| **c == b'`' || **c == b'~')
        else {
            continue;
        };
        let size = rest.bytes().take_while(|b| *b == c).count();
        if size < 3 {
            continue;
        }
        let info = rest[size..].trim();
        match fence {
            None => fence = Some((c, size, start, info)),
            Some((open, n, _, _)) if c == open && size >= n && info.is_empty() => fence = None,
            Some(_) => {}
        }
    }
    let (_, _, at, info) = fence?;
    let (lang, meta) = info.split_once(char::is_whitespace).unwrap_or((info, ""));
    let kind = Kind::of(lang, meta)?;
    let code = text[at..].split_once('\n').map_or("", |(_, rest)| rest);
    Some(OpenFence {
        kind,
        at,
        lang,
        code,
    })
}

/// generating is a drawn block still arriving (RichBlock's GeneratingCard):
/// what it will be, how much of it has come, and a shimmer along its foot.
pub fn generating(open: &OpenFence, id: impl Into<SharedString>) -> AnyElement {
    let id = id.into();
    let icon = match open.kind {
        Kind::Html => "app-window",
        Kind::Svg => "image",
        Kind::Mermaid => "workflow",
    };
    let bytes = open.code.len();
    let size = if bytes < 1024 {
        format!("{bytes} B")
    } else {
        format!("{:.1} KB", bytes as f64 / 1024.)
    };
    well()
        .child(
            h_flex()
                .h(px(56.))
                .items_center()
                .gap_3()
                .px_3()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(crate::icons::ic(icon).size_4().flex_none())
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .child(t(&format!("chat.rich.generating.{}", open.kind.key()))),
                )
                .child(
                    div()
                        .flex_none()
                        .text_color(theme::faint())
                        .child(agentbox_i18n::tv(
                            "chat.rich.generatingSoFar",
                            &[
                                ("lines", (open.lines() as i64).into()),
                                ("size", (&size).into()),
                            ],
                        )),
                ),
        )
        .child(crate::motion::skeleton(
            SharedString::from(format!("{id}-shine")),
            relative(1.),
            px(2.),
        ))
        .into_any_element()
}

/// A picture drawn: its SVG, the image GPUI draws of it, and its own size
/// when the SVG says.
pub struct Drawn {
    pub svg: Arc<str>,
    pub image: Arc<Image>,
    pub size: Option<(f32, f32)>,
}

type Rendered = Result<Arc<Drawn>, String>;

/// Every picture drawn so far, by its source and the look it was drawn in: a
/// streamed reply re-renders its message often, and each diagram should be
/// laid out once.
static CACHE: Mutex<Option<HashMap<String, Rendered>>> = Mutex::new(None);

/// drawn is a block's picture: the diagram mermaid-rs-renderer draws of a
/// ```mermaid block, an ```svg block's own SVG.
pub fn drawn(kind: Kind, src: &str) -> Rendered {
    let key = format!("{:?}\n{kind:?}\n{src}", theme::look());
    let mut cache = CACHE.lock().unwrap();
    let cache = cache.get_or_insert_with(HashMap::new);
    if let Some(r) = cache.get(&key) {
        return r.clone();
    }
    let svg = match kind {
        Kind::Mermaid => crate::mermaid::render_svg(src),
        _ => Ok(with_xmlns(src)),
    };
    let r = svg.map(|svg| {
        Arc::new(Drawn {
            size: svg_size(&svg),
            image: Arc::new(Image::from_bytes(
                ImageFormat::Svg,
                svg.clone().into_bytes(),
            )),
            svg: svg.into(),
        })
    });
    if cache.len() > 256 {
        cache.clear();
    }
    cache.insert(key, r.clone());
    r
}

/// with_xmlns is an SVG with the namespace an `<svg>` written for a page may
/// leave out, which a picture needs (lib/rich.ts svgDocument).
pub fn with_xmlns(svg: &str) -> String {
    let s = svg.trim();
    let lower = s.to_ascii_lowercase();
    let Some(at) = lower.find("<svg") else {
        return s.to_string();
    };
    let end = lower[at..].find('>').map_or(lower.len(), |e| at + e);
    if lower[at..end].contains("xmlns") {
        return s.to_string();
    }
    format!(
        "{}<svg xmlns=\"http://www.w3.org/2000/svg\"{}",
        &s[..at],
        &s[at + 4..]
    )
}

/// svg_size is an SVG's own size, from its width and height in pixels, or
/// else its viewBox (lib/rich.ts svgSize); None when it says neither.
pub fn svg_size(svg: &str) -> Option<(f32, f32)> {
    let lower = svg.to_ascii_lowercase();
    let at = lower.find("<svg")?;
    let end = lower[at..].find('>').map_or(lower.len(), |e| at + e);
    let tag = &svg[at..end];
    let attr = |name: &str| -> Option<&str> {
        let low = tag.to_ascii_lowercase();
        let mut from = 0;
        while let Some(i) = low[from..].find(name) {
            let i = from + i;
            from = i + name.len();
            // A whole attribute's name: after a space, before an `=`.
            if !low[..i].ends_with(|c: char| c.is_ascii_whitespace()) {
                continue;
            }
            let rest = low[from..].trim_start();
            let Some(rest) = rest.strip_prefix('=') else {
                continue;
            };
            let rest = rest.trim_start();
            let quote = rest.chars().next().filter(|c| *c == '"' || *c == '\'')?;
            let start = tag.len() - rest.len() + 1;
            let len = tag[start..].find(quote)?;
            return Some(&tag[start..start + len]);
        }
        None
    };
    let pixels = |v: Option<&str>| {
        let v = v?.trim();
        let v = v.strip_suffix("px").unwrap_or(v).trim();
        v.parse::<f32>().ok().filter(|n| *n > 0. && n.is_finite())
    };
    if let (Some(w), Some(h)) = (pixels(attr("width")), pixels(attr("height"))) {
        return Some((w, h));
    }
    let b: Vec<f32> = attr("viewbox")?
        .split(|c: char| c.is_ascii_whitespace() || c == ',')
        .filter(|s| !s.is_empty())
        .filter_map(|s| s.parse().ok())
        .collect();
    (b.len() == 4 && b[2] > 0. && b[3] > 0.).then(|| (b[2], b[3]))
}

thread_local! {
    /// The blocks showing their code rather than what they draw (RichBlock's
    /// `showing`), by key: a Markdown block has no view to keep it in.
    static CODE: RefCell<HashSet<u64>> = RefCell::new(HashSet::new());
}

fn key_of(kind: Kind, src: &str) -> u64 {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    kind.hash(&mut h);
    src.hash(&mut h);
    h.finish()
}

fn showing_code(key: u64) -> bool {
    CODE.with_borrow(|c| c.contains(&key))
}

fn toggle_code(key: u64, window: &mut Window) {
    CODE.with_borrow_mut(|c| {
        if !c.remove(&key) {
            c.insert(key);
        }
    });
    window.refresh();
}

/// head_button is a size-6 button of a block's header (RichBlock's
/// HeaderButton): an icon, its name as a tooltip.
pub fn head_button(
    id: impl Into<ElementId>,
    icon: &'static str,
    tip: String,
    on_click: impl Fn(&mut Window, &mut App) + 'static,
) -> Stateful<Div> {
    let tip = SharedString::from(tip);
    div()
        .id(id)
        .size_6()
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .rounded_md()
        .cursor_pointer()
        .text_color(theme::subtle())
        .hover(|s| s.bg(theme::surface_raised()).text_color(theme::secondary()))
        .active(|s| s.bg(theme::surface_strong()))
        .child(crate::icons::ic(icon).size(px(14.)))
        .tooltip(move |w, cx| gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx))
        .on_click(move |_, window, cx| {
            cx.stop_propagation();
            on_click(window, cx)
        })
}

type Open = std::rc::Rc<dyn Fn(&mut Window, &mut App)>;

/// header is a drawn block's: what it is at the left; Show the code, Open
/// large (when it can be) and Copy at the right.
fn header(kind: Kind, key: u64, src: &str, open: Option<Open>) -> AnyElement {
    h_flex()
        .h_8()
        .flex_none()
        .items_center()
        .justify_between()
        .gap_2()
        .pl_3()
        .pr_1()
        .border_b_1()
        .border_color(theme::line_faint())
        .text_size(theme::text::CAPTION)
        .line_height(relative(1.625))
        .text_color(theme::subtle())
        .child(div().min_w_0().truncate().child(kind.label()))
        .child(
            h_flex()
                .flex_none()
                .gap_0p5()
                .child(head_button(
                    SharedString::from(format!("rich-code-{key:x}")),
                    "code-xml",
                    t("chat.rich.showCode"),
                    move |window, _| toggle_code(key, window),
                ))
                .when_some(open, |el, open| {
                    el.child(head_button(
                        SharedString::from(format!("rich-large-{key:x}")),
                        "maximize-2",
                        t("chat.rich.openLarge"),
                        move |window, cx| open(window, cx),
                    ))
                })
                .child(crate::app::controls::copy_button(
                    SharedString::from(format!("rich-copy-{key:x}")),
                    src.trim_end_matches('\n').to_string(),
                    t("chat.markdown.copyCode"),
                )),
        )
        .into_any_element()
}

/// well is .chat-codeblock: a border, rounded 0.75rem, on the well's colour.
fn well() -> Div {
    div()
        .w_full()
        .min_w_0()
        .flex()
        .flex_col()
        .rounded(px(12.))
        .overflow_hidden()
        .border_1()
        .border_color(theme::line())
        .bg(theme::well())
}

/// as_code is a block showing its code, with what draws it again in its
/// header (RichBlock's Draw it / Preview the page).
fn as_code(kind: Kind, key: u64, src: &str) -> AnyElement {
    let (icon, tip) = match kind {
        Kind::Html => ("eye", "chat.rich.preview"),
        _ => ("eye", "chat.rich.draw"),
    };
    let lang = match kind {
        Kind::Html => "html",
        k => k.key(),
    };
    crate::mermaid::code_block_with(
        lang,
        src,
        Some(
            head_button(
                SharedString::from(format!("rich-draw-{key:x}")),
                icon,
                t(tip),
                move |window, _| toggle_code(key, window),
            )
            .into_any_element(),
        ),
    )
}

/// picture is a ```mermaid or ```svg block drawn. One that doesn't draw (a
/// reply still streaming in its fence) shows its source.
pub fn picture(kind: Kind, src: &str) -> AnyElement {
    let key = key_of(kind, src);
    if showing_code(key) {
        return as_code(kind, key, src);
    }
    let drawn = drawn(kind, src);
    let open: Option<Open> = drawn.as_ref().ok().map(|d| {
        let d = d.clone();
        let title = kind.label();
        std::rc::Rc::new(move |window: &mut Window, cx: &mut App| {
            crate::large::open(
                title.clone(),
                crate::large::What::Svg {
                    svg: d.svg.clone(),
                    image: d.image.clone(),
                    size: d.size,
                },
                window,
                cx,
            )
        }) as Open
    });
    let body = match (drawn, open.clone()) {
        (Ok(d), Some(open)) => div()
            .id(SharedString::from(format!("rich-picture-{key:x}")))
            .group("rich-picture")
            .relative()
            .w_full()
            .p_3()
            .flex()
            .justify_center()
            .cursor_pointer()
            .child(
                img(d.image.clone())
                    .when_some(d.size, |el, (w, _)| el.w(px(w)))
                    .max_w_full()
                    .max_h(PICTURE_MAX)
                    .flex_none()
                    .object_fit(ObjectFit::Contain),
            )
            // GPUI has no zoom-in cursor: a hand, and a magnifier at the
            // corner while it is pointed at.
            .child(
                div()
                    .absolute()
                    .top_2()
                    .right_2()
                    .size_6()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .bg(theme::overlay())
                    .border_1()
                    .border_color(theme::line())
                    .text_color(theme::secondary())
                    .opacity(0.)
                    .group_hover("rich-picture", |s| s.opacity(1.))
                    .child(crate::icons::ic("zoom-in").size(px(14.))),
            )
            .on_click(move |_, window, cx| open(window, cx))
            .into_any_element(),
        _ => div()
            .p_3()
            .text_xs()
            .font_family(theme::mono())
            .text_color(theme::muted())
            .child(src.to_string())
            .into_any_element(),
    };
    well()
        .child(header(kind, key, src, open))
        .child(body)
        .into_any_element()
}

/// page is an ```html preview block: a webview, or a picture of it, or its
/// source where neither can be.
pub fn page(src: &str, window: &mut Window) -> AnyElement {
    let kind = Kind::Html;
    let key = key_of(kind, src);
    if showing_code(key) {
        return as_code(kind, key, src);
    }
    let id = format!("html-{key:x}");
    let open = |src: &str| -> Option<Open> {
        let (id, html) = (format!("{id}-large"), src.to_string());
        Some(std::rc::Rc::new(move |window: &mut Window, cx: &mut App| {
            crate::large::open(
                kind.label(),
                crate::large::What::Page {
                    id: id.clone(),
                    html: html.clone(),
                },
                window,
                cx,
            )
        }) as Open)
    };
    let block = |open: Option<Open>, body: AnyElement| {
        well()
            .child(header(kind, key, src, open))
            .child(div().bg(theme::card()).child(body))
            .into_any_element()
    };
    // As tall as the page, up to FRAME_VIEWPORT of the window; past that it
    // scrolls inside the block, and Open large shows it whole.
    let max = crate::webview::frame_max(window);
    // A page is made once its text has held still for SETTLE: one whose text
    // is still changing (a fence this side's open_fence didn't see open, a
    // block edited) shows where it will be and loads only when it stops.
    if crate::webview::available() && !settled(key, &id) {
        return block(
            open(src),
            crate::webview::placeholder(&id, crate::webview::frame_height(&id, max))
                .into_any_element(),
        );
    }
    // Wayland (or no child webviews): the page as a picture, drawn offscreen.
    #[cfg(target_os = "linux")]
    if crate::snapshot::wanted(window) && !crate::snapshot::failed(&id) {
        return block(
            open(src),
            crate::snapshot::html_snapshot(id, src.to_string(), Some(max)).into_any_element(),
        );
    }
    if !crate::webview::available() {
        // No webview here (no GTK, or the page failed): its source instead.
        return block(
            None,
            div()
                .p_3()
                .text_xs()
                .font_family(theme::mono())
                .text_color(theme::muted())
                .child(
                    div()
                        .pb_2()
                        .text_color(theme::faint())
                        .child(t("native.preview.source")),
                )
                .child(src.to_string())
                .into_any_element(),
        );
    }
    block(
        open(src),
        crate::webview::html_preview(id, src.to_string(), max).into_any_element(),
    )
}

/// How long a page's text holds still before it is made.
const SETTLE: std::time::Duration = std::time::Duration::from_millis(250);

thread_local! {
    /// When each page's text was first drawn, by its key.
    static FIRST_SEEN: RefCell<HashMap<u64, std::time::Instant>> = RefCell::new(HashMap::new());
}

/// settled is whether the page `key` may be made: it has one already (the
/// same text never makes a second), or its text was first drawn SETTLE ago.
/// Until then the window is woken when it will be.
fn settled(key: u64, id: &str) -> bool {
    if crate::webview::made(id) {
        return true;
    }
    let now = std::time::Instant::now();
    let first = FIRST_SEEN.with_borrow_mut(|seen| {
        if seen.len() > 512 {
            seen.retain(|_, t| now.duration_since(*t) < SETTLE * 40);
        }
        *seen.entry(key).or_insert(now)
    });
    let due = first + SETTLE;
    if now >= due {
        return true;
    }
    crate::webview::wake_at(due);
    false
}

#[cfg(test)]
mod tests {
    use super::{Kind, open_fence, svg_size, with_xmlns};

    #[test]
    fn a_drawn_block_is_open_until_its_closing_fence() {
        let text = "Here:\n\n```html preview\n<b>hi</b>\n<i>there";
        let open = open_fence(text).unwrap();
        assert_eq!(open.kind, Kind::Html);
        assert_eq!(&text[..open.at], "Here:\n\n");
        assert_eq!(open.lang, "html");
        assert_eq!(open.code, "<b>hi</b>\n<i>there");
        assert_eq!(open.lines(), 2);
        // Only the opening fence so far, or not even its line's end.
        assert_eq!(open_fence("```html preview").unwrap().code, "");
        assert_eq!(open_fence("```html preview\n").unwrap().lines(), 0);
        assert_eq!(open_fence("```mermaid\ngraph").unwrap().kind, Kind::Mermaid);
        assert_eq!(open_fence("~~~SVG\n<svg").unwrap().kind, Kind::Svg);
        // Closed: drawn, whatever follows.
        assert_eq!(open_fence("```html preview\n<b>hi</b>\n```\nmore"), None);
        assert_eq!(open_fence("```html preview\n<b>\n```  \r\n"), None);
        // A fence closes only with its own character, at least as long, bare.
        assert!(open_fence("````html preview\na\n```\n").is_some());
        assert!(open_fence("```html preview\na\n~~~\n").is_some());
        assert!(open_fence("```html preview\na\n```js\n").is_some());
        // Code that stays code, open or not, and fences indented as code.
        assert_eq!(open_fence("```html\n<b>"), None);
        assert_eq!(open_fence("```rust\nfn"), None);
        assert_eq!(open_fence("    ```html preview\n<b>"), None);
        // A closed code block before an open page.
        let text = "```sh\nls\n```\n\n```html preview\n<p>";
        assert_eq!(open_fence(text).unwrap().at, 14);
    }

    #[test]
    fn an_svg_is_as_large_as_it_says() {
        assert_eq!(
            svg_size(r#"<svg xmlns="x" width="640" height="480px">"#),
            Some((640., 480.))
        );
        // A size in another unit, or a share, isn't pixels: its viewBox is.
        assert_eq!(
            svg_size(r#"<svg width="100%" viewBox="0 0 1200, 300"><rect/></svg>"#),
            Some((1200., 300.))
        );
        assert_eq!(
            svg_size(r#"<?xml version="1.0"?><SVG VIEWBOX='0 0 10 20'>"#),
            Some((10., 20.))
        );
        // stroke-width isn't the width.
        assert_eq!(svg_size(r#"<svg stroke-width="2" height="5">"#), None);
        assert_eq!(svg_size("<div>no picture</div>"), None);
    }

    #[test]
    fn a_picture_gets_its_namespace() {
        assert_eq!(
            with_xmlns("<svg width=\"2\"><g/></svg>"),
            "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"2\"><g/></svg>"
        );
        let had = r#"<svg xmlns="http://www.w3.org/2000/svg"></svg>"#;
        assert_eq!(with_xmlns(had), had);
    }
}
