//! The first run (components/FirstRun.tsx, lib/firstRun.ts): a new
//! installation's page until it's finished, a step at a time with the steps
//! in a rail beside it — the AI tool, the machine, signing in to the tool,
//! GitHub, a first project cloned from it, and the doctor's check. Each step
//! is skipped once it's done; "Set up later" leaves for Settings → Setup.
//!
//! The daemon says whether it's a first run (Settings.firstRun) and is told
//! when it's over (firstRunDone); this computer remembers it too, as
//! Electron's localStorage does, so a VM removed later never brings it back.
//! Its steps reuse Settings' own: the checklist's rows and their fixes, the
//! Accounts cards, and the doctor. The command-line tool's step is
//! Electron's main process's, and isn't here.
use std::collections::HashSet;
use std::time::Duration;

use agentbox_api::{
    AgentPick, CloneProjectRequest, GitHubRepo, JOB_RUNNING, Job, Project, SetupCheck,
    UpdateSettingsRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note};
use super::Entry;
use super::defaults::{ai_label, time_ago};
use super::setup::{step_mark, step_title, usable};
use crate::{prefs, theme};

const STEPS: [&str; 6] = ["tool", "machine", "login", "github", "project", "check"];
const TOOLS: [&str; 5] = ["claude", "codex", "opencode", "cursor", "gemini"];
/// The machine's steps (machineIds), less the tool's own (cli).
const MACHINE: [&str; 4] = ["incus", "host", "image", "storage"];
/// MARK keeps where this computer's first run is: "started" or "done".
const MARK: &str = "firstRun";
const TOOL: &str = "firstRunTool";

fn static_tool(ai: &str) -> Option<&'static str> {
    TOOLS.into_iter().find(|t| *t == ai)
}

/// Progress is how far the first run has got (FirstRunProgress).
#[derive(Default, Clone, Copy)]
struct Progress {
    tool: bool,
    machine: bool,
    login: bool,
    github: bool,
    project: bool,
}

impl Progress {
    /// at is the first step not done (firstRunStep).
    fn at(&self) -> &'static str {
        match () {
            _ if !self.tool => "tool",
            _ if !self.machine => "machine",
            _ if !self.login => "login",
            _ if !self.github => "github",
            _ if !self.project => "project",
            _ => "check",
        }
    }
    fn done(&self, step: &str) -> bool {
        let i = |s: &str| STEPS.iter().position(|x| *x == s).unwrap_or(0);
        i(step) < i(self.at())
    }
}

pub(in crate::app) struct FirstRun {
    /// active is whether the first run is the page.
    pub active: bool,
    tool: Option<&'static str>,
    picked: &'static str,
    viewing: Option<&'static str>,
    skipped: HashSet<&'static str>,
    told: Option<&'static str>,
    cloned: Option<String>,
    repos: Option<Result<Vec<GitHubRepo>, String>>,
    repo: Option<GitHubRepo>,
    clone_job: Option<String>,
    error: Option<String>,
    finishing: bool,
    polling: bool,
    search: Entity<InputState>,
    _sub: Subscription,
}

impl FirstRun {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let search =
            cx.new(|cx| InputState::new(window, cx).placeholder(t("firstRun.project.search")));
        let sub = cx.subscribe(&search, |_, _, ev: &InputEvent, cx| {
            if matches!(ev, InputEvent::Change) {
                cx.notify();
            }
        });
        let tool = prefs::get(TOOL)
            .and_then(|v| v.as_str().map(str::to_string))
            .and_then(|s| static_tool(&s));
        Self {
            active: false,
            tool,
            picked: tool.unwrap_or("claude"),
            viewing: None,
            skipped: HashSet::new(),
            told: None,
            cloned: None,
            repos: None,
            repo: None,
            clone_job: None,
            error: None,
            finishing: false,
            polling: false,
            search,
            _sub: sub,
        }
    }
}

/// mark is where this computer's first run is.
fn mark() -> Option<String> {
    prefs::get(MARK).and_then(|v| v.as_str().map(str::to_string))
}

impl AppView {
    /// check_first_run reads the settings and makes the first run the page
    /// when the daemon says it is one and this computer hasn't finished it
    /// (showFirstRun).
    pub(in crate::app) fn check_first_run(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let Ok(s) = client.get::<agentbox_api::Settings>("/v1/settings").await else {
                return;
            };
            this.update_in(cx, |this, window, cx| {
                let first = s.first_run;
                this.settings_page.settings = Some(s);
                if !first {
                    if mark().as_deref() != Some("done") {
                        prefs::set(MARK, "done".into());
                    }
                    this.after_first_run(cx);
                    return;
                }
                if this.settings_page.first_run.active {
                    return;
                }
                if mark().as_deref() == Some("done") {
                    this.after_first_run(cx);
                    return;
                }
                if mark().is_none() {
                    prefs::set(MARK, "started".into());
                }
                this.settings_page.first_run.active = true;
                this.load_accounts(window, cx);
                this.load_setup(cx);
                this.poll_first_run(window, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// poll_first_run reads what the steps show every three seconds while
    /// the first run is open: the logins, the checks, the projects.
    fn poll_first_run(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if std::mem::replace(&mut self.settings_page.first_run.polling, true) {
            return;
        }
        cx.spawn_in(window, async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(3)).await;
                let go = this.update_in(cx, |this, window, cx| {
                    if !this.settings_page.first_run.active {
                        this.settings_page.first_run.polling = false;
                        return false;
                    }
                    this.load_accounts(window, cx);
                    this.load_setup(cx);
                    this.first_run_tick(window, cx);
                    true
                });
                if !matches!(go, Ok(true)) {
                    return;
                }
            }
        })
        .detach();
    }

    /// first_run_tick tells the daemon the tool chosen, lists the
    /// repositories once GitHub is there, and picks up a clone that ended.
    fn first_run_tick(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let fr = &self.settings_page.first_run;
        let saved = self
            .settings_page
            .settings
            .as_ref()
            .map(|s| s.last_agent_pick.ai.clone());
        if let (Some(tool), Some(saved)) = (fr.tool, saved)
            && saved != tool
            && fr.told != Some(tool)
        {
            self.settings_page.first_run.told = Some(tool);
            self.save(
                UpdateSettingsRequest {
                    last_agent_pick: Some(AgentPick {
                        ai: tool.into(),
                        ..Default::default()
                    }),
                    ..Default::default()
                },
                cx,
            );
        }
        let github = self
            .settings_page
            .accounts
            .auth
            .as_ref()
            .is_some_and(|a| a.github);
        if github && self.settings_page.first_run.repos.is_none() && self.projects.is_empty() {
            self.settings_page.first_run.repos = Some(Ok(Vec::new()));
            let client = self.client.clone();
            cx.spawn(async move |this, cx| {
                let res = client.get::<Vec<GitHubRepo>>("/v1/github/repos").await;
                this.update(cx, |this, cx| {
                    this.settings_page.first_run.repos = Some(res.map_err(|e| e.to_string()));
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        let ended = self
            .settings_page
            .first_run
            .clone_job
            .as_ref()
            .and_then(|id| {
                self.settings_page
                    .setup
                    .jobs
                    .get(id)
                    .and_then(|v| v.job.clone())
                    .filter(|j| j.status != JOB_RUNNING)
            });
        if let Some(job) = ended {
            self.clone_ended(job, window, cx);
        }
    }

    fn clone_ended(&mut self, job: Job, window: &mut Window, cx: &mut Context<Self>) {
        let fr = &mut self.settings_page.first_run;
        fr.clone_job = None;
        if job.status != "succeeded" {
            fr.error = Some(if job.error.is_empty() {
                job.status
            } else {
                job.error
            });
            cx.notify();
            return;
        }
        if let Ok(p) = serde_json::from_value::<Project>(job.result) {
            fr.cloned = Some(p.name);
        }
        fr.viewing = None;
        self.load_projects(window, cx);
    }

    fn first_run_progress(&self) -> (Progress, bool) {
        let fr = &self.settings_page.first_run;
        let setup = self.settings_page.accounts.setup.as_ref();
        let auth = self.settings_page.accounts.auth.as_ref();
        let loaded = setup.is_some() && auth.is_some() && self.settings_page.settings.is_some();
        let checks = setup.map(|s| s.checks.as_slice()).unwrap_or_default();
        let machine = loaded
            && checks
                .iter()
                .filter(|c| MACHINE.contains(&c.id.as_str()) && c.required && c.id != "image")
                .all(|c| usable(&c.status));
        let login = fr.tool.is_some_and(|tool| {
            checks
                .iter()
                .find(|c| c.id == tool)
                .is_some_and(|c| c.status != "optional" && usable(&c.status))
        });
        let p = Progress {
            tool: fr.tool.is_some(),
            machine,
            login,
            github: auth.is_some_and(|a| a.github) || fr.skipped.contains("github"),
            project: !self.projects.is_empty() || fr.skipped.contains("project"),
        };
        (p, loaded)
    }

    /// finish_first_run tells the daemon it's done and opens the project it
    /// cloned, or the first one, or Home.
    fn finish_first_run(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let fr = &mut self.settings_page.first_run;
        if fr.finishing {
            return;
        }
        fr.finishing = true;
        prefs::set(MARK, "done".into());
        self.save(
            UpdateSettingsRequest {
                first_run_done: Some(true),
                ..Default::default()
            },
            cx,
        );
        let fr = &mut self.settings_page.first_run;
        fr.active = false;
        let to = fr
            .cloned
            .clone()
            .or_else(|| self.projects.first().map(|p| p.name.clone()))
            .unwrap_or_else(|| agentbox_api::HOME_PROJECT.to_string());
        self.select_project(to, window, cx);
        self.after_first_run(cx);
        cx.notify();
    }

    fn go_step(&mut self, step: Option<&'static str>, cx: &mut Context<Self>) {
        self.settings_page.first_run.viewing = step;
        cx.notify();
    }

    /// render_first_run is the page: a heading, the steps in a rail, and
    /// the step open beside them.
    pub(in crate::app) fn render_first_run(&self, cx: &mut Context<Self>) -> AnyElement {
        let fr = &self.settings_page.first_run;
        let (p, loaded) = self.first_run_progress();
        let at = p.at();
        let step = match fr.viewing {
            Some(v) if p.done(v) || v == at => v,
            _ => at,
        };
        let rail =
            v_flex()
                .w(px(208.))
                .flex_none()
                .gap_0p5()
                .children(STEPS.iter().enumerate().map(|(i, s)| {
                    let done = p.done(s);
                    let active = *s == step;
                    let reachable = done || *s == at;
                    let s: &'static str = s;
                    h_flex()
                        .id(SharedString::from(format!("first-run-step-{s}")))
                        .gap_2p5()
                        .px_2()
                        .py_1p5()
                        .rounded_lg()
                        .text_sm()
                        .when(active, |el| {
                            el.bg(theme::surface_raised()).text_color(theme::primary())
                        })
                        .when(!active, |el| el.text_color(theme::muted()))
                        .when(!done && !active, |el| el.opacity(0.6))
                        .when(reachable, |el| {
                            el.cursor_pointer()
                                .hover(|s| s.bg(theme::surface_strong()))
                                .active(|s| s.bg(theme::surface_vivid()))
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.go_step(Some(s), cx)
                                }))
                        })
                        .child(
                            div()
                                .flex_none()
                                .size(px(16.))
                                .rounded_full()
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_size(px(10.))
                                .font_weight(FontWeight::SEMIBOLD)
                                .map(|el| {
                                    if done {
                                        el.text_color(theme::running()).child(crate::icons::of("✓"))
                                    } else if active {
                                        el.bg(theme::brand(500))
                                            .text_color(theme::on_brand())
                                            .child((i + 1).to_string())
                                    } else {
                                        el.border_1()
                                            .border_color(theme::faint())
                                            .text_color(theme::faint())
                                    }
                                }),
                        )
                        .child(
                            div()
                                .min_w_0()
                                .truncate()
                                .child(t(&format!("firstRun.{s}.title"))),
                        )
                }));
        let panel = if loaded || step == "tool" {
            self.first_run_step(step, p, cx)
        } else {
            note(t("common.loading"))
        };
        div()
            .id("first-run")
            .size_full()
            .overflow_y_scroll()
            .child(
                v_flex()
                    .mx_auto()
                    .w_full()
                    .max_w(px(896.))
                    .px_8()
                    .py_8()
                    .gap_6()
                    .child(
                        h_flex()
                            .items_end()
                            .gap_4()
                            .child(crate::motion::enter(
                                theme::logo(px(44.)),
                                "first-run-logo",
                                0,
                            ))
                            .child(
                                v_flex()
                                    .min_w_0()
                                    .flex_1()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(px(24.))
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::title())
                                            .child(t("firstRun.heading")),
                                    )
                                    .child(
                                        div()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(t("firstRun.intro")),
                                    ),
                            )
                            .child(button(
                                "first-run-later",
                                t("firstRun.later"),
                                Btn::Ghost,
                                fr.finishing,
                                cx.listener(|this, _: &ClickEvent, window, cx| {
                                    this.finish_first_run(window, cx)
                                }),
                            )),
                    )
                    .child(
                        h_flex()
                            .items_start()
                            .gap_5()
                            .child(rail)
                            .child(div().flex_1().min_w_0().child(panel)),
                    ),
            )
            .into_any_element()
    }

    /// step_panel is StepPanel: the step's title and intro over what it
    /// does, and its buttons under it.
    fn step_panel(
        &self,
        step: &str,
        title: Option<String>,
        body: AnyElement,
        footer: Vec<AnyElement>,
    ) -> AnyElement {
        let card = v_flex()
            .p_5()
            .rounded(theme::radius::XXL)
            .bg(theme::linear_panel())
            .border_1()
            .border_color(theme::line())
            .shadow(theme::shadow_card());
        v_flex()
            .gap_4()
            .child(crate::motion::enter(
                card.child(
                    div()
                        .text_size(theme::text::HEADING)
                        .text_color(theme::title())
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(title.unwrap_or_else(|| t(&format!("firstRun.{step}.title")))),
                )
                .child(
                    div()
                        .mt_1()
                        .text_sm()
                        .text_color(theme::muted())
                        .child(t(&format!("firstRun.{step}.intro"))),
                )
                .child(div().mt_4().child(body)),
                SharedString::from(format!("first-run-panel-{step}")),
                0,
            ))
            .when(!footer.is_empty(), |el| {
                el.child(h_flex().justify_end().gap_2().children(footer))
            })
            .into_any_element()
    }

    fn next_button(&self, cx: &mut Context<Self>) -> AnyElement {
        button(
            "first-run-next",
            format!("{} →", t("common.next")),
            Btn::Primary,
            false,
            cx.listener(|this, _: &ClickEvent, _, cx| this.go_step(None, cx)),
        )
    }

    fn skip_button(&self, step: &'static str, cx: &mut Context<Self>) -> AnyElement {
        button(
            SharedString::from(format!("first-run-skip-{step}")),
            t("firstRun.skip"),
            Btn::Ghost,
            false,
            cx.listener(move |this, _: &ClickEvent, _, cx| {
                let fr = &mut this.settings_page.first_run;
                fr.viewing = None;
                fr.skipped.insert(step);
                cx.notify();
            }),
        )
    }

    fn first_run_step(
        &self,
        step: &'static str,
        p: Progress,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let fr = &self.settings_page.first_run;
        match step {
            "tool" => {
                let picked = fr.picked;
                let cards = TOOLS.iter().enumerate().map(|(i, tool)| {
                    let tool: &'static str = tool;
                    let on = picked == tool;
                    let card = v_flex()
                        .id(SharedString::from(format!("first-run-tool-{tool}")))
                        .w(relative(0.48))
                        .flex_1()
                        .gap_0p5()
                        .p_3p5()
                        .rounded_xl()
                        .border_1()
                        .cursor_pointer()
                        .map(|el| {
                            if on {
                                el.border_color(theme::brand(400))
                                    .bg(theme::brand(400).opacity(0.1))
                                    .shadow(theme::glow(theme::brand(400), 0.5))
                            } else {
                                el.border_color(theme::line())
                                    .hover(|s| {
                                        s.bg(theme::surface()).border_color(theme::line_strong())
                                    })
                                    .active(|s| s.bg(theme::surface_strong()))
                            }
                        })
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.settings_page.first_run.picked = tool;
                            cx.notify();
                        }))
                        .child(
                            h_flex()
                                .gap_2()
                                .child(crate::icons::ai_icon(
                                    tool,
                                    px(16.),
                                    if on {
                                        theme::brand(300)
                                    } else {
                                        theme::muted()
                                    },
                                ))
                                .child(
                                    div()
                                        .text_sm()
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::title())
                                        .child(ai_label(tool)),
                                ),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t(&format!("firstRun.tool.needs.{tool}"))),
                        );
                    crate::motion::enter(
                        card,
                        SharedString::from(format!("first-run-tool-in-{tool}")),
                        i + 1,
                    )
                });
                let body = v_flex()
                    .gap_3()
                    .child(h_flex().flex_wrap().gap_2().children(cards))
                    .child(note(t("firstRun.tool.later")))
                    .into_any_element();
                let use_it = button(
                    "first-run-use",
                    format!(
                        "{} →",
                        tv("firstRun.tool.use", &[("tool", ai_label(picked).into())])
                    ),
                    Btn::Primary,
                    false,
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        let fr = &mut this.settings_page.first_run;
                        fr.tool = Some(picked);
                        fr.viewing = None;
                        prefs::set(TOOL, picked.into());
                        this.first_run_tick(window, cx);
                        cx.notify();
                    }),
                );
                self.step_panel("tool", None, body, vec![use_it])
            }
            "machine" => {
                let checks: Vec<&SetupCheck> = self
                    .settings_page
                    .accounts
                    .setup
                    .iter()
                    .flat_map(|s| &s.checks)
                    .filter(|c| MACHINE.contains(&c.id.as_str()) && c.required)
                    .collect();
                let open = checks
                    .iter()
                    .find(|c| {
                        !usable(&c.status) || matches!(c.status.as_str(), "building" | "updating")
                    })
                    .map(|c| c.id.clone());
                let rows = checks.iter().map(|c| {
                    v_flex()
                        .p_3()
                        .rounded_xl()
                        .border_1()
                        .border_color(theme::line())
                        .bg(theme::ink())
                        .child(
                            h_flex()
                                .items_start()
                                .gap_2p5()
                                .child(step_mark(&c.status))
                                .child(
                                    v_flex()
                                        .min_w_0()
                                        .flex_1()
                                        .gap_0p5()
                                        .child(
                                            div()
                                                .text_sm()
                                                .font_weight(FontWeight::MEDIUM)
                                                .child(step_title(c)),
                                        )
                                        .when(!c.detail.is_empty(), |el| {
                                            el.child(note(c.detail.clone()))
                                        }),
                                ),
                        )
                        .when(open.as_deref() == Some(c.id.as_str()), |el| {
                            el.children(self.step_body(c, cx).map(|b| div().mt_3().child(b)))
                        })
                });
                let body = v_flex().gap_2().children(rows).into_any_element();
                let footer = if p.machine {
                    vec![self.next_button(cx)]
                } else {
                    vec![]
                };
                self.step_panel("machine", None, body, footer)
            }
            "login" => {
                let tool = fr.tool.unwrap_or("claude");
                let e = Entry {
                    id: tool,
                    label: ai_label(tool).into(),
                    description: t(&format!("settings.setup.desc.{tool}")),
                    keywords: String::new(),
                    essential: true,
                    modified: false,
                    scope: String::new(),
                };
                let body = self.account_card(&e, cx);
                let footer = if p.login {
                    vec![self.next_button(cx)]
                } else {
                    vec![]
                };
                let title = tv(
                    "firstRun.login.titleFor",
                    &[("tool", ai_label(tool).into())],
                );
                self.step_panel("login", Some(title), body, footer)
            }
            "github" => {
                let e = Entry {
                    id: "github",
                    label: t("settings.setup.title.github"),
                    description: t("settings.setup.desc.github"),
                    keywords: String::new(),
                    essential: true,
                    modified: false,
                    scope: String::new(),
                };
                let body = self.account_card(&e, cx);
                let signed = self
                    .settings_page
                    .accounts
                    .auth
                    .as_ref()
                    .is_some_and(|a| a.github);
                let footer = if signed {
                    vec![self.next_button(cx)]
                } else {
                    vec![self.skip_button("github", cx)]
                };
                self.step_panel("github", None, body, footer)
            }
            "project" => {
                let footer = if self.projects.is_empty() {
                    vec![self.skip_button("project", cx)]
                } else {
                    vec![self.next_button(cx)]
                };
                let body = self.project_step(cx);
                self.step_panel("project", None, body, footer)
            }
            _ => {
                let label = if fr.cloned.is_some() || !self.projects.is_empty() {
                    t("firstRun.check.openProject")
                } else {
                    t("firstRun.check.goHome")
                };
                let go = crate::app::controls::button_busy(
                    "first-run-finish",
                    format!("{label} →"),
                    Btn::Primary,
                    fr.finishing,
                    false,
                    cx.listener(|this, _: &ClickEvent, window, cx| {
                        this.finish_first_run(window, cx)
                    }),
                );
                let body = self.doctor_card(cx);
                self.step_panel("check", None, body, vec![go])
            }
        }
    }

    /// project_step is ProjectStep: the projects there are, or a
    /// repository of the GitHub account's to clone as the first.
    fn project_step(&self, cx: &mut Context<Self>) -> AnyElement {
        let fr = &self.settings_page.first_run;
        if !self.projects.is_empty() {
            return v_flex()
                .gap_1p5()
                .children(self.projects.iter().map(|p| {
                    h_flex()
                        .gap_2()
                        .text_sm()
                        .child(
                            div()
                                .text_color(theme::running())
                                .child(crate::icons::of("✓")),
                        )
                        .child(if p.display_name.is_empty() {
                            p.name.clone()
                        } else {
                            p.display_name.clone()
                        })
                }))
                .into_any_element();
        }
        let github = self
            .settings_page
            .accounts
            .auth
            .as_ref()
            .is_some_and(|a| a.github);
        if !github {
            return note(t("firstRun.project.noGitHub"));
        }
        if let (Some(job), Some(repo)) = (&fr.clone_job, &fr.repo) {
            return v_flex()
                .gap_2()
                .child(div().text_sm().child(tv(
                    "firstRun.project.cloning",
                    &[("repo", repo.full_name.clone().into())],
                )))
                .child(self.job_progress(job))
                .into_any_element();
        }
        let query = fr.search.read(cx).value().to_lowercase();
        let words: Vec<&str> = query.split_whitespace().collect();
        let list: AnyElement = match &fr.repos {
            None => h_flex()
                .gap_2()
                .text_sm()
                .text_color(theme::subtle())
                .child(crate::icons::spinner())
                .child(t("firstRun.project.loading"))
                .into_any_element(),
            Some(Err(e)) => crate::app::controls::error_line(e.clone()),
            Some(Ok(r)) if r.is_empty() && fr.repos.is_some() && query.is_empty() => {
                note(t("firstRun.project.none"))
            }
            Some(Ok(repos)) => {
                let shown: Vec<&GitHubRepo> = repos
                    .iter()
                    .filter(|r| {
                        let text = format!("{} {}", r.full_name, r.description).to_lowercase();
                        words.iter().all(|w| text.contains(w))
                    })
                    .collect();
                if shown.is_empty() {
                    note(t("firstRun.project.noMatch"))
                } else {
                    let picked = fr.repo.as_ref().map(|r| r.full_name.clone());
                    v_flex()
                        .id("first-run-repos")
                        .max_h(px(320.))
                        .overflow_y_scroll()
                        .gap_1()
                        .children(shown.into_iter().map(|r| {
                            let on = picked.as_deref() == Some(r.full_name.as_str());
                            let repo = r.clone();
                            h_flex()
                                .id(SharedString::from(format!("repo-{}", r.full_name)))
                                .items_start()
                                .gap_2p5()
                                .px_3()
                                .py_2()
                                .rounded_lg()
                                .border_1()
                                .cursor_pointer()
                                .map(|el| {
                                    if on {
                                        el.border_color(theme::accent())
                                            .bg(theme::accent().opacity(0.1))
                                    } else {
                                        el.border_color(transparent_black())
                                            .hover(|s| s.bg(theme::surface_strong()))
                                            .active(|s| s.bg(theme::surface_vivid()))
                                    }
                                })
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.settings_page.first_run.repo = Some(repo.clone());
                                    cx.notify();
                                }))
                                .child(div().text_xs().text_color(theme::faint()).child(
                                    crate::icons::of(if r.private { "🔒" } else { "⎇" }),
                                ))
                                .child(
                                    v_flex()
                                        .min_w_0()
                                        .flex_1()
                                        .child(
                                            div()
                                                .truncate()
                                                .text_sm()
                                                .font_weight(FontWeight::MEDIUM)
                                                .child(r.full_name.clone()),
                                        )
                                        .when(!r.description.is_empty(), |el| {
                                            el.child(
                                                div()
                                                    .truncate()
                                                    .text_xs()
                                                    .text_color(theme::muted())
                                                    .child(r.description.clone()),
                                            )
                                        }),
                                )
                                .when(!r.pushed_at.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .flex_none()
                                            .text_xs()
                                            .text_color(theme::faint())
                                            .child(time_ago(&r.pushed_at)),
                                    )
                                })
                        }))
                        .into_any_element()
                }
            }
        };
        let label = match &fr.repo {
            Some(r) => tv(
                "firstRun.project.clone",
                &[("repo", r.full_name.clone().into())],
            ),
            None => t("firstRun.project.pick"),
        };
        v_flex()
            .gap_3()
            .child(Input::new(&fr.search))
            .child(list)
            .when_some(fr.error.clone(), |el, e| {
                el.child(crate::app::controls::error_line(e))
            })
            .child(h_flex().child(button(
                "first-run-clone",
                label,
                Btn::Primary,
                fr.repo.is_none(),
                cx.listener(|this, _: &ClickEvent, _, cx| this.clone_first(cx)),
            )))
            .into_any_element()
    }

    fn clone_first(&mut self, cx: &mut Context<Self>) {
        let Some(repo) = self.settings_page.first_run.repo.clone() else {
            return;
        };
        self.settings_page.first_run.error = None;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let req = CloneProjectRequest {
                url: repo.clone_url.clone(),
                ..Default::default()
            };
            let res = client.post::<_, Job>("/v1/projects/clone", &req).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(job) => {
                        this.settings_page.first_run.clone_job = Some(job.id.clone());
                        this.follow_job(job.id, cx);
                    }
                    Err(e) => this.settings_page.first_run.error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn the_first_step_not_done_is_where_it_is() {
        let mut p = Progress::default();
        assert_eq!(p.at(), "tool");
        p.tool = true;
        p.machine = true;
        assert_eq!(p.at(), "login");
        assert!(p.done("machine"));
        assert!(!p.done("github"));
        p.login = true;
        p.github = true;
        p.project = true;
        assert_eq!(p.at(), "check");
    }
}
