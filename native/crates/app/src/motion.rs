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
//! Everything honours reduced motion: GPUI renders a one-shot at its end and a
//! loop at its start when `cx.reduce_motion()` is set, which `init` sets from
//! the platform's own setting (or `AGENTBOX_REDUCE_MOTION=1`).
use std::time::Duration;

use gpui::{
    Animation, AnimationExt, App, ElementId, Hsla, IntoElement, Pixels, SpringAnimation,
    SpringConfig, Styled, div, px,
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
    cx.set_reduce_motion(platform_reduces_motion());
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
    el.with_animation(id, Animation::new(NORMAL).with_easing(ease_out), |el, t| {
        el.opacity(0.4 + 0.6 * t).relative().top(px(10. * (1. - t)))
    })
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
    #[allow(dead_code)] // the sidebar's drawer, when it has one
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
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(1800))
            .repeat()
            .with_max_fps(AMBIENT_FPS),
        |el, t| {
            let wave = (1. - (t * std::f32::consts::TAU).cos()) / 2.;
            el.opacity(1. - 0.6 * wave)
        },
    )
}

/// breathe is a working thing's glow: a soft ring of its colour swelling and
/// easing back (the loop Electron's working dot plays, given to the edge).
pub fn breathe<E: IntoElement + Styled + 'static>(
    el: E,
    id: impl Into<ElementId>,
    color: Hsla,
) -> impl IntoElement {
    el.with_animation(
        id,
        Animation::new(Duration::from_millis(1800))
            .repeat()
            .with_max_fps(AMBIENT_FPS),
        move |el, t| {
            let wave = (1. - (t * std::f32::consts::TAU).cos()) / 2.;
            el.shadow(vec![gpui::BoxShadow {
                color: color.opacity(0.1 + 0.3 * wave),
                offset: gpui::point(px(0.), px(0.)),
                blur_radius: px(4. + 6. * wave),
                spread_radius: px(0.),
                inset: false,
            }])
        },
    )
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

/// spin turns an icon round once a second, Electron's `animate-spin` (a
/// spinner, a refresh while it fetches). A loop: use it only while something
/// is under way. Every spinner is the one animation, so they turn together.
pub fn spin(icon: gpui_component::Icon) -> impl IntoElement {
    icon.with_animation(
        "spin",
        Animation::new(Duration::from_secs(1))
            .repeat()
            .with_max_fps(AMBIENT_FPS),
        |icon, t| icon.transform(gpui::Transformation::rotate(gpui::percentage(t))),
    )
}

/// skeleton is a placeholder bar where text will be, shimmering softly while
/// whatever it stands for loads; a loop, but only until it's replaced.
pub fn skeleton(
    id: impl Into<ElementId>,
    width: impl Into<gpui::DefiniteLength>,
    height: Pixels,
) -> impl IntoElement {
    div()
        .w(width.into())
        .h(height)
        .rounded(theme::radius::SM)
        .bg(theme::surface_strong())
        .with_animation(
            id,
            Animation::new(Duration::from_millis(1600))
                .repeat()
                .with_max_fps(AMBIENT_FPS),
            |el, t| {
                let wave = (1. - (t * std::f32::consts::TAU).cos()) / 2.;
                el.opacity(0.55 + 0.45 * wave)
            },
        )
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
}
