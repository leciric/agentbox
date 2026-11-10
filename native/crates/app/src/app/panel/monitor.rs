//! The agents' tiny monitors (Electron's components/v2/AgentMonitor.tsx and
//! styles.css's .ab-monitor): a screen on a stand acting out what its agent
//! is doing. Working, its tool's mark beside lines of light that type out
//! behind a cursor and step up a line each `pace`; needing you, a "?" that
//! pops from the screen and bobs, over a frame whose amber glow breathes,
//! the mark's hand up and waving; failed, a red screen that glitches now and
//! then; ready to review, a ✓ that pops in once; idle, a dim screen and a
//! "z" floating up.
//!
//! Every loop is `motion::looped`'s, so none plays under reduced motion or
//! while the window isn't in front, and each monitor watches its place
//! (`motion::watch`): one scrolled out of the list holds its still. What
//! moves only now and then (a blink, the glitch) is `motion::sparse`, which
//! draws frames only while it moves; and the idle "z" floats three times
//! when a screen goes dim and then rests, so a list of agents doing nothing
//! asks for no frames at all.
use std::time::Duration;

use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::St;
use crate::character::{Mood, character};
use crate::icons;
use crate::motion;
use crate::theme;

/// Screen is what an agent's monitor shows (shared/monitor.ts MonitorScreen).
#[derive(Clone, Copy, PartialEq, Debug, Hash)]
pub(super) enum Screen {
    Working,
    Needs,
    Error,
    Review,
    Idle,
}

pub(super) fn screen_of(st: St) -> Screen {
    match st {
        St::Needs => Screen::Needs,
        St::Error => Screen::Error,
        St::Review => Screen::Review,
        St::Working => Screen::Working,
        St::Done => Screen::Idle,
    }
}

/// PACE_WINDOW is how far back `typing_pace` counts tool calls.
pub(super) const PACE_WINDOW: Duration = Duration::from_secs(120);

/// SLOW is a working screen's pace with no tool call lately.
pub(super) const SLOW: f32 = 3.2;

/// typing_pace is how many seconds one line of light takes to type and
/// scroll on a working agent's screen (shared/monitor.ts typingPace): slow
/// (3.2 s) with no tool call in the last two minutes, quicker with each,
/// down to 0.8 s at twenty or more.
pub(super) fn typing_pace(calls: usize) -> f32 {
    ((SLOW - calls as f32 * 0.12).max(0.8) * 100.).round() / 100.
}

fn id(key: &str, part: &str) -> ElementId {
    ElementId::Name(format!("monitor-{part}-{key}").into())
}

/// play is `motion::looped` for a monitor in view, and its still otherwise.
fn play<E: IntoElement + 'static>(
    seen: bool,
    el: E,
    id: ElementId,
    period: Duration,
    still: f32,
    f: impl Fn(E, f32) -> E + 'static,
) -> AnyElement {
    if seen {
        motion::looped(el, id, period, still, f)
    } else {
        f(el, still).into_any_element()
    }
}

/// cursor is the terminal's block, blinking as term-cursor does: 1.1 s,
/// lit for the first half and dark for the second, with nothing between.
fn cursor(seen: bool, key: &str, w: f32, h: f32) -> AnyElement {
    let block = div()
        .flex_none()
        .w(px(w))
        .h(px(h))
        .bg(theme::term_foreground());
    play(
        seen,
        block,
        id(key, "cursor"),
        Duration::from_millis(1100),
        0.,
        |el, t| el.opacity(motion::steps(t, &[(0., 1.), (0.5, 0.)])),
    )
}

/// lines are a working screen's lines of light: a column of bars that steps
/// up one line per pace, over one that types at the bottom with the cursor.
/// The column holds its four bars twice, so its loop ends where it starts.
fn lines(seen: bool, key: &str, pace: f32) -> AnyElement {
    const WIDTHS: [f32; 4] = [0.78, 0.52, 0.90, 0.64];
    let pace_d = Duration::from_secs_f32(pace.clamp(0.8, SLOW));
    let column = v_flex().absolute().left_0().right_0().gap(px(4.)).children(
        WIDTHS.iter().chain(WIDTHS.iter()).map(|w| {
            div()
                .flex_none()
                .h(px(3.))
                .w(relative(*w))
                .rounded_full()
                .bg(theme::brand(400).opacity(0.45))
        }),
    );
    // ab-monitor-scroll: each quarter holds a line for as long as the bottom
    // one types, then slides up one (3px of bar and 4px of gap).
    let column = play(seen, column, id(key, "scroll"), pace_d * 4, 0., |el, t| {
        let y = motion::keyframes(
            t,
            &[
                (0., 0.),
                (0.2, 0.),
                (0.25, 7.),
                (0.45, 7.),
                (0.5, 14.),
                (0.7, 14.),
                (0.75, 21.),
                (0.95, 21.),
                (1., 28.),
            ],
            motion::ease,
        );
        el.bottom(px(11. + y))
    });
    // ab-monitor-type and ab-monitor-caret: the bar grows from the left to
    // its 70% over four fifths of the pace, the cursor at its end, then
    // fades as the column takes it up.
    let typed = |t: f32| (t / 0.8).min(1.);
    let bar = div()
        .absolute()
        .left_0()
        .top(px(1.))
        .h(px(3.))
        .rounded_full()
        .bg(theme::brand(300).opacity(0.8));
    let bar = play(seen, bar, id(key, "type"), pace_d, 0.8, move |el, t| {
        el.w(relative(0.7 * typed(t)))
            .opacity(1. - ((t - 0.8) / 0.2).clamp(0., 1.))
    });
    let caret = div().absolute().left_0().top_0().h(px(5.)).child(
        div()
            .absolute()
            .right(px(-4.))
            .top_0()
            .child(cursor(seen, key, 3., 5.)),
    );
    let caret = play(seen, caret, id(key, "caret"), pace_d, 0.8, move |el, t| {
        el.w(relative(0.7 * typed(t)))
    });
    div()
        .relative()
        .min_w_0()
        .flex_1()
        .h_full()
        .overflow_hidden()
        .child(column)
        .child(
            div()
                .absolute()
                .left_0()
                .right_0()
                .bottom(px(2.))
                .h(px(5.))
                .child(bar)
                .child(caret),
        )
        .into_any_element()
}

/// mark is the tool's character on a screen, in the mood its screen calls
/// for (AgentMonitor's screenMood): blinking now and then awake, its raised
/// hand waving when it asks.
fn mark(
    seen: bool,
    key: &str,
    ai: &str,
    screen: Screen,
    size: f32,
    opacity: f32,
    cx: &mut App,
) -> AnyElement {
    let mood = match screen {
        Screen::Working | Screen::Review => Mood::Idle,
        Screen::Needs => Mood::Asking,
        Screen::Error => Mood::Error,
        Screen::Idle => Mood::Sleeping,
    };
    div()
        .flex_none()
        .opacity(opacity)
        .child(character(
            id(key, "mark"),
            ai,
            mood,
            "",
            size,
            theme::brand(300),
            screen == Screen::Idle,
            seen,
            cx,
        ))
        .into_any_element()
}

/// glitch is where ab-monitor-glitch has a failed screen: (shift in px,
/// opacity), for the first tenth of every four seconds, in steps.
fn glitch(seen: bool, cx: &mut App) -> (f32, f32) {
    let Some(t) = seen
        .then(|| motion::sparse(Duration::from_secs(4), 0.1, Duration::from_secs(2), cx))
        .flatten()
    else {
        return (0., 1.);
    };
    // The moving tenth, in fifths: 2%, 4%, 6% and 8% of the whole.
    match (t * 5.) as u32 {
        1 => (-2., 0.7),
        2 => (2., 1.),
        3 => (-1., 0.6),
        4 => (1., 1.),
        _ => (0., 1.),
    }
}

/// POP is ab-monitor-pop: 420 ms on cubic-bezier(0.34, 1.56, 0.64, 1), from
/// faint, a third of its size and 4px low.
const POP: Duration = Duration::from_millis(420);

/// popped is a `size`-px square thing arriving with ab-monitor-pop, once,
/// when it first shows: `draw` draws it at a scale.
fn popped(
    key: &str,
    part: &str,
    size: f32,
    draw: impl Fn(f32) -> AnyElement + 'static,
) -> AnyElement {
    div()
        .flex_none()
        .size(px(size))
        .flex()
        .items_center()
        .justify_center()
        .with_animation(id(key, part), Animation::new(POP), move |el, t| {
            let scale = 0.3 + 0.7 * motion::back_out(t);
            el.opacity(motion::ease(t).min(1.))
                .relative()
                .top(px(4. * (1. - motion::back_out(t))))
                .child(draw(scale))
        })
        .into_any_element()
}

/// zzz is an idle screen's "z" (ab-monitor-z: 3.6 s, up and to the right
/// as it fades in and out). It floats three times when the screen goes dim,
/// then rests, drawn: an agent doing nothing mustn't keep the window drawing.
fn zzz(key: &str, size: f32, right: f32, top: f32, scale: f32) -> AnyElement {
    let z = div()
        .absolute()
        .text_size(px(size))
        .line_height(px(size))
        .font_weight(FontWeight::BOLD)
        .text_color(theme::muted())
        .child("z");
    motion::few(
        z,
        id(key, "z"),
        Duration::from_millis(3600),
        3,
        0.,
        move |el, t| {
            let Some(t) = t else {
                return el.right(px(right)).top(px(top));
            };
            let glide = motion::ease_in_out(t);
            let opacity = motion::keyframes(
                t,
                &[(0., 0.), (0.3, 0.9), (0.6, 0.9), (1., 0.)],
                motion::ease_in_out,
            );
            el.right(px(right - 3. * scale * glide))
                .top(px(top + scale * (3. - 9. * glide)))
                .opacity(opacity)
        },
    )
}

/// glow is a needing screen's amber light round its frame (ab-monitor-glow:
/// 2.4 s, between 35% and 80%; 55% at rest).
fn glow(seen: bool, key: &str, blur: f32, spread: f32, radius: Pixels) -> AnyElement {
    let light = div()
        .absolute()
        .top_0()
        .left_0()
        .size_full()
        .rounded(radius);
    play(
        seen,
        light,
        id(key, "glow"),
        Duration::from_millis(2400),
        0.25,
        move |el, t| {
            let half = if t < 0.5 { t * 2. } else { (1. - t) * 2. };
            let opacity = 0.35 + 0.45 * motion::ease_in_out(half);
            el.shadow(vec![BoxShadow {
                color: theme::amber(400).opacity(opacity),
                offset: point(px(0.), px(0.)),
                blur_radius: px(blur),
                spread_radius: px(spread),
                inset: false,
            }])
        },
    )
}

/// ask is the "?" a needing screen sends up: it pops from the screen's
/// corner (ab-monitor-pop) and then bobs 2px (ab-monitor-bob, 2.4 s).
fn ask(seen: bool, key: &str, size: f32, text: f32, right: f32, top: f32) -> AnyElement {
    let bubble = move |scale: f32| {
        div()
            .flex_none()
            .size(px(size * scale))
            .flex()
            .items_center()
            .justify_center()
            .rounded_full()
            .border_1()
            .border_color(theme::amber(400).opacity(0.7))
            .bg(theme::sunken())
            .shadow_sm()
            .text_size(px(text * scale))
            .line_height(px(text * scale))
            .font_weight(FontWeight::BOLD)
            .text_color(theme::amber(300))
            .child("?")
            .into_any_element()
    };
    let spot = div()
        .absolute()
        .right(px(right))
        .child(popped(key, "ask", size, bubble));
    play(
        seen,
        spot,
        id(key, "bob"),
        Duration::from_millis(2400),
        0.,
        move |el, t| {
            let half = if t < 0.5 { t * 2. } else { (1. - t) * 2. };
            el.top(px(top - 2. * motion::ease_in_out(half)))
        },
    )
}

fn border_of(screen: Screen) -> Hsla {
    match screen {
        Screen::Needs => theme::amber(400).opacity(0.7),
        Screen::Error => theme::rose(400).opacity(0.6),
        Screen::Review => theme::emerald(400).opacity(0.45),
        _ => theme::line_strong(),
    }
}

fn ground_of(screen: Screen) -> Hsla {
    match screen {
        Screen::Error => theme::rose(400).opacity(0.25),
        Screen::Review => theme::emerald(400).opacity(0.1),
        // An idle screen is off: the terminal's ground with some of its ink
        // in it, greyer than a working one's in either theme.
        Screen::Idle => {
            let mut c = motion::mix(theme::term_background(), theme::term_foreground(), 0.86);
            c.a = 1.;
            c
        }
        _ => theme::term_background(),
    }
}

/// agent_monitor is the monitor a row of the list leads with (AgentMonitor,
/// not tiny): a w-28 screen on a stand acting out the agent's state. `key`
/// is the agent's, `pace` its typing_pace.
pub(super) fn agent_monitor(
    key: &str,
    ai: &str,
    screen: Screen,
    pace: f32,
    cx: &mut App,
) -> AnyElement {
    let key = &format!("list-{key}");
    let seen = motion::on_screen(key);
    let glyph =
        |name: &'static str, color: Hsla, scale: f32| icons::bold(name, 3., px(20. * scale), color);
    let content: AnyElement = match screen {
        Screen::Working => h_flex()
            .size_full()
            .gap_2()
            .px_2p5()
            .py_2()
            .child(mark(seen, key, ai, screen, 24., 1., cx))
            .child(lines(seen, key, pace))
            .into_any_element(),
        Screen::Error => {
            let (shift, opacity) = glitch(seen, cx);
            h_flex()
                .size_full()
                .relative()
                .left(px(shift))
                .opacity(opacity)
                .items_center()
                .justify_center()
                .gap_1p5()
                .child(mark(seen, key, ai, screen, 20., 0.6, cx))
                .child(glyph("x", theme::rose(300), 1.))
                .into_any_element()
        }
        Screen::Review => h_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_1p5()
            .child(mark(seen, key, ai, screen, 20., 0.7, cx))
            .child(popped(key, "check", 20., move |scale| {
                glyph("check", theme::emerald(300), scale)
            }))
            .into_any_element(),
        Screen::Needs | Screen::Idle => h_flex()
            .size_full()
            .items_center()
            .justify_center()
            .child(mark(
                seen,
                key,
                ai,
                screen,
                28.,
                if screen == Screen::Idle { 0.4 } else { 1. },
                cx,
            ))
            .into_any_element(),
    };
    // The inner screen is 16:9 of the frame's inside (112 − 2 − 6).
    let inner_w = 112. - 2. - 6.;
    v_flex()
        .flex_none()
        .w(px(112.))
        .items_center()
        .child(
            div()
                .relative()
                .w_full()
                .p(px(3.))
                .rounded_md()
                .border_1()
                .border_color(border_of(screen))
                .bg(theme::surface_strong())
                .child(motion::watch(key.clone()))
                .when(screen == Screen::Needs, |el| {
                    el.child(glow(seen, key, 12., 1., theme::radius::MD))
                })
                .when(screen == Screen::Working || screen == Screen::Idle, |el| {
                    el.group_hover("list-row", |s| {
                        s.border_color(theme::brand(500).opacity(0.6))
                    })
                })
                .child(
                    div()
                        .relative()
                        .w_full()
                        .h(px(inner_w * 9. / 16.))
                        .flex()
                        .items_center()
                        .justify_center()
                        .overflow_hidden()
                        .rounded(px(3.))
                        .bg(ground_of(screen))
                        .text_color(theme::term_foreground())
                        .when(screen == Screen::Idle, |el| el.opacity(0.7))
                        .child(content)
                        .when(screen == Screen::Idle, |el| {
                            el.child(zzz(key, 13., 8., 4., 1.))
                        }),
                )
                .when(screen == Screen::Needs, |el| {
                    el.child(ask(seen, key, 20., 12., -6., -8.))
                }),
        )
        .child(div().h(px(5.)).w_4().bg(theme::ghost()))
        .child(div().h(px(3.)).w_10().rounded_full().bg(theme::ghost()))
        .into_any_element()
}

/// tiny_monitor is the folded strip's (AgentMonitor, tiny): the same frame
/// and states round the agent's initials, with no lines; a working one's
/// label has the terminal's cursor blinking after it. `active` is the open
/// chat's; `finished` is when it finished, if a moment ago, and its frame
/// bursts once (motion::celebrate), which is this app's own.
pub(super) fn tiny_monitor(
    key: &str,
    screen: Screen,
    label: String,
    active: bool,
    finished: Option<std::time::Instant>,
) -> AnyElement {
    let key = &format!("strip-{key}");
    let seen = motion::on_screen(key);
    let badge = |name: &'static str, color: Hsla| {
        div().absolute().right(px(-4.)).top(px(-4.)).child(popped(
            key,
            "badge",
            12.,
            move |scale| {
                div()
                    .flex_none()
                    .size(px(12. * scale))
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_full()
                    .bg(theme::sunken())
                    .child(icons::bold(name, 3.5, px(10. * scale), color))
                    .into_any_element()
            },
        ))
    };
    let frame = div()
        .relative()
        .w_full()
        .p(px(1.))
        .rounded(px(4.))
        .border_1()
        .border_color(
            if active && screen != Screen::Needs && screen != Screen::Error {
                theme::accent()
            } else {
                border_of(screen)
            },
        )
        .bg(theme::surface_strong())
        .child(motion::watch(key.clone()))
        .when(screen == Screen::Needs, |el| {
            el.child(glow(seen, key, 6., 0., px(4.)))
        })
        .child(
            div()
                .relative()
                .w_full()
                .h(px(18.))
                .flex()
                .items_center()
                .justify_center()
                .overflow_hidden()
                .rounded(px(2.))
                .bg(ground_of(screen))
                .text_color(theme::term_foreground())
                .when(screen == Screen::Idle, |el| el.opacity(0.7))
                .child(
                    h_flex()
                        .items_end()
                        .text_size(px(10.))
                        .line_height(px(10.))
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(label)
                        .when(screen == Screen::Working, |el| {
                            el.child(div().ml(px(1.)).child(cursor(seen, key, 2., 7.)))
                        }),
                )
                .when(screen == Screen::Idle, |el| {
                    el.child(zzz(key, 8., 1., 0., 0.5))
                }),
        )
        .when(screen == Screen::Needs, |el| {
            el.child(ask(seen, key, 12., 8., -4., -4.))
        })
        .when(screen == Screen::Error, |el| {
            el.child(badge("x", theme::rose(300)))
        })
        .when(screen == Screen::Review, |el| {
            el.child(badge("check", theme::emerald(300)))
        });
    // The moment it finishes, a ring of the brand bursts out from it once;
    // any other turn (it asks, it failed) rings once in its new colour.
    let turned = motion::changed(&format!("monitor-st-{key}"), &screen);
    let frame = match (finished, turned) {
        (Some(t), _) if screen != Screen::Working => motion::celebrate(
            frame,
            SharedString::from(format!("monitor-done-{key}-{t:?}")),
            theme::brand(400),
        )
        .into_any_element(),
        (_, Some(n)) if screen != Screen::Working && screen != Screen::Idle => motion::ring(
            frame,
            SharedString::from(format!("monitor-turn-{key}-{n}")),
            border_of(screen),
        )
        .into_any_element(),
        _ => frame.into_any_element(),
    };
    v_flex()
        .flex_none()
        .w(px(32.))
        .items_center()
        .child(frame)
        .child(div().h(px(3.)).w(px(6.)).bg(theme::ghost()))
        .child(div().h(px(2.)).w_4().rounded_full().bg(theme::ghost()))
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn the_pace_quickens_with_tool_calls_as_electron_s_does() {
        // shared/monitor.test.ts's cases.
        assert_eq!(typing_pace(0), 3.2);
        assert_eq!(typing_pace(5), 2.6);
        assert_eq!(typing_pace(20), 0.8);
        assert_eq!(typing_pace(200), 0.8);
    }

    #[test]
    fn what_needs_you_or_failed_shows_on_the_screen() {
        assert_eq!(screen_of(St::Needs), Screen::Needs);
        assert_eq!(screen_of(St::Done), Screen::Idle);
        assert_eq!(screen_of(St::Working), Screen::Working);
    }
}
