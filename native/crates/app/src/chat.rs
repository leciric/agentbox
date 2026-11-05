//! One conversation, the lead's or an agent's: its items, kept up to date from
//! the event stream's chat events, and a composer that sends a message.
use std::time::Instant;

use agentbox_api::{ChatEvent, ChatItem, ChatMessageRequest, ChatSession, ChatThread, Client};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use crate::{bench, theme};

/// How many messages the first page holds, as the Electron app asks.
const PAGE: usize = 200;

pub struct ChatView {
    client: Client,
    /// The API prefix of this chat: /v1/projects/<p> (the lead) or /v1/agents/<p>/<a>.
    prefix: String,
    title: SharedString,
    /// The chat's ref, as chat events name it (project/lead, project/agent-01).
    pub agent: String,
    items: Vec<ChatItem>,
    session: ChatSession,
    seq: i64,
    loaded: bool,
    error: Option<SharedString>,
    list: ListState,
    input: Entity<InputState>,
    /// When the last unpainted chunk arrived, for bench's arrival-to-paint.
    arrived: Option<Instant>,
    last_frame: Option<Instant>,
    _subs: Vec<Subscription>,
}

impl ChatView {
    pub fn new(
        client: Client,
        prefix: String,
        agent: String,
        title: SharedString,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let input = cx.new(|cx| InputState::new(window, cx).placeholder("Message…"));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                this.send(window, cx);
            }
        });
        let list = ListState::new(0, ListAlignment::Bottom, px(800.));
        list.set_follow_mode(FollowMode::Tail);
        let mut this = Self {
            client,
            prefix,
            title,
            agent,
            items: Vec::new(),
            session: ChatSession::default(),
            seq: 0,
            loaded: false,
            error: None,
            list,
            input,
            arrived: None,
            last_frame: None,
            _subs: vec![sub],
        };
        this.load(cx);
        this
    }

    /// load reads the latest page, again after the event stream reconnects.
    pub fn load(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat?limit={PAGE}", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client.get::<ChatThread>(&path).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(t) => {
                        this.agent = t.agent;
                        this.seq = t.seq;
                        this.session = t.session;
                        this.items = t.items;
                        this.list.reset(this.items.len());
                        this.list.set_follow_mode(FollowMode::Tail);
                        this.loaded = true;
                        this.error = None;
                        bench::mark("chat-loaded");
                    }
                    Err(e) => this.error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// apply folds one chat event in. Events the page already holds (seq at
    /// or below its own) are skipped.
    pub fn apply(&mut self, ev: ChatEvent, cx: &mut Context<Self>) {
        if !self.loaded || ev.seq <= self.seq {
            return;
        }
        self.seq = ev.seq;
        if ev.cleared {
            self.items.clear();
            self.list.reset(0);
        }
        if let Some(s) = ev.session {
            self.session = s;
        }
        if let Some(item) = ev.item {
            match self.items.iter().position(|i| i.id == item.id) {
                Some(ix) => {
                    self.items[ix] = item;
                    self.list.remeasure_items(ix..ix + 1);
                }
                None => {
                    let ix = self.items.len();
                    self.items.push(item);
                    self.list.splice(ix..ix, 1);
                }
            }
        }
        if let Some(app) = ev.append
            && let Some(ix) = self.items.iter().rposition(|i| i.id == app.id)
        {
            self.items[ix].text.push_str(&app.text);
            self.list.remeasure_items(ix..ix + 1);
            if bench::on() && self.arrived.is_none() {
                self.arrived = Some(Instant::now());
            }
        }
        cx.notify();
    }

    fn send(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.input.read(cx).value().trim().to_string();
        if text.is_empty() {
            return;
        }
        self.input.update(cx, |s, cx| s.set_value("", window, cx));
        let client = self.client.clone();
        let path = format!("{}/chat/messages", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>(
                    &path,
                    &ChatMessageRequest {
                        text,
                        ..Default::default()
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                this.error = res.err().map(|e| e.to_string().into());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// scroll_bench scrolls the list by `step` every frame for `frames`
    /// frames, sampling the time between frames, then reports.
    pub fn scroll_bench(
        &mut self,
        step: f32,
        frames: usize,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        fn tick(
            view: Entity<ChatView>,
            step: f32,
            left: usize,
            last: Instant,
            window: &mut Window,
            cx: &mut App,
        ) {
            let now = Instant::now();
            bench::sample("scroll-frame-interval", now - last);
            if left == 0 {
                bench::report();
                return;
            }
            view.update(cx, |this, cx| {
                this.list.scroll_by(px(step));
                cx.notify();
            });
            let v = view.clone();
            window.on_next_frame(move |window, cx| tick(v, step, left - 1, now, window, cx));
        }
        let view = cx.entity();
        self.list.set_follow_mode(FollowMode::Normal);
        let start = Instant::now();
        window.on_next_frame(move |window, cx| tick(view, step, frames, start, window, cx));
        cx.notify();
    }

    fn render_item(&self, ix: usize) -> AnyElement {
        let Some(item) = self.items.get(ix) else {
            return div().into_any_element();
        };
        if item.hidden {
            return div().into_any_element();
        }
        let text = item.text.clone();
        let row = div().w_full().px_6().py_2();
        match item.kind.as_str() {
            "user" | "aside" => row
                .child(
                    h_flex().w_full().justify_end().child(
                        div()
                            .max_w(relative(0.75))
                            .rounded_lg()
                            .bg(theme::raised())
                            .border_1()
                            .border_color(theme::border())
                            .px_3()
                            .py_2()
                            .text_color(theme::fg())
                            .child(text),
                    ),
                )
                .into_any_element(),
            "assistant" => row
                .child(
                    crate::mermaid::with_mermaid(TextView::markdown(
                        SharedString::from(format!("md-{}", item.id)),
                        text,
                    ))
                    .selectable(true)
                    .text_color(theme::fg()),
                )
                .into_any_element(),
            "tool" => {
                let tool = item.tool.clone().unwrap_or_default();
                row.py_0p5()
                    .child(
                        h_flex()
                            .gap_2()
                            .text_xs()
                            .text_color(theme::muted())
                            .child(div().size_1p5().rounded_full().bg(
                                if tool.status == "completed" {
                                    theme::running()
                                } else {
                                    theme::starting()
                                },
                            ))
                            .child(div().truncate().child(if tool.title.is_empty() {
                                tool.kind
                            } else {
                                tool.title
                            })),
                    )
                    .into_any_element()
            }
            "error" => row
                .text_sm()
                .text_color(theme::error())
                .child(text)
                .into_any_element(),
            _ if text.is_empty() => div().into_any_element(),
            _ => row
                .text_sm()
                .text_color(theme::muted())
                .child(text)
                .into_any_element(),
        }
    }
}

impl Render for ChatView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let viewport = self.list.viewport_bounds();
        crate::webview::set_clip((viewport.size.height > px(0.)).then_some(viewport));
        if bench::on() {
            if let Some(at) = self.arrived.take() {
                window.on_next_frame(move |_, _| {
                    bench::sample("chunk-arrival-to-frame", at.elapsed())
                });
            }
            if self.session.state == "running" {
                let now = Instant::now();
                if let Some(last) = self.last_frame {
                    bench::sample("stream-render-interval", now - last);
                }
                self.last_frame = Some(now);
            } else if self.last_frame.take().is_some() {
                bench::report();
            }
        }
        let view = cx.entity();
        let working = matches!(self.session.state.as_str(), "running" | "starting");
        v_flex()
            .size_full()
            .bg(theme::bg())
            .child(
                h_flex()
                    .h_12()
                    .flex_none()
                    .px_6()
                    .gap_3()
                    .border_b_1()
                    .border_color(theme::border())
                    .child(
                        div()
                            .text_color(theme::fg())
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(self.title.clone()),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(if working {
                                theme::starting()
                            } else {
                                theme::faint()
                            })
                            .child(match self.session.state.as_str() {
                                "running" => "working…".to_string(),
                                "" => String::new(),
                                s => s.to_string(),
                            }),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(format!("{} messages", self.items.len())),
                    ),
            )
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .child(crate::webview::begin())
                    .child(
                        list(self.list.clone(), move |ix, _window, cx| {
                            view.read(cx).render_item(ix)
                        })
                        .size_full(),
                    )
                    .child(crate::webview::end()),
            )
            .when_some(self.error.clone(), |el, e| {
                el.child(
                    div()
                        .px_6()
                        .py_1()
                        .text_xs()
                        .text_color(theme::error())
                        .child(e),
                )
            })
            .child(
                div()
                    .flex_none()
                    .p_4()
                    .border_t_1()
                    .border_color(theme::border())
                    .child(Input::new(&self.input)),
            )
    }
}
