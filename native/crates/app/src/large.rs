//! A chat's picture or page opened large: RichBlock.tsx's RichDialog, a
//! dialog over the window, with ZoomableImage.tsx's zoom for a picture (a
//! diagram, an ```svg block, an image sent in the chat) and ZoomControls'
//! for a page.
//!
//! A picture fits the stage at first, never past its own size. The buttons,
//! Ctrl (or ⌘) with the wheel or + / − / 0, a pinch and a double-click zoom
//! it, keeping the point under the pointer still; a drag or the wheel pans
//! it. An SVG is drawn again, sharp, at the size it is shown (in steps of
//! twice its own width, up to MAX_PIXELS), where GPUI would stretch its one
//! picture of it. Escape, the ✕ or a click beside the dialog closes it.
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

/// The most a picture zooms in, as a multiple of its own pixels, and a step
/// of the buttons and keys (ZoomableImage's MAX_ACTUAL and STEP).
const MAX_ACTUAL: f32 = 8.;
const STEP: f32 = 1.25;
/// The zooms a page steps through (RichBlock's ZOOMS).
const PAGE_ZOOMS: [f32; 10] = [0.5, 0.67, 0.8, 0.9, 1., 1.1, 1.25, 1.5, 1.75, 2.];
/// RichDialog's size: h-[86vh] w-[calc(100vw-3rem)] max-w-[1180px].
const MAX_WIDTH: f32 = 1180.;
/// The header's height; the stage is the rest.
const HEAD: f32 = 48.;
/// The room around a picture fitted to the stage.
const PAD: f32 = 16.;
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

/// Fit is what a picture's zoom is worked out from: the stage's size and
/// the picture's own, in logical pixels.
#[derive(Clone, Copy, Debug)]
struct Fit {
    stage: (f32, f32),
    natural: (f32, f32),
}

impl Fit {
    /// ratio is the picture's pixels to its own at a zoom of 1: as large as
    /// the stage allows, less PAD, never past its own size.
    fn ratio(&self) -> f32 {
        let (bw, bh) = (
            (self.stage.0 - 2. * PAD).max(1.),
            (self.stage.1 - 2. * PAD).max(1.),
        );
        (bw / self.natural.0).min(bh / self.natural.1).min(1.)
    }

    /// max is the most zoom: MAX_ACTUAL of its own pixels, or twice the
    /// fitted size where that is less.
    fn max(&self) -> f32 {
        (MAX_ACTUAL / self.ratio()).max(2.)
    }

    /// shown is the picture's size at a zoom.
    fn shown(&self, scale: f32) -> (f32, f32) {
        let r = self.ratio() * scale;
        (self.natural.0 * r, self.natural.1 * r)
    }

    /// clamp keeps a zoom between fit and the most, and its pan (from the
    /// stage's middle) from taking the picture's edge in past the stage's.
    fn clamp(&self, scale: f32, pan: (f32, f32)) -> (f32, (f32, f32)) {
        let s = scale.clamp(1., self.max());
        if s <= 1. {
            return (1., (0., 0.));
        }
        let (w, h) = self.shown(s);
        let lx = ((w - self.stage.0) / 2.).max(0.);
        let ly = ((h - self.stage.1) / 2.).max(0.);
        (s, (pan.0.clamp(-lx, lx), pan.1.clamp(-ly, ly)))
    }

    /// zoom_at is a new zoom that keeps the point `at` (from the stage's
    /// middle) where it is.
    fn zoom_at(&self, from: (f32, (f32, f32)), scale: f32, at: (f32, f32)) -> (f32, (f32, f32)) {
        let s = scale.clamp(1., self.max());
        let k = s / from.0;
        let pan = (at.0 - (at.0 - from.1.0) * k, at.1 - (at.1 - from.1.1) * k);
        self.clamp(s, pan)
    }
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

struct Open {
    title: SharedString,
    what: What,
    /// The picture's own size, once known.
    natural: Option<(f32, f32)>,
    /// The zoom (1 fits it) and the pan from the stage's middle.
    scale: f32,
    pan: (f32, f32),
    /// Where a drag that pans was last.
    drag: Option<Point<Pixels>>,
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
            scale: 1.,
            pan: (0., 0.),
            drag: None,
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
        let o = self.open.as_ref()?;
        Some(Fit {
            stage: Self::stage_size(window.viewport_size()),
            natural: o.natural.filter(|(w, h)| *w > 0. && *h > 0.)?,
        })
    }

    /// at is a point of the window from the stage's middle.
    fn at(&self, p: Point<Pixels>) -> (f32, f32) {
        let b = self.stage.get();
        let c = b.center();
        (f32::from(p.x - c.x), f32::from(p.y - c.y))
    }

    fn set_view(&mut self, (scale, pan): (f32, (f32, f32)), cx: &mut Context<Self>) {
        if let Some(o) = self.open.as_mut()
            && (o.scale != scale || o.pan != pan)
        {
            o.scale = scale;
            o.pan = pan;
            cx.notify();
        }
    }

    /// zoom_to zooms a picture to `scale`, keeping `at` still (the stage's
    /// middle when None).
    fn zoom_to(
        &mut self,
        scale: f32,
        at: Option<(f32, f32)>,
        window: &Window,
        cx: &mut Context<Self>,
    ) {
        let (Some(fit), Some(o)) = (self.fit(window), self.open.as_ref()) else {
            return;
        };
        let view = fit.zoom_at((o.scale, o.pan), scale, at.unwrap_or((0., 0.)));
        self.set_view(view, cx);
    }

    /// zoom_by is a step in or out: a page's next zoom, a picture's STEP.
    fn zoom_by(&mut self, up: bool, window: Option<&Window>, cx: &mut Context<Self>) {
        let Some(o) = self.open.as_mut() else {
            return;
        };
        if let What::Page { .. } = o.what {
            o.page_zoom = page_step(o.page_zoom, up);
            cx.notify();
            return;
        }
        let scale = if up { o.scale * STEP } else { o.scale / STEP };
        match window {
            Some(window) => self.zoom_to(scale, None, window, cx),
            None => {
                // From a page's key: there is no picture then.
            }
        }
    }

    fn reset(&mut self, cx: &mut Context<Self>) {
        let Some(o) = self.open.as_mut() else {
            return;
        };
        o.page_zoom = 1.;
        o.scale = 1.;
        o.pan = (0., 0.);
        o.drag = None;
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
    fn controls(&self, o: &Open, fit: Option<Fit>, cx: &mut Context<Self>) -> AnyElement {
        let at_fit = o.scale <= 1.001;
        let at_max = fit.is_none_or(|f| o.scale >= f.max() - 1e-3);
        let percent = (o.scale * fit.map_or(1., |f| f.ratio()) * 100.).round();
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

    /// picture is the stage of a picture: it at its zoom and pan, the drag,
    /// the wheel and a double-click on it, the zoom's bar over it.
    fn picture(&mut self, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let fit = self.fit(window);
        let scale_factor = window.scale_factor();
        if let (Some(fit), Some(o)) = (fit, self.open.as_ref()) {
            let device = fit.shown(o.scale).0 * scale_factor;
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
        let zoomed = o.scale > 1.001;
        let dragging = o.drag.is_some();
        let picture = match fit {
            Some(fit) => {
                let (w, h) = fit.shown(o.scale);
                let (sw, sh) = fit.stage;
                div()
                    .absolute()
                    .left(px(sw / 2. + o.pan.0 - w / 2.))
                    .top(px(sh / 2. + o.pan.1 - h / 2.))
                    .w(px(w))
                    .h(px(h))
                    .child(img(source).size_full())
            }
            // Its size isn't known yet: fitted, as the chat drew it.
            None => div()
                .absolute()
                .top(px(PAD))
                .left(px(PAD))
                .right(px(PAD))
                .bottom(px(PAD))
                .child(img(source).size_full().object_fit(ObjectFit::Contain)),
        };
        div()
            .id("large-stage")
            .relative()
            .flex_1()
            .min_h_0()
            .overflow_hidden()
            // GPUI clips square: the card's corners are the stage's own.
            .rounded_b(STAGE_CORNER)
            // The well as the chat shows it, a diagram's own ground.
            .bg(theme::solid(theme::well()))
            .when(zoomed, |el| {
                el.cursor(if dragging {
                    CursorStyle::ClosedHand
                } else {
                    CursorStyle::OpenHand
                })
            })
            .child(picture)
            .on_scroll(cx.listener(|this, ev: &ScrollEvent, window, cx| {
                let d = ev.delta.pixel_delta(px(16.));
                let (dx, dy) = (f32::from(d.x), f32::from(d.y));
                let Some((scale, pan)) = this.open.as_ref().map(|o| (o.scale, o.pan)) else {
                    return;
                };
                if ev.modifiers.control || ev.modifiers.platform {
                    // A notch of the wheel is about a step of the buttons.
                    let k = (dy.clamp(-100., 100.) * 0.005).exp();
                    let at = this.at(ev.position);
                    this.zoom_to(scale * k, Some(at), window, cx);
                } else if let Some(fit) = this.fit(window) {
                    // The wheel pans, Shift's sideways.
                    let (dx, dy) = if ev.modifiers.shift && dx == 0. {
                        (dy, 0.)
                    } else {
                        (dx, dy)
                    };
                    let view = fit.clamp(scale, (pan.0 + dx, pan.1 + dy));
                    this.set_view(view, cx);
                }
            }))
            .on_pinch(cx.listener(|this, ev: &PinchEvent, window, cx| {
                let Some(scale) = this.open.as_ref().map(|o| o.scale) else {
                    return;
                };
                let at = this.at(ev.position);
                this.zoom_to(scale * (1. + ev.delta), Some(at), window, cx);
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, ev: &MouseDownEvent, window, cx| {
                    let Some(scale) = this.open.as_ref().map(|o| o.scale) else {
                        return;
                    };
                    // A double-click is the second click (the DOM's
                    // dblclick), not every one after it.
                    if ev.click_count == 2 {
                        // ZoomableImage's double-click: fit again, or its own
                        // size (twice that when its own size fits).
                        if scale > 1.001 {
                            this.reset(cx);
                        } else if let Some(fit) = this.fit(window) {
                            let own = 1. / fit.ratio();
                            let to = if own > 1.001 { own } else { 2. * own };
                            let at = this.at(ev.position);
                            this.zoom_to(to, Some(at), window, cx);
                        }
                    } else if ev.click_count == 1
                        && scale > 1.001
                        && let Some(o) = this.open.as_mut()
                    {
                        o.drag = Some(ev.position);
                        cx.notify();
                    }
                }),
            )
            .on_mouse_move(cx.listener(|this, ev: &MouseMoveEvent, window, cx| {
                let Some(o) = this.open.as_mut() else { return };
                let Some(from) = o.drag else { return };
                if ev.pressed_button != Some(MouseButton::Left) {
                    o.drag = None;
                    cx.notify();
                    return;
                }
                let d = ev.position - from;
                o.drag = Some(ev.position);
                let (scale, pan) = (o.scale, o.pan);
                if let Some(fit) = this.fit(window) {
                    let view = fit.clamp(scale, (pan.0 + f32::from(d.x), pan.1 + f32::from(d.y)));
                    this.set_view(view, cx);
                }
            }))
            .on_mouse_up(
                MouseButton::Left,
                cx.listener(|this, _: &MouseUpEvent, _, cx| {
                    if let Some(o) = this.open.as_mut()
                        && o.drag.take().is_some()
                    {
                        cx.notify();
                    }
                }),
            )
            .child(self.controls(o, fit, cx))
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
        let record = self.stage.clone();
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
                    .child(stage)
                    // Where the stage is, for the pointer's place on it.
                    .child(
                        canvas(move |bounds, _, _| record.set(bounds), |_, _, _, _| {})
                            .absolute()
                            .size_full(),
                    ),
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
    use super::{Fit, page_step, sharp_width};

    const WIDE: Fit = Fit {
        stage: (1132., 680.),
        natural: (4000., 1000.),
    };

    #[test]
    fn a_picture_fits_without_growing() {
        // 1100 of 4000 across, the stage's width less the room around it.
        assert!((WIDE.ratio() - 1100. / 4000.).abs() < 1e-6);
        let small = Fit {
            stage: (1132., 680.),
            natural: (300., 200.),
        };
        assert_eq!(small.ratio(), 1.);
        // It zooms to 8 of its own pixels, or twice fitted when that's less.
        assert!((WIDE.max() - 8. / WIDE.ratio()).abs() < 1e-3);
        assert_eq!(small.max(), 8.);
    }

    #[test]
    fn a_zoom_keeps_its_point_and_its_edges() {
        // Fitted, there's no pan.
        assert_eq!(WIDE.clamp(0.5, (40., 40.)), (1., (0., 0.)));
        // Twice fitted, 2200x550 in a 1132x680 stage: it pans across but
        // not down, and not past its edges.
        let (s, pan) = WIDE.clamp(2., (900., 30.));
        assert_eq!(s, 2.);
        assert!(
            (pan.0 - (2200. - 1132.) / 2.).abs() < 1e-3 && pan.1 == 0.,
            "{pan:?}"
        );
        // The point under the pointer stays where it is.
        let at = (200., 0.);
        let (s, pan) = WIDE.zoom_at((1., (0., 0.)), 2., at);
        assert_eq!(s, 2.);
        let before = at.0 / WIDE.shown(1.).0;
        let after = (at.0 - pan.0) / WIDE.shown(s).0;
        assert!((before - after).abs() < 1e-5, "{before} {after}");
        // Past the most it stops there.
        assert_eq!(WIDE.zoom_at((1., (0., 0.)), 1e6, (0., 0.)).0, WIDE.max());
    }

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
