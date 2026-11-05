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

    /// choices are Home and the projects matching the search, as (name, label).
    fn new_task_choices(&self, cx: &App) -> Vec<(String, String)> {
        let Some(n) = &self.new_task else {
            return Vec::new();
        };
        let q = n.search.read(cx).value().trim().to_lowercase();
        let mut out = Vec::new();
        if q.is_empty() {
            out.push((HOME_PROJECT.to_string(), t("v2.newTask.home")));
        }
        for p in &self.projects {
            let label = if p.display_name.is_empty() {
                p.name.clone()
            } else {
                p.display_name.clone()
            };
            if q.is_empty() || label.to_lowercase().contains(&q) || p.name.contains(&q) {
                out.push((p.name.clone(), label));
            }
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

    pub(super) fn render_new_task(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let task = self.new_task.as_ref()?;
        let choices = self.new_task_choices(cx);
        Some(
            div()
                .id("new-task")
                .absolute()
                .inset_0()
                .bg(gpui::black().opacity(0.5))
                .flex()
                .justify_center()
                .pt(px(120.))
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
                        .w(px(520.))
                        .max_h(px(480.))
                        .p_4()
                        .gap_3()
                        .rounded_xl()
                        .bg(theme::panel())
                        .border_1()
                        .border_color(theme::border())
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .child(
                            div()
                                .text_color(theme::fg())
                                .font_weight(FontWeight::SEMIBOLD)
                                .child(t("v2.newTask.title")),
                        )
                        .child(Input::new(&task.search))
                        .child(
                            v_flex()
                                .id("new-task-list")
                                .overflow_y_scroll()
                                .when(choices.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .p_2()
                                            .text_sm()
                                            .text_color(theme::faint())
                                            .child(t("v2.newTask.none")),
                                    )
                                })
                                .children(choices.into_iter().enumerate().map(
                                    |(i, (name, label))| {
                                        let on = i == task.selected;
                                        h_flex()
                                            .id(SharedString::from(format!("nt-{name}")))
                                            .px_2()
                                            .py_1p5()
                                            .gap_3()
                                            .rounded_md()
                                            .cursor_pointer()
                                            .when(on, |el| el.bg(theme::hover()))
                                            .hover(|el| el.bg(theme::raised()))
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, window, cx| {
                                                    this.pick_new_task(Some(i), window, cx)
                                                },
                                            ))
                                            .child(
                                                div()
                                                    .w_4()
                                                    .text_xs()
                                                    .text_color(theme::faint())
                                                    .child(if i < 9 {
                                                        (i + 1).to_string()
                                                    } else {
                                                        String::new()
                                                    }),
                                            )
                                            .child(
                                                div()
                                                    .text_sm()
                                                    .text_color(if on {
                                                        theme::fg()
                                                    } else {
                                                        theme::muted()
                                                    })
                                                    .child(label),
                                            )
                                    },
                                )),
                        )
                        .child(
                            h_flex()
                                .gap_4()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(t("v2.newTask.hintMove"))
                                .child(t("v2.newTask.hintOpen"))
                                .child(t("v2.newTask.hintDigit")),
                        ),
                )
                .into_any_element(),
        )
    }
}
