//! A chat's picture or page opened large: RichBlock.tsx's RichDialog, a
//! dialog over the window, with ZoomableImage.tsx's zoom for a picture (a
//! diagram, an ```svg block, an image sent in the chat) and ZoomControls'
//! for a page.
//!
//! A picture fits the stage at first, never past its own size. The buttons,
//! Ctrl (or ⌘) with the wheel or + / − / 0, a pinch and a double-click zoom
//! it, keeping the point under the pointer still; a drag or the wheel pans
//! it: zoom.rs, which the review's stage zooms by too. An SVG is drawn
//! again, sharp, at the size it is shown (in steps of twice its own width, up
//! to MAX_PIXELS), where GPUI would stretch its one picture of it. Escape,
//! the ✕ or a click beside the dialog closes it.
//!
//! There is one for the window, which app.rs draws over the rest: a block in
//! a chat's Markdown has no view of its own to keep one in, so it calls
//! `open`.
use std::cell::Cell;
use std::rc::Rc;
use std::sync::Arc;

use agentbox_i18n::t;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::theme;
use crate::zoom::{self, Fit, Zoom};

/// The zooms a page steps through (RichBlock's ZOOMS).
const PAGE_ZOOMS: [f32; 10] = [0.5, 0.67, 0.8, 0.9, 1., 1.1, 1.25, 1.5, 1.75, 2.];
/// RichDialog's size: h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px].
const MAX_WIDTH: f32 = 1180.;
/// The header's height; the stage is the rest.
const HEAD: f32 = 48.;
/// The most a sharp copy of an SVG may be: past it, the largest is
/// stretched, a little soft. 16M pixels is 64 MB of BGRA.
const MAX_PIXELS: f32 = 16_000_000.;
const MAX_SIDE: f32 = 8192.;
/// The stage's lower corners, inside the card's border.
const STAGE_CORNER: Pixels = px(11.);

/// What is open large.
pub enum What {
    /// An SVG: a diagram as mermaid drew it, an ```svg block. `image` is how
    /// the chat draws it, shown until a sharp copy is ready; `size` is its
    /// own, when it says.
    Svg {
        svg: Arc<str>,
        image: Arc<Image>,
        size: Option<(f32, f32)>,
    },
    /// A picture sent in the chat.
    Image(Arc<Image>),
    /// An ```html preview block's page, in a webview of its own.
    Page { id: String, html: String },
}

/// sharp_width is the width an SVG of `natural` size is drawn at to be
/// shown `device` pixels wide: its own width times the next power of two
/// up, so that zooming draws it again now and then rather than at each
/// step, within MAX_SIDE and MAX_PIXELS.
fn sharp_width(natural: (f32, f32), device: f32) -> u32 {
    let k = (device / natural.0).max(1e-3).log2().ceil();
    let w = natural.0 * 2f32.powf(k);
    let tall = natural.1 / natural.0;
    let cap = MAX_SIDE
        .min(MAX_SIDE / tall)
        .min((MAX_PIXELS / tall).sqrt());
    w.min(cap).max(1.).round() as u32
}

/// page_step is the page zoom after `z` going up or down a step.
fn page_step(z: f32, up: bool) -> f32 {
    let at = PAGE_ZOOMS
        .iter()
        .position(|s| (s - z).abs() < 1e-3)
        .unwrap_or(4);
    if up {
        PAGE_ZOOMS[(at + 1).min(PAGE_ZOOMS.len() - 1)]
    } else {
        PAGE_ZOOMS[at.saturating_sub(1)]
    }
}

/// fit_of is a picture's `Fit` in `window`: the dialog's stage, whose size
/// the window's alone says, and its own, None until that's known.
fn fit_of(natural: Option<(f32, f32)>, window: &Window) -> Option<Fit> {
    Some(Fit {
        stage: Large::stage_size(window.viewport_size()),
        natural: natural.filter(|(w, h)| *w > 0. && *h > 0.)?,
    })
}

struct Open {
    title: SharedString,
    what: What,
    /// The picture's own size, once known.
    natural: Option<(f32, f32)>,
    /// A page's zoom.
    page_zoom: f32,
    /// An SVG's sharp copy and the width it was drawn at; the width one is
    /// being drawn at; whether drawing failed (the chat's picture stays).
    sharp: Option<(u32, Arc<RenderImage>)>,
    drawing: Option<u32>,
    failed: bool,
    parsed: Option<Arc<ParsedSvg>>,
}

pub struct Large {
    open: Option<Open>,
    /// Counts the opens: a sharp copy drawn for an earlier one is dropped.
    seq: u64,
    focus: FocusHandle,
    /// What had the focus before it opened, given it back as it closes.
    before: Option<FocusHandle>,
    /// A picture's zoom and pan (1 fits it).
    view: Zoom,
    /// The stage where it was last painted, for the pointer's place on it.
    stage: Rc<Cell<Bounds<Pixels>>>,
    /// The window it is open in, which a page's keys reach it through: a
    /// webview with the keyboard can leave the window not the active one.
    window: Option<AnyWindowHandle>,
}

struct Handle(Entity<Large>);
impl Global for Handle {}

/// init makes the window's one, for app.rs to draw over everything else.
pub fn init(cx: &mut App) -> Entity<Large> {
    let large = cx.new(|cx| Large {
        open: None,
        seq: 0,
        focus: cx.focus_handle(),
        before: None,
        view: Zoom::default(),
        stage: Rc::default(),
        window: None,
    });
    cx.set_global(Handle(large.clone()));
    large
}

fn handle(cx: &App) -> Option<Entity<Large>> {
    cx.try_global::<Handle>().map(|h| h.0.clone())
}

/// open shows `what` large, named `title`.
pub fn open(title: impl Into<SharedString>, what: What, window: &mut Window, cx: &mut App) {
    let Some(large) = handle(cx) else {
        return;
    };
    let title = title.into();
    large.update(cx, |this, cx| this.show(title, what, window, cx));
}

/// open_image shows a picture sent in the chat: an SVG one as SVGs are.
pub fn open_image(image: Arc<Image>, name: String, window: &mut Window, cx: &mut App) {
    let what = if image.format() == ImageFormat::Svg {
        let svg: Arc<str> = String::from_utf8_lossy(image.bytes()).into();
        What::Svg {
            size: crate::rich::svg_size(&svg),
            svg,
            image,
        }
    } else {
        What::Image(image)
    };
    open(name, what, window, cx);
}

/// is_open says one is open: the window's own keys wait while it is.
pub fn is_open(cx: &App) -> bool {
    handle(cx).is_some_and(|l| l.read(cx).open.is_some())
}

/// page_command is a key or the wheel in the page open large, which has the
/// keyboard while it has the focus (webview.rs's LARGE_SCRIPT): close, in,
/// out or reset.
pub fn page_command(command: &str, cx: &mut App) {
    let Some(large) = handle(cx) else {
        return;
    };
    let Some(window) = large.read(cx).window else {
        return;
    };
    let _ = window.update(cx, |_, window, cx| {
        large.update(cx, |this, cx| match command {
            "close" => this.close(window, cx),
            "in" => this.zoom_by(true, None, cx),
            "out" => this.zoom_by(false, None, cx),
            "reset" => this.reset(cx),
            _ => {}
        })
    });
}

impl Large {
    fn show(
        &mut self,
        title: SharedString,
        what: What,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.drop_sharp(window, cx);
        self.seq += 1;
        self.view = Zoom::default();
        let natural = match &what {
            What::Svg { size, .. } => *size,
            _ => None,
        };
        if let What::Image(image) = &what {
            self.measure(image.clone(), cx);
        }
        self.open = Some(Open {
            title,
            what,
            natural,
            page_zoom: 1.,
            sharp: None,
            drawing: None,
            failed: false,
            parsed: None,
        });
        if self.before.is_none() {
            self.before = window.focused(cx);
        }
        self.window = Some(window.window_handle());
        self.focus.focus(window, cx);
        // A webview draws over everything: the chat's and the panel's hide
        // while this is over them.
        crate::webview::set_covered(true);
        cx.notify();
    }

    fn close(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.open.is_none() {
            return;
        }
        self.drop_sharp(window, cx);
        self.open = None;
        crate::webview::close_large();
        crate::webview::set_covered(false);
        if let Some(before) = self.before.take() {
            before.focus(window, cx);
        }
        cx.notify();
    }

    /// drop_sharp frees an SVG's sharp copy from the window's atlas: the
    /// window being updated is the one to name, which App doesn't hold then.
    fn drop_sharp(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if let Some((_, image)) = self.open.as_mut().and_then(|o| o.sharp.take()) {
            cx.drop_image(image, Some(window));
        }
    }

    /// measure reads a sent picture's own size, off the main thread.
    fn measure(&mut self, image: Arc<Image>, cx: &mut Context<Self>) {
        let seq = self.seq;
        let renderer = cx.svg_renderer();
        let size = cx.background_spawn(async move {
            // Decoded only to be measured: never painted, so it holds no
            // room in the window's atlas once dropped.
            let data = image.to_image_data(renderer).ok()?;
            let s = data.size(0);
            Some((s.width.0 as f32, s.height.0 as f32))
        });
        cx.spawn(async move |this, cx| {
            let size = size.await;
            this.update(cx, |this, cx| {
                if this.seq == seq
                    && let Some(o) = this.open.as_mut()
                {
                    o.natural = size.filter(|(w, h)| *w > 0. && *h > 0.);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// stage_size is the stage's size in a window of `viewport`: the
    /// dialog's, less its header and border.
    fn stage_size(viewport: Size<Pixels>) -> (f32, f32) {
        let (w, h) = (f32::from(viewport.width), f32::from(viewport.height));
        let card = ((w - 48.).clamp(240., MAX_WIDTH), (h * 0.86).max(200.));
        (card.0 - 2., card.1 - HEAD - 2.)
    }

    fn fit(&self, window: &Window) -> Option<Fit> {
        fit_of(self.open.as_ref()?.natural, window)
    }

    /// zoom_by is a step in or out: a page's next zoom, a picture's step.
    fn zoom_by(&mut self, up: bool, window: Option<&Window>, cx: &mut Context<Self>) {
        let Some(o) = self.open.as_mut() else {
            return;
        };
        if let What::Page { .. } = o.what {
            o.page_zoom = page_step(o.page_zoom, up);
            cx.notify();
            return;
        }
        // From a page's key there is no window, and no picture then.
        let Some(window) = window else {
            return;
        };
        if self.view.step(up, fit_of(o.natural, window)) {
            cx.notify();
        }
    }

    fn reset(&mut self, cx: &mut Context<Self>) {
        let Some(o) = self.open.as_mut() else {
            return;
        };
        o.page_zoom = 1.;
        self.view.reset();
        cx.notify();
    }

    /// want_sharp has an SVG drawn again for `device` pixels across, when
    /// the copy it has isn't that one and none is being drawn.
    fn want_sharp(&mut self, device: f32, cx: &mut Context<Self>) {
        let seq = self.seq;
        let Some(o) = self.open.as_mut() else {
            return;
        };
        let (What::Svg { svg, .. }, Some(natural)) = (&o.what, o.natural) else {
            return;
        };
        let width = sharp_width(natural, device);
        if o.failed || o.drawing.is_some() || o.sharp.as_ref().is_some_and(|(w, _)| *w == width) {
            return;
        }
        o.drawing = Some(width);
        let (svg, parsed, renderer) = (svg.clone(), o.parsed.clone(), cx.svg_renderer());
        let drawn = cx.background_spawn(async move {
            let parsed = match parsed {
                Some(p) => p,
                None => Arc::new(renderer.parse_svg(svg.as_bytes()).ok()?),
            };
            let size = SvgSize::Size(size(DevicePixels(width as i32), DevicePixels(0)));
            let image = renderer.render_parsed(&parsed, size).ok()?;
            Some((parsed, image))
        });
        cx.spawn(async move |this, cx| {
            let drawn = drawn.await;
            this.update(cx, |this, cx| {
                let Some(o) = this.open.as_mut().filter(|_| this.seq == seq) else {
                    return;
                };
                o.drawing = None;
                match drawn {
                    Some((parsed, image)) => {
                        o.parsed = Some(parsed);
                        if let Some((_, old)) = o.sharp.replace((width, image)) {
                            cx.drop_image(old, None);
                        }
                    }
                    None => o.failed = true,
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn on_key(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let k = &ev.keystroke;
        if k.modifiers.alt {
            return;
        }
        match k.key.as_str() {
            "escape" => self.close(window, cx),
            "+" | "=" => self.zoom_by(true, Some(window), cx),
            "-" | "_" => self.zoom_by(false, Some(window), cx),
            "0" => self.reset(cx),
            _ => return,
        }
        cx.stop_propagation();
    }

    /// controls is ZoomableImage's bar at the stage's corner: −, the zoom of
    /// its own pixels, +, Fit.
    fn controls(&self, fit: Option<Fit>, cx: &mut Context<Self>) -> AnyElement {
        let scale = self.view.scale;
        let at_fit = !self.view.zoomed();
        let at_max = fit.is_none_or(|f| scale >= f.max() - 1e-3);
        let percent = (scale * fit.map_or(1., |f| f.ratio()) * 100.).round();
        h_flex()
            .id("large-zoom")
            .absolute()
            .bottom_3()
            .right_3()
            .items_center()
            .gap_0p5()
            .p_0p5()
            .rounded_lg()
            .border_1()
            .border_color(theme::line())
            .bg(theme::overlay())
            .shadow(theme::shadow_pop())
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .child(tool(
                "large-zoom-out",
                "minus",
                t("agent.mediaTab.zoomOut"),
                at_fit,
                cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.zoom_by(false, Some(window), cx)
                }),
            ))
            .child(
                div()
                    .w(px(44.))
                    .text_center()
                    .text_xs()
                    .font_features(theme::tabular_features())
                    .text_color(theme::secondary())
                    .child(format!("{percent}%")),
            )
            .child(tool(
                "large-zoom-in",
                "plus",
                t("agent.mediaTab.zoomIn"),
                at_max,
                cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.zoom_by(true, Some(window), cx)
                }),
            ))
            .child(
                tool(
                    "large-zoom-fit",
                    "maximize",
                    t("agent.mediaTab.zoomFit"),
                    at_fit,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.reset(cx)),
                )
                .w_auto()
                .px_2()
                .gap_1p5()
                .text_xs()
                .child(t("agent.mediaTab.zoomFit")),
            )
            .into_any_element()
    }

    /// page_controls is ZoomControls: −, the page's zoom (back to 100% on a
    /// click), +.
    fn page_controls(&self, zoom: f32, cx: &mut Context<Self>) -> AnyElement {
        let first = zoom <= PAGE_ZOOMS[0] + 1e-3;
        let last = zoom >= PAGE_ZOOMS[PAGE_ZOOMS.len() - 1] - 1e-3;
        let reset = SharedString::from(t("chat.rich.zoomReset"));
        h_flex()
            .flex_none()
            .items_center()
            .gap_0p5()
            .child(tool(
                "large-page-out",
                "minus",
                t("chat.rich.zoomOut"),
                first,
                cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.zoom_by(false, Some(window), cx)
                }),
            ))
            .child(
                div()
                    .id("large-page-reset")
                    .h_7()
                    .min_w(px(56.))
                    .px_2()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .cursor_pointer()
                    .text_sm()
                    .font_features(theme::tabular_features())
                    .text_color(theme::secondary())
                    .hover(|s| s.bg(theme::surface_raised()))
                    .child(format!("{}%", (zoom * 100.).round()))
                    .tooltip(move |w, cx| {
                        gpui_component::tooltip::Tooltip::new(reset.clone()).build(w, cx)
                    })
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.reset(cx))),
            )
            .child(tool(
                "large-page-in",
                "plus",
                t("chat.rich.zoomIn"),
                last,
                cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.zoom_by(true, Some(window), cx)
                }),
            ))
            .into_any_element()
    }

    /// picture is the stage of a picture: it at its zoom and pan, zoomed and
    /// panned as zoom.rs does, the zoom's bar over it.
    fn picture(&mut self, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let fit = self.fit(window);
        let scale_factor = window.scale_factor();
        if let Some(fit) = fit {
            let device = fit.shown(self.view.scale).0 * scale_factor;
            self.want_sharp(device, cx);
        }
        let Some(o) = self.open.as_ref() else {
            return div().into_any_element();
        };
        let source: ImageSource = match (&o.what, &o.sharp) {
            (What::Svg { .. }, Some((_, sharp))) => sharp.clone().into(),
            (What::Svg { image, .. }, None) | (What::Image(image), _) => image.clone().into(),
            (What::Page { .. }, _) => return div().into_any_element(),
        };
        let natural = o.natural;
        let fit_now = zoom::fit_fn(move |window| fit_of(natural, window));
        let stage = div()
            .id("large-stage")
            .relative()
            .flex_1()
            .min_h_0()
            .overflow_hidden()
            // GPUI clips square: the card's corners are the stage's own.
            .rounded_b(STAGE_CORNER)
            // The well as the chat shows it, a diagram's own ground.
            .bg(theme::solid(theme::well()))
            .child(zoom::placed(fit, &self.view, source, None));
        zoom::attach(
            stage,
            &self.view,
            |this: &mut Large| &mut this.view,
            &self.stage,
            fit_now,
            true,
            cx,
        )
        .child(self.controls(fit, cx))
        .into_any_element()
    }

    /// page is the stage of a page: a webview of its own at the page's zoom
    /// where the chat's are webviews, a picture of it where they are
    /// pictures (Wayland), and its source where neither can be.
    fn page(&self, id: &str, html: &str, zoom: f32, window: &mut Window) -> AnyElement {
        let stage = div()
            .id("large-stage")
            .relative()
            .flex_1()
            .min_h_0()
            .rounded_b(STAGE_CORNER)
            .bg(theme::card());
        #[cfg(target_os = "linux")]
        if crate::snapshot::wanted(window) {
            return stage
                .overflow_y_scroll()
                .child(crate::snapshot::html_snapshot(
                    id.to_string(),
                    html.to_string(),
                ))
                .into_any_element();
        }
        #[cfg(not(target_os = "linux"))]
        let _ = window;
        if !crate::webview::available() {
            return stage
                .overflow_y_scroll()
                .p_4()
                .text_xs()
                .font_family(theme::mono())
                .text_color(theme::muted())
                .child(html.to_string())
                .into_any_element();
        }
        stage
            .overflow_hidden()
            .child(crate::webview::large_page(
                id.to_string(),
                html.to_string(),
                zoom,
            ))
            .into_any_element()
    }
}

/// tool is a button of the zoom's bars (Button size icon-sm, ghost): an
/// icon, its name as a tooltip, faint while it can't be pressed.
fn tool(
    id: &'static str,
    icon: &'static str,
    tip: String,
    disabled: bool,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> Stateful<Div> {
    let tip = SharedString::from(tip);
    div()
        .id(id)
        .h_7()
        .w_7()
        .flex_none()
        .flex()
        .items_center()
        .justify_center()
        .rounded_md()
        .text_color(theme::secondary())
        .child(crate::icons::ic(icon).size(px(14.)))
        .tooltip(move |w, cx| gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx))
        .map(|el| {
            if disabled {
                el.opacity(0.4)
            } else {
                el.cursor_pointer()
                    .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                    .active(|s| s.bg(theme::surface_strong()))
                    .on_click(move |ev, window, cx| {
                        cx.stop_propagation();
                        on_click(ev, window, cx)
                    })
            }
        })
}

impl Render for Large {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let Some(o) = self.open.as_ref() else {
            return div().into_any_element();
        };
        let title = o.title.clone();
        let page = match &o.what {
            What::Page { id, html } => Some((id.clone(), html.clone(), o.page_zoom)),
            _ => None,
        };
        let (cw, ch) = Self::stage_size(window.viewport_size());
        let stage = match &page {
            Some((id, html, zoom)) => self.page(id, html, *zoom, window),
            None => self.picture(window, cx),
        };
        let close = cx.listener(|this, _: &ClickEvent, window, cx| this.close(window, cx));
        let backdrop = cx.listener(|this, _: &MouseDownEvent, window, cx| this.close(window, cx));
        let card = v_flex()
            .id("large-card")
            .track_focus(&self.focus)
            .w(px(cw + 2.))
            .h(px(ch + HEAD + 2.))
            .rounded(theme::radius::XXL)
            .overflow_hidden()
            .bg(theme::card())
            .border_1()
            .border_color(theme::line_strong())
            .shadow(theme::shadow_modal())
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .on_key_down(cx.listener(Self::on_key))
            .child(
                h_flex()
                    .h(px(HEAD))
                    .flex_none()
                    .items_center()
                    .gap_3()
                    .pl_5()
                    .pr_2()
                    .border_b_1()
                    .border_color(theme::line_faint())
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_sm()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(title),
                    )
                    .when_some(page.as_ref(), |el, (_, _, zoom)| {
                        el.child(self.page_controls(*zoom, cx))
                    })
                    .child(tool("large-close", "x", t("common.close"), false, close)),
            )
            .child(
                div()
                    .relative()
                    .flex_1()
                    .min_h_0()
                    .flex()
                    .flex_col()
                    .child(stage),
            );
        crate::motion::scrim_in(
            div()
                .id("large-backdrop")
                .absolute()
                .inset_0()
                .bg(theme::scrim())
                .flex()
                .items_center()
                .justify_center()
                .occlude()
                .on_mouse_down(MouseButton::Left, backdrop)
                .child(crate::motion::pop_in(card, "large-card-in")),
            "large-backdrop-in",
        )
        .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{page_step, sharp_width};

    #[test]
    fn an_svg_is_drawn_again_in_steps() {
        // Shown at 1100 device pixels across, a 4000-wide diagram is drawn
        // 2000 wide; at 3000, 4000; at 9000, as wide as MAX_PIXELS lets.
        assert_eq!(sharp_width((4000., 1000.), 1100.), 2000);
        assert_eq!(sharp_width((4000., 1000.), 3000.), 4000);
        assert_eq!(sharp_width((4000., 1000.), 9000.), 8000);
        assert_eq!(sharp_width((4000., 1000.), 30000.), 8000);
        // A tall one keeps to MAX_SIDE high.
        assert_eq!(sharp_width((1000., 4000.), 4000.), 2000);
    }

    #[test]
    fn a_page_zooms_in_steps() {
        assert_eq!(page_step(1., true), 1.1);
        assert_eq!(page_step(1., false), 0.9);
        assert_eq!(page_step(2., true), 2.);
        assert_eq!(page_step(0.5, false), 0.5);
    }
}
