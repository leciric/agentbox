//! Cockpit mode (option B, "river"), experimental behind the daemon's
//! Settings.cockpit_mode, as the Electron app's components/cockpit: a view
//! for very wide screens in two levels.
//!
//! All projects: every shown project's lead in one feed, each message tagged
//! by its project, a project filter (any number, down to one), what needs
//! you, and a composer that answers whichever lead it's aimed at — the chat's
//! own (ChatView with composer_only); beside it a live wall of every agent.
//! Focus: one project's lead chat (ChatView), one agent on a big stage with
//! its live visual (its desktop, VncView, or Android emulator, DeviceView)
//! or, with nothing visual, its terminal (TerminalView) and chat, and the
//! project's other agents in a strip under it.
//!
//! It covers the page and the panel under the top bar, and leaves what's
//! open beneath it alone: leaving (Esc, or the top bar's button) is back where
//! you were. What it decides is in logic.rs.

mod logic;

use std::collections::{HashMap, HashSet};
use std::time::Duration;

use agentbox_api::{
    AndroidStatus, BrowserStatus, ChatItem, ChatThread, ProjectChat, TerminalTail, TerminalTails,
};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::chat::{ChatAction, ChatView};
use crate::terminal::TerminalView;
use crate::theme;
use crate::vnc::VncView;
use agentbox_i18n::{t, tv};
use logic::{FEED_PER_PROJECT, FeedRow, Tone, Visual};

/// How often an open cockpit reads the leads, terminals and desktops again.
const POLL: Duration = Duration::from_secs(4);
/// How many desktops the wall streams at once; the rest show their terminal.
const LIVE_MAX: usize = 16;
const PREF_HIDDEN: &str = "cockpit.hidden";

#[derive(Clone, Debug, PartialEq, Eq, Default)]
pub(super) enum Level {
    #[default]
    All,
    Focus(String),
}

/// Cockpit is what the cockpit holds while the app runs: its level and
/// choices, what it read, and the views it shows.
#[derive(Default)]
pub(super) struct Cockpit {
    pub open: bool,
    /// The daemon's Settings.cockpit_mode, read at start (Settings' own copy
    /// is fresher once it's been opened).
    pub offered: bool,
    level: Level,
    hidden: HashSet<String>,
    hidden_loaded: bool,
    /// The agent chosen for each project's stage.
    staged: HashMap<String, String>,
    /// The lead the feed's composer answers.
    target: Option<String>,
    leads: HashMap<String, ProjectChat>,
    threads: HashMap<String, Vec<ChatItem>>,
    tails: HashMap<String, TerminalTail>,
    displays: HashMap<String, bool>,
    android: HashMap<String, AndroidStatus>,
    /// Live desktops, by agent ref: the wall's tiles and the stage.
    live: HashMap<String, Entity<VncView>>,
    reply: Option<(String, Entity<ChatView>)>,
    lead_chat: Option<(String, Entity<ChatView>)>,
    stage_chat: Option<(String, Entity<ChatView>)>,
    stage_term: Option<(String, Entity<TerminalView>)>,
    #[cfg(feature = "video")]
    stage_device: Option<(String, Entity<crate::device::DeviceView>)>,
    feed_scroll: ScrollHandle,
    feed_len: usize,
    subs: Vec<Subscription>,
    poll: Option<Task<()>>,
}

/// An agent as the cockpit shows it.
#[derive(Clone)]
struct Desk {
    r#ref: String,
    project: String,
    name: String,
    title: String,
    ai: String,
    tone: Tone,
    question: String,
}

impl AppView {
    /// cockpit_offered is whether Settings has cockpit mode on.
    pub(super) fn cockpit_offered(&self) -> bool {
        self.settings_page
            .settings
            .as_ref()
            .map_or(self.cockpit.offered, |s| s.cockpit_mode)
    }

    /// toggle_cockpit goes into cockpit mode, or back out of it.
    pub(super) fn toggle_cockpit(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.cockpit.open {
            self.leave_cockpit(cx);
        } else {
            self.open_cockpit(window, cx);
        }
    }

    pub(super) fn open_cockpit(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.cockpit.hidden_loaded {
            self.cockpit.hidden_loaded = true;
            self.cockpit.hidden = crate::prefs::get(PREF_HIDDEN)
                .and_then(|v| serde_json::from_value::<Vec<String>>(v).ok())
                .unwrap_or_default()
                .into_iter()
                .collect();
        }
        self.cockpit.open = true;
        self.cockpit.feed_len = 0;
        self.cockpit_refresh(window, cx);
        let poll = cx.spawn_in(window, async move |this, cx| {
            loop {
                cx.background_executor().timer(POLL).await;
                let alive = this
                    .update_in(cx, |this, window, cx| {
                        if this.cockpit.open {
                            this.cockpit_refresh(window, cx);
                        }
                        this.cockpit.open
                    })
                    .unwrap_or(false);
                if !alive {
                    break;
                }
            }
        });
        self.cockpit.poll = Some(poll);
        cx.notify();
    }

    /// open_cockpit_at opens it at start (AGENTBOX_NATIVE_COCKPIT): "1" on
    /// every project, "project" or "project/agent" focused there.
    pub(super) fn open_cockpit_at(
        &mut self,
        at: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.open_cockpit(window, cx);
        if at != "1" && !at.is_empty() {
            let project = at.split('/').next().unwrap_or_default().to_string();
            let staged = at.contains('/').then(|| at.to_string());
            self.cockpit_focus(project, staged, window, cx);
        }
    }

    /// leave_cockpit closes it, and lets go of its streams and chats: what
    /// was open beneath it is still there.
    pub(super) fn leave_cockpit(&mut self, cx: &mut Context<Self>) {
        let c = &mut self.cockpit;
        c.open = false;
        c.poll = None;
        c.live.clear();
        c.reply = None;
        c.lead_chat = None;
        c.stage_chat = None;
        c.stage_term = None;
        #[cfg(feature = "video")]
        {
            c.stage_device = None;
        }
        c.subs.clear();
        cx.notify();
    }

    fn cockpit_all(&self) -> Vec<String> {
        self.projects
            .iter()
            .filter(|p| p.name != agentbox_api::HOME_PROJECT)
            .map(|p| p.name.clone())
            .collect()
    }

    fn cockpit_shown(&self) -> Vec<String> {
        logic::shown_projects(&self.cockpit_all(), &self.cockpit.hidden)
    }

    /// desks are the agents of these projects, in the projects' order, then
    /// the order they were made.
    fn desks(&self, projects: &[String]) -> Vec<Desk> {
        let mut out = Vec::new();
        for p in projects {
            let asks: HashMap<&str, &str> = self
                .side
                .questions
                .get(p)
                .map(|qs| {
                    qs.iter()
                        .map(|q| (q.r#ref.as_str(), q.question.as_str()))
                        .collect()
                })
                .unwrap_or_default();
            let mut agents: Vec<_> = self
                .side
                .agents
                .iter()
                .filter(|a| &a.project == p)
                .collect();
            agents.sort_by(|a, b| a.created_at.cmp(&b.created_at));
            for a in agents {
                let ask = asks.get(a.r#ref.as_str()).copied();
                let tone = logic::tone_of(&a.state, &a.chat, &a.outcome, ask.is_some());
                let question = match tone {
                    Tone::Needs => ask
                        .map(str::to_string)
                        .unwrap_or_else(|| a.outcome_note.clone()),
                    Tone::Error => a.outcome_note.clone(),
                    _ => String::new(),
                };
                out.push(Desk {
                    r#ref: a.r#ref.clone(),
                    project: a.project.clone(),
                    name: a.name.clone(),
                    title: if a.title.is_empty() {
                        a.name.clone()
                    } else {
                        a.title.clone()
                    },
                    ai: a.ai.clone(),
                    tone,
                    question,
                });
            }
        }
        out
    }

    fn lead_tone(&self, project: &str) -> Tone {
        match self.cockpit.leads.get(project).map(|l| l.chat.as_str()) {
            Some("waiting") => Tone::Needs,
            Some("running" | "starting") => Tone::Working,
            _ => Tone::Idle,
        }
    }

    fn lead_prefix(project: &str, lead: &ProjectChat) -> String {
        if lead.session.is_empty() {
            format!("/v1/projects/{project}")
        } else {
            format!("/v1/projects/{project}/sessions/{}", lead.session)
        }
    }

    /// The projects the cockpit reads now: the shown ones, or the one in focus.
    fn cockpit_projects(&self) -> Vec<String> {
        match &self.cockpit.level {
            Level::All => self.cockpit_shown(),
            Level::Focus(p) => vec![p.clone()],
        }
    }

    /// cockpit_refresh reads what the cockpit shows again: each lead and its
    /// latest messages, the agents' terminals, desktops and phones.
    fn cockpit_refresh(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let projects = self.cockpit_projects();
        let desks = self.desks(&projects);
        let client = self.client.clone();
        let staged = match &self.cockpit.level {
            Level::Focus(p) => self.staged_desk(p).map(|d| d.r#ref),
            Level::All => None,
        };
        cx.spawn_in(window, async move |this, cx| {
            let mut leads = Vec::new();
            for p in &projects {
                if let Ok(lead) = client
                    .get::<ProjectChat>(&format!("/v1/projects/{p}/lead"))
                    .await
                {
                    let path = format!("{}/chat?limit=20", Self::lead_prefix(p, &lead));
                    let thread = client.get::<ChatThread>(&path).await.ok();
                    leads.push((p.clone(), lead, thread));
                }
            }
            let mut tails = Vec::new();
            for p in &projects {
                let names: Vec<&str> = desks
                    .iter()
                    .filter(|d| &d.project == p && d.tone != Tone::Stopped)
                    .map(|d| d.name.as_str())
                    .collect();
                if names.is_empty() {
                    continue;
                }
                let path = format!(
                    "/v1/projects/{p}/terminals?agents={}&lines=8",
                    names.join(",")
                );
                if let Ok(t) = client.get::<TerminalTails>(&path).await {
                    tails.extend(t.agents);
                }
            }
            let mut displays = Vec::new();
            for d in desks.iter().filter(|d| d.tone != Tone::Stopped) {
                let path = format!("/v1/agents/{}/{}/browser", d.project, d.name);
                if let Ok(b) = client.get::<BrowserStatus>(&path).await {
                    displays.push((d.r#ref.clone(), b.display));
                }
            }
            let mut android = None;
            if let Some(r) = &staged {
                let (p, n) = super::agent::split(r);
                if let Ok(s) = client
                    .get::<AndroidStatus>(&format!("/v1/agents/{p}/{n}/android"))
                    .await
                {
                    android = Some((r.clone(), s));
                }
            }
            this.update_in(cx, |this, window, cx| {
                let c = &mut this.cockpit;
                for (p, lead, thread) in leads {
                    if let Some(th) = thread {
                        c.threads.insert(p.clone(), th.items);
                    }
                    c.leads.insert(p, lead);
                }
                for tail in tails {
                    c.tails.insert(tail.r#ref.clone(), tail);
                }
                for (r, up) in displays {
                    c.displays.insert(r, up);
                }
                if let Some((r, s)) = android {
                    c.android.insert(r, s);
                }
                this.cockpit_sync(window, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn staged_desk(&self, project: &str) -> Option<Desk> {
        let desks = self.desks(&[project.to_string()]);
        let keys: Vec<(String, Tone)> = desks.iter().map(|d| (d.r#ref.clone(), d.tone)).collect();
        let r = logic::staged_of(&keys, self.cockpit.staged.get(project).map(String::as_str))?
            .to_string();
        desks.into_iter().find(|d| d.r#ref == r)
    }

    fn visual(&self, r: &str) -> Visual {
        let booted = self
            .cockpit
            .android
            .get(r)
            .is_some_and(|s| s.running && s.booted);
        logic::visual_of(
            booted,
            self.cockpit.displays.get(r).copied().unwrap_or(false),
        )
    }

    /// target is the lead the feed's composer answers: the one chosen, else a
    /// lead waiting on you, else whoever spoke last.
    fn cockpit_target(&self, shown: &[String], rows: &[FeedRow]) -> Option<String> {
        self.cockpit
            .target
            .clone()
            .filter(|t| shown.contains(t))
            .or_else(|| {
                shown
                    .iter()
                    .find(|p| self.lead_tone(p) == Tone::Needs)
                    .cloned()
            })
            .or_else(|| rows.last().map(|r| r.project.clone()))
            .or_else(|| shown.first().cloned())
    }

    fn cockpit_rows(&self, shown: &[String]) -> Vec<FeedRow> {
        logic::feed_of(
            shown.iter().filter_map(|p| {
                self.cockpit
                    .threads
                    .get(p)
                    .map(|items| (p.as_str(), items.as_slice()))
            }),
            FEED_PER_PROJECT,
        )
    }

    fn cockpit_chat(
        &mut self,
        prefix: String,
        r: String,
        title: String,
        composer_only: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Entity<ChatView> {
        let client = self.client.clone();
        let chat = cx.new(|cx| {
            let mut c = ChatView::new(client, prefix, r.clone(), title.into(), window, cx);
            c.header = false;
            c.composer_only = composer_only;
            if !r.ends_with("/lead") && !r.contains("/lead-") {
                c.agent_name = r.rsplit('/').next().unwrap_or_default().to_string();
            }
            c
        });
        self.cockpit
            .subs
            .push(cx.subscribe_in(&chat, window, Self::on_cockpit_chat));
        chat
    }

    /// on_cockpit_chat is what a chat in the cockpit asks of the app: a
    /// message's way out is marked as the app's own chat marks it; anything
    /// that opens a page leaves the cockpit for it.
    fn on_cockpit_chat(
        &mut self,
        chat: &Entity<ChatView>,
        ev: &ChatAction,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        match ev {
            ChatAction::Sent(r) => self.mark_sent(r, cx),
            ChatAction::SendFailed(r) => self.unmark_sent(r, cx),
            ChatAction::OpenAgent(_)
            | ChatAction::OpenProject(_)
            | ChatAction::OpenReview(_)
            | ChatAction::Merge(..) => {
                self.leave_cockpit(cx);
                self.on_chat_action(chat, ev, window, cx);
            }
            // The rest act on the chat open in the app: open this one there.
            _ => {
                let r = chat.read(cx).agent.clone();
                self.leave_cockpit(cx);
                let project = r.split('/').next().unwrap_or_default().to_string();
                if r.contains("/lead") {
                    self.select_project(project, window, cx);
                } else {
                    self.open_agent_ref(r, window, cx);
                }
            }
        }
    }

    /// cockpit_sync makes and drops the views the level shows: the live
    /// desktops, the composer, the lead's chat, the stage's chat, terminal
    /// and phone.
    fn cockpit_sync(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.cockpit.open {
            return;
        }
        let level = self.cockpit.level.clone();
        let projects = self.cockpit_projects();
        let desks = self.desks(&projects);
        // Live desktops: every shown agent whose display is up, at most LIVE_MAX.
        let want: Vec<String> = desks
            .iter()
            .filter(|d| {
                self.cockpit
                    .displays
                    .get(&d.r#ref)
                    .copied()
                    .unwrap_or(false)
            })
            .take(LIVE_MAX)
            .map(|d| d.r#ref.clone())
            .collect();
        self.cockpit.live.retain(|r, _| want.contains(r));
        for r in want {
            if !self.cockpit.live.contains_key(&r) {
                let (p, n) = super::agent::split(&r);
                let client = self.client.clone();
                let v = cx.new(|cx| {
                    let mut v =
                        VncView::new(client, format!("/v1/agents/{p}/{n}/browser/view"), cx);
                    v.control = false;
                    v
                });
                self.cockpit.live.insert(r, v);
            }
        }
        match level {
            Level::All => {
                self.cockpit.lead_chat = None;
                self.cockpit.stage_chat = None;
                self.cockpit.stage_term = None;
                #[cfg(feature = "video")]
                {
                    self.cockpit.stage_device = None;
                }
                let shown = self.cockpit_shown();
                let rows = self.cockpit_rows(&shown);
                let target = self.cockpit_target(&shown, &rows);
                let lead = target
                    .as_ref()
                    .and_then(|p| self.cockpit.leads.get(p).cloned().map(|l| (p.clone(), l)));
                match lead {
                    Some((p, l))
                        if self
                            .cockpit
                            .reply
                            .as_ref()
                            .is_none_or(|(r, _)| *r != l.r#ref) =>
                    {
                        let title = self.label_of(&p);
                        let chat = self.cockpit_chat(
                            Self::lead_prefix(&p, &l),
                            l.r#ref.clone(),
                            title,
                            true,
                            window,
                            cx,
                        );
                        self.cockpit.reply = Some((l.r#ref.clone(), chat));
                    }
                    None => self.cockpit.reply = None,
                    _ => {}
                }
            }
            Level::Focus(p) => {
                self.cockpit.reply = None;
                if let Some(l) = self.cockpit.leads.get(&p).cloned()
                    && self
                        .cockpit
                        .lead_chat
                        .as_ref()
                        .is_none_or(|(r, _)| *r != l.r#ref)
                {
                    let title = format!("{} · {}", self.label_of(&p), t("v2.header.lead"));
                    let chat = self.cockpit_chat(
                        Self::lead_prefix(&p, &l),
                        l.r#ref.clone(),
                        title,
                        false,
                        window,
                        cx,
                    );
                    self.cockpit.lead_chat = Some((l.r#ref, chat));
                }
                let staged = self.staged_desk(&p);
                match staged {
                    Some(d) => {
                        if self
                            .cockpit
                            .stage_chat
                            .as_ref()
                            .is_none_or(|(r, _)| *r != d.r#ref)
                        {
                            let chat = self.cockpit_chat(
                                format!("/v1/agents/{}/{}", d.project, d.name),
                                d.r#ref.clone(),
                                format!("{} · {}", d.name, d.title),
                                false,
                                window,
                                cx,
                            );
                            self.cockpit.stage_chat = Some((d.r#ref.clone(), chat));
                        }
                        let visual = self.visual(&d.r#ref);
                        if visual == Visual::None {
                            if self
                                .cockpit
                                .stage_term
                                .as_ref()
                                .is_none_or(|(r, _)| *r != d.r#ref)
                            {
                                let client = self.client.clone();
                                let path = format!("/v1/agents/{}/{}/terminal", d.project, d.name);
                                let term = cx.new(|cx| TerminalView::new(client, path, cx));
                                self.cockpit.stage_term = Some((d.r#ref.clone(), term));
                            }
                        } else {
                            self.cockpit.stage_term = None;
                        }
                        #[cfg(feature = "video")]
                        {
                            if visual == Visual::Android {
                                if self
                                    .cockpit
                                    .stage_device
                                    .as_ref()
                                    .is_none_or(|(r, _)| *r != d.r#ref)
                                {
                                    let client = self.client.clone();
                                    let path = format!(
                                        "/v1/agents/{}/{}/android/stream",
                                        d.project, d.name
                                    );
                                    let v = cx.new(|cx| {
                                        crate::device::DeviceView::new(client, path, false, cx)
                                    });
                                    self.cockpit.stage_device = Some((d.r#ref.clone(), v));
                                }
                            } else {
                                self.cockpit.stage_device = None;
                            }
                        }
                    }
                    None => {
                        self.cockpit.stage_chat = None;
                        self.cockpit.stage_term = None;
                        #[cfg(feature = "video")]
                        {
                            self.cockpit.stage_device = None;
                        }
                    }
                }
            }
        }
    }

    fn cockpit_level(&mut self, level: Level, window: &mut Window, cx: &mut Context<Self>) {
        if self.cockpit.level != level {
            self.cockpit.level = level;
            self.cockpit.feed_len = 0;
            self.cockpit_sync(window, cx);
            self.cockpit_refresh(window, cx);
        }
        cx.notify();
    }

    fn cockpit_focus(
        &mut self,
        project: String,
        staged: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if let Some(r) = staged {
            self.cockpit.staged.insert(project.clone(), r);
        }
        if self.cockpit.level == Level::Focus(project.clone()) {
            self.cockpit_sync(window, cx);
            cx.notify();
        } else {
            self.cockpit_level(Level::Focus(project), window, cx);
        }
    }

    fn cockpit_set_hidden(
        &mut self,
        hidden: HashSet<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let mut list: Vec<&String> = hidden.iter().collect();
        list.sort();
        crate::prefs::set(PREF_HIDDEN, serde_json::json!(list));
        self.cockpit.hidden = hidden;
        self.cockpit_sync(window, cx);
        self.cockpit_refresh(window, cx);
        cx.notify();
    }

    // ——— Drawing ———

    /// render_cockpit is the cockpit, in the page's and the panel's place.
    pub(super) fn render_cockpit(
        &mut self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let level = self.cockpit.level.clone();
        let projects = self.cockpit_all();
        let needs: usize = self
            .desks(&projects)
            .iter()
            .filter(|d| d.tone.needy())
            .count()
            + projects
                .iter()
                .filter(|p| self.lead_tone(p) == Tone::Needs)
                .count();
        let body = match &level {
            Level::All => self.render_cockpit_all(window, cx),
            Level::Focus(p) => self.render_cockpit_focus(p.clone(), window, cx),
        };
        v_flex()
            .id("cockpit")
            .size_full()
            .min_h_0()
            .bg(theme::ink())
            .child(
                h_flex()
                    .flex_none()
                    .h(px(48.))
                    .px_4()
                    .gap_4()
                    .border_b_1()
                    .border_color(theme::line_faint())
                    .bg(theme::rail())
                    .child(self.render_level_switch(&level, cx))
                    .when(matches!(level, Level::Focus(_)), |el| {
                        el.child(self.render_focus_chips(&level, cx))
                    })
                    .child(div().flex_1())
                    .when(needs > 0, |el| {
                        el.child(
                            h_flex()
                                .flex_none()
                                .h(px(28.))
                                .px_2p5()
                                .gap_1p5()
                                .rounded_md()
                                .bg(theme::tint(theme::amber(400), 0.15))
                                .text_size(theme::text::UI)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::amber(200))
                                .child(crate::icons::ic("bell-ring").size(px(14.)))
                                .child(tv("cockpit.needs.total", &[("count", needs.into())])),
                        )
                    })
                    .child(
                        div()
                            .flex_none()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(t("cockpit.escHint")),
                    ),
            )
            .child(div().flex_1().min_h_0().child(body))
            .into_any_element()
    }

    fn render_level_switch(&self, level: &Level, cx: &mut Context<Self>) -> AnyElement {
        let seg = |id: &'static str, on: bool, icon: &str, label: String| {
            h_flex()
                .id(id)
                .h_full()
                .px_2p5()
                .gap_1p5()
                .rounded_md()
                .cursor_pointer()
                .when(on, |el| {
                    el.bg(theme::surface_strong()).text_color(theme::title())
                })
                .when(!on, |el| {
                    el.text_color(theme::muted())
                        .hover(|s| s.text_color(theme::primary()))
                })
                .child(crate::icons::ic(icon).size(px(14.)))
                .child(label)
        };
        let focus_label = match level {
            Level::Focus(p) => self.label_of(p),
            Level::All => t("cockpit.level.focus"),
        };
        // Focus goes to the project last in focus, else one waiting on you,
        // else the first shown.
        let last = self.cockpit.staged.keys().next().cloned();
        let shown = self.cockpit_shown();
        let next = match level {
            Level::Focus(p) => p.clone(),
            Level::All => shown
                .iter()
                .find(|p| {
                    self.lead_tone(p) == Tone::Needs
                        || self.desks(&[(*p).clone()]).iter().any(|d| d.tone.needy())
                })
                .cloned()
                .or(last)
                .or_else(|| shown.first().cloned())
                .unwrap_or_default(),
        };
        h_flex()
            .flex_none()
            .h(px(32.))
            .p(px(2.))
            .rounded_lg()
            .border_1()
            .border_color(theme::line_strong())
            .bg(theme::surface_faint())
            .text_size(theme::text::UI)
            .child(
                seg(
                    "cockpit-all",
                    *level == Level::All,
                    "layers",
                    t("cockpit.level.all"),
                )
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.cockpit_level(Level::All, window, cx)
                })),
            )
            .child(
                seg(
                    "cockpit-focus",
                    matches!(level, Level::Focus(_)),
                    "maximize-2",
                    focus_label,
                )
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    if !next.is_empty() {
                        this.cockpit_focus(next.clone(), None, window, cx)
                    }
                })),
            )
            .into_any_element()
    }

    /// render_focus_chips switches focus to another project, those waiting
    /// on you counted.
    fn render_focus_chips(&self, level: &Level, cx: &mut Context<Self>) -> AnyElement {
        let current = match level {
            Level::Focus(p) => p.clone(),
            Level::All => String::new(),
        };
        let mut row = h_flex().min_w_0().gap_1p5().overflow_hidden();
        for (ix, p) in self.cockpit_all().into_iter().enumerate() {
            if p == current {
                continue;
            }
            let n = self
                .desks(std::slice::from_ref(&p))
                .iter()
                .filter(|d| d.tone.needy())
                .count()
                + usize::from(self.lead_tone(&p) == Tone::Needs);
            let label = self.label_of(&p);
            let icon = self
                .projects
                .iter()
                .find(|x| x.name == p)
                .map(|x| self.project_icon(x, 16.));
            row = row.child(
                h_flex()
                    .id(("cockpit-chip", ix))
                    .flex_none()
                    .h(px(24.))
                    .px_2()
                    .gap_1p5()
                    .rounded_full()
                    .border_1()
                    .border_color(theme::line_faint())
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::secondary())
                    .cursor_pointer()
                    .hover(|s| s.bg(theme::surface_raised()))
                    .children(icon)
                    .child(label)
                    .when(n > 0, |el| el.child(count_badge(n)))
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.cockpit_focus(p.clone(), None, window, cx)
                    })),
            );
        }
        row.into_any_element()
    }

    // ——— All projects ———

    fn render_cockpit_all(&mut self, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let width = f32::from(window.viewport_size().width);
        let height = f32::from(window.viewport_size().height);
        let feed_w = (width * 0.32).clamp(544., 1408.);
        let rail_w = 288.;
        let shown = self.cockpit_shown();
        let desks = self.desks(&shown);
        let wall_w = (width - rail_w - feed_w - 32.).max(300.);
        // The window less the top bar, the cockpit's own bar, the wall's
        // heading and padding.
        let wall_h = (height - 56. - 48. - 32. - 40.).max(200.);
        h_flex()
            .size_full()
            .min_h_0()
            .items_start()
            .child(self.render_rail(rail_w, cx))
            .child(
                v_flex()
                    .flex_none()
                    .w(px(feed_w))
                    .h_full()
                    .border_r_1()
                    .border_color(theme::line_faint())
                    .bg(theme::chat())
                    .child(self.render_river(&shown, cx)),
            )
            .child(self.render_wall(&desks, wall_w, wall_h, cx))
            .into_any_element()
    }

    fn render_rail(&self, w: f32, cx: &mut Context<Self>) -> AnyElement {
        let all = self.cockpit_all();
        let shown = self.cockpit_shown();
        let mut list = v_flex()
            .gap(px(2.))
            .child(section_label(t("cockpit.filter.title")));
        for (ix, p) in all.iter().enumerate() {
            let on = shown.contains(p);
            let lead = self.lead_tone(p);
            let desks = self.desks(std::slice::from_ref(p));
            let n =
                desks.iter().filter(|d| d.tone.needy()).count() + usize::from(lead == Tone::Needs);
            let status = format!(
                "{} · {}",
                t(match lead {
                    Tone::Needs => "cockpit.lead.waits",
                    Tone::Working => "cockpit.lead.working",
                    _ => "cockpit.lead.quiet",
                }),
                tv("cockpit.agents", &[("count", desks.len().into())])
            );
            let icon = self
                .projects
                .iter()
                .find(|x| &x.name == p)
                .map(|x| self.project_icon(x, 20.));
            let (toggle, only) = (p.clone(), p.clone());
            list = list.child(
                h_flex()
                    .id(("cockpit-filter", ix))
                    .group("cockpit-filter-row")
                    .relative()
                    .h(px(48.))
                    .px_2()
                    .gap_2p5()
                    .rounded_md()
                    .hover(|s| s.bg(theme::surface_raised()))
                    .text_color(if on { theme::primary() } else { theme::faint() })
                    .child(
                        h_flex()
                            .id(("cockpit-filter-toggle", ix))
                            .flex_1()
                            .min_w_0()
                            .gap_2p5()
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                let next = logic::toggle_shown(
                                    &this.cockpit_all(),
                                    &this.cockpit.hidden,
                                    &toggle,
                                );
                                this.cockpit_set_hidden(next, window, cx);
                            }))
                            .child(
                                div()
                                    .flex_none()
                                    .size_4()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .rounded_sm()
                                    .border_1()
                                    .when(on, |el| {
                                        el.border_color(theme::brand(400))
                                            .bg(theme::brand(500))
                                            .text_color(theme::on_brand())
                                            .child(crate::icons::ic("check").size(px(12.)))
                                    })
                                    .when(!on, |el| el.border_color(theme::line_strong())),
                            )
                            .children(icon.map(|i| {
                                div().flex_none().when(!on, |el| el.opacity(0.5)).child(i)
                            }))
                            .child(
                                v_flex()
                                    .min_w_0()
                                    .flex_1()
                                    .child(
                                        div()
                                            .truncate()
                                            .text_size(theme::text::UI)
                                            .font_weight(FontWeight::MEDIUM)
                                            .child(self.label_of(p)),
                                    )
                                    .child(
                                        div()
                                            .truncate()
                                            .text_size(theme::text::CAPTION)
                                            .text_color(if lead == Tone::Needs {
                                                theme::amber(300)
                                            } else {
                                                theme::faint()
                                            })
                                            .child(status),
                                    ),
                            ),
                    )
                    .child(
                        div()
                            .id(("cockpit-only", ix))
                            // Over the row's end on hover: it takes no room.
                            .absolute()
                            .right_2()
                            .bg(theme::surface_raised())
                            .opacity(0.)
                            .group_hover("cockpit-filter-row", |s| s.opacity(1.))
                            .px_1p5()
                            .rounded_sm()
                            .border_1()
                            .border_color(theme::line())
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::muted())
                            .cursor_pointer()
                            .hover(|s| s.text_color(theme::primary()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                let next = logic::only_shown(
                                    &this.cockpit_all(),
                                    &this.cockpit.hidden,
                                    &only,
                                );
                                this.cockpit_set_hidden(next, window, cx);
                            }))
                            .child(t("cockpit.filter.only")),
                    )
                    .when(n > 0, |el| el.child(count_badge(n))),
            );
        }
        // What needs you: leads waiting, then agents asking or failed.
        let mut needs = v_flex()
            .gap_1p5()
            .child(section_label(t("cockpit.needs.title")));
        let mut any = false;
        let mut ix = 0usize;
        for p in &shown {
            let mut items: Vec<(Option<Desk>, Tone)> = Vec::new();
            if self.lead_tone(p) == Tone::Needs {
                items.push((None, Tone::Needs));
            }
            for d in self
                .desks(std::slice::from_ref(p))
                .into_iter()
                .filter(|d| d.tone.needy())
            {
                let tone = d.tone;
                items.push((Some(d), tone));
            }
            for (d, tone) in items {
                any = true;
                ix += 1;
                let (project, staged) = (p.clone(), d.as_ref().map(|d| d.r#ref.clone()));
                let who = d
                    .as_ref()
                    .map_or_else(|| t("cockpit.lead"), |d| d.title.clone());
                let text = match &d {
                    Some(d) if !d.question.is_empty() => d.question.clone(),
                    Some(_) if tone == Tone::Error => t("cockpit.agent.failed"),
                    Some(_) => t("cockpit.agent.asks"),
                    None => t("cockpit.lead.waits"),
                };
                let hue = if tone == Tone::Error {
                    theme::rose(400)
                } else {
                    theme::amber(400)
                };
                needs = needs.child(
                    v_flex()
                        .id(("cockpit-need", ix))
                        .gap_1()
                        .px_2p5()
                        .py_2()
                        .rounded_md()
                        .border_1()
                        .border_color(theme::tint(hue, 0.3))
                        .bg(theme::tint(hue, 0.05))
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::tint(hue, 0.1)))
                        .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.cockpit_focus(project.clone(), staged.clone(), window, cx)
                        }))
                        .child(
                            h_flex()
                                .min_w_0()
                                .gap_1p5()
                                .text_size(theme::text::CAPTION)
                                .child(
                                    div()
                                        .flex_none()
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::primary())
                                        .child(self.label_of(p)),
                                )
                                .child(div().truncate().text_color(theme::muted()).child(who)),
                        )
                        .child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::secondary())
                                .line_clamp(2)
                                .child(text),
                        ),
                );
            }
        }
        if !any {
            needs = needs.child(
                div()
                    .px_2()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::faint())
                    .child(t("cockpit.needs.none")),
            );
        }
        v_flex()
            .id("cockpit-rail")
            .flex_none()
            .w(px(w))
            .h_full()
            .overflow_y_scroll()
            .gap_6()
            .p_3()
            .border_r_1()
            .border_color(theme::line_faint())
            .bg(theme::rail())
            .child(list)
            .child(needs)
            .into_any_element()
    }

    fn render_river(&mut self, shown: &[String], cx: &mut Context<Self>) -> AnyElement {
        let rows = self.cockpit_rows(shown);
        let target = self.cockpit_target(shown, &rows);
        // The feed stays at its newest message as messages arrive.
        if rows.len() != self.cockpit.feed_len {
            self.cockpit.feed_len = rows.len();
            self.cockpit.feed_scroll.scroll_to_bottom();
        }
        let mut last: HashMap<&str, usize> = HashMap::new();
        for (i, r) in rows.iter().enumerate() {
            last.insert(r.project.as_str(), i);
        }
        let mut feed = v_flex()
            .id("cockpit-feed")
            .track_scroll(&self.cockpit.feed_scroll)
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .gap_1()
            .px_4()
            .py_3();
        if rows.is_empty() {
            feed = feed.child(
                div()
                    .py_8()
                    .text_center()
                    .text_size(theme::text::UI)
                    .text_color(theme::faint())
                    .child(if self.cockpit.leads.is_empty() {
                        t("common.loading")
                    } else {
                        t("cockpit.river.empty")
                    }),
            );
        }
        for (i, r) in rows.iter().enumerate() {
            let me = r.item.kind == "user";
            let is_target = target.as_deref() == Some(r.project.as_str());
            let (reply, focus) = (r.project.clone(), r.project.clone());
            let icon = self
                .projects
                .iter()
                .find(|x| x.name == r.project)
                .map(|x| self.project_icon(x, 16.));
            let mut block = v_flex().gap_1p5().child(
                h_flex()
                    .id(("cockpit-row", i))
                    .group("cockpit-row")
                    .relative()
                    .items_start()
                    .gap_3()
                    .py_1p5()
                    .pl_3()
                    .pr_2()
                    .rounded_md()
                    .border_l_2()
                    .border_color(if is_target {
                        theme::brand(400)
                    } else {
                        theme::line_faint()
                    })
                    .when(is_target, |el| el.bg(theme::surface_faint()))
                    .child(
                        div()
                            .flex_none()
                            .w(px(64.))
                            .pt_0p5()
                            .text_right()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(clock(&r.item.created_at)),
                    )
                    .child(
                        v_flex()
                            .min_w_0()
                            .flex_1()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_1p5()
                                    .text_size(theme::text::CAPTION)
                                    .child(
                                        h_flex()
                                            .id(("cockpit-row-project", i))
                                            .gap_1p5()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::title())
                                            .cursor_pointer()
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, window, cx| {
                                                    this.cockpit_focus(
                                                        focus.clone(),
                                                        None,
                                                        window,
                                                        cx,
                                                    )
                                                },
                                            ))
                                            .children(icon)
                                            .child(self.label_of(&r.project)),
                                    )
                                    .child(div().text_color(theme::faint()).child("·"))
                                    .child(div().text_color(theme::muted()).child(if me {
                                        t("cockpit.you")
                                    } else {
                                        t("cockpit.lead")
                                    })),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::UI)
                                    .text_color(if me {
                                        theme::primary()
                                    } else {
                                        theme::secondary()
                                    })
                                    .line_clamp(8)
                                    .child(r.item.text.trim().to_string()),
                            ),
                    )
                    .child(
                        h_flex()
                            .id(("cockpit-reply", i))
                            .absolute()
                            .top(px(-8.))
                            .right_2()
                            .opacity(0.)
                            .group_hover("cockpit-row", |s| s.opacity(1.))
                            .h(px(28.))
                            .px_2()
                            .gap_1()
                            .rounded_md()
                            .border_1()
                            .border_color(theme::line_strong())
                            .bg(theme::overlay())
                            .shadow(theme::shadow_pop())
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::primary())
                            .cursor_pointer()
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.cockpit.target = Some(reply.clone());
                                this.cockpit_sync(window, cx);
                                cx.notify();
                            }))
                            .child(crate::icons::ic("message-square").size(px(14.)))
                            .child(tv(
                                "cockpit.river.reply",
                                &[("project", self.label_of(&r.project).into())],
                            )),
                    ),
            );
            if last.get(r.project.as_str()) == Some(&i) {
                match self.lead_tone(&r.project) {
                    Tone::Working => {
                        block = block.child(
                            h_flex()
                                .pl(px(92.))
                                .gap_1p5()
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::muted())
                                .child(crate::icons::spinner())
                                .child(tv(
                                    "cockpit.river.writing",
                                    &[("project", self.label_of(&r.project).into())],
                                )),
                        )
                    }
                    Tone::Needs => {
                        let p = r.project.clone();
                        block = block.child(
                            h_flex()
                                .id(("cockpit-waits", i))
                                .ml(px(92.))
                                .px_2p5()
                                .py_1p5()
                                .gap_2()
                                .rounded_md()
                                .border_1()
                                .border_color(theme::tint(theme::amber(400), 0.5))
                                .bg(theme::tint(theme::amber(400), 0.1))
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::amber(200))
                                .cursor_pointer()
                                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                    this.cockpit.target = Some(p.clone());
                                    this.cockpit_sync(window, cx);
                                    cx.notify();
                                }))
                                .child(crate::icons::ic("bell-ring").size(px(14.)))
                                .child(tv(
                                    "cockpit.river.waits",
                                    &[("project", self.label_of(&r.project).into())],
                                )),
                        )
                    }
                    _ => {}
                }
            }
            feed = feed.child(block);
        }
        // To: which lead the composer answers.
        let mut to = h_flex()
            .flex_wrap()
            .gap_1p5()
            .text_size(theme::text::CAPTION)
            .child(
                div()
                    .text_color(theme::muted())
                    .child(t("cockpit.river.to")),
            );
        for (ix, p) in shown.iter().enumerate() {
            let on = target.as_deref() == Some(p.as_str());
            let waits = self.lead_tone(p) == Tone::Needs;
            let pick = p.clone();
            to = to.child(
                h_flex()
                    .id(("cockpit-to", ix))
                    .h(px(24.))
                    .px_2()
                    .gap_1()
                    .rounded_md()
                    .cursor_pointer()
                    .when(on, |el| {
                        el.bg(theme::surface_strong()).text_color(theme::title())
                    })
                    .when(!on, |el| {
                        el.text_color(theme::muted())
                            .hover(|s| s.bg(theme::surface_raised()))
                    })
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.cockpit.target = Some(pick.clone());
                        this.cockpit_sync(window, cx);
                        cx.notify();
                    }))
                    .child(self.label_of(p))
                    .when(waits, |el| {
                        el.child(
                            crate::icons::ic("bell-ring")
                                .size(px(12.))
                                .text_color(theme::amber(300)),
                        )
                    }),
            );
        }
        v_flex()
            .size_full()
            .min_h_0()
            .child(feed)
            .child(
                v_flex()
                    .flex_none()
                    .gap_1p5()
                    .pt_2()
                    .border_t_1()
                    .border_color(theme::line_faint())
                    .child(div().px_3().child(to))
                    .children(self.cockpit.reply.as_ref().map(|(_, c)| c.clone())),
            )
            .into_any_element()
    }

    fn render_wall(&self, desks: &[Desk], w: f32, h: f32, cx: &mut Context<Self>) -> AnyElement {
        let gap = 8.;
        let cols = logic::fit_wall(desks.len(), w, h, gap, 28., 300.);
        let tile_w = ((w - gap * (cols as f32 - 1.)) / cols as f32).max(100.);
        let working = desks.iter().filter(|d| d.tone == Tone::Working).count();
        let waiting = desks.iter().filter(|d| d.tone.needy()).count();
        let mut grid = v_flex().gap(px(gap));
        for (row, chunk) in desks.chunks(cols).enumerate() {
            let mut line = h_flex().gap(px(gap)).items_start();
            for (col, d) in chunk.iter().enumerate() {
                line = line.child(self.render_tile(d, row * cols + col, tile_w, true, cx));
            }
            grid = grid.child(line);
        }
        v_flex()
            .flex_1()
            .min_w_0()
            .h_full()
            .gap_3()
            .p_4()
            .child(
                h_flex()
                    .gap_2()
                    .text_size(theme::text::UI)
                    .child(
                        crate::icons::ic("monitor")
                            .size(px(16.))
                            .text_color(theme::muted()),
                    )
                    .child(
                        div()
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(theme::title())
                            .child(t("cockpit.wall.title")),
                    )
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(tv(
                                "cockpit.wall.counts",
                                &[
                                    ("agents", desks.len().into()),
                                    ("working", working.into()),
                                    ("waiting", waiting.into()),
                                ],
                            )),
                    ),
            )
            .child(
                div()
                    .id("cockpit-wall")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .when(desks.is_empty(), |el| {
                        el.flex()
                            .items_center()
                            .justify_center()
                            .text_color(theme::faint())
                            .child(t("cockpit.wall.empty"))
                    })
                    .when(!desks.is_empty(), |el| el.child(grid)),
            )
            .into_any_element()
    }

    /// render_tile is an agent on the wall or in the strip: its desktop live
    /// while it's up, else the end of its terminal; flagged when it needs you.
    fn render_tile(
        &self,
        d: &Desk,
        ix: usize,
        w: f32,
        with_project: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let h = w * 10. / 16.;
        let screen: AnyElement = match self.cockpit.live.get(&d.r#ref) {
            Some(v) => div().size_full().child(v.clone()).into_any_element(),
            None => {
                let lines = self
                    .cockpit
                    .tails
                    .get(&d.r#ref)
                    .map(|t| t.lines.clone())
                    .unwrap_or_default();
                let from = lines.len().saturating_sub(8);
                v_flex()
                    .size_full()
                    .justify_end()
                    .p_2()
                    .overflow_hidden()
                    .font_family(theme::MONO)
                    .text_size(theme::text::MICRO)
                    .text_color(theme::secondary())
                    .when(lines.is_empty(), |el| {
                        el.items_center().justify_center().child(
                            crate::icons::ic("square-terminal")
                                .size(px(20.))
                                .text_color(theme::faint()),
                        )
                    })
                    .children(
                        lines[from..]
                            .iter()
                            .map(|l| div().truncate().child(strip_ansi(l))),
                    )
                    .into_any_element()
            }
        };
        let hue = match d.tone {
            Tone::Needs => Some(theme::amber(400)),
            Tone::Error => Some(theme::rose(400)),
            _ => None,
        };
        let (project, r) = (d.project.clone(), d.r#ref.clone());
        let icon = if with_project {
            self.projects
                .iter()
                .find(|x| x.name == d.project)
                .map(|x| self.project_icon(x, 16.))
        } else {
            None
        };
        v_flex()
            .id(("cockpit-tile", ix))
            .flex_none()
            .w(px(w))
            .gap_1()
            .p_1()
            .rounded_md()
            .cursor_pointer()
            .hover(|s| s.bg(theme::surface_raised()))
            .when_some(hue, |el, hue| {
                el.bg(theme::tint(hue, 0.1))
                    .border_2()
                    .border_color(theme::tint(hue, 0.7))
            })
            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                this.cockpit_focus(project.clone(), Some(r.clone()), window, cx)
            }))
            .child(
                div()
                    .relative()
                    .w_full()
                    .h(px(h))
                    .overflow_hidden()
                    .rounded_sm()
                    .border_1()
                    .border_color(theme::line_faint())
                    .bg(theme::stage())
                    .child(screen)
                    .when(d.tone.needy() && !d.question.is_empty(), |el| {
                        el.child(
                            div()
                                .absolute()
                                .bottom_0()
                                .left_0()
                                .right_0()
                                .px_2()
                                .py_1()
                                .bg(theme::overlay())
                                .text_size(theme::text::CAPTION)
                                .text_color(if d.tone == Tone::Error {
                                    theme::rose(200)
                                } else {
                                    theme::amber(200)
                                })
                                .line_clamp(2)
                                .child(d.question.clone()),
                        )
                    }),
            )
            .child(
                h_flex()
                    .min_w_0()
                    .h(px(20.))
                    .px_0p5()
                    .gap_1p5()
                    .text_size(theme::text::CAPTION)
                    .children(icon)
                    .when(with_project, |el| {
                        el.child(
                            div()
                                .flex_none()
                                .text_color(theme::muted())
                                .child(self.label_of(&d.project)),
                        )
                    })
                    .child(tone_mark(d.tone))
                    .child(crate::icons::tool_mark(&d.ai, px(14.), theme::muted()))
                    .child(
                        div()
                            .truncate()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(d.title.clone()),
                    ),
            )
            .into_any_element()
    }

    // ——— Focus ———

    fn render_cockpit_focus(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let width = f32::from(window.viewport_size().width);
        let lead_w = (width * 0.24).clamp(480., 896.);
        let desks = self.desks(std::slice::from_ref(&project));
        let staged = self.staged_desk(&project);
        let rest: Vec<Desk> = desks
            .iter()
            .filter(|d| Some(&d.r#ref) != staged.as_ref().map(|s| &s.r#ref))
            .cloned()
            .collect();
        let stage: AnyElement = match &staged {
            Some(d) => self.render_stage(d, width, cx),
            None => div()
                .flex_1()
                .flex()
                .items_center()
                .justify_center()
                .rounded_lg()
                .border_1()
                .border_color(theme::line())
                .bg(theme::chat())
                .text_color(theme::faint())
                .child(tv(
                    "cockpit.focus.noAgents",
                    &[("project", self.label_of(&project).into())],
                ))
                .into_any_element(),
        };
        let mut strip = h_flex()
            .id("cockpit-strip")
            .flex_none()
            .h(px(240.))
            .gap_3()
            .overflow_x_scroll();
        for (ix, d) in rest.iter().enumerate() {
            strip = strip.child(self.render_tile(d, 1000 + ix, 320., false, cx));
        }
        h_flex()
            .size_full()
            .min_h_0()
            .child(
                div()
                    .flex_none()
                    .w(px(lead_w))
                    .h_full()
                    .border_r_1()
                    .border_color(theme::line_faint())
                    .bg(theme::chat())
                    .children(self.cockpit.lead_chat.as_ref().map(|(_, c)| c.clone())),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .h_full()
                    .gap_3()
                    .p_3()
                    .child(stage)
                    .when(!rest.is_empty(), |el| el.child(strip)),
            )
            .into_any_element()
    }

    fn render_stage(&self, d: &Desk, width: f32, cx: &mut Context<Self>) -> AnyElement {
        let visual = self.visual(&d.r#ref);
        let chat = self
            .cockpit
            .stage_chat
            .as_ref()
            .filter(|(r, _)| *r == d.r#ref)
            .map(|(_, c)| c.clone());
        let screen: Option<AnyElement> = match visual {
            Visual::Display => self
                .cockpit
                .live
                .get(&d.r#ref)
                .map(|v| v.clone().into_any_element()),
            #[cfg(feature = "video")]
            Visual::Android => self
                .cockpit
                .stage_device
                .as_ref()
                .filter(|(r, _)| *r == d.r#ref)
                .map(|(_, v)| v.clone().into_any_element()),
            _ => None,
        };
        let body: AnyElement = if visual == Visual::None || screen.is_none() {
            let term = self
                .cockpit
                .stage_term
                .as_ref()
                .filter(|(r, _)| *r == d.r#ref)
                .map(|(_, t)| t.clone());
            h_flex()
                .flex_1()
                .min_h_0()
                .child(
                    div()
                        .flex_grow(3.)
                        .flex_basis(px(0.))
                        .min_w_0()
                        .h_full()
                        .border_r_1()
                        .border_color(theme::line_faint())
                        .children(term),
                )
                .child(
                    div()
                        .flex_grow(2.)
                        .flex_basis(px(0.))
                        .min_w_0()
                        .h_full()
                        .bg(theme::chat())
                        .children(chat),
                )
                .into_any_element()
        } else {
            h_flex()
                .flex_1()
                .min_h_0()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .h_full()
                        .bg(theme::stage())
                        .children(screen),
                )
                // Beside the screen only where there's room for both.
                .when(width >= 3000., |el| {
                    el.child(
                        div()
                            .flex_none()
                            .w(px(544.))
                            .h_full()
                            .border_l_1()
                            .border_color(theme::line_faint())
                            .bg(theme::chat())
                            .children(chat),
                    )
                })
                .into_any_element()
        };
        let (label, icon) = match visual {
            Visual::Display => ("cockpit.visual.display", "monitor"),
            Visual::Android => ("cockpit.visual.android", "smartphone"),
            Visual::None => ("cockpit.visual.none", "square-terminal"),
        };
        let open = d.r#ref.clone();
        v_flex()
            .flex_1()
            .min_h_0()
            .overflow_hidden()
            .rounded_lg()
            .border_1()
            .border_color(if d.tone.needy() {
                theme::tint(theme::amber(400), 0.7)
            } else {
                theme::line()
            })
            .bg(theme::chat())
            .child(
                h_flex()
                    .flex_none()
                    .h(px(40.))
                    .px_3()
                    .gap_2()
                    .border_b_1()
                    .border_color(theme::line_faint())
                    .text_size(theme::text::UI)
                    .child(tone_mark(d.tone))
                    .child(crate::icons::tool_mark(&d.ai, px(16.), theme::muted()))
                    .child(
                        div()
                            .flex_none()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::title())
                            .child(d.title.clone()),
                    )
                    .when(!d.question.is_empty(), |el| {
                        el.child(
                            div()
                                .truncate()
                                .text_color(if d.tone == Tone::Error {
                                    theme::rose(300)
                                } else {
                                    theme::amber(300)
                                })
                                .child(d.question.clone()),
                        )
                    })
                    .child(div().flex_1())
                    .child(
                        h_flex()
                            .flex_none()
                            .gap_1()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::faint())
                            .child(crate::icons::ic(icon).size(px(14.)))
                            .child(t(label)),
                    )
                    .child(
                        h_flex()
                            .id("cockpit-open-agent")
                            .flex_none()
                            .h(px(28.))
                            .px_2()
                            .gap_1()
                            .rounded_md()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::muted())
                            .cursor_pointer()
                            .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.leave_cockpit(cx);
                                this.open_agent_ref(open.clone(), window, cx);
                            }))
                            .child(t("cockpit.stage.open"))
                            .child(crate::icons::ic("arrow-up-right").size(px(14.))),
                    ),
            )
            .child(body)
            .into_any_element()
    }

    /// cockpit_button is the top bar's way in and out, while Settings offers it.
    pub(super) fn cockpit_button(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !self.cockpit_offered() && !self.cockpit.open {
            return None;
        }
        let on = self.cockpit.open;
        Some(
            h_flex()
                .id("top-cockpit")
                .flex_none()
                .h(px(32.))
                .px_2p5()
                .gap_1p5()
                .rounded_lg()
                .border_1()
                .text_size(theme::text::UI)
                .cursor_pointer()
                .when(on, |el| {
                    el.border_color(theme::tint(theme::brand(400), 0.6))
                        .bg(theme::tint(theme::brand(500), 0.15))
                        .text_color(theme::title())
                })
                .when(!on, |el| {
                    el.border_color(theme::line_strong())
                        .text_color(theme::muted())
                        .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                })
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .on_click(
                    cx.listener(|this, _: &ClickEvent, window, cx| this.toggle_cockpit(window, cx)),
                )
                .child(crate::icons::ic("gauge").size(px(16.)))
                .child(t("cockpit.button"))
                .into_any_element(),
        )
    }
}

fn section_label(text: String) -> Div {
    div()
        .px_2()
        .pb_1()
        .text_size(theme::text::MICRO)
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::faint())
        .child(text.to_uppercase())
}

fn count_badge(n: usize) -> Div {
    div()
        .flex_none()
        .min_w_4()
        .h_4()
        .px_1()
        .flex()
        .items_center()
        .justify_center()
        .rounded_full()
        .bg(theme::amber(400))
        .text_size(theme::text::MICRO)
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::on_bright())
        .child(n.to_string())
}

fn tone_mark(tone: Tone) -> AnyElement {
    let (name, color) = match tone {
        Tone::Working => {
            return div()
                .flex_none()
                .text_color(theme::brand(300))
                .child(crate::icons::spinner())
                .into_any_element();
        }
        Tone::Needs => ("bell-ring", theme::amber(300)),
        Tone::Error => ("triangle-alert", theme::rose(300)),
        Tone::Finished => ("circle-dot", theme::sky(300)),
        _ => ("circle-dot", theme::faint()),
    };
    crate::icons::ic(name)
        .size(px(14.))
        .text_color(color)
        .into_any_element()
}

/// clock is a message's time of day, HH:MM, in the computer's time zone.
fn clock(at: &str) -> String {
    crate::dates::local(at)
        .map(|d| d.format("%H:%M").to_string())
        .unwrap_or_default()
}

/// strip_ansi takes a terminal line's colours and cursor moves out.
fn strip_ansi(line: &str) -> String {
    let mut out = String::with_capacity(line.len());
    let mut chars = line.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '\u{1b}' {
            if chars.peek() == Some(&'[') {
                chars.next();
                for c in chars.by_ref() {
                    if ('@'..='~').contains(&c) {
                        break;
                    }
                }
            }
            continue;
        }
        out.push(c);
    }
    out
}
