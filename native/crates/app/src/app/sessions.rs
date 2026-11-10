//! A project's chat sessions (ProjectChatPanel.tsx's SessionChatPanel,
//! chat/SessionTabs.tsx, SessionHeader.tsx, SessionHistory.tsx and
//! lib/sessions.ts): one tab per open session above the lead's chat, each
//! with where it stands (what waits on you, an error, working, unread), a
//! new one, closing, renaming and pinning; the strip under them with the
//! session's agents and Snooze; the snoozed ones, woken from the tabs or the
//! sidebar; History, the closed ones to reopen or continue; the toast when
//! sessions settle by themselves. The session shown is the one chosen last
//! (kept between runs), else the most recently active.
use std::collections::{HashMap, HashSet};

use agentbox_api::{
    LeadSession, LeadSessionFork, LeadSessionPatch, LeadSessionSnooze, NewLeadSessionRequest,
    SeeLeadSessionRequest, SessionAgent, SessionOutput,
};
use agentbox_i18n::{t, tv};
use chrono::{Datelike as _, Duration as Days, Local, TimeZone as _, Timelike as _};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::menu::{ContextMenuExt as _, DropdownMenu as _, PopupMenuItem};
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::settings::defaults::time_ago;
use crate::toasts::{self, Kind, Toast};
use crate::{prefs, theme};

const CHOSEN: &str = "sessions.current";

pub struct Sessions {
    /// Home's tab: 0 its chat, 1 Status (every project's agents), 2 Memory.
    pub home_tab: u8,
    pub lists: HashMap<String, Vec<LeadSession>>,
    closed: HashMap<String, Vec<LeadSession>>,
    chosen: HashMap<String, String>,
    /// When this app last showed each session, by its ref (ms).
    seen: HashMap<String, i64>,
    pub history: bool,
    adding: bool,
    /// Which list reads are still current, by project (Epochs).
    epochs: Epochs,
    output: HashMap<String, SessionOutput>,
    renaming: Option<String>,
    rename: Entity<InputState>,
    /// The projects whose snoozed sessions are open under their row.
    pub snoozed_open: HashSet<String>,
    /// A closed session read from History, its chat read-only.
    pub reading: Option<(LeadSession, Entity<crate::chat::ChatView>, Subscription)>,
    /// A closed session about to be deleted, once confirmed.
    deleting: Option<LeadSession>,
    /// The session header's Changes: its topic and every agent's diff.
    changes: Option<(String, Vec<ChangesOf>)>,
    _sub: Subscription,
}

/// An agent of a session and its diff, as it's read.
type ChangesOf = (
    SessionAgent,
    Option<Result<Entity<crate::diff::DiffView>, String>>,
);

/// A session's tab being dragged to another place among the tabs.
#[derive(Clone)]
pub struct DragSession {
    project: String,
    id: String,
    topic: SharedString,
}

/// What follows the pointer while a tab is dragged: its topic.
struct TabPreview(SharedString);

impl Render for TabPreview {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div()
            .px_2p5()
            .py_1()
            .rounded_md()
            .bg(theme::surface_raised())
            .border_1()
            .border_color(theme::accent())
            .text_sm()
            .text_color(theme::primary())
            .child(self.0.clone())
    }
}

/// move_tab is the sessions with id dragged to where to was (lib/sessions.ts
/// moveTab), kept on its side of the pinned ones.
fn move_tab(sessions: &[LeadSession], id: &str, to: &str) -> Vec<LeadSession> {
    let mut sorted: Vec<LeadSession> = sessions.to_vec();
    sorted.sort_by_key(|s| !s.pinned);
    let (Some(from), Some(to)) = (
        sorted.iter().position(|s| s.id == id),
        sorted.iter().position(|s| s.id == to),
    ) else {
        return sorted;
    };
    let moved = sorted.remove(from);
    // Rightwards it lands after the tab it's dropped on, leftwards before.
    let at = to.min(sorted.len());
    sorted.insert(at, moved);
    sorted.sort_by_key(|s| !s.pinned);
    sorted
}

fn millis(time: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(time).map_or(0, |d| d.timestamp_millis())
}

fn now() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

pub fn is_snoozed(s: &LeadSession) -> bool {
    s.snoozed_until
        .as_deref()
        .is_some_and(|u| millis(u) > now())
}

pub fn is_working(s: &LeadSession) -> bool {
    matches!(s.chat.as_str(), "running" | "starting" | "waiting")
}

/// status is where a session stands (sessionStatus).
pub fn status(s: &LeadSession) -> &'static str {
    match s.status.as_str() {
        "needsYou" => "needsYou",
        "failed" => "failed",
        "working" => "working",
        "idle" => "idle",
        _ if s.chat == "waiting" => "needsYou",
        _ if s.chat == "error" => "failed",
        _ if is_working(s) => "working",
        _ => "idle",
    }
}

pub(super) fn topic(s: &LeadSession) -> String {
    if s.untitled || s.topic.is_empty() {
        t("sessions.untitled")
    } else {
        s.topic.clone()
    }
}

/// wake_time is when a snoozed session comes back, short: "Thu 09:00".
fn wake_time(iso: &str) -> String {
    chrono::DateTime::parse_from_rfc3339(iso)
        .map(|d| crate::dates::weekday_time(d.with_timezone(&Local)))
        .unwrap_or_default()
}

/// snooze_choices are the times a session can sleep until (snoozeChoices).
fn snooze_choices() -> Vec<(&'static str, chrono::DateTime<Local>)> {
    let now = Local::now();
    let at = |days: i64, hour: u32| {
        let d = (now + Days::days(days)).date_naive();
        Local
            .from_local_datetime(&d.and_hms_opt(hour, 0, 0).unwrap_or_default())
            .single()
            .unwrap_or(now)
    };
    let mut out = vec![("hour", now + Days::hours(1))];
    let evening = at(0, 18);
    if (evening - now).num_minutes() > 60 {
        out.push(("evening", evening));
    }
    out.push(("tomorrow", at(1, 9)));
    let dow = now.weekday().num_days_from_sunday() as i64;
    let to_monday = match (8 - dow) % 7 {
        0 => 7,
        n => n,
    };
    out.push(("week", at(to_monday, 9)));
    let _ = now.hour();
    out
}

fn mark(s: &LeadSession, unread: bool) -> Option<AnyElement> {
    match status(s) {
        "needsYou" => Some(
            div()
                .flex_none()
                .min_w(px(16.))
                .h(px(16.))
                .px_1()
                .rounded_full()
                .bg(theme::starting())
                .flex()
                .items_center()
                .justify_center()
                .text_size(px(10.))
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::on_bright())
                .child(s.needs_you.max(1).to_string())
                .into_any_element(),
        ),
        "failed" => Some(
            div()
                .flex_none()
                .text_xs()
                .text_color(theme::error())
                .child(crate::icons::of("⚠"))
                .into_any_element(),
        ),
        "working" => Some(
            div()
                .flex_none()
                .text_xs()
                .text_color(theme::accent())
                .child(crate::icons::of("◌"))
                .into_any_element(),
        ),
        _ if unread => Some(
            div()
                .flex_none()
                .size_1p5()
                .rounded_full()
                .bg(theme::brand(500))
                .into_any_element(),
        ),
        _ => None,
    }
}

/// Epochs tells a list read that started before a session was added from one
/// that started after: the older one doesn't have the session, and when it
/// lands late it must not take the session out of the list (and so the
/// selection with it).
#[derive(Default)]
pub struct Epochs(HashMap<String, u64>);

impl Epochs {
    /// begin is the epoch a read of the project's list starts in.
    pub fn begin(&self, project: &str) -> u64 {
        self.0.get(project).copied().unwrap_or(0)
    }

    /// changed starts a new epoch: reads begun before it are stale.
    pub fn changed(&mut self, project: &str) {
        *self.0.entry(project.to_string()).or_default() += 1;
    }

    /// current is whether a read begun in `epoch` may still be applied.
    pub fn current(&self, project: &str, epoch: u64) -> bool {
        self.begin(project) == epoch
    }
}

impl Sessions {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let chosen = prefs::get(CHOSEN)
            .and_then(|v| serde_json::from_value::<HashMap<String, String>>(v).ok())
            .unwrap_or_default();
        let rename = cx.new(|cx| InputState::new(window, cx));
        let sub = cx.subscribe_in(
            &rename,
            window,
            |this, _, ev: &InputEvent, _, cx| match ev {
                InputEvent::PressEnter { .. } | InputEvent::Blur => this.finish_rename(cx),
                _ => {}
            },
        );
        Self {
            home_tab: 0,
            lists: HashMap::new(),
            closed: HashMap::new(),
            chosen,
            seen: HashMap::new(),
            history: false,
            adding: false,
            epochs: Epochs::default(),
            output: HashMap::new(),
            renaming: None,
            rename,
            snoozed_open: HashSet::new(),
            reading: None,
            deleting: None,
            changes: None,
            _sub: sub,
        }
    }

    pub fn awake(&self, project: &str) -> Vec<&LeadSession> {
        self.lists
            .get(project)
            .map(|l| l.iter().filter(|s| !is_snoozed(s)).collect())
            .unwrap_or_default()
    }

    pub fn snoozed(&self, project: &str) -> Vec<&LeadSession> {
        let mut out: Vec<&LeadSession> = self
            .lists
            .get(project)
            .map(|l| l.iter().filter(|s| is_snoozed(s)).collect())
            .unwrap_or_default();
        out.sort_by_key(|s| s.snoozed_until.as_deref().map(millis));
        out
    }

    /// current is the session the project's chat shows.
    pub fn current(&self, project: &str) -> Option<&LeadSession> {
        let awake = self.awake(project);
        let chosen = self.chosen.get(project);
        awake
            .iter()
            .find(|s| Some(&s.id) == chosen)
            .or_else(|| awake.iter().max_by_key(|s| millis(&s.active_at)))
            .copied()
    }

    /// unread is whether a session said something this app hasn't shown.
    pub fn unread(&self, s: &LeadSession) -> bool {
        let Some(last) = &s.last else { return false };
        let seen_here = self.seen.get(&s.r#ref).copied().unwrap_or(0);
        let seen = s.seen_at.as_deref().map(millis).unwrap_or(0).max(seen_here);
        let since = if seen == 0 {
            millis(&s.created_at)
        } else {
            seen
        };
        millis(&last.at) > since
    }

    fn choose(&mut self, project: &str, id: &str) {
        if self.chosen.get(project).map(String::as_str) != Some(id) {
            self.chosen.insert(project.to_string(), id.to_string());
            prefs::set(
                CHOSEN,
                serde_json::to_value(&self.chosen).unwrap_or_default(),
            );
        }
    }
}

impl AppView {
    /// lead_target is the API prefix and the ref of the lead's chat the
    /// project shows: its current session's.
    pub(super) fn lead_target(&self, project: &str) -> (String, String, Option<String>) {
        match self.sessions.current(project) {
            Some(s) => (
                format!("/v1/projects/{project}/sessions/{}", s.id),
                s.r#ref.clone(),
                Some(topic(s)),
            ),
            None => (
                format!("/v1/projects/{project}"),
                format!("{project}/lead"),
                None,
            ),
        }
    }

    pub(super) fn load_sessions(&mut self, project: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let open = self.project.as_deref() == Some(project.as_str());
        let epoch = self.sessions.epochs.begin(&project);
        cx.spawn(async move |this, cx| {
            let list = client
                .get::<Vec<LeadSession>>(&format!("/v1/projects/{project}/sessions"))
                .await;
            let closed = if open {
                client
                    .get::<Vec<LeadSession>>(&format!(
                        "/v1/projects/{project}/sessions?closed=true"
                    ))
                    .await
                    .ok()
            } else {
                None
            };
            this.update(cx, |this, cx| {
                let Ok(list) = list else { return };
                // A session was added since this read began: it doesn't
                // list it, and the read that follows the add will.
                if !this.sessions.epochs.current(&project, epoch) {
                    return;
                }
                this.sessions.lists.insert(project.clone(), list);
                if let Some(c) = closed {
                    this.sessions.closed.insert(project.clone(), c);
                }
                this.session_shown(&project, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// session_shown opens the chat again when the session it should show
    /// isn't the one it does, and reads what that session made.
    fn session_shown(&mut self, project: &str, cx: &mut Context<Self>) {
        if self.project.as_deref() != Some(project) || self.open != "lead" {
            return;
        }
        let (_, r, _) = self.lead_target(project);
        let showing = self.chat.as_ref().map(|c| c.read(cx).agent.clone());
        if showing.as_deref() != Some(r.as_str()) {
            self.reopen_lead = true;
            cx.notify();
        }
        if let Some(s) = self.sessions.current(project).cloned() {
            self.see_session(&s, cx);
            self.load_output(project.to_string(), s.id.clone(), cx);
        }
    }

    /// see_session marks a session seen now, here and in the daemon.
    pub(super) fn see_session(&mut self, s: &LeadSession, cx: &mut Context<Self>) {
        if !self.sessions.unread(s) && self.sessions.seen.contains_key(&s.r#ref) {
            return;
        }
        self.sessions.seen.insert(s.r#ref.clone(), now());
        let client = self.client.clone();
        let path = format!("/v1/projects/{}/sessions/{}/seen", s.project, s.id);
        cx.spawn(async move |_, _| {
            let at = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
            let _ = client
                .post::<_, serde_json::Value>(&path, &SeeLeadSessionRequest { at: Some(at) })
                .await;
        })
        .detach();
    }

    fn load_output(&mut self, project: String, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<SessionOutput>(&format!("/v1/projects/{project}/sessions/{id}/output"))
                .await;
            this.update(cx, |this, cx| {
                // A failed read is no agents, not "Loading…" for good; the
                // next status event asks again.
                this.sessions.output.insert(id, res.unwrap_or_default());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// on_session_status takes where sessions stand now (lead.session.status).
    pub(super) fn on_session_status(
        &mut self,
        project: String,
        sessions: Vec<LeadSession>,
        cx: &mut Context<Self>,
    ) {
        let prev = self.sessions.lists.get(&project).cloned();
        if let Some(prev) = &prev {
            self.check_sessions(&project, prev, &sessions, cx);
        }
        if let Some(list) = self.sessions.lists.get_mut(&project) {
            for s in sessions {
                match list.iter_mut().find(|x| x.id == s.id) {
                    Some(x) => *x = s,
                    None => list.push(s),
                }
            }
        }
        if let Some(s) = self.sessions.current(&project).cloned()
            && self.project.as_deref() == Some(project.as_str())
            && self.open == "lead"
        {
            self.see_session(&s, cx);
        }
        cx.notify();
    }

    /// on_sessions_settled says when sessions settled into History by
    /// themselves, with Undo (SettledToasts).
    pub(super) fn on_sessions_settled(
        &mut self,
        project: String,
        sessions: Vec<LeadSession>,
        cx: &mut Context<Self>,
    ) {
        if sessions.is_empty() {
            return;
        }
        let message = if sessions.len() == 1 {
            tv("sessions.settled", &[("topic", topic(&sessions[0]).into())])
        } else {
            tv("sessions.settledMany", &[("count", sessions.len().into())])
        };
        let view = cx.entity().downgrade();
        let ids: Vec<String> = sessions.iter().map(|s| s.id.clone()).collect();
        Toast::new(Kind::Info, message)
            .action(t("sessions.undo"), move |_, cx| {
                for id in &ids {
                    let (p, id) = (project.clone(), id.clone());
                    view.update(cx, |this, cx| this.reopen_lead_session(p, id, cx))
                        .ok();
                }
            })
            .show(cx);
    }

    /// select_session shows one of the project's sessions.
    pub(super) fn select_session(
        &mut self,
        project: String,
        id: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.sessions.choose(&project, &id);
        self.close_media(cx);
        self.sessions.history = false;
        self.sessions.reading = None;
        if self.project.as_deref() != Some(project.as_str()) {
            self.select_project(project, window, cx);
            return;
        }
        self.open_chat("lead".into(), window, cx);
        if let Some(s) = self.sessions.current(&project).cloned() {
            self.see_session(&s, cx);
            self.load_output(project, s.id, cx);
        }
    }

    /// new_session opens a session: from the one shown, forked from an item
    /// of it, or continuing a closed one; a first message goes there.
    pub(super) fn new_session(
        &mut self,
        req: NewLeadSessionRequest,
        send: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.sessions.adding {
            return;
        }
        self.sessions.adding = true;
        cx.notify();
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .post::<_, LeadSession>(&format!("/v1/projects/{project}/sessions"), &req)
                .await;
            if let Ok(s) = &res {
                let base = format!("/v1/projects/{project}/sessions/{}", s.id);
                let _ = client
                    .post::<_, serde_json::Value>(
                        &format!("{base}/chat/start"),
                        &serde_json::json!({}),
                    )
                    .await;
                if let Some(text) = send.filter(|t| !t.is_empty()) {
                    let _ = client
                        .post::<_, serde_json::Value>(
                            &format!("{base}/chat/messages"),
                            &agentbox_api::ChatMessageRequest {
                                text,
                                ..Default::default()
                            },
                        )
                        .await;
                }
            }
            this.update_in(cx, |this, window, cx| {
                this.sessions.adding = false;
                match res {
                    Ok(s) => {
                        let list = this.sessions.lists.entry(project.clone()).or_default();
                        if !list.iter().any(|x| x.id == s.id) {
                            list.push(s.clone());
                        }
                        this.sessions.epochs.changed(&project);
                        this.select_session(project.clone(), s.id, window, cx);
                        this.focus_lead_composer(window, cx);
                        this.load_sessions(project, cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn focus_lead_composer(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        // The chat may be opened again within moments (session_shown): the
        // one that stays takes the cursor too.
        self.focus_lead = true;
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_secs(2))
                .await;
            this.update(cx, |this, _| this.focus_lead = false).ok();
        })
        .detach();
        if let Some(chat) = &self.chat {
            chat.update(cx, |c, cx| c.focus_composer(window, cx));
        }
    }

    /// add_session is New session: one after the session shown.
    pub(super) fn add_session(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let from = self
            .project
            .as_deref()
            .and_then(|p| self.sessions.current(p))
            .map(|s| s.id.clone())
            .unwrap_or_default();
        self.new_session(
            NewLeadSessionRequest {
                from,
                ..Default::default()
            },
            None,
            window,
            cx,
        );
    }

    /// fork_session starts a session from an item of the one shown on.
    pub(super) fn fork_session(
        &mut self,
        item: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(s) = self
            .project
            .as_deref()
            .and_then(|p| self.sessions.current(p))
            .cloned()
        else {
            return;
        };
        self.new_session(
            NewLeadSessionRequest {
                fork_from: Some(LeadSessionFork {
                    session: s.id,
                    item,
                }),
                ..Default::default()
            },
            None,
            window,
            cx,
        );
    }

    /// session_with_message is the prompt cache card's new session, its
    /// message sent there (topicFrom names it).
    pub(super) fn session_with_message(
        &mut self,
        text: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let from = self
            .project
            .as_deref()
            .and_then(|p| self.sessions.current(p))
            .map(|s| s.id.clone())
            .unwrap_or_default();
        let first = text
            .lines()
            .next()
            .unwrap_or("")
            .split_whitespace()
            .collect::<Vec<_>>()
            .join(" ");
        let topic = if first.chars().count() <= 60 {
            first
        } else {
            format!("{}…", first.chars().take(59).collect::<String>().trim_end())
        };
        self.new_session(
            NewLeadSessionRequest {
                topic,
                from,
                ..Default::default()
            },
            Some(text),
            window,
            cx,
        );
    }

    #[allow(clippy::type_complexity)]
    fn session_post(
        &mut self,
        project: String,
        path: String,
        body: serde_json::Value,
        then: fn(&mut AppView, String, LeadSession, &mut Window, &mut Context<AppView>),
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client.post::<_, LeadSession>(&path, &body).await;
            this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(s) => then(this, project.clone(), s, window, cx),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_sessions(project, cx);
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn close_session(
        &mut self,
        project: String,
        id: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        // The tab beside it takes its place.
        let awake: Vec<String> = self
            .sessions
            .awake(&project)
            .iter()
            .map(|s| s.id.clone())
            .collect();
        if self.sessions.current(&project).map(|s| s.id.as_str()) == Some(id.as_str())
            && let Some(i) = awake.iter().position(|x| *x == id)
        {
            let rest: Vec<&String> = awake.iter().filter(|x| **x != id).collect();
            if let Some(next) = rest.get(i.min(rest.len().saturating_sub(1))) {
                self.sessions.choose(&project, next);
            }
        }
        if let Some(l) = self.sessions.lists.get_mut(&project) {
            l.retain(|s| s.id != id);
        }
        self.open_chat("lead".into(), window, cx);
        self.session_post(
            project.clone(),
            format!("/v1/projects/{project}/sessions/{id}/close"),
            serde_json::json!({}),
            |_, project, s, _, cx| {
                let view = cx.entity().downgrade();
                let id = s.id.clone();
                Toast::new(
                    Kind::Info,
                    tv("sessions.closed", &[("topic", topic(&s).into())]),
                )
                .action(t("sessions.reopen"), move |_, cx| {
                    let (p, id) = (project.clone(), id.clone());
                    view.update(cx, |this, cx| this.reopen_lead_session(p, id, cx))
                        .ok();
                })
                .show(cx);
            },
            window,
            cx,
        );
    }

    pub(super) fn reopen_session(
        &mut self,
        project: String,
        id: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.session_post(
            project.clone(),
            format!("/v1/projects/{project}/sessions/{id}/reopen"),
            serde_json::json!({}),
            |this, project, s, window, cx| this.select_session(project, s.id, window, cx),
            window,
            cx,
        );
    }

    /// reopen_lead_session reopens a session from a toast's action, which
    /// has no window of its own: the chat follows on the next frame.
    pub(super) fn reopen_lead_session(
        &mut self,
        project: String,
        id: String,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, LeadSession>(
                    &format!("/v1/projects/{project}/sessions/{id}/reopen"),
                    &serde_json::json!({}),
                )
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(s) => {
                        this.sessions.choose(&project, &s.id);
                        this.reopen_lead = true;
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_sessions(project, cx);
            })
            .ok();
        })
        .detach();
    }

    /// snooze hides a session until a time, or wakes it (until None).
    pub(super) fn snooze(
        &mut self,
        project: String,
        id: String,
        until: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let waking = until.is_none();
        if let Some(u) = &until
            && let Some(l) = self.sessions.lists.get_mut(&project)
            && let Some(s) = l.iter_mut().find(|s| s.id == id)
        {
            s.snoozed_until = Some(u.clone());
            self.reopen_lead = true;
        }
        self.session_post(
            project.clone(),
            format!("/v1/projects/{project}/sessions/{id}/snooze"),
            serde_json::to_value(LeadSessionSnooze { until }).unwrap_or_default(),
            if waking {
                |this: &mut AppView,
                 project: String,
                 s: LeadSession,
                 window: &mut Window,
                 cx: &mut Context<AppView>| {
                    if let Some(l) = this.sessions.lists.get_mut(&project)
                        && let Some(x) = l.iter_mut().find(|x| x.id == s.id)
                    {
                        *x = s.clone();
                    }
                    this.select_session(project, s.id, window, cx)
                }
            } else {
                |this: &mut AppView,
                 project: String,
                 s: LeadSession,
                 _: &mut Window,
                 cx: &mut Context<AppView>| {
                    let view = cx.entity().downgrade();
                    let id = s.id.clone();
                    Toast::new(
                        Kind::Info,
                        tv("sessions.snoozed.done", &[("topic", topic(&s).into())]),
                    )
                    .action(t("sessions.snoozed.wake"), move |window, cx| {
                        let (p, id) = (project.clone(), id.clone());
                        view.update(cx, |this, cx| this.snooze(p, id, None, window, cx))
                            .ok();
                    })
                    .show(cx);
                    this.reopen_lead = true;
                }
            },
            window,
            cx,
        );
        cx.notify();
    }

    fn patch_session(
        &mut self,
        project: String,
        id: String,
        patch: LeadSessionPatch,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&patch).unwrap_or_default();
            let res = client
                .raw(
                    agentbox_api::client::Method::PATCH,
                    &format!("/v1/projects/{project}/sessions/{id}"),
                    Some(body),
                )
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_sessions(project, cx);
            })
            .ok();
        })
        .detach();
    }

    fn rename_session(&mut self, s: &LeadSession, window: &mut Window, cx: &mut Context<Self>) {
        self.sessions.renaming = Some(s.id.clone());
        let topic = topic(s);
        self.sessions.rename.update(cx, |i, cx| {
            i.set_value(topic, window, cx);
            i.focus(window, cx);
            i.select_all(window, cx);
        });
        cx.notify();
    }

    fn finish_rename(&mut self, cx: &mut Context<Self>) {
        let Some(id) = self.sessions.renaming.take() else {
            return;
        };
        let Some(project) = self.project.clone() else {
            return;
        };
        let topic = self.sessions.rename.read(cx).value().trim().to_string();
        let was = self
            .sessions
            .lists
            .get(&project)
            .and_then(|l| l.iter().find(|s| s.id == id))
            .map(|s| s.topic.clone());
        if !topic.is_empty() && was.as_deref() != Some(topic.as_str()) {
            self.patch_session(
                project,
                id,
                LeadSessionPatch {
                    topic,
                    pinned: None,
                },
                cx,
            );
        }
        cx.notify();
    }

    /// session_key is the sessions' keys, while the lead's chat shows:
    /// Ctrl+T new, Ctrl+W close, Ctrl+Tab next, Ctrl+1…9 one of them.
    pub(super) fn session_key(
        &mut self,
        ev: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        let k = &ev.keystroke;
        if !k.modifiers.control || self.open != "lead" || self.settings || self.work.is_some() {
            return false;
        }
        let Some(project) = self.project.clone() else {
            return false;
        };
        let awake: Vec<String> = self
            .sessions
            .awake(&project)
            .iter()
            .map(|s| s.id.clone())
            .collect();
        let current = self.sessions.current(&project).map(|s| s.id.clone());
        match k.key.as_str() {
            "t" if !k.modifiers.shift => self.add_session(window, cx),
            "w" if !k.modifiers.shift => {
                if let Some(id) = current {
                    self.close_session(project, id, window, cx);
                }
            }
            "tab" if !awake.is_empty() => {
                let i = current
                    .and_then(|c| awake.iter().position(|x| *x == c))
                    .unwrap_or(0);
                let n = awake.len();
                let to = if k.modifiers.shift {
                    (i + n - 1) % n
                } else {
                    (i + 1) % n
                };
                self.select_session(project, awake[to].clone(), window, cx);
            }
            d if d.len() == 1 && ("1"..="9").contains(&d) => {
                let n: usize = d.parse().unwrap_or(1);
                if let Some(id) = awake.get(n - 1) {
                    self.select_session(project, id.clone(), window, cx);
                }
            }
            _ => return false,
        }
        true
    }

    /// render_session_tabs is the row of the project's sessions above its chat.
    pub(super) fn render_session_tabs(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let project = self.project.clone()?;
        if self.open != "lead" || !self.sessions.lists.contains_key(&project) {
            return None;
        }
        let awake = self.sessions.awake(&project);
        let snoozed: Vec<LeadSession> = self
            .sessions
            .snoozed(&project)
            .into_iter()
            .cloned()
            .collect();
        let current = self.sessions.current(&project).map(|s| s.id.clone());
        let closed = self.sessions.closed.get(&project).map_or(0, |c| c.len());
        let mut sorted: Vec<&LeadSession> = awake.clone();
        sorted.sort_by_key(|s| !s.pinned);
        let tab_group = format!("session-tabs-{project}");
        // A session just opened slides into the strip.
        let tab_ids: Vec<String> = sorted.iter().map(|s| s.id.clone()).collect();
        let tabs = sorted.into_iter().map(|s| {
            let on = Some(&s.id) == current.as_ref();
            let unread = !on && self.sessions.unread(s);
            if self.sessions.renaming.as_ref() == Some(&s.id) {
                return div()
                    .w(px(200.))
                    .flex_none()
                    .child(Input::new(&self.sessions.rename).small())
                    .into_any_element();
            }
            let (p, id) = (project.clone(), s.id.clone());
            let (p2, id2) = (project.clone(), s.id.clone());
            let s2 = s.clone();
            let view = cx.entity().downgrade();
            let (pinned, menu_s) = (s.pinned, s.clone());
            let drag = DragSession {
                project: project.clone(),
                id: s.id.clone(),
                topic: topic(s).into(),
            };
            let target = s.id.clone();
            h_flex()
                .id(SharedString::from(format!("session-{}", s.id)))
                .on_drag(drag, |d: &DragSession, _, _, cx| {
                    let name = d.topic.clone();
                    cx.new(|_| TabPreview(name))
                })
                .drag_over::<DragSession>(|st, _, _, _| {
                    st.border_l_2().border_color(theme::accent())
                })
                .on_drop(cx.listener(move |this, d: &DragSession, _, cx| {
                    this.reorder_session(&d.project, &d.id, &target, cx)
                }))
                // SessionTabs' tab: h-8 max-w-56 rounded-t-lg border pl-2.5
                // pr-7 text-ui, its close button in the padding.
                .relative()
                .group(SharedString::from(format!("session-tab-{}", s.id)))
                .h_8()
                .max_w(px(224.))
                .flex_none()
                .gap(px(6.))
                .pl(px(10.))
                .pr_7()
                .rounded_t_lg()
                .border_1()
                .border_color(theme::line())
                .cursor_pointer()
                .text_size(theme::text::UI)
                .when(on, |el| {
                    el.border_b_0()
                        .bg(theme::ink())
                        .text_color(theme::primary())
                })
                .when(!on, |el| {
                    el.border_color(theme::line_faint())
                        .text_color(theme::muted())
                        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                })
                .children(mark(s, unread))
                .when(pinned && status(s) == "idle" && !unread, |el| {
                    el.child(
                        div()
                            .flex_none()
                            .text_color(theme::subtle())
                            .child(crate::icons::ic("pin").size(px(12.))),
                    )
                })
                .child(div().min_w_0().truncate().child(topic(s)))
                .when(s.agents > 0, |el| {
                    el.child(
                        div()
                            .flex_none()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(s.agents.to_string()),
                    )
                })
                .child(
                    div()
                        .id(SharedString::from(format!("session-close-{}", s.id)))
                        .absolute()
                        .right(px(6.))
                        .top(px(6.))
                        .size_5()
                        .flex()
                        .items_center()
                        .justify_center()
                        .rounded_md()
                        .text_color(theme::faint())
                        .when(!on, |el| {
                            el.opacity(0.)
                                .group_hover(format!("session-tab-{}", s.id), |el| el.opacity(1.))
                        })
                        .hover(|el| el.bg(theme::surface_strong()).text_color(theme::primary()))
                        .child(crate::icons::ic("x").size(px(12.)))
                        .tooltip(move |w, cx| {
                            gpui_component::tooltip::Tooltip::new(t("sessions.close")).build(w, cx)
                        })
                        .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                            cx.stop_propagation();
                            this.close_session(p2.clone(), id2.clone(), window, cx)
                        })),
                )
                .on_click(cx.listener(move |this, ev: &ClickEvent, window, cx| {
                    if ev.click_count() >= 2 {
                        this.rename_session(&s2, window, cx);
                        return;
                    }
                    this.select_session(p.clone(), id.clone(), window, cx)
                }))
                .context_menu(move |menu, _, _| {
                    let (v1, v2, v3) = (view.clone(), view.clone(), view.clone());
                    let (s1, s2, s3) = (menu_s.clone(), menu_s.clone(), menu_s.clone());
                    menu.item(
                        PopupMenuItem::new(if pinned {
                            t("sessions.unpin")
                        } else {
                            t("sessions.pin")
                        })
                        .icon(crate::icons::ic("pin"))
                        .on_click(move |_, _, cx| {
                            v1.update(cx, |this, cx| {
                                this.patch_session(
                                    s1.project.clone(),
                                    s1.id.clone(),
                                    LeadSessionPatch {
                                        topic: String::new(),
                                        pinned: Some(!pinned),
                                    },
                                    cx,
                                )
                            })
                            .ok();
                        }),
                    )
                    .item(
                        PopupMenuItem::new(t("sessions.rename"))
                            .icon(crate::icons::ic("pencil"))
                            .on_click(move |_, window, cx| {
                                v2.update(cx, |this, cx| this.rename_session(&s2, window, cx))
                                    .ok();
                            }),
                    )
                    .separator()
                    .item(
                        PopupMenuItem::new(t("sessions.close"))
                            .icon(crate::icons::ic("x"))
                            .on_click(move |_, window, cx| {
                                v3.update(cx, |this, cx| {
                                    this.close_session(
                                        s3.project.clone(),
                                        s3.id.clone(),
                                        window,
                                        cx,
                                    )
                                })
                                .ok();
                            }),
                    )
                })
                .into_any_element()
        });
        let tabs = tabs
            .zip(tab_ids)
            .map(|(tab, id)| crate::motion::arrive(div().flex_none().child(tab), &tab_group, &id));
        let view = cx.entity().downgrade();
        let p_snooze = project.clone();
        Some(
            h_flex()
                .id("session-tabs")
                .flex_none()
                .items_end()
                .gap_1()
                .px_5()
                .pt_1()
                .overflow_x_scroll()
                .children(tabs)
                .child(
                    div()
                        .id("session-new")
                        .mb_0p5()
                        .h_7()
                        .px_2()
                        .flex()
                        .items_center()
                        .gap_1()
                        .rounded_lg()
                        .cursor_pointer()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                        .when(self.sessions.adding, |el| el.opacity(0.5))
                        .child(crate::icons::ic("plus").size(px(14.)))
                        .when(awake.is_empty(), |el| el.child(t("sessions.new")))
                        .tooltip(|w, cx| {
                            gpui_component::tooltip::Tooltip::new(format!(
                                "{} · Ctrl+T",
                                t("sessions.new")
                            ))
                            .build(w, cx)
                        })
                        .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.add_session(window, cx)
                        })),
                )
                .child(div().flex_1().min_w_2())
                .when(!snoozed.is_empty(), |el| {
                    let n = snoozed.len();
                    el.child(
                        Button::new("sessions-snoozed")
                            .ghost()
                            .xsmall()
                            .mb_0p5()
                            .label(format!("⏰ {n}"))
                            .dropdown_menu_with_anchor(Anchor::TopRight, move |menu, _, _| {
                                let mut menu = menu.label(t("sessions.snoozed.title"));
                                for s in &snoozed {
                                    let (v, p, id) = (view.clone(), p_snooze.clone(), s.id.clone());
                                    menu = menu.item(
                                        PopupMenuItem::new(format!(
                                            "{} · {}",
                                            topic(s),
                                            tv(
                                                "sessions.snoozed.back",
                                                &[(
                                                    "when",
                                                    wake_time(
                                                        s.snoozed_until.as_deref().unwrap_or("")
                                                    )
                                                    .into()
                                                )]
                                            )
                                        ))
                                        .icon(crate::icons::ic("alarm-clock"))
                                        .on_click(
                                            move |_, window, cx| {
                                                v.update(cx, |this, cx| {
                                                    this.snooze(
                                                        p.clone(),
                                                        id.clone(),
                                                        None,
                                                        window,
                                                        cx,
                                                    )
                                                })
                                                .ok();
                                            },
                                        ),
                                    );
                                }
                                menu
                            }),
                    )
                })
                .child(
                    div()
                        .id("sessions-history")
                        .mb_0p5()
                        .h_7()
                        .px_2()
                        .flex()
                        .items_center()
                        .gap_1()
                        .rounded_lg()
                        .cursor_pointer()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                        .when(self.sessions.history, |el| el.bg(theme::surface_strong()))
                        .child(crate::icons::ic("rotate-ccw-clock").size(px(14.)))
                        .when(closed > 0, |el| {
                            el.child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(closed.to_string()),
                            )
                        })
                        .tooltip(move |w, cx| {
                            let tip = if closed > 0 {
                                tv("sessions.historyCount", &[("count", closed.into())])
                            } else {
                                t("sessions.history")
                            };
                            gpui_component::tooltip::Tooltip::new(tip).build(w, cx)
                        })
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.sessions.history = !this.sessions.history;
                            this.sessions.reading = None;
                            if let Some(p) = this.project.clone() {
                                this.load_sessions(p, cx);
                            }
                            cx.notify();
                        })),
                )
                .into_any_element(),
        )
    }

    /// render_session_header is the strip under the tabs: where it came
    /// from, its agents with their pull requests, and Snooze.
    pub(super) fn render_session_header(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let project = self.project.clone()?;
        if self.open != "lead" || self.sessions.history {
            return None;
        }
        let s = self.sessions.current(&project)?.clone();
        let output = self.sessions.output.get(&s.id);
        let agents = output.map(|o| o.agents.clone()).unwrap_or_default();
        let view = cx.entity().downgrade();
        let (p, id) = (project.clone(), s.id.clone());
        let parent = s.parent.clone().map(|parent| {
            let forked = !parent.item.is_empty();
            let label = if parent.topic.is_empty() {
                t(if forked {
                    "sessions.forkedFromGone"
                } else {
                    "sessions.continuedFromGone"
                })
            } else {
                tv(
                    if forked {
                        "sessions.forkedFrom"
                    } else {
                        "sessions.continuedFrom"
                    },
                    &[("topic", (&parent.topic).into())],
                )
            };
            let (p, ps) = (project.clone(), parent.session.clone());
            div()
                .id("session-parent")
                .flex_none()
                .text_xs()
                .text_color(theme::faint())
                .cursor_pointer()
                .hover(|el| el.text_color(theme::primary()).underline())
                .child(format!("↳ {label}"))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    if this.sessions.awake(&p).iter().any(|s| s.id == ps) {
                        this.select_session(p.clone(), ps.clone(), window, cx);
                    } else {
                        this.sessions.history = true;
                        cx.notify();
                    }
                }))
        });
        Some(
            // SessionHeader: border-b border-line-faint px-4 py-1.5, a Bot first.
            h_flex()
                .flex_none()
                .min_w_0()
                .gap_2()
                .px_4()
                .py(px(6.))
                .border_b_1()
                .border_color(theme::line_faint())
                .child(
                    div()
                        .flex_none()
                        .text_color(theme::faint())
                        .child(crate::icons::ic("bot").size(px(14.))),
                )
                .children(parent)
                .child(
                    h_flex()
                        .flex_1()
                        .min_w_0()
                        .gap_1p5()
                        .overflow_hidden()
                        .when(agents.is_empty(), |el| {
                            el.child(
                                div()
                                    .truncate()
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::faint())
                                    .child(if output.is_some() {
                                        t("sessions.header.none")
                                    } else {
                                        t("common.loading")
                                    }),
                            )
                        })
                        .children(agents.iter().map(|a| {
                            let name = if a.title.is_empty() {
                                a.name.clone()
                            } else {
                                a.title.clone()
                            };
                            let r = a.r#ref.clone();
                            h_flex()
                                .id(SharedString::from(format!("session-agent-{}", a.r#ref)))
                                .flex_none()
                                .h_6()
                                .gap_1p5()
                                .px_2()
                                .rounded_full()
                                .border_1()
                                .border_color(theme::line())
                                .bg(theme::card())
                                .text_xs()
                                .text_color(if a.archived {
                                    theme::faint()
                                } else {
                                    theme::muted()
                                })
                                .when(a.working, |el| {
                                    el.child(
                                        div()
                                            .text_color(theme::accent())
                                            .child(crate::icons::of("◌")),
                                    )
                                })
                                .when(a.archived, |el| el.child(crate::icons::of("▤")))
                                .child(div().max_w(px(160.)).truncate().child(name))
                                .when_some(a.pr.as_ref().filter(|p| p.number > 0), |el, pr| {
                                    el.child(
                                        div()
                                            .text_color(theme::faint())
                                            .child(format!("#{}", pr.number)),
                                    )
                                })
                                .when(!a.archived, |el| {
                                    el.cursor_pointer()
                                        .hover(|el| {
                                            el.bg(theme::surface_raised())
                                                .text_color(theme::primary())
                                        })
                                        .on_click(cx.listener(
                                            move |this, _: &ClickEvent, window, cx| {
                                                this.open_agent_ref(r.clone(), window, cx)
                                            },
                                        ))
                                })
                        })),
                )
                .when(!agents.is_empty(), |el| {
                    let (topic, agents) = (topic(&s), agents.clone());
                    el.child(
                        Button::new("session-changes")
                            .ghost()
                            .xsmall()
                            .label(t("sessions.header.changes"))
                            .tooltip(t("sessions.header.changesHint"))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.open_changes(topic.clone(), agents.clone(), cx)
                            })),
                    )
                })
                .child(
                    Button::new("session-snooze")
                        .ghost()
                        .compact()
                        .h_6()
                        .px(px(6.))
                        .gap_1()
                        .rounded(gpui_component::button::ButtonRounded::Size(px(8.)))
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::muted())
                        .icon(crate::icons::ic("alarm-clock").size(px(14.)))
                        // SnoozeMenu's ghost sm Button: 12px medium, muted.
                        .child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .line_height(px(16.))
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::muted())
                                .child(t("sessions.snooze.label")),
                        )
                        .tooltip(t("sessions.snooze.hint"))
                        .dropdown_menu_with_anchor(Anchor::TopRight, move |menu, _, _| {
                            let mut menu = menu.label(t("sessions.snooze.until"));
                            for (key, until) in snooze_choices() {
                                let (v, p, id) = (view.clone(), p.clone(), id.clone());
                                let iso = until
                                    .with_timezone(&chrono::Utc)
                                    .to_rfc3339_opts(chrono::SecondsFormat::Secs, true);
                                menu = menu.item(
                                    PopupMenuItem::new(format!(
                                        "{}  ·  {}",
                                        t(&format!("sessions.snooze.{key}")),
                                        crate::dates::weekday_time(until)
                                    ))
                                    .on_click(
                                        move |_, window, cx| {
                                            v.update(cx, |this, cx| {
                                                this.snooze(
                                                    p.clone(),
                                                    id.clone(),
                                                    Some(iso.clone()),
                                                    window,
                                                    cx,
                                                )
                                            })
                                            .ok();
                                        },
                                    ),
                                );
                            }
                            menu
                        }),
                )
                .into_any_element(),
        )
    }

    /// render_no_session is the chat with every session closed or snoozed.
    pub(super) fn render_no_session(&self, cx: &mut Context<Self>) -> AnyElement {
        let project = self.project.clone().unwrap_or_default();
        let closed = self.sessions.closed.get(&project).map_or(0, |c| c.len());
        v_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_3()
            .child(
                div()
                    .text_color(theme::primary())
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(t("sessions.none")),
            )
            .child(
                div()
                    .max_w(px(420.))
                    .text_center()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(t("sessions.noneHint")),
            )
            .child(
                h_flex()
                    .gap_2()
                    .child(controls::button(
                        "no-session-new",
                        t("sessions.new"),
                        Btn::Primary,
                        self.sessions.adding,
                        cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.add_session(window, cx)
                        }),
                    ))
                    .when(closed > 0, |el| {
                        el.child(controls::button(
                            "no-session-history",
                            tv("sessions.historyCount", &[("count", closed.into())]),
                            Btn::Secondary,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.sessions.history = true;
                                cx.notify();
                            }),
                        ))
                    }),
            )
            .into_any_element()
    }

    /// render_history is History: the closed sessions, to reopen or to
    /// start a new session from.
    /// reorder_session drops a tab where another was; the daemon keeps the
    /// order, for every computer and the phone (PUT …/sessions/order).
    fn reorder_session(&mut self, project: &str, id: &str, to: &str, cx: &mut Context<Self>) {
        if id == to {
            return;
        }
        let Some(list) = self.sessions.lists.get(project) else {
            return;
        };
        let next = move_tab(list, id, to);
        let ids: Vec<String> = next.iter().map(|s| s.id.clone()).collect();
        self.sessions.lists.insert(project.to_string(), next);
        let client = self.client.clone();
        let project = project.to_string();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    agentbox_api::client::Method::PUT,
                    &format!("/v1/projects/{project}/sessions/order"),
                    serde_json::to_vec(&serde_json::json!({ "ids": ids })).ok(),
                )
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_sessions(project, cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// open_changes shows every agent's diff of a session in one view
    /// (SessionChanges): a live agent's against its base, a retired one's
    /// from its kept branch.
    fn open_changes(&mut self, topic: String, agents: Vec<SessionAgent>, cx: &mut Context<Self>) {
        self.sessions.changes = Some((topic, agents.iter().map(|a| (a.clone(), None)).collect()));
        for a in agents {
            let Some((p, name)) = a.r#ref.split_once('/') else {
                continue;
            };
            let path = if a.archived {
                format!("/v1/archived-agents/{p}/{name}/diff")
            } else {
                format!("/v1/agents/{p}/{name}/diff")
            };
            let client = self.client.clone();
            let r#ref = a.r#ref.clone();
            cx.spawn(async move |this, cx| {
                let res = client
                    .raw(agentbox_api::client::Method::GET, &path, None)
                    .await;
                this.update(cx, |this, cx| {
                    let view = match res {
                        Ok(b) => {
                            let text = String::from_utf8_lossy(&b).into_owned();
                            let id = format!("changes-{ref}", ref = r#ref.replace('/', "-"));
                            Ok(cx.new(|cx| crate::diff::DiffView::text(&id, &text, cx)))
                        }
                        Err(e) => Err(e.to_string()),
                    };
                    if let Some((_, list)) = &mut this.sessions.changes
                        && let Some(slot) = list.iter_mut().find(|(a, _)| a.r#ref == r#ref)
                    {
                        slot.1 = Some(view);
                    }
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        cx.notify();
    }

    pub(super) fn render_session_changes(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let (topic, list) = self.sessions.changes.as_ref()?;
        let close = cx.listener(|this, _: &ClickEvent, _, cx| {
            this.sessions.changes = None;
            cx.notify();
        });
        Some(
            crate::motion::scrim_in(
                div()
                    .id("session-changes")
                    .absolute()
                    .inset_0()
                    .bg(theme::scrim())
                    .flex()
                    .items_center()
                    .justify_center()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, _, _, cx| {
                            this.sessions.changes = None;
                            cx.notify();
                        }),
                    )
                    .child(crate::motion::pop_in(
                        v_flex()
                            .w(px(1024.))
                            .max_w(relative(0.94))
                            .h(relative(0.88))
                            .rounded_xl()
                            .bg(theme::card())
                            .border_1()
                            .border_color(theme::line())
                            .overflow_hidden()
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .child(
                                h_flex()
                                    .flex_none()
                                    .px_6()
                                    .py_4()
                                    .gap_3()
                                    .border_b_1()
                                    .border_color(theme::line())
                                    .child(
                                        v_flex()
                                            .flex_1()
                                            .min_w_0()
                                            .child(
                                                div()
                                                    .text_lg()
                                                    .font_weight(FontWeight::SEMIBOLD)
                                                    .text_color(theme::primary())
                                                    .child(tv(
                                                        "sessions.header.changesTitle",
                                                        &[("topic", topic.into())],
                                                    )),
                                            )
                                            .child(
                                                div().text_sm().text_color(theme::muted()).child(
                                                    tv(
                                                        "sessions.header.changesDescription",
                                                        &[("count", list.len().into())],
                                                    ),
                                                ),
                                            ),
                                    )
                                    .child(controls::button(
                                        "session-changes-close",
                                        "✕",
                                        Btn::Ghost,
                                        false,
                                        close,
                                    )),
                            )
                            .child(
                                v_flex()
                                    .id("session-changes-list")
                                    .flex_1()
                                    .min_h_0()
                                    .overflow_y_scroll()
                                    .children(list.iter().map(|(a, view)| {
                                        v_flex()
                                            .border_b_1()
                                            .border_color(theme::line())
                                            .child(
                                                h_flex()
                                                    .px_5()
                                                    .py_2p5()
                                                    .gap_2()
                                                    .border_b_1()
                                                    .border_color(theme::line().opacity(0.5))
                                                    .child(
                                                        div()
                                                            .min_w_0()
                                                            .truncate()
                                                            .text_sm()
                                                            .font_weight(FontWeight::MEDIUM)
                                                            .text_color(theme::primary())
                                                            .child(if a.title.is_empty() {
                                                                a.name.clone()
                                                            } else {
                                                                a.title.clone()
                                                            }),
                                                    )
                                                    .when(!a.branch.is_empty(), |el| {
                                                        el.child(
                                                            div()
                                                                .min_w_0()
                                                                .truncate()
                                                                .font_family(crate::theme::mono())
                                                                .text_xs()
                                                                .text_color(theme::faint())
                                                                .child(crate::icons::lead(
                                                                    "git-branch",
                                                                    a.branch.clone(),
                                                                )),
                                                        )
                                                    })
                                                    .when_some(a.pr.as_ref(), |el, pr| {
                                                        el.child(super::work::pr_chip(pr))
                                                    })
                                                    .when(a.archived, |el| {
                                                        el.child(
                                                            div()
                                                                .text_xs()
                                                                .text_color(theme::faint())
                                                                .child(t(
                                                                    "sessions.header.retired",
                                                                )),
                                                        )
                                                    }),
                                            )
                                            .child(match view {
                                                None => div()
                                                    .p_4()
                                                    .child(crate::motion::skeleton_lines(
                                                        "loading-sessions-6",
                                                        4,
                                                    ))
                                                    .into_any_element(),
                                                Some(Err(e)) => div()
                                                    .p_4()
                                                    .text_sm()
                                                    .text_color(theme::error())
                                                    .child(e.clone())
                                                    .into_any_element(),
                                                Some(Ok(d)) => div()
                                                    .h(px(520.))
                                                    .child(d.clone())
                                                    .into_any_element(),
                                            })
                                    })),
                            ),
                        "session-changes-in",
                    )),
                "idsession-changes-in",
            )
            .into_any_element(),
        )
    }

    /// read_closed opens a closed session's conversation, read-only.
    fn read_closed(&mut self, s: LeadSession, window: &mut Window, cx: &mut Context<Self>) {
        let project = s.project.clone();
        let client = self.client.clone();
        let chat = cx.new(|cx| {
            let mut c = crate::chat::ChatView::new(
                client,
                format!("/v1/projects/{project}/sessions/{}", s.id),
                s.r#ref.clone(),
                topic(&s).into(),
                window,
                cx,
            );
            c.header = false;
            c.closed = true;
            c
        });
        let (p, id) = (project.clone(), s.id.clone());
        let sub = cx.subscribe_in(
            &chat,
            window,
            move |this: &mut Self, _, ev: &crate::chat::ChatAction, window, cx| match ev {
                crate::chat::ChatAction::ContinueClosed => {
                    this.sessions.reading = None;
                    this.new_session(
                        NewLeadSessionRequest {
                            continue_from: id.clone(),
                            ..Default::default()
                        },
                        None,
                        window,
                        cx,
                    )
                }
                crate::chat::ChatAction::ReopenClosed => {
                    this.sessions.reading = None;
                    this.reopen_session(p.clone(), id.clone(), window, cx)
                }
                crate::chat::ChatAction::DeleteClosed => {
                    this.sessions.deleting =
                        this.sessions.reading.as_ref().map(|(s, ..)| s.clone());
                    cx.notify();
                }
                _ => {}
            },
        );
        self.sessions.reading = Some((s, chat, sub));
        cx.notify();
    }

    /// delete_closed deletes a closed session for good (DELETE …/sessions/<id>).
    fn delete_closed(&mut self, s: LeadSession, cx: &mut Context<Self>) {
        self.sessions.deleting = None;
        self.sessions.reading = None;
        if let Some(list) = self.sessions.closed.get_mut(&s.project) {
            list.retain(|x| x.id != s.id);
        }
        let client = self.client.clone();
        let path = format!("/v1/projects/{}/sessions/{}", s.project, s.id);
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(agentbox_api::client::Method::DELETE, &path, None)
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(_) => Toast::new(
                        Kind::Success,
                        tv("sessions.deleted", &[("topic", topic(&s).into())]),
                    )
                    .show(cx),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_sessions(s.project.clone(), cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn render_reading(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let (s, chat, _) = self.sessions.reading.as_ref()?;
        let meta = [
            s.closed_at
                .as_deref()
                .map(|c| tv("sessions.closedAgo", &[("when", time_ago(c).into())])),
            (s.agents > 0).then(|| tv("sessions.agentCount", &[("count", s.agents.into())])),
        ]
        .into_iter()
        .flatten()
        .collect::<Vec<_>>()
        .join(" · ");
        let deleting = self.sessions.deleting.clone().map(|d| {
            let d2 = d.clone();
            super::agent::modal(
                "session-delete",
                tv("sessions.deleteTitle", &[("topic", topic(&d).into())]),
                Some(t("sessions.deleteDescription")),
                Vec::new(),
                {
                    let me = cx.entity().downgrade();
                    std::rc::Rc::new(move |_, cx: &mut App| {
                        me.update(cx, |this, cx| {
                            this.sessions.deleting = None;
                            cx.notify();
                        })
                        .ok();
                    })
                },
                t("common.delete"),
                true,
                false,
                cx.listener(move |this, _: &ClickEvent, _, cx| this.delete_closed(d2.clone(), cx)),
            )
        });
        Some(
            v_flex()
                .relative()
                .size_full()
                .child(
                    h_flex()
                        .flex_none()
                        .px_6()
                        .pt_4()
                        .pb_3()
                        .gap_3()
                        .child(controls::button(
                            "history-reading-back",
                            format!("← {}", t("sessions.backToHistory")),
                            Btn::Ghost,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.sessions.reading = None;
                                cx.notify();
                            }),
                        ))
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .child(
                                    div()
                                        .truncate()
                                        .text_lg()
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::primary())
                                        .child(topic(s)),
                                )
                                .child(div().text_xs().text_color(theme::faint()).child(meta)),
                        ),
                )
                .child(
                    div()
                        .flex_1()
                        .min_h_0()
                        .mx_6()
                        .mb_6()
                        .rounded_xl()
                        .border_1()
                        .border_color(theme::line())
                        .overflow_hidden()
                        .child(chat.clone()),
                )
                .children(deleting)
                .into_any_element(),
        )
    }

    pub(super) fn render_history(&self, cx: &mut Context<Self>) -> AnyElement {
        if let Some(r) = self.render_reading(cx) {
            return r;
        }
        let project = self.project.clone().unwrap_or_default();
        let closed = self
            .sessions
            .closed
            .get(&project)
            .cloned()
            .unwrap_or_default();
        // SessionHistory: a header (px-8 pt-4 pb-3: Back to the chat, ghost
        // sm, and the title), then the list in a box mx-6 mb-6 rounded-2xl:
        // the hint (px-5 pt-3 pb-1), and the sessions (p-2 gap-px), each a
        // button px-3 py-2.5 rounded-xl, gap-1: its topic, its meta, and
        // the last line it said. A row opens the session to read.
        let caption = theme::text::CAPTION;
        v_flex()
            .id("session-history")
            .size_full()
            .child(
                h_flex()
                    .flex_none()
                    .w_full()
                    .px_8()
                    .pt_4()
                    .pb_3()
                    .gap_3()
                    .child(controls::button(
                        "history-back",
                        format!("← {}", t("sessions.backToChat")),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.sessions.history = false;
                            cx.notify();
                        }),
                    ))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_size(theme::text::HEADING)
                            .line_height(px(24.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::title())
                            .child(t("sessions.historyTitle")),
                    ),
            )
            .child(
                v_flex()
                    .id("session-history-list")
                    .flex_1()
                    .min_h_0()
                    .mx_6()
                    .mb_6()
                    .rounded_2xl()
                    .border_1()
                    .border_color(theme::line())
                    .overflow_y_scroll()
                    .when(closed.is_empty(), |el| {
                        el.child(
                            div()
                                .p_6()
                                .text_size(caption)
                                .text_color(theme::subtle())
                                .child(t("sessions.historyEmpty")),
                        )
                    })
                    .when(!closed.is_empty(), |el| {
                        el.child(
                            div()
                                .px_5()
                                .pt_3()
                                .pb_1()
                                .text_size(caption)
                                .line_height(px(18.))
                                .text_color(theme::subtle())
                                .child(t("sessions.historyHint")),
                        )
                    })
                    .child(
                        v_flex()
                            .p_2()
                            .gap_px()
                            .children(closed.into_iter().map(|s| {
                                let read = s.clone();
                                let when = |icon: Option<&'static str>, text: String| {
                                    h_flex()
                                        .gap_1()
                                        .when_some(icon, |el, i| {
                                            el.child(crate::icons::ic(i).size(px(12.)).flex_none())
                                        })
                                        .child(theme::fit(text, caption, FontWeight::NORMAL, false))
                                };
                                let meta = h_flex()
                                    .flex_wrap()
                                    .gap_x_3()
                                    .gap_y_1()
                                    .text_size(caption)
                                    .line_height(px(18.))
                                    .text_color(theme::subtle())
                                    .child(when(
                                        Some("calendar-days"),
                                        tv(
                                            "sessions.activeAgo",
                                            &[("when", time_ago(&s.active_at).into())],
                                        ),
                                    ))
                                    .when_some(s.closed_at.as_deref(), |el, c| {
                                        let key = if s.settled {
                                            "sessions.settledAgo"
                                        } else {
                                            "sessions.closedAgo"
                                        };
                                        el.child(when(
                                            None,
                                            tv(key, &[("when", time_ago(c).into())]),
                                        ))
                                    })
                                    .when(s.agents > 0, |el| {
                                        el.child(when(
                                            Some("bot"),
                                            tv(
                                                "sessions.agentCount",
                                                &[("count", s.agents.into())],
                                            ),
                                        ))
                                    });
                                let last = s
                                    .last
                                    .as_ref()
                                    .map(|l| l.text.clone())
                                    .filter(|t| !t.is_empty());
                                v_flex()
                                    .id(SharedString::from(format!("history-row-{}", s.id)))
                                    .w_full()
                                    .min_w_0()
                                    .gap_1()
                                    .px_3()
                                    .py_2p5()
                                    .rounded_xl()
                                    .cursor_pointer()
                                    .hover(|el| el.bg(theme::surface_raised()))
                                    .active(|s| s.bg(theme::surface_strong()))
                                    .on_click(cx.listener(
                                        move |this, _: &ClickEvent, window, cx| {
                                            this.read_closed(read.clone(), window, cx)
                                        },
                                    ))
                                    .child(
                                        div()
                                            .truncate()
                                            .text_size(theme::text::UI)
                                            .line_height(px(19.5))
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::primary())
                                            .child(topic(&s)),
                                    )
                                    .child(meta)
                                    .when_some(last, |el, l| {
                                        el.child(
                                            div()
                                                .min_w_0()
                                                .line_clamp(2)
                                                .text_size(caption)
                                                .line_height(px(18.))
                                                .text_color(theme::muted())
                                                .child(l),
                                        )
                                    })
                            })),
                    ),
            )
            .into_any_element()
    }

    /// render_snoozed_rows are a project's snoozed sessions, under its row
    /// in the sidebar: folded to a count, and open, each with when it comes
    /// back; clicking one wakes it now (SnoozedRows).
    pub(super) fn render_snoozed_rows(
        &self,
        project: &str,
        cx: &mut Context<Self>,
    ) -> Option<AnyElement> {
        let snoozed = self.sessions.snoozed(project);
        if snoozed.is_empty() {
            return None;
        }
        let open = self.sessions.snoozed_open.contains(project);
        let p = project.to_string();
        Some(
            v_flex()
                .ml(px(22.))
                .mr_1()
                .child(
                    h_flex()
                        .id(SharedString::from(format!("snoozed-{project}")))
                        .gap_1p5()
                        .px_2()
                        .py_0p5()
                        .rounded_md()
                        .cursor_pointer()
                        .text_xs()
                        .text_color(theme::faint())
                        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::muted()))
                        .child(crate::motion::chevron("chev-2167", open, None))
                        .child(crate::icons::of("⏰"))
                        .child(div().truncate().child(tv(
                            "sessions.snoozed.count",
                            &[("count", snoozed.len().into())],
                        )))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            if !this.sessions.snoozed_open.remove(&p) {
                                this.sessions.snoozed_open.insert(p.clone());
                            }
                            cx.notify();
                        })),
                )
                .when(open, |el| {
                    el.children(snoozed.into_iter().map(|s| {
                        let (p, id) = (s.project.clone(), s.id.clone());
                        h_flex()
                            .id(SharedString::from(format!(
                                "snoozed-{}-{}",
                                s.project, s.id
                            )))
                            .gap_2()
                            .py_0p5()
                            .pl_7()
                            .pr_2()
                            .rounded_md()
                            .cursor_pointer()
                            .text_xs()
                            .hover(|el| el.bg(theme::surface_raised()))
                            .active(|s| s.bg(theme::surface_strong()))
                            .tooltip(|w, cx| {
                                gpui_component::tooltip::Tooltip::new(t("sessions.snoozed.wake"))
                                    .build(w, cx)
                            })
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(theme::muted())
                                    .child(topic(s)),
                            )
                            .child(
                                div()
                                    .flex_none()
                                    .text_color(theme::faint())
                                    .child(wake_time(s.snoozed_until.as_deref().unwrap_or(""))),
                            )
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.snooze(p.clone(), id.clone(), None, window, cx)
                            }))
                    }))
                })
                .into_any_element(),
        )
    }

    /// project_sessions is how a project's sessions mark its row: any of
    /// them working, unread, or needing you.
    pub(super) fn project_sessions(&self, project: &str) -> (bool, bool, bool) {
        let list = self.sessions.lists.get(project);
        let active = self.project.as_deref() == Some(project);
        let iter = || list.into_iter().flatten();
        (
            iter().any(is_working),
            !active && iter().any(|s| self.sessions.unread(s)),
            iter().any(|s| status(s) == "needsYou"),
        )
    }

    /// ask_home hands a failed check to the Home chat: the daemon opens a
    /// session there with what to look into, which the app then shows
    /// (DoctorPanel's Ask Home to fix it).
    pub(super) fn ask_home(&mut self, check: String, window: &mut Window, cx: &mut Context<Self>) {
        if self.asking_home.is_some() {
            return;
        }
        self.asking_home = Some(check.clone());
        cx.notify();
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .post::<_, agentbox_api::DoctorHomeResult>(
                    "/v1/doctor/home",
                    &agentbox_api::DoctorHomeRequest { check },
                )
                .await;
            this.update_in(cx, |this, window, cx| {
                this.asking_home = None;
                match res {
                    Ok(r) => {
                        let home = agentbox_api::HOME_PROJECT.to_string();
                        this.sessions.choose(&home, &r.session);
                        this.load_sessions(home.clone(), cx);
                        this.select_project(home, window, cx);
                    }
                    Err(e) => Toast::new(Kind::Error, t("doctor.askHomeFailed"))
                        .description(e.to_string())
                        .show(cx),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }
}

#[cfg(test)]
mod epoch_tests {
    use super::Epochs;

    #[test]
    fn a_read_begun_before_a_session_was_added_is_stale() {
        let mut e = Epochs::default();
        let early = e.begin("p");
        assert!(e.current("p", early));
        e.changed("p");
        assert!(!e.current("p", early), "the early read lacks the session");
        let late = e.begin("p");
        assert!(e.current("p", late), "a read begun after is current");
    }

    #[test]
    fn projects_have_their_own_epochs() {
        let mut e = Epochs::default();
        let other = e.begin("q");
        e.changed("p");
        assert!(e.current("q", other));
    }
}
