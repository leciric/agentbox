//! A picture's zoom and pan over a stage, shared by the chat's large view
//! (large.rs) and the review's stage (app/review.rs), so a picture is
//! shown and moved the same way in both: ZoomableImage.tsx's.
//!
//! A picture fits the stage at first, whole, never past its own size. The
//! buttons, Ctrl (or ⌘) with the wheel or + / − / 0, a pinch and a double-click
//! zoom it, keeping the point under the pointer still; a drag or the wheel
//! pans it.
//!
//! `Fit` is the arithmetic (pure, tested); `Zoom` is the view: its zoom and
//! pan, and what each event does to them; `attach` wires a stage's events to
//! a `Zoom` held by whatever view draws the stage; `placed` is the picture on
//! it.
use std::cell::Cell;
use std::rc::Rc;

use gpui::prelude::FluentBuilder as _;
use gpui::*;

/// The most a picture zooms in, as a multiple of its own pixels, and a step
/// of the buttons and keys (ZoomableImage's MAX_ACTUAL and STEP).
pub const MAX_ACTUAL: f32 = 8.;
pub const STEP: f32 = 1.25;
/// The room around a picture fitted to the stage.
pub const PAD: f32 = 16.;
/// A zoom this close to fit is fit.
const NEAR_FIT: f32 = 1.001;

/// Fit is what a picture's zoom is worked out from: the stage's size and
/// the picture's own, in logical pixels.
#[derive(Clone, Copy, Debug)]
pub struct Fit {
    pub stage: (f32, f32),
    pub natural: (f32, f32),
}

impl Fit {
    /// ratio is the picture's pixels to its own at a zoom of 1: as large as
    /// the stage allows, less PAD, never past its own size.
    pub fn ratio(&self) -> f32 {
        let (bw, bh) = (
            (self.stage.0 - 2. * PAD).max(1.),
            (self.stage.1 - 2. * PAD).max(1.),
        );
        (bw / self.natural.0).min(bh / self.natural.1).min(1.)
    }

    /// max is the most zoom: MAX_ACTUAL of its own pixels, or twice the
    /// fitted size where that is less.
    pub fn max(&self) -> f32 {
        (MAX_ACTUAL / self.ratio()).max(2.)
    }

    /// shown is the picture's size at a zoom.
    pub fn shown(&self, scale: f32) -> (f32, f32) {
        let r = self.ratio() * scale;
        (self.natural.0 * r, self.natural.1 * r)
    }

    /// clamp keeps a zoom between fit and the most, and its pan (from the
    /// stage's middle) from taking the picture's edge in past the stage's.
    pub fn clamp(&self, scale: f32, pan: (f32, f32)) -> (f32, (f32, f32)) {
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
    pub fn zoom_at(
        &self,
        from: (f32, (f32, f32)),
        scale: f32,
        at: (f32, f32),
    ) -> (f32, (f32, f32)) {
        let s = scale.clamp(1., self.max());
        let k = s / from.0;
        let pan = (at.0 - (at.0 - from.1.0) * k, at.1 - (at.1 - from.1.1) * k);
        self.clamp(s, pan)
    }
}

/// FitFn makes a picture's `Fit` on the frame's stage, None while its size
/// isn't known: what `attach` reads for each event, so it must not borrow
/// the view that draws the stage.
pub type FitFn = Rc<dyn Fn(&Window) -> Option<Fit>>;

/// fit_fn is a `FitFn` from a closure.
pub fn fit_fn(f: impl Fn(&Window) -> Option<Fit> + 'static) -> FitFn {
    Rc::new(f)
}

/// Zoom is how far a picture is zoomed (1 fits it) and where: its pan from
/// the stage's middle, and where a drag that pans was last. Each event's
/// method says whether it changed anything, for the view to redraw.
#[derive(Clone, Copy, Debug)]
pub struct Zoom {
    pub scale: f32,
    pub pan: (f32, f32),
    drag: Option<Point<Pixels>>,
}

impl Default for Zoom {
    fn default() -> Self {
        Self {
            scale: 1.,
            pan: (0., 0.),
            drag: None,
        }
    }
}

impl Zoom {
    /// zoomed is past fit.
    pub fn zoomed(&self) -> bool {
        self.scale > NEAR_FIT
    }

    pub fn dragging(&self) -> bool {
        self.drag.is_some()
    }

    /// shown is the zoom and pan as drawn: kept inside what `fit` allows,
    /// which a stage that changed size since may have moved.
    pub fn shown(&self, fit: Fit) -> (f32, (f32, f32)) {
        fit.clamp(self.scale, self.pan)
    }

    fn set(&mut self, (scale, pan): (f32, (f32, f32))) -> bool {
        let changed = self.scale != scale || self.pan != pan;
        self.scale = scale;
        self.pan = pan;
        changed
    }

    /// reset is back to fit.
    pub fn reset(&mut self) -> bool {
        let was = *self;
        *self = Self::default();
        was.scale != self.scale || was.pan != self.pan || was.drag.is_some()
    }

    /// zoom_to zooms to `scale`, keeping `at` (from the stage's middle, its
    /// middle when None) still.
    pub fn zoom_to(&mut self, scale: f32, at: Option<(f32, f32)>, fit: Option<Fit>) -> bool {
        let Some(fit) = fit else {
            return false;
        };
        let view = fit.zoom_at((self.scale, self.pan), scale, at.unwrap_or((0., 0.)));
        self.set(view)
    }

    /// step is a step in or out of the buttons and keys.
    pub fn step(&mut self, up: bool, fit: Option<Fit>) -> bool {
        let scale = if up {
            self.scale * STEP
        } else {
            self.scale / STEP
        };
        self.zoom_to(scale, None, fit)
    }

    /// scroll is the wheel: with Ctrl (or ⌘) a zoom at the pointer, a notch
    /// about a step; else a pan, Shift's sideways.
    pub fn scroll(&mut self, ev: &ScrollEvent, at: (f32, f32), fit: Option<Fit>) -> bool {
        let Some(fit) = fit else {
            return false;
        };
        let d = ev.delta.pixel_delta(px(16.));
        let (dx, dy) = (f32::from(d.x), f32::from(d.y));
        if ev.modifiers.control || ev.modifiers.platform {
            let k = (dy.clamp(-100., 100.) * 0.005).exp();
            return self.zoom_to(self.scale * k, Some(at), Some(fit));
        }
        let (dx, dy) = if ev.modifiers.shift && dx == 0. {
            (dy, 0.)
        } else {
            (dx, dy)
        };
        let view = fit.clamp(self.scale, (self.pan.0 + dx, self.pan.1 + dy));
        self.set(view)
    }

    /// pinch is a trackpad's.
    pub fn pinch(&mut self, ev: &PinchEvent, at: (f32, f32), fit: Option<Fit>) -> bool {
        self.zoom_to(self.scale * (1. + ev.delta), Some(at), fit)
    }

    /// press is a click: its second (the DOM's dblclick, not every one after
    /// it) is ZoomableImage's double-click, fit again or its own size (twice
    /// that when its own size fits); its first starts a drag where `drags`
    /// and there is something to pan.
    pub fn press(
        &mut self,
        ev: &MouseDownEvent,
        at: (f32, f32),
        fit: Option<Fit>,
        drags: bool,
    ) -> bool {
        if ev.click_count == 2 {
            if self.zoomed() {
                return self.reset();
            }
            let Some(f) = fit else {
                return false;
            };
            let own = 1. / f.ratio();
            let to = if own > NEAR_FIT { own } else { 2. * own };
            return self.zoom_to(to, Some(at), fit);
        }
        if ev.click_count == 1 && drags && self.zoomed() {
            self.drag = Some(ev.position);
            return true;
        }
        false
    }

    /// drag moves a drag that pans, which a button let go of elsewhere ends.
    pub fn drag(&mut self, ev: &MouseMoveEvent, fit: Option<Fit>) -> bool {
        let Some(from) = self.drag else {
            return false;
        };
        if ev.pressed_button != Some(MouseButton::Left) {
            self.drag = None;
            return true;
        }
        let d = ev.position - from;
        self.drag = Some(ev.position);
        let Some(fit) = fit else {
            return false;
        };
        let view = fit.clamp(
            self.scale,
            (self.pan.0 + f32::from(d.x), self.pan.1 + f32::from(d.y)),
        );
        self.set(view)
    }

    /// release ends the drag.
    pub fn release(&mut self) -> bool {
        self.drag.take().is_some()
    }
}

/// at is a point of the window from the middle of a stage's bounds.
fn from_middle(bounds: Bounds<Pixels>, p: Point<Pixels>) -> (f32, f32) {
    let c = bounds.center();
    (f32::from(p.x - c.x), f32::from(p.y - c.y))
}

/// attach makes `stage` of a view `T` the place a picture is zoomed and
/// panned: the wheel, a pinch, the clicks and a drag, as `Zoom` says. `view`
/// is the view's `Zoom` for them to change, `state` its current one, for the
/// cursor; `fit` makes the picture's `Fit` on this frame's stage, None while
/// its size isn't known; `drags` is whether a drag pans (a stage with a
/// drag of its own, a divider, says no). The stage's bounds are kept in
/// `bounds`, the view drawing again when its size changes, for `fit` to read.
pub fn attach<T: 'static>(
    stage: Stateful<Div>,
    state: &Zoom,
    view: fn(&mut T) -> &mut Zoom,
    bounds: &Rc<Cell<Bounds<Pixels>>>,
    fit: FitFn,
    drags: bool,
    cx: &mut Context<T>,
) -> Stateful<Div> {
    let (zoomed, dragging) = (state.zoomed() && drags, state.dragging());
    let measure = {
        let (bounds, me) = (bounds.clone(), cx.entity().downgrade());
        canvas(
            move |b, window, _| {
                // Measured: the picture takes its size next frame.
                if bounds.get().size != b.size {
                    let me = me.clone();
                    window.on_next_frame(move |_, cx| {
                        me.update(cx, |_, cx| cx.notify()).ok();
                    });
                }
                bounds.set(b)
            },
            |_, _, _, _| {},
        )
        .absolute()
        .size_full()
    };
    let (on_scroll, on_pinch, on_down, on_move) = (
        (bounds.clone(), fit.clone()),
        (bounds.clone(), fit.clone()),
        (bounds.clone(), fit.clone()),
        fit,
    );
    stage
        .when(zoomed, |el| {
            el.cursor(if dragging {
                CursorStyle::ClosedHand
            } else {
                CursorStyle::OpenHand
            })
        })
        .child(measure)
        .on_scroll(cx.listener(move |this, ev: &ScrollEvent, window, cx| {
            let at = from_middle(on_scroll.0.get(), ev.position);
            if view(this).scroll(ev, at, (on_scroll.1)(window)) {
                cx.notify();
            }
        }))
        .on_pinch(cx.listener(move |this, ev: &PinchEvent, window, cx| {
            let at = from_middle(on_pinch.0.get(), ev.position);
            if view(this).pinch(ev, at, (on_pinch.1)(window)) {
                cx.notify();
            }
        }))
        .on_mouse_down(
            MouseButton::Left,
            cx.listener(move |this, ev: &MouseDownEvent, window, cx| {
                let at = from_middle(on_down.0.get(), ev.position);
                if view(this).press(ev, at, (on_down.1)(window), drags) {
                    cx.notify();
                }
            }),
        )
        .on_mouse_move(cx.listener(move |this, ev: &MouseMoveEvent, window, cx| {
            if view(this).drag(ev, on_move(window)) {
                cx.notify();
            }
        }))
        .on_mouse_up(
            MouseButton::Left,
            cx.listener(move |this, _: &MouseUpEvent, _, cx| {
                if view(this).release() {
                    cx.notify();
                }
            }),
        )
}

/// placed is a picture on its stage at its zoom and pan, whole at fit; with
/// an `overlay` (the changes a comparison marks) over it, at its size. Where
/// the picture's size or the stage's isn't known yet it is fitted as the
/// stage is, as it can.
pub fn placed(
    fit: Option<Fit>,
    zoom: &Zoom,
    source: impl Into<ImageSource>,
    overlay: Option<ImageSource>,
) -> Div {
    let Some(fit) = fit else {
        let (source, overlay) = (source.into(), overlay);
        return div()
            .absolute()
            .top(px(PAD))
            .left(px(PAD))
            .right(px(PAD))
            .bottom(px(PAD))
            .child(img(source).size_full().object_fit(ObjectFit::Contain))
            .when_some(overlay, |el, o| {
                el.child(
                    img(o)
                        .absolute()
                        .top_0()
                        .left_0()
                        .size_full()
                        .object_fit(ObjectFit::Contain),
                )
            });
    };
    let (scale, pan) = zoom.shown(fit);
    let (w, h) = fit.shown(scale);
    let (sw, sh) = fit.stage;
    div()
        .absolute()
        .left(px(sw / 2. + pan.0 - w / 2.))
        .top(px(sh / 2. + pan.1 - h / 2.))
        .w(px(w))
        .h(px(h))
        .child(img(source).size_full())
        .when_some(overlay, |el, o| {
            el.child(img(o).absolute().top_0().left_0().size_full())
        })
}

#[cfg(test)]
mod tests {
    use super::{Fit, Zoom};

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
    fn a_tall_picture_fits_whole_too() {
        // 900x3200 in a 600x500 stage: the height is what limits it, and
        // all of it is shown.
        let tall = Fit {
            stage: (600., 500.),
            natural: (900., 3200.),
        };
        let (w, h) = tall.shown(1.);
        assert!((h - 468.).abs() < 1e-3, "{h}");
        assert!(w <= 568. && (w / h - 900. / 3200.).abs() < 1e-6);
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
    fn steps_and_reset_say_what_changed() {
        let mut z = Zoom::default();
        assert!(!z.zoomed());
        // Without a size to work from nothing moves.
        assert!(!z.step(true, None));
        // In, then out, then back to fit; at fit out and reset change nothing.
        assert!(z.step(true, Some(WIDE)) && z.zoomed());
        assert!(z.step(false, Some(WIDE)) && !z.zoomed());
        assert!(!z.step(false, Some(WIDE)));
        assert!(z.step(true, Some(WIDE)));
        assert!(z.reset() && z.scale == 1. && z.pan == (0., 0.));
        assert!(!z.reset());
    }

    #[test]
    fn a_zoom_is_redrawn_inside_a_stage_that_changed() {
        // Panned to the edge of a wide stage, then shown in a narrow one: the
        // picture isn't left past the stage's edge.
        let mut z = Zoom::default();
        z.zoom_to(3., None, Some(WIDE));
        z.pan = WIDE.clamp(3., (1e6, 0.)).1;
        let narrow = Fit {
            stage: (500., 680.),
            natural: (4000., 1000.),
        };
        let (s, pan) = z.shown(narrow);
        let (w, _) = narrow.shown(s);
        assert!(pan.0.abs() <= (w - 500.) / 2. + 1e-3, "{pan:?}");
    }
}
