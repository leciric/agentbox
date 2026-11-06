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
    // Electron's Switch: a white thumb on line-strong, on the brand when on;
    // the thumb travels on a spring rather than jumping.
    let id: ElementId = id.into();
    let knob = ElementId::Name(format!("{id}-knob").into());
    div()
        .id(id)
        .flex_none()
        .relative()
        .w(px(36.))
        .h(px(20.))
        .rounded_full()
        .bg(if on {
            theme::brand(500)
        } else {
            theme::line_strong()
        })
        .border_1()
        .border_color(if on {
            gpui::transparent_black()
        } else {
            theme::line_vivid()
        })
        .when(disabled, |el| el.opacity(0.45))
        .when(!disabled, |el| el.cursor_pointer().on_click(on_click))
        .child(
            div()
                .absolute()
                .top(px(1.))
                .size(px(16.))
                .rounded_full()
                .bg(gpui::white())
                .shadow(theme::shadow_card())
                .with_spring(
                    knob,
                    SpringAnimation::new(crate::motion::SNAPPY).to(if on { 1f32 } else { 0. }),
                    |el, x| el.left(px(1. + 16. * x)),
                ),
        )
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
        .bg(theme::surface())
        .children(options.into_iter().map(|(value, label)| {
            let on = current == Some(value.as_ref());
            let pick = on_pick.clone();
            div()
                .id(SharedString::from(format!("{id}-{value}")))
                .px_3()
                .py_1()
                .rounded_md()
                .text_sm()
                .text_size(theme::text::UI)
                .when(on, |el| {
                    el.bg(theme::surface_vivid())
                        .text_color(theme::title())
                        .font_weight(FontWeight::MEDIUM)
                        .shadow(theme::shadow_card())
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

/// choices is Settings' radiogroup (SettingsView.tsx's Appearance, Language,
/// update channel and pages-stack pickers): rounded-xl border-line bg-rail
/// p-0.5, each choice h-8 rounded-[10px] px-3 text-ui font-medium with a
/// 15px icon, the picked one on surface-strong.
pub fn choices(
    id: &str,
    options: Vec<(SharedString, String, Option<&'static str>)>,
    current: Option<&str>,
    disabled: bool,
    on_pick: Pick<str>,
) -> AnyElement {
    h_flex()
        .child(
            h_flex()
                .flex_wrap()
                .w_auto()
                .p(px(2.))
                .rounded_xl()
                .border_1()
                .border_color(theme::line())
                .bg(theme::rail())
                .children(options.into_iter().map(|(value, label, icon)| {
                    let on = current == Some(value.as_ref());
                    let pick = on_pick.clone();
                    h_flex()
                        .id(SharedString::from(format!("{id}-{value}")))
                        .h_8()
                        .gap(px(6.))
                        .px_3()
                        .rounded(px(10.))
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .when(on, |el| {
                            el.bg(theme::surface_strong()).text_color(theme::title())
                        })
                        .when(!on, |el| el.text_color(theme::muted()))
                        .when(disabled, |el| el.opacity(0.5))
                        .when(!disabled && !on, |el| {
                            el.cursor_pointer()
                                .hover(|s| s.text_color(theme::primary()))
                                .on_click(move |_, window, cx| pick(&value, window, cx))
                        })
                        .when_some(icon, |el, i| el.child(crate::icons::ic(i).size(px(15.))))
                        .child(label)
                })),
        )
        .into_any_element()
}

/// SCROLLBAR is the width Chromium's thin scrollbar takes from a scroller
/// that overflows: Electron's content is laid out in what's left.
pub const SCROLLBAR: Pixels = px(10.);

/// scrollbar draws, over the scroller `handle` tracks, the thin scrollbar
/// Chromium gives Electron's (`scrollbar-width: thin` in line-vivid): an
/// arrow at each end in a 12px button, a 6px pill between, two pixels in
/// from the gutter's left. Put it after the scroller, in a relative parent.
/// `gutter` is whether this frame was laid out with the gutter: when the
/// scroller turns out to overflow, or not, otherwise, it draws again.
pub fn scrollbar(handle: &ScrollHandle, gutter: bool) -> AnyElement {
    let handle = handle.clone();
    canvas(
        |_, _, _| (),
        move |_, _, window, _| {
            let vp = handle.bounds();
            let x = vp.right() - SCROLLBAR;
            paint_scrollbar(
                vp,
                x,
                handle.max_offset().y,
                -handle.offset().y,
                gutter,
                window,
            );
        },
    )
    .absolute()
    .top_0()
    .left_0()
    .size_full()
    .into_any_element()
}

/// list_scrollbar is `scrollbar` for a list: drawn in the SCROLLBAR gutter
/// to the right of the list (its parent pads it by that much while `gutter`).
pub fn list_scrollbar(list: &ListState, gutter: bool) -> AnyElement {
    let list = list.clone();
    canvas(
        |_, _, _| (),
        move |_, _, window, _| {
            let vp = list.viewport_bounds();
            let max = list.max_offset_for_scrollbar().y;
            let scrolled = -list.scroll_px_offset_for_scrollbar().y;
            paint_scrollbar(vp, vp.right(), max, scrolled, gutter, window);
        },
    )
    .absolute()
    .top_0()
    .left_0()
    .size_full()
    .into_any_element()
}

/// list_overflows is whether a list scrolls, so lays out with the gutter.
pub fn list_overflows(list: &ListState) -> bool {
    list.max_offset_for_scrollbar().y > px(0.5)
}

fn paint_scrollbar(
    vp: Bounds<Pixels>,
    x: Pixels,
    max: Pixels,
    scrolled: Pixels,
    gutter: bool,
    window: &mut Window,
) {
    let overflow = max > px(0.5);
    if overflow != gutter {
        window.refresh();
    }
    if !overflow || vp.size.height < px(40.) {
        return;
    }
    let color = theme::line_vivid();
    let hovered = vp.contains(&window.mouse_position());
    // Chromium brightens the arrows of the scroller the pointer is in.
    let arrow = if hovered {
        gpui::white().opacity(0.75)
    } else {
        color
    };
    let top = vp.top();
    let bottom = vp.bottom();
    let row = |window: &mut Window, y: Pixels, from: f32, w: f32, c: Hsla| {
        window.paint_quad(fill(
            Bounds::new(point(x + px(from), y), size(px(w), px(1.))),
            c,
        ));
    };
    let up = if scrolled > px(0.5) { arrow } else { color };
    let down = if scrolled < max - px(0.5) {
        arrow
    } else {
        color
    };
    // Each arrow is three rows, 2, 4 and 6 wide, its point outward.
    for i in 0..3 {
        let i = i as f32;
        row(window, top + px(4. + i), 4. - i, 2. + 2. * i, up);
        row(window, bottom - px(7. - i), 2. + i, 6. - 2. * i, down);
    }
    let track = vp.size.height - px(24.);
    let content = vp.size.height + max;
    let len = (track * (vp.size.height / content)).round().max(px(18.));
    let at = ((scrolled / max).clamp(0., 1.) * (track - len)).round();
    window.paint_quad(
        fill(
            Bounds::new(point(x + px(2.), top + px(12.) + at), size(px(6.), len)),
            color,
        )
        .corner_radii(px(3.)),
    );
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
    /// Quiet danger: rose text that tints on hover (a row's Delete).
    DangerQuiet,
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
        // Electron's Button, size sm: 32 px tall, 13 px, rounded-lg; a
        // hover or press moves the fill a step rather than lighting it.
        .h(px(32.))
        .px_3()
        .flex()
        .items_center()
        .justify_center()
        .gap_2()
        .rounded_lg()
        .text_size(theme::text::UI)
        .font_weight(FontWeight::MEDIUM)
        .map(|el| match kind {
            Btn::Primary => el.bg(theme::brand(500)).text_color(theme::on_brand()),
            Btn::Secondary => el
                .bg(theme::surface_raised())
                .border_1()
                .border_color(theme::line())
                .text_color(theme::primary()),
            Btn::Ghost => el.text_color(theme::muted()),
            Btn::Danger => el.bg(theme::destructive(0)).text_color(gpui::white()),
            Btn::DangerQuiet => el.text_color(theme::rose(300)),
        })
        .when(disabled, |el| el.opacity(0.45))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .hover(move |s| match kind {
                    Btn::Primary => s.bg(theme::brand(600)),
                    Btn::Danger => s.bg(theme::destructive(1)),
                    Btn::DangerQuiet => s
                        .bg(theme::rose(400).opacity(0.1))
                        .text_color(theme::rose(200)),
                    Btn::Ghost => s.bg(theme::surface_raised()).text_color(theme::primary()),
                    Btn::Secondary => s.bg(theme::surface_strong()),
                })
                .active(move |s| match kind {
                    Btn::Primary => s.bg(theme::brand(700)),
                    Btn::Danger => s.bg(theme::destructive(2)),
                    Btn::DangerQuiet => s.bg(theme::rose(400).opacity(0.2)),
                    Btn::Ghost => s.bg(theme::surface_strong()),
                    Btn::Secondary => s.bg(theme::surface_vivid()),
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
    // Its own width, not its column's, as segmented's.
    let button = Button::new(SharedString::from(format!("select-{id}")))
        .outline()
        .small()
        .min_w(px(160.))
        .max_w(px(280.))
        .label(format!("{label}  ▾"))
        .disabled(disabled)
        .dropdown_menu_with_anchor(anchor, select_menu(choices, current, on_pick));
    h_flex().child(button).into_any_element()
}

/// Trigger is Electron's selectTrigger as a dropdown's trigger: its border
/// goes brand while the menu is open.
#[derive(IntoElement)]
pub struct Trigger {
    base: Stateful<Div>,
    open: bool,
}

impl Styled for Trigger {
    fn style(&mut self) -> &mut StyleRefinement {
        self.base.style()
    }
}

impl InteractiveElement for Trigger {
    fn interactivity(&mut self) -> &mut Interactivity {
        self.base.interactivity()
    }
}

impl gpui_component::Selectable for Trigger {
    fn selected(mut self, selected: bool) -> Self {
        self.open = selected;
        self
    }
    fn is_selected(&self) -> bool {
        self.open
    }
}

impl gpui_component::menu::DropdownMenu for Trigger {}

impl RenderOnce for Trigger {
    fn render(self, _: &mut Window, _: &mut App) -> impl IntoElement {
        let open = self.open;
        self.base
            .when(open, |el| el.border_color(theme::brand(400).opacity(0.6)))
    }
}

/// field_select is Settings' Select (ui/select.tsx): a full-width h-9
/// trigger, rounded-lg in line-strong on the sunken ground, its choice in
/// text-sm and ChevronsUpDown at its end, over the same menu as select's;
/// `icon` goes before the choice (the project's model picker's Sparkles).
pub fn field_select(
    id: &str,
    choices: Vec<Choice>,
    current: &str,
    disabled: bool,
    on_pick: Pick<str>,
    anchor: Anchor,
    icon: Option<(&'static str, Hsla)>,
) -> AnyElement {
    let label = choices
        .iter()
        .find(|c| c.value.as_ref() == current)
        .map(|c| c.label.clone())
        .unwrap_or_else(|| current.to_string());
    let base = h_flex()
        .id(SharedString::from(format!("select-{id}")))
        .w_full()
        .h_9()
        .gap_2()
        .px_3()
        .rounded_lg()
        .border_1()
        .border_color(theme::line_strong())
        .bg(theme::sunken())
        .text_size(theme::text::BODY)
        .line_height(px(20.))
        .text_color(theme::primary())
        .when(disabled, |el| el.opacity(0.5))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .hover(|s| s.border_color(theme::line_vivid()))
        })
        .when_some(icon, |el, (name, color)| {
            el.child(
                div()
                    .flex_none()
                    .text_color(color)
                    .child(crate::icons::ic(name).size(px(16.))),
            )
        })
        .child(div().flex_1().min_w_0().truncate().child(label))
        .child(
            div()
                .flex_none()
                .text_color(theme::subtle())
                .child(crate::icons::ic("chevrons-up-down").size(px(14.))),
        );
    let trigger = Trigger { base, open: false };
    if disabled {
        return div().w_full().child(trigger).into_any_element();
    }
    div()
        .w_full()
        .child(trigger.dropdown_menu_with_anchor(anchor, select_menu(choices, current, on_pick)))
        .into_any_element()
}

/// settings_select is field_select hung from its top right, as select is.
pub fn settings_select(
    id: &str,
    choices: Vec<Choice>,
    current: &str,
    disabled: bool,
    on_pick: Pick<str>,
) -> AnyElement {
    field_select(
        id,
        choices,
        current,
        disabled,
        on_pick,
        Anchor::TopRight,
        None,
    )
}

/// settings_select_at is field_select hung from another corner.
pub fn settings_select_at(
    id: &str,
    choices: Vec<Choice>,
    current: &str,
    disabled: bool,
    on_pick: Pick<str>,
    anchor: Anchor,
) -> AnyElement {
    field_select(id, choices, current, disabled, on_pick, anchor, None)
}

/// select_menu is a select's menu: every choice, grouped, the current one
/// checked, each with what it means under it.
fn select_menu(
    choices: Vec<Choice>,
    current: &str,
    on_pick: Pick<str>,
) -> impl Fn(
    gpui_component::menu::PopupMenu,
    &mut Window,
    &mut Context<gpui_component::menu::PopupMenu>,
) -> gpui_component::menu::PopupMenu
+ 'static {
    let current = SharedString::from(current.to_string());
    let choices = Rc::new(choices);
    move |menu, _, _| {
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
            let (name, about, on) = (c.label.clone(), c.description.clone(), c.value == current);
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
    }
}

/// range is Chromium's `<input type=range>` in the brand's accent (the chime's
/// volume, w-40): a pill track 8px high, filled to the thumb in brand-500 and
/// light grey past it, and a 16px brand thumb; a click or a drag along it
/// sets the value, 0 to 100.
pub fn range(id: &str, value: i64, disabled: bool, on_change: Pick<i64>) -> AnyElement {
    const W: f32 = 160.;
    let at = value.clamp(0, 100) as f32 / 100.;
    let center = 8. + at * (W - 16.);
    let bounds: Rc<std::cell::Cell<Bounds<Pixels>>> = Rc::default();
    let pick = {
        let bounds = bounds.clone();
        move |x: Pixels| {
            let b = bounds.get();
            let f = ((x - b.left() - px(8.)) / (b.size.width - px(16.))).clamp(0., 1.);
            (f * 100.).round() as i64
        }
    };
    let (down, drag) = (on_change.clone(), on_change);
    let (pick_down, pick_drag) = (pick.clone(), pick);
    let edge = |c: u32| gpui::rgb(c);
    div()
        .id(SharedString::from(format!("{id}-range")))
        .relative()
        .flex_none()
        .w(px(W))
        .h(px(16.))
        .when(disabled, |el| el.opacity(0.5))
        .child({
            let bounds = bounds.clone();
            canvas(move |b, _, _| bounds.set(b), |_, _, _, _| {})
                .absolute()
                .size_full()
        })
        // The track: filled to the thumb, the rest light.
        .child(
            div()
                .absolute()
                .top(px(4.))
                .left_0()
                .w(px(center))
                .h(px(8.))
                .rounded_l(px(4.))
                .border_y_1()
                .border_l_1()
                .border_color(edge(0x7d65b3))
                .bg(theme::brand(500)),
        )
        .child(
            div()
                .absolute()
                .top(px(4.))
                .left(px(center))
                .right_0()
                .h(px(8.))
                .rounded_r(px(4.))
                .border_y_1()
                .border_r_1()
                .border_color(edge(0xb2b2b2))
                .bg(edge(0xefefef)),
        )
        .child(
            div()
                .absolute()
                .top_0()
                .left(px(center - 8.))
                .size_4()
                .rounded_full()
                .bg(theme::brand(500)),
        )
        .when(!disabled, |el| {
            el.on_mouse_down(MouseButton::Left, move |e, window, cx| {
                down(&pick_down(e.position.x), window, cx)
            })
            .on_mouse_move(move |e, window, cx| {
                if e.pressed_button == Some(MouseButton::Left) {
                    drag(&pick_drag(e.position.x), window, cx)
                }
            })
        })
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
        .text_size(theme::text::CAPTION)
        .line_height(relative(1.625))
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
    /// code is a part of the description set as `<code>`: mono, tertiary
    /// (the project's id in its Name's).
    pub code: Option<String>,
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
            code: None,
        }
    }

    pub fn render(self) -> AnyElement {
        let more_id = SharedString::from(format!("{}-more", self.id));
        // SettingRow: the label text-ui font-medium; the description mt-1
        // text-caption leading-relaxed subtle, with "More" in the same line
        // after a space (text-tertiary, underlined in line-strong); the
        // details under it, mt-1.5 in the same type.
        let more = self.details.clone().zip(self.on_more.clone());
        let description = (!self.description.is_empty()).then(|| {
            // Chromium's two 19.5px lines are gpui's 19 and 20, with the
            // margin a pixel less and the pixel after the words.
            let words = div()
                .mt(px(3.))
                .text_size(theme::text::CAPTION)
                .line_height(px(20.))
                .text_color(theme::subtle());
            match &more {
                Some((_, on_more)) => {
                    let link = if self.more {
                        t("settings.row.less")
                    } else {
                        t("settings.row.more")
                    };
                    let text = format!("{} {link}", self.description);
                    let range = self.description.len() + 1..text.len();
                    let style = HighlightStyle {
                        color: Some(theme::tertiary()),
                        underline: Some(UnderlineStyle {
                            thickness: px(1.),
                            color: Some(theme::line_strong()),
                            wavy: false,
                        }),
                        ..Default::default()
                    };
                    let on_more = on_more.clone();
                    words.child(
                        InteractiveText::new(
                            more_id.clone(),
                            StyledText::new(text).with_highlights(vec![(range.clone(), style)]),
                        )
                        .on_click(vec![range], move |_, window, cx| {
                            on_more(&ClickEvent::default(), window, cx)
                        }),
                    )
                }
                None => match self
                    .code
                    .as_ref()
                    .and_then(|c| self.description.find(c.as_str()).map(|at| (at, c.len())))
                {
                    Some((at, len)) => {
                        let run = |len: usize, mono: bool| TextRun {
                            len,
                            font: if mono {
                                gpui::font(theme::MONO)
                            } else {
                                Font {
                                    features: theme::sans_features(),
                                    ..gpui::font(theme::SANS)
                                }
                            },
                            color: if mono {
                                theme::tertiary()
                            } else {
                                theme::subtle()
                            },
                            background_color: None,
                            underline: None,
                            strikethrough: None,
                        };
                        let total = self.description.len();
                        let runs =
                            vec![run(at, false), run(len, true), run(total - at - len, false)]
                                .into_iter()
                                .filter(|r| r.len > 0)
                                .collect();
                        words.child(StyledText::new(self.description.clone()).with_runs(runs))
                    }
                    None => words.child(self.description.clone()),
                },
            }
        });
        let words = v_flex()
            .flex_1()
            .min_w_0()
            .pb(px(1.))
            .child(
                div()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::primary())
                    .child(self.label),
            )
            .children(description)
            .when_some(more.filter(|_| self.more), |el, (details, _)| {
                el.child(
                    div()
                        .mt_1p5()
                        .text_size(theme::text::CAPTION)
                        .line_height(relative(1.625))
                        .text_color(theme::subtle())
                        .child(details),
                )
            });
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
                        .bg(theme::brand(400)),
                )
            })
            .child(
                v_flex()
                    .gap_3()
                    .child(
                        div()
                            .flex()
                            .when(NARROW_ROWS.with(|n| n.get()), |el| el.flex_col().gap_3())
                            .when(!NARROW_ROWS.with(|n| n.get()), |el| {
                                el.flex_row().items_start().gap_6()
                            })
                            .child(words)
                            // The control's column: w-64, its control at
                            // the right, so a group's controls line up.
                            .children(self.control.map(|c| {
                                let narrow = NARROW_ROWS.with(|n| n.get());
                                h_flex()
                                    .flex_none()
                                    .min_w_0()
                                    .items_center()
                                    .when(!narrow, |el| el.w(px(256.)).justify_end())
                                    .child(c)
                            })),
                    )
                    .children(self.body),
            )
            .into_any_element()
    }
}

thread_local! {
    /// Whether the Rows drawn now are in a column narrower than SettingRow's
    /// @lg (512px), where the control goes under the words.
    static NARROW_ROWS: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
}

/// narrow_rows draws `f`'s Rows stacked, as SettingRow is in a column under
/// 512px wide (the right-hand panel's project settings).
pub fn narrow_rows<R>(f: impl FnOnce() -> R) -> R {
    let was = NARROW_ROWS.with(|n| n.replace(true));
    let out = f();
    NARROW_ROWS.with(|n| n.set(was));
    out
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
                .child(crate::motion::pop_in(
                    div()
                        .id(SharedString::from(format!("{id}-card")))
                        .w(width)
                        .max_w(relative(0.92))
                        .max_h(relative(0.88))
                        .flex()
                        .flex_col()
                        .rounded(theme::radius::XXL)
                        .bg(theme::card())
                        .border_1()
                        .border_color(theme::line_strong())
                        .shadow(theme::shadow_modal())
                        .overflow_hidden()
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .on_click(|_, _, cx| cx.stop_propagation())
                        .child(card),
                    SharedString::from(format!("{id}-card-in")),
                )),
        ),
    )
    .with_priority(2)
    .into_any_element()
}
