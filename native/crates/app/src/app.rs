//! The window: the v2 layout's sidebar (projects), main (the open chat) and
//! rail (the project's lead and agents, with their live state).
use std::time::Duration;

use agentbox_api::{
    AgentChange, ChatEvent, Client, EVENT_AGENT, EVENT_CHAT, EVENT_PROJECT, Event, Fleet,
    FleetAgent, Project,
};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

mod add_project;
pub(crate) mod agent;
mod alerts;
pub use alerts::initial_bounds;
mod agent_settings;
pub(crate) mod agent_status;
mod connection;
pub(crate) mod controls;
mod foot;
mod issues;
mod merge;
mod new_agent;
mod new_task;
mod office_entry;
mod panel;
mod past;
mod presence;
pub(crate) mod pulls;
mod recap;
mod recap_card;
mod resources;
mod review;
mod schedules;
mod search;
mod sessions;
mod settings;
mod sidebar;
mod sidebar_actions;
mod sidebar_dialogs;
mod topbar;
mod tour;
mod usage;
mod views;
mod width;
mod work;

use crate::chat::{ChatAction, ChatView};
use crate::terminal::TerminalView;
use crate::vnc::VncView;

/// One agent in the rail.
struct RailRow {
    project: String,
    name: String,
    title: String,
    state: String,
    busy: bool,
    pr: Option<agentbox_api::PullRequest>,
}

use crate::{bench, theme};
use agentbox_i18n::t;

/// state_label is an agent's state in the user's language (agent.state.*).
fn state_label(state: &str) -> String {
    let key = format!("agent.state.{state}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        state.to_string()
    }
}

pub struct AppView {
    client: Client,
    projects: Vec<Project>,
    project: Option<String>,
    fleet: Vec<FleetAgent>,
    fleet_error: Option<SharedString>,
    error: Option<SharedString>,
    connected: bool,
    /// The daemon's connection, and its start (connection.rs).
    conn: connection::Conn,
    /// The top bar's chips (topbar.rs).
    topbar: topbar::TopBar,
    /// The welcome tour and What's new (tour.rs).
    onboarding: tour::Onboarding,
    /// The top bar's meters (usage.rs).
    meters: usage::Meters,
    /// Which chat is open: "lead" or an agent's name.
    open: String,
    chat: Option<Entity<ChatView>>,
    /// The open agent's terminal, in the panel.
    terminal: Option<Entity<TerminalView>>,
    /// The open agent's desktop (VNC), in the panel.
    desktop: Option<Entity<VncView>>,
    /// The right-hand panel beside the chat.
    panel: panel::Panel,
    /// The open agent's page: its Snapshots, dialogs and info card.
    agent: agent::AgentPage,
    /// The panel's Browser, Android and picker (views.rs).
    views: views::Views,
    schedules: schedules::Schedules,
    issues: issues::Issues,
    agent_settings: agent_settings::AgentSettings,
    /// The past agent open (past.rs).
    past: past::Past,
    /// The panel's Resources view of an agent (resources.rs).
    resources: resources::Resources,
    /// The window's presence lease (presence.rs).
    presence: presence::Presence,
    /// The panel's widths, and its drawer (width.rs).
    widths: width::Widths,
    side: sidebar::Sidebar,
    /// The New task picker, while it's open.
    new_task: Option<new_task::NewTask>,
    /// The Settings page is open in the main area.
    settings: bool,
    settings_page: settings::SettingsPage,
    /// The Work page, while it's open in the main area.
    work: Option<Entity<work::WorkView>>,
    /// The Office (crate::office), made the first time it's opened and kept,
    /// and whether it's open over the window now.
    office: Option<Entity<crate::office::OfficeView>>,
    office_open: bool,
    /// The review, over everything, while it's open.
    review: Option<Entity<review::ReviewView>>,
    /// The open project's pull requests, and the one open over the window.
    pulls: pulls::Pulls,
    toasts: Entity<crate::toasts::Toasts>,
    /// The notices, the tray and the window's place (alerts.rs).
    alerts: alerts::Alerts,
    /// The lead's chat sessions, every project's.
    sessions: sessions::Sessions,
    /// The lead's chat should show another session: opened on the next frame.
    reopen_lead: bool,
    /// The doctor's check being handed to the Home chat.
    asking_home: Option<String>,
    _chat_sub: Option<Subscription>,
    _subs: Vec<Subscription>,
    _review_sub: Option<Subscription>,
    _events: Task<()>,
}

impl AppView {
    /// overlaid is whether something is drawn over the window, which a
    /// page's webview (a child window, drawn over everything) must not hide.
    fn overlaid(&self) -> bool {
        self.new_task.is_some()
            || self.review.is_some()
            || self.pulls.open()
            || self.pulls.confirm.is_some()
            || self.agent.destroy.is_some()
            || self.agent.confirm.is_some()
            || self.agent.fork.is_some()
            || self.agent.rename.is_some()
            || self.agent.info.is_some()
            || self.past.continuing
            || self.past.deleting
            || self.schedules.dialog_open()
            || self.settings
            || self.work.is_some()
            || self.office_open
            || self.first_run_open()
            || self.onboarding_open()
    }

    /// first_run_open is whether the first run is the page.
    fn first_run_open(&self) -> bool {
        self.connected && self.settings_page.first_run.active
    }

    /// cut_off is the daemon never having answered: its page is the window.
    fn cut_off(&self) -> bool {
        !self.connected && !self.conn.answered
    }

    pub fn new(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let events = Self::follow(client.clone(), window, cx);
        let pulls = pulls::Pulls::new(window, cx);
        let search_sub = cx.subscribe_in(
            &pulls.search,
            window,
            |this, _, ev: &gpui_component::input::InputEvent, _, cx| pulls::on_search(this, ev, cx),
        );
        let (views, views_sub) = views::Views::new(window, cx);
        let mut this = Self {
            client,
            projects: Vec::new(),
            project: None,
            fleet: Vec::new(),
            fleet_error: None,
            error: None,
            connected: false,
            conn: Default::default(),
            topbar: Default::default(),
            onboarding: Default::default(),
            meters: Default::default(),
            open: "lead".into(),
            chat: None,
            terminal: None,
            desktop: None,
            panel: panel::Panel::new(cx),
            agent: agent::AgentPage::new(window, cx),
            views,
            schedules: schedules::Schedules::new(window, cx),
            issues: issues::Issues::default(),
            agent_settings: agent_settings::AgentSettings::new(window, cx),
            past: past::Past::new(window, cx),
            resources: Default::default(),
            presence: Default::default(),
            widths: width::Widths::load(),
            side: sidebar::Sidebar::new(window, cx),
            new_task: None,
            settings: false,
            settings_page: settings::SettingsPage::new(window, cx),
            work: None,
            office: None,
            office_open: false,
            review: None,
            pulls,
            toasts: crate::toasts::init(cx),
            alerts: Default::default(),
            sessions: sessions::Sessions::new(window, cx),
            reopen_lead: false,
            asking_home: None,
            _chat_sub: None,
            _subs: vec![search_sub, views_sub],
            _review_sub: None,
            _events: events,
        };
        this.load_settings(window, cx);
        this.load_theme(window, cx);
        this.load_projects(window, cx);
        this.watch_connection(window, cx);
        this.watch_topbar(cx);
        this.start_alerts(window, cx);
        this.start_presence(window, cx);
        if let Ok(secs) =
            std::env::var("AGENTBOX_NATIVE_SCROLL_BENCH").map(|s| s.parse::<u64>().unwrap_or(3))
        {
            // Once the chat is in, scroll it up for a while and report frame times.
            cx.spawn_in(window, async move |this, cx| {
                cx.background_executor()
                    .timer(Duration::from_secs(secs))
                    .await;
                this.update_in(cx, |this, window, cx| {
                    if let Some(chat) = &this.chat {
                        chat.update(cx, |c, cx| c.scroll_bench(-24., 600, window, cx));
                    }
                })
                .ok();
            })
            .detach();
        }
        this
    }

    /// follow applies the daemon's events for as long as the window is open.
    fn follow(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Task<()> {
        let mut rx = client.events();
        cx.spawn_in(window, async move |this, cx| {
            while let Some(msg) = rx.recv().await {
                let ok = this.update_in(cx, |this, window, cx| this.on_event(msg, window, cx));
                if ok.is_err() {
                    return;
                }
            }
        })
    }

    fn on_event(&mut self, msg: Option<Event>, window: &mut Window, cx: &mut Context<Self>) {
        let Some(ev) = msg else {
            // (Re)connected: whatever happened meanwhile is in a fresh read.
            self.connected = true;
            self.tray_soon();
            self.load_settings(window, cx);
            self.load_theme(window, cx);
            if self.settings {
                self.load_settings_page(cx);
            }
            self.load_projects(window, cx);
            self.reload_review(cx);
            let names: Vec<String> = self.sessions.lists.keys().cloned().collect();
            for p in names {
                self.load_sessions(p, cx);
            }
            self.load_pulls(false, cx);
            self.reload_resources(cx);
            if let Some(chat) = &self.chat {
                chat.update(cx, |c, cx| c.load(cx));
            }
            return;
        };
        self.office_poke(cx);
        if alerts::moves_tray(&ev.r#type) {
            self.tray_soon();
        }
        match ev.r#type.as_str() {
            EVENT_AGENT => {
                let Ok(change) = serde_json::from_value::<AgentChange>(ev.data) else {
                    return;
                };
                if !self
                    .side
                    .apply_agent(&change.r#ref, Some(&change.state), None)
                    && !change.removed
                {
                    self.load_sidebar(cx);
                }
                self.check_agents(cx);
                cx.notify();
                let mine = self
                    .project
                    .as_deref()
                    .is_some_and(|p| change.r#ref.starts_with(&format!("{p}/")));
                if !mine {
                    return;
                }
                match self.fleet.iter().position(|a| a.r#ref == change.r#ref) {
                    Some(ix) if change.removed => {
                        let gone = self.fleet.remove(ix);
                        // Its chat was open: back to the lead's.
                        if self.open == gone.name {
                            self.open_chat("lead".into(), window, cx);
                        }
                    }
                    Some(ix) => self.fleet[ix].state = change.state,
                    None => self.load_fleet(window, cx),
                }
                self.sync_chat_agent(cx);
                cx.notify();
            }
            EVENT_CHAT => {
                let Ok(chat_ev) = serde_json::from_value::<ChatEvent>(ev.data) else {
                    return;
                };
                if let Some(s) = &chat_ev.session
                    && self.side.apply_agent(&chat_ev.agent, None, Some(&s.state))
                {
                    self.check_agents(cx);
                    cx.notify();
                }
                if let Some(s) = &chat_ev.session {
                    // An agent's chat working is what the rail shows as busy.
                    if let Some(a) = self.fleet.iter_mut().find(|a| a.r#ref == chat_ev.agent) {
                        a.busy = s.state == "running";
                        cx.notify();
                    }
                }
                // A lead's message changes its project's line in the sidebar,
                // and where its session stands (unread).
                if let Some((p, lead)) = chat_ev.agent.split_once('/')
                    && (lead == "lead" || lead.starts_with("lead."))
                    && chat_ev.item.as_ref().is_some_and(|i| {
                        !i.streaming && (i.kind == "user" || i.kind == "assistant")
                    })
                {
                    self.load_line(p.to_string(), cx);
                    self.load_sessions(p.to_string(), cx);
                }
                if let Some(chat) = &self.chat
                    && chat.read(cx).agent == chat_ev.agent
                {
                    chat.update(cx, |c, cx| c.apply(chat_ev, cx));
                }
            }
            EVENT_PROJECT => self.load_projects(window, cx),
            agentbox_api::EVENT_LEAD_SESSIONS => {
                if let Ok(c) = serde_json::from_value::<agentbox_api::LeadSessionsChange>(ev.data) {
                    self.load_sessions(c.project, cx);
                }
            }
            agentbox_api::EVENT_LEAD_SESSION_STATUS => {
                if let Ok(c) =
                    serde_json::from_value::<agentbox_api::LeadSessionStatusChange>(ev.data)
                {
                    self.on_session_status(c.project, c.sessions, cx);
                }
            }
            agentbox_api::EVENT_SESSIONS_SETTLED => {
                if let Ok(c) = serde_json::from_value::<agentbox_api::SessionsSettled>(ev.data) {
                    let p = c.project.clone();
                    self.on_sessions_settled(c.project, c.sessions, cx);
                    self.load_sessions(p, cx);
                }
            }
            agentbox_api::EVENT_FILES => {
                if let Ok(change) = serde_json::from_value::<agentbox_api::FilesChange>(ev.data) {
                    self.on_files_event(change, cx);
                }
            }
            agentbox_api::EVENT_MEDIA => self.on_media_event(cx),
            agentbox_api::EVENT_AGENT_RESOURCES => self.on_resources_event(ev.data, cx),
            agentbox_api::EVENT_AGENT_EVENT => {
                if let Some(p) = ev.data.get("project").and_then(|v| v.as_str())
                    && self.project.as_deref() == Some(p)
                {
                    self.panel_sync(window, cx);
                }
            }
            agentbox_api::EVENT_PULLS => {
                let Ok(change) = serde_json::from_value::<agentbox_api::PullsChange>(ev.data)
                else {
                    return;
                };
                if self.project.as_deref() == Some(change.project.as_str()) {
                    self.load_pulls(false, cx);
                    if !change.unchanged {
                        self.load_fleet(window, cx);
                    }
                }
                if !change.unchanged {
                    self.reload_review(cx);
                }
            }
            agentbox_api::EVENT_REVIEW => self.reload_review(cx),
            agentbox_api::EVENT_THEME => self.load_theme(window, cx),
            agentbox_api::EVENT_UPDATE => self.reload_update(cx),
            agentbox_api::EVENT_NOTIFICATION => {
                if let Ok(n) = serde_json::from_value::<agentbox_api::Notification>(ev.data) {
                    self.on_notification(n, cx);
                }
            }
            agentbox_api::EVENT_QUESTION => {
                if let Some(p) = ev.data.get("project").and_then(|v| v.as_str()) {
                    self.load_questions(p.to_string(), cx);
                }
            }
            _ => {}
        }
    }

    /// load_settings takes the language the user picked (Settings.language),
    /// unless AGENTBOX_NATIVE_LANG overrides it.
    fn load_settings(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if let Ok(lang) = std::env::var("AGENTBOX_NATIVE_LANG") {
            agentbox_i18n::set_language(&lang);
            return;
        }
        let client = self.client.clone();
        cx.spawn_in(window, async move |_, cx| {
            if let Ok(s) = client.get::<agentbox_api::Settings>("/v1/settings").await {
                agentbox_i18n::set_language(&s.language);
                cx.update(|window, _| window.refresh()).ok();
            }
        })
        .detach();
    }

    fn load_projects(&mut self, _window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn_in(_window, async move |this, cx| {
            let res = client.get::<Vec<Project>>("/v1/projects").await;
            this.update_in(cx, |this, window, cx| {
                match res {
                    Ok(p) => {
                        this.projects = p;
                        this.load_sidebar(cx);
                        let mut names: Vec<String> =
                            this.projects.iter().map(|p| p.name.clone()).collect();
                        names.push(agentbox_api::HOME_PROJECT.into());
                        for n in names {
                            if !this.sessions.lists.contains_key(&n) {
                                this.load_sessions(n, cx);
                            }
                        }
                        this.error = None;
                        bench::mark("projects-loaded");
                        if this.project.is_none() {
                            // The app opens on Home, as v2 does; a benchmark
                            // opening an agent (AGENTBOX_NATIVE_OPEN) looks for
                            // it in fake-daemon's project, demo.
                            let first = match std::env::var_os("AGENTBOX_NATIVE_OPEN") {
                                Some(_) => this
                                    .projects
                                    .iter()
                                    .find(|p| p.name == "demo")
                                    .or(this.projects.first())
                                    .map(|p| p.name.clone()),
                                None => Some(agentbox_api::HOME_PROJECT.to_string()),
                            };
                            if let Some(first) = first {
                                this.select_project(first, window, cx);
                            }
                            // AGENTBOX_NATIVE_SETTINGS=<section> opens Settings there (checks).
                            if let Ok(id) = std::env::var("AGENTBOX_NATIVE_SETTINGS") {
                                this.open_settings_at(id, window, cx);
                            }
                            // AGENTBOX_NATIVE_OFFICE=1 opens the Office at start (checks, recordings).
                            if std::env::var_os("AGENTBOX_NATIVE_OFFICE").is_some() {
                                this.open_office(window, cx);
                            }
                        }
                    }
                    Err(e) => this.error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_fleet(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if project == agentbox_api::HOME_PROJECT {
            return; // Home's rail is every project's agents (rail_rows)
        }
        let client = self.client.clone();
        cx.spawn_in(window, async move |this, cx| {
            let res = client
                .get::<Fleet>(&format!("/v1/projects/{project}/fleet"))
                .await;
            this.update_in(cx, |this, window, cx| {
                if this.project.as_deref() != Some(&project) {
                    return;
                }
                match res {
                    Ok(f) => {
                        this.fleet = f.agents;
                        this.fleet_error = None;
                        bench::mark("fleet-loaded");
                        // AGENTBOX_NATIVE_OPEN=<agent> opens that agent's chat at start (benchmarks).
                        if let Ok(open) = std::env::var("AGENTBOX_NATIVE_OPEN")
                            && this.open == "lead"
                            && this.fleet.iter().any(|a| a.name == open)
                        {
                            this.open_chat(open, window, cx);
                        }
                        this.panel_sync(window, cx);
                        this.sync_chat_agent(cx);
                    }
                    Err(e) => this.fleet_error = Some(e.to_string().into()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn select_project(&mut self, name: String, window: &mut Window, cx: &mut Context<Self>) {
        self.settings = false;
        self.work = None;
        self.side.page = None;
        self.sessions.history = false;
        self.sessions.reading = None;
        self.load_sessions(name.clone(), cx);
        self.project = Some(name);
        self.fleet.clear();
        self.load_fleet(window, cx);
        self.load_pulls(false, cx);
        self.open_chat("lead".into(), window, cx);
    }

    fn open_chat(&mut self, who: String, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        let display = self
            .projects
            .iter()
            .find(|p| p.name == project)
            .map(|p| p.display_name.clone())
            .unwrap_or_else(|| {
                if project == agentbox_api::HOME_PROJECT {
                    t("shell.nav.home")
                } else {
                    project.clone()
                }
            });
        let (prefix, agent, title) = if who == "lead" {
            let (prefix, r, topic) = self.lead_target(&project);
            (
                prefix,
                r,
                format!(
                    "{display} · {}",
                    topic.unwrap_or_else(|| t("v2.header.lead"))
                ),
            )
        } else {
            let t = self
                .fleet
                .iter()
                .find(|a| a.name == who)
                .map(|a| a.title.clone())
                .or_else(|| {
                    self.side
                        .agents
                        .iter()
                        .find(|a| a.project == project && a.name == who)
                        .map(|a| a.title.clone())
                })
                .unwrap_or_default();
            (
                format!("/v1/agents/{project}/{who}"),
                format!("{project}/{who}"),
                format!("{who} · {t}"),
            )
        };
        self.keep_draft(cx);
        self.open = who;
        self.terminal = None;
        self.desktop = None;
        self.panel.forget();
        self.views.forget(window, cx);
        self.past.agent = None;
        self.agent.reset();
        let client = self.client.clone();
        // Under the project's header, the chat draws no title of its own.
        let header = self.is_home();
        let name = if self.open == "lead" {
            String::new()
        } else {
            self.open.clone()
        };
        let draft = self.side.drafts.get(&agent).cloned();
        let chat = cx.new(|cx| {
            let mut c = ChatView::new(client, prefix, agent, title.into(), window, cx);
            c.header = header;
            c.agent_name = name;
            if let Some(d) = &draft {
                c.set_draft(d, window, cx);
            }
            c
        });
        self._chat_sub = Some(cx.subscribe_in(&chat, window, Self::on_chat_action));
        self.chat = Some(chat);
        self.sync_chat_agent(cx);
        self.panel_sync(window, cx);
        cx.notify();
    }

    /// sync_chat_agent tells the open chat whether its agent sleeps.
    fn sync_chat_agent(&mut self, cx: &mut Context<Self>) {
        let state = self
            .open_agent()
            .map(|a| a.state.clone())
            .filter(|s| s == "stopped" || s == "paused");
        if let Some(chat) = &self.chat
            && chat.read(cx).asleep != state
        {
            chat.update(cx, |c, cx| {
                c.asleep = state;
                cx.notify();
            });
        }
    }

    /// open_agent_ref opens an agent's chat, in its own project.
    fn open_agent_ref(&mut self, r#ref: String, window: &mut Window, cx: &mut Context<Self>) {
        if let Some(past) = r#ref.strip_prefix("past:") {
            return self.open_past(past.to_string(), window, cx);
        }
        let Some((p, a)) = r#ref.split_once('/') else {
            return;
        };
        // An agent no longer running anywhere is a past agent's record.
        let live = self.fleet.iter().any(|x| x.r#ref == r#ref)
            || self
                .side
                .agents
                .iter()
                .any(|x| x.project == p && x.name == a);
        if !live && self.panel.past_agent(&r#ref).is_some() {
            return self.open_past(r#ref, window, cx);
        }
        if self.project.as_deref() != Some(p) {
            self.select_project(p.to_string(), window, cx);
        }
        self.open_chat(a.to_string(), window, cx);
    }

    /// on_chat_action does what the chat asks of the window.
    fn on_chat_action(
        &mut self,
        _: &Entity<ChatView>,
        ev: &ChatAction,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        match ev.clone() {
            ChatAction::Rollback { checkpoint, n } => {
                self.agent.confirm = Some(agent::Confirm::Rollback { checkpoint, n });
                cx.notify();
            }
            ChatAction::Fork { checkpoint, n } => {
                let name = self.open.clone();
                self.start_fork(
                    agentbox_i18n::tv(
                        "chat.turn.forkTitle",
                        &[("name", (&name).into()), ("n", n.into())],
                    ),
                    t("chat.turn.forkDescription"),
                    agentbox_api::ForkRequest {
                        checkpoint,
                        ..Default::default()
                    },
                    window,
                    cx,
                );
            }
            ChatAction::OpenAgent(r) => self.open_agent_ref(r, window, cx),
            ChatAction::OpenProject(p) => self.select_project(p, window, cx),
            ChatAction::ForkSession(item) => self.fork_session(item, window, cx),
            ChatAction::NewSession { text } => self.session_with_message(text, window, cx),
            ChatAction::ContinuePast => {
                self.past.continuing = true;
                cx.notify();
            }
            ChatAction::DeletePast => {
                self.past.deleting = true;
                cx.notify();
            }
            // Only the closed session's own view (History) emits these.
            ChatAction::ContinueClosed | ChatAction::ReopenClosed | ChatAction::DeleteClosed => {}
            ChatAction::Merge(project, number) => self.ask_merge(project, number, cx),
            ChatAction::OpenReview(item) => {
                // Waiting for a verdict while it finished and has none
                // (lib/work.ts awaitingReview).
                let waiting = !item.fingerprint.is_empty() && item.verdict.is_empty();
                let w = agentbox_api::WorkItem {
                    status: if waiting { "review" } else { "reviewed" }.into(),
                    item: *item,
                    ..Default::default()
                };
                self.open_review(w, Vec::new(), window, cx)
            }
            ChatAction::Wake => {
                if let Some(a) = self.open_agent() {
                    let action = if a.state == "paused" {
                        agent_status::Action::Resume
                    } else {
                        agent_status::Action::Start
                    };
                    let r = a.r#ref.clone();
                    self.run_action(r, action, cx);
                }
            }
        }
    }

    fn is_home(&self) -> bool {
        self.project.as_deref() == Some(agentbox_api::HOME_PROJECT)
    }

    /// rail_rows are the agents the rail lists: the project's, or on Home
    /// every project's.
    fn rail_rows(&self) -> Vec<RailRow> {
        if self.is_home() {
            return self
                .side
                .agents
                .iter()
                .map(|a| RailRow {
                    project: a.project.clone(),
                    name: a.name.clone(),
                    title: format!("{} · {}", a.project, a.title),
                    state: a.state.clone(),
                    busy: a.chat == "running",
                    pr: None,
                })
                .collect();
        }
        self.fleet
            .iter()
            .map(|a| RailRow {
                project: a.project.clone(),
                name: a.name.clone(),
                title: a.title.clone(),
                state: a.state.clone(),
                busy: a.busy,
                pr: a.pr.clone().filter(|p| p.number > 0),
            })
            .collect()
    }

    fn render_rail(&self, cx: &mut Context<Self>) -> Div {
        let row = |id: String, selected: bool| {
            h_flex()
                .id(SharedString::from(id))
                .mx_2()
                .px_2()
                .py_2()
                .gap_2()
                .rounded_md()
                .cursor_pointer()
                .when(selected, |el| el.bg(theme::surface_strong()))
                .hover(|el| el.bg(theme::surface_raised()))
        };
        let lead = row("rail-lead".into(), self.open == "lead")
            .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                this.open_chat("lead".into(), window, cx)
            }))
            .child(div().size_2().rounded_full().bg(theme::brand(500)))
            .child(
                v_flex()
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::primary())
                            .child(t("v2.header.lead")),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(t("v2.header.openLead")),
                    ),
            );
        let pulls_tab = !self.is_home() && self.pulls.tab == pulls::RailTab::Pulls;
        let rail = v_flex()
            .w(px(if pulls_tab { 400. } else { 300. }))
            .flex_none()
            .h_full()
            .bg(theme::card())
            .border_l_1()
            .border_color(theme::line())
            .child(
                h_flex()
                    .h_12()
                    .px_3()
                    .when(!self.is_home(), |el| {
                        el.child(self.render_rail_switch(self.rail_rows().len(), cx))
                    })
                    .when(self.is_home(), |el| {
                        el.px_4()
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(theme::primary())
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .child(t("shell.home.agents")),
                            )
                            .child(div().flex_1())
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(format!("{}", self.rail_rows().len())),
                            )
                    }),
            );
        if pulls_tab {
            return rail.child(self.render_pulls_panel(cx));
        }
        rail.child(lead)
            .child(div().mx_4().my_1().h(px(1.)).bg(theme::line()))
            .children(self.rail_rows().into_iter().map(|a| {
                let (name, project) = (a.name.clone(), a.project.clone());
                let busy = a.busy;
                let home = self.is_home();
                row(
                    format!("rail-{}-{}", a.project, a.name),
                    self.open == a.name && !home,
                )
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    // From Home, an agent opens in its own project.
                    if this.is_home() {
                        this.select_project(project.clone(), window, cx);
                    }
                    this.open_chat(name.clone(), window, cx)
                }))
                .child(
                    div()
                        .flex_none()
                        .size_2()
                        .rounded_full()
                        .bg(theme::state_color(&a.state)),
                )
                .child(
                    v_flex()
                        .min_w_0()
                        .flex_1()
                        .child(
                            h_flex()
                                .gap_2()
                                .child(
                                    div()
                                        .text_sm()
                                        .text_color(theme::primary())
                                        .child(a.name.clone()),
                                )
                                .when_some(a.pr.as_ref(), |el, pr| {
                                    el.child(pulls::pr_chip(
                                        SharedString::from(format!(
                                            "rail-pr-{}-{}",
                                            a.project, a.name
                                        )),
                                        pr,
                                    ))
                                })
                                .when(busy, |el| {
                                    el.child(
                                        div()
                                            .text_xs()
                                            .text_color(theme::starting())
                                            .child(t("chat.subagent.working")),
                                    )
                                }),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .truncate()
                                .child(a.title.clone()),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(state_label(&a.state)),
                        ),
                )
            }))
            .when_some(self.fleet_error.clone(), |el, e| {
                el.child(div().p_4().text_xs().text_color(theme::error()).child(e))
            })
    }
}

impl AppView {
    /// show_terminal makes the open agent's terminal, for the panel.
    fn show_terminal(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.terminal.is_none() {
            let path = format!("/v1/agents/{project}/{}/terminal", self.open);
            let client = self.client.clone();
            self.terminal = Some(cx.new(|cx| TerminalView::new(client, path, cx)));
        }
        if let Some(t) = &self.terminal {
            let focus = t.read(cx).focus_handle().clone();
            focus.focus(window, cx);
        }
        cx.notify();
    }
}

impl Render for AppView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.panel.track(&self.fleet);
        self.presence_check(window, cx);
        bench::mark("first-render");
        controls::set_viewport(window.viewport_size());
        self.focus_tour(window, cx);
        // The tour's spotlight reads where its anchors were drawn last.
        crate::spot::measure(self.onboarding_touring());
        if window.focused(cx).is_none() {
            self.side.root_focus.focus(window, cx);
        }
        // The sidebar's search follows a language switch.
        if self.side.lang != agentbox_i18n::language() {
            self.side.lang = agentbox_i18n::language();
            self.side.search.update(cx, |s, cx| {
                s.set_placeholder(t("v2.filters.search"), window, cx)
            });
        }
        self.widths.window = f32::from(window.viewport_size().width);
        if std::mem::take(&mut self.reopen_lead) && self.open == "lead" {
            cx.defer_in(window, |this, window, cx| {
                this.open_chat("lead".into(), window, cx)
            });
        }
        // The Office covers the window: nothing beneath it is drawn, so the
        // app's own motion (a working agent's pulse) costs nothing meanwhile.
        if let Some(office) = self.render_office() {
            return div()
                .id("app")
                .relative()
                .size_full()
                .bg(theme::ink())
                .child(office)
                .child(self.toasts.clone())
                .into_any_element();
        }
        let overlay = self.render_new_task(cx);
        let full_page = self.cut_off() || self.first_run_open();
        let lead_view = self.open == "lead"
            && !self.settings
            && self.work.is_none()
            && self.side.page.is_none()
            && self.project.is_some()
            && !full_page;
        let no_session = lead_view
            && self.project.as_ref().is_some_and(|p| {
                self.sessions.lists.contains_key(p) && self.sessions.current(p).is_none()
            });
        h_flex()
            .id("app")
            .track_focus(&self.side.root_focus)
            .relative()
            // Ctrl+N anywhere opens New task.
            // A session's keys go first: Ctrl+Tab and Ctrl+W are a text box's too.
            .capture_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                if this.session_key(ev, window, cx) {
                    cx.stop_propagation();
                }
            }))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                if ev.keystroke.modifiers.control
                    && ev.keystroke.key == "n"
                    && this.new_task.is_none()
                {
                    this.open_new_task(window, cx);
                    cx.stop_propagation();
                }
                // Ctrl+K searches everything; Ctrl+/ lists the shortcuts.
                if ev.keystroke.modifiers.control
                    && ev.keystroke.key == "k"
                    && this.side.palette.is_none()
                {
                    this.open_search(window, cx);
                    cx.stop_propagation();
                }
                if ev.keystroke.modifiers.control && ev.keystroke.key == "," {
                    this.open_settings(window, cx);
                    cx.stop_propagation();
                }
                if ev.keystroke.modifiers.control && ev.keystroke.key == "/" {
                    this.side.dialog = Some(sidebar_dialogs::Dialog::Keys);
                    cx.notify();
                    cx.stop_propagation();
                }
                // Ctrl+D folds the panel to its strip and back (a terminal's
                // or a controlled desktop's own keys stop before here).
                if ev.keystroke.modifiers.control
                    && ev.keystroke.key == "d"
                    && !this.is_home()
                    && this.project.is_some()
                {
                    this.toggle_panel(window, cx);
                    cx.stop_propagation();
                }
            }))
            .size_full()
            .bg(theme::ink())
            .text_color(theme::primary())
            .font_family(theme::SANS)
            .child(theme::window_washes(window))
            .child(crate::webview::pages_begin())
            .when(self.widths.dragging(), |el| {
                el.cursor_col_resize()
                    .on_mouse_move(cx.listener(|this, ev: &MouseMoveEvent, _, cx| {
                        this.drag_to(ev.position.x, cx)
                    }))
                    .on_mouse_up(
                        MouseButton::Left,
                        cx.listener(|this, _: &MouseUpEvent, _, cx| this.drag_end(cx)),
                    )
            })
            .child(self.render_sidebar(cx))
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .h_full()
                    .when(!self.connected && self.conn.answered, |el| {
                        el.child(self.render_offline_notice())
                    })
                    .when(
                        !self.settings
                            && self.work.is_none()
                            && self.side.page.is_none()
                            && self.chat.is_some()
                            && !full_page,
                        |el| {
                            el.children(self.render_home_bar(cx))
                                .children(self.render_project_header(cx))
                                .children(self.render_agent_header(cx))
                                .children(self.render_past_header(cx))
                                .children(self.render_machine_bar(cx))
                                .children(self.render_session_tabs(cx))
                                .when(!no_session, |el| {
                                    el.children(self.render_session_header(cx))
                                })
                        },
                    )
                    .child(
                        div().flex_1().min_h_0().child(match &self.chat {
                            _ if self.cut_off() => self.render_unreachable(cx),
                            _ if self.first_run_open() => self.render_first_run(cx),
                            _ if self.settings => self.render_settings(cx),
                            _ if self.work.is_some() => self.render_work(cx),
                            _ if self.side.page.is_some() => self.render_foot_page(cx),
                            Some(_) if lead_view && self.sessions.history => {
                                self.render_history(cx)
                            }
                            Some(_) if no_session => self.render_no_session(cx),
                            Some(_)
                                if self.open != "lead"
                                    && self.agent.page == Some(agent::Page::Snapshots) =>
                            {
                                self.render_snapshots(cx)
                            }
                            Some(_)
                                if self.open != "lead"
                                    && self.agent.page == Some(agent::Page::Settings) =>
                            {
                                self.render_agent_settings(cx)
                            }
                            Some(chat) => chat.clone().into_any_element(),
                            None => div()
                                .size_full()
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_color(theme::faint())
                                .when(self.projects.is_empty(), |el| {
                                    el.child(t("shell.sidebar.addFirst"))
                                })
                                .into_any_element(),
                        }),
                    ),
            )
            .when(!full_page, |el| {
                el.child(if self.is_home() || self.project.is_none() {
                    self.render_rail(cx).into_any_element()
                } else {
                    self.render_panel(cx)
                })
            })
            // Last, so it draws over the rest.
            .children(self.render_drawer(cx))
            .children(self.render_desktop_full(cx))
            .children(overlay)
            .children(self.review.clone())
            .children(self.render_pull(cx))
            .children(self.render_merge_confirm(cx))
            .children(self.render_agent_dialogs(cx))
            .children(self.render_past_dialogs(cx))
            .children(self.render_schedule_dialogs(cx))
            .children(self.render_onboarding(window, cx))
            .children(self.render_usage_sent(cx))
            .children(self.render_sidebar_dialog(cx))
            .children(self.render_session_changes(cx))
            .children(self.render_move_agents(cx))
            .children(self.render_search(cx))
            .child(self.toasts.clone())
            .child(crate::webview::pages_end())
            .into_any_element()
    }
}

/// pr_chip_for is a pull request as a small pill, for views outside app/.
pub(crate) fn pr_chip_for(pr: &agentbox_api::PullRequest) -> AnyElement {
    work::pr_chip(pr)
}
