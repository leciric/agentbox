//! An AI tool's mark as a little character acting out what its agent is
//! doing (Electron's components/AgentCharacter.tsx and styles.css's
//! .ab-char): working, it bobs and blinks (and Codex's cursor blinks);
//! idle, it blinks now and then; asking, its hand is up and waves; asleep,
//! its eyes are shut and its arm droops; broken, its eyes are crossed.
//!
//! The poses are `icons::Pose`s, each a picture drawn once. The loops are
//! `motion`'s: a working or asking character is drawn at the ambient rate
//! while its window is in front, an idle one only for the moment it blinks.
use std::time::Duration;

use gpui::{AnyElement, App, ElementId, Hsla, IntoElement, ParentElement, Styled, div, px};

use crate::icons::{self, Pose};
use crate::motion;

/// Mood is what a character acts out (lib/agentStatus.ts Mood).
#[derive(Clone, Copy, PartialEq, Debug)]
pub enum Mood {
    Working,
    Asking,
    Idle,
    Sleeping,
    Error,
}

/// phase spreads a column of characters' loops out, so they don't blink in
/// step: a fixed offset per seed (an agent's ref), as AgentCharacter's.
pub fn phase(seed: &str) -> Duration {
    let mut h: i32 = 0;
    for c in seed.encode_utf16() {
        h = h.wrapping_mul(31).wrapping_add(c as i32);
    }
    Duration::from_millis((h.unsigned_abs() % 7000) as u64)
}

/// ab-blink's moving part: the last 5% of its turn.
const BLINK_PART: f32 = 0.05;

fn blink_at(t: f32) -> f32 {
    if t > 1. - BLINK_PART {
        Pose::blink((t - (1. - BLINK_PART)) / BLINK_PART)
    } else {
        1.
    }
}

/// character is `ai`'s character in `mood`, `size` px, moving as the mood
/// does when `play` (it is in view). `id` is its own; `seed` sets its phase.
/// A tool with no character is its icon in `color`.
#[allow(clippy::too_many_arguments)]
pub fn character(
    id: impl Into<ElementId>,
    ai: &str,
    mood: Mood,
    seed: &str,
    size: f32,
    color: Hsla,
    grey: bool,
    play: bool,
    cx: &mut App,
) -> AnyElement {
    let offset = phase(seed);
    let has = icons::has_character(ai);
    let ai = ai.to_string();
    let shell = div().flex_none();
    let posed = move |el: gpui::Div, pose: Pose| {
        el.child(icons::tool_mark_posed(&ai, px(size), color, grey, pose))
    };
    if !has || !play || !motion::looping() {
        return posed(shell, rest(mood)).into_any_element();
    }
    match mood {
        Mood::Sleeping | Mood::Error => posed(shell, rest(mood)).into_any_element(),
        // ab-wave: 3.6 s, a small wave and then a rest with the hand up.
        Mood::Asking => {
            motion::looped(shell, id, Duration::from_millis(3600), 0.6, move |el, t| {
                posed(
                    el,
                    Pose {
                        arm: Pose::ASKING.arm + Pose::wave(t),
                        ..Pose::ASKING
                    },
                )
            })
        }
        // ab-bob 3.4 s, ab-blink 8 s and ab-cursor 2.4 s, each on the
        // clock: one loop draws all three.
        Mood::Working => {
            motion::looped(shell, id, Duration::from_millis(3400), 0., move |el, _| {
                let (_, bob) = motion::cycle(Duration::from_millis(3400), offset);
                let (_, blink) = motion::cycle(Duration::from_secs(8), offset);
                let (_, cursor) = motion::cycle(Duration::from_millis(2400), Duration::ZERO);
                let half = if bob < 0.5 { bob * 2. } else { (1. - bob) * 2. };
                posed(
                    el,
                    Pose {
                        rise: 0.025 * motion::ease_in_out(half),
                        eyes: blink_at(blink),
                        cursor: if cursor < 0.5 { 1. } else { 0.25 },
                        ..Pose::REST
                    },
                )
            })
        }
        // ab-blink at 12 s: drawn only while the eyes move.
        Mood::Idle => {
            let eyes = motion::sparse(Duration::from_secs(12), BLINK_PART, offset, cx)
                .map_or(1., Pose::blink);
            posed(shell, Pose { eyes, ..Pose::REST }).into_any_element()
        }
    }
}

/// rest is a mood's pose with nothing moving.
pub fn rest(mood: Mood) -> Pose {
    match mood {
        Mood::Working | Mood::Idle => Pose::REST,
        Mood::Asking => Pose::ASKING,
        Mood::Sleeping => Pose::ASLEEP,
        Mood::Error => Pose::BROKEN,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_seed_s_phase_is_electron_s() {
        // AgentCharacter's phase(): h = h*31 + code, |h| % 7000 ms.
        assert_eq!(phase(""), Duration::ZERO);
        assert_eq!(phase("a"), Duration::from_millis(97));
        assert_eq!(phase("ab"), Duration::from_millis((97 * 31 + 98) % 7000));
        assert!(phase("demo/agent-01") < Duration::from_secs(7));
    }

    #[test]
    fn eyes_are_open_until_the_blink_and_shut_at_its_middle() {
        assert_eq!(blink_at(0.5), 1.);
        assert_eq!(blink_at(0.95), 1.);
        assert!((blink_at(0.975) - 0.12).abs() < 0.01);
        assert!(blink_at(0.999) > 0.9);
    }

    #[test]
    fn each_mood_rests_in_its_pose() {
        assert_eq!(rest(Mood::Asking), Pose::ASKING);
        assert!(rest(Mood::Sleeping).eyes < 0.2);
        assert!(rest(Mood::Error).crossed);
    }
}
