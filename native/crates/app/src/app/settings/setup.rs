//! Settings → Setup, the status view (SettingsView.tsx's Setup section,
//! DoctorPanel.tsx, SetupChecklist, ChecklistStep, OpenCodeInImage): what
//! `agentbox doctor` finds wrong and what fixes it (GET /v1/doctor, POST
//! /v1/doctor/fix), the checklist of what agents can't run without and the
//! optional steps (GET /v1/setup), the base image (GET /v1/image, POST
//! /v1/image/build, its job followed in the page), and the image's choices.
//! It reads them again every five seconds while it's open, as Electron does.
//!
//! The first-run wizard is a later task. What Electron's main process does
//! itself isn't here: the command-line tool's step, running host setup with
//! sudo (its command is shown to copy instead), and moving to a VM.
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use agentbox_api::client::Method;
use agentbox_api::{
    BuildImageRequest, DOCTOR_FAIL, DOCTOR_WARN, DoctorFixRequest, DoctorFixResult, DoctorReport,
    ImageStatus, JOB_RUNNING, Job, SetupCheck, SetupStatus, UpdateSettingsRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note_in, toggle};
use super::kit::{badge, note};
use super::kit::{card, enc, link, title};
use super::{Entry, Group, entry, group};
use crate::theme;

/// The daemon's checks about the machine, not a login (environmentIds).
const MACHINE: [&str; 6] = ["incus", "host", "image", "storage", "android", "preview"];
/// What a step waits for before it can be done (dependsOn).
fn depends_on(id: &str) -> Option<&'static str> {
    match id {
        "host" | "image" => Some("incus"),
        _ => None,
    }
}

/// usable is a step agents can go on with: done, under way, or a caveat.
pub(super) fn usable(status: &str) -> bool {
    matches!(status, "ok" | "updating" | "building" | "warn")
}

/// background is a step whose work runs on by itself.
fn background(status: &str) -> bool {
    matches!(status, "updating" | "building")
}

/// JobView is a job the page follows: what it is and its log so far.
#[derive(Default)]
pub(super) struct JobView {
    pub job: Option<Job>,
    pub lines: Vec<String>,
    polling: bool,
}

#[derive(Default)]
pub(super) struct SetupPage {
    doctor: Option<Result<DoctorReport, String>>,
    image: Option<Result<ImageStatus, String>>,
    fixing: bool,
    building: bool,
    said: Option<String>,
    error: Option<String>,
    unfolded: HashSet<String>,
    /// image_job is a build this page started, kept open to read its log.
    image_job: Option<String>,
    opencode_job: Option<String>,
    pub jobs: HashMap<String, JobView>,
    polling: bool,
}

pub(super) fn step_title(c: &SetupCheck) -> String {
    let key = format!("settings.setup.title.{}", c.id);
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        c.title.clone()
    }
}

fn step_description(id: &str) -> String {
    let key = format!("settings.setup.desc.{id}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        String::new()
    }
}

/// step_mark is StepIcon: a tick, a ring, dots, or a warning.
pub(super) fn step_mark(status: &str) -> AnyElement {
    let (mark, color) = match status {
        "ok" => ("✓", theme::running()),
        "optional" => ("○", theme::faint()),
        "checking" => ("…", theme::faint()),
        s if background(s) => ("↻", theme::accent()),
        _ => ("!", theme::starting()),
    };
    div()
        .flex_none()
        .size(px(18.))
        .rounded_full()
        .flex()
        .items_center()
        .justify_center()
        .bg(color.opacity(0.15))
        .text_xs()
        .font_weight(FontWeight::BOLD)
        .text_color(color)
        .child(crate::icons::of(mark))
        .into_any_element()
}

/// step_icon is Electron's StepIcon itself: a circled check, a dashed
/// circle, a spinner or a warning, in the status's colour, at a size.
fn step_icon(status: &str, size: f32) -> AnyElement {
    let (icon, color) = match status {
        "ok" => ("circle-check", theme::emerald(400)),
        "optional" => ("square-dashed", theme::subtle()),
        "checking" => ("loader-circle", theme::subtle()),
        s if background(s) => ("loader-circle", theme::sky(400)),
        _ => ("triangle-alert", theme::amber(300)),
    };
    div()
        .flex_none()
        .text_size(px(size))
        .text_color(color)
        .child(crate::icons::of(icon))
        .into_any_element()
}

/// step_badge is StepBadge: where a step stands, beside its title.
fn step_badge(c: &SetupCheck) -> Option<AnyElement> {
    let warn = theme::starting();
    Some(match c.status.as_str() {
        "warn" => badge(t("settings.setup.badgeCheck"), warn),
        "optional" => badge(t("settings.setup.badgeOptional"), theme::muted()),
        "building" => badge(t("settings.setup.badgeBuilding"), theme::muted()),
        "updating" => badge(t("settings.setup.badgeUpdating"), theme::muted()),
        "ok" | "checking" => return None,
        _ if !c.required => badge(t("settings.setup.badgeNotSetUp"), warn),
        "outdated" => badge(t("settings.setup.badgeOutdated"), warn),
        _ => badge(t("settings.setup.badgeNeeded"), warn),
    })
}

fn job_status(status: &str) -> (String, Hsla) {
    match status {
        "succeeded" => (t("agent.job.succeeded"), theme::running()),
        "failed" => (t("agent.job.failed"), theme::error()),
        "cancelled" => (t("agent.job.cancelled"), theme::muted()),
        _ => (t("agent.state.running"), theme::accent()),
    }
}

impl AppView {
    pub(super) fn setup_groups(&self) -> Vec<Group> {
        let setup = self.settings_page.accounts.setup.as_ref();
        let checks: Vec<&SetupCheck> = setup
            .map(|s| {
                s.checks
                    .iter()
                    .filter(|c| MACHINE.contains(&c.id.as_str()))
                    .collect()
            })
            .unwrap_or_default();
        let checklist_words = checks
            .iter()
            .filter(|c| c.required)
            .map(|c| format!("{} {} {}", step_title(c), step_description(&c.id), c.detail))
            .collect::<Vec<_>>()
            .join(" ");
        let s = self.settings_page.settings.as_ref();
        let optional: Vec<Entry> = checks
            .iter()
            .filter(|c| !c.required)
            .filter_map(|c| {
                let id = MACHINE.iter().find(|m| **m == c.id)?;
                Some(Entry {
                    id,
                    label: step_title(c),
                    description: step_description(&c.id),
                    keywords: c.detail.clone(),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                })
            })
            .collect();
        let mut groups = vec![
            group(
                "doctor",
                "settings.doctor.title",
                vec![Entry {
                    id: "doctor",
                    label: t("settings.doctor.title"),
                    description: String::new(),
                    keywords: "agentbox doctor".into(),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .cards(),
            group(
                "required",
                "settings.checklist.title",
                vec![Entry {
                    id: "checklist",
                    label: t("settings.checklist.title"),
                    description: t("settings.section.setup.description"),
                    keywords: checklist_words,
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .cards(),
        ];
        if !optional.is_empty() {
            groups.push(group("optional", "settings.group.optional.title", optional).cards());
        }
        groups.push(group(
            "image",
            "settings.group.image.title",
            vec![
                entry(
                    "opencode-image",
                    "settings.entry.opencode-image.label",
                    "defaults.newAgent.openCodeDescription",
                    "settings.entry.opencode-image.keywords",
                )
                .modified(setup.is_some_and(|s| s.image.components.opencode)),
                entry(
                    "opencode-v2",
                    "settings.entry.opencode-v2.label",
                    "settings.openCodeV2.description",
                    "settings.entry.opencode-v2.keywords",
                )
                .modified(s.is_some_and(|s| s.open_code_version == "2")),
            ],
        ));
        groups
    }

    /// load_setup reads the checks, the doctor and the image, and again
    /// every five seconds while Setup is open.
    pub(super) fn load_setup(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let setup = client.get::<SetupStatus>("/v1/setup").await;
            let doctor = client.get::<DoctorReport>("/v1/doctor").await;
            let image = client.get::<ImageStatus>("/v1/image").await;
            this.update(cx, |this, cx| {
                if let Ok(s) = setup {
                    this.settings_page.accounts.setup = Some(s);
                }
                let p = &mut this.settings_page.setup;
                p.doctor = Some(doctor.map_err(|e| e.to_string()));
                p.image = Some(image.map_err(|e| e.to_string()));
                // A build started elsewhere is followed too.
                let running: Vec<String> = this
                    .settings_page
                    .accounts
                    .setup
                    .iter()
                    .flat_map(|s| &s.checks)
                    .filter(|c| !c.job.is_empty())
                    .map(|c| c.job.clone())
                    .collect();
                for j in running {
                    this.follow_job(j, cx);
                }
                this.poll_setup(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn poll_setup(&mut self, cx: &mut Context<Self>) {
        let p = &mut self.settings_page.setup;
        if p.polling {
            return;
        }
        p.polling = true;
        cx.spawn(async move |this, cx| {
            cx.background_executor().timer(Duration::from_secs(5)).await;
            this.update(cx, |this, cx| {
                this.settings_page.setup.polling = false;
                if this.settings && this.settings_page.open == "setup" {
                    this.load_setup(cx);
                }
            })
            .ok();
        })
        .detach();
    }

    /// follow_job reads a job and its log once a second until it ends.
    pub(in crate::app) fn follow_job(&mut self, id: String, cx: &mut Context<Self>) {
        let v = self.settings_page.setup.jobs.entry(id.clone()).or_default();
        if v.polling {
            return;
        }
        v.polling = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            loop {
                let job = client.get::<Job>(&format!("/v1/jobs/{}", enc(&id))).await;
                let log = client
                    .raw(Method::GET, &format!("/v1/jobs/{}/log", enc(&id)), None)
                    .await;
                let done = job.as_ref().map_or(true, |j| j.status != JOB_RUNNING);
                let gone = this
                    .update(cx, |this, cx| {
                        let v = this.settings_page.setup.jobs.entry(id.clone()).or_default();
                        if let Ok(j) = job {
                            v.job = Some(j);
                        }
                        if let Ok(b) = log {
                            v.lines = String::from_utf8_lossy(&b)
                                .lines()
                                .map(String::from)
                                .collect();
                        }
                        if done {
                            v.polling = false;
                            // What the job changed: the checks, the image.
                            this.load_setup(cx);
                            this.load_transfer_preview(cx);
                        }
                        cx.notify();
                    })
                    .is_err();
                if done || gone {
                    break;
                }
                cx.background_executor().timer(Duration::from_secs(1)).await;
            }
        })
        .detach();
    }

    /// job_progress is JobProgress: a job's state and the end of its log.
    pub(in crate::app) fn job_progress(&self, id: &str) -> AnyElement {
        let v = self.settings_page.setup.jobs.get(id);
        let job = v.and_then(|v| v.job.as_ref());
        let (label, color) = job_status(job.map_or(JOB_RUNNING, |j| j.status.as_str()));
        let lines = v.map(|v| v.lines.as_slice()).unwrap_or_default();
        let tail = &lines[lines.len().saturating_sub(14)..];
        v_flex()
            .gap_3()
            .child(
                h_flex().gap_2().child(badge(label, color)).child(
                    div()
                        .text_size(theme::text::BODY)
                        .text_color(theme::tertiary())
                        .child(match job {
                            Some(j) => format!("{} {}", j.kind, j.target),
                            None => tv("shell.jobProgress.job", &[("id", id.into())]),
                        }),
                ),
            )
            .when_some(job.filter(|j| !j.error.is_empty()), |el, j| {
                el.child(note_in(j.error.clone(), theme::rose(300)))
            })
            .when(!tail.is_empty(), |el| {
                el.child(
                    v_flex()
                        .p(px(14.))
                        .rounded_xl()
                        .bg(theme::well())
                        .border_1()
                        .border_color(theme::line())
                        .font_family(crate::theme::mono())
                        .text_size(theme::text::CAPTION)
                        .line_height(rems(1.6))
                        .children(tail.iter().map(|l| {
                            div()
                                .text_color(if l.starts_with("==>") {
                                    theme::secondary()
                                } else {
                                    theme::subtle()
                                })
                                .child(l.clone())
                        })),
                )
            })
            .into_any_element()
    }

    /// command_box is CommandBox: a command to run, and a way to copy it.
    pub(in crate::app) fn command_box(
        &self,
        id: &str,
        command: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let text = command.to_string();
        h_flex()
            .gap_2()
            .pl(px(14.))
            .pr_1p5()
            .py_1p5()
            .rounded_xl()
            .bg(theme::well())
            .border_1()
            .border_color(theme::line_strong())
            .font_family(crate::theme::mono())
            .text_size(theme::text::UI)
            .text_color(theme::secondary())
            .child(div().flex_none().text_color(theme::faint()).child("$"))
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .truncate()
                    .child(command.to_string()),
            )
            .child(link(
                SharedString::from(format!("copy-{id}")),
                t("common.copy"),
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    cx.write_to_clipboard(ClipboardItem::new_string(text.clone()));
                    this.settings_page.setup.said = Some(t("common.copied"));
                    cx.notify();
                }),
            ))
            .into_any_element()
    }

    fn doctor_fix(&mut self, checks: Vec<String>, cx: &mut Context<Self>) {
        self.settings_page.setup.fixing = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, DoctorFixResult>("/v1/doctor/fix", &DoctorFixRequest { checks })
                .await;
            this.update(cx, |this, cx| {
                let p = &mut this.settings_page.setup;
                p.fixing = false;
                match res {
                    Ok(r) => {
                        let failed: Vec<String> = r
                            .fixes
                            .iter()
                            .filter(|f| !f.error.is_empty())
                            .map(|f| {
                                format!(
                                    "{} {}",
                                    tv("doctor.fixFailed", &[("check", (&f.id).into())]),
                                    f.error
                                )
                            })
                            .collect();
                        p.error = (!failed.is_empty()).then(|| failed.join(" "));
                        p.doctor = Some(Ok(r.report));
                    }
                    Err(e) => p.error = Some(e.to_string()),
                }
                this.load_setup(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn build_image(&mut self, req: BuildImageRequest, opencode: bool, cx: &mut Context<Self>) {
        self.settings_page.setup.building = true;
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.post::<_, Job>("/v1/image/build", &req).await;
            this.update(cx, |this, cx| {
                let p = &mut this.settings_page.setup;
                p.building = false;
                match res {
                    Ok(job) => {
                        if opencode {
                            p.opencode_job = Some(job.id.clone());
                        } else {
                            p.image_job = Some(job.id.clone());
                        }
                        p.error = None;
                        this.follow_job(job.id, cx);
                    }
                    Err(e) => p.error = Some(e.to_string()),
                }
                this.load_setup(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn doctor_card(&self, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.settings_page.setup;
        let report = match &p.doctor {
            None => return note(t("doctor.checking")),
            Some(Err(e)) => return note_in(e.clone(), theme::rose(300)),
            Some(Ok(r)) => r,
        };
        let list: Vec<_> = report
            .checks
            .iter()
            .filter(|c| c.status == DOCTOR_FAIL)
            .chain(report.checks.iter().filter(|c| c.status == DOCTOR_WARN))
            .collect();
        let fixable: Vec<String> = list
            .iter()
            .filter(|c| c.auto_fix && c.job.is_empty())
            .map(|c| c.id.clone())
            .collect();
        let n = fixable.len();
        v_flex()
            .id("doctor")
            .gap_2()
            .child(
                h_flex()
                    .gap_2()
                    .child(div().flex_1().min_w_0().child(title(if list.is_empty() {
                        t("doctor.allOk")
                    } else {
                        tv("doctor.title", &[("count", list.len().into())])
                    })))
                    .when(n > 1, |el| {
                        el.child(button(
                            "doctor-fix-all",
                            tv("doctor.fixAll", &[("count", n.into())]),
                            Btn::Secondary,
                            p.fixing,
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.doctor_fix(Vec::new(), cx)
                            }),
                        ))
                    })
                    .child(button(
                        "doctor-again",
                        t("doctor.checkAgain"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.settings_page.setup.doctor = None;
                            this.load_setup(cx);
                            cx.notify();
                        }),
                    )),
            )
            .when(list.is_empty(), |el| {
                el.child(note(tv(
                    "doctor.allOkDetail",
                    &[("count", report.checks.len().into())],
                )))
            })
            .children(list.into_iter().map(|c| {
                let fail = c.status == DOCTOR_FAIL;
                let id = c.id.clone();
                card(SharedString::from(format!("doctor-{}", c.id)))
                    .p_4()
                    .when(fail, |el| el.border_color(theme::rose(400).opacity(0.2)))
                    .child(
                        h_flex()
                            .gap_3()
                            .child(if fail {
                                div()
                                    .flex_none()
                                    .text_size(px(20.))
                                    .text_color(theme::rose(400))
                                    .child(crate::icons::of("circle-x"))
                                    .into_any_element()
                            } else {
                                step_icon("warn", 20.)
                            })
                            .child(title(c.title.clone())),
                    )
                    .child(
                        div()
                            .text_size(theme::text::UI)
                            .text_color(theme::muted())
                            .child(c.message.clone()),
                    )
                    .when(!c.job.is_empty(), |el| {
                        el.child(note(t("doctor.beingFixed")))
                    })
                    .when(c.job.is_empty() && !c.fix.is_empty() && !c.auto_fix, |el| {
                        let command = ["agentbox ", "sudo ", "brew "]
                            .iter()
                            .any(|p| c.fix.starts_with(p));
                        el.child(if command {
                            self.command_box(&format!("doctor-{}", c.id), &c.fix, cx)
                        } else {
                            note(tv("doctor.fix", &[("fix", (&c.fix).into())]))
                        })
                    })
                    .when(c.auto_fix && c.job.is_empty(), |el| {
                        el.child(h_flex().child(button(
                            SharedString::from(format!("doctor-fix-{}", c.id)),
                            t("doctor.fixIt"),
                            Btn::Secondary,
                            p.fixing,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.doctor_fix(vec![id.clone()], cx)
                            }),
                        )))
                    })
                    // A failure with no fix of the doctor's own goes to the
                    // Home chat, in a session of its own (canAskHome).
                    .when(
                        fail && !c.auto_fix
                            && c.job.is_empty()
                            && c.id != agentbox_api::DOCTOR_AI_LOGIN,
                        |el| {
                            let check = c.id.clone();
                            let asking = self.asking_home.as_deref() == Some(c.id.as_str());
                            el.child(h_flex().child(button(
                                SharedString::from(format!("doctor-home-{}", c.id)),
                                t("doctor.askHome"),
                                Btn::Secondary,
                                asking,
                                cx.listener(move |this, _: &ClickEvent, window, cx| {
                                    this.ask_home(check.clone(), window, cx)
                                }),
                            )))
                        },
                    )
            }))
            .into_any_element()
    }

    /// checklist_card is SetupChecklist: what agents can't run without, in
    /// the order it has to be done, and the Claude Code login.
    pub(super) fn checklist_card(&self, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.settings_page.setup;
        let Some(setup) = self.settings_page.accounts.setup.as_ref() else {
            return super::super::panel::loading("checklist");
        };
        let steps: Vec<&SetupCheck> = setup
            .checks
            .iter()
            .filter(|c| (MACHINE.contains(&c.id.as_str()) && c.required) || c.id == "claude")
            .collect();
        let by_id = |id: &str| steps.iter().find(|c| c.id == id).copied();
        let waits_for = |c: &SetupCheck| {
            depends_on(&c.id)
                .and_then(by_id)
                .filter(|d| !usable(&d.status))
        };
        let next = steps
            .iter()
            .find(|c| c.status != "checking" && !usable(&c.status) && waits_for(c).is_none())
            .map(|c| c.id.clone());
        let done = steps.iter().filter(|c| usable(&c.status)).count();
        let total = steps.len();
        v_flex()
            .id("checklist")
            .gap_2()
            .when_some(p.error.clone(), |el, e| {
                el.child(note_in(e, theme::rose(300)))
            })
            .when_some(p.said.clone(), |el, s| {
                el.child(note_in(s, theme::emerald(300)))
            })
            .child(note(tv(
                "settings.checklist.progress",
                &[("done", done.into()), ("total", total.into())],
            )))
            .child(
                v_flex()
                    .overflow_hidden()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::surface().opacity(0.4))
                    .children(steps.iter().enumerate().map(|(i, c)| {
                        let is_next = next.as_deref() == Some(c.id.as_str());
                        let after = waits_for(c);
                        let body = self.step_body(c, cx);
                        let expanded = is_next
                            || background(&c.status)
                            || p.unfolded.contains(&c.id)
                            || (c.id == "image" && p.image_job.is_some());
                        let id = c.id.clone();
                        let has_body = body.is_some();
                        v_flex()
                            .id(SharedString::from(format!("setup-step-{}", c.id)))
                            .when(i > 0, |el| el.border_t_1().border_color(theme::line()))
                            .when(is_next, |el| {
                                el.border_l(px(3.))
                                    .border_color(theme::amber(400))
                                    .bg(theme::surface_raised())
                            })
                            .child(
                                h_flex()
                                    .id(SharedString::from(format!("setup-step-head-{}", c.id)))
                                    .gap_3()
                                    .px_4()
                                    .py_3()
                                    .when(after.is_some(), |el| el.opacity(0.6))
                                    .when(has_body && !is_next, |el| {
                                        el.cursor_pointer()
                                            .hover(|st| st.bg(theme::surface()))
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, _, cx| {
                                                    let u = &mut this.settings_page.setup.unfolded;
                                                    if !u.remove(&id) {
                                                        u.insert(id.clone());
                                                    }
                                                    cx.notify();
                                                },
                                            ))
                                    })
                                    .child(if after.is_some() {
                                        div()
                                            .flex_none()
                                            .text_size(px(16.))
                                            .text_color(theme::subtle())
                                            .child(crate::icons::of("square-dashed"))
                                            .into_any_element()
                                    } else {
                                        step_icon(&c.status, 16.)
                                    })
                                    .child(
                                        div()
                                            .flex_1()
                                            .min_w_0()
                                            .truncate()
                                            .text_size(theme::text::UI)
                                            .text_color(if is_next {
                                                theme::title()
                                            } else {
                                                theme::secondary()
                                            })
                                            .when(is_next, |el| {
                                                el.font_weight(FontWeight::SEMIBOLD)
                                            })
                                            .child(if is_next {
                                                tv(
                                                    "settings.checklist.next",
                                                    &[("step", (&step_title(c)).into())],
                                                )
                                            } else {
                                                step_title(c)
                                            }),
                                    )
                                    .map(|el| {
                                        if is_next {
                                            el.child(
                                                div()
                                                    .flex_none()
                                                    .text_size(theme::text::CAPTION)
                                                    .font_weight(FontWeight::SEMIBOLD)
                                                    .text_color(theme::amber(300))
                                                    .child(t("settings.checklist.nextBadge")),
                                            )
                                        } else if let Some(a) = after {
                                            el.child(note(tv(
                                                "settings.checklist.after",
                                                &[("step", (&step_title(a)).into())],
                                            )))
                                        } else if !c.detail.is_empty() {
                                            el.child(
                                                div()
                                                    .max_w(relative(0.5))
                                                    .truncate()
                                                    .text_xs()
                                                    .text_color(if usable(&c.status) {
                                                        theme::emerald(300).opacity(0.8)
                                                    } else {
                                                        theme::subtle()
                                                    })
                                                    .child(c.detail.clone()),
                                            )
                                        } else {
                                            el
                                        }
                                    }),
                            )
                            .when(expanded && after.is_none(), |el| {
                                el.child(
                                    v_flex()
                                        .gap_2()
                                        .px_4()
                                        .pb_3()
                                        .pl(px(44.))
                                        .when(is_next && !c.detail.is_empty(), |el| {
                                            el.child(note(c.detail.clone()))
                                        })
                                        .children(body),
                                )
                            })
                    })),
            )
            .child(note(if next.is_some() || done < total {
                t("settings.checklist.leadMeanwhile")
            } else {
                t("settings.checklist.allDone")
            }))
            .into_any_element()
    }

    /// step_body is what a step offers to do about itself.
    pub(super) fn step_body(&self, c: &SetupCheck, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = &self.settings_page.setup;
        match c.id.as_str() {
            "claude" => Some(
                h_flex()
                    .child(button(
                        "checklist-accounts",
                        t("settings.checklist.openAccounts"),
                        Btn::Primary,
                        false,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.open_settings_section("accounts".into(), window, cx)
                        }),
                    ))
                    .into_any_element(),
            ),
            "host" => Some(
                v_flex()
                    .gap_2()
                    .child(note(t("settings.setup.hostBodyAfterIncus")))
                    .child(self.command_box(
                        "host",
                        if c.fix.is_empty() {
                            "sudo agentbox host setup"
                        } else {
                            &c.fix
                        },
                        cx,
                    ))
                    .into_any_element(),
            ),
            "image" => {
                let incus_ok = self
                    .settings_page
                    .accounts
                    .setup
                    .iter()
                    .flat_map(|s| &s.checks)
                    .any(|x| x.id == "incus" && x.status == "ok");
                let job = p
                    .image_job
                    .clone()
                    .or_else(|| (!c.job.is_empty()).then(|| c.job.clone()));
                Some(
                    v_flex()
                        .gap_3()
                        .child(h_flex().child(button(
                            "setup-build-image",
                            if c.status == "missing" {
                                t("settings.setup.buildImage")
                            } else {
                                t("settings.setup.rebuildImage")
                            },
                            if c.status == "ok" {
                                Btn::Secondary
                            } else {
                                Btn::Primary
                            },
                            p.building || !incus_ok || background(&c.status),
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.build_image(BuildImageRequest::default(), false, cx)
                            }),
                        )))
                        .when(background(&c.status), |el| {
                            el.child(note(if c.status == "building" {
                                t("settings.setup.imageBuildingNote")
                            } else {
                                t("settings.setup.imageRebuildingNote")
                            }))
                        })
                        .when_some(p.image.as_ref().and_then(|r| r.as_ref().ok()), |el, im| {
                            let words: Vec<&str> = [im.step.as_str(), im.progress.as_str()]
                                .into_iter()
                                .filter(|s| !s.is_empty())
                                .collect();
                            el.when(!words.is_empty(), |el| el.child(note(words.join(" · "))))
                                .when(!im.error.is_empty(), |el| {
                                    el.child(note_in(im.error.clone(), theme::rose(300)))
                                })
                        })
                        .children(self.image_downloads(cx))
                        .when_some(job, |el, j| el.child(self.job_progress(&j)))
                        .into_any_element(),
                )
            }
            _ if !c.fix.is_empty() => Some(self.command_box(&c.id, &c.fix, cx)),
            _ => None,
        }
    }

    /// optional_step_card is an optional step's card (ChecklistStep).
    pub(super) fn optional_step_card(&self, e: &Entry, cx: &mut Context<Self>) -> AnyElement {
        let c = self
            .settings_page
            .accounts
            .setup
            .iter()
            .flat_map(|s| &s.checks)
            .find(|c| c.id == e.id)
            .cloned()
            .unwrap_or_default();
        let ok = c.status == "ok";
        card(SharedString::from(format!("setup-card-{}", e.id)))
            .p_4()
            .child(
                h_flex()
                    .gap(px(14.))
                    .items_start()
                    .child(
                        div()
                            .flex_none()
                            .mt_0p5()
                            .size_7()
                            .rounded_full()
                            .flex()
                            .items_center()
                            .justify_center()
                            .border_1()
                            .border_color(theme::line())
                            .bg(theme::surface())
                            .child(step_icon(&c.status, 20.)),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .child(title(e.label.clone()))
                                    .children(step_badge(&c)),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::UI)
                                    .text_color(theme::muted())
                                    .child(e.description.clone()),
                            )
                            .when(!c.detail.is_empty(), |el| {
                                el.child(div().mt_1().child(note_in(
                                    c.detail.clone(),
                                    if ok {
                                        theme::emerald(300).opacity(0.8)
                                    } else {
                                        theme::subtle()
                                    },
                                )))
                            })
                            .when(!c.fix.is_empty(), |el| {
                                el.child(div().pt_3().child(self.command_box(&c.id, &c.fix, cx)))
                            }),
                    ),
            )
            .into_any_element()
    }

    /// setup_entry draws the Base image group's two rows.
    pub(super) fn setup_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        match e.id {
            "opencode-image" => {
                let setup = self.settings_page.accounts.setup.as_ref();
                let p = &self.settings_page.setup;
                let wanted = setup.is_some_and(|s| s.image.components.opencode);
                let installed = setup.is_some_and(|s| s.image.installed.opencode);
                let mut row = self.more_row(e, Some(t("defaults.newAgent.openCodeDetails")), cx);
                row.control = Some(toggle(
                    "switch-opencode-image",
                    wanted,
                    p.building || p.opencode_job.is_some() || setup.is_none(),
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.build_image(
                            BuildImageRequest {
                                opencode: Some(!wanted),
                                ..Default::default()
                            },
                            true,
                            cx,
                        )
                    }),
                ));
                if wanted && !installed && p.opencode_job.is_none() {
                    row.body.push(note_in(
                        t("defaults.newAgent.openCodeMissing"),
                        theme::amber(300),
                    ));
                }
                if let Some(j) = &p.opencode_job {
                    row.body.push(self.job_progress(j));
                }
                Some(row.render())
            }
            "opencode-v2" => Some(
                self.switch_row(
                    e,
                    "settings.openCodeV2.details",
                    |s| s.open_code_version == "2",
                    |v| UpdateSettingsRequest {
                        open_code_version: Some(if v { "2" } else { "1" }.into()),
                        ..Default::default()
                    },
                    cx,
                )
                .render(),
            ),
            _ => None,
        }
    }
}
