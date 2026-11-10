//! What the app tells you while you look elsewhere (renderer App.tsx's
//! toastNotice, lib/useSessionAlerts.ts, lib/useReadyAlerts.ts), the tray
//! (main/tray.ts) and the window's place (main/windowstate.ts):
//!
//! - a question passed to you (the daemon's `notification` event), an AI tool
//!   asking for a permission, an agent stopping on an error and a chat
//!   session needing you, failing or finishing: a toast in the window, the
//!   chime, and an OS notification while the window isn't the one in front,
//!   as Settings → Notifications and Do not disturb allow; clicking either
//!   goes where it points. An agent newly ready to review gets the chime and
//!   the OS notification only (the sidebar marks it).
//! - the launcher icon's count of what waits on you across the sessions, or
//!   the window asking for attention where there's no count to set.
//! - the tray icon, its counts and menu, read again a second after the event
//!   stream says something it shows moved, and every minute besides. With
//!   "Keep running when the window is closed" closing the window minimises it
//!   instead (GPUI can't hide one), so its notices still come.
//! - the window's size and place, kept for the next launch.
//!
//! Like the rest, no AgentBox logic: what it reads and calls is the API's.
use std::collections::{HashMap, HashSet};
use std::time::{Duration, Instant};

use agentbox_api::{
    Agent, LeadSession, Notification, NotificationPage, Project, ReviewItem, ReviewPage,
    SeeNotificationsRequest, SeeNotificationsResult,
};
use agentbox_i18n::{t, tv};
use gpui::*;
use serde_json::json;

use super::AppView;
use super::agent_status::{Tone as AgentTone, chat_label};
use super::sessions;
use crate::toasts::{Kind, Toast};
use crate::tray::{self, Action, Target, Tray};
use crate::{notices, prefs};

/// Go is where a notice points.
#[derive(Clone, Debug)]
pub enum Go {
    To(Target),
    /// A project's chat, where a question or a permission is answered.
    ProjectChat(String),
    /// One of a project's chat sessions.
    Session(String, String),
}

/// POLL is how often the window takes the tray's and the notifications'
/// clicks; SETTLE how long after an event the tray reads again.
const POLL: Duration = Duration::from_millis(150);
const SETTLE: Duration = Duration::from_secs(1);
const TRAY_EVERY: Duration = Duration::from_secs(60);
const PAGE: &str = "50";
/// READY_BATCH is how close together finishes are told as one.
const READY_BATCH: Duration = Duration::from_millis(1500);

pub struct Alerts {
    tray: Option<Tray>,
    status: tray::Status,
    tray_due: Option<Instant>,
    tray_read: Instant,
    reading: bool,
    /// Whether the window is the one in front.
    active: bool,
    /// Where each OS notification shown goes, by its id.
    pending: HashMap<String, Go>,
    /// Each agent's chat state, as last seen (None before the first list).
    chats: Option<HashMap<String, String>>,
    errored: Option<HashSet<String>>,
    /// The finishes already known (ref:fingerprint): the first list is the
    /// baseline, not news.
    ready: Option<HashSet<String>>,
    /// What landed in Ready to review in the last moment, told together
    /// once nothing more arrives for READY_BATCH (reviewAlert.ts batch).
    ready_batch: Vec<ReviewItem>,
    _ready_timer: Option<Task<()>>,
    badge: i64,
    /// The window's bounds, saved half a second after they last changed.
    bounds_due: Option<Instant>,
    _subs: Vec<Subscription>,
    _poll: Option<Task<()>>,
}

impl Default for Alerts {
    fn default() -> Self {
        Self {
            tray: None,
            status: tray::OFFLINE,
            tray_due: Some(Instant::now()),
            tray_read: Instant::now(),
            reading: false,
            active: true,
            pending: HashMap::new(),
            chats: None,
            errored: None,
            ready: None,
            ready_batch: Vec::new(),
            _ready_timer: None,
            badge: 0,
            bounds_due: None,
            _subs: Vec::new(),
            _poll: None,
        }
    }
}

/// moves_tray is whether an event can change what the tray shows.
pub fn moves_tray(ty: &str) -> bool {
    matches!(
        ty,
        "agent"
            | "notification"
            | "notification.seen"
            | "question"
            | "review"
            | "pulls"
            | "project"
    )
}

/// notice_text is a question's: its title names the agent and what it did,
/// and its body the text and where it's from (noticeText).
pub fn notice_text(n: &Notification, project: &str) -> (String, String) {
    let verb = t(if n.kind == "error" {
        "shell.notice.verb.error"
    } else {
        "shell.notice.verb.question"
    });
    let from: Vec<&str> = [project, n.title.as_str()]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect();
    let body: Vec<String> = [n.text.clone(), from.join(" · ")]
        .into_iter()
        .filter(|s| !s.is_empty())
        .collect();
    (format!("{} {verb}", n.agent), body.join("\n"))
}

/// SessionAlert is sessionAlerts': what changed in a project's sessions.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum SessionAlert {
    NeedsYou,
    Failed,
    Done,
}

impl SessionAlert {
    fn key(self) -> &'static str {
        match self {
            Self::NeedsYou => "needsYou",
            Self::Failed => "failed",
            Self::Done => "done",
        }
    }

    /// kind is the notification settings' kind it goes by.
    fn kind(self) -> &'static str {
        match self {
            Self::NeedsYou => "question",
            Self::Failed => "error",
            Self::Done => "session",
        }
    }
}

fn needs_you(s: &LeadSession) -> i64 {
    if sessions::status(s) == "needsYou" {
        s.needs_you.max(1)
    } else {
        0
    }
}

/// session_alert is what one session's change says, if anything: a
/// permission request now waiting in it (a question notifies by itself), its
/// chat stopping, or its lead's turn ending with none of its agents working.
pub fn session_alert(prev: &LeadSession, next: &LeadSession) -> Option<SessionAlert> {
    let asks = |s: &LeadSession| needs_you(s) - s.questions;
    let (was, now) = (sessions::status(prev), sessions::status(next));
    if asks(next) > asks(prev) {
        Some(SessionAlert::NeedsYou)
    } else if now == "failed" && was != "failed" {
        Some(SessionAlert::Failed)
    } else if was == "working" && now == "idle" && next.agents_working == 0 {
        Some(SessionAlert::Done)
    } else {
        None
    }
}

/// newly_ready is detectNewlyReady: the finishes not known yet, and known
/// grown by the list (never pruned, so one that drops off and comes back
/// with the same finish isn't announced twice).
pub fn newly_ready<'a>(
    known: &mut Option<HashSet<String>>,
    items: &'a [ReviewItem],
) -> Vec<&'a ReviewItem> {
    let key = |i: &ReviewItem| format!("{}:{}", i.r#ref, i.fingerprint);
    let fresh = match known {
        Some(k) => items.iter().filter(|i| !k.contains(&key(i))).collect(),
        None => Vec::new(),
    };
    known
        .get_or_insert_with(HashSet::new)
        .extend(items.iter().map(key));
    fresh
}

fn millis() -> i64 {
    chrono::Local::now().timestamp_millis()
}

impl AppView {
    /// start_alerts puts the icon in the tray, restores nothing (main.rs
    /// placed the window) but saves the window's place as it changes, and
    /// starts the loop that takes the clicks.
    pub(super) fn start_alerts(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if std::env::var_os("AGENTBOX_NATIVE_NO_TRAY").is_none() {
            self.alerts.tray = Tray::new();
        }
        self.draw_tray();
        self.alerts.active = window.is_window_active();
        let activation = cx.observe_window_activation(window, |this, window, cx| {
            this.alerts.active = window.is_window_active();
            if this.alerts.active {
                // Back in front: what the toasts said is on screen.
                cx.notify();
            }
        });
        let bounds = cx.observe_window_bounds(window, |this, _, _| {
            this.alerts.bounds_due = Some(Instant::now() + Duration::from_millis(500));
        });
        self.alerts._subs = vec![activation, bounds];
        // Closing the window quits (quit.rs), or minimises it when it's
        // kept running in the tray. The close button is the window
        // manager's (WM_DELETE_WINDOW, xdg_toplevel close), or ours where
        // the compositor draws none (topbar.rs, window_controls).
        let has_tray = self.alerts.tray.is_some();
        window.on_window_should_close(cx, move |window, cx| {
            !close_window(has_tray, "the window was closed", window, cx)
        });
        self.alerts._poll = Some(cx.spawn_in(window, async move |this, cx| {
            loop {
                cx.background_executor().timer(POLL).await;
                if this
                    .update_in(cx, |this, window, cx| this.poll_alerts(window, cx))
                    .is_err()
                {
                    return;
                }
            }
        }));
    }

    fn poll_alerts(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let now = Instant::now();
        for id in notices::take_clicks() {
            if let Some(go) = self.alerts.pending.remove(&id) {
                self.go(go, window, cx);
            }
        }
        if self.alerts.tray.is_some() {
            let (chosen, clicked) = tray::events();
            if clicked {
                window.activate_window();
            }
            for id in chosen {
                let action = self.alerts.tray.as_ref().and_then(|t| t.action(&id));
                if let Some(a) = action {
                    self.tray_act(a, window, cx);
                }
            }
            if now.duration_since(self.alerts.tray_read) >= TRAY_EVERY {
                self.alerts.tray_due = Some(now);
            }
            if self.alerts.tray_due.is_some_and(|d| d <= now) && !self.alerts.reading {
                self.alerts.tray_due = None;
                self.refresh_tray(cx);
            }
        }
        if self.alerts.bounds_due.is_some_and(|d| d <= now) {
            self.alerts.bounds_due = None;
            save_bounds(window);
        }
        // The launcher's count: what waits on you in every session read.
        let badge: i64 = self.sessions.lists.values().flatten().map(needs_you).sum();
        if badge != self.alerts.badge {
            if badge > self.alerts.badge && !self.alerts.active && cfg!(not(target_os = "linux")) {
                window.request_attention();
            }
            self.alerts.badge = badge;
            notices::set_badge(badge);
        }
    }

    /// tray_changed redraws the tray's menu after Settings changed its switch.
    pub(super) fn tray_changed(&mut self) {
        self.draw_tray();
    }

    /// tray_soon reads the tray's counts again a moment after the last event.
    pub(super) fn tray_soon(&mut self) {
        self.alerts.tray_due = Some(Instant::now() + SETTLE);
    }

    fn draw_tray(&mut self) {
        let keep = prefs::tray_keep_running();
        let status = self.alerts.status.clone();
        if let Some(tray) = &mut self.alerts.tray {
            tray.draw(&status, keep);
        }
    }

    fn refresh_tray(&mut self, cx: &mut Context<Self>) {
        self.alerts.reading = true;
        self.alerts.tray_read = Instant::now();
        let client = self.client.clone();
        let connected = self.connected;
        cx.spawn(async move |this, cx| {
            let data = if connected {
                let unseen = format!("/v1/notifications?limit={PAGE}&unseen=1");
                let review = format!("/v1/review?limit={PAGE}");
                let (agents, projects, questions, review) = tokio::join!(
                    client.get::<Vec<Agent>>("/v1/agents"),
                    client.get::<Vec<Project>>("/v1/projects"),
                    client.get::<NotificationPage>(&unseen),
                    client.get::<ReviewPage>(&review),
                );
                match (agents, projects, questions, review) {
                    (Ok(agents), Ok(projects), Ok(questions), Ok(review)) => Some(tray::Data {
                        agents,
                        projects,
                        questions,
                        review: Some(review),
                    }),
                    _ => None,
                }
            } else {
                None
            };
            this.update(cx, |this, _| {
                this.alerts.reading = false;
                this.alerts.status = tray::status(data.as_ref());
                this.draw_tray();
            })
            .ok();
        })
        .detach();
    }

    fn tray_act(&mut self, action: Action, window: &mut Window, cx: &mut Context<Self>) {
        match action {
            Action::Open => window.activate_window(),
            Action::Go(target) => self.go(Go::To(target), window, cx),
            Action::PauseAll => self.pause_all(cx),
            Action::KeepRunning => {
                prefs::set_bool(prefs::TRAY_KEEP_RUNNING, !prefs::tray_keep_running());
                self.draw_tray();
                cx.notify();
            }
            Action::Quit => crate::quit::quit(cx, "the tray's Quit"),
        }
        self.alerts.tray_due = Some(Instant::now());
    }

    /// pause_all pauses every running agent, as each one's Pause does.
    fn pause_all(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res: anyhow::Result<()> = async {
                let agents = client.get::<Vec<Agent>>("/v1/agents").await?;
                let mut failed = Vec::new();
                for a in agents.iter().filter(|a| a.state == "running") {
                    let path = format!("/v1/agents/{}/pause", a.r#ref);
                    if let Err(e) = client.post::<_, serde_json::Value>(&path, &json!({})).await {
                        failed.push(e.to_string());
                    }
                }
                anyhow::ensure!(failed.is_empty(), failed.join("\n"));
                Ok(())
            }
            .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    Toast::new(Kind::Error, t("main.tray.failed"))
                        .description(e.to_string())
                        .show(cx);
                }
                this.tray_soon();
            })
            .ok();
        })
        .detach();
    }

    /// go brings the window forward and opens where a notice points.
    pub(super) fn go(&mut self, go: Go, window: &mut Window, cx: &mut Context<Self>) {
        window.activate_window();
        self.review = None;
        self.new_task = None;
        self.settings = false;
        self.work = None;
        match go {
            Go::To(Target::Agent(r)) => self.open_agent_ref(r, window, cx),
            Go::To(Target::Url(u)) => cx.open_url(&u),
            Go::To(Target::Question(n)) => {
                self.see_notice(&n.id, cx);
                self.go_project_chat(n.project, window, cx);
            }
            Go::To(Target::Project(p)) | Go::ProjectChat(p) => self.go_project_chat(p, window, cx),
            Go::Session(p, id) => self.select_session(p, id, window, cx),
        }
        cx.notify();
    }

    fn go_project_chat(&mut self, project: String, window: &mut Window, cx: &mut Context<Self>) {
        if self.project.as_deref() != Some(project.as_str()) {
            self.select_project(project, window, cx);
        } else {
            self.open_chat("lead".into(), window, cx);
        }
    }

    /// see_notice marks a question seen (POST /v1/notifications/seen), which
    /// the daemon tells every window.
    fn see_notice(&mut self, id: &str, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let req = SeeNotificationsRequest {
            ids: vec![id.to_string()],
            ..Default::default()
        };
        cx.spawn(async move |_, _| {
            let _ = client
                .post::<_, SeeNotificationsResult>("/v1/notifications/seen", &req)
                .await;
        })
        .detach();
    }

    fn project_label(&self, project: &str) -> String {
        self.projects
            .iter()
            .find(|p| p.name == project)
            .map(|p| p.display_name.clone())
            .filter(|l| !l.is_empty())
            .unwrap_or_else(|| project.to_string())
    }

    /// notice says something: a toast (unless toast is false), the chime and
    /// the OS notification while the window isn't in front, as kind allows.
    #[allow(clippy::too_many_arguments)]
    fn notice(
        &mut self,
        kind: &str,
        id: String,
        title: String,
        body: String,
        go: Go,
        toast: Option<String>,
        cx: &mut Context<Self>,
    ) {
        if let Some(label) = toast {
            let view = cx.entity().downgrade();
            let to = go.clone();
            Toast::new(Kind::Info, title.clone())
                .description(body.replace('\n', " · "))
                .action(label, move |window, cx| {
                    view.update(cx, |this, cx| this.go(to.clone(), window, cx))
                        .ok();
                })
                .show(cx);
        }
        if prefs::may_notify(kind, "sound") {
            notices::chime(prefs::review_volume());
        }
        if prefs::may_notify(kind, "system") && !self.alerts.active {
            self.alerts.pending.insert(id.clone(), go);
            notices::show(id, title, body);
        }
    }

    /// on_notification is the daemon's `notification` event: a question that
    /// reached you. A kept screenshot or recording is one too, for the Media
    /// views' marks only.
    pub(super) fn on_notification(&mut self, n: Notification, cx: &mut Context<Self>) {
        self.tray_soon();
        if n.kind == agentbox_api::NOTIFY_MEDIA || n.seen {
            return;
        }
        let (title, body) = notice_text(&n, &self.project_label(&n.project));
        let id = n.id.clone();
        self.notice(
            "question",
            id,
            title,
            body,
            Go::To(Target::Question(Box::new(n))),
            Some(t("shell.app.view")),
            cx,
        );
    }

    /// shown_agent is whether ref's chat is the one on screen.
    fn shown_agent(&self, r#ref: &str) -> bool {
        self.alerts.active
            && !self.settings
            && self
                .project
                .as_deref()
                .is_some_and(|p| r#ref == format!("{p}/{}", self.open))
    }

    /// check_agents tells you when an agent's AI tool starts waiting for a
    /// permission, or an agent stops on an error, while you look elsewhere.
    pub(super) fn check_agents(&mut self, cx: &mut Context<Self>) {
        let agents = self.side.agents.clone();
        let chats: HashMap<String, String> = agents
            .iter()
            .map(|a| (a.r#ref.clone(), a.chat.clone()))
            .collect();
        let in_review: HashSet<&str> = self.side.review.iter().map(|r| r.r#ref.as_str()).collect();
        let errored: HashSet<String> = agents
            .iter()
            .filter(|a| {
                chat_label(*a).1 == AgentTone::Error && !in_review.contains(a.r#ref.as_str())
            })
            .map(|a| a.r#ref.clone())
            .collect();
        let before_chats = self.alerts.chats.replace(chats);
        let before_errored = self.alerts.errored.replace(errored.clone());
        let ms = millis();
        if let Some(before) = before_chats {
            for a in &agents {
                if a.chat != "waiting"
                    || before.get(&a.r#ref).is_some_and(|c| c == "waiting")
                    || self.shown_agent(&a.r#ref)
                {
                    continue;
                }
                let n = Notification {
                    id: format!("waiting:{}:{ms}", a.r#ref),
                    kind: "question".into(),
                    project: a.project.clone(),
                    agent: a.name.clone(),
                    r#ref: a.r#ref.clone(),
                    title: a.title.clone(),
                    text: tv(
                        "shell.app.asksPermission",
                        &[("ai", super::agent::ai_label(&a.ai).into())],
                    ),
                    ..Default::default()
                };
                let (title, body) = notice_text(&n, &self.project_label(&n.project));
                // Answered from the project's chat, where its card is.
                let go = Go::ProjectChat(a.project.clone());
                self.notice(
                    "question",
                    n.id,
                    title,
                    body,
                    go,
                    Some(t("shell.app.view")),
                    cx,
                );
            }
        }
        if let Some(before) = before_errored {
            for a in agents.iter().filter(|a| errored.contains(&a.r#ref)) {
                if before.contains(&a.r#ref) || self.shown_agent(&a.r#ref) {
                    continue;
                }
                let n = Notification {
                    id: format!("error:{}:{ms}", a.r#ref),
                    kind: "error".into(),
                    project: a.project.clone(),
                    agent: a.name.clone(),
                    r#ref: a.r#ref.clone(),
                    title: a.title.clone(),
                    text: chat_label(a).0,
                    ..Default::default()
                };
                let (title, body) = notice_text(&n, &self.project_label(&n.project));
                let go = Go::To(Target::Agent(a.r#ref.clone()));
                self.notice(
                    "error",
                    n.id,
                    title,
                    body,
                    go,
                    Some(t("shell.app.view")),
                    cx,
                );
            }
        }
    }

    /// check_ready tells you when agents newly land in Ready to review: the
    /// chime and one OS notification for all that arrive within 1.5 s of
    /// each other.
    pub(super) fn check_ready(&mut self, cx: &mut Context<Self>) {
        let items = self.side.review.clone();
        let fresh = newly_ready(&mut self.alerts.ready, &items);
        if fresh.is_empty() {
            return;
        }
        self.alerts.ready_batch.extend(fresh.into_iter().cloned());
        self.alerts._ready_timer = Some(cx.spawn(async move |this, cx| {
            cx.background_executor().timer(READY_BATCH).await;
            this.update(cx, |this, cx| this.tell_ready(cx)).ok();
        }));
    }

    fn tell_ready(&mut self, cx: &mut Context<Self>) {
        let fresh = std::mem::take(&mut self.alerts.ready_batch);
        let Some(first) = fresh.first() else {
            return;
        };
        let body = if fresh.len() == 1 {
            let agent = if first.title.is_empty() {
                &first.agent
            } else {
                &first.title
            };
            tv(
                "review.notice.one",
                &[
                    ("agent", agent.as_str().into()),
                    ("project", self.project_label(&first.project).into()),
                ],
            )
        } else {
            tv("review.notice.many", &[("count", fresh.len().into())])
        };
        let id = format!("ready:{}:{}", first.r#ref, millis());
        let go = Go::To(Target::Agent(first.r#ref.clone()));
        self.notice("review", id, t("review.notice.title"), body, go, None, cx);
    }

    /// check_sessions tells you about a chat session you aren't looking at,
    /// from where a project's sessions stood before (prev) and stand now.
    pub(super) fn check_sessions(
        &mut self,
        project: &str,
        prev: &[LeadSession],
        next: &[LeadSession],
        cx: &mut Context<Self>,
    ) {
        let shown = self.alerts.active
            && self.project.as_deref() == Some(project)
            && self.open == "lead"
            && !self.settings;
        let current = self.sessions.current(project).map(|s| s.id.clone());
        let label = self.project_label(project);
        for s in next {
            let Some(p) = prev.iter().find(|p| p.id == s.id) else {
                continue;
            };
            let Some(alert) = session_alert(p, s) else {
                continue;
            };
            if shown && current.as_deref() == Some(s.id.as_str()) {
                continue;
            }
            let vars = [
                ("topic", sessions::topic(s).into()),
                ("project", label.as_str().into()),
            ];
            let title = tv(&format!("sessions.notice.{}.title", alert.key()), &vars);
            let body = tv(&format!("sessions.notice.{}.body", alert.key()), &vars);
            let id = format!("session:{}:{}:{}", s.r#ref, alert.key(), millis());
            let go = Go::Session(project.to_string(), s.id.clone());
            self.notice(
                alert.kind(),
                id,
                title,
                body,
                go,
                Some(t("sessions.notice.open")),
                cx,
            );
        }
    }
}

impl AppView {
    /// has_tray is whether the tray's icon is up (it can't be on a desktop
    /// without one).
    pub(super) fn has_tray(&self) -> bool {
        self.alerts.tray.is_some()
    }
}

/// close_window minimises the window when the tray keeps the app running,
/// and quits otherwise, saving where the window was either way. It says
/// whether the window stays.
pub(super) fn close_window(has_tray: bool, why: &str, window: &mut Window, cx: &mut App) -> bool {
    save_bounds(window);
    if has_tray && prefs::tray_keep_running() {
        window.minimize_window();
        return true;
    }
    crate::quit::quit(cx, why);
    false
}

/// save_bounds keeps the window's place for the next launch: a maximised or
/// full-screen window's is where it goes back to.
fn save_bounds(window: &Window) {
    let (b, maximized) = match window.window_bounds() {
        WindowBounds::Windowed(b) => (b, false),
        WindowBounds::Maximized(b) | WindowBounds::Fullscreen(b) => (b, true),
    };
    let value = json!({
        "x": f32::from(b.origin.x),
        "y": f32::from(b.origin.y),
        "width": f32::from(b.size.width),
        "height": f32::from(b.size.height),
        "maximized": maximized,
    });
    if prefs::get(prefs::WINDOW).as_ref() != Some(&value) {
        prefs::set(prefs::WINDOW, value);
    }
}

/// Rect is a window's or a display's place, in logical pixels.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

/// MIN is the smallest the window can be (windowbounds.ts' minSize), and
/// DEFAULT a new window's size.
pub const MIN: (f32, f32) = (480., 480.);
const DEFAULT: (f32, f32) = (1440., 900.);

fn overlap(a: Rect, b: Rect) -> f32 {
    let w = (a.x + a.w).min(b.x + b.w) - a.x.max(b.x);
    let h = (a.y + a.h).min(b.y + b.h) - a.y.max(b.y);
    if w > 0. && h > 0. { w * h } else { 0. }
}

/// place is placeWindow: last launch's place fitted to the displays there
/// are now (on the one most of it is on, or the nearest, shrunk and slid
/// until it's all on it), or a new window centred on the first display.
pub fn place(saved: Option<Rect>, displays: &[Rect]) -> Option<Rect> {
    let first = *displays.first()?;
    let Some(s) = saved.filter(|s| s.w > 0. && s.h > 0.) else {
        let w = DEFAULT.0.min(first.w * 0.92).max(MIN.0);
        let h = DEFAULT.1.min(first.h * 0.92).max(MIN.1);
        let (w, h) = (w.round(), h.round());
        let x = (first.x + (first.w - w) / 2.).round();
        let y = (first.y + (first.h - h) / 2.).round();
        return Some(Rect { x, y, w, h });
    };
    let centre = |r: Rect| (r.x + r.w / 2., r.y + r.h / 2.);
    let (cx, cy) = centre(s);
    let area = displays
        .iter()
        .copied()
        .max_by(|a, b| {
            let (oa, ob) = (overlap(s, *a), overlap(s, *b));
            if oa != ob {
                return oa.total_cmp(&ob);
            }
            let d = |r: Rect| {
                let (x, y) = centre(r);
                -((x - cx).powi(2) + (y - cy).powi(2))
            };
            d(*a).total_cmp(&d(*b))
        })
        .unwrap_or(first);
    let w = s.w.min(area.w).max(MIN.0.min(s.w));
    let h = s.h.min(area.h).max(MIN.1.min(s.h));
    let x = s.x.max(area.x).min((area.x + area.w - w).max(area.x));
    let y = s.y.max(area.y).min((area.y + area.h - h).max(area.y));
    Some(Rect {
        x: x.round(),
        y: y.round(),
        w: w.round(),
        h: h.round(),
    })
}

/// initial_bounds is where main.rs opens the window.
pub fn initial_bounds(cx: &App) -> WindowBounds {
    // AGENTBOX_NATIVE_SIZE=1440x900 opens the window that size at the top
    // left (native/scripts/parity.sh, which compares it with Electron's).
    if let Some((w, h)) = std::env::var("AGENTBOX_NATIVE_SIZE")
        .ok()
        .and_then(|s| {
            s.split_once('x')
                .map(|(w, h)| (w.parse::<f32>(), h.parse::<f32>()))
        })
        .and_then(|(w, h)| Some((w.ok()?, h.ok()?)))
    {
        return WindowBounds::Windowed(Bounds::new(point(px(0.), px(0.)), size(px(w), px(h))));
    }
    let saved = prefs::get(prefs::WINDOW);
    let num = |k: &str| {
        saved
            .as_ref()
            .and_then(|v| v.get(k))
            .and_then(|v| v.as_f64())
            .map(|v| v as f32)
    };
    let rect = match (num("x"), num("y"), num("width"), num("height")) {
        (Some(x), Some(y), Some(w), Some(h)) => Some(Rect { x, y, w, h }),
        _ => None,
    };
    let maximized = saved
        .as_ref()
        .and_then(|v| v.get("maximized"))
        .and_then(|v| v.as_bool())
        .unwrap_or(false);
    let mut displays: Vec<Rect> = Vec::new();
    if let Some(p) = cx.primary_display() {
        displays.push(rect_of(p.visible_bounds()));
    }
    for d in cx.displays() {
        let r = rect_of(d.visible_bounds());
        if !displays.contains(&r) {
            displays.push(r);
        }
    }
    let bounds = match place(rect, &displays) {
        Some(r) => Bounds::new(point(px(r.x), px(r.y)), size(px(r.w), px(r.h))),
        None => Bounds::centered(None, size(px(DEFAULT.0), px(DEFAULT.1)), cx),
    };
    if maximized {
        WindowBounds::Maximized(bounds)
    } else {
        WindowBounds::Windowed(bounds)
    }
}

fn rect_of(b: Bounds<Pixels>) -> Rect {
    Rect {
        x: f32::from(b.origin.x),
        y: f32::from(b.origin.y),
        w: f32::from(b.size.width),
        h: f32::from(b.size.height),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn session(id: &str, status: &str, needs: i64) -> LeadSession {
        LeadSession {
            id: id.into(),
            status: status.into(),
            needs_you: needs,
            ..Default::default()
        }
    }

    #[::core::prelude::v1::test]
    fn sessions_say_what_changed() {
        let a = |p, n| session_alert(&p, &n);
        assert_eq!(
            a(session("s", "working", 0), session("s", "needsYou", 1)),
            Some(SessionAlert::NeedsYou)
        );
        assert_eq!(
            a(session("s", "working", 0), session("s", "failed", 0)),
            Some(SessionAlert::Failed)
        );
        assert_eq!(
            a(session("s", "working", 0), session("s", "idle", 0)),
            Some(SessionAlert::Done)
        );
        let busy = LeadSession {
            agents_working: 2,
            ..session("s", "idle", 0)
        };
        assert_eq!(a(session("s", "working", 0), busy), None);
        // A question passed on to you notifies by itself.
        let asked = LeadSession {
            questions: 1,
            ..session("s", "needsYou", 1)
        };
        assert_eq!(a(session("s", "working", 0), asked), None);
    }

    #[::core::prelude::v1::test]
    fn the_first_review_list_is_the_baseline() {
        let item = |r: &str, f: &str| ReviewItem {
            r#ref: r.into(),
            fingerprint: f.into(),
            ..Default::default()
        };
        let mut known = None;
        assert!(newly_ready(&mut known, &[item("d/a", "1")]).is_empty());
        let list = [item("d/a", "1"), item("d/b", "1"), item("d/a", "2")];
        let fresh: Vec<_> = newly_ready(&mut known, &list)
            .iter()
            .map(|i| i.fingerprint.clone() + &i.r#ref)
            .collect();
        assert_eq!(fresh, ["1d/b", "2d/a"]);
        assert!(newly_ready(&mut known, &list).is_empty());
    }

    #[::core::prelude::v1::test]
    fn a_notice_names_the_agent_and_where_its_from() {
        let n = Notification {
            agent: "agent-02".into(),
            kind: "question".into(),
            title: "Fix login".into(),
            text: "Which port?".into(),
            ..Default::default()
        };
        let (title, body) = notice_text(&n, "Demo");
        assert_eq!(title, "agent-02 asks you");
        assert_eq!(body, "Which port?\nDemo · Fix login");
    }

    #[::core::prelude::v1::test]
    fn the_window_comes_back_on_a_display() {
        let screen = Rect {
            x: 0.,
            y: 0.,
            w: 1440.,
            h: 900.,
        };
        // A new window: centred, a little smaller than the screen.
        let new = place(None, &[screen]).unwrap();
        assert_eq!(
            new,
            Rect {
                x: 58.,
                y: 36.,
                w: 1325.,
                h: 828.
            }
        );
        // One left on a display that's gone slides back, shrunk to fit.
        let off = Rect {
            x: 3000.,
            y: 200.,
            w: 2000.,
            h: 800.,
        };
        assert_eq!(
            place(Some(off), &[screen]),
            Some(Rect {
                x: 0.,
                y: 100.,
                w: 1440.,
                h: 800.
            })
        );
        let kept = Rect {
            x: 100.,
            y: 50.,
            w: 800.,
            h: 600.,
        };
        assert_eq!(place(Some(kept), &[screen]), Some(kept));
        assert_eq!(place(Some(kept), &[]), None);
    }
}
