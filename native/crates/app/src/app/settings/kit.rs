//! Small pieces the later sections share: a card, an empty state, a
//! question asked over the window (a confirmation dialog), a checkbox, a link,
//! sizes and dates in the user's words.
use agentbox_i18n::t;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};
use std::rc::Rc;

use super::super::controls::{Btn, button};
use crate::theme;

/// card is a panel of its own (Electron's Panel): a list's row, a form.
pub(super) fn card(id: impl Into<ElementId>) -> Stateful<Div> {
    v_flex()
        .id(id)
        .px_4()
        .py_3()
        .gap_2()
        .rounded_xl()
        .border_1()
        .border_color(theme::border())
        .bg(theme::panel())
}

/// empty is EmptyState: a title and a line under it, in a card.
pub(super) fn empty(id: &str, title: String, body: String) -> AnyElement {
    card(SharedString::from(format!("empty-{id}")))
        .items_center()
        .py_8()
        .child(
            div()
                .text_sm()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::fg())
                .child(title),
        )
        .when(!body.is_empty(), |el| {
            el.child(
                div()
                    .max_w(px(420.))
                    .text_center()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(body),
            )
        })
        .into_any_element()
}

/// ask is ConfirmDialog drawn where the thing it asks about is: the
/// question, what it means, and the two ways out.
#[allow(clippy::too_many_arguments)]
pub(super) fn ask(
    id: &str,
    question: String,
    description: String,
    confirm: String,
    destructive: bool,
    busy: bool,
    on_yes: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
    on_no: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    // Asked over the window, as the Electron app's AlertDialog asks: a
    // click beside it is No.
    let no = Rc::new(on_no);
    let backdrop = no.clone();
    let card = v_flex()
        .id(SharedString::from(format!("ask-{id}")))
        .gap_4()
        .p_6()
        .child(
            div()
                .text_lg()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::fg())
                .child(question),
        )
        .when(!description.is_empty(), |el| {
            el.child(
                div()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(description),
            )
        })
        .child(
            h_flex()
                .gap_2()
                .justify_end()
                .child(button(
                    SharedString::from(format!("ask-{id}-no")),
                    t("common.cancel"),
                    Btn::Ghost,
                    false,
                    move |e, w, cx| no(e, w, cx),
                ))
                .child(button(
                    SharedString::from(format!("ask-{id}-yes")),
                    confirm,
                    if destructive {
                        Btn::Danger
                    } else {
                        Btn::Primary
                    },
                    busy,
                    on_yes,
                )),
        );
    crate::app::controls::window_modal(
        format!("ask-{id}"),
        px(460.),
        card,
        Rc::new(move |e: &ClickEvent, w: &mut Window, cx: &mut App| backdrop(e, w, cx)),
    )
}

/// check is a checkbox and its words.
pub(super) fn check(
    id: impl Into<ElementId>,
    on: bool,
    disabled: bool,
    label: impl IntoElement,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    h_flex()
        .id(id)
        .gap_2()
        .items_start()
        .text_sm()
        .text_color(theme::fg())
        .when(disabled, |el| el.opacity(0.5))
        .when(!disabled, |el| el.cursor_pointer().on_click(on_click))
        .child(
            div()
                .flex_none()
                .mt(px(2.))
                .size(px(14.))
                .rounded_sm()
                .border_1()
                .border_color(if on { theme::accent() } else { theme::border() })
                .bg(if on { theme::accent() } else { theme::bg() })
                .flex()
                .items_center()
                .justify_center()
                .text_color(gpui::white())
                .text_size(px(10.))
                .when(on, |el| el.child(crate::icons::of("✓"))),
        )
        .child(div().flex_1().min_w_0().child(label))
        .into_any_element()
}

/// link is a word that does something, in the accent.
pub(super) fn link(
    id: impl Into<ElementId>,
    label: impl Into<SharedString>,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .text_color(theme::accent())
        .cursor_pointer()
        .hover(|s| s.underline())
        .on_click(on_click)
        .child(label.into())
        .into_any_element()
}

/// mono is a path, a command or an address, as it's typed.
pub(super) fn mono(text: impl Into<SharedString>) -> AnyElement {
    div()
        .font_family(crate::theme::mono())
        .text_xs()
        .text_color(theme::muted())
        .child(text.into())
        .into_any_element()
}

/// title is a card's name.
pub(super) fn title(text: impl Into<SharedString>) -> AnyElement {
    div()
        .text_sm()
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::fg())
        .child(text.into())
        .into_any_element()
}

/// local_date reads an RFC 3339 time as the user's own date and time
/// (formatDateTime), or as it came when it can't be read.
pub(super) fn local_date_time(time: &str) -> String {
    crate::dates::date_time_of(time)
}

pub(super) fn local_date(time: &str) -> String {
    crate::dates::date_of(time)
}

/// enc is a path segment, percent-encoded.
pub(super) fn enc(s: &str) -> String {
    let mut out = String::new();
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

/// plain drops a rich message's tags (<url>, <mono>, <code>), keeping what's
/// inside them, for a line drawn in one style.
pub(crate) fn plain(text: &str) -> String {
    let mut out = String::new();
    let mut rest = text;
    while let Some(i) = rest.find('<') {
        out.push_str(&rest[..i]);
        let after = &rest[i..];
        match after.find('>') {
            Some(j)
                if after[1..j]
                    .trim_start_matches('/')
                    .chars()
                    .all(|c| c.is_ascii_alphanumeric())
                    && j > 1 =>
            {
                rest = &after[j + 1..];
            }
            _ => {
                out.push('<');
                rest = &after[1..];
            }
        }
    }
    out.push_str(rest);
    out
}

#[cfg(test)]
mod tests {
    #[test]
    fn plain_drops_tags_only() {
        assert_eq!(
            super::plain("Phones open <url>http://a:1/</url> (a < b)."),
            "Phones open http://a:1/ (a < b)."
        );
    }
}
