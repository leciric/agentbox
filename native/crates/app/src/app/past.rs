//! A past agent, whose machine was removed (PastAgents.tsx's PastAgentView):
//! its record over its chat, read-only, with Continue (a new agent on its
//! branch, POST /v1/archived-agents/{p}/{a}/continue) and Delete; beside it
//! the panel's views that need no machine (Files of its branch, read-only;
//! Evidence; Timeline), as RightPanel.tsx shows a "past:" ref.
use agentbox_api::{ArchivedAgent, ContinueArchivedRequest, FleetAgent, Job};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Textarea, TextareaState};
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::panel::PView;
use super::settings::defaults::time_ago;
use crate::chat::ChatView;
use crate::theme;
use crate::toasts;

/// The views a past agent has.
pub const PAST_VIEWS: [PView; 3] = [PView::Files, PView::Evidence, PView::Timeline];

pub struct Past {
    /// The past agent open, while its chat is.
    pub agent: Option<ArchivedAgent>,
    pub view: PView,
    /// Continue's dialog, and what to tell the new agent.
    pub continuing: bool,
    pub message: Entity<TextareaState>,
    pub deleting: bool,
    busy: bool,
}

impl Past {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        Self {
            agent: None,
            view: PView::Evidence,
            continuing: false,
            message: cx.new(|cx| {
                TextareaState::new(window, cx)
                    .auto_grow(3, 8)
                    .placeholder(t("pastAgents.continuePlaceholder"))
            }),
            deleting: false,
            busy: false,
        }
    }
}

/// local_or is an RFC 3339 time in the user's own date and time.
pub fn local_or(iso: &str) -> String {
    crate::dates::date_time_of(iso)
}

/// as_fleet is a past agent as the views that take an agent want it.
pub fn as_fleet(a: &ArchivedAgent) -> FleetAgent {
    FleetAgent {
        r#ref: a.r#ref.clone(),
        project: a.project.clone(),
        name: a.name.clone(),
        title: a.title.clone(),
        ai: a.ai.clone(),
        branch: a.branch.clone(),
        state: "archived".into(),
        ..Default::default()
    }
}

impl AppView {
    /// past_ref is the past agent whose chat is open.
    pub(super) fn past_ref(&self) -> Option<String> {
        self.past
            .agent
            .as_ref()
            .filter(|a| a.name == self.open && Some(&a.project) == self.project.as_ref())
            .map(|a| a.r#ref.clone())
    }

    /// open_past opens a past agent's chat, read-only, and its record.
    pub(super) fn open_past(&mut self, r#ref: String, window: &mut Window, cx: &mut Context<Self>) {
        let Some((p, a)) = r#ref.split_once('/') else {
            return;
        };
        let (p, a) = (p.to_string(), a.to_string());
        if self.project.as_deref() != Some(p.as_str()) {
            self.select_project(p.clone(), window, cx);
        }
        let known = self.panel.past_agent(&r#ref);
        self.open = a.clone();
        self.terminal = None;
        self.desktop = None;
        self.panel.forget();
        self.views.forget(window, cx);
        self.agent.reset();
        self.past.agent = Some(known.clone().unwrap_or_else(|| ArchivedAgent {
            r#ref: r#ref.clone(),
            project: p.clone(),
            name: a.clone(),
            ..Default::default()
        }));
        self.past.view = PView::Evidence;
        let client = self.client.clone();
        let title = known.as_ref().map(|k| k.title.clone()).unwrap_or_default();
        let chat = cx.new(|cx| {
            let mut c = ChatView::new(
                client.clone(),
                format!("/v1/agents/{p}/{a}"),
                r#ref.clone(),
                format!("{a} · {title}").into(),
                window,
                cx,
            );
            c.header = false;
            c.agent_name = a.clone();
            c.past = true;
            c
        });
        self._chat_sub = Some(cx.subscribe_in(&chat, window, Self::on_chat_action));
        self.chat = Some(chat);
        // The record itself: its tokens, its pull request.
        let path = format!("/v1/archived-agents/{p}/{a}");
        cx.spawn(async move |this, cx| {
            let res = client.get::<ArchivedAgent>(&path).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(rec) => {
                        if this
                            .past
                            .agent
                            .as_ref()
                            .is_some_and(|x| x.r#ref == rec.r#ref)
                        {
                            this.past.agent = Some(rec);
                        }
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        self.panel_sync(window, cx);
        cx.notify();
    }

    /// past_sync reads what the past agent's view needs.
    pub(super) fn past_sync(&mut self, r#ref: &str, window: &mut Window, cx: &mut Context<Self>) {
        let (p, a) = super::agent::split(r#ref);
        match self.past.view {
            PView::Files => self.ensure_files(
                format!("/v1/archived-agents/{p}/{a}/files"),
                format!("past:{ref}", ref = r#ref),
                window,
                cx,
            ),
            PView::Timeline => self.load_timeline(r#ref.to_string(), cx),
            _ => self.load_media(r#ref.to_string(), false, cx),
        }
    }

    fn continue_past(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.past.agent.clone() else {
            return;
        };
        let message = self.past.message.read(cx).value().trim().to_string();
        let client = self.client.clone();
        self.past.busy = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Job>(
                    &format!("/v1/archived-agents/{}/{}/continue", a.project, a.name),
                    &ContinueArchivedRequest {
                        message,
                        ..Default::default()
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                this.past.busy = false;
                match res {
                    Ok(job) => {
                        this.past.continuing = false;
                        let agent = job
                            .result
                            .get("name")
                            .and_then(|v| v.as_str())
                            .map(String::from)
                            .unwrap_or_else(|| a.title.clone());
                        toasts::info(
                            cx,
                            tv("pastAgents.continued", &[("agent", (&agent).into())]),
                        );
                        this.load_fleet_now(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn delete_past(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(a) = self.past.agent.clone() else {
            return;
        };
        let client = self.client.clone();
        self.past.busy = true;
        let name = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let handle = window.window_handle();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    agentbox_api::client::Method::DELETE,
                    &format!("/v1/archived-agents/{}/{}", a.project, a.name),
                    None,
                )
                .await;
            handle
                .update(cx, |_, window, cx| {
                    this.update(cx, |this, cx| {
                        this.past.busy = false;
                        this.past.deleting = false;
                        match res {
                            Ok(_) => {
                                toasts::Toast::new(
                                    toasts::Kind::Success,
                                    tv("pastAgents.deleted", &[("count", 1i64.into())]),
                                )
                                .description(name.clone())
                                .show(cx);
                                this.past.agent = None;
                                this.open_chat("lead".into(), window, cx);
                            }
                            Err(e) => toasts::error(cx, e.to_string()),
                        }
                        cx.notify();
                    })
                    .ok();
                })
                .ok();
        })
        .detach();
        cx.notify();
    }

    /// render_past_header is the past agent's record over its chat.
    pub(super) fn render_past_header(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        self.past_ref()?;
        let a = self.past.agent.clone()?;
        let title = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let pr = a.pr.clone().filter(|p| p.number > 0);
        Some(
            h_flex()
                .flex_none()
                .px_4()
                .py_2p5()
                .gap_3()
                .border_b_1()
                .border_color(theme::line())
                .child(
                    v_flex()
                        .min_w_0()
                        .flex_1()
                        .gap_0p5()
                        .child(
                            h_flex()
                                .gap_2()
                                .items_baseline()
                                .child(
                                    div()
                                        .truncate()
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .child(title),
                                )
                                .when(!a.title.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .flex_none()
                                            .text_xs()
                                            .font_family(theme::mono())
                                            .text_color(theme::faint())
                                            .child(a.name.clone()),
                                    )
                                }),
                        )
                        .child(
                            h_flex()
                                .flex_wrap()
                                .gap_3()
                                .text_xs()
                                .text_color(theme::muted())
                                .when(!a.branch.is_empty(), |el| {
                                    el.child(div().font_family(theme::mono()).child(
                                        crate::icons::lead("git-branch", a.branch.to_string()),
                                    ))
                                })
                                .child(tv(
                                    "pastAgents.archived",
                                    &[("when", time_ago(&a.archived_at).into())],
                                ))
                                .when(a.tokens > 0, |el| {
                                    el.child(tv(
                                        "pastAgents.tokens",
                                        &[("tokens", crate::chat::tokens(a.tokens).into())],
                                    ))
                                })
                                .when_some(pr, |el, pr| {
                                    let url = pr.url.clone();
                                    let tip: SharedString =
                                        tv("pastAgents.openPr", &[("number", pr.number.into())])
                                            .into();
                                    el.child(
                                        div()
                                            .id("past-pr")
                                            .cursor_pointer()
                                            .hover(|s| s.text_color(theme::primary()))
                                            .active(|s| s.opacity(0.7))
                                            .tooltip(move |w, cx| {
                                                Tooltip::new(tip.clone()).build(w, cx)
                                            })
                                            .on_click(move |_, _, cx| cx.open_url(&url))
                                            .child(format!("#{} {}", pr.number, pr.title)),
                                    )
                                }),
                        ),
                )
                .child(controls::button(
                    "past-header-continue",
                    t("pastAgents.continue"),
                    Btn::Secondary,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.past.continuing = true;
                        cx.notify();
                    }),
                ))
                .into_any_element(),
        )
    }

    /// render_past_dialogs are Continue's and Delete's, over the window.
    pub(super) fn render_past_dialogs(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let a = self.past.agent.clone()?;
        let name = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let busy = self.past.busy;
        let view = cx.entity().downgrade();
        let cancel: super::agent::OnCancel = std::rc::Rc::new(move |_, cx| {
            view.update(cx, |this, cx| {
                this.past.continuing = false;
                this.past.deleting = false;
                cx.notify();
            })
            .ok();
        });
        if self.past.continuing {
            return Some(super::agent::modal(
                "past-continue",
                tv("pastAgents.continueTitle", &[("name", (&name).into())]),
                Some(t("pastAgents.continueDescription")),
                vec![
                    v_flex()
                        .gap_1p5()
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t("pastAgents.continueMessage")),
                        )
                        .child(Textarea::new(&self.past.message))
                        .into_any_element(),
                ],
                cancel,
                t("pastAgents.continue"),
                false,
                busy,
                cx.listener(|this, _: &ClickEvent, _, cx| this.continue_past(cx)),
            ));
        }
        if self.past.deleting {
            return Some(super::agent::modal(
                "past-delete",
                tv("pastAgents.deleteTitle", &[("name", (&name).into())]),
                Some(t("pastAgents.deleteDescription")),
                Vec::new(),
                cancel,
                t("common.delete"),
                true,
                busy,
                cx.listener(|this, _: &ClickEvent, w, cx| this.delete_past(w, cx)),
            ));
        }
        None
    }

    /// past_panel_header is the panel's header beside a past agent: back,
    /// its unplugged mark and title, and its views.
    pub(super) fn past_panel_header(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let view = self.past.view;
        let a = self.past.agent.clone().unwrap_or_default();
        let title = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let freed: SharedString = t("panel.machine.freed").into();
        let _ = r;
        h_flex()
            .flex_none()
            .h(px(44.))
            .pl_2()
            .pr_1p5()
            .gap_1()
            .border_b_1()
            .border_color(theme::line())
            .child(
                super::panel::icon_button("past-back", "←", t("panel.back")).on_click(cx.listener(
                    |this, _: &ClickEvent, w, cx| {
                        this.open_chat("lead".into(), w, cx);
                    },
                )),
            )
            .child(
                div()
                    .id("past-dot")
                    .px_1()
                    .text_xs()
                    .text_color(theme::faint())
                    .tooltip(move |w, cx| Tooltip::new(freed.clone()).build(w, cx))
                    .child(crate::icons::of("⏏")),
            )
            .child(
                div()
                    .min_w(px(24.))
                    .max_w(px(160.))
                    .truncate()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(title),
            )
            .child(
                h_flex()
                    .p(px(2.))
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::ink())
                    .children(PAST_VIEWS.into_iter().map(|v| {
                        let on = v == view;
                        let tip: SharedString = v.label().into();
                        h_flex()
                            .id(SharedString::from(format!("past-view-{}", v.key())))
                            .h(px(28.))
                            .gap_1p5()
                            .rounded_md()
                            .text_xs()
                            .font_weight(FontWeight::MEDIUM)
                            .when(on, |el| {
                                el.px_2p5()
                                    .bg(theme::surface_raised())
                                    .text_color(theme::primary())
                            })
                            .when(!on, |el| {
                                el.w(px(28.))
                                    .justify_center()
                                    .text_color(theme::muted())
                                    .cursor_pointer()
                                    .hover(|s| s.text_color(theme::primary()))
                                    .active(|s| s.opacity(0.7))
                                    .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                                        this.past.view = v;
                                        this.panel_sync(w, cx);
                                        cx.notify();
                                    }))
                            })
                            .child(crate::icons::of(v.glyph()))
                            .when(on, |el| el.child(v.label()))
                    })),
            )
            .child(div().flex_1())
            .into_any_element()
    }

    /// render_past_view is the past agent's view in the panel.
    pub(super) fn render_past_view(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let Some(a) = self.past.agent.as_ref().filter(|a| a.r#ref == r) else {
            return super::panel::loading("past");
        };
        let fleet = as_fleet(a);
        match self.past.view {
            PView::Files => self.render_files(),
            PView::Timeline => self.render_timeline(r, cx),
            _ => self.render_evidence(Some(&fleet), cx),
        }
    }
}
