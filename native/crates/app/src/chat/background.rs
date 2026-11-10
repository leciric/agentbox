//! Background work, item by item (components/chat/BackgroundTasks.tsx): what
//! a session runs outside its turns (ChatSession.backgroundTasks) is a chip
//! each in the timeline, and behind a chip its status and its output as it
//! is written, read a piece at a time from GET …/chat/background/{task}. A
//! subagent's chip goes to its card instead, where all it said and did is.
use std::time::Duration;

use agentbox_api::{ChatBackgroundDetail, ChatBackgroundTask};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::ChatView;
use crate::theme;

/// Output is a task's output as the app holds it: bytes [start, end) of a
/// file of size bytes that is still being written.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Output {
    pub start: i64,
    pub end: i64,
    pub size: i64,
    pub text: String,
}

/// with_tail takes the latest read of the output's end: what is held stays
/// and what is new is added. A read that doesn't join what is held (the file
/// grew by more than a piece, or was replaced) starts over from it.
pub fn with_tail(held: Option<Output>, d: &ChatBackgroundDetail) -> Output {
    let end = d.output_start + d.output.len() as i64;
    let fresh = || Output {
        start: d.output_start,
        end,
        size: d.output_size,
        text: d.output.clone(),
    };
    let Some(mut h) = held else { return fresh() };
    if d.output_start > h.end || end < h.end || d.output_start < h.start {
        return fresh();
    }
    let from = (h.end - d.output_start) as usize;
    let Some(new) = d.output.get(from..) else {
        return fresh();
    };
    h.text.push_str(new);
    h.end = end;
    h.size = d.output_size;
    h
}

/// with_earlier puts the piece that ends where the held output starts
/// before it.
pub fn with_earlier(mut held: Output, d: &ChatBackgroundDetail) -> Output {
    if d.output_start + d.output.len() as i64 != held.start {
        return held;
    }
    held.text.insert_str(0, &d.output);
    held.start = d.output_start;
    held
}

/// Opened is the task shown over the chat.
pub struct Opened {
    id: String,
    task: ChatBackgroundTask,
    out: Option<Output>,
    error: Option<String>,
    /// Its first read has answered; earlier is a piece before being read.
    read: bool,
    earlier: bool,
    focus: FocusHandle,
    scroll: ScrollHandle,
    /// The output follows its end until it's scrolled up.
    follow: bool,
    _poll: Task<()>,
}

fn kind_icon(kind: &str) -> &'static str {
    match kind {
        "command" => "square-terminal",
        "monitor" => "eye",
        "subagent" => "bot",
        _ => "workflow",
    }
}

fn kind_label(kind: &str) -> String {
    t(match kind {
        "command" => "chat.background.kind.command",
        "monitor" => "chat.background.kind.monitor",
        "subagent" => "chat.background.kind.subagent",
        _ => "chat.background.kind.task",
    })
}

fn status_label(status: &str) -> String {
    t(match status {
        "running" => "chat.background.status.running",
        "done" => "chat.background.status.done",
        "failed" => "chat.background.status.failed",
        "timed_out" => "chat.background.status.timed_out",
        _ => "chat.background.status.stopped",
    })
}

fn status_color(status: &str) -> Hsla {
    match status {
        "done" => theme::emerald(400),
        "failed" => theme::rose(400),
        "timed_out" => theme::amber(400),
        _ => theme::faint(),
    }
}

fn status_icon(id: &str, status: &str) -> AnyElement {
    let icon = |name: &str| {
        crate::icons::ic(name)
            .size(px(14.))
            .text_color(status_color(status))
    };
    match status {
        "running" => crate::motion::spin(
            icon(crate::icons::SPINNER),
            SharedString::from(format!("bg-spin-{id}")),
        )
        .into_any_element(),
        "done" => icon("check").into_any_element(),
        "failed" => icon("circle-alert").into_any_element(),
        "timed_out" => icon("timer-off").into_any_element(),
        _ => icon("circle-slash").into_any_element(),
    }
}

impl ChatView {
    /// render_background_row is the timeline's row of them.
    pub(super) fn render_background_row(
        &self,
        names: &[String],
        tasks: &[ChatBackgroundTask],
        waiting: bool,
        view: &Entity<Self>,
    ) -> AnyElement {
        let row = h_flex()
            .mb_3()
            .gap_2()
            .items_start()
            .text_sm()
            .text_color(theme::faint())
            .child(div().mt(px(3.)).child(crate::icons::of("⌛")));
        if tasks.is_empty() {
            return row
                .child(tv(
                    "chat.timeline.background",
                    &[("tasks", names.join(", ").into())],
                ))
                .into_any_element();
        }
        let chips = tasks.iter().map(|task| {
            let v = view.clone();
            let task2 = task.clone();
            let to_card = task.kind == "subagent" && !task.item.is_empty();
            let tip = if to_card {
                "chat.background.openSubagent"
            } else {
                "chat.background.open"
            };
            h_flex()
                .id(SharedString::from(format!("bg-task-{}", task.id)))
                .min_w_0()
                .max_w_full()
                .gap_1p5()
                .px_2()
                .py_0p5()
                .rounded_md()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .text_size(theme::text::UI)
                .text_color(theme::secondary())
                .cursor_pointer()
                .hover(|el| {
                    el.border_color(theme::line())
                        .bg(theme::surface_raised())
                        .text_color(theme::primary())
                })
                .tooltip(move |w, cx| gpui_component::tooltip::Tooltip::new(t(tip)).build(w, cx))
                .on_click(move |_, window, cx| {
                    let task = task2.clone();
                    v.update(cx, |this, cx| {
                        if to_card {
                            this.reveal(task.item, cx);
                        } else {
                            this.open_background(task, window, cx);
                        }
                    })
                })
                .child(
                    crate::icons::ic(kind_icon(&task.kind))
                        .size(px(14.))
                        .text_color(theme::subtle()),
                )
                .child(div().min_w_0().truncate().child(task.title.clone()))
                .child(status_icon(&task.id, &task.status))
        });
        row.child(
            h_flex()
                .flex_1()
                .min_w_0()
                .flex_wrap()
                .gap_1p5()
                .child(div().py_0p5().child(t(if waiting {
                    "chat.background.awaiting"
                } else {
                    "chat.background.heading"
                })))
                .children(chips),
        )
        .into_any_element()
    }

    /// open_background shows a task over the chat and reads its output: again
    /// every second and a half while it runs, and once more as it ends.
    pub(super) fn open_background(
        &mut self,
        task: ChatBackgroundTask,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let focus = cx.focus_handle();
        focus.focus(window);
        let id = task.id.clone();
        let client = self.client.clone();
        let path = format!(
            "{}/chat/background/{}",
            self.prefix,
            crate::files::encode(&id)
        );
        let poll = cx.spawn({
            let id = id.clone();
            async move |this, cx| {
                loop {
                    let res = client.get::<ChatBackgroundDetail>(&path).await;
                    let mut running = false;
                    let shown = this
                        .update(cx, |this, cx| {
                            let Some(o) = this.background.as_mut().filter(|o| o.id == id) else {
                                return false;
                            };
                            o.read = true;
                            match res {
                                Ok(d) => {
                                    running = d.task.status == "running";
                                    if d.output_error.is_empty() {
                                        o.out = Some(with_tail(o.out.take(), &d));
                                        o.error = None;
                                    } else {
                                        o.error = Some(d.output_error.clone());
                                    }
                                    o.task = d.task;
                                }
                                Err(_) => o.error = Some(t("chat.background.gone")),
                            }
                            cx.notify();
                            true
                        })
                        .unwrap_or(false);
                    if !shown || !running {
                        break;
                    }
                    cx.background_executor()
                        .timer(Duration::from_millis(1500))
                        .await;
                }
            }
        });
        self.background = Some(Opened {
            id,
            task,
            out: None,
            error: None,
            read: false,
            earlier: false,
            focus,
            scroll: ScrollHandle::new(),
            follow: true,
            _poll: poll,
        });
        cx.notify();
    }

    fn close_background(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.background = None;
        self.focus_composer(window, cx);
        cx.notify();
    }

    /// earlier_background reads the piece before what is held.
    fn earlier_background(&mut self, cx: &mut Context<Self>) {
        let Some(o) = self.background.as_mut() else {
            return;
        };
        let Some(start) = o.out.as_ref().map(|b| b.start).filter(|s| *s > 0) else {
            return;
        };
        if o.earlier {
            return;
        }
        o.earlier = true;
        o.follow = false;
        let id = o.id.clone();
        let client = self.client.clone();
        let path = format!(
            "{}/chat/background/{}?before={start}",
            self.prefix,
            crate::files::encode(&id)
        );
        cx.spawn(async move |this, cx| {
            let res = client.get::<ChatBackgroundDetail>(&path).await;
            this.update(cx, |this, cx| {
                let Some(o) = this.background.as_mut().filter(|o| o.id == id) else {
                    return;
                };
                o.earlier = false;
                match res {
                    Ok(d) if d.output_error.is_empty() => {
                        o.out = o.out.take().map(|held| with_earlier(held, &d));
                        // What was being read is now further down: start at
                        // the top of the piece that came in.
                        o.scroll.set_offset(point(px(0.), px(0.)));
                    }
                    Ok(d) => crate::toasts::error(cx, d.output_error),
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// render_background is the opened task: a card over the whole window.
    pub(super) fn render_background(
        &mut self,
        window: &Window,
        cx: &mut Context<Self>,
    ) -> Option<AnyElement> {
        let o = self.background.as_mut()?;
        // The list knows of its end before the dialog's own read does.
        if let Some(listed) = self
            .session
            .background_tasks
            .iter()
            .find(|task| task.id == o.id)
        {
            if o.task.status == "running" || listed.status != "running" {
                o.task = listed.clone();
            }
        }
        // Follow the end until it's scrolled up from it.
        let (off, max) = (o.scroll.offset(), o.scroll.max_offset());
        if max.height > px(0.) {
            let at_end = max.height + off.y < px(24.);
            if o.follow && !at_end {
                o.scroll.scroll_to_bottom();
            }
        }
        let task = o.task.clone();
        let running = task.status == "running";
        let size = window.viewport_size();
        let field = |label: &str, value: AnyElement| {
            h_flex()
                .items_start()
                .gap_4()
                .text_size(theme::text::UI)
                .child(
                    div()
                        .w(px(84.))
                        .flex_none()
                        .text_color(theme::subtle())
                        .child(t(label)),
                )
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .text_color(theme::secondary())
                        .child(value),
                )
        };
        let mut status = h_flex()
            .gap_1p5()
            .text_color(if running {
                theme::subtle()
            } else {
                status_color(&task.status)
            })
            .child(status_icon("dialog", &task.status))
            .child(status_label(&task.status));
        if let Some(code) = task.exit_code {
            status = status.child(div().text_color(theme::subtle()).child(format!(
                "· {}",
                tv("chat.background.exitCode", &[("code", code.into())])
            )));
        }
        let mut fields = v_flex()
            .gap_1p5()
            .child(field(
                "chat.background.field.status",
                status.into_any_element(),
            ))
            .child(field(
                "chat.background.field.kind",
                kind_label(&task.kind).into_any_element(),
            ));
        if !task.command.is_empty() {
            fields = fields.child(field(
                if task.kind == "subagent" {
                    "chat.background.field.task"
                } else {
                    "chat.background.field.command"
                },
                div()
                    .font_family(theme::mono())
                    .text_size(theme::text::CAPTION)
                    .child(task.command.clone())
                    .into_any_element(),
            ));
        }
        fields = fields.child(field(
            "chat.background.field.started",
            crate::dates::date_time_of(&task.started_at).into_any_element(),
        ));
        if let Some(ended) = &task.ended_at {
            let took = match (
                crate::dates::local(&task.started_at),
                crate::dates::local(ended),
            ) {
                (Some(a), Some(b)) => format!(
                    " · {}",
                    tv(
                        "chat.background.took",
                        &[(
                            "time",
                            super::rows::format_duration((b - a).num_milliseconds() as f64 / 1000.)
                                .into()
                        )]
                    )
                ),
                _ => String::new(),
            };
            fields = fields.child(field(
                "chat.background.field.ended",
                format!("{}{took}", crate::dates::date_time_of(ended)).into_any_element(),
            ));
        }
        if !task.summary.is_empty() {
            fields = fields.child(field(
                "chat.background.field.summary",
                task.summary.clone().into_any_element(),
            ));
        }

        let has_text = o.out.as_ref().is_some_and(|b| !b.text.is_empty());
        let more = o.out.as_ref().is_some_and(|b| b.start > 0);
        let earlier_busy = o.earlier;
        let output: AnyElement = if has_text {
            let text = o.out.as_ref().map(|b| b.text.clone()).unwrap_or_default();
            div()
                .id("bg-output")
                .max_h(size.height * 0.45)
                .min_h(px(96.))
                .overflow_y_scroll()
                .track_scroll(&o.scroll)
                .on_scroll_wheel(cx.listener(|this, ev: &ScrollWheelEvent, window, cx| {
                    let Some(o) = this.background.as_mut() else {
                        return;
                    };
                    let dy = ev.delta.pixel_delta(window.line_height()).y;
                    if dy > px(0.) {
                        o.follow = false;
                    } else {
                        let (off, max) = (o.scroll.offset(), o.scroll.max_offset());
                        o.follow = max.height + off.y + dy < px(24.);
                    }
                    cx.notify();
                }))
                .rounded_lg()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .p_3()
                .font_family(theme::mono())
                .text_size(theme::text::CAPTION)
                .line_height(px(18.))
                .text_color(theme::secondary())
                .children(text.lines().map(|line| {
                    div()
                        .w_full()
                        .min_h(px(18.))
                        .child(SharedString::from(line.to_string()))
                }))
                .into_any_element()
        } else {
            let say = match &o.error {
                Some(e) => tv("chat.background.outputError", &[("error", e.clone().into())]),
                None if !o.read => t("chat.background.reading"),
                None if task.has_output => t("chat.background.empty"),
                None => t("chat.background.noOutput"),
            };
            div()
                .id("bg-no-output")
                .rounded_lg()
                .border_1()
                .border_color(theme::line_faint())
                .bg(theme::surface_faint())
                .px_3()
                .py_2p5()
                .text_size(theme::text::UI)
                .text_color(theme::subtle())
                .child(say)
                .into_any_element()
        };

        let card = v_flex()
            .id("bg-card")
            .w(px(760.))
            .max_w(relative(0.92))
            .max_h(relative(0.9))
            .gap_4()
            .p_6()
            .rounded(theme::radius::XXL)
            .bg(theme::card())
            .border_1()
            .border_color(theme::line_strong())
            .shadow(theme::shadow_modal())
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .child(
                h_flex()
                    .gap_2()
                    .items_start()
                    .child(
                        div().mt(px(3.)).child(
                            crate::icons::ic(kind_icon(&task.kind))
                                .size(px(16.))
                                .text_color(theme::subtle()),
                        ),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .text_size(theme::text::HEADING)
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::primary())
                            .child(task.title.clone()),
                    )
                    .child(
                        div()
                            .id("bg-close")
                            .p_1()
                            .rounded_md()
                            .cursor_pointer()
                            .text_color(theme::subtle())
                            .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                            .child(crate::icons::ic("x").size(px(16.)))
                            .on_click(cx.listener(|this, _, window, cx| {
                                this.close_background(window, cx)
                            })),
                    ),
            )
            .child(fields)
            .child(
                v_flex()
                    .gap_1p5()
                    .min_h_0()
                    .child(
                        h_flex()
                            .gap_2()
                            .text_size(theme::text::UI)
                            .text_color(theme::subtle())
                            .child(t("chat.background.output"))
                            .when(running && has_text, |el| {
                                el.child(
                                    div()
                                        .text_size(theme::text::CAPTION)
                                        .text_color(theme::running())
                                        .child(t("chat.background.live")),
                                )
                            })
                            .child(div().flex_1())
                            .when(more, |el| {
                                el.child(crate::app::controls::button(
                                    "bg-earlier",
                                    t("chat.background.earlier"),
                                    crate::app::controls::Btn::Ghost,
                                    earlier_busy,
                                    cx.listener(|this, _, _, cx| this.earlier_background(cx)),
                                ))
                            }),
                    )
                    .child(output),
            );
        let stage = div()
            .id("bg-backdrop")
            .track_focus(&o.focus)
            .w(size.width)
            .h(size.height)
            .bg(theme::scrim())
            .flex()
            .items_center()
            .justify_center()
            .occlude()
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                if ev.keystroke.key == "escape" {
                    this.close_background(window, cx);
                    cx.stop_propagation();
                }
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _, window, cx| this.close_background(window, cx)),
            )
            .child(crate::motion::pop_in(card, "bg-card-in"));
        Some(
            deferred(
                anchored()
                    .position(Point::default())
                    .child(crate::motion::scrim_in(stage, "bg-scrim-in")),
            )
            .with_priority(2)
            .into_any_element(),
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn read(output: &str, start: i64, size: i64) -> ChatBackgroundDetail {
        ChatBackgroundDetail {
            output: output.into(),
            output_start: start,
            output_size: size,
            ..Default::default()
        }
    }

    #[test]
    fn a_later_read_of_the_end_adds_only_what_is_new() {
        let buf = with_tail(None, &read("é1\n", 10, 14));
        assert_eq!((buf.start, buf.end, buf.text.as_str()), (10, 14, "é1\n"));
        let buf = with_tail(Some(buf), &read("1\né2\n", 12, 18));
        assert_eq!(
            (buf.start, buf.end, buf.size, buf.text.as_str()),
            (10, 18, 18, "é1\né2\n")
        );
    }

    #[test]
    fn a_read_that_does_not_join_starts_over() {
        let buf = with_tail(None, &read("abc", 0, 3));
        let far = with_tail(Some(buf.clone()), &read("xyz", 100, 103));
        assert_eq!((far.start, far.text.as_str()), (100, "xyz"));
        let shrunk = with_tail(Some(buf), &read("a", 0, 1));
        assert_eq!((shrunk.end, shrunk.text.as_str()), (1, "a"));
    }

    #[test]
    fn an_earlier_piece_goes_in_front_when_it_joins() {
        let buf = with_tail(None, &read("def", 3, 6));
        let joined = with_earlier(buf.clone(), &read("abc", 0, 6));
        assert_eq!((joined.start, joined.text.as_str()), (0, "abcdef"));
        assert_eq!(with_earlier(buf.clone(), &read("ab", 0, 6)), buf);
    }
}
