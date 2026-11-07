//! v2's right-hand panel beside a project's chats (components/v2/RightPanel.tsx,
//! AgentList.tsx, lib/rightPanel.ts). Beside the lead's chat it is a switch of
//! the project's Agents (grouped by what needs you, idle folded, past agents
//! at the foot), Pull requests, Files (the lead's checkout) and Evidence;
//! beside an agent's chat it is that agent, with a back arrow to the lead, a
//! switch of its views (Desktop, Terminal, Files, Evidence, Timeline) and ✕
//! for the list. It folds to a strip of the agents (PanelButton, Ctrl+D).
//! Each agent keeps the view it was last shown in.
use std::collections::{HashMap, HashSet};
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use agentbox_api::{
    AgentEvent, ArchivedAgent, BrowserStatus, CPUUsage, FleetAgent, MediaItem, MemoryUsage,
    RecordingStatus, Timeline,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::menu::ContextMenuExt as _;
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::split;
use super::agent_status::{self as st, Action, St, Tone};
use super::controls::{self, Btn};
use super::pulls::{self, RailTab};
use super::settings::defaults::time_ago;
use crate::files::FileBrowser;
use crate::media;
use crate::theme;
use crate::toasts::{self, Kind, Toast};
use crate::vnc::VncView;

mod timeline;

/// An agent's views on the panel's switch, in its order.
#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum PView {
    Desktop,
    Terminal,
    Browser,
    Android,
    Files,
    Evidence,
    Timeline,
    /// What its machine uses (resources.rs).
    Resources,
    /// The project's schedules, picked on an agent's switch: its header stays.
    Schedules,
}

impl PView {
    pub const ALL: [PView; 9] = [
        PView::Desktop,
        PView::Terminal,
        PView::Browser,
        PView::Android,
        PView::Files,
        PView::Evidence,
        PView::Timeline,
        PView::Resources,
        PView::Schedules,
    ];

    pub fn key(self) -> &'static str {
        match self {
            PView::Desktop => "desktop",
            PView::Terminal => "terminal",
            PView::Files => "files",
            PView::Evidence => "evidence",
            PView::Timeline => "timeline",
            PView::Resources => "resources",
            PView::Browser => "browser",
            PView::Android => "android",
            PView::Schedules => "schedules",
        }
    }

    pub fn label(self) -> String {
        t(&format!("panel.view.{}", self.key()))
    }

    pub fn glyph(self) -> &'static str {
        match self {
            PView::Desktop => "monitor",
            PView::Terminal => "square-terminal",
            PView::Files => "folder-tree",
            PView::Evidence => "images",
            PView::Timeline => "chart-no-axes-gantt",
            PView::Resources => "activity",
            PView::Browser => "globe",
            PView::Android => "smartphone",
            PView::Schedules => "alarm-clock",
        }
    }

    /// machine views need the agent's machine up.
    fn machine(self) -> bool {
        matches!(
            self,
            PView::Desktop | PView::Terminal | PView::Browser | PView::Android | PView::Resources
        )
    }
}

/// What the panel shows.
#[derive(Clone, PartialEq, Debug)]
pub enum Whose {
    /// Beside the lead's chat: its tabs.
    Lead,
    /// Beside an agent's chat, the agents' list (its ✕).
    List,
    /// An agent, by ref.
    Agent(String),
    /// A past agent, by ref: its views that need no machine.
    Past(String),
}

pub struct Panel {
    /// Each agent's standing as last drawn, and when it last went from
    /// working to finished: that tile bursts once (motion::celebrate).
    seen: std::cell::RefCell<HashMap<String, (St, Option<std::time::Instant>)>>,
    pub folded: bool,
    pub list_beside: bool,
    pub(super) views: HashMap<String, PView>,
    idle_open: bool,
    usage: HashMap<String, (Option<f64>, Option<i64>)>,
    /// Each agent's chat session as one read on hovering its row: its
    /// model and context, for the row's tooltip.
    peeked: HashMap<String, agentbox_api::ChatSession>,
    peeking: HashSet<String>,
    host_memory: i64,
    /// Each agent's last report: when, and what it said.
    reports: HashMap<String, (String, String)>,
    /// What each stopped machine of the project holds on disk, by ref
    /// (useRailAgents' stoppedMachines).
    disk: HashMap<String, i64>,
    past: Vec<ArchivedAgent>,
    /// Whose media is read ("p/agent-01", or "p/lead" for the project's).
    media_for: String,
    media: Option<Result<Vec<MediaItem>, String>>,
    files_cache: HashMap<String, PathBuf>,
    fetching: HashSet<String>,
    /// Recordings' first frames, for their thumbnails.
    posters: HashMap<String, Arc<RenderImage>>,
    featured: Option<String>,
    viewer: Option<String>,
    #[cfg(feature = "video")]
    player: Option<(String, Entity<crate::video::VideoPlayer>)>,
    timeline: Option<(String, Result<Timeline, String>)>,
    /// The timeline's playhead, on its track, and whether it plays.
    tl_x: f32,
    tl_playing: bool,
    tl_scroll: ScrollHandle,
    browser: Option<BrowserStatus>,
    pub(super) recording: Option<RecordingStatus>,
    shooting: bool,
    files: Option<Entity<FileBrowser>>,
    /// The desktop asked for last: only that one connects (see connect_desktop).
    desktop_gen: u64,
    /// Whether the desktop is shown full screen, over the whole window
    /// (AppView::render_desktop_full), rather than in the panel.
    pub(super) desktop_full: bool,
    /// Take control as it was before full screen, to put back after.
    control_before_full: bool,
    ticks: u64,
    _poll: Task<()>,
}

impl Panel {
    /// track notes every agent that went from working to finished since the
    /// last look; AppView calls it on every render, panel shown or not.
    pub fn track(&self, fleet: &[FleetAgent]) {
        let mut seen = self.seen.borrow_mut();
        for a in fleet {
            let st = st::agent_st(a, false, false);
            let e = seen.entry(a.r#ref.clone()).or_insert((st, None));
            if e.0 == St::Working && matches!(st, St::Review | St::Done) {
                e.1 = Some(std::time::Instant::now());
            }
            e.0 = st;
        }
    }

    /// finished is when `r` finished, if it was a moment ago.
    pub fn finished(&self, r: &str) -> Option<std::time::Instant> {
        self.seen
            .borrow()
            .get(r)
            .and_then(|e| e.1)
            .filter(|t| t.elapsed() < std::time::Duration::from_secs(3))
    }

    /// any_finished is the latest finish among the agents, if a moment ago.
    pub fn any_finished(&self) -> Option<std::time::Instant> {
        self.seen
            .borrow()
            .values()
            .filter_map(|e| e.1)
            .filter(|t| t.elapsed() < std::time::Duration::from_secs(3))
            .max()
    }

    pub fn new(cx: &mut Context<AppView>) -> Self {
        let poll = cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(Duration::from_secs(1)).await;
                if this.update(cx, |this, cx| this.panel_tick(cx)).is_err() {
                    return;
                }
            }
        });
        Self {
            folded: false,
            list_beside: false,
            views: HashMap::new(),
            idle_open: false,
            seen: Default::default(),
            usage: HashMap::new(),
            peeked: HashMap::new(),
            peeking: HashSet::new(),
            host_memory: 0,
            reports: HashMap::new(),
            disk: HashMap::new(),
            past: Vec::new(),
            media_for: String::new(),
            media: None,
            files_cache: HashMap::new(),
            fetching: HashSet::new(),
            posters: HashMap::new(),
            featured: None,
            viewer: None,
            #[cfg(feature = "video")]
            player: None,
            timeline: None,
            tl_x: 0.,
            tl_playing: false,
            tl_scroll: ScrollHandle::new(),
            browser: None,
            recording: None,
            shooting: false,
            files: None,
            desktop_gen: 0,
            desktop_full: false,
            control_before_full: false,
            ticks: 0,
            _poll: poll,
        }
    }

    pub fn usage_of(&self, r#ref: &str) -> Option<(Option<f64>, Option<i64>)> {
        self.usage.get(r#ref).copied()
    }

    pub fn view_of(&self, r#ref: &str) -> PView {
        self.views.get(r#ref).copied().unwrap_or(PView::Desktop)
    }

    pub fn desktop_up(&self) -> bool {
        self.browser.as_ref().is_some_and(|b| b.display)
    }

    /// last_frame is the latest screenshot of an agent, fetched.
    pub fn last_frame(&self, r#ref: &str) -> Option<PathBuf> {
        if self.media_for != r#ref {
            return None;
        }
        let items = self.media.as_ref()?.as_ref().ok()?;
        let shot = items
            .iter()
            .filter(|m| m.kind == "screenshot")
            .max_by_key(|m| media::ms(&m.created_at))?;
        self.files_cache.get(&shot.id).cloned()
    }

    /// forget drops what was read of the agent shown before.
    pub fn forget(&mut self) {
        self.list_beside = false;
        self.featured = None;
        self.viewer = None;
        self.browser = None;
        self.recording = None;
        self.desktop_full = false;
        #[cfg(feature = "video")]
        {
            self.player = None;
        }
    }

    pub fn browser_status(&self) -> Option<&BrowserStatus> {
        self.browser.as_ref()
    }

    pub fn recording_status(&self) -> Option<RecordingStatus> {
        self.recording.clone()
    }

    /// past_agent is a past agent of the list's, by ref.
    pub fn past_agent(&self, r#ref: &str) -> Option<ArchivedAgent> {
        self.past.iter().find(|a| a.r#ref == r#ref).cloned()
    }

    /// has_media is whether whose (a ref) has media in Evidence.
    pub fn has_media(&self, whose: &str) -> bool {
        self.media_for == whose && matches!(&self.media, Some(Ok(m)) if !m.is_empty())
    }

    fn items(&self) -> Vec<MediaItem> {
        let mut items = match &self.media {
            Some(Ok(m)) => m.clone(),
            _ => Vec::new(),
        };
        items.sort_by_key(|m| -media::ms(&m.created_at));
        items
    }
}

/// switch_box is the frame of the panel's switches (LeadSwitch, ViewSwitch):
/// rounded-lg border-line bg-surface-faint p-0.5.
pub(super) fn switch_box() -> Div {
    h_flex()
        .flex_none()
        .p(px(2.))
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .bg(theme::surface_faint())
}

/// switch_item is one of a switch's buttons: h-7 gap-1.5 rounded-md 11.5px
/// medium, its icon 14px; lit, bg-surface-raised text-title shadow-sm, else
/// text-subtle, text-title on hover. The caller sets its width.
pub(super) fn switch_item(id: &'static str, icon: &str, on: bool) -> Stateful<Div> {
    h_flex()
        .id(id)
        .relative()
        .h(px(28.))
        .gap_1p5()
        .rounded_md()
        .text_size(px(11.5))
        .line_height(px(17.25))
        .font_weight(FontWeight::MEDIUM)
        .when(on, |el| {
            el.bg(theme::surface_raised())
                .text_color(theme::title())
                .shadow_xs()
        })
        .when(!on, |el| {
            el.text_color(theme::subtle())
                .cursor_pointer()
                .hover(|s| s.text_color(theme::title()))
        })
        .child(crate::icons::ic(icon).size(px(14.)).flex_none())
}

fn is_capture(m: &MediaItem) -> bool {
    m.kind == "screenshot" || m.kind == "recording"
}

/// clock is seconds as m:ss.
pub(super) fn clock(secs: f64) -> String {
    let s = secs.max(0.).round() as u64;
    format!("{}:{:02}", s / 60, s % 60)
}

/// ListRow is one agent of the list, read for its row.
struct ListRow {
    agent: FleetAgent,
    st: St,
    /// It finished a moment ago: its tile plays the finish.
    finished: Option<std::time::Instant>,
    reason: String,
    color: Hsla,
    approval: bool,
    when: Option<String>,
}

impl AppView {
    /// panel_whose is what the panel shows beside the open chat.
    pub(super) fn panel_whose(&self) -> Whose {
        if self.open == "lead" {
            return Whose::Lead;
        }
        if let Some(r) = self.past_ref() {
            return Whose::Past(r);
        }
        if self.panel.list_beside {
            return Whose::List;
        }
        match self.open_agent() {
            Some(a) => Whose::Agent(a.r#ref.clone()),
            None => Whose::List,
        }
    }

    /// panel_shows_agent is whether the panel shows the open agent, which
    /// its machine bar then leaves to it.
    pub(super) fn panel_shows_agent(&self) -> bool {
        !self.panel.folded && matches!(self.panel_whose(), Whose::Agent(_))
    }

    /// set_view shows an agent in the panel on a view.
    pub(super) fn set_view(
        &mut self,
        r#ref: &str,
        view: PView,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.panel.views.insert(r#ref.to_string(), view);
        self.panel.folded = false;
        self.panel.list_beside = false;
        self.panel_sync(window, cx);
        cx.notify();
    }

    pub(super) fn show_panel_view(
        &mut self,
        r#ref: &str,
        view: PView,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.set_view(r#ref, view, window, cx)
    }

    pub(super) fn toggle_panel(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.narrow() {
            // Where it doesn't fit beside the chat, it opens over it.
            if self.widths.drawer {
                self.close_drawer(cx);
            } else {
                self.widths.drawer = true;
            }
            self.panel.folded = false;
            self.panel_sync(window, cx);
            cx.notify();
            return;
        }
        self.panel.folded = !self.panel.folded;
        self.panel_sync(window, cx);
        cx.notify();
    }

    /// panel_sync reads and makes what the panel shows now needs.
    pub(super) fn panel_sync(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.is_home() {
            return;
        }
        let lead = format!("{project}/lead");
        match self.panel_whose() {
            Whose::Lead => match self.pulls.tab {
                RailTab::Files => {
                    self.ensure_files(format!("/v1/projects/{project}/files"), lead, window, cx)
                }
                RailTab::Evidence => self.load_media(lead, false, cx),
                RailTab::Agents => self.load_list_data(cx),
                RailTab::Pulls => {}
                RailTab::Issues => self.load_issues(false, cx),
                RailTab::Schedules => self.load_schedules(cx),
            },
            Whose::List => self.load_list_data(cx),
            Whose::Past(r) => self.past_sync(&r, window, cx),
            Whose::Agent(r) => {
                let (p, a) = split(&r);
                let view = self.panel.view_of(&r);
                // The machine bar's last frame, and the desktop's captures.
                self.load_media(r.clone(), false, cx);
                match view {
                    PView::Desktop => {
                        if self.desktop.is_none() && !self.panel.folded {
                            self.connect_desktop(p, a, cx);
                        }
                        self.load_desktop_status(cx);
                    }
                    PView::Terminal => self.show_terminal(window, cx),
                    PView::Files => self.ensure_files(
                        format!("/v1/agents/{p}/{a}/files"),
                        r.clone(),
                        window,
                        cx,
                    ),
                    PView::Evidence => {}
                    PView::Timeline => self.load_timeline(r.clone(), cx),
                    PView::Resources => self.load_resources(r.clone(), cx),
                    PView::Browser => {
                        self.load_desktop_status(cx);
                        self.load_preview(cx);
                    }
                    PView::Android => {
                        self.load_android(cx);
                        self.load_desktop_status(cx);
                    }
                    PView::Schedules => self.load_schedules(cx),
                }
                if view != PView::Desktop && view != PView::Browser {
                    // What the switch offers: the browser's pages, the emulator.
                    self.load_desktop_status(cx);
                }
                if view != PView::Android {
                    self.load_android(cx);
                }
                self.sync_device(cx);
            }
        }
        self.sync_player(cx);
    }

    /// connect_desktop opens the agent's desktop a moment after it's asked
    /// for, if it still is: a VNC server counts a connection dropped during
    /// its handshake as a failed login, and blocks the address after a few,
    /// which going through agents quickly would do.
    fn connect_desktop(&mut self, project: String, agent: String, cx: &mut Context<Self>) {
        self.panel.desktop_gen += 1;
        let generation = self.panel.desktop_gen;
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(Duration::from_millis(400))
                .await;
            this.update(cx, |this, cx| {
                let wanted = this.panel.desktop_gen == generation
                    && this.open == agent
                    && this.desktop.is_none()
                    && this.desktop_up() != Some(false)
                    && this.panel_shows_agent()
                    && this
                        .open_agent()
                        .is_some_and(|a| this.panel.view_of(&a.r#ref) == PView::Desktop);
                if !wanted {
                    return;
                }
                let path = format!("/v1/agents/{project}/{agent}/browser/view");
                let client = this.client.clone();
                this.desktop = Some(cx.new(|cx| {
                    let mut v = VncView::new(client, path, cx);
                    v.control = false;
                    v
                }));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn ensure_files(
        &mut self,
        base: String,
        r#ref: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if self
            .panel
            .files
            .as_ref()
            .is_some_and(|f| f.read(cx).r#ref == r#ref)
        {
            return;
        }
        let client = self.client.clone();
        self.panel.files = Some(cx.new(|cx| FileBrowser::new(client, base, r#ref, window, cx)));
    }

    /// panel_tick runs every second: the recording's clock, and every few
    /// seconds what the panel shows is read again, as the Electron app polls.
    fn panel_tick(&mut self, cx: &mut Context<Self>) {
        self.panel.ticks += 1;
        let n = self.panel.ticks;
        if self.panel.recording.as_ref().is_some_and(|r| r.recording) {
            cx.notify();
        }
        if self.is_home() || self.project.is_none() {
            return;
        }
        if n.is_multiple_of(5) {
            match self.panel_whose() {
                Whose::Lead if self.pulls.tab == RailTab::Agents => self.load_usage(cx),
                Whose::List => self.load_usage(cx),
                Whose::Agent(r)
                    if matches!(self.panel.view_of(&r), PView::Desktop | PView::Browser) =>
                {
                    self.load_desktop_status(cx)
                }
                Whose::Agent(r) if self.panel.view_of(&r) == PView::Android => {
                    self.load_android(cx);
                    self.load_desktop_status(cx)
                }
                _ => {}
            }
        }
        if n.is_multiple_of(15)
            && let Whose::Agent(r) = self.panel_whose()
            && self.panel.view_of(&r) != PView::Android
        {
            self.load_android(cx);
            self.load_desktop_status(cx);
        }
        if n.is_multiple_of(30) {
            self.load_fleet_now(cx);
        }
    }

    fn load_list_data(&mut self, cx: &mut Context<Self>) {
        self.load_usage(cx);
        let Some(project) = self.project.clone() else {
            return;
        };
        let client = self.client.clone();
        let stopped = self
            .fleet
            .iter()
            .any(|a| a.project == project && a.state == "stopped");
        cx.spawn(async move |this, cx| {
            let disk = if stopped {
                client
                    .get::<agentbox_api::StoppedMachines>(&format!(
                        "/v1/stopped-machines?project={project}"
                    ))
                    .await
                    .map(|m| {
                        m.machines
                            .into_iter()
                            .filter_map(|m| Some((m.r#ref, m.machine?)))
                            .collect()
                    })
                    .unwrap_or_default()
            } else {
                HashMap::new()
            };
            let events = client
                .get::<Vec<AgentEvent>>(&format!("/v1/projects/{project}/agent-events"))
                .await
                .unwrap_or_default();
            let past = client
                .get::<Vec<ArchivedAgent>>(&format!(
                    "/v1/archived-agents?project={project}&limit=50"
                ))
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                // Newest first: the first of each agent's is its last.
                let mut reports = HashMap::new();
                for ev in &events {
                    if ev.kind == "docker_pruned" || ev.summary.is_empty() {
                        continue;
                    }
                    reports
                        .entry(ev.r#ref.clone())
                        .or_insert((ev.at.clone(), ev.summary.clone()));
                }
                this.panel.reports = reports;
                this.panel.past = past;
                this.panel.disk = disk;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_usage(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let cpu = client
                .get::<CPUUsage>("/v1/usage/cpu?interval=500ms")
                .await
                .ok();
            let mem = client.get::<MemoryUsage>("/v1/usage/memory").await.ok();
            this.update(cx, |this, cx| {
                let mut usage: HashMap<String, (Option<f64>, Option<i64>)> = HashMap::new();
                for a in cpu.iter().flat_map(|c| &c.agents) {
                    usage.entry(a.r#ref.clone()).or_default().0 = Some(a.cpu);
                }
                for a in mem.iter().flat_map(|m| &m.agents) {
                    usage.entry(a.r#ref.clone()).or_default().1 = Some(a.memory);
                }
                this.panel.host_memory = mem.map_or(0, |m| m.host_total);
                this.panel.usage = usage;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// desktop_up is Electron's desktopUp: the agent's machine running and its
    /// display up; None until its status is read.
    pub(super) fn desktop_up(&self) -> Option<bool> {
        let running = self.open_agent().is_some_and(|a| a.state == "running");
        match &self.panel.browser {
            _ if !running => Some(false),
            Some(b) => Some(b.display),
            None => None,
        }
    }

    /// sync_desktop connects the desktop when it's up and shown, and lets it
    /// go when it isn't up (the view itself reconnects across a drop).
    pub(super) fn sync_desktop(&mut self, cx: &mut Context<Self>) {
        match self.desktop_up() {
            Some(false) => {
                if self.desktop.take().is_some() {
                    self.panel.desktop_full = false;
                }
            }
            Some(true) if self.desktop.is_none() && !self.panel.folded => {
                if let Some(a) = self.open_agent()
                    && self.panel.view_of(&a.r#ref) == PView::Desktop
                {
                    let (p, n) = (a.project.clone(), a.name.clone());
                    self.connect_desktop(p, n, cx);
                }
            }
            _ => {}
        }
    }

    pub(super) fn load_desktop_status(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        if a.state != "running" {
            self.sync_desktop(cx);
            return;
        }
        let (p, n) = (a.project.clone(), a.name.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let b = client
                .get::<BrowserStatus>(&format!("/v1/agents/{p}/{n}/browser"))
                .await
                .ok();
            let r = client
                .get::<RecordingStatus>(&format!("/v1/agents/{p}/{n}/media/record"))
                .await
                .ok();
            this.update(cx, |this, cx| {
                if this.open == n {
                    this.panel.browser = b;
                    this.panel.recording = r;
                    this.sync_desktop(cx);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// load_media reads whose's media: an agent's, or the project's for the lead.
    pub(super) fn load_media(&mut self, whose: String, force: bool, cx: &mut Context<Self>) {
        if !force && self.panel.media_for == whose && self.panel.media.is_some() {
            return;
        }
        if self.panel.media_for != whose {
            self.panel.media = None;
            self.panel.featured = None;
            self.panel.viewer = None;
            #[cfg(feature = "video")]
            {
                self.panel.player = None;
            }
        }
        self.panel.media_for = whose.clone();
        let (p, a) = split(&whose);
        let path = if a == "lead" {
            format!("/v1/projects/{p}/media")
        } else {
            format!("/v1/agents/{p}/{a}/media")
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<Vec<MediaItem>>(&path)
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.panel.media_for != whose {
                    return;
                }
                this.panel.media = Some(res);
                let list = this.panel.items();
                this.sync_media_list(&whose, list);
                this.fetch_media_files(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// fetch_media_files brings the screenshots and recordings shown into
    /// the cache, to show from disk.
    fn fetch_media_files(&mut self, cx: &mut Context<Self>) {
        let wanted: Vec<MediaItem> = self
            .panel
            .items()
            .into_iter()
            .filter(is_capture)
            .take(24)
            .collect();
        for m in wanted {
            if self.panel.files_cache.contains_key(&m.id)
                || !self.panel.fetching.insert(m.id.clone())
            {
                continue;
            }
            let client = self.client.clone();
            cx.spawn(async move |this, cx| {
                let id = m.id.clone();
                let res = media::fetch(client, m).await;
                this.update(cx, |this, cx| {
                    this.panel.fetching.remove(&id);
                    if let Ok(p) = res {
                        this.make_poster(&id, &p, cx);
                        this.panel.files_cache.insert(id, p);
                    }
                    this.sync_player(cx);
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        self.sync_player(cx);
    }

    /// make_poster draws a fetched recording's first frame, on a thread.
    #[cfg(feature = "video")]
    fn make_poster(&mut self, id: &str, path: &std::path::Path, cx: &mut Context<Self>) {
        if !path
            .extension()
            .is_some_and(|e| e == "mp4" || e == "webm" || e == "mkv")
            || self.panel.posters.contains_key(id)
        {
            return;
        }
        let (id, path) = (id.to_string(), path.to_path_buf());
        let (tx, rx) = tokio::sync::oneshot::channel();
        std::thread::spawn(move || {
            let _ = tx.send(crate::video::first_frame(&path, 320).ok());
        });
        cx.spawn(async move |this, cx| {
            if let Ok(Some(img)) = rx.await {
                let image = Arc::new(RenderImage::new(vec![image::Frame::new(img)]));
                this.update(cx, |this, cx| {
                    this.panel.posters.insert(id, image);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    #[cfg(not(feature = "video"))]
    fn make_poster(&mut self, _id: &str, _path: &std::path::Path, _cx: &mut Context<Self>) {}

    /// featured is Evidence's large item: the one picked, else the latest recording.
    fn featured(&self) -> Option<MediaItem> {
        let items = self.panel.items();
        items
            .iter()
            .find(|i| Some(&i.id) == self.panel.featured.as_ref() && is_capture(i))
            .or_else(|| items.iter().find(|i| i.kind == "recording"))
            .cloned()
    }

    #[cfg_attr(not(feature = "video"), allow(dead_code))]
    fn evidence_shown(&self) -> bool {
        match self.panel_whose() {
            Whose::Lead => self.pulls.tab == RailTab::Evidence,
            Whose::Agent(r) => self.panel.view_of(&r) == PView::Evidence,
            Whose::Past(_) => self.past.view == PView::Evidence,
            Whose::List => false,
        }
    }

    /// sync_player keeps a player for the featured recording while Evidence shows.
    #[cfg(feature = "video")]
    fn sync_player(&mut self, cx: &mut Context<Self>) {
        let want = self
            .featured()
            .filter(|m| m.kind == "recording" && self.evidence_shown() && !self.panel.folded);
        let Some(m) = want else {
            self.panel.player = None;
            return;
        };
        if self
            .panel
            .player
            .as_ref()
            .is_some_and(|(id, _)| id == &m.id)
        {
            return;
        }
        let Some(path) = self.panel.files_cache.get(&m.id).cloned() else {
            self.panel.player = None;
            return;
        };
        let marks: Vec<f64> = media::gallery(&self.panel.items())
            .into_iter()
            .find_map(|s| match s {
                media::Shown::Recording(t) if t.media.id == m.id => {
                    Some(t.chapters.iter().map(|c| c.at).collect())
                }
                _ => None,
            })
            .unwrap_or_default();
        let player = cx.new(|cx| {
            let mut p = crate::video::VideoPlayer::new(path, cx);
            p.set_marks(marks, cx);
            p
        });
        self.panel.player = Some((m.id.clone(), player));
    }

    #[cfg(not(feature = "video"))]
    fn sync_player(&mut self, _cx: &mut Context<Self>) {}

    /// on_files_event passes an EventFiles to the files shown.
    pub(super) fn on_files_event(
        &mut self,
        change: agentbox_api::FilesChange,
        cx: &mut Context<Self>,
    ) {
        if let Some(f) = &self.panel.files
            && f.read(cx).r#ref == change.r#ref
        {
            f.update(cx, |f, cx| f.apply(&change, cx));
        }
    }

    pub(super) fn on_media_event(&mut self, cx: &mut Context<Self>) {
        if !self.panel.media_for.is_empty() {
            let whose = self.panel.media_for.clone();
            self.load_media(whose, true, cx);
        }
    }

    pub(super) fn screenshot(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let client = self.client.clone();
        self.panel.shooting = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, MediaItem>(
                    &format!("/v1/agents/{p}/{n}/media/screenshot"),
                    &agentbox_api::ScreenshotRequest {
                        target: "display".into(),
                        name: "screen".into(),
                        ..Default::default()
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                this.panel.shooting = false;
                match res {
                    Ok(m) => this.saved(r.clone(), m, cx),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// saved says a capture was saved, with a way to see it in Evidence.
    pub(super) fn saved(&mut self, r#ref: String, m: MediaItem, cx: &mut Context<Self>) {
        let view = cx.entity().downgrade();
        let id = m.id.clone();
        Toast::new(
            Kind::Info,
            tv("agent.media.saved", &[("name", (&m.name).into())]),
        )
        .action(t("agent.media.view"), move |window, cx| {
            let (r, id) = (r#ref.clone(), id.clone());
            view.update(cx, |this, cx| {
                this.panel.featured = Some(id);
                this.set_view(&r, PView::Evidence, window, cx);
            })
            .ok();
        })
        .show(cx);
        let whose = self.panel.media_for.clone();
        self.load_media(whose, true, cx);
    }

    fn toggle_recording(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n, r) = (a.project.clone(), a.name.clone(), a.r#ref.clone());
        let recording = self.panel.recording.as_ref().is_some_and(|r| r.recording);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let base = format!("/v1/agents/{p}/{n}/media/record");
            if recording {
                let res = client
                    .post::<_, MediaItem>(&format!("{base}/stop"), &serde_json::json!({}))
                    .await;
                this.update(cx, |this, cx| {
                    match res {
                        Ok(m) => this.saved(r.clone(), m, cx),
                        Err(e) => toasts::error(cx, e.to_string()),
                    }
                    this.load_desktop_status(cx);
                })
                .ok();
            } else {
                let res = client
                    .post::<_, RecordingStatus>(
                        &format!("{base}/start"),
                        &agentbox_api::RecordRequest {
                            name: "screen recording".into(),
                            ..Default::default()
                        },
                    )
                    .await;
                this.update(cx, |this, cx| {
                    match res {
                        Ok(s) => this.panel.recording = Some(s),
                        Err(e) => toasts::error(cx, e.to_string()),
                    }
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    fn start_desktop(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n) = (a.project.clone(), a.name.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, BrowserStatus>(
                    &format!("/v1/agents/{p}/{n}/browser/start"),
                    &serde_json::json!({}),
                )
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(b) => {
                        this.panel.browser = Some(b);
                        // Up now: connect, or end the wait of a view that is
                        // trying again.
                        if let Some(d) = &this.desktop {
                            d.update(cx, |d, _| d.retry());
                        }
                        this.sync_desktop(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// set_desktop_full shows the desktop over the whole window, taking the
    /// pointer and keys (Take control), or puts it back in the panel with
    /// Take control as it was.
    pub(super) fn set_desktop_full(&mut self, full: bool, w: &mut Window, cx: &mut Context<Self>) {
        let Some(d) = self.desktop.clone() else {
            self.panel.desktop_full = false;
            return;
        };
        if full == self.panel.desktop_full {
            return;
        }
        self.panel.desktop_full = full;
        let before = self.panel.control_before_full;
        let was = d.update(cx, |d, cx| {
            let was = d.control;
            d.set_control(if full { true } else { before });
            cx.notify();
            was
        });
        if full {
            self.panel.control_before_full = was;
        }
        if full {
            d.read(cx).focus_handle().clone().focus(w, cx);
        }
        cx.notify();
    }

    /// render_desktop_full is the desktop over the whole window, with a bar
    /// to leave it: the panel's view, moved (one entity draws in one place).
    pub(super) fn render_desktop_full(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !self.panel.desktop_full {
            return None;
        }
        let d = self.desktop.clone()?;
        let name = self
            .open_agent()
            .map(|a| a.name.clone())
            .unwrap_or_default();
        let control = d.read(cx).control;
        let connected = d.read(cx).connected();
        Some(
            v_flex()
                .id("desktop-full-screen")
                .absolute()
                .inset_0()
                .bg(theme::pitch())
                .occlude()
                // Escape leaves, unless the desktop has the keys (it bubbles
                // up only when it doesn't).
                .on_key_down(cx.listener(|this, ev: &KeyDownEvent, w, cx| {
                    if ev.keystroke.key == "escape" {
                        this.set_desktop_full(false, w, cx);
                        cx.stop_propagation();
                    }
                }))
                .child(
                    h_flex()
                        .h(px(36.))
                        .px_3()
                        .gap_2()
                        .items_center()
                        .border_b_1()
                        .border_color(theme::line())
                        .bg(theme::surface())
                        .child(
                            div()
                                .text_color(theme::muted())
                                .child(crate::icons::of("▣")),
                        )
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .truncate()
                                .text_sm()
                                .text_color(theme::primary())
                                .child(name),
                        )
                        .child(controls::toggle(
                            "desktop-full-control",
                            control,
                            !connected,
                            cx.listener(|this, _: &ClickEvent, w, cx| {
                                if let Some(d) = &this.desktop {
                                    d.update(cx, |d, cx| {
                                        let on = !d.control;
                                        d.set_control(on);
                                        cx.notify();
                                    });
                                    d.read(cx).focus_handle().clone().focus(w, cx);
                                }
                                cx.notify();
                            }),
                        ))
                        .child(
                            div()
                                .text_sm()
                                .text_color(if control {
                                    theme::accent()
                                } else {
                                    theme::muted()
                                })
                                .child(t("agent.browser.takeControl")),
                        )
                        .child(controls::button(
                            "desktop-full-exit",
                            format!("{} ✕", t("agent.browser.exitFullscreen")),
                            Btn::Ghost,
                            false,
                            cx.listener(|this, _: &ClickEvent, w, cx| {
                                this.set_desktop_full(false, w, cx)
                            }),
                        )),
                )
                .child(div().flex_1().min_h_0().child(d))
                .into_any_element(),
        )
    }

    /// ask_lead drafts, in the lead's chat, a request for the agent (or the
    /// agents) to record their work, and opens it: not sent.
    fn ask_lead(&mut self, agent: Option<String>, window: &mut Window, cx: &mut Context<Self>) {
        let text = match &agent {
            Some(a) => tv("agent.mediaTab.askText", &[("agent", a.into())]),
            None => t("project.media.askText"),
        };
        if self.open != "lead" {
            self.open_chat("lead".into(), window, cx);
        }
        if let Some(chat) = &self.chat {
            chat.update(cx, |c, cx| {
                c.set_draft(&text, window, cx);
                c.focus_composer(window, cx);
            });
        }
    }

    /// list_rows are the project's agents as the list reads them, in its order.
    fn list_rows(&self) -> Vec<ListRow> {
        let project = self.project.clone().unwrap_or_default();
        let questions = self.side_questions(&project);
        let review = self.review_refs(&project);
        let mut agents: Vec<FleetAgent> = self.fleet.clone();
        agents.sort_by_key(st::rank);
        agents
            .into_iter()
            .map(|a| {
                let question = questions
                    .iter()
                    .find(|(r, _)| r == &a.r#ref)
                    .map(|(_, q)| q.clone());
                let st = st::agent_st(&a, question.is_some(), review.contains(&a.r#ref));
                let finished = self.panel.finished(&a.r#ref);
                let (label, tone) = st::chat_label(&a);
                let report = self.panel.reports.get(&a.r#ref);
                let (reason, color) = if let Some(q) = question {
                    (q, st_color(St::Needs))
                } else if a.chat == "waiting" {
                    (t("v2.tag.approval"), st_color(St::Needs))
                } else if st == St::Error {
                    (label.clone(), st_color(St::Error))
                } else if let Some((_, summary)) = report {
                    (
                        summary.lines().next().unwrap_or("").to_string(),
                        st_color(st),
                    )
                } else if st == St::Review {
                    (t("panel.group.review"), st_color(st))
                } else {
                    (
                        label.clone(),
                        if tone == Tone::Muted {
                            theme::muted()
                        } else {
                            st_color(st)
                        },
                    )
                };
                let when = a
                    .last_active
                    .clone()
                    .or_else(|| report.map(|(at, _)| at.clone()));
                ListRow {
                    approval: a.chat == "waiting",
                    st,
                    finished,
                    reason,
                    color,
                    when,
                    agent: a,
                }
            })
            .collect()
    }

    /// render_panel is the right-hand panel beside a project's chat.
    pub(super) fn render_panel(&self, cx: &mut Context<Self>) -> AnyElement {
        if self.panel.folded || self.narrow() {
            return self.render_strip(cx);
        }
        let width = self.panel_box().width;
        div()
            .relative()
            .flex_none()
            .h_full()
            .child(self.render_panel_body(width, cx))
            .child(self.width_handle(width, cx))
            .into_any_element()
    }

    /// render_panel_body is the panel at a width: its header and its view.
    pub(super) fn render_panel_body(&self, width: f32, cx: &mut Context<Self>) -> AnyElement {
        let whose = self.panel_whose();
        if matches!(whose, Whose::Lead) && self.panel_settings.is_some() {
            let (header, body) = self.render_project_settings(cx);
            return v_flex()
                .w(px(width))
                .flex_none()
                .h_full()
                .bg(theme::sunken())
                .border_l_1()
                .border_color(theme::line())
                .child(header)
                .child(div().flex_1().min_h_0().child(body))
                .into_any_element();
        }
        let header = match &whose {
            Whose::Lead => self.lead_header(cx),
            Whose::List => self.list_header(cx),
            Whose::Agent(r) => self.agent_panel_header(r, cx),
            Whose::Past(r) => self.past_panel_header(r, cx),
        };
        let body: AnyElement = match &whose {
            Whose::Lead => match self.pulls.tab {
                RailTab::Agents => self.render_list(cx),
                RailTab::Pulls => self.render_pulls_panel(cx),
                RailTab::Files => self.render_files(),
                RailTab::Evidence => self.render_evidence(None, cx),
                RailTab::Issues => self.render_issues(cx),
                RailTab::Schedules => self.render_schedules(cx),
            },
            Whose::List => self.render_list(cx),
            Whose::Agent(r) => self.render_agent_view(r, cx),
            Whose::Past(r) => self.render_past_view(r, cx),
        };
        v_flex()
            .w(px(width))
            .flex_none()
            .h_full()
            .bg(theme::sunken())
            .border_l_1()
            .border_color(theme::line())
            .child(header)
            .child(div().flex_1().min_h_0().child(body))
            .into_any_element()
    }

    /// header_row is the panel's header (RightPanel's: h-11 border-b
    /// border-line-faint bg-rail pl-2 pr-1.5, gap-1.5 while the switch's
    /// lit view is named, which it always is here).
    fn header_row(&self) -> Div {
        h_flex()
            .flex_none()
            .h(px(44.))
            .pl_2()
            .pr_1p5()
            .gap_1p5()
            .overflow_hidden()
            .border_b_1()
            .border_color(theme::line_faint())
            .bg(theme::rail())
    }

    fn lead_header(&self, cx: &mut Context<Self>) -> AnyElement {
        // A row too narrow for them all squeezes the switch and the buttons
        // alike, by their widths, as Chromium shrinks flex items.
        let agents = self.pulls.tab == RailTab::Agents;
        let [work, settings] = self.project_button_items(cx);
        let shrink = |el: Stateful<Div>| el.flex_shrink(1.).into_any_element();
        self.header_row()
            .child(self.render_rail_switch(self.fleet.len(), cx))
            .child(div().flex_1().min_w_0())
            .when(agents, |el| el.child(shrink(self.new_agent_button(cx))))
            .child(shrink(work))
            .child(shrink(settings))
            .when(agents, |el| el.child(shrink(self.fold_button(cx))))
            .into_any_element()
    }

    fn list_header(&self, cx: &mut Context<Self>) -> AnyElement {
        self.header_row()
            .child(
                h_flex()
                    .pl_1p5()
                    .gap_2()
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::muted())
                            .child(t("agent.rail.agents")),
                    )
                    .when(!self.fleet.is_empty(), |el| {
                        el.child(count_pill(self.fleet.len()))
                    }),
            )
            .child(div().flex_1())
            .child(self.new_agent_button(cx))
            .child(self.fold_button(cx))
            .into_any_element()
    }

    fn new_agent_button(&self, cx: &mut Context<Self>) -> Stateful<Div> {
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
        icon_button(
            "panel-new-agent",
            "plus",
            tv("agent.rail.newAgentIn", &[("project", (&project).into())]),
        )
        .on_click(cx.listener(|this, _: &ClickEvent, w, cx| this.open_new_task(w, cx)))
    }

    fn fold_button(&self, cx: &mut Context<Self>) -> Stateful<Div> {
        icon_button("panel-fold", "»", t("panel.list.fold"))
            .on_click(cx.listener(|this, _: &ClickEvent, w, cx| this.toggle_panel(w, cx)))
    }

    /// render_panel_button is the chat header's button that folds the panel
    /// to its strip and back (PanelButton).
    pub(super) fn render_panel_button(&self, cx: &mut Context<Self>) -> AnyElement {
        let open = !self.panel.folded;
        let tip: SharedString = tv("panel.toggleTip", &[("keys", "Ctrl+D".into())]).into();
        div()
            .id("panel-toggle")
            .flex_none()
            // Button size="icon" variant="ghost": size-9, rounded-lg, a 16px icon.
            .size_9()
            .flex()
            .items_center()
            .justify_center()
            .rounded_lg()
            .cursor_pointer()
            .text_color(if open {
                theme::primary()
            } else {
                theme::muted()
            })
            .when(open, |el| el.bg(theme::surface_raised()))
            .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
            .active(|el| el.bg(theme::surface_strong()))
            .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
            .on_click(cx.listener(|this, _: &ClickEvent, w, cx| this.toggle_panel(w, cx)))
            .child(crate::icons::ic("panel-right").size(px(16.)))
            .into_any_element()
    }

    fn agent_panel_header(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let Some(a) = self.agent_by_ref(r).cloned() else {
            return self.header_row().into_any_element();
        };
        let asleep = st::asleep(&a.state);
        let view = self.panel.view_of(r);
        let busy = a.busy || a.chat == "running";
        let (shown, hidden) = self.agent_views(&a, view);
        let booted = self
            .views
            .android
            .as_ref()
            .is_some_and(|(r, s)| *r == a.r#ref && s.booted);
        let dot_tip: SharedString = t(if asleep {
            "panel.machine.asleep"
        } else {
            "panel.machine.running"
        })
        .into();
        let switch = h_flex()
            .p(px(2.))
            .rounded_lg()
            .border_1()
            .border_color(theme::line())
            .bg(theme::ink())
            .children(shown.into_iter().map(|v| {
                let on = v == view;
                let r = r.to_string();
                let tip: SharedString = v.label().into();
                h_flex()
                    .id(SharedString::from(format!("panel-view-{}", v.key())))
                    .h(px(28.))
                    .gap_1p5()
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
                            .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                            .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                                this.set_view(&r, v, w, cx)
                            }))
                    })
                    .rounded_md()
                    .text_xs()
                    .font_weight(FontWeight::MEDIUM)
                    .relative()
                    .child(crate::icons::of(v.glyph()))
                    .when(on, |el| el.child(v.label()))
                    // Android, once its emulator has booted.
                    .when(v == PView::Android && booted, |el| {
                        el.child(
                            div()
                                .absolute()
                                .top(px(4.))
                                .right(px(4.))
                                .size(px(5.))
                                .rounded_full()
                                .bg(theme::running()),
                        )
                    })
            }));
        self.header_row()
            .child(
                // On the press: the layout changes under the pointer, and
                // its release mustn't land on what moved there.
                icon_button("panel-back", "←", t("panel.back")).on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _: &MouseDownEvent, w, cx| {
                        cx.stop_propagation();
                        this.pulls.tab = RailTab::Agents;
                        this.open_chat("lead".into(), w, cx);
                    }),
                ),
            )
            .child(
                div()
                    .id("panel-dot")
                    .px_1()
                    .tooltip(move |w, cx| Tooltip::new(dot_tip.clone()).build(w, cx))
                    .child(
                        div()
                            .size(px(8.))
                            .rounded_full()
                            .bg(if asleep {
                                theme::faint()
                            } else {
                                theme::running()
                            })
                            .when(busy, |el| {
                                el.border_1().border_color(theme::primary().opacity(0.5))
                            }),
                    ),
            )
            .child(switch)
            .child(self.render_more_views(r, hidden, cx))
            .child(div().flex_1())
            .child(
                icon_button("panel-close", "✕", t("panel.back")).on_click(cx.listener(
                    |this, _: &ClickEvent, w, cx| {
                        this.panel.list_beside = true;
                        this.panel_sync(w, cx);
                        cx.notify();
                    },
                )),
            )
            .into_any_element()
    }

    /// render_list is the project's agents, grouped by what needs you.
    fn render_list(&self, cx: &mut Context<Self>) -> AnyElement {
        let rows = self.list_rows();
        let active = self.open.clone();
        let project_label = self
            .projects
            .iter()
            .find(|p| Some(&p.name) == self.project.as_ref())
            .map(|p| p.display_name.clone())
            .unwrap_or_default();
        let mut groups: Vec<AnyElement> = Vec::new();
        for g in St::ORDER {
            let mine: Vec<&ListRow> = rows.iter().filter(|r| r.st == g).collect();
            if mine.is_empty() {
                continue;
            }
            let folds = g == St::Done;
            let open =
                !folds || self.panel.idle_open || mine.iter().any(|r| r.agent.name == active);
            let disk: i64 = if folds {
                mine.iter()
                    .filter_map(|r| self.panel.disk.get(&r.agent.r#ref))
                    .sum()
            } else {
                0
            };
            // The group's head: h-7 px-2 gap-1.5 12px muted (a button when it folds).
            let head = h_flex()
                .id(SharedString::from(format!("list-group-{g:?}")))
                .w_full()
                .min_w_0()
                .h(px(28.))
                .px_2()
                .gap_1p5()
                .rounded_md()
                .text_size(px(12.))
                .text_color(theme::muted())
                .when(folds, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.bg(theme::surface()).text_color(theme::primary()))
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.panel.idle_open = !this.panel.idle_open;
                            cx.notify();
                        }))
                        .child(
                            crate::icons::ic(if open {
                                "chevron-down"
                            } else {
                                "chevron-right"
                            })
                            .size(px(14.)),
                        )
                })
                .child(div().min_w_0().truncate().child(g.group_label()))
                .child(
                    div()
                        .text_color(theme::faint())
                        .child(mine.len().to_string()),
                )
                // What the stopped ones hold on disk, at the row's end.
                .when(disk > 0, |el| {
                    let tip: SharedString = t("agent.rail.stoppedTip").into();
                    el.child(
                        h_flex()
                            .id("list-disk")
                            .ml_auto()
                            .flex_none()
                            .gap_1()
                            .text_color(theme::faint())
                            .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                            .child(crate::icons::ic("hard-drive").size(px(12.)))
                            .child(crate::files::human_bytes(disk)),
                    )
                });
            groups.push(
                v_flex()
                    .pt_1p5()
                    .child(head)
                    .when(open, |el| {
                        el.child(
                            v_flex().gap(px(1.)).children(
                                mine.into_iter()
                                    .map(|r| self.list_row(r, r.agent.name == active, cx)),
                            ),
                        )
                    })
                    .into_any_element(),
            );
        }
        let past = self.panel.past.len();
        div()
            .id("panel-list")
            .size_full()
            .overflow_y_scroll()
            .px_1p5()
            .pt_1()
            .pb_3()
            .children(groups)
            .when(rows.is_empty(), |el| {
                el.child(
                    v_flex()
                        .items_center()
                        .gap_2()
                        .px_2()
                        .py_8()
                        .child(
                            div()
                                .text_sm()
                                .text_color(theme::muted())
                                .text_center()
                                .child(tv(
                                    "agent.rail.empty",
                                    &[("project", (&project_label).into())],
                                )),
                        )
                        .child(controls::button(
                            "list-new-agent",
                            format!("+ {}", t("agent.rail.newAgent")),
                            Btn::Secondary,
                            false,
                            cx.listener(|this, _: &ClickEvent, w, cx| this.open_new_task(w, cx)),
                        )),
                )
            })
            .when(past > 0, |el| {
                el.child(
                    h_flex()
                        .id("list-past")
                        .mt_1p5()
                        .px_2()
                        .py_1p5()
                        .gap_1p5()
                        .rounded_lg()
                        .text_size(px(12.))
                        .text_color(theme::subtle())
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface()).text_color(theme::primary()))
                        .on_click(cx.listener(|this, _: &ClickEvent, w, cx| {
                            let p = this.project.clone().unwrap_or_default();
                            this.open_work(p, String::new(), w, cx)
                        }))
                        .child(crate::icons::ic("archive").size(px(14.)))
                        .child(t("pastAgents.title"))
                        .child(div().text_color(theme::faint()).child(format!(
                            "· {}",
                            if past >= 50 {
                                "50+".to_string()
                            } else {
                                past.to_string()
                            }
                        ))),
                )
            })
            .into_any_element()
    }

    /// peek_session reads an agent's chat session once, on hovering its row
    /// (peekThread), for the model and context its tooltip names.
    fn peek_session(&mut self, r: String, cx: &mut Context<Self>) {
        if !self.panel.peeking.insert(r.clone()) {
            return;
        }
        let Some((p, a)) = r.split_once('/') else {
            return;
        };
        let path = format!("/v1/agents/{p}/{a}/chat?limit=1");
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<agentbox_api::ChatThread>(&path).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(t) => {
                        this.panel.peeked.insert(r, t.session);
                        cx.notify();
                    }
                    // Read again on the next hover.
                    Err(_) => {
                        this.panel.peeking.remove(&r);
                    }
                }
            })
            .ok();
        })
        .detach();
    }

    /// list_row is one agent: its monitor, then its lines (AgentLine).
    fn list_row(&self, r: &ListRow, active: bool, cx: &mut Context<Self>) -> AnyElement {
        let a = &r.agent;
        let title = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let usage = self
            .panel
            .usage_of(&a.r#ref)
            .filter(|_| a.state == "running");
        let heavy = usage.is_some_and(|(c, m)| st::heavy(c, m, self.panel.host_memory));
        let changes = (a.changes.files > 0).then(|| a.changes.clone());
        let mut tip = vec![title.clone(), st::chat_label(a).0];
        if let Some(pr) = a.pr.as_ref().filter(|p| p.number > 0) {
            tip.push(format!("#{} {}", pr.number, pr.title));
        }
        if let Some((c, m)) = usage {
            tip.push(tv(
                "panel.list.usage",
                &[
                    ("cpu", (&format!("{:.1}", c.unwrap_or(0.))).into()),
                    ("memory", (&m.map_or("–".into(), st::mem_short)).into()),
                ],
            ));
        }
        // The model and context come from the agent's session, read on the
        // first hover: the tooltip fills them in where it stands when the
        // read returns, so the first hover shows them too.
        let tip_head = tip;
        let tip_tail: Vec<String> = changes
            .iter()
            .map(|c| {
                format!(
                    "+{} −{} · {}",
                    c.insertions,
                    c.deletions,
                    tv("panel.list.files", &[("count", c.files.into())])
                )
            })
            .collect();
        let (tip_app, tip_ref) = (cx.entity().downgrade(), a.r#ref.clone());
        let name = a.name.clone();
        let disk = self.panel.disk.get(&a.r#ref).copied();
        // Its model, from its session (AgentLine reads every row's chat).
        let model = match self.panel.peeked.get(&a.r#ref) {
            Some(s) => model_name(s),
            None => {
                let r = a.r#ref.clone();
                let me = cx.entity();
                cx.defer(move |cx| me.update(cx, |this, cx| this.peek_session(r, cx)));
                None
            }
        };
        let menu = self.agent_menu(a, false, cx);
        h_flex()
            .id(SharedString::from(format!("list-{}", a.r#ref)))
            .group("list-row")
            .w_full()
            .min_w_0()
            .items_start()
            .gap_2p5()
            .px_2()
            .py_1p5()
            .rounded_lg()
            .cursor_pointer()
            .when(active, |el| el.bg(theme::surface_raised()))
            .when(!active, |el| el.hover(|s| s.bg(theme::surface_strong())))
            .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                this.panel.list_beside = false;
                this.open_chat(name.clone(), w, cx)
            }))
            // What the row leaves out, on its monitor: the rest of the row
            // keeps clear for its menu.
            .child(
                div()
                    .id(SharedString::from(format!("list-monitor-{}", a.r#ref)))
                    .on_hover({
                        let r = a.r#ref.clone();
                        cx.listener(move |this, hovered: &bool, _, cx| {
                            if *hovered {
                                this.peek_session(r.clone(), cx)
                            }
                        })
                    })
                    .tooltip(move |w, cx| {
                        let (head, tail) = (tip_head.clone(), tip_tail.clone());
                        let (app, r) = (tip_app.clone(), tip_ref.clone());
                        Tooltip::element(move |_, cx| {
                            let peeked = app
                                .upgrade()
                                .and_then(|a| a.read(cx).panel.peeked.get(&r).map(peek_lines))
                                .unwrap_or_default();
                            let all: Vec<String> = head
                                .iter()
                                .cloned()
                                .chain(peeked)
                                .chain(tail.iter().cloned())
                                .collect();
                            all.join("\n")
                        })
                        .build(w, cx)
                    })
                    .pt_0p5()
                    .child(agent_monitor(
                        &a.ai,
                        if !a.hold.is_empty() && a.state != "running" && a.state != "paused" {
                            // On hold, its machine stopped: its screen is off.
                            Screen::Idle
                        } else {
                            screen_of(r.st)
                        },
                    )),
            )
            // AgentLine's lines: the title row (leading-[18px]), why it's
            // where it is (12px, leading-4, in its state's tone), then what it
            // runs on and has made (mt-1, 11px, leading-4, faint).
            .child(
                v_flex()
                    .min_w_0()
                    .flex_1()
                    .child(
                        h_flex()
                            .min_w_0()
                            .gap_1p5()
                            .line_height(px(18.))
                            .child(
                                div()
                                    .min_w_0()
                                    .flex_1()
                                    .truncate()
                                    .text_size(px(13.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(if active {
                                        theme::title()
                                    } else if r.st == St::Done {
                                        theme::subtle()
                                    } else {
                                        theme::primary()
                                    })
                                    .when(a.title.is_empty(), |el| {
                                        el.font_family(theme::mono()).text_size(px(12.))
                                    })
                                    .child(title),
                            )
                            .when_some(st::number(&a.name), |el, n| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .text_size(px(12.))
                                        .text_color(theme::faint())
                                        .child(format!("#{n}")),
                                )
                            })
                            .when_some(r.when.as_ref(), |el, w| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .text_size(px(12.))
                                        .text_color(theme::faint())
                                        .child(super::sidebar::ago(w)),
                                )
                            }),
                    )
                    .child(
                        h_flex()
                            .min_w_0()
                            .gap_1()
                            .text_size(px(12.))
                            .line_height(px(16.))
                            .text_color(r.color)
                            .when(matches!(r.st, St::Needs | St::Error), |el| {
                                el.font_weight(FontWeight::MEDIUM)
                            })
                            .when(r.approval, |el| {
                                el.child(crate::icons::ic("shield-alert").size(px(12.)))
                            })
                            .child(div().min_w_0().truncate().child(r.reason.clone())),
                    )
                    .when(
                        a.pr.as_ref().is_some_and(|p| p.number > 0)
                            || usage.is_some()
                            || changes.is_some()
                            || disk.is_some()
                            || model.is_some(),
                        |el| {
                            el.child(
                                h_flex()
                                    .mt_1()
                                    .min_w_0()
                                    .gap_1p5()
                                    .overflow_hidden()
                                    .whitespace_nowrap()
                                    .text_size(px(11.))
                                    .line_height(px(16.))
                                    .text_color(theme::faint())
                                    .when_some(a.pr.as_ref().filter(|p| p.number > 0), |el, pr| {
                                        el.child(pulls::pr_chip(
                                            SharedString::from(format!("list-pr-{}", a.r#ref)),
                                            pr,
                                        ))
                                    })
                                    .when_some(usage, |el, (c, m)| {
                                        let mut parts = Vec::new();
                                        if let Some(c) = c {
                                            parts.push(format!("{}%", c.round()));
                                        }
                                        if let Some(m) = m.filter(|m| *m > 0) {
                                            parts.push(st::mem_short(m));
                                        }
                                        el.child(
                                            div()
                                                .flex_none()
                                                .rounded_sm()
                                                .font_family(theme::mono())
                                                .when(heavy, |el| {
                                                    el.px_1()
                                                        .bg(theme::amber(400).opacity(0.15))
                                                        .font_weight(FontWeight::MEDIUM)
                                                        .text_color(theme::amber(300))
                                                })
                                                .when(!heavy, |el| {
                                                    el.text_color(theme::secondary())
                                                })
                                                .child(parts.join(" · ")),
                                        )
                                    })
                                    .when_some(disk, |el, d| {
                                        el.child(
                                            div()
                                                .flex_none()
                                                .font_family(theme::mono())
                                                .text_color(theme::secondary())
                                                .child(crate::files::human_bytes(d)),
                                        )
                                    })
                                    .when_some(changes, |el, c| {
                                        el.child(
                                            div().min_w_0().truncate().child(
                                                StyledText::new(format!(
                                                    "+{} −{}",
                                                    c.insertions, c.deletions
                                                ))
                                                .with_highlights({
                                                    let plus = format!("+{}", c.insertions).len();
                                                    let all = plus
                                                        + 1
                                                        + format!("−{}", c.deletions).len();
                                                    vec![
                                                        (
                                                            0..plus,
                                                            HighlightStyle {
                                                                color: Some(
                                                                    theme::emerald(300)
                                                                        .opacity(0.8),
                                                                ),
                                                                ..Default::default()
                                                            },
                                                        ),
                                                        (
                                                            plus + 1..all,
                                                            HighlightStyle {
                                                                color: Some(
                                                                    theme::rose(300).opacity(0.8),
                                                                ),
                                                                ..Default::default()
                                                            },
                                                        ),
                                                    ]
                                                }),
                                            ),
                                        )
                                    })
                                    .when_some(model, |el, m| el.child(div().flex_none().child(m))),
                            )
                        },
                    ),
            )
            .context_menu(menu)
            .into_any_element()
    }

    /// render_strip is the panel folded: the agents as tiles (AgentStrip).
    fn render_strip(&self, cx: &mut Context<Self>) -> AnyElement {
        let rows = self.list_rows();
        let mut ordered: Vec<&ListRow> = Vec::new();
        for g in St::ORDER {
            ordered.extend(rows.iter().filter(|r| r.st == g));
        }
        // Initials, or the number where two share them.
        let labels: Vec<String> = ordered
            .iter()
            .map(|r| st::initials(&r.agent.title, &r.agent.name))
            .collect();
        let unfold: SharedString = t("panel.list.unfold").into();
        v_flex()
            .id("panel-strip")
            .w(px(52.))
            .flex_none()
            .h_full()
            .items_center()
            .gap_1()
            .py_1p5()
            .bg(theme::card())
            .border_l_1()
            .border_color(theme::line())
            .overflow_y_scroll()
            .child(
                div()
                    .id("panel-unfold")
                    .mb_1()
                    .px_1p5()
                    .rounded_md()
                    .text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                    .tooltip(move |w, cx| Tooltip::new(unfold.clone()).build(w, cx))
                    .on_click(cx.listener(|this, _: &ClickEvent, w, cx| this.toggle_panel(w, cx)))
                    .child(crate::icons::of("«")),
            )
            .children(ordered.iter().enumerate().map(|(i, r)| {
                let dup = labels.iter().filter(|l| **l == labels[i]).count() > 1;
                let label = match (dup, st::number(&r.agent.name)) {
                    (true, Some(n)) => n.to_string(),
                    _ => labels[i].clone(),
                };
                let title = if r.agent.title.is_empty() {
                    r.agent.name.clone()
                } else {
                    r.agent.title.clone()
                };
                let tip: SharedString = format!("{title} · {}", r.st.name()).into();
                let name = r.agent.name.clone();
                let active = self.open == r.agent.name;
                div()
                    .id(SharedString::from(format!("strip-{}", r.agent.r#ref)))
                    .p(px(3.))
                    .rounded_lg()
                    .cursor_pointer()
                    .when(active, |el| el.bg(theme::surface_raised()))
                    .hover(|s| s.bg(theme::surface_strong()))
                    .tooltip(move |w, cx| Tooltip::new(tip.clone()).build(w, cx))
                    .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                        this.open_chat(name.clone(), w, cx)
                    }))
                    .child(monitor(
                        &r.agent.r#ref,
                        &r.agent.ai,
                        r.st,
                        r.agent.state == "running",
                        Some(label),
                        active,
                        r.finished,
                    ))
            }))
            .into_any_element()
    }

    pub(super) fn render_files(&self) -> AnyElement {
        match &self.panel.files {
            Some(f) => f.clone().into_any_element(),
            None => loading("panel"),
        }
    }

    fn render_agent_view(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let Some(a) = self.agent_by_ref(r).cloned() else {
            return loading("panel");
        };
        let view = self.panel.view_of(r);
        if view.machine() && st::asleep(&a.state) {
            return self.render_stopped(&a, view, cx);
        }
        match view {
            PView::Desktop => self.render_desktop(&a, cx),
            PView::Terminal => match &self.terminal {
                Some(t) => div().size_full().child(t.clone()).into_any_element(),
                None => loading("panel"),
            },
            PView::Files => self.render_files(),
            PView::Evidence => self.render_evidence(Some(&a), cx),
            PView::Timeline => self.render_timeline(r, cx),
            PView::Resources => self.render_resources(r),
            PView::Browser => self.render_browser(&a, self.overlaid(), cx),
            PView::Android => self.render_android(&a, cx),
            PView::Schedules => self.render_schedules(cx),
        }
    }

    /// render_stopped is a machine view of an agent whose machine sleeps.
    fn render_stopped(&self, a: &FleetAgent, view: PView, cx: &mut Context<Self>) -> AnyElement {
        let paused = a.state == "paused";
        let name = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let r = a.r#ref.clone();
        v_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_3()
            .px_6()
            .child(
                div()
                    .size(px(48.))
                    .rounded_2xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::ink())
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_lg()
                    .text_color(theme::muted())
                    .child(crate::icons::of("⏻")),
            )
            .child(
                div()
                    .text_sm()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::primary())
                    .child(tv(
                        if paused {
                            "panel.paused.title"
                        } else {
                            "panel.stopped.title"
                        },
                        &[("name", (&name).into())],
                    )),
            )
            .child(
                div()
                    .max_w(px(320.))
                    .text_xs()
                    .text_center()
                    .text_color(theme::muted())
                    .child(tv(
                        "panel.stopped.body",
                        &[("view", (&view.label().to_lowercase()).into())],
                    )),
            )
            .child(controls::button(
                "panel-wake",
                format!(
                    "▶ {}",
                    t(if paused {
                        "panel.stopped.resume"
                    } else {
                        "panel.stopped.start"
                    })
                ),
                Btn::Primary,
                self.agent_busy(),
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.run_action(
                        r.clone(),
                        if paused {
                            Action::Resume
                        } else {
                            Action::Start
                        },
                        cx,
                    )
                }),
            ))
            .into_any_element()
    }

    /// render_desktop is the agent's desktop, live, with what it captured
    /// under it (PanelDesktop and CaptureStrip).
    fn render_desktop(&self, a: &FleetAgent, cx: &mut Context<Self>) -> AnyElement {
        let up = self.desktop_up();
        let full = self.panel.desktop_full;
        let connected = self
            .desktop
            .as_ref()
            .is_some_and(|d| d.read(cx).connected());
        let control = self.desktop.as_ref().is_some_and(|d| d.read(cx).control);
        let recording = self.panel.recording.clone().filter(|r| r.recording);
        let page = self
            .panel
            .browser
            .as_ref()
            .and_then(|b| b.pages.first())
            .map(|p| {
                if p.title.is_empty() {
                    p.url.clone()
                } else {
                    p.title.clone()
                }
            })
            .unwrap_or_default();
        let elapsed = recording
            .as_ref()
            .and_then(|r| r.started_at.as_deref())
            .map(|s| (chrono::Utc::now().timestamp_millis() - media::ms(s)) as f64 / 1000.);
        let shot_tip: SharedString = t("agent.browser.screenshotTip").into();
        let full_tip: SharedString = t("agent.browser.fullscreen").into();
        let rec_tip: SharedString = t(if recording.is_some() {
            "agent.browser.stopRecording"
        } else {
            "agent.browser.recordTip"
        })
        .into();
        let control_tip: SharedString = t("agent.browser.takeControl").into();
        let toolbar = h_flex()
            .flex_none()
            .h(px(36.))
            .px_3()
            .gap_1p5()
            .when(connected, |el| {
                el.child(
                    h_flex()
                        .flex_none()
                        .gap_1()
                        .px_1p5()
                        .rounded_sm()
                        .bg(theme::error())
                        .text_color(theme::on_bright())
                        .text_xs()
                        .font_weight(FontWeight::BOLD)
                        .child(div().size(px(5.)).rounded_full().bg(theme::on_bright()))
                        .child(t("panel.live")),
                )
            })
            .child(
                div()
                    .min_w_0()
                    .flex_1()
                    .truncate()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(page),
            )
            .child(
                div()
                    .id("desktop-shot")
                    .px_1p5()
                    .rounded_md()
                    .text_sm()
                    .text_color(theme::muted())
                    .when(connected && !self.panel.shooting, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                            .on_click(
                                cx.listener(|this, _: &ClickEvent, _, cx| this.screenshot(cx)),
                            )
                    })
                    .when(!connected || self.panel.shooting, |el| el.opacity(0.4))
                    .tooltip(move |w, cx| Tooltip::new(shot_tip.clone()).build(w, cx))
                    .child(crate::icons::of(if self.panel.shooting {
                        "…"
                    } else {
                        "⌾"
                    })),
            )
            .child(self.render_pick_button(a, connected, cx))
            .child(
                h_flex()
                    .id("desktop-record")
                    .h(px(26.))
                    .px_2()
                    .gap_1()
                    .rounded_md()
                    .when(recording.is_some(), |el| {
                        el.bg(theme::error().opacity(0.15))
                    })
                    .when(connected, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::surface_strong()))
                            .on_click(
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.toggle_recording(cx)
                                }),
                            )
                    })
                    .when(!connected, |el| el.opacity(0.4))
                    .tooltip(move |w, cx| Tooltip::new(rec_tip.clone()).build(w, cx))
                    .child(div().size(px(9.)).bg(theme::error()).map(|el| {
                        if recording.is_some() {
                            el.rounded_sm()
                        } else {
                            el.rounded_full()
                        }
                    }))
                    .when_some(elapsed, |el, s| {
                        el.child(
                            div()
                                .text_xs()
                                .font_family(theme::mono())
                                .text_color(theme::primary())
                                .child(clock(s)),
                        )
                    }),
            )
            .child(
                div()
                    .id("desktop-full")
                    .px_1p5()
                    .rounded_md()
                    .text_sm()
                    .text_color(theme::muted())
                    .when(connected, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                            .on_click(cx.listener(|this, _: &ClickEvent, w, cx| {
                                this.set_desktop_full(true, w, cx)
                            }))
                    })
                    .when(!connected, |el| el.opacity(0.4))
                    .tooltip(move |w, cx| Tooltip::new(full_tip.clone()).build(w, cx))
                    .child(crate::icons::ic("maximize-2")),
            )
            .child(
                h_flex()
                    .gap_1()
                    .child(controls::toggle(
                        "desktop-control",
                        control,
                        !connected,
                        cx.listener(|this, _: &ClickEvent, w, cx| {
                            if let Some(d) = &this.desktop {
                                d.update(cx, |d, cx| {
                                    let on = !d.control;
                                    d.set_control(on);
                                    cx.notify();
                                });
                                let focus = d.read(cx).focus_handle().clone();
                                focus.focus(w, cx);
                            }
                            cx.notify();
                        }),
                    ))
                    .child(
                        div()
                            .id("desktop-control-label")
                            .text_sm()
                            .text_color(if control {
                                theme::accent()
                            } else {
                                theme::muted()
                            })
                            .tooltip(move |w, cx| Tooltip::new(control_tip.clone()).build(w, cx))
                            .child(crate::icons::of("⌖")),
                    ),
            );
        let screen = div()
            .relative()
            .w_full()
            .h(px(335.))
            .rounded_lg()
            .overflow_hidden()
            .border_1()
            .border_color(if control && connected {
                theme::accent()
            } else {
                theme::line()
            })
            .bg(theme::pitch())
            .map(|el| match (up, &self.desktop) {
                (Some(false), _) => el.child(
                    v_flex()
                        .size_full()
                        .items_center()
                        .justify_center()
                        .gap_2p5()
                        .px_4()
                        .child(
                            div()
                                .text_lg()
                                .text_color(theme::faint())
                                .child(crate::icons::of("▣")),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(tv("panel.desktop.off", &[("name", (&a.name).into())])),
                        )
                        .child(controls::button(
                            "desktop-start",
                            format!("▶ {}", t("agent.browser.startDesktop")),
                            Btn::Primary,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.start_desktop(cx)),
                        )),
                ),
                (_, Some(_)) if full => el.child(
                    v_flex()
                        .size_full()
                        .items_center()
                        .justify_center()
                        .gap_2p5()
                        .px_4()
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t("native.vnc.fullHere")),
                        )
                        .child(controls::button(
                            "desktop-unfull",
                            t("agent.browser.exitFullscreen"),
                            Btn::Secondary,
                            false,
                            cx.listener(|this, _: &ClickEvent, w, cx| {
                                this.set_desktop_full(false, w, cx)
                            }),
                        )),
                ),
                (_, Some(d)) => el.child(d.clone()),
                _ => el.child(note(t("agent.browser.connecting"))),
            });
        // The latest twelve screenshots and recordings; the rest are in Evidence.
        let captures: Vec<MediaItem> = if self.panel.media_for == a.r#ref {
            self.panel
                .items()
                .into_iter()
                .filter(is_capture)
                .take(12)
                .collect()
        } else {
            Vec::new()
        };
        let r = a.r#ref.clone();
        let strip = v_flex()
            .flex_1()
            .min_h(px(160.))
            .px_3()
            .pt_3()
            .pb_3()
            .child(
                h_flex()
                    .mb_1p5()
                    .gap_2()
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::muted())
                            .child(t("panel.desktop.latest")),
                    )
                    .child(div().flex_1())
                    .child(
                        div()
                            .id("desktop-all-evidence")
                            .text_xs()
                            .text_color(theme::muted())
                            .cursor_pointer()
                            .hover(|s| s.text_color(theme::primary()))
                            .on_click({
                                let r = r.clone();
                                cx.listener(move |this, _: &ClickEvent, w, cx| {
                                    this.set_view(&r, PView::Evidence, w, cx)
                                })
                            })
                            .child(format!("{} →", t("panel.desktop.all"))),
                    ),
            )
            .map(|el| {
                if captures.is_empty() {
                    el.child(
                        v_flex()
                            .flex_1()
                            .min_h(px(100.))
                            .items_center()
                            .justify_center()
                            .gap_2()
                            .rounded_lg()
                            .border_1()
                            .border_dashed()
                            .border_color(theme::line())
                            .text_xs()
                            .text_color(theme::faint())
                            .child(crate::icons::of("◫"))
                            .child(if self.panel.media.is_none() {
                                t("common.loading")
                            } else {
                                t("panel.desktop.none")
                            }),
                    )
                } else {
                    el.child(
                        h_flex()
                            .flex_wrap()
                            .gap_2()
                            .children(captures.into_iter().map(|m| {
                                let (r, id) = (r.clone(), m.id.clone());
                                let caption = format!(
                                    "{}{}",
                                    if m.kind == "recording" && m.meta.duration > 0. {
                                        format!("{} · ", clock(m.meta.duration))
                                    } else {
                                        String::new()
                                    },
                                    time_ago(&m.created_at)
                                );
                                v_flex()
                                    .id(SharedString::from(format!("capture-{}", m.id)))
                                    .w(px(120.))
                                    .cursor_pointer()
                                    .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                                        this.panel.featured = Some(id.clone());
                                        this.set_view(&r, PView::Evidence, w, cx)
                                    }))
                                    .child(self.thumb(&m, 120., 75.))
                                    .child(
                                        div()
                                            .mt_1()
                                            .truncate()
                                            .text_xs()
                                            .text_color(theme::muted())
                                            .child(caption),
                                    )
                            })),
                    )
                }
            });
        div()
            .id("panel-desktop")
            .size_full()
            .overflow_y_scroll()
            .child(
                v_flex()
                    .child(toolbar)
                    .child(div().px_3().child(screen))
                    .child(strip),
            )
            .into_any_element()
    }

    /// thumb is a screenshot, or a recording's mark, with its kind's glyph.
    fn thumb(&self, m: &MediaItem, w: f32, h: f32) -> AnyElement {
        let path = (m.kind == "screenshot")
            .then(|| self.panel.files_cache.get(&m.id).cloned())
            .flatten();
        div()
            .relative()
            .w(px(w))
            .h(px(h))
            .rounded_md()
            .overflow_hidden()
            .border_1()
            .border_color(theme::line())
            .bg(theme::ink())
            .hover(|s| s.border_color(theme::accent()))
            .map(|el| match (path, self.panel.posters.get(&m.id)) {
                (Some(p), _) => el.child(img(p).size_full().object_fit(ObjectFit::Cover)),
                (None, Some(poster)) => {
                    el.child(img(poster.clone()).size_full().object_fit(ObjectFit::Cover))
                }
                (None, None) => el
                    .flex()
                    .items_center()
                    .justify_center()
                    .text_color(theme::faint())
                    .child(crate::icons::of(if m.kind == "recording" {
                        "▶"
                    } else {
                        "…"
                    })),
            })
            .child(
                div()
                    .absolute()
                    .bottom(px(3.))
                    .left(px(3.))
                    .px_1()
                    .rounded_sm()
                    .bg(theme::media_shade(0.6))
                    .text_color(theme::on_media())
                    .text_xs()
                    .child(crate::icons::of(if m.kind == "recording" {
                        "▶"
                    } else {
                        "▣"
                    })),
            )
            .into_any_element()
    }

    /// render_evidence is what whose left to show (Evidence): a recording
    /// large with its chapters, the other captures, then the reports.
    pub(super) fn render_evidence(
        &self,
        agent: Option<&FleetAgent>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let lead = agent.is_none();
        let name = agent.map(|a| a.name.clone()).unwrap_or_default();
        match &self.panel.media {
            None => return loading("panel"),
            Some(Err(e)) => return note(e.clone()),
            _ => {}
        }
        let items = self.panel.items();
        if items.is_empty() {
            let ask = agent.map(|a| a.name.clone());
            return v_flex()
                .size_full()
                .items_center()
                .justify_center()
                .gap_2()
                .px_6()
                .child(
                    div()
                        .text_lg()
                        .text_color(theme::faint())
                        .child(crate::icons::of("◫")),
                )
                .child(
                    div()
                        .max_w(px(300.))
                        .text_sm()
                        .text_center()
                        .text_color(theme::muted())
                        .child(if lead {
                            t("panel.evidence.emptyRepo")
                        } else {
                            tv("panel.evidence.empty", &[("name", (&name).into())])
                        }),
                )
                .child(controls::button(
                    "evidence-ask",
                    t("agent.mediaTab.askLead"),
                    Btn::Primary,
                    false,
                    cx.listener(move |this, _: &ClickEvent, w, cx| {
                        this.ask_lead(ask.clone(), w, cx)
                    }),
                ))
                .into_any_element();
        }
        if let Some(id) = &self.panel.viewer
            && let Some(m) = items.iter().find(|m| &m.id == id)
        {
            return self.render_viewer(m, cx);
        }
        let featured = self.featured();
        let label = |m: &MediaItem| {
            let mut parts = Vec::new();
            if lead && !m.agent_name.is_empty() {
                parts.push(m.agent_name.clone());
            } else if lead && !m.agent.is_empty() {
                parts.push(m.agent.clone());
            }
            parts.push(time_ago(&m.created_at));
            parts.join(" · ")
        };
        let captures: Vec<MediaItem> = items
            .iter()
            .filter(|m| is_capture(m) && Some(&m.id) != featured.as_ref().map(|f| &f.id))
            .cloned()
            .collect();
        let reports: Vec<MediaItem> = items.iter().filter(|m| !is_capture(m)).cloned().collect();
        let chapters: Vec<media::Chapter> = featured
            .as_ref()
            .filter(|m| m.kind == "recording")
            .and_then(|f| {
                media::gallery(&items).into_iter().find_map(|s| match s {
                    media::Shown::Recording(t) if t.media.id == f.id => Some(t.chapters),
                    _ => None,
                })
            })
            .unwrap_or_default();
        let featured_el = featured.map(|m| {
            let mut meta = vec![label(&m)];
            if m.meta.duration > 0. {
                meta.insert(0, clock(m.meta.duration));
            }
            let stage: AnyElement = if m.kind == "recording" {
                self.player_for(&m)
            } else {
                match self.panel.files_cache.get(&m.id) {
                    Some(p) => img(p.clone())
                        .w_full()
                        .h(px(335.))
                        .object_fit(ObjectFit::Contain)
                        .into_any_element(),
                    None => loading("panel"),
                }
            };
            v_flex()
                .child(
                    h_flex()
                        .mb_1p5()
                        .gap_1p5()
                        .text_xs()
                        .child(div().text_color(theme::muted()).child(crate::icons::of(
                            if m.kind == "recording" { "▶" } else { "▣" },
                        )))
                        .child(
                            div()
                                .min_w_0()
                                .truncate()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(media::readable(&m.name)),
                        )
                        .child(
                            div()
                                .flex_none()
                                .text_color(theme::faint())
                                .child(meta.join(" · ")),
                        ),
                )
                .child(
                    div()
                        .rounded_lg()
                        .overflow_hidden()
                        .border_1()
                        .border_color(theme::line())
                        .bg(theme::pitch())
                        .child(stage),
                )
                .when(!chapters.is_empty(), |el| {
                    el.child(
                        v_flex()
                            .mt_2()
                            .child(
                                div()
                                    .mb_1()
                                    .text_xs()
                                    .text_color(theme::muted())
                                    .child(t("panel.evidence.chapters")),
                            )
                            .children(chapters.iter().enumerate().map(|(i, c)| {
                                let at = c.at;
                                h_flex()
                                    .id(SharedString::from(format!("chapter-{i}")))
                                    .gap_2()
                                    .px_1p5()
                                    .py_1()
                                    .rounded_md()
                                    .text_xs()
                                    .cursor_pointer()
                                    .hover(|s| s.bg(theme::surface_strong()))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.seek_player(at, cx)
                                    }))
                                    .child(
                                        div()
                                            .flex_none()
                                            .font_family(theme::mono())
                                            .text_color(theme::accent())
                                            .child(clock(at)),
                                    )
                                    .child(
                                        div()
                                            .min_w_0()
                                            .truncate()
                                            .text_color(theme::primary())
                                            .child(c.title.clone()),
                                    )
                            })),
                    )
                })
        });
        div()
            .id("panel-evidence")
            .size_full()
            .overflow_y_scroll()
            .px_3()
            .py_3()
            .child(
                v_flex()
                    .children(featured_el)
                    .when(!captures.is_empty(), |el| {
                        el.child(
                            v_flex()
                                .mt_4()
                                .child(
                                    div()
                                        .mb_1p5()
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(t("panel.evidence.captures")),
                                )
                                .child(h_flex().flex_wrap().gap_2().children(captures.iter().map(
                                    |m| {
                                        let (id, rec) = (m.id.clone(), m.kind == "recording");
                                        v_flex()
                                            .id(SharedString::from(format!("evidence-{}", m.id)))
                                            .w(px(160.))
                                            .cursor_pointer()
                                            .on_click(cx.listener(
                                                move |this, _: &ClickEvent, _, cx| {
                                                    if rec {
                                                        this.panel.featured = Some(id.clone());
                                                        this.sync_player(cx);
                                                    } else {
                                                        this.panel.viewer = Some(id.clone());
                                                    }
                                                    cx.notify();
                                                },
                                            ))
                                            .child(self.thumb(m, 160., 100.))
                                            .child(
                                                div()
                                                    .mt_1()
                                                    .truncate()
                                                    .text_xs()
                                                    .text_color(theme::primary())
                                                    .child(media::readable(&m.name)),
                                            )
                                            .child(
                                                div()
                                                    .truncate()
                                                    .text_xs()
                                                    .text_color(theme::faint())
                                                    .child(label(m)),
                                            )
                                    },
                                ))),
                        )
                    })
                    .when(!reports.is_empty(), |el| {
                        el.child(
                            v_flex()
                                .mt_4()
                                .gap_1()
                                .child(
                                    div()
                                        .mb_0p5()
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(t("panel.evidence.pagesReports")),
                                )
                                .children(reports.iter().map(|m| {
                                    let item = m.clone();
                                    let kind_key = format!("agent.media.noun.{}", m.kind);
                                    let kind = if agentbox_i18n::has(&kind_key) {
                                        tv(&kind_key, &[("count", 1usize.into())])
                                    } else {
                                        m.kind.clone()
                                    };
                                    h_flex()
                                        .id(SharedString::from(format!("report-{}", m.id)))
                                        .gap_2p5()
                                        .px_1p5()
                                        .py_1p5()
                                        .rounded_lg()
                                        .cursor_pointer()
                                        .hover(|s| s.bg(theme::surface_strong()))
                                        // Read in the app's media viewer, as the
                                        // Media page opens them: a log or a file
                                        // as text, a folder report's page framed,
                                        // a picture as itself.
                                        .on_click(cx.listener(
                                            move |this, _: &ClickEvent, window, cx| {
                                                let list = this.panel.items();
                                                let source = this.panel.media_for.clone();
                                                this.open_media(
                                                    item.clone(),
                                                    list,
                                                    source,
                                                    window,
                                                    cx,
                                                );
                                            },
                                        ))
                                        .child(
                                            div()
                                                .flex_none()
                                                .w(px(64.))
                                                .h(px(40.))
                                                .rounded_sm()
                                                .border_1()
                                                .border_color(theme::line())
                                                .bg(theme::ink())
                                                .flex()
                                                .items_center()
                                                .justify_center()
                                                .text_color(theme::muted())
                                                .child(crate::icons::of("✎")),
                                        )
                                        .child(
                                            v_flex()
                                                .min_w_0()
                                                .flex_1()
                                                .child(
                                                    div()
                                                        .truncate()
                                                        .text_sm()
                                                        .text_color(theme::primary())
                                                        .child(m.name.clone()),
                                                )
                                                .child(
                                                    div()
                                                        .truncate()
                                                        .text_xs()
                                                        .text_color(theme::muted())
                                                        .child(format!("{kind} · {}", label(m))),
                                                ),
                                        )
                                })),
                        )
                    }),
            )
            .into_any_element()
    }

    /// render_viewer is one item of Evidence large, over the rest, with ✕.
    fn render_viewer(&self, m: &MediaItem, cx: &mut Context<Self>) -> AnyElement {
        let body: AnyElement = if m.kind == "screenshot" {
            match self.panel.files_cache.get(&m.id) {
                Some(p) => img(p.clone())
                    .w_full()
                    .object_fit(ObjectFit::Contain)
                    .into_any_element(),
                None => loading("panel"),
            }
        } else {
            div()
                .text_sm()
                .text_color(theme::primary())
                .child(if m.text.is_empty() {
                    m.name.clone()
                } else {
                    m.text.clone()
                })
                .into_any_element()
        };
        v_flex()
            .size_full()
            .child(
                h_flex()
                    .flex_none()
                    .h(px(32.))
                    .px_3()
                    .gap_2()
                    .border_b_1()
                    .border_color(theme::line())
                    .child(
                        div()
                            .min_w_0()
                            .flex_1()
                            .truncate()
                            .text_xs()
                            .text_color(theme::primary())
                            .child(media::readable(&m.name)),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(time_ago(&m.created_at)),
                    )
                    .child(
                        icon_button("viewer-close", "✕", t("common.close")).on_click(cx.listener(
                            |this, _: &ClickEvent, _, cx| {
                                this.panel.viewer = None;
                                cx.notify();
                            },
                        )),
                    ),
            )
            .child(
                div()
                    .id("viewer-body")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .p_3()
                    .child(body),
            )
            .into_any_element()
    }

    #[cfg(feature = "video")]
    fn player_for(&self, m: &MediaItem) -> AnyElement {
        match &self.panel.player {
            Some((id, p)) if id == &m.id => div()
                .w_full()
                .h(px(335.))
                .child(p.clone())
                .into_any_element(),
            _ => div().h(px(335.)).child(loading("panel")).into_any_element(),
        }
    }

    #[cfg(not(feature = "video"))]
    fn player_for(&self, _m: &MediaItem) -> AnyElement {
        div()
            .h(px(335.))
            .child(note(t("native.video.unavailable")))
            .into_any_element()
    }

    #[cfg(feature = "video")]
    fn seek_player(&mut self, at: f64, cx: &mut Context<Self>) {
        if let Some((_, p)) = &self.panel.player {
            p.update(cx, |p, cx| {
                p.seek(at, cx);
                p.play(cx);
            });
        }
    }

    #[cfg(not(feature = "video"))]
    fn seek_player(&mut self, _at: f64, _cx: &mut Context<Self>) {}

    pub(super) fn agent_busy(&self) -> bool {
        self.agent.busy_now()
    }

    /// side_questions are a project's open questions, by agent ref.
    fn side_questions(&self, project: &str) -> Vec<(String, String)> {
        self.side
            .questions_of(project)
            .iter()
            .filter(|q| !q.r#ref.is_empty())
            .map(|q| (q.r#ref.clone(), q.question.clone()))
            .collect()
    }

    fn review_refs(&self, project: &str) -> HashSet<String> {
        self.side.review_of(project).into_iter().collect()
    }
}

/// st_color is a state's colour on a row's second line: colour only for
/// what needs you (stTone).
fn st_color(st: St) -> Hsla {
    match st {
        St::Needs => theme::starting(),
        St::Error => theme::error(),
        St::Done => theme::muted(),
        _ => theme::primary().opacity(0.8),
    }
}

/// monitor is an agent's tiny computer acting out its state (AgentMonitor):
/// its screen lit by what it does, its AI tool's mark or its tile's label.
/// monitor is an agent's little screen: its tool's mark in its standing's
/// colour. Working, its edge breathes; the moment it finishes, a ring of
/// the brand bursts out from it once.
fn monitor(
    key: &str,
    ai: &str,
    st: St,
    running: bool,
    label: Option<String>,
    active: bool,
    finished: Option<std::time::Instant>,
) -> AnyElement {
    let color = match st {
        St::Needs => theme::starting(),
        St::Error => theme::error(),
        St::Review => theme::accent(),
        St::Working => theme::running(),
        St::Done => theme::faint(),
    };
    let mark = label.unwrap_or_else(|| {
        ai.chars()
            .next()
            .map(|c| c.to_uppercase().to_string())
            .unwrap_or_default()
    });
    v_flex()
        .flex_none()
        .items_center()
        .mt_0p5()
        .child({
            let screen = div()
                .w(px(36.))
                .h(px(25.))
                .rounded_md()
                .border_2()
                .border_color(if active {
                    theme::accent()
                } else {
                    color.opacity(0.8)
                })
                .bg(if running {
                    color.opacity(0.14)
                } else {
                    theme::ink()
                })
                .flex()
                .items_center()
                .justify_center()
                .text_xs()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(if running { color } else { theme::faint() })
                .child(crate::icons::of(mark));
            match (st, finished) {
                (St::Working, _) => crate::motion::breathe(
                    screen,
                    SharedString::from(format!("monitor-work-{key}")),
                    color,
                )
                .into_any_element(),
                (_, Some(t)) => crate::motion::celebrate(
                    screen,
                    SharedString::from(format!("monitor-done-{key}-{:?}", t)),
                    theme::brand(400),
                )
                .into_any_element(),
                _ => screen.into_any_element(),
            }
        })
        .child(div().w(px(10.)).h(px(3.)).bg(theme::line()))
        .into_any_element()
}

/// Screen is what an agent's monitor shows (shared/monitor.ts MonitorScreen).
#[derive(Clone, Copy, PartialEq)]
enum Screen {
    Working,
    Needs,
    Error,
    Review,
    Idle,
}

fn screen_of(st: St) -> Screen {
    match st {
        St::Needs => Screen::Needs,
        St::Error => Screen::Error,
        St::Review => Screen::Review,
        St::Working => Screen::Working,
        St::Done => Screen::Idle,
    }
}

/// mix is CSS's color-mix in srgb: `a` at `amount`, the rest `b`.
fn mix(a: Hsla, b: Hsla, amount: f32) -> Hsla {
    let (a, b) = (a.to_rgb(), b.to_rgb());
    let m = |x: f32, y: f32| x * amount + y * (1. - amount);
    Rgba {
        r: m(a.r, b.r),
        g: m(a.g, b.g),
        b: m(a.b, b.b),
        a: 1.,
    }
    .into()
}

/// agent_monitor is the monitor a row of the list leads with (AgentMonitor,
/// not tiny): a w-28 screen on a stand acting out the agent's state, still
/// (the motion is Electron's CSS; here each screen holds its still).
fn agent_monitor(ai: &str, screen: Screen) -> AnyElement {
    let mark = |size: f32, opacity: f32, grey: bool| {
        div()
            .flex_none()
            .opacity(opacity)
            .child(crate::icons::tool_mark_grey(
                ai,
                px(size),
                theme::brand(300),
                grey,
            ))
    };
    let glyph = |name: &'static str, color: Hsla| crate::icons::bold(name, 3., px(20.), color);
    let content: AnyElement = match screen {
        Screen::Working => h_flex()
            .size_full()
            .gap_2()
            .px_2p5()
            .py_2()
            .child(mark(24., 1., false))
            .child(
                // Lines of light: four bars over the one that types.
                div()
                    .relative()
                    .min_w_0()
                    .flex_1()
                    .h_full()
                    .overflow_hidden()
                    .child(
                        v_flex()
                            .absolute()
                            .left_0()
                            .right_0()
                            .bottom(px(11.))
                            .gap(px(4.))
                            .children([0.78, 0.52, 0.90, 0.64].map(|w| {
                                div()
                                    .h(px(3.))
                                    .w(relative(w))
                                    .rounded_full()
                                    .bg(theme::brand(400).opacity(0.45))
                            })),
                    )
                    .child(
                        div()
                            .absolute()
                            .left_0()
                            .bottom(px(3.))
                            .h(px(3.))
                            .w(relative(0.7))
                            .rounded_full()
                            .bg(theme::brand(300).opacity(0.8)),
                    ),
            )
            .into_any_element(),
        Screen::Error => h_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_1p5()
            .child(mark(20., 0.6, false))
            .child(glyph("x", theme::rose(300)))
            .into_any_element(),
        Screen::Review => h_flex()
            .size_full()
            .items_center()
            .justify_center()
            .gap_1p5()
            .child(mark(20., 0.7, false))
            .child(glyph("check", theme::emerald(300)))
            .into_any_element(),
        Screen::Needs | Screen::Idle => h_flex()
            .size_full()
            .items_center()
            .justify_center()
            .child(if screen == Screen::Idle {
                mark(28., 0.4, true)
            } else {
                mark(28., 1., false)
            })
            .into_any_element(),
    };
    let border = match screen {
        Screen::Needs => theme::amber(400).opacity(0.7),
        Screen::Error => theme::rose(400).opacity(0.6),
        Screen::Review => theme::emerald(400).opacity(0.45),
        _ => theme::line_strong(),
    };
    let ground = match screen {
        Screen::Error => theme::rose(400).opacity(0.25),
        Screen::Review => theme::emerald(400).opacity(0.1),
        Screen::Idle => mix(theme::term_background(), theme::term_foreground(), 0.86),
        _ => theme::term_background(),
    };
    // The inner screen is 16:9 of the frame's inside (112 − 2 − 6).
    let inner_w = 112. - 2. - 6.;
    v_flex()
        .flex_none()
        .w(px(112.))
        .items_center()
        .child(
            div()
                .relative()
                .w_full()
                .p(px(3.))
                .rounded_md()
                .border_1()
                .border_color(border)
                .bg(theme::surface_strong())
                .when(screen == Screen::Needs, |el| {
                    el.shadow(vec![BoxShadow {
                        color: theme::amber(400).opacity(0.55),
                        offset: point(px(0.), px(0.)),
                        blur_radius: px(12.),
                        spread_radius: px(1.),
                        inset: false,
                    }])
                })
                .when(screen == Screen::Working || screen == Screen::Idle, |el| {
                    el.group_hover("list-row", |s| {
                        s.border_color(theme::brand(500).opacity(0.6))
                    })
                })
                .child(
                    div()
                        .relative()
                        .w_full()
                        .h(px(inner_w * 9. / 16.))
                        .flex()
                        .items_center()
                        .justify_center()
                        .overflow_hidden()
                        .rounded(px(3.))
                        .bg(ground)
                        .text_color(theme::term_foreground())
                        .when(screen == Screen::Idle, |el| el.opacity(0.7))
                        .child(content)
                        .when(screen == Screen::Idle, |el| {
                            el.child(
                                div()
                                    .absolute()
                                    .right_2()
                                    .top_1()
                                    .text_size(px(13.))
                                    .line_height(px(13.))
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(theme::muted())
                                    .child("z"),
                            )
                        }),
                )
                .when(screen == Screen::Needs, |el| {
                    el.child(
                        div()
                            .absolute()
                            .right(px(-6.))
                            .top(px(-8.))
                            .size_5()
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_full()
                            .border_1()
                            .border_color(theme::amber(400).opacity(0.7))
                            .bg(theme::sunken())
                            .text_size(px(12.))
                            .line_height(px(12.))
                            .font_weight(FontWeight::BOLD)
                            .text_color(theme::amber(300))
                            .child("?"),
                    )
                }),
        )
        .child(div().h(px(5.)).w_4().bg(theme::ghost()))
        .child(div().h(px(3.)).w_10().rounded_full().bg(theme::ghost()))
        .into_any_element()
}

fn count_pill(n: usize) -> AnyElement {
    div()
        .px_1p5()
        .rounded_full()
        .bg(theme::surface_strong())
        .text_xs()
        .text_color(theme::muted())
        .child(n.to_string())
        .into_any_element()
}

/// loading is what stands where a list will be while it loads: rows of
/// skeletons shimmering softly, rather than a word.
pub(super) fn loading(id: &str) -> AnyElement {
    crate::motion::skeleton_rows(id, 3).into_any_element()
}

pub(super) fn note(text: String) -> AnyElement {
    div()
        .p_4()
        .text_xs()
        .text_color(theme::faint())
        .child(text)
        .into_any_element()
}

pub(super) fn icon_button(id: &'static str, glyph: &'static str, tip: String) -> Stateful<Div> {
    // Button size icon-sm, ghost: size-8 rounded-lg, a 15px icon, muted.
    let tip: SharedString = tip.into();
    div()
        .id(id)
        .flex_none()
        .size_8()
        .flex()
        .items_center()
        .justify_center()
        .rounded_lg()
        .text_color(theme::muted())
        .cursor_pointer()
        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
        .tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
        .child(match crate::icons::lucide(glyph) {
            Some(name) => crate::icons::ic(name).size(px(15.)).into_any_element(),
            None => div().text_sm().child(glyph).into_any_element(),
        })
}

/// peek_lines are what an agent's session adds to its row's tooltip: its
/// model and how full its context is.
fn model_name(s: &agentbox_api::ChatSession) -> Option<String> {
    let m = s
        .options
        .iter()
        .find(|o| o.category == "model" && o.r#type == "select")?;
    Some(
        m.choices
            .iter()
            .find(|c| c.value == m.value)
            .map(|c| {
                c.name
                    .trim_end_matches(" (recommended)")
                    .trim_end_matches(" (Recommended)")
                    .to_string()
            })
            .unwrap_or_else(|| m.value.clone()),
    )
}

fn peek_lines(s: &agentbox_api::ChatSession) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(m) = s
        .options
        .iter()
        .find(|o| o.category == "model" && o.r#type == "select")
    {
        out.push(
            m.choices
                .iter()
                .find(|c| c.value == m.value)
                .map(|c| c.name.clone())
                .unwrap_or_else(|| m.value.clone()),
        );
    }
    if s.context_size > 0 {
        out.push(tv(
            "panel.list.contextTip",
            &[
                ("percent", (s.context_used * 100 / s.context_size).into()),
                ("size", crate::chat::tokens(s.context_size).into()),
            ],
        ));
    }
    out
}
