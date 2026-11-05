//! Find in this chat (FindBar.tsx): Ctrl+F opens a box over the
//! conversation that asks the daemon what holds the words, loaded or not
//! (GET …/chat/search?q=), and jumps to each match, reading the messages
//! around it when they aren't held (…/chat?around=).
use std::time::Duration;

use agentbox_api::{ChatSearch, ChatSearchHit};
use agentbox_i18n::{t, tv};
use gpui::*;
use gpui_component::h_flex;
use gpui_component::input::{Input, InputEvent, InputState};

use super::ChatView;
use crate::theme;

pub struct Find {
    input: Entity<InputState>,
    hits: Vec<ChatSearchHit>,
    /// The match shown, an index into hits (oldest first).
    ix: usize,
    searched: String,
    _sub: Subscription,
    _search: Option<Task<()>>,
}

impl ChatView {
    pub(super) fn open_find(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(f) = &self.find {
            f.input.update(cx, |s, cx| s.focus(window, cx));
            return;
        }
        let input =
            cx.new(|cx| InputState::new(window, cx).placeholder(t("chat.find.placeholder")));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, _, cx| match ev {
            InputEvent::Change => this.search_soon(cx),
            InputEvent::PressEnter { shift, .. } => this.step_find(!*shift, cx),
            _ => {}
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        self.find = Some(Find {
            input,
            hits: Vec::new(),
            ix: 0,
            searched: String::new(),
            _sub: sub,
            _search: None,
        });
        cx.notify();
    }

    /// find_text opens the find bar on a query (the search palette's "Find
    /// in this chat").
    pub fn find_text(&mut self, query: String, window: &mut Window, cx: &mut Context<Self>) {
        self.open_find(window, cx);
        if let Some(f) = &self.find {
            f.input.update(cx, |s, cx| s.set_value(query, window, cx));
        }
        self.search_soon(cx);
    }

    fn close_find(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.find = None;
        self.focus_composer(window, cx);
        cx.notify();
    }

    /// search_soon asks once typing pauses.
    fn search_soon(&mut self, cx: &mut Context<Self>) {
        let Some(f) = &mut self.find else { return };
        let q = f.input.read(cx).value().trim().to_string();
        let client = self.client.clone();
        let path = format!("{}/chat/search?q={}", self.prefix, crate::files::encode(&q));
        f._search = Some(cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(Duration::from_millis(200))
                .await;
            let res = if q.is_empty() {
                Ok(ChatSearch::default())
            } else {
                client.get::<ChatSearch>(&path).await
            };
            this.update(cx, |this, cx| {
                let Some(f) = &mut this.find else { return };
                match res {
                    Ok(s) => {
                        f.hits = s.hits;
                        f.ix = f.hits.len().saturating_sub(1);
                        f.searched = q;
                        // The newest match first, as the bar starts there.
                        if let Some(h) = f.hits.last() {
                            let id = h.id.clone();
                            this.reveal(id, cx);
                        }
                    }
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        }));
    }

    /// step_find moves to the match before (older) or after.
    fn step_find(&mut self, older: bool, cx: &mut Context<Self>) {
        let Some(f) = &mut self.find else { return };
        if f.hits.is_empty() {
            return;
        }
        let n = f.hits.len();
        f.ix = if older {
            (f.ix + n - 1) % n
        } else {
            (f.ix + 1) % n
        };
        let id = f.hits[f.ix].id.clone();
        self.reveal(id, cx);
        cx.notify();
    }

    /// found is whether an item is the match shown, for its row's mark.
    pub(super) fn found(&self, id: &str) -> bool {
        self.find
            .as_ref()
            .and_then(|f| f.hits.get(f.ix))
            .is_some_and(|h| h.id == id)
    }

    pub(super) fn render_find(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let f = self.find.as_ref()?;
        let count = if f.searched.is_empty() {
            String::new()
        } else if f.hits.is_empty() {
            t("chat.find.none")
        } else {
            tv(
                "chat.find.count",
                &[
                    ("current", (f.ix + 1).into()),
                    ("total", f.hits.len().into()),
                ],
            )
        };
        let snippet: Option<String> = f
            .hits
            .get(f.ix)
            .map(|h| h.snippet.iter().map(|p| p.text.as_str()).collect());
        let arrow = |id: &'static str, label: &'static str, tip: String, older: bool| {
            div()
                .id(id)
                .px_1p5()
                .rounded_md()
                .cursor_pointer()
                .text_color(theme::muted())
                .hover(|el| el.bg(theme::hover()).text_color(theme::fg()))
                .child(crate::icons::of(label))
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| this.step_find(older, cx)))
        };
        Some(
            h_flex()
                .flex_none()
                .gap_2()
                .px_4()
                .py_1p5()
                .border_b_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .on_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                    if ev.keystroke.key == "escape" {
                        this.close_find(window, cx);
                        cx.stop_propagation();
                    }
                }))
                .child(
                    div()
                        .w(px(260.))
                        .flex_none()
                        .child(Input::new(&f.input).small()),
                )
                .child(
                    div()
                        .flex_none()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(count),
                )
                .child(arrow("find-older", "↑", t("chat.find.older"), true))
                .child(arrow("find-newer", "↓", t("chat.find.newer"), false))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_xs()
                        .text_color(theme::muted())
                        .children(snippet),
                )
                .child(
                    div()
                        .id("find-close")
                        .px_1p5()
                        .rounded_md()
                        .cursor_pointer()
                        .text_color(theme::muted())
                        .hover(|el| el.bg(theme::hover()).text_color(theme::fg()))
                        .child(crate::icons::of("✕"))
                        .tooltip(|w, cx| {
                            gpui_component::tooltip::Tooltip::new(t("chat.find.close")).build(w, cx)
                        })
                        .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.close_find(window, cx)
                        })),
                )
                .into_any_element(),
        )
    }
}

use gpui_component::Sizable as _;
