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

/// blocks draws `text` a markdown block at a time, the newest fading in.
fn blocks(id: &str, text: &str) -> AnyElement {
    let parts = split(text);
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
                motion::enter(block, SharedString::from(format!("block-in-{id}-{k}")), 0)
                    .into_any_element()
            } else {
                block.into_any_element()
            }
        }))
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
