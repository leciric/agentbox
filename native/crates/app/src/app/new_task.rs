//! New task (components/v2/NewTaskDialog.tsx): where a task goes, picked from
//! the keyboard — type to search, ↑↓ and Enter, or a digit for one of the first
//! nine — and then that chat, a project's or Home's, opens with its composer
//! focused. It makes nothing itself.
use agentbox_api::HOME_PROJECT;
use agentbox_i18n::t;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::theme;

pub struct NewTask {
    search: Entity<InputState>,
    selected: usize,
    _sub: Subscription,
}

impl AppView {
    pub(super) fn open_new_task(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let search = cx.new(|cx| InputState::new(window, cx).placeholder(t("v2.newTask.search")));
        let sub = cx.subscribe_in(
            &search,
            window,
            |this, _, ev: &InputEvent, window, cx| match ev {
                InputEvent::Change => {
                    if let Some(n) = this.new_task.as_mut() {
                        n.selected = 0;
                    }
                    cx.notify();
                }
                InputEvent::PressEnter { .. } => this.pick_new_task(None, window, cx),
                _ => {}
            },
        );
        search.update(cx, |s, cx| s.focus(window, cx));
        self.new_task = Some(NewTask {
            search,
            selected: 0,
            _sub: sub,
        });
        cx.notify();
    }

    /// choices are the projects matching the search, the most recently
    /// talked to first, then Home (newTask.ts taskTargets), as (name, label).
    fn new_task_choices(&self, cx: &App) -> Vec<(String, String)> {
        let Some(n) = &self.new_task else {
            return Vec::new();
        };
        let q = n.search.read(cx).value().to_lowercase();
        let words: Vec<&str> = q.split_whitespace().collect();
        let matches = |name: &str, label: &str| {
            let label = label.to_lowercase();
            words
                .iter()
                .all(|w| label.contains(w) || name.to_lowercase().contains(w))
        };
        let mut out: Vec<(String, String, i64)> = Vec::new();
        for p in &self.projects {
            let label = if p.display_name.is_empty() {
                p.name.clone()
            } else {
                p.display_name.clone()
            };
            if matches(&p.name, &label) {
                let at = self.lead_line_at(&p.name);
                out.push((p.name.clone(), label, at));
            }
        }
        out.sort_by(|a, b| b.2.cmp(&a.2));
        let mut out: Vec<(String, String)> = out.into_iter().map(|(n, l, _)| (n, l)).collect();
        let home = t("v2.newTask.home");
        if matches("", &home) {
            out.push((HOME_PROJECT.to_string(), home));
        }
        out
    }

    fn pick_new_task(&mut self, index: Option<usize>, window: &mut Window, cx: &mut Context<Self>) {
        let choices = self.new_task_choices(cx);
        let ix = index.unwrap_or_else(|| self.new_task.as_ref().map_or(0, |n| n.selected));
        let Some((name, _)) = choices.get(ix).cloned() else {
            return;
        };
        self.new_task = None;
        self.select_project(name, window, cx);
        if let Some(chat) = &self.chat {
            chat.update(cx, |c, cx| c.focus_composer(window, cx));
        }
    }

    fn on_new_task_key(&mut self, ev: &KeyDownEvent, window: &mut Window, cx: &mut Context<Self>) {
        let n = self.new_task_choices(cx).len();
        let Some(task) = self.new_task.as_mut() else {
            return;
        };
        let k = &ev.keystroke;
        match k.key.as_str() {
            "escape" => self.new_task = None,
            "down" if n > 0 => task.selected = (task.selected + 1) % n,
            "up" if n > 0 => task.selected = (task.selected + n - 1) % n,
            // Alt+digit, as the search box takes plain digits as text once typed in.
            d if d.len() == 1
                && d.as_bytes()[0].is_ascii_digit()
                && d != "0"
                && (k.modifiers.alt || task.search.read(cx).value().is_empty()) =>
            {
                let ix = (d.as_bytes()[0] - b'1') as usize;
                cx.stop_propagation();
                return self.pick_new_task(Some(ix), window, cx);
            }
            _ => return,
        }
        cx.stop_propagation();
        cx.notify();
    }

    /// render_new_task is NewTaskDialog: over the scrim, a card at 18vh,
    /// max-w-xl, its search over the targets and the keys.
    pub(super) fn render_new_task(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let task = self.new_task.as_ref()?;
        let choices = self.new_task_choices(cx);
        let none = choices.is_empty();
        let typed = !task.search.read(cx).value().is_empty();
        let vp = super::controls::viewport();
        let rows = choices.into_iter().enumerate().map(|(i, (name, label))| {
            let on = i == task.selected;
            let home = name == HOME_PROJECT;
            let project = self.projects.iter().find(|p| p.name == name);
            h_flex()
                .id(SharedString::from(format!("nt-{name}")))
                .w_full()
                .min_w_0()
                .items_center()
                .gap_3()
                .rounded_lg()
                .px_3()
                .py_2()
                .text_size(theme::text::BODY)
                .cursor_pointer()
                .when(home, |el| el.mt_1())
                .when(on, |el| {
                    el.bg(theme::surface_raised()).text_color(theme::title())
                })
                .when(!on, |el| el.text_color(theme::secondary()))
                .on_mouse_move(cx.listener(move |this, _: &MouseMoveEvent, _, cx| {
                    if let Some(n) = this.new_task.as_mut()
                        && n.selected != i
                    {
                        n.selected = i;
                        cx.notify();
                    }
                }))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.pick_new_task(Some(i), window, cx)
                }))
                .map(|el| match project.filter(|_| !home) {
                    Some(p) => el.child(self.project_icon(p, 24.)),
                    None => el.child(
                        div()
                            .flex_none()
                            .size_6()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_md()
                            .bg(theme::surface_strong())
                            .text_color(theme::secondary())
                            .child(crate::icons::ic("house").size_3p5()),
                    ),
                })
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .font_weight(FontWeight::MEDIUM)
                        .child(label),
                )
                .when(on, |el| {
                    el.child(
                        crate::icons::ic("corner-down-left")
                            .size_3p5()
                            .flex_none()
                            .text_color(theme::faint()),
                    )
                })
                .when(i < 9, |el| {
                    el.child(
                        div()
                            .flex_none()
                            .w_5()
                            .rounded(px(4.))
                            .bg(theme::surface_strong())
                            .text_center()
                            .font_family(theme::mono())
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child((i + 1).to_string()),
                    )
                })
        });
        Some(
            crate::motion::scrim_in(
                div()
                    .id("new-task")
                    .absolute()
                    .inset_0()
                    .bg(theme::scrim())
                    .flex()
                    .justify_center()
                    .pt(vp.height * 0.18)
                    .on_key_down(cx.listener(Self::on_new_task_key))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, _, _, cx| {
                            this.new_task = None;
                            cx.notify();
                        }),
                    )
                    .child(
                        v_flex()
                            .id("new-task-card")
                            .w(vp.width - px(32.))
                            .max_w(px(576.))
                            .h_auto()
                            .self_start()
                            .overflow_hidden()
                            .rounded(theme::radius::XXL)
                            .border_1()
                            .border_color(theme::line_strong())
                            .bg(theme::modal())
                            .shadow(vec![BoxShadow {
                                color: theme::shadow_deep(),
                                offset: point(px(0.), px(40.)),
                                blur_radius: px(120.),
                                spread_radius: px(-30.),
                                inset: false,
                            }])
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .child(
                                h_flex()
                                    .items_center()
                                    .gap(px(10.))
                                    .border_b_1()
                                    .border_color(theme::line())
                                    .px_4()
                                    .py_3()
                                    .child(
                                        crate::icons::ic("search")
                                            .size_4()
                                            .flex_none()
                                            .text_color(theme::faint()),
                                    )
                                    .child(
                                        div()
                                            .flex_1()
                                            .min_w_0()
                                            .h(px(24.))
                                            .text_size(theme::text::HEADING)
                                            .text_color(theme::primary())
                                            .child(
                                                // text-heading, its 24px line and no padding.
                                                Input::new(&task.search)
                                                    .appearance(false)
                                                    .px_0()
                                                    .py_0()
                                                    .h(px(24.))
                                                    .text_size(theme::text::HEADING)
                                                    .line_height(px(24.))
                                                    // gpui-component's editor sets its
                                                    // line lower than Chromium's input.
                                                    .mt(px(-3.)),
                                            ),
                                    ),
                            )
                            .child(
                                v_flex()
                                    .id("new-task-list")
                                    .max_h(vp.height * 0.5)
                                    .overflow_y_scroll()
                                    .p(px(6.))
                                    .children(rows)
                                    .when(none, |el| {
                                        el.child(
                                            div()
                                                .px_3()
                                                .py_4()
                                                .text_size(theme::text::UI)
                                                .text_color(theme::subtle())
                                                .child(t("v2.newTask.none")),
                                        )
                                    }),
                            )
                            .child(
                                h_flex()
                                    .items_center()
                                    .gap_3()
                                    .border_t_1()
                                    .border_color(theme::line())
                                    .px_4()
                                    .py_2()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::faint())
                                    .child(t("v2.newTask.hintMove"))
                                    .child(t("v2.newTask.hintOpen"))
                                    .child(t(if typed {
                                        "v2.newTask.hintAltDigit"
                                    } else {
                                        "v2.newTask.hintDigit"
                                    })),
                            ),
                    ),
                "idnew-task-in",
            )
            .into_any_element(),
        )
    }
}
