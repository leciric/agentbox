//! A project's scheduled tasks (SchedulesPanel.tsx, ScheduleDialog.tsx,
//! lib/schedules.ts), beside its lead's chat or picked on an agent's switch:
//! each with when it runs next, a switch to pause it, Run now, Edit, its runs
//! and Delete; under them every run, newest first, each opening the agent it
//! went to, with Rerun. The dialog makes or edits one (ScheduleRequest): once,
//! days of the week, every N, cron, or on a GitHub event; to the chat or an
//! agent; with a precheck and a grace period. GET/POST/PATCH/DELETE
//! /v1/projects/{p}/schedules, …/{id}/run, …/schedules/runs.
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{Schedule, ScheduleRequest, ScheduleRun};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputState, Textarea, TextareaState};
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn, Choice};
use super::settings::connectors::time_until;
use super::settings::defaults::time_ago;
use super::work::{Tone, tag};
use crate::theme;
use crate::toasts::{self, Kind, Toast};

/// SetForm changes the dialog's form from a pick.
type SetForm = Rc<dyn Fn(&mut AppView, &str)>;

#[derive(Clone, Copy, PartialEq)]
enum Mode {
    Once,
    Weekly,
    Every,
    Cron,
    Event,
}

const MODES: [(Mode, &str); 5] = [
    (Mode::Once, "once"),
    (Mode::Weekly, "weekly"),
    (Mode::Every, "every"),
    (Mode::Cron, "cron"),
    (Mode::Event, "event"),
];

const EVENTS: [&str; 4] = [
    "ci_failed",
    "review_requested",
    "issue_labeled",
    "pr_opened",
];

/// A template to start from (lib/schedules.ts's templates).
struct Template {
    id: &'static str,
    cron: &'static str,
    every: i64,
    event: &'static str,
    label: &'static str,
    target: &'static str,
    precheck: &'static str,
}

const TEMPLATES: [Template; 6] = [
    Template {
        id: "audit",
        cron: "0 9 * * 1-5",
        every: 0,
        event: "",
        label: "",
        target: "agent",
        precheck: "",
    },
    Template {
        id: "release",
        cron: "0 10 * * 5",
        every: 0,
        event: "",
        label: "",
        target: "lead",
        precheck: "",
    },
    Template {
        id: "review",
        cron: "0 18 * * *",
        every: 0,
        event: "",
        label: "",
        target: "lead",
        precheck: "",
    },
    Template {
        id: "ci",
        cron: "",
        every: 60,
        event: "",
        label: "",
        target: "lead",
        precheck: "gh pr list --state open --limit 1 | grep -q .",
    },
    Template {
        id: "fixmain",
        cron: "",
        every: 0,
        event: "ci_failed",
        label: "",
        target: "agent",
        precheck: "",
    },
    Template {
        id: "bugs",
        cron: "",
        every: 0,
        event: "issue_labeled",
        label: "bug",
        target: "agent",
        precheck: "",
    },
];

/// The dialog's form (ScheduleDialog's Form).
struct Form {
    /// The schedule edited, or none for a new one.
    id: Option<String>,
    mode: Mode,
    days: Vec<u32>,
    unit: char,
    event: String,
    target: String,
    session: String,
}

pub struct Schedules {
    project: String,
    list: Option<Result<Vec<Schedule>, String>>,
    runs: Vec<ScheduleRun>,
    /// Whose runs are listed under it.
    open: Option<String>,
    running: Option<String>,
    deleting: Option<Schedule>,
    form: Option<Form>,
    saving: bool,
    error: Option<String>,
    inputs: Vec<(&'static str, Entity<InputState>)>,
    task: Entity<TextareaState>,
}

/// The dialog's text boxes, by name.
const FIELDS: [(&str, &str); 11] = [
    ("title", "schedules.field.titlePlaceholder"),
    ("task", ""),
    ("at", ""),
    ("time", ""),
    ("every", ""),
    ("cron", ""),
    ("label", ""),
    ("timezone", ""),
    ("model", "schedules.field.defaultPlaceholder"),
    ("effort", "schedules.field.defaultPlaceholder"),
    ("precheck", ""),
];

impl Schedules {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let mut inputs: Vec<(&'static str, Entity<InputState>)> = FIELDS
            .iter()
            .map(|(name, ph)| {
                let ph = if ph.is_empty() { String::new() } else { t(ph) };
                (
                    *name,
                    cx.new(|cx| InputState::new(window, cx).placeholder(ph)),
                )
            })
            .collect();
        inputs.push(("grace", cx.new(|cx| InputState::new(window, cx))));
        Self {
            project: String::new(),
            list: None,
            runs: Vec::new(),
            open: None,
            running: None,
            deleting: None,
            form: None,
            saving: false,
            error: None,
            inputs,
            task: cx.new(|cx| TextareaState::new(window, cx).auto_grow(3, 8)),
        }
    }

    fn input(&self, name: &str) -> &Entity<InputState> {
        &self.inputs.iter().find(|(n, _)| *n == name).unwrap().1
    }

    fn value(&self, name: &str, cx: &App) -> String {
        if name == "task" {
            return self.task.read(cx).value().trim().to_string();
        }
        self.input(name).read(cx).value().trim().to_string()
    }

    pub fn dialog_open(&self) -> bool {
        self.form.is_some() || self.deleting.is_some()
    }
}

/// parse_weekly is lib/schedules.ts's parseWeekly: a cron that runs at one
/// time on some days of the week, as (days, hour, minute).
fn parse_weekly(cron: &str) -> Option<(Vec<u32>, u32, u32)> {
    let text = if cron.trim().eq_ignore_ascii_case("@daily") {
        "0 0 * * *"
    } else {
        cron.trim()
    };
    let f: Vec<&str> = text.split_whitespace().collect();
    if f.len() != 5 || f[2] != "*" || f[3] != "*" {
        return None;
    }
    let minute: u32 = f[0].parse().ok().filter(|m| *m <= 59)?;
    let hour: u32 = f[1].parse().ok().filter(|h| *h <= 23)?;
    let mut days = Vec::new();
    if f[4] == "*" {
        days = (0..7).collect();
    } else {
        for part in f[4].split(',') {
            let (range, step) = match part.split_once('/') {
                Some((r, s)) => (r, s.parse::<u32>().ok().filter(|s| *s > 0)?),
                None => (part, 1),
            };
            let (from, to) = if range == "*" {
                (0, 7)
            } else if let Some((a, b)) = range.split_once('-') {
                (a.parse().ok()?, b.parse().ok()?)
            } else {
                let a: u32 = range.parse().ok()?;
                (a, if part.contains('/') { 7 } else { a })
            };
            if from > to || to > 7 {
                return None;
            }
            let mut d = from;
            while d <= to {
                days.push(d % 7);
                d += step;
            }
        }
    }
    days.sort();
    days.dedup();
    (!days.is_empty()).then_some((days, hour, minute))
}

/// weekly_cron is lib/schedules.ts's weeklyCron.
fn weekly_cron(days: &[u32], hour: u32, minute: u32) -> String {
    let mut sorted: Vec<u32> = days.to_vec();
    sorted.sort();
    sorted.dedup();
    let mut dow = "*".to_string();
    if !sorted.is_empty() && sorted.len() < 7 {
        let mut parts = Vec::new();
        let mut i = 0;
        while i < sorted.len() {
            let mut j = i;
            while j + 1 < sorted.len() && sorted[j + 1] == sorted[j] + 1 {
                j += 1;
            }
            if j - i >= 2 {
                parts.push(format!("{}-{}", sorted[i], sorted[j]));
            } else {
                parts.extend(sorted[i..=j].iter().map(|d| d.to_string()));
            }
            i = j + 1;
        }
        dow = parts.join(",");
    }
    format!("{minute} {hour} * * {dow}")
}

fn every_label(minutes: i64) -> String {
    if minutes > 0 && minutes % 1440 == 0 {
        tv("schedules.every.days", &[("n", (minutes / 1440).into())])
    } else if minutes > 0 && minutes % 60 == 0 {
        tv("schedules.every.hours", &[("n", (minutes / 60).into())])
    } else {
        tv("schedules.every.minutes", &[("n", minutes.into())])
    }
}

fn day_name(d: u32, long: bool) -> String {
    crate::dates::weekday(d, long)
}

/// local_time is an RFC 3339 time as the user's own date and time.
fn local_time(iso: &str) -> String {
    crate::dates::date_time_of(iso)
}

/// when_label is lib/schedules.ts's whenLabel.
fn when_label(s: &Schedule) -> String {
    match s.kind.as_str() {
        "event" => {
            if EVENTS.contains(&s.event.as_str()) {
                tv(
                    &format!("schedules.when.event.{}", s.event),
                    &[("label", (&s.event_label).into())],
                )
            } else {
                s.event.clone()
            }
        }
        "once" => tv(
            "schedules.when.once",
            &[(
                "at",
                s.at.as_deref().map(local_time).unwrap_or_default().into(),
            )],
        ),
        "every" => every_label(s.every_minutes),
        _ => match parse_weekly(&s.cron) {
            None => tv("schedules.when.cron", &[("cron", (&s.cron).into())]),
            Some((days, h, m)) => {
                let time = format!("{h:02}:{m:02}");
                if days.len() == 7 {
                    tv("schedules.when.daily", &[("time", time.into())])
                } else if days == [1, 2, 3, 4, 5] {
                    tv("schedules.when.weekdays", &[("time", time.into())])
                } else {
                    let names: Vec<String> =
                        days.iter().map(|d| day_name(*d, days.len() == 1)).collect();
                    tv(
                        "schedules.when.days",
                        &[("days", names.join(", ").into()), ("time", time.into())],
                    )
                }
            }
        },
    }
}

fn outcome_label(run: &ScheduleRun) -> (String, bool) {
    let bad = run.outcome == "failed" || run.outcome == "skipped";
    let label = match run.outcome.as_str() {
        "sent" => t("schedules.run.sent"),
        "agent" if !run.agent.is_empty() => {
            tv("schedules.run.agent", &[("agent", (&run.agent).into())])
        }
        "agent" => t("schedules.run.agentPending"),
        "failed" => t("schedules.run.failed"),
        "skipped" => t("schedules.run.skipped"),
        _ => t("schedules.run.running"),
    };
    (format!("{label} · {}", time_ago(&run.ran_at)), bad)
}

impl AppView {
    pub(super) fn load_schedules(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.schedules.project != project {
            self.schedules.project = project.clone();
            self.schedules.list = None;
            self.schedules.runs.clear();
        }
        let client = self.client.clone();
        let enc = crate::files::encode(&project);
        cx.spawn(async move |this, cx| {
            let list = client
                .get::<Vec<Schedule>>(&format!("/v1/projects/{enc}/schedules"))
                .await
                .map_err(|e| e.to_string());
            let runs = client
                .get::<Vec<ScheduleRun>>(&format!("/v1/projects/{enc}/schedules/runs"))
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                if this.schedules.project == project {
                    this.schedules.list = Some(list);
                    this.schedules.runs = runs;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    fn schedule_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<serde_json::Value>,
        done: impl FnOnce(&mut AppView, Result<Vec<u8>, String>, &mut Context<AppView>) + 'static,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        let base = format!(
            "/v1/projects/{}/schedules",
            crate::files::encode(&self.schedules.project)
        );
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    method,
                    &format!("{base}{path}"),
                    body.map(|b| serde_json::to_vec(&b).unwrap_or_default()),
                )
                .await
                .map(|b| b.to_vec())
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                done(this, res, cx);
                this.load_schedules(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn toggle_schedule(&mut self, s: &Schedule, cx: &mut Context<Self>) {
        let body = serde_json::json!({ "paused": !s.paused });
        self.schedule_call(
            Method::PATCH,
            format!("/{}", crate::files::encode(&s.id)),
            Some(body),
            |_, res, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e);
                }
            },
            cx,
        );
    }

    fn run_schedule(&mut self, id: String, title: String, cx: &mut Context<Self>) {
        self.schedules.running = Some(id.clone());
        self.schedule_call(
            Method::POST,
            format!("/{}/run", crate::files::encode(&id)),
            None,
            move |this, res, cx| {
                this.schedules.running = None;
                match res.and_then(|b| {
                    serde_json::from_slice::<ScheduleRun>(&b).map_err(|e| e.to_string())
                }) {
                    Ok(run) if run.outcome == "failed" || run.outcome == "skipped" => {
                        let why = if run.error.is_empty() {
                            run.outcome.clone()
                        } else {
                            run.error.clone()
                        };
                        toasts::error(cx, format!("{title}: {why}"));
                    }
                    Ok(_) => Toast::new(
                        Kind::Success,
                        tv("schedules.ran", &[("title", (&title).into())]),
                    )
                    .show(cx),
                    Err(e) => toasts::error(cx, e),
                }
            },
            cx,
        );
    }

    fn delete_schedule(&mut self, cx: &mut Context<Self>) {
        let Some(s) = self.schedules.deleting.take() else {
            return;
        };
        self.schedule_call(
            Method::DELETE,
            format!("/{}", crate::files::encode(&s.id)),
            None,
            |_, res, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e);
                }
            },
            cx,
        );
    }

    fn set_field(
        &mut self,
        name: &str,
        value: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if name == "task" {
            let task = self.schedules.task.clone();
            task.update(cx, |s, cx| s.set_value(value, window, cx));
            return;
        }
        let input = self.schedules.input(name).clone();
        input.update(cx, |s, cx| s.set_value(value, window, cx));
    }

    /// open_schedule_dialog opens the dialog on a schedule, a template, or nothing.
    fn open_schedule_dialog(
        &mut self,
        schedule: Option<Schedule>,
        template: Option<usize>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let in_an_hour = (chrono::Local::now() + chrono::Duration::hours(1))
            .format("%Y-%m-%d %H:00")
            .to_string();
        let tz = std::env::var("TZ")
            .ok()
            .filter(|z| !z.is_empty())
            .unwrap_or_else(|| {
                std::fs::read_to_string("/etc/timezone")
                    .map(|s| s.trim().to_string())
                    .unwrap_or_else(|_| "UTC".into())
            });
        let mut form = Form {
            id: None,
            mode: Mode::Once,
            days: vec![1, 2, 3, 4, 5],
            unit: 'h',
            event: "ci_failed".into(),
            target: "lead".into(),
            session: "fresh".into(),
        };
        let mut v: Vec<(&str, String)> = vec![
            ("title", String::new()),
            ("task", String::new()),
            ("at", in_an_hour),
            ("time", "09:00".into()),
            ("every", "1".into()),
            ("cron", "0 9 * * 1-5".into()),
            ("label", String::new()),
            ("timezone", tz),
            ("model", String::new()),
            ("effort", String::new()),
            ("precheck", String::new()),
            ("grace", String::new()),
        ];
        let mut set = |name: &str, value: String| {
            if let Some(x) = v.iter_mut().find(|(n, _)| *n == name) {
                x.1 = value;
            }
        };
        let timing = |form: &mut Form,
                      set: &mut dyn FnMut(&str, String),
                      cron: &str,
                      every: i64,
                      event: &str,
                      label: &str| {
            if !event.is_empty() {
                form.mode = Mode::Event;
                form.event = event.into();
                set("label", label.into());
            } else if !cron.is_empty() {
                set("cron", cron.into());
                match parse_weekly(cron) {
                    Some((days, h, m)) => {
                        form.mode = Mode::Weekly;
                        form.days = days;
                        set("time", format!("{h:02}:{m:02}"));
                    }
                    None => form.mode = Mode::Cron,
                }
            } else {
                form.mode = Mode::Every;
                let m = if every > 0 { every } else { 30 };
                let (n, u) = if m % 1440 == 0 {
                    (m / 1440, 'd')
                } else if m % 60 == 0 {
                    (m / 60, 'h')
                } else {
                    (m, 'm')
                };
                form.unit = u;
                set("every", n.to_string());
            }
        };
        if let Some(i) = template {
            let tpl = &TEMPLATES[i];
            timing(
                &mut form, &mut set, tpl.cron, tpl.every, tpl.event, tpl.label,
            );
            set("title", t(&format!("schedules.template.{}.title", tpl.id)));
            set("task", t(&format!("schedules.template.{}.task", tpl.id)));
            set("precheck", tpl.precheck.into());
            form.target = tpl.target.into();
        } else if let Some(s) = &schedule {
            form.id = Some(s.id.clone());
            set("title", s.title.clone());
            set("task", s.task.clone());
            if !s.timezone.is_empty() {
                set("timezone", s.timezone.clone());
            }
            form.target = if s.target == "agent" { "agent" } else { "lead" }.into();
            form.session = if s.session == "reuse" {
                "reuse"
            } else {
                "fresh"
            }
            .into();
            set("model", s.model.clone());
            set("effort", s.effort.clone());
            set("precheck", s.precheck.clone());
            if s.grace_minutes > 0 {
                set("grace", s.grace_minutes.to_string());
            }
            match s.kind.as_str() {
                "once" => {
                    form.mode = Mode::Once;
                    if let Some(at) = &s.at {
                        set("at", local_time(at));
                    }
                }
                "every" => timing(&mut form, &mut set, "", s.every_minutes, "", ""),
                "event" => timing(&mut form, &mut set, "", 0, &s.event, &s.event_label),
                _ => timing(&mut form, &mut set, &s.cron, 0, "", ""),
            }
        }
        for (name, value) in v {
            self.set_field(name, value, window, cx);
        }
        self.schedules.form = Some(form);
        self.schedules.error = None;
        cx.notify();
    }

    /// schedule_request is the form as the daemon takes it (timingOf).
    fn schedule_request(&self, cx: &App) -> Result<ScheduleRequest, String> {
        let s = &self.schedules;
        let f = s.form.as_ref().ok_or_else(String::new)?;
        let v = |n: &str| s.value(n, cx);
        let opt = |n: &str| Some(v(n));
        let mut r = ScheduleRequest {
            title: opt("title"),
            task: opt("task"),
            timezone: opt("timezone"),
            target: Some(f.target.clone()),
            session: (f.target == "agent").then(|| f.session.clone()),
            model: opt("model"),
            effort: opt("effort"),
            precheck: opt("precheck"),
            grace_minutes: Some(v("grace").parse().unwrap_or(0)),
            ..Default::default()
        };
        match f.mode {
            Mode::Once => {
                let at = chrono::NaiveDateTime::parse_from_str(&v("at"), "%Y-%m-%d %H:%M")
                    .map_err(|_| format!("{}: {}", t("schedules.field.at"), v("at")))?;
                let local = at
                    .and_local_timezone(chrono::Local)
                    .single()
                    .ok_or_else(|| v("at"))?;
                r.at = Some(
                    local
                        .with_timezone(&chrono::Utc)
                        .to_rfc3339_opts(chrono::SecondsFormat::Secs, true),
                );
            }
            Mode::Every => {
                r.every = Some(format!(
                    "{}{}",
                    v("every").parse::<i64>().unwrap_or(1).max(1),
                    f.unit
                ))
            }
            Mode::Weekly => {
                let time = v("time");
                let (h, m) = time.split_once(':').unwrap_or(("0", "0"));
                r.cron = Some(weekly_cron(
                    &f.days,
                    h.parse().unwrap_or(0),
                    m.parse().unwrap_or(0),
                ));
            }
            Mode::Cron => r.cron = Some(v("cron")),
            Mode::Event => {
                r.on = Some(f.event.clone());
                if f.event == "issue_labeled" {
                    r.label = Some(v("label"));
                }
            }
        }
        Ok(r)
    }

    fn save_schedule(&mut self, cx: &mut Context<Self>) {
        let req = match self.schedule_request(cx) {
            Ok(r) => r,
            Err(e) => {
                self.schedules.error = Some(e);
                cx.notify();
                return;
            }
        };
        let id = self.schedules.form.as_ref().and_then(|f| f.id.clone());
        self.schedules.saving = true;
        let (method, path) = match &id {
            Some(id) => (Method::PATCH, format!("/{}", crate::files::encode(id))),
            None => (Method::POST, String::new()),
        };
        let body = serde_json::to_value(&req).ok();
        self.schedule_call(
            method,
            path,
            body,
            |this, res, cx| {
                this.schedules.saving = false;
                match res
                    .and_then(|b| serde_json::from_slice::<Schedule>(&b).map_err(|e| e.to_string()))
                {
                    Ok(s) => {
                        this.schedules.form = None;
                        Toast::new(
                            Kind::Success,
                            tv("schedules.saved", &[("title", (&s.title).into())]),
                        )
                        .show(cx);
                    }
                    Err(e) => this.schedules.error = Some(e),
                }
            },
            cx,
        );
        cx.notify();
    }

    /// render_schedules is SchedulesPanel under its title.
    pub(super) fn render_schedules(&self, cx: &mut Context<Self>) -> AnyElement {
        let project = self
            .projects
            .iter()
            .find(|p| Some(&p.name) == self.project.as_ref())
            .map(|p| {
                if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                }
            })
            .unwrap_or_default();
        let title = h_flex()
            .flex_none()
            .h(px(32.))
            .px_3()
            .gap_1p5()
            .border_b_1()
            .border_color(theme::line())
            .text_xs()
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::muted())
            .child(crate::icons::of("⏰"))
            .child(t("panel.view.schedules"))
            .child(
                div()
                    .truncate()
                    .font_weight(FontWeight::NORMAL)
                    .text_color(theme::faint())
                    .child(format!("· {project}")),
            );
        let s = &self.schedules;
        let mut body = v_flex().gap_4().p_3();
        body = body.child(
            h_flex()
                .items_start()
                .gap_3()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .text_size(theme::text::UI)
                        .line_height(relative(1.625))
                        .text_color(theme::subtle())
                        .child(t("schedules.description")),
                )
                .child(controls::button(
                    "schedule-new",
                    format!("+ {}", t("schedules.new")),
                    Btn::Primary,
                    false,
                    cx.listener(|this, _: &ClickEvent, w, cx| {
                        this.open_schedule_dialog(None, None, w, cx)
                    }),
                )),
        );
        match &s.list {
            None => body = body.child(super::panel::loading("schedules")),
            Some(Err(e)) => body = body.child(controls::error_line(e.clone())),
            Some(Ok(list)) if list.is_empty() => {
                body = body.child(
                    v_flex()
                        .gap_3()
                        .px_4()
                        .py_5()
                        .rounded_xl()
                        .border_1()
                        .border_dashed()
                        .border_color(theme::line())
                        .child(
                            h_flex()
                                .gap_2()
                                .text_size(theme::text::BODY)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(
                                    crate::icons::ic("alarm-clock")
                                        .size_4()
                                        .text_color(theme::brand(300)),
                                )
                                .child(t("schedules.empty.title")),
                        )
                        .child(
                            div()
                                .text_size(theme::text::UI)
                                .text_color(theme::subtle())
                                .child(t("schedules.empty.body")),
                        )
                        .child(h_flex().flex_wrap().gap_2().children(
                            TEMPLATES.iter().enumerate().map(|(i, tpl)| {
                                controls::button(
                                    SharedString::from(format!("schedule-template-{}", tpl.id)),
                                    t(&format!("schedules.template.{}.title", tpl.id)),
                                    Btn::Secondary,
                                    false,
                                    cx.listener(move |this, _: &ClickEvent, w, cx| {
                                        this.open_schedule_dialog(None, Some(i), w, cx)
                                    }),
                                )
                            }),
                        )),
                );
            }
            Some(Ok(list)) => {
                body = body.child(v_flex().min_w_0().gap_2().children(
                    list.iter().enumerate().map(|(i, sc)| {
                        crate::motion::enter(
                            div().min_w_0().child(self.schedule_row(sc, cx)),
                            SharedString::from(format!("schedule-enter-{}", sc.id)),
                            i,
                        )
                    }),
                ));
            }
        }
        if !s.runs.is_empty() {
            body = body.child(
                v_flex()
                    .gap_2()
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::muted())
                            .child(t("schedules.history.recent")),
                    )
                    .child(self.run_list(s.runs.iter().take(20).cloned().collect(), true, cx)),
            );
        }
        v_flex()
            .size_full()
            .child(title)
            .child(
                div()
                    .id("schedules-scroll")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .child(body),
            )
            .into_any_element()
    }

    fn schedule_row(&self, s: &Schedule, cx: &mut Context<Self>) -> AnyElement {
        let mine: Vec<ScheduleRun> = self
            .schedules
            .runs
            .iter()
            .filter(|r| r.schedule == s.id)
            .cloned()
            .collect();
        let open = self.schedules.open.as_deref() == Some(s.id.as_str());
        let running = self.schedules.running.as_deref() == Some(s.id.as_str());
        let target = if s.target == "agent" {
            tag(
                format!(
                    "◎ {}",
                    t(if s.session == "reuse" {
                        "schedules.target.reuse"
                    } else {
                        "schedules.target.agent"
                    })
                ),
                Tone::Brand,
            )
        } else {
            tag(format!("▭ {}", t("schedules.target.lead")), Tone::Plain)
        };
        let next: AnyElement = if s.paused {
            div()
                .text_color(theme::amber(200))
                .child(t("schedules.paused"))
                .into_any_element()
        } else if s.kind == "event" {
            div()
                .child(crate::icons::lead("zap", t("schedules.watching")))
                .into_any_element()
        } else if let Some(n) = &s.next_run {
            let tip: SharedString = local_time(n).into();
            div()
                .id(SharedString::from(format!("schedule-next-{}", s.id)))
                .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                .child(tv("schedules.next", &[("when", time_until(n).into())]))
                .into_any_element()
        } else {
            div().child(t("schedules.noNext")).into_any_element()
        };
        let (s1, s2, s3, s4) = (s.clone(), s.clone(), s.clone(), s.clone());
        let id = s.id.clone();
        v_flex()
            .id(SharedString::from(format!("schedule-{}", s.id)))
            .min_w_0()
            .px_3p5()
            .py_3()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface())
            .when(s.paused, |el| el.opacity(0.7))
            .child(
                h_flex()
                    .items_start()
                    .gap_3()
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .min_w_0()
                                    .flex_wrap()
                                    .gap_1p5()
                                    .child(
                                        div()
                                            .min_w_0()
                                            .truncate()
                                            .text_size(theme::text::BODY)
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::primary())
                                            .child(s.title.clone()),
                                    )
                                    .child(target)
                                    .when(!s.precheck.is_empty(), |el| {
                                        el.child(tag(
                                            format!("✓ {}", t("schedules.precheckBadge")),
                                            Tone::Plain,
                                        ))
                                    })
                                    .when(s.created_by == "lead", |el| {
                                        el.child(
                                            div()
                                                .text_size(theme::text::CAPTION)
                                                .text_color(theme::faint())
                                                .child(t("schedules.byLead")),
                                        )
                                    }),
                            )
                            .child(super::controls::full_on_hover(
                                div()
                                    .text_size(theme::text::UI)
                                    .text_color(theme::tertiary())
                                    .line_clamp(2)
                                    .text_ellipsis()
                                    .child(s.task.clone()),
                                SharedString::from(format!("schedule-task-{}", s.id)),
                                s.task.clone(),
                            ))
                            .child(
                                h_flex()
                                    .flex_wrap()
                                    .gap_x_2()
                                    .mt_1()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::subtle())
                                    .child(when_label(s))
                                    .child("·")
                                    .child(next)
                                    .when_some(s.last_run.clone(), |el, run| {
                                        let (label, bad) = outcome_label(&run);
                                        el.child("·").child(
                                            div()
                                                .when(bad, |el| el.text_color(theme::rose(300)))
                                                .child(label),
                                        )
                                    }),
                            ),
                    )
                    .child(controls::toggle(
                        SharedString::from(format!("schedule-toggle-{}", s.id)),
                        !s.paused,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.toggle_schedule(&s1, cx)
                        }),
                    )),
            )
            .child(
                h_flex()
                    .mt_2()
                    .gap_1()
                    .flex_wrap()
                    .items_center()
                    .child(controls::button(
                        SharedString::from(format!("schedule-run-{}", s.id)),
                        format!(
                            "{} {}",
                            if running { "…" } else { "▶" },
                            t("schedules.runNow")
                        ),
                        Btn::Ghost,
                        self.schedules.running.is_some(),
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.run_schedule(s2.id.clone(), s2.title.clone(), cx)
                        }),
                    ))
                    .child(controls::button(
                        SharedString::from(format!("schedule-edit-{}", s.id)),
                        format!("✎ {}", t("schedules.edit")),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, w, cx| {
                            this.open_schedule_dialog(Some(s3.clone()), None, w, cx)
                        }),
                    ))
                    .when(!mine.is_empty(), |el| {
                        el.child(controls::button(
                            SharedString::from(format!("schedule-history-{}", s.id)),
                            format!(
                                "↺ {}",
                                tv("schedules.history.count", &[("count", s.runs.into())])
                            ),
                            Btn::Ghost,
                            false,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.schedules.open =
                                    if this.schedules.open.as_deref() == Some(id.as_str()) {
                                        None
                                    } else {
                                        Some(id.clone())
                                    };
                                cx.notify();
                            }),
                        ))
                    })
                    .child(div().flex_1())
                    .child(controls::button(
                        SharedString::from(format!("schedule-delete-{}", s.id)),
                        t("schedules.delete"),
                        Btn::DangerQuiet,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.schedules.deleting = Some(s4.clone());
                            cx.notify();
                        }),
                    )),
            )
            .when(open, |el| {
                el.child(div().mt_2().child(self.run_list(mine, false, cx)))
            })
            .into_any_element()
    }

    fn run_list(&self, runs: Vec<ScheduleRun>, titled: bool, cx: &mut Context<Self>) -> AnyElement {
        if runs.is_empty() {
            return div()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(t("schedules.history.empty"))
                .into_any_element();
        }
        let project = self.schedules.project.clone();
        let known: Vec<String> = match &self.schedules.list {
            Some(Ok(l)) => l.iter().map(|s| s.id.clone()).collect(),
            _ => Vec::new(),
        };
        v_flex()
            .gap_1()
            .children(runs.into_iter().map(|run| {
                let (label, bad) = outcome_label(&run);
                let agent = (!run.agent.is_empty()).then(|| run.agent.clone());
                let r = format!("{project}/{}", run.agent);
                let (sid, title) = (run.schedule.clone(), run.title.clone());
                let rerun = known.contains(&sid);
                h_flex()
                    .id(SharedString::from(format!("schedule-run-row-{}", run.id)))
                    .flex_wrap()
                    .gap_x_2()
                    .gap_y_1()
                    .px_2p5()
                    .py_1p5()
                    .min_w_0()
                    .items_center()
                    .rounded_lg()
                    .bg(theme::sunken())
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::tertiary())
                    .when(titled, |el| {
                        el.child(
                            div()
                                .min_w_0()
                                .truncate()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::secondary())
                                .child(run.title.clone()),
                        )
                    })
                    .child(
                        div()
                            .when(bad, |el| el.text_color(theme::rose(300)))
                            .child(if bad { format!("⚠ {label}") } else { label }),
                    )
                    .when(run.trigger == "manual", |el| {
                        el.child(tag(t("schedules.run.manual"), Tone::Plain))
                    })
                    .when(run.late, |el| {
                        let mut l = t("schedules.run.late");
                        if run.missed > 0 {
                            l = format!(
                                "{l} · {}",
                                tv("schedules.run.missed", &[("count", run.missed.into())])
                            );
                        }
                        el.child(tag(l, Tone::Warning))
                    })
                    .when(!run.event.is_empty(), |el| {
                        el.child(
                            div()
                                .w_full()
                                .min_w_0()
                                .text_color(theme::subtle())
                                .child(crate::icons::lead("zap", run.event.to_string())),
                        )
                    })
                    .child(div().flex_1())
                    .when_some(agent, |el, a| {
                        el.child(small_ghost(
                            SharedString::from(format!("run-open-{}", run.id)),
                            format!("◎ {a}"),
                            false,
                            cx.listener(move |this, _: &ClickEvent, w, cx| {
                                this.open_agent_ref(r.clone(), w, cx)
                            }),
                        ))
                    })
                    .when(rerun, |el| {
                        el.child(small_ghost(
                            SharedString::from(format!("run-rerun-{}", run.id)),
                            format!("↻ {}", t("schedules.rerun")),
                            self.schedules.running.is_some(),
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.run_schedule(sid.clone(), title.clone(), cx)
                            }),
                        ))
                    })
            }))
            .into_any_element()
    }

    /// render_schedule_dialogs are the schedule's dialog and Delete's.
    pub(super) fn render_schedule_dialogs(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let view = cx.entity().downgrade();
        let cancel: super::agent::OnCancel = Rc::new(move |_, cx| {
            view.update(cx, |this, cx| {
                this.schedules.form = None;
                this.schedules.deleting = None;
                cx.notify();
            })
            .ok();
        });
        if let Some(s) = &self.schedules.deleting {
            return Some(super::agent::modal(
                "schedule-delete",
                tv("schedules.deleteTitle", &[("title", (&s.title).into())]),
                Some(t("schedules.deleteBody")),
                Vec::new(),
                cancel,
                t("schedules.delete"),
                true,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| this.delete_schedule(cx)),
            ));
        }
        let f = self.schedules.form.as_ref()?;
        let s = &self.schedules;
        let field = |label: String, name: &str, hint: Option<String>| -> AnyElement {
            v_flex()
                .gap_1p5()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(label),
                )
                .map(|el| {
                    if name == "task" {
                        el.child(Textarea::new(&s.task))
                    } else {
                        el.child(Input::new(s.input(name)))
                    }
                })
                .when_some(hint, |el, h| {
                    el.child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child(h),
                    )
                })
                .into_any_element()
        };
        let pick = |id: &str, f: SetForm| -> controls::Pick<str> {
            let view = cx.entity().downgrade();
            let _ = id;
            Rc::new(move |v: &str, _, cx| {
                let f = f.clone();
                let v = v.to_string();
                view.update(cx, |this, cx| {
                    f(this, &v);
                    cx.notify();
                })
                .ok();
            })
        };
        let modes = controls::segmented(
            "schedule-mode",
            MODES
                .iter()
                .map(|(_, k)| (SharedString::from(*k), t(&format!("schedules.mode.{k}"))))
                .collect(),
            MODES.iter().find(|(m, _)| *m == f.mode).map(|(_, k)| *k),
            false,
            pick(
                "mode",
                Rc::new(|this, v| {
                    if let (Some(f), Some((m, _))) = (
                        this.schedules.form.as_mut(),
                        MODES.iter().find(|(_, k)| *k == v),
                    ) {
                        f.mode = *m;
                    }
                }),
            ),
        );
        let timing: AnyElement = match f.mode {
            Mode::Once => field(
                t("schedules.field.at"),
                "at",
                Some("YYYY-MM-DD HH:MM".into()),
            ),
            Mode::Weekly => v_flex()
                .gap_2()
                .child(
                    v_flex()
                        .gap_1p5()
                        .child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::muted())
                                .child(t("schedules.field.days")),
                        )
                        .child(h_flex().gap_1().children(
                            [1u32, 2, 3, 4, 5, 6, 0].into_iter().map(|d| {
                                let on = f.days.contains(&d);
                                div()
                                    .id(SharedString::from(format!("schedule-day-{d}")))
                                    .h(px(32.))
                                    .min_w(px(40.))
                                    .px_2()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .rounded_md()
                                    .text_size(theme::text::CAPTION)
                                    .border_1()
                                    .when(on, |el| {
                                        el.border_color(theme::brand(400).opacity(0.5))
                                            .bg(theme::brand(500).opacity(0.15))
                                            .text_color(theme::brand(200))
                                    })
                                    .when(!on, |el| {
                                        el.border_color(theme::line())
                                            .text_color(theme::subtle())
                                            .hover(|s| s.text_color(theme::primary()))
                                            .active(|s| s.opacity(0.7))
                                    })
                                    .cursor_pointer()
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        if let Some(f) = this.schedules.form.as_mut() {
                                            if f.days.contains(&d) {
                                                f.days.retain(|x| *x != d);
                                            } else {
                                                f.days.push(d);
                                            }
                                        }
                                        cx.notify();
                                    }))
                                    .child(day_name(d, false))
                            }),
                        )),
                )
                .child(field(t("schedules.field.time"), "time", None))
                .into_any_element(),
            Mode::Every => v_flex()
                .gap_1p5()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t("schedules.field.every")),
                )
                .child(
                    h_flex()
                        .gap_2()
                        .child(div().w(px(96.)).child(Input::new(s.input("every"))))
                        .child(controls::segmented(
                            "schedule-unit",
                            [
                                ("m", "schedules.unit.minutes"),
                                ("h", "schedules.unit.hours"),
                                ("d", "schedules.unit.days"),
                            ]
                            .iter()
                            .map(|(v, k)| (SharedString::from(*v), t(k)))
                            .collect(),
                            Some(match f.unit {
                                'm' => "m",
                                'd' => "d",
                                _ => "h",
                            }),
                            false,
                            pick(
                                "unit",
                                Rc::new(|this, v| {
                                    if let Some(f) = this.schedules.form.as_mut() {
                                        f.unit = v.chars().next().unwrap_or('h');
                                    }
                                }),
                            ),
                        )),
                )
                .into_any_element(),
            Mode::Cron => field(
                t("schedules.field.cron"),
                "cron",
                Some(t("schedules.field.cronHint")),
            ),
            Mode::Event => v_flex()
                .gap_2()
                .child(
                    v_flex()
                        .gap_1p5()
                        .child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::muted())
                                .child(t("schedules.field.event")),
                        )
                        .child(controls::select(
                            "schedule-event",
                            EVENTS
                                .iter()
                                .map(|e| Choice::new(*e, t(&format!("schedules.event.{e}"))))
                                .collect(),
                            &f.event,
                            false,
                            pick(
                                "event",
                                Rc::new(|this, v| {
                                    if let Some(f) = this.schedules.form.as_mut() {
                                        f.event = v.to_string();
                                    }
                                }),
                            ),
                        )),
                )
                .when(f.event == "issue_labeled", |el| {
                    el.child(field(t("schedules.field.label"), "label", None))
                })
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::subtle())
                        .child(t("schedules.field.eventHint")),
                )
                .into_any_element(),
        };
        let target = v_flex()
            .gap_1p5()
            .child(
                div()
                    .text_size(theme::text::CAPTION)
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::muted())
                    .child(t("schedules.field.target")),
            )
            .child(controls::segmented(
                "schedule-target",
                vec![
                    ("lead".into(), t("schedules.target.lead")),
                    ("agent".into(), t("schedules.target.agent")),
                ],
                Some(f.target.as_str()),
                false,
                pick(
                    "target",
                    Rc::new(|this, v| {
                        if let Some(f) = this.schedules.form.as_mut() {
                            f.target = v.to_string();
                        }
                    }),
                ),
            ));
        let session = (f.target == "agent").then(|| {
            v_flex()
                .gap_1p5()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t("schedules.field.session")),
                )
                .child(controls::segmented(
                    "schedule-session",
                    vec![
                        ("fresh".into(), t("schedules.session.fresh")),
                        ("reuse".into(), t("schedules.session.reuse")),
                    ],
                    Some(f.session.as_str()),
                    false,
                    pick(
                        "session",
                        Rc::new(|this, v| {
                            if let Some(f) = this.schedules.form.as_mut() {
                                f.session = v.to_string();
                            }
                        }),
                    ),
                ))
                .into_any_element()
        });
        let templates =
            f.id.is_none().then(|| {
                v_flex()
                    .gap_1p5()
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::muted())
                            .child(t("schedules.dialog.templates")),
                    )
                    .child(h_flex().flex_wrap().gap_1p5().children(
                        TEMPLATES.iter().enumerate().map(|(i, tpl)| {
                            div()
                                .id(SharedString::from(format!(
                                    "schedule-dialog-template-{}",
                                    tpl.id
                                )))
                                .px_2p5()
                                .py_1()
                                .rounded_full()
                                .border_1()
                                .border_color(theme::line())
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::secondary())
                                .cursor_pointer()
                                .hover(|s| {
                                    s.border_color(theme::line_strong())
                                        .bg(theme::surface_raised())
                                })
                                .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                                    this.open_schedule_dialog(None, Some(i), w, cx)
                                }))
                                .child(t(&format!("schedules.template.{}.title", tpl.id)))
                        }),
                    ))
                    .into_any_element()
            });
        let mut body: Vec<AnyElement> = Vec::new();
        body.extend(templates);
        body.push(field(t("schedules.field.title"), "title", None));
        body.push(field(
            t("schedules.field.task"),
            "task",
            Some(t("schedules.field.taskHint")),
        ));
        body.push(
            v_flex()
                .gap_2()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t("schedules.field.when")),
                )
                .child(modes)
                .into_any_element(),
        );
        body.push(timing);
        if f.mode != Mode::Event {
            body.push(field(t("schedules.field.timezone"), "timezone", None));
        }
        body.push(target.into_any_element());
        body.extend(session);
        if f.target == "agent" {
            body.push(
                h_flex()
                    .gap_2()
                    .child(
                        div()
                            .flex_1()
                            .child(field(t("schedules.field.model"), "model", None)),
                    )
                    .child(
                        div()
                            .flex_1()
                            .child(field(t("schedules.field.effort"), "effort", None)),
                    )
                    .into_any_element(),
            );
        }
        body.push(field(
            t("schedules.field.precheck"),
            "precheck",
            Some(t("schedules.field.precheckHint")),
        ));
        if f.mode != Mode::Event {
            body.push(field(
                t("schedules.field.grace"),
                "grace",
                Some(t("schedules.field.graceHint")),
            ));
        }
        if let Some(e) = &s.error {
            body.push(controls::error_line(e.clone()));
        }
        let scroll = vec![
            div()
                .id("schedule-dialog-scroll")
                .max_h(px(520.))
                .overflow_y_scroll()
                .child(v_flex().gap_4().pr_1().children(body))
                .into_any_element(),
        ];
        Some(super::agent::modal(
            "schedule-dialog",
            t(if f.id.is_some() {
                "schedules.dialog.edit"
            } else {
                "schedules.dialog.new"
            }),
            Some(t("schedules.dialog.description")),
            scroll,
            cancel,
            t(if f.id.is_some() {
                "schedules.save"
            } else {
                "schedules.create"
            }),
            false,
            s.saving,
            cx.listener(|this, _: &ClickEvent, _, cx| this.save_schedule(cx)),
        ))
    }
}

/// small_ghost is a run row's ghost Button squeezed to h-6 px-2 text-caption.
fn small_ghost(
    id: impl Into<ElementId>,
    label: impl Into<SharedString>,
    disabled: bool,
    on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .h(px(24.))
        .px_2()
        .flex()
        .items_center()
        .rounded_lg()
        .text_size(theme::text::CAPTION)
        .font_weight(FontWeight::MEDIUM)
        .text_color(theme::muted())
        .when(disabled, |el| el.opacity(0.45))
        .when(!disabled, |el| {
            el.cursor_pointer()
                .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                .active(|s| s.bg(theme::surface_strong()))
                .on_click(on_click)
        })
        .child(crate::icons::glyph(label))
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::{parse_weekly, weekly_cron};

    #[test]
    fn weekly_crons_go_both_ways() {
        assert_eq!(
            parse_weekly("0 9 * * 1-5"),
            Some((vec![1, 2, 3, 4, 5], 9, 0))
        );
        assert_eq!(
            parse_weekly("30 18 * * *"),
            Some(((0..7).collect(), 18, 30))
        );
        assert_eq!(parse_weekly("0 9 1 * *"), None);
        assert_eq!(weekly_cron(&[1, 2, 3, 4, 5], 9, 0), "0 9 * * 1-5");
        assert_eq!(weekly_cron(&[0, 6], 8, 15), "15 8 * * 0,6");
        assert_eq!(weekly_cron(&[0, 1, 2, 3, 4, 5, 6], 7, 0), "0 7 * * *");
    }
}
