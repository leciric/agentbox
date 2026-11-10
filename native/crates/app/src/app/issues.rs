//! A project's open GitHub issues beside its lead (IssuesPanel.tsx), read
//! with the project's GitHub account like its pull requests: the switch that
//! picks up labelled issues by itself (PATCH /v1/projects/{p}), the Waiting /
//! With an agent / All filters, and each issue with its labels, author,
//! comments, who has it and its pull request, and Hand to an agent (POST
//! /v1/agents with the issue's number). GET /v1/projects/{p}/issues.
use agentbox_api::{Issue, Job, Project, ProjectIssues, UpdateProjectRequest};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::pulls::{chip, empty_state, github_fix, github_sentence, hex, link, mix, notice};
use super::settings::defaults::time_ago;
use crate::theme;
use crate::toasts;

#[derive(Clone, Copy, PartialEq, Default)]
pub enum Filter {
    #[default]
    Waiting,
    Handed,
    All,
}

#[derive(Default)]
pub struct Issues {
    project: String,
    data: Option<Result<ProjectIssues, String>>,
    filter: Filter,
    refreshing: bool,
    handing: Option<i64>,
    picking_up: bool,
}

fn handed_now(i: &Issue) -> bool {
    i.handed.as_ref().is_some_and(|h| !h.retired)
}

fn filtered(issues: &[Issue], f: Filter) -> Vec<&Issue> {
    issues
        .iter()
        .filter(|i| match f {
            Filter::Waiting => !handed_now(i),
            Filter::Handed => handed_now(i),
            Filter::All => true,
        })
        .collect()
}

impl AppView {
    pub(super) fn load_issues(&mut self, refresh: bool, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.issues.project != project {
            self.issues = Issues {
                project: project.clone(),
                ..Default::default()
            };
        }
        self.issues.refreshing = refresh;
        let client = self.client.clone();
        let path = format!(
            "/v1/projects/{}/issues{}",
            crate::files::encode(&project),
            if refresh { "?refresh=1" } else { "" }
        );
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<ProjectIssues>(&path)
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.issues.project == project {
                    this.issues.refreshing = false;
                    this.issues.data = Some(res);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn hand_issue(&mut self, n: i64, cx: &mut Context<Self>) {
        let project = self.issues.project.clone();
        let client = self.client.clone();
        self.issues.handing = Some(n);
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Job>(
                    "/v1/agents",
                    &serde_json::json!({ "project": project, "issue": n, "ai": "" }),
                )
                .await;
            this.update(cx, |this, cx| {
                this.issues.handing = None;
                match res {
                    Ok(_) => toasts::info(cx, tv("issues.handedToast", &[("number", n.into())])),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_issues(false, cx);
                this.load_fleet_now(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn set_pick_up(&mut self, on: bool, cx: &mut Context<Self>) {
        let project = self.issues.project.clone();
        let client = self.client.clone();
        self.issues.picking_up = true;
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&UpdateProjectRequest {
                pick_up_issues: Some(on),
                ..Default::default()
            })
            .unwrap_or_default();
            let res = client
                .raw(
                    agentbox_api::client::Method::PATCH,
                    &format!("/v1/projects/{}", crate::files::encode(&project)),
                    Some(body),
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<Project>(&b)?));
            this.update(cx, |this, cx| {
                this.issues.picking_up = false;
                match res {
                    Ok(p) => {
                        let name = if p.display_name.is_empty() {
                            p.name.clone()
                        } else {
                            p.display_name.clone()
                        };
                        toasts::info(
                            cx,
                            tv(
                                if p.pick_up_issues {
                                    "defaults.project.pickUpOnToast"
                                } else {
                                    "defaults.project.pickUpOffToast"
                                },
                                &[("name", (&name).into())],
                            ),
                        );
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_issues(false, cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// render_issues is IssuesPanel under its title.
    pub(super) fn render_issues(&self, cx: &mut Context<Self>) -> AnyElement {
        let body: AnyElement = match &self.issues.data {
            None => super::panel::loading("issues"),
            Some(Err(e)) => controls::note_in(e.clone(), theme::error()),
            Some(Ok(d)) if d.github.is_empty() && d.github_error.is_none() => {
                empty_state(t("issues.noRepo.title"), t("issues.noRepo.body"))
            }
            Some(Ok(d)) => self.issues_body(d, cx),
        };
        div()
            .id("issues-scroll")
            .size_full()
            .overflow_y_scroll()
            .child(div().p_3().child(body))
            .into_any_element()
    }

    fn issues_body(&self, d: &ProjectIssues, cx: &mut Context<Self>) -> AnyElement {
        let project = d.project.clone();
        let mut col = v_flex().gap_3();
        if !d.github.is_empty() {
            col = col.child(
                h_flex()
                    .flex_wrap()
                    .gap_1p5()
                    .px_1()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(div().font_family(theme::mono()).child(d.github.clone()))
                    .when(!d.github_account.is_empty(), |el| {
                        let p = project.clone();
                        el.child(t("memory.pulls.as")).child(
                            link("issues-account", d.github_account.clone()).on_click(cx.listener(
                                move |this, _: &ClickEvent, w, cx| {
                                    this.open_settings_at(format!("project:{p}"), w, cx)
                                },
                            )),
                        )
                    }),
            );
        }
        if let Some(e) = &d.github_error {
            let p = project.clone();
            col = col.child(
                v_flex()
                    .gap_1()
                    .child(notice(github_sentence(e), theme::starting()))
                    .children(github_fix("issues-fix", e, p, cx)),
            );
        }
        if !d.github.is_empty() {
            let on = d.pick_up_issues;
            let label = d.label.clone();
            let sentence = tv(
                if on {
                    "issues.pickupOn"
                } else {
                    "issues.pickupOff"
                },
                &[("label", "\u{1}".into())],
            );
            let (before, after) = sentence.split_once('\u{1}').unwrap_or((&sentence, ""));
            let green = hex("0e8a16");
            col = col.child(
                h_flex()
                    .gap_3()
                    .px_4()
                    .py_3()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::surface_raised())
                    .child(
                        div()
                            .text_color(theme::muted())
                            .child(crate::icons::of("✦")),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .child(t("issues.pickupSwitch")),
                            )
                            .child(
                                h_flex()
                                    .flex_wrap()
                                    .gap_1()
                                    .text_xs()
                                    .text_color(theme::muted())
                                    .child(before.to_string())
                                    .child(
                                        div()
                                            .px_1p5()
                                            .rounded_full()
                                            .border_1()
                                            .border_color(green.opacity(0.6))
                                            .bg(green.opacity(0.18))
                                            .text_color(mix(green, theme::primary(), 0.4))
                                            .child(label),
                                    )
                                    .child(after.to_string()),
                            ),
                    )
                    .child(controls::toggle(
                        "issues-pickup",
                        on,
                        self.issues.picking_up,
                        cx.listener(move |this, _: &ClickEvent, _, cx| this.set_pick_up(!on, cx)),
                    )),
            );
        }
        if d.issues.is_empty() {
            if d.github_error.is_none() {
                col = col.child(empty_state(
                    t("issues.none.title"),
                    tv("issues.none.body", &[("repo", (&d.github).into())]),
                ));
            }
            return col.into_any_element();
        }
        let f = self.issues.filter;
        let filter_chip = |id: &str, key: &str, to: Filter, n: usize| {
            chip(id, t(key), Some(n), f == to).on_click(cx.listener(
                move |this, _: &ClickEvent, _, cx| {
                    this.issues.filter = to;
                    cx.notify();
                },
            ))
        };
        let refresh_tip: SharedString = t("memory.pulls.refresh").into();
        col = col.child(
            h_flex()
                .flex_wrap()
                .gap_1()
                .child(filter_chip(
                    "issues-waiting",
                    "issues.filter.waiting",
                    Filter::Waiting,
                    filtered(&d.issues, Filter::Waiting).len(),
                ))
                .child(filter_chip(
                    "issues-handed",
                    "issues.filter.handed",
                    Filter::Handed,
                    filtered(&d.issues, Filter::Handed).len(),
                ))
                .child(filter_chip(
                    "issues-all",
                    "issues.filter.all",
                    Filter::All,
                    d.issues.len(),
                ))
                .child(div().flex_1())
                .child(
                    h_flex()
                        .gap_1p5()
                        .text_xs()
                        .text_color(theme::faint())
                        .when(self.issues.refreshing, |el| {
                            el.child(crate::icons::spinner())
                        })
                        .child(if self.issues.refreshing {
                            t("memory.pulls.refreshing")
                        } else {
                            d.fetched_at
                                .as_deref()
                                .map(|w| {
                                    tv("memory.pulls.readAgo", &[("when", time_ago(w).into())])
                                })
                                .unwrap_or_default()
                        }),
                )
                .child(
                    div()
                        .id("issues-refresh")
                        .px_1p5()
                        .rounded_md()
                        .text_color(theme::muted())
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                        .tooltip(move |w, cx| Tooltip::new(refresh_tip.clone()).build(w, cx))
                        .on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| this.load_issues(true, cx)),
                        )
                        .child(crate::icons::of("↻")),
                ),
        );
        let visible = filtered(&d.issues, f);
        if visible.is_empty() {
            col = col.child(
                div()
                    .px_1()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(t("issues.noMatch")),
            );
        }
        for i in visible {
            col = col.child(self.issue_row(&project, i, cx));
        }
        col.into_any_element()
    }

    fn issue_row(&self, project: &str, i: &Issue, cx: &mut Context<Self>) -> AnyElement {
        let h = i.handed.clone();
        let handing = self.issues.handing == Some(i.number);
        let url = i.url.clone();
        let n = i.number;
        let hand_tip: SharedString = tv("issues.handTip", &[("number", n.into())]).into();
        let agent_line = h.clone().map(|h| {
            let r = format!("{project}/{}", h.agent);
            let mut el = h_flex().gap_1p5().child(crate::icons::of("◎"));
            el = if h.agent.is_empty() {
                el.child(t("issues.starting"))
            } else if !h.retired {
                el.child(
                    link(
                        SharedString::from(format!("issue-agent-{n}")),
                        tv("issues.with", &[("name", (&h.agent).into())]),
                    )
                    .on_click(cx.listener(
                        move |this, _: &ClickEvent, w, cx| this.open_agent_ref(r.clone(), w, cx),
                    )),
                )
            } else {
                el.child(tv("issues.retired", &[("name", (&h.agent).into())]))
            };
            el.when(h.auto, |el| {
                el.child(controls::badge(t("issues.auto"), theme::muted()))
            })
        });
        let pr = h.as_ref().and_then(|h| h.pull_request.clone());
        v_flex()
            .id(SharedString::from(format!("issue-{n}")))
            .px_4()
            .py_3p5()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface_raised())
            .child(
                h_flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .text_color(theme::running())
                            .child(crate::icons::of("◉")),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .child(
                                div()
                                    .id(SharedString::from(format!("issue-title-{n}")))
                                    .truncate()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .cursor_pointer()
                                    .hover(|s| s.text_color(theme::accent()))
                                    .on_click(move |_, _, cx| cx.open_url(&url))
                                    .child(format!("{} ↗", i.title)),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .font_family(theme::mono())
                                    .text_color(theme::muted())
                                    .child(format!("#{n}")),
                            )
                            .when(!i.labels.is_empty(), |el| {
                                el.child(h_flex().mt_1p5().flex_wrap().gap_1().children(
                                    i.labels.iter().map(|l| {
                                        let c = hex(&l.color);
                                        div()
                                            .px_2()
                                            .rounded_full()
                                            .border_1()
                                            .border_color(c.opacity(0.6))
                                            .bg(c.opacity(0.18))
                                            .text_xs()
                                            .text_color(mix(c, theme::primary(), 0.4))
                                            .child(l.name.clone())
                                    }),
                                ))
                            }),
                    ),
            )
            .child(
                h_flex()
                    .mt_2p5()
                    .flex_wrap()
                    .gap_x_4()
                    .gap_y_1p5()
                    .text_xs()
                    .text_color(theme::muted())
                    .when(!i.author.is_empty(), |el| {
                        el.child(format!("◯ {}", i.author))
                    })
                    .when(i.comments > 0, |el| {
                        el.child(format!(
                            "▭ {}",
                            tv("memory.pulls.comments", &[("count", i.comments.into())])
                        ))
                    })
                    .children(agent_line)
                    .when_some(pr, |el, pr| {
                        let url = pr.url.clone();
                        el.child(
                            h_flex()
                                .id(SharedString::from(format!("issue-pr-{n}")))
                                .gap_1p5()
                                .cursor_pointer()
                                .hover(|s| s.text_color(theme::primary()))
                                .on_click(move |_, _, cx| cx.open_url(&url))
                                .child(format!(
                                    "⇄ {}",
                                    tv("issues.pullRequest", &[("number", pr.number.into())])
                                ))
                                .child(super::pulls::state_badge(&pr.state, false)),
                        )
                    })
                    .child(div().flex_1())
                    .when_some(i.updated_at.clone(), |el, u| {
                        el.child(div().text_color(theme::faint()).child(time_ago(&u)))
                    })
                    .when(!handed_now(i), |el| {
                        el.child(
                            div()
                                .id(SharedString::from(format!("issue-hand-tip-{n}")))
                                .tooltip(move |w, cx| Tooltip::new(hand_tip.clone()).build(w, cx))
                                .child(controls::button(
                                    SharedString::from(format!("issue-hand-{n}")),
                                    format!(
                                        "{} {}",
                                        if handing { "…" } else { "◎" },
                                        t(if h.is_some() {
                                            "issues.handAgain"
                                        } else {
                                            "issues.hand"
                                        })
                                    ),
                                    Btn::Secondary,
                                    handing,
                                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.hand_issue(n, cx)
                                    }),
                                )),
                        )
                    }),
            )
            .into_any_element()
    }
}
