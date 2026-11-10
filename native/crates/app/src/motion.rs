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

/// spin turns a loader icon once a second (Tailwind's animate-spin). Loops:
/// use it only on what is working right now.
pub fn spin(icon: gpui_component::Icon, id: impl Into<ElementId>) -> impl IntoElement {
    icon.with_animation(
        id,
        Animation::new(Duration::from_millis(1000))
            .repeat()
            .with_max_fps(AMBIENT_FPS),
        |icon, t| icon.rotate(radians(t * std::f32::consts::TAU)),
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
