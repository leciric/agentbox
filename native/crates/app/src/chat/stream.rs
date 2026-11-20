//! A streaming reply, revealed smoothly. Text arrives in chunks of a few words
//! at a time; drawn as it lands it jumps. Instead the message on screen eases
//! toward what has arrived: each chunk starts a short animation (keyed by how
//! much has arrived, so GPUI asks for frames only until it is shown) from what
//! is on screen now to the end, and each new paragraph fades in as it starts.
//!
//! While streaming, the markdown is drawn a block at a time (split at blank
//! lines outside code fences), so a new block can fade without the ones above
//! it; once the message is done it is drawn whole again (chat/items.rs).
//! Reduced motion draws every animation at its end: the text as it arrives.
use std::cell::Cell;
use std::rc::Rc;
use std::time::Duration;

use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::text::TextView;
use gpui_component::v_flex;

use crate::{motion, theme};

/// reveal is a streaming message's body, `shown` holding how many bytes of it
/// are on screen across frames.
pub fn reveal(id: &str, text: &str, shown: Rc<Cell<usize>>) -> AnyElement {
    let target = text.len();
    let from = shown.get().min(target);
    // Quick enough to keep up with any stream, slow enough to read as a flow:
    // what is behind is caught up in 120–360 ms.
    let behind = target - from;
    let ms = (behind as u64 * 4).clamp(120, 360);
    let text: SharedString = text.to_string().into();
    let id: SharedString = id.to_string().into();
    let key = ElementId::Name(format!("reveal-{id}-{target}").into());
    div()
        .w_full()
        .min_w_0()
        .with_animation(
            key,
            Animation::new(Duration::from_millis(ms)).with_easing(ease_in_out),
            move |el, t| {
                let n = boundary(&text, from + ((target - from) as f32 * t).round() as usize);
                shown.set(n.max(shown.get().min(target)));
                el.child(blocks(&id, &text[..n]))
            },
        )
        .into_any_element()
}

/// blocks draws `text` a markdown block at a time, the newest fading in. A
/// diagram, picture or page still arriving is a card saying so until its
/// closing fence has come (rich::open_fence), then drawn once, whole.
fn blocks(id: &str, text: &str) -> AnyElement {
    let open = crate::rich::open_fence(text);
    let parts = split(open.as_ref().map_or(text, |o| &text[..o.at]));
    let card = open.map(|o| {
        let k = parts.len();
        motion::stream_in(
            div()
                .w_full()
                .min_w_0()
                .pr(crate::mermaid::HANG)
                .child(crate::rich::generating(&o, format!("generating-{id}-{k}"))),
            SharedString::from(format!("block-in-{id}-{k}")),
        )
        .into_any_element()
    });
    let last = parts.len().saturating_sub(1);
    v_flex()
        .w_full()
        .min_w_0()
        .gap(rems(0.75))
        .children(parts.into_iter().enumerate().map(|(k, part)| {
            let view = crate::mermaid::with_mermaid(TextView::markdown(
                SharedString::from(format!("md-{id}-{k}")),
                part.to_string(),
            ))
            .selectable(true)
            .text_color(theme::primary());
            let block = div().w_full().min_w_0().child(view);
            if k == last {
                motion::stream_in(block, SharedString::from(format!("block-in-{id}-{k}")))
                    .into_any_element()
            } else {
                block.into_any_element()
            }
        }))
        .children(card)
        .into_any_element()
}

/// message is a reply's body drawn whole: one streaming since before the
/// chat loaded, or done. A diagram, picture or page it ends inside of is a
/// card while it streams, and its code if it ended without the closing fence
/// (Markdown.tsx), never a half-drawn page.
pub fn message(id: &str, text: &str, streaming: bool) -> AnyElement {
    let markdown = |text: &str| {
        crate::mermaid::with_mermaid(TextView::markdown(
            SharedString::from(format!("md-{id}")),
            text.to_string(),
        ))
        .selectable(true)
        .text_color(theme::tertiary())
        .into_any_element()
    };
    let Some(open) = crate::rich::open_fence(text) else {
        return markdown(text);
    };
    let before = &text[..open.at];
    let tail = if streaming {
        crate::rich::generating(&open, format!("generating-{id}"))
    } else {
        crate::mermaid::code_block(open.lang, open.code)
    };
    v_flex()
        .w_full()
        .min_w_0()
        .gap(rems(0.75))
        .when(!before.trim().is_empty(), |el| el.child(markdown(before)))
        .child(
            div()
                .w_full()
                .min_w_0()
                .pr(crate::mermaid::HANG)
                .child(tail),
        )
        .into_any_element()
}

/// split cuts markdown at blank lines, never inside a fenced code block.
pub fn split(text: &str) -> Vec<&str> {
    let mut out = Vec::new();
    let (mut start, mut fenced, mut at) = (0, false, 0);
    for line in text.split_inclusive('\n') {
        let trimmed = line.trim_start();
        if trimmed.starts_with("```") || trimmed.starts_with("~~~") {
            fenced = !fenced;
        }
        if !fenced && line.trim().is_empty() && at > start {
            let part = text[start..at].trim_end();
            if !part.is_empty() {
                out.push(part);
            }
            start = at + line.len();
        }
        at += line.len();
    }
    let rest = text[start.min(text.len())..].trim_end();
    if !rest.is_empty() {
        out.push(rest);
    }
    out
}

/// boundary is the last char boundary at or before `n`.
fn boundary(text: &str, n: usize) -> usize {
    let mut n = n.min(text.len());
    while !text.is_char_boundary(n) {
        n -= 1;
    }
    n
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn blocks_split_at_blank_lines_but_not_in_code() {
        let md = "One line.\n\nTwo\nlines.\n\n```\na\n\nb\n```\n\nEnd";
        assert_eq!(
            split(md),
            vec!["One line.", "Two\nlines.", "```\na\n\nb\n```", "End"]
        );
        assert_eq!(split("partial wo"), vec!["partial wo"]);
        assert!(split("").is_empty());
    }

    #[::core::prelude::v1::test]
    fn a_cut_never_splits_a_character() {
        let s = "olá";
        assert_eq!(boundary(s, 3), 2, "inside á goes back to its start");
        assert_eq!(boundary(s, 9), 4);
    }
}
