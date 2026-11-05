//! Toasts (desktop/src/renderer/components/Toasts.tsx, sonner): the app's
//! small notices, stacked at the window's bottom right — what was done
//! (merged, sent), what failed and why, and now and then a way back (an
//! action, such as Cancel). Each goes by itself after a few seconds, or with
//! its ✕. Anything with an App pushes one: `toasts::success(cx, …)`.
use std::rc::Rc;
use std::time::Duration;

use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::theme;

/// How long a toast stays: sonner's 4 s, longer for one to act on or read.
const SHORT: Duration = Duration::from_secs(4);
const LONG: Duration = Duration::from_secs(7);
/// How many show at once; the oldest goes first.
const MAX: usize = 4;

#[derive(Clone, Copy, PartialEq, Debug)]
pub enum Kind {
    Info,
    Success,
    Error,
}

pub type Action = Rc<dyn Fn(&mut Window, &mut App)>;

/// Toast is one notice: what happened, a line under it, and an action.
#[derive(Clone)]
pub struct Toast {
    pub kind: Kind,
    pub title: SharedString,
    pub description: Option<SharedString>,
    pub action: Option<(SharedString, Action)>,
}

impl Toast {
    pub fn new(kind: Kind, title: impl Into<SharedString>) -> Self {
        Self {
            kind,
            title: title.into(),
            description: None,
            action: None,
        }
    }

    pub fn description(mut self, d: impl Into<SharedString>) -> Self {
        let d = d.into();
        if !d.is_empty() {
            self.description = Some(d);
        }
        self
    }

    pub fn action(
        mut self,
        label: impl Into<SharedString>,
        f: impl Fn(&mut Window, &mut App) + 'static,
    ) -> Self {
        self.action = Some((label.into(), Rc::new(f)));
        self
    }

    /// show puts it on screen.
    pub fn show(self, cx: &mut App) {
        let Some(stack) = cx.try_global::<Stack>().map(|s| s.0.clone()) else {
            return;
        };
        stack.update(cx, |s, cx| s.push(self, cx));
    }
}

pub fn info(cx: &mut App, title: impl Into<SharedString>) {
    Toast::new(Kind::Info, title).show(cx)
}

pub fn error(cx: &mut App, title: impl Into<SharedString>) {
    Toast::new(Kind::Error, title).show(cx)
}

/// Toasts is the stack on screen.
pub struct Toasts {
    items: Vec<(u64, Toast)>,
    next: u64,
}

struct Stack(Entity<Toasts>);
impl Global for Stack {}

/// init makes the stack, for the window to draw over everything else.
pub fn init(cx: &mut App) -> Entity<Toasts> {
    let stack = cx.new(|_| Toasts {
        items: Vec::new(),
        next: 0,
    });
    cx.set_global(Stack(stack.clone()));
    stack
}

impl Toasts {
    fn push(&mut self, toast: Toast, cx: &mut Context<Self>) {
        let id = self.next;
        self.next += 1;
        let stay = if toast.action.is_some() || toast.kind == Kind::Error {
            LONG
        } else {
            SHORT
        };
        self.items.push((id, toast));
        if self.items.len() > MAX {
            self.items.remove(0);
        }
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(stay).await;
            this.update(cx, |this, cx| this.dismiss(id, cx)).ok();
        })
        .detach();
        cx.notify();
    }

    fn dismiss(&mut self, id: u64, cx: &mut Context<Self>) {
        self.items.retain(|(i, _)| *i != id);
        cx.notify();
    }
}

impl Render for Toasts {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if self.items.is_empty() {
            return div().into_any_element();
        }
        v_flex()
            .id("toasts")
            .absolute()
            .bottom_4()
            .right_4()
            .w(px(356.))
            .gap_2()
            .children(self.items.iter().map(|(id, toast)| {
                let id = *id;
                let (mark, color) = match toast.kind {
                    Kind::Success => ("✓", theme::running()),
                    Kind::Error => ("✕", theme::error()),
                    Kind::Info => ("•", theme::muted()),
                };
                h_flex()
                    .id(("toast", id))
                    .items_start()
                    .gap_2p5()
                    .px_4()
                    .py_3()
                    .rounded(px(14.))
                    .bg(theme::raised())
                    .border_1()
                    .border_color(if toast.kind == Kind::Error {
                        theme::error().opacity(0.5)
                    } else {
                        theme::border()
                    })
                    .shadow_lg()
                    // A click on it stays on it, never reaching what's under.
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        div()
                            .flex_none()
                            .w(px(14.))
                            .text_sm()
                            .font_weight(FontWeight::BOLD)
                            .text_color(color)
                            .child(mark),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::fg())
                                    .child(toast.title.clone()),
                            )
                            .when_some(toast.description.clone(), |el, d| {
                                el.child(div().text_xs().text_color(theme::muted()).child(d))
                            }),
                    )
                    .when_some(toast.action.clone(), |el, (label, act)| {
                        el.child(
                            div()
                                .id(("toast-action", id))
                                .flex_none()
                                .px_2()
                                .py_0p5()
                                .rounded_md()
                                .bg(theme::hover())
                                .text_xs()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::fg())
                                .cursor_pointer()
                                .hover(|s| s.bg(theme::border()))
                                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                    act(window, cx);
                                    this.dismiss(id, cx);
                                }))
                                .child(label),
                        )
                    })
                    .child(
                        div()
                            .id(("toast-close", id))
                            .flex_none()
                            .text_xs()
                            .text_color(theme::faint())
                            .cursor_pointer()
                            .hover(|s| s.text_color(theme::fg()))
                            .on_click(
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.dismiss(id, cx)
                                }),
                            )
                            .child("✕"),
                    )
            }))
            .into_any_element()
    }
}
