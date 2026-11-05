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
use agentbox_i18n::{t, tv};

/// session_line is what the chat's header says of its session (chat.status.*).
fn session_line(s: &ChatSession) -> String {
    let key = format!("chat.status.{}", s.state);
    if s.state.is_empty() || !agentbox_i18n::has(&key) {
        return String::new();
    }
    let tool = match s.tool.as_str() {
        "claude" => "Claude Code",
        "codex" => "Codex",
        "opencode" => "OpenCode",
        other => other,
    };
    tv(&key, &[("tool", tool.into())])
}

/// How many messages the first page holds, as the Electron app asks.
const PAGE: usize = 200;

pub struct ChatView {
    client: Client,
    /// The API prefix of this chat: /v1/projects/<p> (the lead) or /v1/agents/<p>/<a>.
    prefix: String,
    title: SharedString,
    /// Whether it draws its own title bar: not under the project's header.
    pub header: bool,
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
    /// Tool calls opened to show their command and output.
    expanded: std::collections::HashSet<String>,
    /// Permission requests whose answer is on its way.
    answering: std::collections::HashSet<String>,
    /// The conversation has items before those held, and a read of them is out.
    older: bool,
    loading_older: bool,
    _subs: Vec<Subscription>,
}

impl ChatView {
    /// status_line is what its session is doing, in words.
    pub fn status_line(&self) -> String {
        session_line(&self.session)
    }

    /// working is whether a turn runs.
    pub fn working(&self) -> bool {
        matches!(self.session.state.as_str(), "running" | "starting")
    }

    pub fn new(
        client: Client,
        prefix: String,
        agent: String,
        title: SharedString,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let input =
            cx.new(|cx| InputState::new(window, cx).placeholder(t("chat.composer.placeholder")));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                this.send(window, cx);
            }
        });
        let list = ListState::new(0, ListAlignment::Bottom, px(800.));
        list.set_follow_mode(FollowMode::Tail);
        // Scrolled to the top of what's loaded: read the page before it.
        let view = cx.entity().downgrade();
        list.set_scroll_handler(move |ev, _, cx| {
            if ev.visible_range.start == 0 && ev.is_scrolled {
                view.update(cx, |this, cx| this.load_older(cx)).ok();
            }
        });
        let mut this = Self {
            client,
            prefix,
            title,
            header: true,
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
            expanded: Default::default(),
            answering: Default::default(),
            older: false,
            loading_older: false,
            _subs: vec![sub],
        };
        this.load(cx);
        this
    }

    /// load reads the latest page, again after the event stream reconnects.
    /// load_older reads the page before the oldest item held, and puts it above
    /// without moving what's on screen.
    fn load_older(&mut self, cx: &mut Context<Self>) {
        if !self.older || self.loading_older {
            return;
        }
        let Some(first) = self.items.first().map(|i| i.id.clone()) else {
            return;
        };
        self.loading_older = true;
        let client = self.client.clone();
        let path = format!("{}/chat?limit={PAGE}&before={first}", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client.get::<ChatThread>(&path).await;
            this.update(cx, |this, cx| {
                this.loading_older = false;
                let Ok(t) = res else { return };
                // The page may overlap what an event already brought.
                let have: std::collections::HashSet<&str> =
                    this.items.iter().map(|i| i.id.as_str()).collect();
                let page: Vec<ChatItem> = t
                    .items
                    .into_iter()
                    .filter(|i| !have.contains(i.id.as_str()))
                    .collect();
                let n = page.len();
                let top = this.list.logical_scroll_top();
                this.items.splice(0..0, page);
                this.older = t.older;
                this.list.splice(0..0, n);
                this.list.scroll_to(ListOffset {
                    item_ix: top.item_ix + n,
                    offset_in_item: top.offset_in_item,
                });
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// focus_composer puts the cursor in the message box.
    /// set_draft puts text in the composer, not sent (lib/drafts.ts composeIn).
    pub fn set_draft(&mut self, text: &str, window: &mut Window, cx: &mut Context<Self>) {
        let text = text.to_string();
        self.input.update(cx, |s, cx| s.set_value(text, window, cx));
    }

    pub fn focus_composer(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.input.update(cx, |s, cx| s.focus(window, cx));
    }

    /// stop ends the turn in progress (POST …/chat/cancel).
    fn stop(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/cancel", self.prefix);
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(agentbox_api::client::Method::POST, &path, None)
                .await;
            this.update(cx, |this, cx| {
                this.error = res.err().map(|e| e.to_string().into());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

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
                        this.older = t.older;
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

    fn toggle(&mut self, id: &str, ix: usize, cx: &mut Context<Self>) {
        if !self.expanded.remove(id) {
            self.expanded.insert(id.to_string());
        }
        self.list.remeasure_items(ix..ix + 1);
        cx.notify();
    }

    /// answer picks one of a permission request's options.
    fn answer(&mut self, item: String, option: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/permissions/{item}", self.prefix);
        self.answering.insert(item.clone());
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, serde_json::Value>(
                    &path,
                    &agentbox_api::ChatAnswerRequest { option_id: option },
                )
                .await;
            this.update(cx, |this, cx| {
                this.answering.remove(&item);
                this.error = res.err().map(|e| e.to_string().into());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn render_tool(&self, item: &ChatItem, ix: usize, view: Entity<Self>) -> AnyElement {
        let tool = item.tool.clone().unwrap_or_default();
        let open = self.expanded.contains(&item.id);
        let id = item.id.clone();
        let dot = match tool.status.as_str() {
            "completed" => theme::running(),
            "failed" => theme::error(),
            _ => theme::starting(),
        };
        let title = if tool.title.is_empty() {
            tool.kind.clone()
        } else {
            tool.title.clone()
        };
        let mut body = v_flex().gap_1();
        if open {
            let mono = || {
                div()
                    .p_2()
                    .rounded_md()
                    .bg(theme::raised())
                    .text_xs()
                    .font_family(crate::theme::mono())
                    .text_color(theme::muted())
            };
            if !tool.command.is_empty() {
                body = body.child(mono().child(format!("$ {}", tool.command)));
            }
            for d in &tool.diffs {
                let (old, new) = (d.old_text.lines().count(), d.new_text.lines().count());
                body = body.child(
                    div()
                        .text_xs()
                        .text_color(theme::muted())
                        .child(format!("{}  −{old} +{new}", d.path)),
                );
            }
            if !tool.output.is_empty() {
                let out: String = tool.output.chars().take(4000).collect();
                body = body.child(mono().child(out));
            }
        }
        div()
            .w_full()
            .px_6()
            .py_0p5()
            .child(
                h_flex()
                    .id(SharedString::from(format!("tool-{}", item.id)))
                    .gap_2()
                    .cursor_pointer()
                    .text_xs()
                    .text_color(theme::muted())
                    .hover(|el| el.text_color(theme::fg()))
                    .on_click(move |_, _, cx| view.update(cx, |this, cx| this.toggle(&id, ix, cx)))
                    .child(div().flex_none().size_1p5().rounded_full().bg(dot))
                    .child(div().truncate().child(title))
                    .child(div().flex_none().text_color(theme::faint()).child(if open {
                        "▾"
                    } else {
                        "▸"
                    })),
            )
            .when(open, |el| el.child(div().pl_4().pt_1().child(body)))
            .into_any_element()
    }

    fn render_permission(&self, item: &ChatItem, view: Entity<Self>) -> AnyElement {
        let p = item.permission.clone().unwrap_or_default();
        let sending = self.answering.contains(&item.id);
        let mut buttons = h_flex().gap_2().flex_wrap();
        if p.outcome.is_empty() {
            for o in &p.options {
                let key = match o.kind.as_str() {
                    "allow_once" => Some("chat.permission.allow"),
                    "allow_always" => Some("chat.permission.alwaysAllow"),
                    "reject_once" => Some("chat.permission.deny"),
                    "reject_always" => Some("chat.permission.alwaysDeny"),
                    _ => None,
                };
                let label = key.map(t).unwrap_or_else(|| o.name.clone());
                let (item_id, option) = (item.id.clone(), o.id.clone());
                let view = view.clone();
                let primary = o.kind == "allow_once";
                buttons = buttons.child(
                    div()
                        .id(SharedString::from(format!("perm-{}-{}", item.id, o.id)))
                        .px_3()
                        .py_1()
                        .rounded_md()
                        .cursor_pointer()
                        .text_sm()
                        .bg(if primary {
                            theme::accent()
                        } else {
                            theme::hover()
                        })
                        .text_color(theme::fg())
                        .when(!sending, |el| {
                            el.on_click(move |_, _, cx| {
                                view.update(cx, |this, cx| {
                                    this.answer(item_id.clone(), option.clone(), cx)
                                })
                            })
                        })
                        .child(label),
                );
            }
        }
        div()
            .w_full()
            .px_6()
            .py_2()
            .child(
                v_flex()
                    .gap_2()
                    .p_3()
                    .rounded_lg()
                    .border_1()
                    .border_color(if p.outcome.is_empty() {
                        theme::starting()
                    } else {
                        theme::border()
                    })
                    .bg(theme::panel())
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::fg())
                            .child(p.title.clone()),
                    )
                    .when(!p.detail.is_empty(), |el| {
                        el.child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(p.detail.clone()),
                        )
                    })
                    .when(sending, |el| {
                        el.child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t("chat.permission.sending")),
                        )
                    })
                    .when(!p.outcome.is_empty(), |el| {
                        let chosen = p
                            .options
                            .iter()
                            .find(|o| o.id == p.outcome)
                            .map(|o| o.name.clone())
                            .unwrap_or(p.outcome.clone());
                        el.child(div().text_xs().text_color(theme::faint()).child(chosen))
                    })
                    .child(buttons),
            )
            .into_any_element()
    }

    fn render_item(&self, ix: usize, view: Entity<Self>) -> AnyElement {
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
            "tool" => self.render_tool(item, ix, view),
            "permission" => self.render_permission(item, view),
            "plan" => row
                .py_1()
                .child(
                    v_flex()
                        .gap_0p5()
                        .text_sm()
                        .children(item.plan.iter().map(|e| {
                            let (mark, color) = match e.status.as_str() {
                                "completed" => ("✓", theme::running()),
                                "in_progress" => ("●", theme::starting()),
                                _ => ("○", theme::faint()),
                            };
                            h_flex()
                                .gap_2()
                                .child(div().flex_none().text_color(color).child(mark))
                                .child(
                                    div()
                                        .text_color(if e.status == "completed" {
                                            theme::muted()
                                        } else {
                                            theme::fg()
                                        })
                                        .child(e.content.clone()),
                                )
                        })),
                )
                .into_any_element(),
            "thought" => row
                .text_sm()
                .italic()
                .text_color(theme::faint())
                .child(text)
                .into_any_element(),
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
        crate::webview::set_scroller(self.list.clone());
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
            .when(self.header, |el| {
                el.child(
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
                                .child(session_line(&self.session)),
                        )
                        .child(div().flex_1()),
                )
            })
            .child(
                div()
                    .flex_1()
                    .min_h_0()
                    .child(crate::webview::begin())
                    .child(
                        list(self.list.clone(), move |ix, _window, cx| {
                            view.read(cx).render_item(ix, view.clone())
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
                    .child(
                        h_flex()
                            .gap_2()
                            .child(div().flex_1().child(Input::new(&self.input)))
                            // While a turn runs, it can be stopped.
                            .when(working, |el| {
                                el.child(
                                    div()
                                        .id("stop-turn")
                                        .flex_none()
                                        .px_3()
                                        .py_1p5()
                                        .rounded_md()
                                        .cursor_pointer()
                                        .bg(theme::hover())
                                        .text_sm()
                                        .text_color(theme::fg())
                                        .child(t("chat.composer.stopTurn"))
                                        .on_click(
                                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                                this.stop(cx)
                                            }),
                                        ),
                                )
                            }),
                    ),
            )
    }
}
