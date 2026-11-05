//! The search palette (SearchModal.tsx), Ctrl+K from anywhere: what's typed
//! searched in every project at once (GET /v1/search) — projects, sessions,
//! agents, chats, memory, media, skills, connectors, notes and pull requests
//! — in groups, ↑↓ to move and Enter to open where it is (lib/search.ts
//! searchTarget). Its actions and settings, and its filters, aren't here yet.
use agentbox_api::{
    HOME_PROJECT, SEARCH_AGENTS, SEARCH_CHATS, SEARCH_PROJECTS, SEARCH_PULLS, SEARCH_SESSIONS,
    SearchHit, SearchResults,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::theme;

pub struct Palette {
    input: Entity<InputState>,
    results: Option<SearchResults>,
    asked: String,
    selected: usize,
    _sub: Subscription,
    _task: Option<Task<()>>,
}

impl AppView {
    pub(super) fn open_search(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let input = cx.new(|cx| InputState::new(window, cx).placeholder(t("search.placeholder")));
        let sub = cx.subscribe_in(
            &input,
            window,
            |this, _, ev: &InputEvent, window, cx| match ev {
                InputEvent::Change => this.run_search(cx),
                InputEvent::PressEnter { .. } => this.open_hit(None, window, cx),
                _ => {}
            },
        );
        input.update(cx, |s, cx| s.focus(window, cx));
        self.side.palette = Some(Palette {
            input,
            results: None,
            asked: String::new(),
            selected: 0,
            _sub: sub,
            _task: None,
        });
        cx.notify();
    }

    /// run_search asks once the typing pauses.
    fn run_search(&mut self, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        let q = p.input.read(cx).value().trim().to_string();
        if q.is_empty() {
            p.results = None;
            p._task = None;
            cx.notify();
            return;
        }
        let client = self.client.clone();
        p._task = Some(cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(150))
                .await;
            let path = format!("/v1/search?q={}&limit=8", crate::files::encode(&q));
            let res = client.get::<SearchResults>(&path).await;
            this.update(cx, |this, cx| {
                if let Some(p) = this.side.palette.as_mut() {
                    p.results = res.ok();
                    p.asked = q;
                    p.selected = 0;
                }
                cx.notify();
            })
            .ok();
        }));
    }

    fn hits(&self) -> Vec<(String, SearchHit)> {
        let Some(r) = self.side.palette.as_ref().and_then(|p| p.results.as_ref()) else {
            return Vec::new();
        };
        r.groups
            .iter()
            .flat_map(|g| g.hits.iter().map(|h| (g.kind.clone(), h.clone())))
            .collect()
    }

    /// open_hit opens a result where it lives.
    fn open_hit(&mut self, index: Option<usize>, window: &mut Window, cx: &mut Context<Self>) {
        let hits = self.hits();
        let ix = index.unwrap_or_else(|| self.side.palette.as_ref().map_or(0, |p| p.selected));
        let Some((kind, hit)) = hits.get(ix).cloned() else {
            return;
        };
        self.side.palette = None;
        let project = if hit.project.is_empty() && kind == SEARCH_PROJECTS {
            hit.id.clone()
        } else {
            hit.project.clone()
        };
        match kind.as_str() {
            k if k == SEARCH_AGENTS && !hit.archived => self.open_agent_ref(hit.id, window, cx),
            k if k == SEARCH_CHATS && !hit.agent.is_empty() && !hit.archived => {
                self.open_agent_ref(format!("{project}/{}", hit.agent), window, cx)
            }
            k if k == SEARCH_SESSIONS => {
                let id = if hit.session.is_empty() {
                    hit.id
                } else {
                    hit.session
                };
                self.select_project(project.clone(), window, cx);
                self.select_session(project, id, window, cx);
            }
            k if k == SEARCH_PULLS && !project.is_empty() => {
                self.select_project(project, window, cx);
                if let Ok(n) = hit.id.trim_start_matches('#').parse::<i64>() {
                    self.open_pull(n, window, cx);
                }
            }
            _ if !project.is_empty() => self.select_project(project, window, cx),
            _ if !hit.url.is_empty() => cx.open_url(&hit.url),
            _ => self.select_project(HOME_PROJECT.into(), window, cx),
        }
        cx.notify();
    }

    pub(super) fn on_search_key(
        &mut self,
        ev: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let n = self.hits().len();
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        match ev.keystroke.key.as_str() {
            "escape" => {
                self.side.palette = None;
                self.side.root_focus.focus(window, cx);
            }
            "down" if n > 0 => p.selected = (p.selected + 1) % n,
            "up" if n > 0 => p.selected = (p.selected + n - 1) % n,
            _ => return,
        }
        cx.stop_propagation();
        cx.notify();
    }

    pub(super) fn render_search(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.side.palette.as_ref()?;
        let typed = !p.input.read(cx).value().trim().is_empty();
        let mut rows: Vec<AnyElement> = Vec::new();
        let mut i = 0;
        if let Some(r) = &p.results {
            for g in r.groups.iter().filter(|g| !g.hits.is_empty()) {
                let key = format!("search.group.{}", g.kind);
                let label = if agentbox_i18n::has(&key) {
                    t(&key)
                } else {
                    t("search.group.other")
                };
                rows.push(
                    div()
                        .px_4()
                        .pt_3()
                        .pb_1()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(label.to_uppercase())
                        .into_any_element(),
                );
                for h in &g.hits {
                    let ix = i;
                    let on = ix == p.selected;
                    let project = if h.project == HOME_PROJECT {
                        t("shell.nav.home")
                    } else {
                        self.projects
                            .iter()
                            .find(|p| p.name == h.project)
                            .map_or_else(|| h.project.clone(), super::sidebar_actions::label_of)
                    };
                    let place = [project, h.agent.clone()]
                        .into_iter()
                        .filter(|s| !s.is_empty())
                        .collect::<Vec<_>>()
                        .join(" · ");
                    rows.push(
                        h_flex()
                            .id(SharedString::from(format!("hit-{ix}")))
                            .mx_2()
                            .px_3()
                            .py_1p5()
                            .gap_3()
                            .rounded_md()
                            .cursor_pointer()
                            .when(on, |el| el.bg(theme::hover()))
                            .hover(|el| el.bg(theme::raised()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.open_hit(Some(ix), window, cx)
                            }))
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .child(
                                        div()
                                            .text_sm()
                                            .truncate()
                                            .text_color(theme::fg())
                                            .child(h.title.clone()),
                                    )
                                    .when(!h.detail.is_empty(), |el| {
                                        el.child(
                                            div()
                                                .text_xs()
                                                .truncate()
                                                .text_color(theme::faint())
                                                .child(h.detail.replace('\n', " ")),
                                        )
                                    }),
                            )
                            .when(!place.is_empty(), |el| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .max_w(px(200.))
                                        .truncate()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(place),
                                )
                            })
                            .into_any_element(),
                    );
                    i += 1;
                }
            }
        }
        let nothing = typed && p.results.is_some() && i == 0;
        Some(
            div()
                .id("search-palette")
                .absolute()
                .inset_0()
                .bg(gpui::black().opacity(0.5))
                .flex()
                .justify_center()
                .pt(px(96.))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _: &MouseDownEvent, window, cx| {
                        this.side.palette = None;
                        this.side.root_focus.focus(window, cx);
                        cx.notify();
                    }),
                )
                .capture_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                    this.on_search_key(ev, window, cx)
                }))
                .child(
                    v_flex()
                        .id("search-card")
                        .w(px(640.))
                        .max_w(relative(0.92))
                        .max_h(px(560.))
                        .rounded_xl()
                        .bg(theme::panel())
                        .border_1()
                        .border_color(theme::border())
                        .shadow_lg()
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .child(div().p_3().child(Input::new(&p.input).cleanable(true)))
                        .child(
                            div()
                                .id("search-results")
                                .flex_1()
                                .min_h_0()
                                .overflow_y_scroll()
                                .pb_2()
                                .when(!typed, |el| {
                                    el.child(
                                        div()
                                            .px_4()
                                            .pb_3()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(t("search.hint")),
                                    )
                                })
                                .when(nothing, |el| {
                                    el.child(
                                        div()
                                            .px_4()
                                            .pb_3()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(tv(
                                                "search.nothing",
                                                &[("query", (&p.asked).into())],
                                            )),
                                    )
                                })
                                .children(rows),
                        )
                        .child(
                            h_flex()
                                .px_4()
                                .py_2()
                                .gap_4()
                                .border_t_1()
                                .border_color(theme::border())
                                .text_xs()
                                .text_color(theme::faint())
                                .child(format!("↑↓ {}", t("search.keys.move")))
                                .child(format!("Enter {}", t("search.keys.open"))),
                        ),
                )
                .into_any_element(),
        )
    }
}
