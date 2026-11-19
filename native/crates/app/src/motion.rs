//! Motion: the app's few kinds of movement, each a function, so every screen
//! moves the same way and the rules live in one place.
//!
//! - **One-shot** (an item entering, a dialog opening, a card arriving): GPUI's
//!   `with_animation`, which asks for frames only until it finishes. Keyed by
//!   the thing's own id, so it plays when that thing first appears and not on
//!   every render.
//! - **Springs** (a drawer, a panel, a selection moving): GPUI's `with_spring`,
//!   which keeps position and velocity across a retarget, so a drawer closed
//!   halfway open turns back smoothly, and settles to no frames at all.
//! - **Loops** (a working agent's pulse, a skeleton's shimmer, a spinner): only while
//!   something is actually happening, and at [`AMBIENT_FPS`], since every frame
//!   repaints the window; nothing loops on an idle screen.
//!
//! A loop also holds still while the window isn't the one in front
//! ([`set_live`], as Electron's `html[data-blurred]` pauses its endless
//! animations) and, for what watches its place ([`watch`], [`on_screen`]),
//! while it is scrolled out of view: an unwatched window costs no frames.
//!
//! Everything honours reduced motion: GPUI renders a one-shot at its end and a
//! loop at its start when `cx.reduce_motion()` is set, which `init` sets from
//! the platform's own setting (or `AGENTBOX_REDUCE_MOTION=1`).
use std::time::Duration;

use gpui::{
    Animation, AnimationExt, App, ElementId, Hsla, IntoElement, Pixels, SpringAnimation,
    SpringConfig, Styled, div, px, radians,
};

use crate::theme;

/// Durations, as Electron's keyframes have them.
pub const FAST: Duration = Duration::from_millis(140);
pub const NORMAL: Duration = Duration::from_millis(200);

/// AMBIENT_FPS caps a loop's frames: a breathing dot reads the same at 30 as at
/// 60 and repaints the window half as often.
pub const AMBIENT_FPS: f32 = 30.;

/// SNAPPY is a drawer's or panel's spring: quick, a hair of overshoot.
pub const SNAPPY: SpringConfig = SpringConfig::new(520., 42., 1.);
/// GENTLE is a selection's or a reorder's spring: softer, no overshoot.
pub const GENTLE: SpringConfig = SpringConfig::new(300., 36., 1.);

/// ease_out is cubic-bezier(0.16, 1, 0.3, 1), Electron's pop-in and slide-up:
/// fast out of the gate and long to settle (an exponential-out).
pub fn ease_out(t: f32) -> f32 {
    if t >= 1. {
        1.
    } else {
        1. - 2f32.powf(-10. * t)
    }
}

/// delayed shifts an easing so it holds at 0 for `delay` of `total`, for
/// staggering a list's items without a timer per item.
fn delayed(delay: f32, easing: fn(f32) -> f32) -> impl Fn(f32) -> f32 {
    move |t| {
        if t <= delay {
            0.
        } else {
            easing(((t - delay) / (1. - delay)).min(1.))
        }
    }
}

/// init reads the platform's reduced-motion setting once, at start.
pub fn init(cx: &mut App) {
    let reduce = platform_reduces_motion();
    REDUCED.with(|r| r.set(reduce));
    APP.with(|a| *a.borrow_mut() = Some(cx.to_async()));
    cx.set_reduce_motion(reduce);
}

thread_local! {
    static REDUCED: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
    static LIVE: std::cell::Cell<bool> = const { std::cell::Cell::new(true) };
}

/// reduced is whether motion is reduced (what `init` read), for what draws a
/// still of its own rather than a loop's first frame.
pub fn reduced() -> bool {
    REDUCED.with(|r| r.get())
}

/// set_live says whether the window is the one in front. Out of focus, every
/// loop holds still (Electron's `html[data-blurred]`): each one, however
/// light, has the window drawn again many times a second.
/// `AGENTBOX_MOTION_UNFOCUSED=1` keeps them playing (a recording of a window
/// that never gets the focus).
pub fn set_live(live: bool) {
    let always = std::env::var_os("AGENTBOX_MOTION_UNFOCUSED").is_some();
    LIVE.with(|l| l.set(live || always));
}

/// looping is whether a loop may play now: the window in front and motion
/// not reduced.
pub fn looping() -> bool {
    LIVE.with(|l| l.get()) && !reduced()
}

/// bezier is CSS's cubic-bezier(x1, y1, x2, y2) as an easing.
pub fn bezier(x1: f32, y1: f32, x2: f32, y2: f32) -> impl Fn(f32) -> f32 + Copy {
    move |x| {
        if x <= 0. {
            return 0.;
        }
        if x >= 1. {
            return 1.;
        }
        let at = |a: f32, b: f32, t: f32| {
            let u = 1. - t;
            3. * u * u * t * a + 3. * u * t * t * b + t * t * t
        };
        // Bisect for the curve's parameter at x: x(t) only rises.
        let (mut lo, mut hi) = (0f32, 1f32);
        for _ in 0..20 {
            let mid = (lo + hi) / 2.;
            if at(x1, x2, mid) < x {
                lo = mid;
            } else {
                hi = mid;
            }
        }
        at(y1, y2, (lo + hi) / 2.)
    }
}

/// ease is CSS's `ease`, what a keyframe's stretch eases with when it names
/// no timing function.
pub fn ease(t: f32) -> f32 {
    bezier(0.25, 0.1, 0.25, 1.)(t)
}

/// ease_in_out is CSS's `ease-in-out`.
pub fn ease_in_out(t: f32) -> f32 {
    bezier(0.42, 0., 0.58, 1.)(t)
}

/// back_out is cubic-bezier(0.34, 1.56, 0.64, 1): a pop that overshoots.
pub fn back_out(t: f32) -> f32 {
    bezier(0.34, 1.56, 0.64, 1.)(t)
}

/// keyframes reads a CSS @keyframes at `t` (0 to 1): `stops` are (offset,
/// value) in order, each stretch between two eased by `easing`, as CSS eases
/// each one and not the whole.
pub fn keyframes(t: f32, stops: &[(f32, f32)], easing: fn(f32) -> f32) -> f32 {
    let Some(&(first, mut value)) = stops.first() else {
        return 0.;
    };
    if t <= first {
        return value;
    }
    for pair in stops.windows(2) {
        let ((a, from), (b, to)) = (pair[0], pair[1]);
        if t <= b {
            if b <= a {
                return to;
            }
            return from + (to - from) * easing((t - a) / (b - a));
        }
        value = to;
    }
    value
}

/// steps is `keyframes` with `steps(1, end)`: each value holds until the
/// next stop, with nothing in between (a cursor's blink, a glitch).
pub fn steps(t: f32, stops: &[(f32, f32)]) -> f32 {
    stops
        .iter()
        .rev()
        .find(|(at, _)| t >= *at)
        .or(stops.first())
        .map_or(0., |s| s.1)
}

/// looped plays `f` over and over, `period` a turn, while a loop may play
/// (`looping`), and otherwise draws `f` at `still`, with no frames asked for.
/// Loops of one period keep step with each other (two parts of one thing,
/// drawn apart). The one place a loop is started: every endless animation
/// goes through it.
pub fn looped<E: IntoElement + 'static>(
    el: E,
    id: impl Into<ElementId>,
    period: Duration,
    still: f32,
    f: impl Fn(E, f32) -> E + 'static,
) -> gpui::AnyElement {
    looped_at(el, id, period, still, AMBIENT_FPS, f)
}

/// looped_at is `looped` at `fps` frames a second, for a loop slow enough to
/// be drawn with fewer than [`AMBIENT_FPS`].
pub fn looped_at<E: IntoElement + 'static>(
    el: E,
    id: impl Into<ElementId>,
    period: Duration,
    still: f32,
    fps: f32,
    f: impl Fn(E, f32) -> E + 'static,
) -> gpui::AnyElement {
    // Not GPUI's own repeating animation: each of those keeps a timer of its
    // own, and a screenful of them draws the window many more times a second
    // than any one asks for. Here every loop reads the one clock and asks
    // for the one next beat (`beat`), so thirty loops cost what one does.
    let _ = id.into();
    if !looping() || period.is_zero() {
        return f(el, still).into_any_element();
    }
    beat(fps);
    let (_, t) = cycle(period, Duration::ZERO);
    f(el, t).into_any_element()
}

thread_local! {
    static APP: std::cell::RefCell<Option<gpui::AsyncApp>> =
        const { std::cell::RefCell::new(None) };
}

/// beat has the windows drawn again a frame from now, at `fps`: what every
/// loop asks for each time it is drawn, all of them getting the same frame.
fn beat(fps: f32) {
    tick(Duration::from_secs_f32(1. / fps.clamp(1., 60.)));
}

/// tick has the windows drawn once more, `after` from now, unless a draw is
/// already on its way sooner: many asks become the earliest one.
fn tick(after: Duration) {
    let now = std::time::Instant::now();
    let at = now + after;
    if WAKE.with(|w| w.get()).is_some_and(|p| p <= at && p > now) {
        return;
    }
    let Some(app) = APP.with(|a| a.borrow().clone()) else {
        return;
    };
    WAKE.with(|w| w.set(Some(at)));
    app.spawn(async move |cx| {
        cx.background_executor().timer(after).await;
        WAKE.with(|w| {
            if w.get() == Some(at) {
                w.set(None)
            }
        });
        cx.update(|cx| cx.refresh_windows());
    })
    .detach();
}

struct Tween {
    from: f32,
    to: f32,
    at: std::time::Instant,
}

thread_local! {
    static TWEENS: std::cell::RefCell<std::collections::HashMap<String, Tween>> =
        std::cell::RefCell::new(std::collections::HashMap::new());
}

/// tween is a CSS transition on a number: where the value called `key`
/// stands now on its way to `target`, having set out (ease-out, over
/// `duration`) from wherever it stood when `target` last changed. Its first
/// value is where it starts, with nothing to ease from. Frames are asked for
/// only while it moves. For what moves from place to place (the tour's
/// spotlight and card), where a spring's overshoot would be wrong.
pub fn tween(key: &str, target: f32, duration: Duration) -> f32 {
    let now = std::time::Instant::now();
    let at = |t: &Tween| {
        let p =
            (now.duration_since(t.at).as_secs_f32() / duration.as_secs_f32().max(0.001)).min(1.);
        (
            t.from + (t.to - t.from) * bezier(0., 0., 0.58, 1.)(p),
            p < 1.,
        )
    };
    TWEENS.with(|tweens| {
        let mut tweens = tweens.borrow_mut();
        if reduced() {
            return target;
        }
        if tweens.len() > 1024 {
            tweens.clear();
        }
        let tween = tweens.entry(key.to_string()).or_insert(Tween {
            from: target,
            to: target,
            at: now,
        });
        if tween.to != target {
            *tween = Tween {
                from: at(tween).0,
                to: target,
                at: now,
            };
        }
        let (value, moving) = at(tween);
        if moving {
            beat(60.);
        }
        value
    })
}

/// few plays a loop of `period` only `turns` times, from when the thing
/// first shows, and then rests: for what Electron loops for ever on
/// something that isn't doing anything (a sleeping agent's "z"), which here
/// would keep an idle window drawing. `f` gets how far through a turn it is
/// (`offset` of a turn ahead), or None at rest.
pub fn few<E: IntoElement + 'static>(
    el: E,
    id: impl Into<ElementId>,
    period: Duration,
    turns: u32,
    offset: f32,
    f: impl Fn(E, Option<f32>) -> E + 'static,
) -> gpui::AnyElement {
    if !looping() {
        return f(el, None).into_any_element();
    }
    el.with_animation(
        id,
        Animation::new(period * turns).with_max_fps(AMBIENT_FPS),
        move |el, t| {
            if t >= 1. {
                f(el, None)
            } else {
                f(el, Some((t * turns as f32 + offset).fract()))
            }
        },
    )
    .into_any_element()
}

thread_local! {
    static SCREEN: std::cell::RefCell<std::collections::HashMap<String, bool>> =
        std::cell::RefCell::new(std::collections::HashMap::new());
    static WAKE: std::cell::Cell<Option<std::time::Instant>> =
        const { std::cell::Cell::new(None) };
    static EPOCH: std::time::Instant = std::time::Instant::now();
}

/// on_screen is whether the thing called `key` was in view when last drawn
/// (true until its `watch` has said otherwise): a card scrolled out of a
/// list doesn't loop (Electron's `[data-paused]`, AgentList's useSeenRows).
pub fn on_screen(key: &str) -> bool {
    SCREEN.with(|s| s.borrow().get(key).copied().unwrap_or(true))
}

/// watch goes inside the thing called `key`, filling it, and notes each
/// frame whether any of it shows through what clips it; the frame after it
/// comes into view or leaves is drawn again, for `on_screen` to be read.
pub fn watch(key: impl Into<String>) -> impl IntoElement {
    let key = key.into();
    gpui::canvas(
        move |bounds, window, _| {
            let seen = bounds.intersects(&window.content_mask().bounds);
            let was = SCREEN.with(|s| {
                let mut s = s.borrow_mut();
                if s.len() > 4096 {
                    s.clear();
                }
                s.insert(key, seen)
            });
            if was.unwrap_or(true) != seen {
                window.request_animation_frame();
            }
        },
        |_, _, _, _| {},
    )
    .absolute()
    .top_0()
    .left_0()
    .size_full()
}

/// cycle is where the app's clock stands in a loop of `period` started
/// `offset` early: which turn it is, and how far through it (0 to 1). For a
/// sparse loop (a blink every twelve seconds), which reads the clock when
/// drawn and asks to be drawn again with `wake`, rather than have the window
/// drawn thirty times a second to move for a quarter of one.
pub fn cycle(period: Duration, offset: Duration) -> (u64, f32) {
    let elapsed = EPOCH.with(|e| e.elapsed()) + offset;
    let p = period.as_secs_f64().max(0.001);
    let turns = elapsed.as_secs_f64() / p;
    (turns as u64, turns.fract() as f32)
}

/// wake has the windows drawn once more, `after` from now: the next frame of
/// a sparse loop, or the moment its next burst starts. Many asks become the
/// earliest one. Nothing is asked while loops don't play.
pub fn wake(after: Duration, cx: &mut App) {
    if !looping() {
        return;
    }
    let _ = cx;
    tick(after);
}

/// sparse is a loop that moves for only the last `active` of each `period`
/// (a blink, a glitch): None while it rests, having asked to be woken when
/// it next moves, else how far through the moving part it is (0 to 1),
/// having asked for the next frame. `offset` keeps a column of them out of
/// step.
pub fn sparse(period: Duration, active: f32, offset: Duration, cx: &mut App) -> Option<f32> {
    if !looping() {
        return None;
    }
    let (_, t) = cycle(period, offset);
    let rest = 1. - active;
    if t < rest {
        wake(period.mul_f32(rest - t), cx);
        None
    } else {
        wake(Duration::from_secs_f32(1. / AMBIENT_FPS), cx);
        Some(((t - rest) / active).min(1.))
    }
}

fn platform_reduces_motion() -> bool {
    if let Ok(v) = std::env::var("AGENTBOX_REDUCE_MOTION") {
        return v == "1" || v == "true";
    }
    let read = |cmd: &str, args: &[&str]| {
        std::process::Command::new(cmd)
            .args(args)
            .stderr(std::process::Stdio::null())
            .output()
            .ok()
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
    };
    if cfg!(target_os = "macos") {
        return read(
            "defaults",
            &["read", "com.apple.universalaccess", "reduceMotion"],
        )
        .as_deref()
            == Some("1");
    }
    if cfg!(target_os = "linux") {
        // GNOME's (and most GTK desktops') switch; KDE's animation speed 0
        // is "instant".
        if read(
            "gsettings",
            &["get", "org.gnome.desktop.interface", "enable-animations"],
        )
        .as_deref()
            == Some("false")
        {
            return true;
        }
        return read(
            "kreadconfig6",
            &["--group", "KDE", "--key", "AnimationDurationFactor"],
        )
        .as_deref()
            == Some("0");
    }
    false
}

/// enter fades something in and lifts it the last few pixels into place:
/// Electron's slide-up. `index` staggers a list's items 30 ms apart (the first
/// eight; the rest arrive with the eighth).
pub fn enter<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    index: usize,
) -> impl IntoElement {
    let step = index.min(8) as f32 * 30.;
    let total = NORMAL.as_millis() as f32 + step;
    let delay = step / total;
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(total as u64)).with_easing(delayed(delay, ease_out)),
        |el, t| el.opacity(t).relative().top(px(6. * (1. - t))),
    )
}

/// pop_in is a dialog's or menu's arrival: from slightly below and faint.
pub fn pop_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    // Electron's pop-in: 180 ms, from nothing and 2% of its height low (its
    // 3% of scale is left out: GPUI doesn't scale a box).
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(180)).with_easing(ease_out),
        |el, t| el.opacity(t).relative().top(px(10. * (1. - t))),
    )
}

/// scrim_in fades a dialog's backdrop in under it.
pub fn scrim_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(id, Animation::new(FAST), |el, t| el.opacity(t))
}

/// Side is the edge a drawer slides from.
#[derive(Clone, Copy)]
pub enum Side {
    Left,
    Right,
}

/// drawer slides an absolutely placed drawer in from its edge on a spring,
/// `width` being how far it travels. It plays from closed the first time it's
/// drawn and then follows `open`, so a caller that keeps rendering it while it
/// closes gets the slide out too.
pub fn drawer<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    side: Side,
    width: Pixels,
    open: bool,
) -> impl IntoElement {
    el.with_spring(
        id,
        SpringAnimation::new(SNAPPY).to(open).from(false),
        move |el, phase| {
            let shut = (1. - phase.0).max(-0.05);
            match side {
                Side::Left => el.left(-width * shut),
                Side::Right => el.right(-width * shut),
            }
        },
    )
}

/// pulse breathes something that is working right now (Electron's `working`:
/// down to 40% and back over 1.8 s). Loops: use it only on what is moving.
pub fn pulse<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    looped(el, id, Duration::from_millis(1800), 0., |el, t| {
        let wave = (1. - (t * std::f32::consts::TAU).cos()) / 2.;
        el.opacity(1. - 0.6 * wave)
    })
}

/// pulse_soft is Tailwind's animate-pulse (a placeholder, a dot that waits):
/// down to half and back over 2 s, on cubic-bezier(0.4, 0, 0.6, 1).
pub fn pulse_soft<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    looped(el, id, Duration::from_millis(2000), 0., |el, t| {
        let half = if t < 0.5 { t * 2. } else { (1. - t) * 2. };
        el.opacity(1. - 0.5 * bezier(0.4, 0., 0.6, 1.)(half))
    })
}

/// spin turns a loader icon once a second (Tailwind's animate-spin). Loops:
/// use it only on what is working right now.
pub fn spin(icon: gpui_component::Icon, id: impl Into<ElementId>) -> impl IntoElement {
    looped(icon, id, Duration::from_millis(1000), 0., |icon, t| {
        icon.rotate(radians(t * std::f32::consts::TAU))
    })
}

/// celebrate is the one moment an agent finishing or a pull request merging
/// gets: a ring of `color` bursts out from behind the thing once and is gone.
/// Keyed by what finished, so it plays once per finish.
pub fn celebrate<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    color: Hsla,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(900)).with_easing(ease_out),
        move |el, t| {
            el.shadow(vec![gpui::BoxShadow {
                color: color.opacity(0.55 * (1. - t)),
                offset: gpui::point(px(0.), px(0.)),
                blur_radius: px(4. + 10. * t),
                spread_radius: px(12. * t),
                inset: false,
            }])
        },
    )
}

/// toast_in slides a toast in from the right edge; a success also sends a
/// ring of `burst` out from it as it lands (a pull request merged, an agent
/// done).
pub fn toast_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    burst: Option<Hsla>,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(520)).with_easing(ease_out),
        move |el, t| {
            let slide = (t / 0.55).min(1.);
            let el = el
                .opacity(slide)
                .relative()
                .left(px(32. * (1. - ease_out(slide))));
            match burst {
                Some(color) if t > 0.25 && t < 1. => {
                    let r = (t - 0.25) / 0.75;
                    el.shadow(vec![gpui::BoxShadow {
                        color: color.opacity(0.5 * (1. - r)),
                        offset: gpui::point(px(0.), px(0.)),
                        blur_radius: px(6. + 12. * r),
                        spread_radius: px(14. * r),
                        inset: false,
                    }])
                }
                _ => el,
            }
        },
    )
}

/// leave is something going away to the right (a toast dismissed): it fades
/// and slides out, and whoever owns it removes it once [`FAST`] has passed.
/// Don't use it under reduced motion, where it would simply vanish: remove
/// the thing at once instead.
pub fn leave<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(id, Animation::new(FAST), |el, t| {
        el.opacity(1. - t).relative().left(px(24. * t))
    })
}

/// skeleton is a placeholder bar where text will be, shimmering softly while
/// whatever it stands for loads; a loop, but only until it's replaced.
pub fn skeleton(
    id: impl Into<ElementId>,
    width: impl Into<gpui::DefiniteLength>,
    height: Pixels,
) -> impl IntoElement {
    use gpui::ParentElement;
    let bar = div()
        .w(width.into())
        .h(height)
        .flex()
        .rounded(theme::radius::SM)
        .overflow_hidden()
        .bg(theme::surface_faint());
    // Electron's .skeleton: a gradient faint, strong, faint, twice the bar's
    // width and repeating, sliding left four widths in 1.6 s; so the bar
    // shows half a wave of it, and the same picture every 0.8 s. Drawn as
    // the two straight stretches either side of the wave's turn.
    looped(bar, id, Duration::from_millis(800), 0.25, |bar, u| {
        let wave = |p: f32| 1. - (2. * p.fract() - 1.).abs();
        let shade = |v: f32| mix(theme::surface_strong(), theme::surface_faint(), v);
        let (turn, at) = if u < 0.5 {
            (1., 1. - 2. * u)
        } else {
            (0., 2. * (1. - u))
        };
        let stretch = |w: f32, from: f32, to: f32| {
            div()
                .h_full()
                .w(gpui::relative(w))
                .bg(gpui::linear_gradient(
                    90.,
                    gpui::linear_color_stop(shade(from), 0.),
                    gpui::linear_color_stop(shade(to), 1.),
                ))
        };
        bar.child(stretch(at, wave(u), turn))
            .child(stretch(1. - at, turn, wave(u + 0.5)))
    })
}

/// shine is a label with a highlight sweeping across it: what's happening
/// right now (Electron's .chat-shine: the words in subtle, and a 4.5rem band
/// of the title's colour crossing them every 2.2 s). The band is laid over
/// the letters themselves, a letter at a time. At rest, or out of focus,
/// the words are plain; under reduced motion they are muted, as there.
pub fn shine(text: impl Into<gpui::SharedString>, id: impl Into<ElementId>) -> gpui::AnyElement {
    use gpui::ParentElement;
    let text: gpui::SharedString = text.into();
    let holder = div().min_w_0().truncate();
    if reduced() {
        return holder
            .text_color(theme::muted())
            .child(text)
            .into_any_element();
    }
    let (base, lit) = (theme::subtle(), theme::title().opacity(0.9));
    looped(
        holder.text_color(base),
        id,
        Duration::from_millis(2200),
        -1.,
        move |el, t| {
            if t < 0. {
                return el.child(text.clone());
            }
            // The band is some ten letters wide (4.5rem of 14px text).
            const HALF: f32 = 5.;
            let n = text.chars().count() as f32;
            let centre = -HALF + (n + 2. * HALF) * t;
            let lights = text.char_indices().enumerate().filter_map(|(i, (at, c))| {
                let glow = 1. - ((i as f32 + 0.5) - centre).abs() / HALF;
                (glow > 0.).then(|| {
                    (
                        at..at + c.len_utf8(),
                        gpui::HighlightStyle {
                            color: Some(mix(lit, base, glow)),
                            ..Default::default()
                        },
                    )
                })
            });
            el.child(
                gpui::StyledText::new(text.clone()).with_highlights(lights.collect::<Vec<_>>()),
            )
        },
    )
}

/// mix is CSS's color-mix in srgb: `a` at `amount`, the rest `b`, alpha too.
pub fn mix(a: Hsla, b: Hsla, amount: f32) -> Hsla {
    let (a, b) = (a.to_rgb(), b.to_rgb());
    let m = |x: f32, y: f32| x * amount + y * (1. - amount);
    gpui::Rgba {
        r: m(a.r, b.r),
        g: m(a.g, b.g),
        b: m(a.b, b.b),
        a: m(a.a, b.a),
    }
    .into()
}

/// skeleton_rows is a list's worth of skeletons: a dot and two lines a row.
pub fn skeleton_rows(id: &str, rows: usize) -> impl IntoElement {
    use gpui::ParentElement;
    gpui_component::v_flex()
        .gap_3()
        .p_3()
        .children((0..rows).map(|i| {
            gpui_component::h_flex()
                .gap_2p5()
                .items_start()
                .child(skeleton(
                    ElementId::Name(format!("{id}-dot-{i}").into()),
                    px(28.),
                    px(28.),
                ))
                .child(
                    gpui_component::v_flex()
                        .flex_1()
                        .gap_1p5()
                        .child(skeleton(
                            ElementId::Name(format!("{id}-a-{i}").into()),
                            gpui::relative(0.55 + 0.1 * (i % 3) as f32),
                            px(10.),
                        ))
                        .child(skeleton(
                            ElementId::Name(format!("{id}-b-{i}").into()),
                            gpui::relative(0.8 - 0.12 * (i % 2) as f32),
                            px(8.),
                        )),
                )
        }))
}

/// skeleton_lines is a few bars where lines of text or rows of a list will
/// be (a diff, a file, a pull request's details): `rows` of them, of uneven
/// lengths as text is.
pub fn skeleton_lines(id: &str, rows: usize) -> impl IntoElement {
    use gpui::ParentElement;
    const WIDTHS: [f32; 5] = [0.62, 0.84, 0.48, 0.73, 0.36];
    gpui_component::v_flex()
        .w_full()
        .max_w(px(520.))
        .gap_2p5()
        .children((0..rows).map(|i| {
            skeleton(
                ElementId::Name(format!("{id}-{i}").into()),
                gpui::relative(WIDTHS[i % WIDTHS.len()]),
                px(10.),
            )
        }))
}

/// skeleton_chat is a conversation's shape while it loads: a question on the
/// right, an answer's lines on the left, twice, faded toward the top.
pub fn skeleton_chat(id: &str) -> impl IntoElement {
    use gpui::{ParentElement, relative};
    let bar = |k: String, w: f32, h: f32| skeleton(ElementId::Name(k.into()), relative(w), px(h));
    gpui_component::v_flex()
        .size_full()
        .justify_end()
        .gap_4()
        .px_6()
        .pb_6()
        .children((0..2).map(|i| {
            gpui_component::v_flex()
                .gap_3()
                .opacity(if i == 0 { 0.5 } else { 1. })
                .child(
                    gpui_component::h_flex().justify_end().child(
                        div()
                            .w(relative(0.45))
                            .child(bar(format!("{id}-q{i}"), 1., 34.)),
                    ),
                )
                .child(
                    gpui_component::v_flex()
                        .gap_2()
                        .child(bar(format!("{id}-a{i}0"), 0.82, 10.))
                        .child(bar(format!("{id}-a{i}1"), 0.9, 10.))
                        .child(bar(format!("{id}-a{i}2"), 0.6, 10.)),
                )
        }))
}

/// fade_in is a view taking the place of another (a tab's page, a section of
/// Settings, the panel's view): it fades up over a few pixels, quickly, so a
/// switch reads as a change and not a cut. Key it by what is shown.
pub fn fade_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(id, Animation::new(FAST).with_easing(ease_out), |el, t| {
        el.opacity(0.35 + 0.65 * t)
            .relative()
            .top(px(4. * (1. - t)))
    })
}

/// unfolded is a disclosure's body, shown: when it has just been opened
/// (`opened` is `changed(key, &open)`, asked every frame, open or not) it
/// fades up under its head; otherwise it is drawn as it is, so a body already
/// open doesn't fade each time its row scrolls back into a list.
pub fn unfolded<E: IntoElement + Styled + 'static>(
    body: E,
    opened: Option<u64>,
    id: &'static str,
) -> gpui::AnyElement {
    match opened {
        Some(n) => fade_in(body, (id, n as usize)).into_any_element(),
        None => body.into_any_element(),
    }
}

/// appear is Electron's animate-fade-in: 160 ms ease-out from nothing, with
/// no movement (an overlay, a menu of ours, a hint).
pub fn appear<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(160)).with_easing(bezier(0., 0., 0.58, 1.)),
        |el, t| el.opacity(t),
    )
}

/// appear_late shows something only once it has been there long enough to
/// matter (Electron's .appear-late: nothing for 0.8 s, then a 0.2 s fade),
/// like a message still on its way after most have arrived.
pub fn appear_late<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(id, Animation::new(Duration::from_millis(1000)), |el, t| {
        el.opacity(bezier(0., 0., 0.58, 1.)(((t - 0.8) / 0.2).clamp(0., 1.)))
    })
}

/// stream_in is a block of a streaming reply arriving (Electron's
/// .chat-stream blocks): a 600 ms ease-out fade from nothing, with no lift.
pub fn stream_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(600)).with_easing(bezier(0., 0., 0.58, 1.)),
        |el, t| el.opacity(t),
    )
}

thread_local! {
    static FLASHED: std::cell::RefCell<Option<(String, std::time::Instant, u64)>> =
        const { std::cell::RefCell::new(None) };
}

/// flash marks the thing called `key` as just revealed (a search's hit
/// scrolled to, a setting a search opened its section at): `revealed` lights
/// it for a moment so the eye finds it.
pub fn flash(key: impl Into<String>) {
    FLASHED.with(|f| {
        let n = f.borrow().as_ref().map_or(0, |f| f.2 + 1);
        *f.borrow_mut() = Some((key.into(), std::time::Instant::now(), n));
    });
}

/// Reveal is which of Electron's two highlights a revealed thing wears.
#[derive(Clone, Copy)]
pub enum Reveal {
    /// ab-revealed: 2.4 s, a brand outline and halo held to 45%, then gone.
    Hit,
    /// setting-revealed: 1.6 s, a brand wash and inner ring held to 40%.
    Setting,
}

/// revealed lights `el` if it is the thing `flash` named a moment ago, and
/// is `el` otherwise.
pub fn revealed<E: IntoElement + Styled + 'static>(
    el: E,
    key: &str,
    kind: Reveal,
) -> gpui::AnyElement {
    let (total, hold) = match kind {
        Reveal::Hit => (Duration::from_millis(2400), 0.45),
        Reveal::Setting => (Duration::from_millis(1600), 0.4),
    };
    let n = FLASHED.with(|f| {
        f.borrow()
            .as_ref()
            .filter(|(k, at, _)| k == key && at.elapsed() < total)
            .map(|f| f.2)
    });
    let Some(n) = n else {
        return el.into_any_element();
    };
    el.with_animation(
        ElementId::Name(format!("revealed-{key}-{n}").into()),
        Animation::new(total),
        move |el, t| {
            if t >= 1. {
                return el;
            }
            let left = 1. - bezier(0., 0., 0.58, 1.)(((t - hold) / (1. - hold)).clamp(0., 1.));
            let brand = theme::brand(400);
            let ring = |spread: f32, alpha: f32| gpui::BoxShadow {
                color: brand.opacity(alpha * left),
                offset: gpui::point(px(0.), px(0.)),
                blur_radius: px(0.),
                spread_radius: px(spread),
                inset: false,
            };
            match kind {
                // The outline, 2px at 3px out, over its 6px halo.
                Reveal::Hit => el.shadow(vec![ring(5., 1.), ring(9., 0.18)]),
                Reveal::Setting => el
                    .bg(brand.opacity(0.14 * left))
                    .shadow(vec![ring(1., 0.55)]),
            }
        },
    )
    .into_any_element()
}

/// menu_in is a menu, popover or picker we draw ourselves opening: the same
/// rise and fade gpui-component gives its own dropdowns (150 ms, 4 px), so
/// ours and its are told apart by nothing.
pub fn menu_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(150)).with_easing(ease_out),
        |el, t| el.opacity(t).relative().top(px(-4. * (1. - t))),
    )
}

/// chevron is a disclosure's arrow: a chevron-right that turns to point down
/// when open, on a spring, where Electron rotates it with a transition. With
/// no `size` it is the text's, as any icon. Its id need only be its own
/// within the head it sits in, which has an id of its own.
pub fn chevron(id: impl Into<ElementId>, open: bool, size: Option<Pixels>) -> gpui::AnyElement {
    let icon = crate::icons::ic("chevron-right");
    let icon = match size {
        Some(size) => icon.size(size),
        None => icon,
    };
    icon.with_spring(
        id,
        SpringAnimation::new(SNAPPY).to(if open { 1f32 } else { 0. }),
        |icon, t| {
            icon.rotate(gpui::radians(
                std::f32::consts::FRAC_PI_2 * t.clamp(0., 1.1),
            ))
        },
    )
    .into_any_element()
}

/// flip is a chevron-down that turns to point up when its menu or section
/// is open (Electron's `rotate-180` with a 150 ms transition).
pub fn flip(icon: gpui_component::Icon, id: impl Into<ElementId>, open: bool) -> gpui::AnyElement {
    icon.with_spring(
        id,
        SpringAnimation::new(SNAPPY).to(if open { 1f32 } else { 0. }),
        |icon, t| icon.rotate(gpui::radians(std::f32::consts::PI * t.clamp(0., 1.05))),
    )
    .into_any_element()
}

/// progress is a bar's fill, as a fraction of its track: it fills from
/// empty when the bar first appears and glides to each new value after,
/// where it would otherwise jump.
pub fn progress<E: IntoElement + Styled + 'static>(
    fill: E,
    id: impl Into<ElementId>,
    fraction: f32,
) -> impl IntoElement {
    fill.with_spring(
        id,
        SpringAnimation::new(GENTLE)
            .to(fraction.clamp(0., 1.))
            .from(0.),
        |el, f| el.w(gpui::relative(f.clamp(0., 1.))),
    )
}

/// How long after a change its animation may still start: a row scrolled back
/// into view a minute later mustn't replay what happened then.
const CHANGE_WINDOW: Duration = Duration::from_millis(1200);

struct Seen {
    value: u64,
    generation: u64,
    at: std::time::Instant,
}

thread_local! {
    static SEEN: std::cell::RefCell<std::collections::HashMap<String, Seen>> =
        std::cell::RefCell::new(std::collections::HashMap::new());
}

fn hash_of(value: &impl std::hash::Hash) -> u64 {
    use std::hash::Hasher as _;
    let mut h = std::collections::hash_map::DefaultHasher::new();
    value.hash(&mut h);
    h.finish()
}

/// changed says whether the thing called `key` has just changed value: the
/// number of the change while it is fresh, None when it has its first value
/// (a list loading isn't every row changing) or changed long ago. It is how a
/// status dot, a count or a label animates on the change and only then; key
/// the animation by `(key, n)` and it plays once per change.
pub fn changed(key: &str, value: &impl std::hash::Hash) -> Option<u64> {
    changed_at(key, hash_of(value), std::time::Instant::now())
}

fn changed_at(key: &str, value: u64, now: std::time::Instant) -> Option<u64> {
    SEEN.with(|seen| {
        let mut seen = seen.borrow_mut();
        // Keys are agents, sessions and counts: thousands at most, but a long
        // run forgets the ones gone quiet rather than keep them for good.
        if seen.len() > 4096 {
            seen.retain(|_, s| now.duration_since(s.at) < Duration::from_secs(600));
        }
        match seen.get_mut(key) {
            None => {
                seen.insert(
                    key.to_string(),
                    Seen {
                        value,
                        generation: 0,
                        at: now,
                    },
                );
                None
            }
            Some(s) => {
                if s.value != value {
                    s.value = value;
                    s.generation += 1;
                    s.at = now;
                }
                (s.generation > 0 && now.duration_since(s.at) < CHANGE_WINDOW)
                    .then_some(s.generation)
            }
        }
    })
}

/// ring is a change calling for a glance: a ring of `color` leaves the element
/// once and is gone (a status dot turning, a count going up). Quicker and
/// smaller than `celebrate`, which is for a finish.
pub fn ring<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    color: Hsla,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(600)).with_easing(ease_out),
        move |el, t| {
            if t >= 1. {
                return el;
            }
            el.shadow(vec![gpui::BoxShadow {
                color: color.opacity(0.6 * (1. - t)),
                offset: gpui::point(px(0.), px(0.)),
                blur_radius: px(2. + 4. * t),
                spread_radius: px(5. * t),
                inset: false,
            }])
        },
    )
}

/// on_change rings `el` when the value under `key` has just changed, and
/// leaves it alone otherwise: `changed` and `ring` together, the usual case.
pub fn on_change<E: IntoElement + Styled + 'static>(
    el: E,
    key: &str,
    value: &impl std::hash::Hash,
    color: Hsla,
) -> gpui::AnyElement {
    match changed(key, value) {
        Some(n) => {
            ring(el, ElementId::Name(format!("{key}-ring-{n}").into()), color).into_any_element()
        }
        None => el.into_any_element(),
    }
}

/// lit is a choice becoming the picked one (a segment, a tab, a view's
/// button): its fill fades up under it, so the selection is seen to move.
/// `key` names the control and `value` what is picked; nothing plays when
/// the control is first drawn.
pub fn lit<E: IntoElement + Styled + 'static>(
    el: E,
    key: &str,
    value: &impl std::hash::Hash,
) -> gpui::AnyElement {
    match changed(key, value) {
        Some(n) => el
            .with_animation(
                ElementId::Name(format!("{key}-lit-{n}").into()),
                Animation::new(NORMAL).with_easing(ease_out),
                |el, t| el.opacity(0.3 + 0.7 * t),
            )
            .into_any_element(),
        None => el.into_any_element(),
    }
}

struct Group {
    seen: std::collections::HashSet<String>,
    since: std::time::Instant,
    fresh: std::collections::HashMap<String, std::time::Instant>,
}

thread_local! {
    static GROUPS: std::cell::RefCell<std::collections::HashMap<String, Group>> =
        std::cell::RefCell::new(std::collections::HashMap::new());
}

/// How long a list is taken to be still filling: what it gets in its first
/// moments is what it had, not news.
const SETTLE: Duration = Duration::from_millis(1500);

/// newcomer says whether `key` has just joined the list called `group`: true
/// for a moment for a row that wasn't there (an agent started, a session
/// opened, a pull request arrived), never for the rows a list is first drawn
/// with. It is what lets a list's new row slide in (`arrive`) without the
/// whole list sliding in every time its page is opened.
pub fn newcomer(group: &str, key: &str) -> bool {
    newcomer_at(group, key, std::time::Instant::now())
}

fn newcomer_at(group: &str, key: &str, now: std::time::Instant) -> bool {
    GROUPS.with(|groups| {
        let mut groups = groups.borrow_mut();
        let g = groups.entry(group.to_string()).or_insert_with(|| Group {
            seen: Default::default(),
            since: now,
            fresh: Default::default(),
        });
        if g.seen.insert(key.to_string()) && now.duration_since(g.since) > SETTLE {
            g.fresh.insert(key.to_string(), now);
        }
        g.fresh
            .retain(|_, at| now.duration_since(*at) < CHANGE_WINDOW);
        g.fresh.contains_key(key)
    })
}

/// arrive is a list's row: one that has just joined the list slides in
/// (`enter`), the rest are drawn as they are.
pub fn arrive<E: IntoElement + Styled + 'static>(
    el: E,
    group: &str,
    key: &str,
) -> gpui::AnyElement {
    if newcomer(group, key) {
        enter(
            el,
            ElementId::Name(format!("{group}-arrive-{key}").into()),
            0,
        )
        .into_any_element()
    } else {
        el.into_any_element()
    }
}

/// swap_in is a label or an icon replaced by another in the same place (Copy
/// turning to a tick, a count ticking over): the new one fades in.
pub fn swap_in<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
) -> impl IntoElement {
    el.with_animation(id, Animation::new(FAST).with_easing(ease_out), |el, t| {
        el.opacity(t)
    })
}

thread_local! {
    static COPIED: std::cell::RefCell<Option<(String, std::time::Instant)>> =
        const { std::cell::RefCell::new(None) };
}

/// How long a copy button shows its tick (Electron's 1200 ms).
pub const COPIED_FOR: Duration = Duration::from_millis(1200);

/// copy puts `text` on the clipboard and remembers that the button called
/// `key` did it, so `copied(key)` is true for a moment and the button can
/// show a tick; the window is redrawn when the moment is over.
pub fn copy(key: &str, text: impl Into<String>, window: &mut gpui::Window, cx: &mut App) {
    cx.write_to_clipboard(gpui::ClipboardItem::new_string(text.into()));
    COPIED.with(|c| *c.borrow_mut() = Some((key.to_string(), std::time::Instant::now())));
    window.refresh();
    let handle = window.window_handle();
    cx.spawn(async move |cx| {
        cx.background_executor()
            .timer(COPIED_FOR + Duration::from_millis(30))
            .await;
        handle.update(cx, |_, window, _| window.refresh()).ok();
    })
    .detach();
}

/// copied says whether the button called `key` copied something just now.
pub fn copied(key: &str) -> bool {
    COPIED.with(|c| {
        c.borrow()
            .as_ref()
            .is_some_and(|(k, at)| k == key && at.elapsed() < COPIED_FOR)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn easings_start_at_zero_and_land_at_one() {
        assert_eq!(ease_out(0.), 0.);
        assert_eq!(ease_out(1.), 1.);
        assert!(ease_out(0.3) > 0.8, "fast out of the gate");
        let d = delayed(0.5, ease_out);
        assert_eq!(d(0.25), 0., "holds through its delay");
        assert_eq!(d(1.), 1.);
    }

    #[test]
    fn a_bezier_is_css_s() {
        // CSS's ease at its middle, to three places.
        assert!((ease(0.5) - 0.8024).abs() < 0.002, "{}", ease(0.5));
        assert!((ease_in_out(0.5) - 0.5).abs() < 0.001);
        assert_eq!(ease(0.), 0.);
        assert_eq!(ease(1.), 1.);
        assert!(back_out(0.6) > 1., "a pop overshoots");
    }

    #[test]
    fn keyframes_hold_ease_and_step() {
        fn linear(t: f32) -> f32 {
            t
        }
        // ab-monitor-scroll's first two stretches: held, then up a line.
        let stops = [(0., 0.), (0.2, 0.), (0.25, 7.), (0.45, 7.)];
        assert_eq!(keyframes(0.1, &stops, linear), 0.);
        assert!((keyframes(0.225, &stops, linear) - 3.5).abs() < 0.001);
        assert_eq!(keyframes(0.3, &stops, linear), 7.);
        assert_eq!(
            keyframes(0.9, &stops, linear),
            7.,
            "past the last, it holds"
        );
        // term-cursor: steps(1, end), lit then dark.
        let blink = [(0., 1.), (0.5, 0.)];
        assert_eq!(steps(0.49, &blink), 1.);
        assert_eq!(steps(0.5, &blink), 0.);
    }

    #[test]
    fn something_unwatched_is_taken_to_be_on_screen() {
        assert!(on_screen("motion-test-never-watched"));
        SCREEN.with(|s| s.borrow_mut().insert("motion-test-off".into(), false));
        assert!(!on_screen("motion-test-off"));
    }

    #[test]
    fn a_tween_starts_where_it_is_and_heads_for_its_target() {
        let d = Duration::from_millis(300);
        assert_eq!(tween("motion-test-tween", 10., d), 10., "its first value");
        let v = tween("motion-test-tween", 20., d);
        assert!((10. ..20.).contains(&v), "on its way: {v}");
        TWEENS.with(|t| {
            t.borrow_mut().get_mut("motion-test-tween").unwrap().at -= Duration::from_secs(1)
        });
        assert_eq!(tween("motion-test-tween", 20., d), 20., "arrived");
    }

    #[test]
    fn a_cycle_runs_from_nought_to_one() {
        let (_, t) = cycle(Duration::from_secs(12), Duration::from_secs(3));
        assert!((0. ..1.).contains(&t));
        let (n, _) = cycle(Duration::from_secs(1), Duration::from_secs(5));
        assert!(n >= 5);
    }

    #[test]
    fn a_change_is_fresh_once_and_a_first_value_is_not_a_change() {
        let t0 = std::time::Instant::now();
        assert_eq!(changed_at("motion-test-dot", 1, t0), None, "first value");
        assert_eq!(changed_at("motion-test-dot", 1, t0), None, "same value");
        assert_eq!(changed_at("motion-test-dot", 2, t0), Some(1), "it changed");
        let soon = t0 + Duration::from_millis(300);
        assert_eq!(
            changed_at("motion-test-dot", 2, soon),
            Some(1),
            "still fresh"
        );
        let later = t0 + CHANGE_WINDOW + Duration::from_millis(1);
        assert_eq!(changed_at("motion-test-dot", 2, later), None, "long ago");
        assert_eq!(
            changed_at("motion-test-dot", 3, later),
            Some(2),
            "and again"
        );
    }

    #[test]
    fn a_list_s_first_rows_are_not_newcomers_and_a_later_one_is_for_a_while() {
        let t0 = std::time::Instant::now();
        assert!(!newcomer_at("motion-test-list", "a", t0));
        assert!(!newcomer_at(
            "motion-test-list",
            "b",
            t0 + Duration::from_millis(400)
        ));
        let later = t0 + SETTLE + Duration::from_millis(10);
        assert!(!newcomer_at("motion-test-list", "a", later), "it was there");
        assert!(newcomer_at("motion-test-list", "c", later), "it wasn't");
        assert!(newcomer_at(
            "motion-test-list",
            "c",
            later + Duration::from_millis(200)
        ));
        assert!(!newcomer_at("motion-test-list", "c", later + CHANGE_WINDOW));
    }

    #[test]
    fn nothing_is_copied_until_something_is() {
        assert!(!copied("motion-test-copy"));
        COPIED.with(|c| {
            *c.borrow_mut() = Some(("motion-test-copy".into(), std::time::Instant::now()))
        });
        assert!(copied("motion-test-copy"));
        assert!(!copied("another"));
    }
}
