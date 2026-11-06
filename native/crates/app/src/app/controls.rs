//! The controls a setting is made of (components/ui/settings.tsx, switch.tsx,
//! the segmented radiogroups of SettingsView.tsx), drawn in the app's palette
//! so every Settings section, and a project's own settings later, look alike.
//! Each takes plain values and a callback, so they hold no state themselves:
//! get callbacks from `cx.listener(|this, v: &str, window, cx| …)`.
use std::rc::Rc;

use agentbox_i18n::t;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::Button;
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::{Disableable as _, Sizable as _, h_flex, v_flex};

use crate::theme;

/// Pick is what a segmented control or a stepper calls with the new value.
pub type Pick<V> = Rc<dyn Fn(&V, &mut Window, &mut App)>;

/// OnClick is a click handler a Row keeps.
pub type OnClick = Rc<dyn Fn(&ClickEvent, &mut Window, &mut App)>;

/// toggle is a switch: a pill with its knob at the right when on.
pub fn toggle(
    id: impl Into<ElementId>,
    on: bool,
    disabled: bool,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .w(px(34.))
        .h(px(20.))
        .p(px(2.))
        .rounded_full()
        .flex()
        .when(on, |el| el.justify_end())
        .bg(if on {
            theme::accent()
        } else {
            theme::surface_strong()
        })
        .border_1()
        .border_color(if on { theme::accent() } else { theme::line() })
        .when(disabled, |el| el.opacity(0.5))
        .when(!disabled, |el| el.cursor_pointer().on_click(on_click))
        .child(div().size(px(14.)).rounded_full().bg(if on {
            theme::on_brand()
        } else {
            theme::muted()
        }))
        .into_any_element()
}

/// segmented is a row of choices, one of them picked: a radiogroup, as
/// Appearance and the update channel are (and a short select).
pub fn segmented(
    id: &str,
    options: Vec<(SharedString, String)>,
    current: Option<&str>,
    disabled: bool,
    on_pick: Pick<str>,
) -> AnyElement {
    // Its own width, not its column's: a column stretches what it holds.
    h_flex()
        .child(segments(id, options, current, disabled, on_pick))
        .into_any_element()
}

fn segments(
    id: &str,
    options: Vec<(SharedString, String)>,
    current: Option<&str>,
    disabled: bool,
    on_pick: Pick<str>,
) -> impl IntoElement {
    h_flex()
        .flex_wrap()
        .w_auto()
        .p(px(2.))
        .gap(px(2.))
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .bg(theme::card())
        .children(options.into_iter().map(|(value, label)| {
            let on = current == Some(value.as_ref());
            let pick = on_pick.clone();
            div()
                .id(SharedString::from(format!("{id}-{value}")))
                .px_3()
                .py_1()
                .rounded_md()
                .text_sm()
                .when(on, |el| {
                    el.bg(theme::surface_strong())
                        .text_color(theme::primary())
                        .font_weight(FontWeight::MEDIUM)
                })
                .when(!on, |el| el.text_color(theme::muted()))
                .when(disabled, |el| el.opacity(0.5))
                .when(!disabled && !on, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.text_color(theme::primary()))
                        .on_click(move |_, window, cx| pick(&value, window, cx))
                })
                .child(label)
        }))
}

/// Btn is how loud a button is: the one thing to do, one of several, or
/// one that's barely there (Electron's primary, secondary and ghost).
#[derive(Clone, Copy, PartialEq)]
pub enum Btn {
    Primary,
    Secondary,
    Ghost,
    /// One that destroys something (Electron's destructive).
    Danger,
}

/// button is a labelled button in the app's palette.
pub fn button(
    id: impl Into<ElementId>,
    label: impl Into<SharedString>,
    kind: Btn,
    disabled: bool,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .px_3()
        .py(px(5.))
        .rounded_md()
        .text_sm()
        .font_weight(FontWeight::MEDIUM)
        .map(|el| match kind {
            Btn::Primary => el.bg(theme::brand(500)).text_color(theme::on_brand()),
            Btn::Secondary => el
                .bg(theme::surface_raised())
                .border_1()
                .border_color(theme::line())
                .text_color(theme::primary()),
            Btn::Ghost => el.text_color(theme::muted()),
            Btn::Danger => el.bg(theme::error()).text_color(theme::on_bright()),
        })
        .when(disabled, |el| el.opacity(0.5))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .hover(move |s| match kind {
                    Btn::Primary | Btn::Danger => s.opacity(0.9),
                    _ => s.bg(theme::surface_strong()).text_color(theme::primary()),
                })
                .on_click(on_click)
        })
        .child(crate::icons::glyph(label))
        .into_any_element()
}

/// badge is a word in a pill beside a name: default, valid, optional.
pub fn badge(text: impl Into<SharedString>, color: Hsla) -> AnyElement {
    div()
        .flex_none()
        .px_1p5()
        .rounded_full()
        .bg(color.opacity(0.15))
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .text_color(color)
        .child(text.into())
        .into_any_element()
}

/// labeled is a form field: its name over its input.
pub fn labeled(label: impl Into<SharedString>, input: impl IntoElement) -> AnyElement {
    v_flex()
        .flex_1()
        .min_w(px(140.))
        .gap_1()
        .child(
            div()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .child(label.into()),
        )
        .child(input)
        .into_any_element()
}

/// Choice is one entry of a select: what it saves, what it reads, and a
/// line under it when it has one; group is the heading it sits under.
#[derive(Clone, Default)]
pub struct Choice {
    pub value: SharedString,
    pub label: String,
    pub description: String,
    pub group: String,
    pub disabled: bool,
}

impl Choice {
    pub fn new(value: impl Into<SharedString>, label: impl Into<String>) -> Self {
        Self {
            value: value.into(),
            label: label.into(),
            ..Default::default()
        }
    }
    pub fn described(mut self, description: impl Into<String>) -> Self {
        self.description = description.into();
        self
    }
}

/// select is a button reading the current choice that opens a menu of
/// them all (ui/select.tsx, and the model's menu): for lists too long, or
/// too wordy, for a segmented control. A choice no longer on offer keeps
/// its place, as the caller adds it.
pub fn select(
    id: &str,
    choices: Vec<Choice>,
    current: &str,
    disabled: bool,
    on_pick: Pick<str>,
) -> AnyElement {
    select_at(id, choices, current, disabled, on_pick, Anchor::TopRight)
}

/// select_at is a select whose menu hangs from another corner: TopLeft
/// for one under a setting, at the column's left.
pub fn select_at(
    id: &str,
    choices: Vec<Choice>,
    current: &str,
    disabled: bool,
    on_pick: Pick<str>,
    anchor: Anchor,
) -> AnyElement {
    let label = choices
        .iter()
        .find(|c| c.value.as_ref() == current)
        .map(|c| c.label.clone())
        .unwrap_or_else(|| current.to_string());
    let current = SharedString::from(current.to_string());
    let choices = Rc::new(choices);
    // Its own width, not its column's, as segmented's.
    let button = Button::new(SharedString::from(format!("select-{id}")))
        .outline()
        .small()
        .min_w(px(160.))
        .max_w(px(280.))
        .label(format!("{label}  ▾"))
        .disabled(disabled)
        .dropdown_menu_with_anchor(anchor, move |menu, _, _| {
            let mut menu = menu
                .min_w(px(240.))
                .max_w(px(380.))
                .max_h(px(420.))
                .scrollable(true);
            let mut group = String::new();
            for c in choices.iter() {
                if c.group != group {
                    group = c.group.clone();
                    menu = menu.separator();
                    if !group.is_empty() {
                        menu = menu.label(group.clone());
                    }
                }
                // gpui-component's check is an icon, and this app ships no
                // icon assets: the mark is text, in a column of its own.
                let (name, about, on) =
                    (c.label.clone(), c.description.clone(), c.value == current);
                let item = PopupMenuItem::element(move |_, _| {
                    h_flex()
                        .items_start()
                        .gap_2()
                        .child(
                            div()
                                .w(px(12.))
                                .flex_none()
                                .text_sm()
                                .text_color(theme::accent())
                                .child(crate::icons::of(if on { "✓" } else { "" })),
                        )
                        .child(
                            v_flex()
                                .py_0p5()
                                .child(div().text_sm().child(name.clone()))
                                .when(!about.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .text_xs()
                                            .text_color(theme::muted())
                                            .child(about.clone()),
                                    )
                                }),
                        )
                });
                let (pick, value) = (on_pick.clone(), c.value.clone());
                menu = menu.item(
                    item.disabled(c.disabled)
                        .on_click(move |_, window, cx| pick(&value, window, cx)),
                );
            }
            menu
        });
    h_flex().child(button).into_any_element()
}

/// stepper is a number within bounds, moved by a step: − value +.
pub fn stepper(
    id: &str,
    value: i64,
    (min, max, step): (i64, i64, i64),
    unit: &str,
    disabled: bool,
    on_change: Pick<i64>,
) -> AnyElement {
    let button = |name: &str, label: &'static str, next: i64| {
        let change = on_change.clone();
        let usable = !disabled && next != value;
        div()
            .id(SharedString::from(format!("{id}-{name}")))
            .w(px(26.))
            .h(px(26.))
            .flex()
            .items_center()
            .justify_center()
            .rounded_md()
            .text_color(if usable {
                theme::primary()
            } else {
                theme::faint()
            })
            .when(usable, |el| {
                el.cursor_pointer()
                    .hover(|s| s.bg(theme::surface_strong()))
                    .on_click(move |_, window, cx| change(&next, window, cx))
            })
            .child(label)
    };
    h_flex()
        .w_auto()
        .p(px(2.))
        .gap_1()
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .bg(theme::card())
        .child(button("down", "−", (value - step).max(min)))
        .child(
            div()
                .min_w(px(48.))
                .text_center()
                .text_sm()
                .text_color(theme::primary())
                .child(format!("{value}{unit}")),
        )
        .child(button("up", "+", (value + step).min(max)))
        .into_any_element()
}

/// note is a line under a setting's control: why it's off, what's out.
pub fn note(text: impl Into<SharedString>) -> AnyElement {
    note_in(text, theme::muted())
}

/// note_in is a note in a colour of its own: a warning, an error, done.
pub fn note_in(text: impl Into<SharedString>, color: Hsla) -> AnyElement {
    div()
        .min_w_0()
        .text_xs()
        .line_height(rems(1.25))
        .text_color(color)
        .child(text.into())
        .into_any_element()
}

/// Row is one setting (SettingRow): its name, what it does, "More" for the
/// rest of that when there is a rest, its control beside them and anything
/// else under them; marked at its edge when it's changed from its default.
pub struct Row {
    pub id: SharedString,
    pub label: String,
    pub description: String,
    pub details: Option<String>,
    /// more is whether the details are open; on_more opens or closes them.
    pub more: bool,
    pub on_more: Option<OnClick>,
    pub modified: bool,
    pub control: Option<AnyElement>,
    pub body: Vec<AnyElement>,
}

impl Row {
    pub fn new(id: impl Into<SharedString>, label: String, description: String) -> Self {
        Self {
            id: id.into(),
            label,
            description,
            details: None,
            more: false,
            on_more: None,
            modified: false,
            control: None,
            body: Vec::new(),
        }
    }

    pub fn render(self) -> AnyElement {
        let more_id = SharedString::from(format!("{}-more", self.id));
        let words = v_flex()
            .flex_1()
            .min_w_0()
            .gap_1()
            .child(
                div()
                    .text_sm()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::primary())
                    .child(self.label),
            )
            .when(!self.description.is_empty(), |el| {
                el.child(
                    div()
                        .text_xs()
                        .line_height(rems(1.25))
                        .text_color(theme::muted())
                        .child(self.description),
                )
            })
            .when_some(
                self.details.clone().zip(self.on_more.clone()),
                |el, (details, on_more)| {
                    el.when(self.more, |el| {
                        el.child(
                            div()
                                .text_xs()
                                .line_height(rems(1.25))
                                .text_color(theme::muted())
                                .child(details),
                        )
                    })
                    .child(
                        div()
                            .id(more_id)
                            .w_auto()
                            .text_xs()
                            .text_color(theme::faint())
                            .underline()
                            .cursor_pointer()
                            .hover(|s| s.text_color(theme::primary()))
                            .on_click(move |ev, window, cx| on_more(ev, window, cx))
                            .child(if self.more {
                                t("settings.row.less")
                            } else {
                                t("settings.row.more")
                            }),
                    )
                },
            );
        div()
            .id(self.id)
            .relative()
            .px_5()
            .py_4()
            .when(self.modified, |el| {
                el.child(
                    div()
                        .absolute()
                        .left_0()
                        .top_4()
                        .bottom_4()
                        .w(px(2.))
                        .rounded_r_full()
                        .bg(theme::brand(500)),
                )
            })
            .child(
                v_flex()
                    .gap_3()
                    .child(
                        h_flex()
                            .items_start()
                            .gap_6()
                            .child(words)
                            .children(self.control.map(|c| div().flex_none().child(c))),
                    )
                    .children(self.body),
            )
            .into_any_element()
    }
}

thread_local! {
    /// The window's size as it was last drawn, for overlays drawn from deep
    /// in a page.
    static VIEWPORT: std::cell::Cell<Size<Pixels>> = const { std::cell::Cell::new(Size { width: px(0.), height: px(0.) }) };
}

/// set_viewport records the window's size, each frame.
pub fn set_viewport(size: Size<Pixels>) {
    VIEWPORT.with(|v| v.set(size));
}

/// window_modal is a dialog over the whole window from wherever in a page it
/// is drawn (the Electron app's Dialog): the page dimmed behind it, a click
/// there closing it.
pub fn window_modal(
    id: impl Into<SharedString>,
    width: Pixels,
    card: impl IntoElement,
    on_backdrop: OnClick,
) -> AnyElement {
    let id: SharedString = id.into();
    let size = VIEWPORT.with(|v| v.get());
    deferred(
        anchored().position(point(px(0.), px(0.))).child(
            div()
                .id(SharedString::from(format!("{id}-backdrop")))
                .w(size.width)
                .h(size.height)
                .bg(theme::scrim())
                .flex()
                .items_center()
                .justify_center()
                .occlude()
                .on_click(move |e, w, cx| on_backdrop(e, w, cx))
                .child(
                    div()
                        .id(SharedString::from(format!("{id}-card")))
                        .w(width)
                        .max_w(relative(0.92))
                        .max_h(relative(0.88))
                        .flex()
                        .flex_col()
                        .rounded_xl()
                        .bg(theme::card())
                        .border_1()
                        .border_color(theme::line())
                        .shadow_lg()
                        .overflow_hidden()
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .on_click(|_, _, cx| cx.stop_propagation())
                        .child(card),
                ),
        ),
    )
    .with_priority(2)
    .into_any_element()
}
