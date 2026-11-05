//! The search palette (SearchModal.tsx), Ctrl+K from anywhere: what's typed
//! searched in every project at once (GET /v1/search) — projects, sessions,
//! agents, chats, memory, media, skills, connectors, notes and pull requests
//! — with chips narrowing it to one kind, past agents' chats on request, the
//! project and how far back as menus, newest first or best of each, and the
//! result you're on read beside the list before you go there (a chat message
//! among the ones around it, a report or a memory whole, a screenshot).
//!
//! It is the command palette too (paletteCommands.tsx, lib/palette.ts): the
//! actions and every setting, matched as you type, above what was found when
//! what you typed starts with a verb, below it otherwise, and alone after
//! ">". An action for an agent or a project runs on the one you're on, or
//! asks for one in the palette (Shift+Enter asks for another).
use std::path::PathBuf;

use agentbox_api::{
    ChatItem, ChatThread, HOME_PROJECT, MediaItem, RecordingStatus, SEARCH_AGENTS, SEARCH_CHATS,
    SEARCH_CONNECTORS, SEARCH_EVENTS, SEARCH_MEDIA, SEARCH_MEMORIES, SEARCH_PROJECTS, SEARCH_PULLS,
    SEARCH_REPORTS, SEARCH_SESSIONS, SEARCH_SKILLS, SearchHit, SearchResults,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent_status::{Action, lifecycle_actions};
use super::controls::{Choice, select_at};
use crate::{theme, toasts};

/// The palette's own chips, beside the daemon's kinds.
const ACTIONS: &str = "@actions";
const SETTINGS: &str = "@settings";
/// How many of one kind a chip asks for (SearchModal.tsx kindLimit).
const KIND_LIMIT: usize = 50;
/// How many of a chat's messages the preview reads around the one found.
const AROUND: usize = 7;

#[derive(Clone, Copy, PartialEq)]
enum Needs {
    Agent,
    Project,
}

/// Cmd is something the palette does rather than finds: an action, or a
/// setting, which opens Settings at it.
#[derive(Clone)]
struct Cmd {
    id: String,
    setting: bool,
    title: String,
    keywords: String,
    /// place is where a setting lives: its section and group.
    place: String,
    needs: Option<Needs>,
    keys: &'static str,
    section: String,
}

#[derive(Clone)]
enum Row {
    Cmd(Cmd),
    Hit(String, Box<SearchHit>),
}

enum Preview {
    Chat(Vec<ChatItem>),
    Image(PathBuf),
    Failed(String),
}

pub struct Palette {
    input: Entity<InputState>,
    all: Option<SearchResults>,
    one: Option<SearchResults>,
    asked: String,
    selected: usize,
    kind: String,
    project: String,
    since: String,
    archived: bool,
    newest: bool,
    /// An action that asked which agent or project it's for, and the query
    /// typed before it asked.
    picking: Option<Cmd>,
    saved: String,
    open_when_answered: bool,
    error: Option<String>,
    preview: Option<(String, Preview)>,
    _sub: Subscription,
    _task: Option<Task<()>>,
    _preview: Option<Task<()>>,
}

// ——— Matching (lib/palette.ts) ———

fn normalize(text: &str) -> String {
    text.chars()
        .filter(|c| *c != '\'' && *c != '’')
        .flat_map(char::to_lowercase)
        .map(|c| match c {
            'á' | 'à' | 'â' | 'ã' | 'ä' => 'a',
            'é' | 'è' | 'ê' | 'ë' => 'e',
            'í' | 'ì' | 'î' | 'ï' => 'i',
            'ó' | 'ò' | 'ô' | 'õ' | 'ö' => 'o',
            'ú' | 'ù' | 'û' | 'ü' => 'u',
            'ç' => 'c',
            'ñ' => 'n',
            c => c,
        })
        .collect()
}

fn words_of(text: &str) -> Vec<String> {
    normalize(text)
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| !w.is_empty())
        .map(String::from)
        .collect()
}

/// commands_only is a query starting with ">": only actions and settings.
fn commands_only(query: &str) -> (bool, String) {
    let q = query.trim_start();
    match q.strip_prefix('>') {
        Some(rest) => (true, rest.trim().to_string()),
        None => (false, query.trim().to_string()),
    }
}

/// looks_like_verb is whether the query starts with one of the language's
/// verbs, or the start of one typed so far.
fn looks_like_verb(query: &str, verbs: &[String]) -> bool {
    let Some(first) = words_of(&commands_only(query).1).into_iter().next() else {
        return false;
    };
    if first.chars().count() < 2 {
        return false;
    }
    verbs.iter().any(|v| {
        let v = normalize(v);
        v == first || (first.chars().count() >= 3 && v.starts_with(&first))
    })
}

/// score is how well a command answers the query, 0 for not at all.
fn score(c: &Cmd, query: &str) -> i32 {
    let words = words_of(query);
    if words.is_empty() {
        return 0;
    }
    let title = words_of(&c.title);
    let rest = words_of(&format!("{} {}", c.keywords, c.place));
    let starts = |l: &[String], w: &str| l.iter().any(|x| x.starts_with(w));
    let inside = |l: &[String], w: &str| w.chars().count() >= 3 && l.iter().any(|x| x.contains(w));
    if !words
        .iter()
        .all(|w| starts(&title, w) || starts(&rest, w) || inside(&title, w) || inside(&rest, w))
    {
        return 0;
    }
    let brevity = (50 - title.len() as i32 * 5).max(0);
    if title.join(" ").starts_with(&words.join(" ")) {
        400 + brevity
    } else if words.iter().all(|w| starts(&title, w)) {
        300 + brevity
    } else if words.iter().all(|w| starts(&title, w) || inside(&title, w)) {
        200 + brevity
    } else {
        100 + brevity
    }
}

fn rank(commands: Vec<Cmd>, query: &str) -> Vec<Cmd> {
    let q = commands_only(query).1;
    if words_of(&q).is_empty() {
        return commands;
    }
    let mut scored: Vec<(i32, usize, Cmd)> = commands
        .into_iter()
        .enumerate()
        .map(|(i, c)| (score(&c, &q), i, c))
        .filter(|(s, _, _)| *s > 0)
        .collect();
    scored.sort_by(|a, b| b.0.cmp(&a.0).then(a.1.cmp(&b.1)));
    scored.into_iter().map(|(_, _, c)| c).collect()
}

/// marked is text with what was searched for marked in it.
fn marked(text: &str, query: &str) -> AnyElement {
    let lower = text.to_lowercase();
    let mut ranges: Vec<std::ops::Range<usize>> = Vec::new();
    if lower.len() == text.len() {
        for w in query.split_whitespace().map(str::to_lowercase) {
            if w.is_empty() {
                continue;
            }
            let mut from = 0;
            while let Some(i) = lower[from..].find(&w) {
                let s = from + i;
                ranges.push(s..s + w.len());
                from = s + w.len();
            }
        }
    }
    if ranges.is_empty() {
        return text.to_string().into_any_element();
    }
    ranges.sort_by_key(|r| r.start);
    let mut merged: Vec<std::ops::Range<usize>> = Vec::new();
    for r in ranges {
        match merged.last_mut() {
            Some(l) if r.start <= l.end => l.end = l.end.max(r.end),
            _ => merged.push(r),
        }
    }
    let style = HighlightStyle {
        background_color: Some(theme::accent().opacity(0.3)),
        ..Default::default()
    };
    StyledText::new(text.to_string())
        .with_highlights(merged.into_iter().map(|r| (r, style)))
        .into_any_element()
}

/// a_message is whether a result is a chat message.
fn a_message(kind: &str, hit: &SearchHit) -> bool {
    kind == SEARCH_CHATS || (kind == SEARCH_SESSIONS && !hit.item.is_empty())
}

fn kind_label(kind: &str) -> String {
    let key = format!("search.group.{kind}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        t("search.group.other")
    }
}

/// since_time is how far back a "since" asks, as an RFC 3339 time.
fn since_time(since: &str) -> Option<String> {
    let days = match since {
        "day" => 1,
        "week" => 7,
        "month" => 30,
        _ => return None,
    };
    Some((chrono::Utc::now() - chrono::Duration::days(days)).to_rfc3339())
}

/// chat_path is where a found message's chat is read.
fn chat_path(hit: &SearchHit) -> String {
    let p = &hit.project;
    if !hit.session.is_empty() && hit.session != "main" {
        format!("/v1/projects/{p}/sessions/{}/chat", hit.session)
    } else if hit.agent.is_empty() || hit.agent == "lead" || hit.agent.starts_with("lead.") {
        match hit.agent.strip_prefix("lead.") {
            Some(s) => format!("/v1/projects/{p}/sessions/{s}/chat"),
            None => format!("/v1/projects/{p}/chat"),
        }
    } else {
        format!("/v1/agents/{p}/{}/chat", hit.agent)
    }
}

impl AppView {
    pub(super) fn open_search(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let input = cx.new(|cx| InputState::new(window, cx).placeholder(t("search.placeholder")));
        let sub = cx.subscribe_in(
            &input,
            window,
            |this, _, ev: &InputEvent, window, cx| match ev {
                InputEvent::Change => this.on_typed(cx),
                InputEvent::PressEnter { shift, .. } => this.palette_enter(*shift, window, cx),
                _ => {}
            },
        );
        input.update(cx, |s, cx| s.focus(window, cx));
        self.side.palette = Some(Palette {
            input,
            all: None,
            one: None,
            asked: String::new(),
            selected: 0,
            kind: String::new(),
            project: String::new(),
            since: String::new(),
            archived: false,
            newest: false,
            picking: None,
            saved: String::new(),
            open_when_answered: false,
            error: None,
            preview: None,
            _sub: sub,
            _task: None,
            _preview: None,
        });
        cx.notify();
    }

    fn close_search(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.side.palette = None;
        self.side.root_focus.focus(window, cx);
        cx.notify();
    }

    fn on_typed(&mut self, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        p.selected = 0;
        if p.picking.is_some() {
            cx.notify();
            return;
        }
        self.run_search(cx);
    }

    /// run_search asks once the typing pauses: every kind at once, for the
    /// chips' counts and the list, and the picked kind on its own for more.
    fn run_search(&mut self, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        let typed = p.input.read(cx).value().to_string();
        let (only, _) = commands_only(&typed);
        let q = typed.trim().to_string();
        if q.is_empty() || only {
            p.all = None;
            p.one = None;
            p.asked = String::new();
            p._task = None;
            self.sync_preview(cx);
            cx.notify();
            return;
        }
        let mut params = format!("q={}", crate::files::encode(&q));
        if !p.project.is_empty() {
            params.push_str(&format!("&project={}", crate::files::encode(&p.project)));
        }
        if let Some(s) = since_time(&p.since) {
            params.push_str(&format!("&since={}", crate::files::encode(&s)));
        }
        if p.archived {
            params.push_str("&archived=true");
        }
        let kind = (!p.kind.starts_with('@'))
            .then(|| p.kind.clone())
            .filter(|k| !k.is_empty());
        let client = self.client.clone();
        p._task = Some(cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(140))
                .await;
            let all = client
                .get::<SearchResults>(&format!("/v1/search?{params}"))
                .await;
            let one = match &kind {
                Some(k) => Some(
                    client
                        .get::<SearchResults>(&format!(
                            "/v1/search?{params}&kinds={k}&limit={KIND_LIMIT}"
                        ))
                        .await,
                ),
                None => None,
            };
            this.update(cx, |this, cx| {
                let Some(p) = this.side.palette.as_mut() else {
                    return;
                };
                match all {
                    Ok(r) => {
                        p.all = Some(r);
                        p.error = None;
                    }
                    Err(e) => p.error = Some(e.to_string()),
                }
                p.one = one.and_then(Result::ok);
                if p.asked != q {
                    p.selected = 0;
                }
                p.asked = q;
                if std::mem::take(&mut p.open_when_answered) {
                    this.palette_go(Some(0), false, None, cx);
                } else {
                    this.sync_preview(cx);
                }
                cx.notify();
            })
            .ok();
        }));
    }

    // ——— What can be done ———

    /// current_agent is the agent whose page is open (not a lead's).
    fn current_agent(&self) -> Option<String> {
        if self.settings || self.work.is_some() || self.side.page.is_some() || self.is_home() {
            return None;
        }
        self.open_agent().map(|a| a.r#ref.clone())
    }

    fn current_project(&self) -> Option<String> {
        if self.settings || self.side.page.is_some() {
            return None;
        }
        self.project.clone().filter(|p| p != HOME_PROJECT)
    }

    /// commands is every action that can run here, then every setting.
    fn commands(&self, cx: &App) -> Vec<Cmd> {
        let action = |id: &str, needs: Option<Needs>, keys: &'static str| Cmd {
            id: id.to_string(),
            setting: false,
            title: t(&format!("palette.{id}.title")),
            keywords: t(&format!("palette.{id}.keywords")),
            place: String::new(),
            needs,
            keys,
            section: String::new(),
        };
        let agent = |id: &str| action(id, Some(Needs::Agent), "");
        let project = |id: &str| action(id, Some(Needs::Project), "");
        let here = self.current_agent();
        let here_agent = here.as_ref().and_then(|r| self.agent_by_ref(r));
        let states: Vec<Action> = match here_agent {
            Some(a) => lifecycle_actions(&a.state),
            None => vec![Action::Start, Action::Stop, Action::Pause, Action::Resume],
        };
        let android = here.is_none()
            || here_agent
                .and_then(|a| self.projects.iter().find(|p| p.name == a.project))
                .is_some_and(|p| p.android);
        let mut out = vec![
            action("new-agent", None, ""),
            action("new-task", None, "Ctrl+N"),
            action("add-project", None, ""),
            project("fan-out"),
            agent("record"),
            agent("record-stop"),
            agent("screenshot"),
        ];
        if android {
            out.push(agent("android-start"));
            out.push(agent("android-stop"));
        }
        for a in [Action::Start, Action::Stop, Action::Pause, Action::Resume] {
            if states.contains(&a) {
                out.push(agent(&format!("agent-{}", a.path())));
            }
        }
        out.extend(["agent-chat", "agent-terminal", "agent-desktop"].map(agent));
        if android {
            out.push(agent("agent-android"));
        }
        out.extend(
            [
                "agent-media",
                "agent-snapshots",
                "agent-settings",
                "agent-secrets",
            ]
            .map(agent),
        );
        out.extend(
            [
                "project-chat",
                "project-pulls",
                "project-media",
                "project-past",
                "project-settings",
                "project-repository",
                "project-notes",
                "project-memory",
                "project-tokens",
                "project-secrets",
                "project-connectors",
                "project-skills",
                "schedule",
                "nesting",
            ]
            .map(project),
        );
        out.extend(
            [
                ("home", ""),
                ("jobs", ""),
                ("all-media", ""),
                ("past-agents", ""),
                ("settings", "Ctrl+,"),
                ("shortcuts", "Ctrl+/"),
                ("add-connector", ""),
                ("add-skill", ""),
            ]
            .map(|(id, k)| action(id, None, k)),
        );
        // Phones pair with this machine's own daemon, which Settings has.
        #[cfg(target_os = "linux")]
        out.push(action("connect-phone", None, ""));
        let dnd = crate::prefs::dnd_until() > chrono::Utc::now().timestamp_millis();
        out.push(action(if dnd { "dnd-off" } else { "dnd-on" }, None, ""));
        out.extend(
            ["report-problem", "diagnostics", "whats-new", "tour"].map(|id| action(id, None, "")),
        );
        for s in self.palette_settings(cx) {
            out.push(Cmd {
                id: format!("setting:{}:{}", s.section, s.id),
                setting: true,
                title: s.label,
                keywords: s.keywords,
                place: s.place,
                needs: None,
                keys: "",
                section: s.section,
            });
        }
        out
    }

    /// rows is the list as it stands: the commands and what was found, in
    /// the order the arrows walk (paletteRows).
    fn rows(&self, cx: &App) -> Vec<Row> {
        let Some(p) = self.side.palette.as_ref() else {
            return Vec::new();
        };
        let query = p.input.read(cx).value().to_string();
        let (only, rest) = commands_only(&query);
        let typed = !rest.is_empty();
        let ranked = rank(self.commands(cx), &query);
        let actions = || ranked.iter().filter(|c| !c.setting).cloned().map(Row::Cmd);
        let settings = || ranked.iter().filter(|c| c.setting).cloned().map(Row::Cmd);
        if p.kind == ACTIONS {
            return actions().collect();
        }
        if p.kind == SETTINGS {
            return settings().collect();
        }
        let results = if p.kind.is_empty() {
            p.all.as_ref()
        } else {
            p.one.as_ref().or(p.all.as_ref())
        };
        let mut found: Vec<Row> = if p.asked.is_empty() || only {
            Vec::new()
        } else {
            results
                .map(|r| {
                    r.groups
                        .iter()
                        .filter(|g| p.kind.is_empty() || g.kind == p.kind)
                        .flat_map(|g| {
                            g.hits
                                .iter()
                                .map(|h| Row::Hit(g.kind.clone(), Box::new(h.clone())))
                        })
                        .collect()
                })
                .unwrap_or_default()
        };
        if p.newest {
            let at = |r: &Row| match r {
                Row::Hit(_, h) => h.at.clone().unwrap_or_default(),
                Row::Cmd(_) => String::new(),
            };
            found.sort_by_key(|r| std::cmp::Reverse(at(r)));
        }
        if !p.kind.is_empty() {
            return found;
        }
        if !typed {
            return actions().collect();
        }
        let commands: Vec<Row> = actions().chain(settings()).collect();
        if only {
            return commands;
        }
        let verbs: Vec<String> = t("palette.verbs")
            .split_whitespace()
            .map(String::from)
            .collect();
        if looks_like_verb(&query, &verbs) {
            commands.into_iter().chain(found).collect()
        } else {
            found.into_iter().chain(commands).collect()
        }
    }

    /// choices is what a pick offers: the agents (not a lead) or the
    /// projects, the one you're on first.
    fn choices(&self, needs: Needs, query: &str) -> Vec<(String, String, String)> {
        let all: Vec<(String, String, String)> = match needs {
            Needs::Agent => self
                .side
                .agents
                .iter()
                .filter(|a| a.name != "lead" && !a.name.starts_with("lead."))
                .map(|a| {
                    let title = if a.title.is_empty() {
                        a.name.clone()
                    } else {
                        a.title.clone()
                    };
                    (
                        a.r#ref.clone(),
                        title,
                        format!("{} · {}", a.name, self.label_of(&a.project)),
                    )
                })
                .collect(),
            Needs::Project => self
                .projects
                .iter()
                .map(|p| {
                    (
                        p.name.clone(),
                        super::sidebar_actions::label_of(p),
                        p.name.clone(),
                    )
                })
                .collect(),
        };
        let words = words_of(query);
        let current = match needs {
            Needs::Agent => self.current_agent(),
            Needs::Project => self.current_project(),
        };
        let mut found: Vec<_> = all
            .into_iter()
            .filter(|(id, title, detail)| {
                let hay = words_of(&format!("{title} {id} {detail}"));
                words.iter().all(|w| {
                    hay.iter().any(|x| {
                        x.starts_with(w.as_str())
                            || (w.chars().count() >= 3 && x.contains(w.as_str()))
                    })
                })
            })
            .collect();
        found.sort_by_key(|(id, _, _)| Some(id) != current.as_ref());
        found
    }

    fn label_of(&self, project: &str) -> String {
        if project == HOME_PROJECT {
            return t("shell.nav.home");
        }
        self.projects
            .iter()
            .find(|p| p.name == project)
            .map_or_else(|| project.to_string(), super::sidebar_actions::label_of)
    }

    fn agent_title(&self, project: &str, name: &str) -> String {
        if name == "lead" || name.starts_with("lead.") {
            return t("chat.find.from.lead");
        }
        self.side
            .agents
            .iter()
            .find(|a| a.project == project && a.name == name && !a.title.is_empty())
            .map_or_else(|| name.to_string(), |a| a.title.clone())
    }

    /// where_of is a result's place: its project, its agent, past.
    fn where_of(&self, kind: &str, h: &SearchHit) -> String {
        let project = if !h.project.is_empty() {
            self.label_of(&h.project)
        } else if kind == SEARCH_SKILLS || kind == SEARCH_CONNECTORS {
            t("search.agentboxWide")
        } else {
            String::new()
        };
        let agent = if !h.agent.is_empty() && kind != SEARCH_AGENTS {
            self.agent_title(&h.project, &h.agent)
        } else {
            String::new()
        };
        let past = if h.archived {
            t(if kind == SEARCH_SESSIONS {
                "search.closedSession"
            } else {
                "search.pastAgent"
            })
        } else {
            String::new()
        };
        [project, agent, past]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join(" · ")
    }

    /// for_whom is who an action runs on: the one you're on, or that it asks.
    fn for_whom(&self, c: &Cmd) -> Option<String> {
        let needs = c.needs?;
        Some(match needs {
            Needs::Agent => match self.current_agent() {
                Some(r) => {
                    let (p, a) = r.split_once('/').unwrap_or((&r, ""));
                    tv(
                        "palette.for.agent",
                        &[("name", self.agent_title(p, a).into())],
                    )
                }
                None => t("palette.asksAgent"),
            },
            Needs::Project => match self.current_project() {
                Some(p) => tv("palette.for.project", &[("name", self.label_of(&p).into())]),
                None => t("palette.asksProject"),
            },
        })
    }

    // ——— Doing it ———

    fn palette_enter(&mut self, shift: bool, window: &mut Window, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        if p.picking.is_some() {
            return self.pick(None, window, cx);
        }
        let typed = p.input.read(cx).value().trim().to_string();
        let selected = p.selected;
        let rows = self.rows(cx);
        let pending = !commands_only(&typed).0
            && typed
                != self
                    .side
                    .palette
                    .as_ref()
                    .map_or(String::new(), |p| p.asked.clone());
        // Commands answer at once; what the daemon finds is waited for.
        if pending && !matches!(rows.get(selected), Some(Row::Cmd(_))) {
            if let Some(p) = self.side.palette.as_mut() {
                p.open_when_answered = true;
            }
            return;
        }
        self.palette_go(None, shift, Some(window), cx);
    }

    /// palette_go runs the row: a command, or opens what was found (Shift:
    /// another agent or project, or the chat's find on what was searched).
    fn palette_go(
        &mut self,
        index: Option<usize>,
        shift: bool,
        window: Option<&mut Window>,
        cx: &mut Context<Self>,
    ) {
        let Some(p) = self.side.palette.as_ref() else {
            return;
        };
        let ix = index.unwrap_or(p.selected);
        let asked = p.asked.clone();
        let Some(row) = self.rows(cx).into_iter().nth(ix) else {
            return;
        };
        let Some(window) = window else {
            // From the answer's arrival: on the next frame, with its window.
            let view = cx.entity().downgrade();
            cx.defer(move |cx| {
                if let Some(w) = cx.active_window() {
                    w.update(cx, |_, window, cx| {
                        view.update(cx, |this, cx| {
                            this.palette_go(Some(ix), shift, Some(window), cx)
                        })
                        .ok();
                    })
                    .ok();
                }
            });
            return;
        };
        match row {
            Row::Cmd(c) => self.run_command(c, shift, window, cx),
            Row::Hit(kind, hit) => {
                self.close_search(window, cx);
                self.open_hit(kind, *hit, shift.then_some(asked), window, cx);
            }
        }
    }

    fn run_command(&mut self, c: Cmd, other: bool, window: &mut Window, cx: &mut Context<Self>) {
        if c.setting {
            self.close_search(window, cx);
            return self.open_setting(c.section, c.title, window, cx);
        }
        let current = match c.needs {
            Some(Needs::Agent) => self.current_agent(),
            Some(Needs::Project) => self.current_project(),
            None => Some(String::new()),
        };
        match current {
            Some(target) if !other || c.needs.is_none() => {
                self.close_search(window, cx);
                self.run_action_on(&c.id, target, window, cx);
            }
            _ => {
                // Ask which, in the palette.
                let Some(p) = self.side.palette.as_mut() else {
                    return;
                };
                p.saved = p.input.read(cx).value().to_string();
                let placeholder = t(if c.needs == Some(Needs::Agent) {
                    "palette.pick.agent"
                } else {
                    "palette.pick.project"
                });
                p.picking = Some(c);
                p.selected = 0;
                p.input.update(cx, |s, cx| {
                    s.set_value("", window, cx);
                    s.set_placeholder(placeholder, window, cx);
                });
                cx.notify();
            }
        }
    }

    /// pick runs the action asked about on the agent or project chosen.
    fn pick(&mut self, index: Option<usize>, window: &mut Window, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_ref() else {
            return;
        };
        let Some(c) = p.picking.clone() else { return };
        let q = p.input.read(cx).value().to_string();
        let ix = index.unwrap_or(p.selected);
        let Some((id, _, _)) = self
            .choices(c.needs.unwrap_or(Needs::Project), &q)
            .into_iter()
            .nth(ix)
        else {
            return;
        };
        self.close_search(window, cx);
        self.run_action_on(&c.id, id, window, cx);
    }

    /// back steps out of a pick, to the query before it.
    fn pick_back(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        p.picking = None;
        p.selected = 0;
        let saved = std::mem::take(&mut p.saved);
        p.input.update(cx, |s, cx| {
            s.set_value(saved, window, cx);
            s.set_placeholder(t("search.placeholder"), window, cx);
        });
        cx.notify();
    }

    /// run_action_on does an action, on the agent's ref or the project's name.
    fn run_action_on(
        &mut self,
        id: &str,
        target: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        use super::panel::PView;
        use super::pulls::RailTab;
        let project_of = |r: &str| {
            r.split_once('/')
                .map_or(r.to_string(), |(p, _)| p.to_string())
        };
        let name_of = |this: &Self, r: &str| {
            let (p, a) = r.split_once('/').unwrap_or((r, ""));
            this.agent_title(p, a)
        };
        let lead_tab =
            |this: &mut Self, tab: RailTab, window: &mut Window, cx: &mut Context<Self>| {
                this.select_project(target.clone(), window, cx);
                this.pulls.tab = tab;
                if tab == RailTab::Pulls {
                    this.load_pulls(false, cx);
                }
                this.panel_sync(window, cx);
            };
        match id {
            "new-agent" => {
                let p = self
                    .current_project()
                    .or_else(|| self.projects.first().map(|p| p.name.clone()))
                    .unwrap_or_default();
                self.open_new_agent(p, window, cx);
            }
            "new-task" => self.open_new_task(window, cx),
            "add-project" => self.open_add_project(window, cx),
            "fan-out" => self.open_fan_out(target, window, cx),
            "record" => {
                self.open_agent_ref(target.clone(), window, cx);
                self.set_view(&target, PView::Desktop, window, cx);
                let (p, a) = target.split_once('/').unwrap_or_default();
                let path = format!("/v1/agents/{p}/{a}/media/record/start");
                let done = tv(
                    "palette.done.record",
                    &[("agent", name_of(self, &target).into())],
                );
                let client = self.client.clone();
                cx.spawn(async move |this, cx| {
                    let res = client
                        .post::<_, RecordingStatus>(
                            &path,
                            &agentbox_api::RecordRequest {
                                name: "screen recording".into(),
                                ..Default::default()
                            },
                        )
                        .await;
                    this.update(cx, |this, cx| match res {
                        Ok(s) => {
                            this.panel.recording = Some(s);
                            toasts::info(cx, done);
                            cx.notify();
                        }
                        Err(e) => toasts::error(cx, e.to_string()),
                    })
                    .ok();
                })
                .detach();
            }
            "record-stop" => {
                let (p, a) = target.split_once('/').unwrap_or_default();
                let path = format!("/v1/agents/{p}/{a}/media/record/stop");
                let client = self.client.clone();
                cx.spawn(async move |this, cx| {
                    let res = client
                        .post::<_, MediaItem>(&path, &serde_json::json!({}))
                        .await;
                    this.update(cx, |this, cx| match res {
                        Ok(m) => {
                            this.saved(target, m, cx);
                            this.load_desktop_status(cx);
                        }
                        Err(e) => toasts::error(cx, e.to_string()),
                    })
                    .ok();
                })
                .detach();
            }
            "screenshot" => {
                self.open_agent_ref(target, window, cx);
                self.screenshot(cx);
            }
            "android-start" => {
                self.open_agent_ref(target.clone(), window, cx);
                self.set_view(&target, PView::Android, window, cx);
                self.start_android(cx);
            }
            "android-stop" => {
                self.open_agent_ref(target, window, cx);
                self.stop_android(cx);
            }
            "agent-start" | "agent-stop" | "agent-pause" | "agent-resume" => {
                let action = match id {
                    "agent-start" => Action::Start,
                    "agent-stop" => Action::Stop,
                    "agent-pause" => Action::Pause,
                    _ => Action::Resume,
                };
                let done = tv(
                    &format!("palette.done.{}", action.path()),
                    &[("agent", name_of(self, &target).into())],
                );
                self.run_action(target, action, cx);
                toasts::info(cx, done);
            }
            "agent-chat" => self.open_agent_ref(target, window, cx),
            "agent-terminal" | "agent-desktop" | "agent-android" | "agent-media" => {
                self.open_agent_ref(target.clone(), window, cx);
                let view = match id {
                    "agent-terminal" => PView::Terminal,
                    "agent-desktop" => PView::Desktop,
                    "agent-android" => PView::Android,
                    _ => PView::Evidence,
                };
                self.set_view(&target, view, window, cx);
            }
            "agent-snapshots" => {
                self.open_agent_ref(target, window, cx);
                self.open_snapshots(cx);
            }
            "agent-settings" | "agent-secrets" => {
                self.open_agent_ref(target, window, cx);
                self.open_agent_settings(cx);
            }
            "project-chat" => self.select_project(target, window, cx),
            "project-pulls" => lead_tab(self, RailTab::Pulls, window, cx),
            "project-media" => lead_tab(self, RailTab::Evidence, window, cx),
            "schedule" => lead_tab(self, RailTab::Schedules, window, cx),
            "project-past" => self.open_work(target, String::new(), window, cx),
            "project-settings" | "project-repository" | "project-notes" | "project-memory"
            | "project-tokens" | "project-secrets" | "project-connectors" | "project-skills"
            | "nesting" => {
                self.open_settings_at(format!("project:{}", project_of(&target)), window, cx)
            }
            "home" => self.select_project(HOME_PROJECT.into(), window, cx),
            "jobs" => self.open_foot_page(super::foot::FootPage::Jobs, window, cx),
            "all-media" => self.open_foot_page(super::foot::FootPage::Media, window, cx),
            "past-agents" => self.open_work(String::new(), String::new(), window, cx),
            "settings" => self.open_settings(window, cx),
            "shortcuts" => {
                self.side.dialog = Some(super::sidebar_dialogs::Dialog::Keys);
            }
            "add-connector" => self.open_settings_at("connectors".into(), window, cx),
            "add-skill" => self.open_settings_at("skills".into(), window, cx),
            "connect-phone" => self.open_settings_at("phone".into(), window, cx),
            "dnd-on" => {
                crate::prefs::set_dnd_until(chrono::Utc::now().timestamp_millis() + 3_600_000);
                toasts::info(cx, t("palette.done.dnd"));
            }
            "dnd-off" => crate::prefs::set_dnd_until(0),
            "report-problem" => self.open_settings_at("general".into(), window, cx),
            "diagnostics" => self.open_settings_at("diagnostics".into(), window, cx),
            "whats-new" => {
                let v = self.conn.version.clone().unwrap_or_default();
                self.show_whats_new(v, cx);
            }
            "tour" => self.replay_tour(cx),
            _ => {}
        }
        cx.notify();
    }

    /// open_hit opens a result where it lives; a chat message at itself, or
    /// with the chat's find on what was searched (find).
    fn open_hit(
        &mut self,
        kind: String,
        hit: SearchHit,
        find: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let project = if hit.project.is_empty() && kind == SEARCH_PROJECTS {
            hit.id.clone()
        } else {
            hit.project.clone()
        };
        let message = a_message(&kind, &hit);
        let item = if hit.item.is_empty() {
            hit.id.clone()
        } else {
            hit.item.clone()
        };
        match kind.as_str() {
            k if k == SEARCH_AGENTS && !hit.archived => self.open_agent_ref(hit.id, window, cx),
            k if k == SEARCH_CHATS
                && !hit.agent.is_empty()
                && !hit.archived
                && hit.agent != "lead"
                && !hit.agent.starts_with("lead.") =>
            {
                self.open_agent_ref(format!("{project}/{}", hit.agent), window, cx)
            }
            k if k == SEARCH_SESSIONS || (k == SEARCH_CHATS && !hit.session.is_empty()) => {
                let id = if hit.session.is_empty() {
                    hit.id
                } else {
                    hit.session
                };
                self.select_project(project.clone(), window, cx);
                self.select_session(project, id, window, cx);
            }
            k if k == SEARCH_PULLS && !project.is_empty() => {
                self.select_project(project, window, cx);
                if let Ok(n) = hit.id.trim_start_matches('#').parse::<i64>() {
                    self.open_pull(n, window, cx);
                }
            }
            _ if !project.is_empty() => self.select_project(project, window, cx),
            _ if !hit.url.is_empty() => cx.open_url(&hit.url),
            _ => self.select_project(HOME_PROJECT.into(), window, cx),
        }
        if message && let Some(chat) = self.chat.clone() {
            chat.update(cx, |c, cx| match find {
                Some(q) => c.find_text(q, window, cx),
                None => c.reveal(item, cx),
            });
        }
        cx.notify();
    }

    // ——— The preview ———

    /// sync_preview reads what the result you're on needs to be shown: a
    /// chat message's neighbours, or a screenshot.
    fn sync_preview(&mut self, cx: &mut Context<Self>) {
        let rows = self.rows(cx);
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        let Some(Row::Hit(kind, hit)) = rows.get(p.selected).filter(|_| p.picking.is_none()) else {
            return;
        };
        let item = if hit.item.is_empty() {
            hit.id.clone()
        } else {
            hit.item.clone()
        };
        let key = format!("{kind}:{}:{}:{item}", hit.project, hit.agent);
        if p.preview.as_ref().is_some_and(|(k, _)| *k == key) {
            return;
        }
        let client = self.client.clone();
        if a_message(kind, hit) {
            let path = format!(
                "{}?around={}&limit={AROUND}",
                chat_path(hit),
                crate::files::encode(&item)
            );
            p.preview = None;
            p._preview = Some(cx.spawn(async move |this, cx| {
                let res = client.get::<ChatThread>(&path).await;
                this.update(cx, |this, cx| {
                    if let Some(p) = this.side.palette.as_mut() {
                        p.preview = Some((
                            key,
                            match res {
                                Ok(t) => Preview::Chat(t.items),
                                Err(e) => Preview::Failed(e.to_string()),
                            },
                        ));
                    }
                    cx.notify();
                })
                .ok();
            }));
        } else if let Some(m) = hit.media.clone().filter(|m| m.kind == "screenshot") {
            p.preview = None;
            p._preview = Some(cx.spawn(async move |this, cx| {
                let res = crate::media::fetch(client, m).await;
                this.update(cx, |this, cx| {
                    if let Some(p) = this.side.palette.as_mut() {
                        p.preview = Some((
                            key,
                            match res {
                                Ok(path) => Preview::Image(path),
                                Err(e) => Preview::Failed(e.to_string()),
                            },
                        ));
                    }
                    cx.notify();
                })
                .ok();
            }));
        }
    }

    pub(super) fn on_search_key(
        &mut self,
        ev: &KeyDownEvent,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(p) = self.side.palette.as_ref() else {
            return;
        };
        let picking = p.picking.clone();
        let empty = p.input.read(cx).value().is_empty();
        let n = match &picking {
            Some(c) => self
                .choices(
                    c.needs.unwrap_or(Needs::Project),
                    p.input.read(cx).value().as_ref(),
                )
                .len(),
            None => self.rows(cx).len(),
        };
        let key = ev.keystroke.key.as_str();
        match key {
            "escape" if picking.is_some() => self.pick_back(window, cx),
            "escape" => self.close_search(window, cx),
            "backspace" if picking.is_some() && empty => self.pick_back(window, cx),
            "down" | "up" if n > 0 => {
                let p = self.side.palette.as_mut().expect("palette");
                p.selected = if key == "down" {
                    (p.selected + 1) % n
                } else {
                    (p.selected + n - 1) % n
                };
                self.sync_preview(cx);
            }
            "tab" if picking.is_none() => {
                let chips = self.chips(cx);
                if chips.len() < 2 {
                    return;
                }
                let p = self.side.palette.as_ref().expect("palette");
                let at = chips.iter().position(|(k, _, _)| *k == p.kind).unwrap_or(0);
                let len = chips.len();
                let next = if ev.keystroke.modifiers.shift {
                    (at + len - 1) % len
                } else {
                    (at + 1) % len
                };
                let kind = chips[next].0.clone();
                self.set_kind(kind, cx);
            }
            _ => return,
        }
        cx.stop_propagation();
        cx.notify();
    }

    fn set_kind(&mut self, kind: String, cx: &mut Context<Self>) {
        let Some(p) = self.side.palette.as_mut() else {
            return;
        };
        p.kind = kind;
        p.selected = 0;
        p.one = None;
        self.run_search(cx);
    }

    /// chips are the list's narrowings: All, the commands that answer,
    /// and every kind something was found of, each with how many.
    fn chips(&self, cx: &App) -> Vec<(String, String, String)> {
        let Some(p) = self.side.palette.as_ref() else {
            return Vec::new();
        };
        let query = p.input.read(cx).value().to_string();
        let typed = !commands_only(&query).1.is_empty();
        let ranked = rank(self.commands(cx), &query);
        let actions = ranked.iter().filter(|c| !c.setting).count();
        let settings = if typed {
            ranked.iter().filter(|c| c.setting).count()
        } else {
            0
        };
        let groups: Vec<_> = if p.asked.is_empty() {
            Vec::new()
        } else {
            p.all
                .as_ref()
                .map(|r| {
                    r.groups
                        .iter()
                        .filter(|g| g.total > 0 || !g.hits.is_empty() || g.kind == p.kind)
                        .cloned()
                        .collect()
                })
                .unwrap_or_default()
        };
        let count = |total: i64, capped: bool| format!("{total}{}", if capped { "+" } else { "" });
        let total: i64 = groups.iter().map(|g| g.total).sum::<i64>()
            + if typed { ranked.len() as i64 } else { 0 };
        let capped = groups.iter().any(|g| g.capped);
        let mut out = vec![(
            String::new(),
            t("search.all"),
            if !groups.is_empty() || typed {
                count(total, capped)
            } else {
                String::new()
            },
        )];
        if actions > 0 || p.kind == ACTIONS {
            out.push((
                ACTIONS.into(),
                t("palette.group.actions"),
                actions.to_string(),
            ));
        }
        if settings > 0 || p.kind == SETTINGS {
            out.push((
                SETTINGS.into(),
                t("palette.group.settings"),
                settings.to_string(),
            ));
        }
        for g in &groups {
            out.push((
                g.kind.clone(),
                kind_label(&g.kind),
                count(g.total, g.capped),
            ));
        }
        if !p.kind.is_empty()
            && !p.kind.starts_with('@')
            && !groups.iter().any(|g| g.kind == p.kind)
        {
            out.push((p.kind.clone(), kind_label(&p.kind), "0".into()));
        }
        out
    }

    // ——— Drawing it ———

    fn chip(
        &self,
        id: String,
        on: bool,
        label: String,
        count: String,
        cx: &mut Context<Self>,
        click: impl Fn(&mut Self, &mut Context<Self>) + 'static,
    ) -> AnyElement {
        h_flex()
            .id(SharedString::from(format!("chip-{id}")))
            .flex_none()
            .gap_1p5()
            .px_2p5()
            .py_0p5()
            .rounded_full()
            .border_1()
            .text_xs()
            .cursor_pointer()
            .when(on, |el| {
                el.border_color(theme::accent().opacity(0.4))
                    .bg(theme::accent().opacity(0.15))
                    .text_color(theme::accent())
            })
            .when(!on, |el| {
                el.border_color(theme::line())
                    .text_color(theme::muted())
                    .hover(|s| s.text_color(theme::primary()))
            })
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                click(this, cx);
                cx.notify();
            }))
            .child(label)
            .when(!count.is_empty(), |el| {
                el.child(div().opacity(0.7).child(count))
            })
            .into_any_element()
    }

    fn filters_bar(&self, cx: &mut Context<Self>) -> AnyElement {
        let p = self.side.palette.as_ref().expect("palette");
        let view = cx.entity().downgrade();
        let mut chips: Vec<AnyElement> = Vec::new();
        for (k, label, count) in self.chips(cx) {
            let on = p.kind == k;
            let id = if k.is_empty() {
                "all".to_string()
            } else {
                k.clone()
            };
            chips.push(self.chip(id, on, label, count, cx, move |this, cx| {
                let next = if on && !k.is_empty() {
                    String::new()
                } else {
                    k.clone()
                };
                this.set_kind(next, cx);
            }));
        }
        let archived = p.archived;
        let past = self.chip(
            "archived".into(),
            archived,
            t("search.includePast"),
            String::new(),
            cx,
            |this, cx| {
                if let Some(p) = this.side.palette.as_mut() {
                    p.archived = !p.archived;
                }
                this.run_search(cx);
            },
        );
        let mut projects = vec![Choice::new("", t("search.anyProject"))];
        projects.extend(
            self.projects
                .iter()
                .map(|p| Choice::new(p.name.clone(), super::sidebar_actions::label_of(p))),
        );
        projects.push(Choice::new(HOME_PROJECT, t("shell.nav.home")));
        let v1 = view.clone();
        let project = select_at(
            "search-project",
            projects,
            &p.project,
            false,
            std::rc::Rc::new(move |v: &str, _, cx| {
                let v = v.to_string();
                v1.update(cx, |this, cx| {
                    if let Some(p) = this.side.palette.as_mut() {
                        p.project = v;
                    }
                    this.run_search(cx);
                    cx.notify();
                })
                .ok();
            }),
            gpui::Anchor::TopRight,
        );
        let since = select_at(
            "search-since",
            vec![
                Choice::new("", t("search.since.any")),
                Choice::new("day", t("search.since.day")),
                Choice::new("week", t("search.since.week")),
                Choice::new("month", t("search.since.month")),
            ],
            &p.since,
            false,
            std::rc::Rc::new(move |v: &str, _, cx| {
                let v = v.to_string();
                view.update(cx, |this, cx| {
                    if let Some(p) = this.side.palette.as_mut() {
                        p.since = v;
                    }
                    this.run_search(cx);
                    cx.notify();
                })
                .ok();
            }),
            gpui::Anchor::TopRight,
        );
        h_flex()
            .flex_wrap()
            .gap_1p5()
            .px_3()
            .py_2()
            .border_b_1()
            .border_color(theme::line())
            .children(chips)
            .child(div().flex_1())
            .child(past)
            .child(div().w(px(150.)).child(project))
            .child(div().w(px(150.)).child(since))
            .into_any_element()
    }

    fn command_row(
        &self,
        ix: usize,
        on: bool,
        c: &Cmd,
        query: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let whom = self.for_whom(c);
        h_flex()
            .id(SharedString::from(format!("cmd-{ix}")))
            .mx_2()
            .px_3()
            .py_1p5()
            .gap_3()
            .rounded_md()
            .cursor_pointer()
            .when(on, |el| el.bg(theme::surface_strong()))
            .hover(|el| el.bg(theme::surface_raised()))
            .on_click(cx.listener(move |this, e: &ClickEvent, window, cx| {
                if e.click_count() > 1 {
                    return this.palette_go(Some(ix), false, Some(window), cx);
                }
                if let Some(p) = this.side.palette.as_mut() {
                    p.selected = ix;
                }
                this.sync_preview(cx);
                cx.notify();
            }))
            .child(
                div()
                    .flex_none()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(crate::icons::of(if c.setting { "⚙" } else { "⚡" })),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .child(
                        div()
                            .text_sm()
                            .truncate()
                            .text_color(theme::primary())
                            .child(marked(&c.title, query)),
                    )
                    .when_some(whom.filter(|_| on), |el, w| {
                        el.child(
                            div()
                                .text_xs()
                                .truncate()
                                .text_color(theme::faint())
                                .child(w),
                        )
                    }),
            )
            .when(!c.place.is_empty(), |el| {
                el.child(
                    div()
                        .flex_none()
                        .max_w(px(220.))
                        .truncate()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(c.place.clone()),
                )
            })
            .when(!c.keys.is_empty(), |el| {
                el.child(
                    div()
                        .flex_none()
                        .px_1()
                        .rounded_sm()
                        .border_1()
                        .border_color(theme::line())
                        .text_xs()
                        .text_color(theme::faint())
                        .child(c.keys),
                )
            })
            .into_any_element()
    }

    fn hit_row(
        &self,
        ix: usize,
        on: bool,
        kind: &str,
        h: &SearchHit,
        asked: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let place = self.where_of(kind, h);
        h_flex()
            .id(SharedString::from(format!("hit-{ix}")))
            .mx_2()
            .px_3()
            .py_1p5()
            .gap_3()
            .items_start()
            .rounded_md()
            .cursor_pointer()
            .when(on, |el| el.bg(theme::surface_strong()))
            .hover(|el| el.bg(theme::surface_raised()))
            .on_click(cx.listener(move |this, e: &ClickEvent, window, cx| {
                if e.click_count() > 1 {
                    return this.palette_go(Some(ix), false, Some(window), cx);
                }
                if let Some(p) = this.side.palette.as_mut() {
                    p.selected = ix;
                }
                this.sync_preview(cx);
                cx.notify();
            }))
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .child(
                        h_flex()
                            .gap_2()
                            .min_w_0()
                            .child(
                                div()
                                    .min_w_0()
                                    .text_sm()
                                    .truncate()
                                    .text_color(theme::primary())
                                    .when(kind == SEARCH_EVENTS, |el| el.font_family(theme::mono()))
                                    .child(marked(&h.title.replace('\n', " "), asked)),
                            )
                            .when(!h.tag.is_empty(), |el| {
                                el.child(
                                    div()
                                        .flex_none()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .font_family(theme::mono())
                                        .child(h.tag.clone()),
                                )
                            }),
                    )
                    .when(!h.detail.is_empty(), |el| {
                        el.child(
                            div()
                                .text_xs()
                                .line_clamp(2)
                                .text_color(theme::muted())
                                .child(marked(&h.detail.replace('\n', " "), asked)),
                        )
                    }),
            )
            .child(
                v_flex()
                    .flex_none()
                    .max_w(px(220.))
                    .items_end()
                    .text_xs()
                    .text_color(theme::faint())
                    .when(!place.is_empty(), |el| {
                        el.child(div().truncate().child(place))
                    })
                    .when_some(h.at.as_ref(), |el, at| el.child(super::sidebar::ago(at))),
            )
            .into_any_element()
    }

    fn command_preview(&self, c: &Cmd, cx: &mut Context<Self>) -> AnyElement {
        let about = if c.setting {
            t("palette.about.setting")
        } else {
            t(&format!("palette.{}.about", c.id))
        };
        let whom = self.for_whom(c);
        let cmd = c.clone();
        let other = c.needs.map(|n| {
            t(if n == Needs::Agent {
                "palette.keys.otherAgent"
            } else {
                "palette.keys.otherProject"
            })
        });
        let cmd2 = c.clone();
        aside(
            if c.setting {
                c.place.clone()
            } else {
                t("palette.group.actions")
            },
            String::new(),
            v_flex()
                .gap_2()
                .child(
                    div()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::primary())
                        .child(c.title.clone()),
                )
                .child(div().text_color(theme::muted()).child(about))
                .when(c.setting && !c.keywords.trim().is_empty(), |el| {
                    el.child(
                        div()
                            .text_xs()
                            .line_clamp(4)
                            .text_color(theme::faint())
                            .child(c.keywords.clone()),
                    )
                })
                .when_some(whom, |el, w| {
                    el.child(div().text_xs().text_color(theme::faint()).child(w))
                })
                .into_any_element(),
            h_flex()
                .gap_2()
                .child(primary(
                    "palette-run",
                    t(if c.setting {
                        "palette.openSetting"
                    } else {
                        "palette.run"
                    }),
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.run_command(cmd.clone(), false, window, cx)
                    }),
                ))
                .when_some(other, |el, o| {
                    el.child(secondary(
                        "palette-other",
                        format!("{o} ⇧↵"),
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.run_command(cmd2.clone(), true, window, cx)
                        }),
                    ))
                })
                .into_any_element(),
        )
    }

    fn hit_preview(
        &self,
        kind: &str,
        h: &SearchHit,
        asked: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let p = self.side.palette.as_ref().expect("palette");
        let place = self.where_of(kind, h);
        let item = if h.item.is_empty() {
            h.id.clone()
        } else {
            h.item.clone()
        };
        let key = format!("{kind}:{}:{}:{item}", h.project, h.agent);
        let preview = p
            .preview
            .as_ref()
            .filter(|(k, _)| *k == key)
            .map(|(_, v)| v);
        let body: AnyElement =
            if a_message(kind, h) {
                match preview {
                    None => div()
                        .text_color(theme::faint())
                        .child("…")
                        .into_any_element(),
                    Some(Preview::Failed(e)) => div()
                        .text_color(theme::error())
                        .child(e.clone())
                        .into_any_element(),
                    Some(Preview::Chat(items)) => {
                        let ai = self.agent_title(
                            &h.project,
                            if h.agent.is_empty() { "lead" } else { &h.agent },
                        );

                        v_flex()
                            .gap_3()
                            .children(
                                items
                                    .iter()
                                    .filter(|it| {
                                        ["user", "aside", "assistant", "notice", "error"]
                                            .contains(&it.kind.as_str())
                                            && !it.text.is_empty()
                                            && !it.hidden
                                    })
                                    .map(|it| {
                                        let mine = it.kind == "user" || it.kind == "aside";
                                        let here = it.id == item;
                                        let who = if mine {
                                            t("chat.find.from.you")
                                        } else if it.kind == "assistant" {
                                            ai.clone()
                                        } else {
                                            t("chat.find.notice")
                                        };
                                        v_flex()
                                            .gap_1()
                                            .when(mine, |el| el.items_end())
                                            .child(
                                                div()
                                                    .px_1()
                                                    .text_xs()
                                                    .text_color(theme::faint())
                                                    .child(who),
                                            )
                                            .child(
                                                div()
                                                    .max_w(relative(0.92))
                                                    .px_3()
                                                    .py_2()
                                                    .rounded_xl()
                                                    .when(mine, |el| el.bg(theme::surface_raised()))
                                                    .when(here, |el| {
                                                        el.border_1()
                                                            .border_color(theme::amber(400))
                                                    })
                                                    .when(!here, |el| {
                                                        el.line_clamp(4).text_color(theme::muted())
                                                    })
                                                    .child(if here {
                                                        marked(&it.text, asked)
                                                    } else {
                                                        it.text.clone().into_any_element()
                                                    }),
                                            )
                                    })
                                    .collect::<Vec<_>>(),
                            )
                            .into_any_element()
                    }
                    Some(Preview::Image(_)) => div().into_any_element(),
                }
            } else if let Some(r) = h.report.as_ref().filter(|_| kind == SEARCH_REPORTS) {
                let lists = [
                    ("search.report.discoveries", &r.discoveries),
                    ("search.report.decisions", &r.decisions),
                    ("search.report.remaining", &r.remaining_issues),
                ];
                v_flex()
                    .gap_3()
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                div()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(marked(
                                        if r.task.is_empty() {
                                            &r.summary
                                        } else {
                                            &r.task
                                        },
                                        asked,
                                    )),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .font_family(theme::mono())
                                    .text_color(theme::faint())
                                    .child(r.status.clone()),
                            ),
                    )
                    .child(
                        div()
                            .text_color(theme::primary())
                            .child(marked(&r.summary, asked)),
                    )
                    .children(lists.into_iter().filter(|(_, l)| !l.is_empty()).map(
                        |(label, lines)| {
                            v_flex()
                                .gap_1()
                                .child(
                                    div()
                                        .text_xs()
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::faint())
                                        .child(t(label)),
                                )
                                .children(lines.iter().map(|l| {
                                    h_flex()
                                        .gap_1()
                                        .items_start()
                                        .text_color(theme::muted())
                                        .child("•")
                                        .child(marked(l, asked))
                                }))
                        },
                    ))
                    .into_any_element()
            } else if let Some(m) = h.memory.as_ref().filter(|_| kind == SEARCH_MEMORIES) {
                v_flex()
                    .gap_2()
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                div()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(marked(&m.title, asked)),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .font_family(theme::mono())
                                    .text_color(theme::faint())
                                    .child(m.kind.clone()),
                            ),
                    )
                    .child(
                        div()
                            .text_color(theme::primary())
                            .child(marked(&m.content, asked)),
                    )
                    .into_any_element()
            } else if let Some(m) = h.media.as_ref().filter(|_| kind == SEARCH_MEDIA) {
                v_flex()
                    .gap_3()
                    .when_some(
                        match preview {
                            Some(Preview::Image(p)) => Some(p.clone()),
                            _ => None,
                        },
                        |el, path| {
                            el.child(
                                img(path)
                                    .max_h(px(288.))
                                    .w_full()
                                    .object_fit(ObjectFit::Contain)
                                    .rounded_lg(),
                            )
                        },
                    )
                    .child(
                        div()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(marked(&m.name, asked)),
                    )
                    .when(!m.text.is_empty(), |el| {
                        el.child(
                            div()
                                .text_color(theme::muted())
                                .child(marked(&m.text, asked)),
                        )
                    })
                    .into_any_element()
            } else {
                v_flex()
                    .gap_2()
                    .child(
                        div()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .when(kind == SEARCH_EVENTS, |el| {
                                el.font_family(theme::mono()).text_xs()
                            })
                            .child(marked(&h.title, asked)),
                    )
                    .when(!h.detail.is_empty(), |el| {
                        el.child(
                            div()
                                .text_color(theme::muted())
                                .child(marked(&h.detail, asked)),
                        )
                    })
                    .into_any_element()
            };
        let message = a_message(kind, h);
        let open = t(if kind == SEARCH_CHATS || kind == SEARCH_SESSIONS {
            "search.openInChat"
        } else {
            "search.open.item"
        });
        let (k1, h1, k2, h2) = (kind.to_string(), h.clone(), kind.to_string(), h.clone());
        let asked2 = asked.to_string();
        aside(
            if place.is_empty() {
                kind_label(kind)
            } else {
                place
            },
            h.at.as_deref()
                .map(crate::dates::date_time_of)
                .unwrap_or_default(),
            body,
            h_flex()
                .gap_2()
                .child(primary(
                    "search-open",
                    format!("{open} ↵"),
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.close_search(window, cx);
                        this.open_hit(k1.clone(), h1.clone(), None, window, cx)
                    }),
                ))
                .when(message, |el| {
                    el.child(secondary(
                        "search-find",
                        format!("{} ⇧↵", t("search.findInChat")),
                        cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.close_search(window, cx);
                            this.open_hit(k2.clone(), h2.clone(), Some(asked2.clone()), window, cx)
                        }),
                    ))
                })
                .into_any_element(),
        )
    }

    pub(super) fn render_search(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let p = self.side.palette.as_ref()?;
        let query = p.input.read(cx).value().to_string();
        let (only, rest) = commands_only(&query);
        let typed = !rest.is_empty();
        let mut list: Vec<AnyElement> = Vec::new();
        let mut side: Option<AnyElement> = None;
        let mut current_cmd: Option<Cmd> = None;
        let mut current_message = false;
        if let Some(c) = &p.picking {
            let choices = self.choices(c.needs.unwrap_or(Needs::Project), &query);
            let current = match c.needs {
                Some(Needs::Agent) => self.current_agent(),
                _ => self.current_project(),
            };
            if choices.is_empty() {
                list.push(note(t(if c.needs == Some(Needs::Agent) {
                    "palette.pick.noAgent"
                } else {
                    "palette.pick.noProject"
                })));
            }
            for (i, (id, title, detail)) in choices.into_iter().enumerate() {
                let on = i == p.selected;
                let detail = if Some(&id) == current.as_ref() {
                    t("palette.pick.current")
                } else {
                    detail
                };
                list.push(
                    h_flex()
                        .id(SharedString::from(format!("choice-{i}")))
                        .mx_2()
                        .px_3()
                        .py_1p5()
                        .gap_3()
                        .rounded_md()
                        .cursor_pointer()
                        .when(on, |el| el.bg(theme::surface_strong()))
                        .hover(|el| el.bg(theme::surface_raised()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                            this.pick(Some(i), window, cx)
                        }))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .truncate()
                                .text_sm()
                                .text_color(theme::primary())
                                .child(marked(&title, &query)),
                        )
                        .child(
                            div()
                                .flex_none()
                                .max_w(px(240.))
                                .truncate()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(detail),
                        )
                        .into_any_element(),
                );
            }
        } else {
            let rows = self.rows(cx);
            let pending = !only && query.trim() != p.asked;
            if p.asked.is_empty() && !typed && rows.is_empty() {
                list.push(note(t("search.hint")));
            } else if let (Some(e), true) = (&p.error, rows.is_empty()) {
                list.push(
                    div()
                        .px_4()
                        .py_6()
                        .text_sm()
                        .text_color(theme::error())
                        .child(e.clone())
                        .into_any_element(),
                );
            } else if rows.is_empty()
                && (only || p.kind.starts_with('@') || (!pending && p.all.is_some()))
            {
                list.push(note(tv(
                    "search.nothing",
                    &[(
                        "query",
                        if rest.is_empty() {
                            query.clone()
                        } else {
                            rest.clone()
                        }
                        .into(),
                    )],
                )));
            }
            if !typed && p.kind.is_empty() && !rows.is_empty() {
                list.push(
                    div()
                        .px_4()
                        .pt_1()
                        .pb_2()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(t("palette.hint"))
                        .into_any_element(),
                );
            }
            let group_of = |r: &Row| match r {
                Row::Cmd(c) if c.setting => 1,
                Row::Cmd(_) => 0,
                Row::Hit(..) => 2,
            };
            let mut last = -1;
            for (i, row) in rows.iter().enumerate() {
                let g = group_of(row);
                let on = i == p.selected;
                if g != last {
                    last = g;
                    if g == 2 {
                        let shown = rows.iter().filter(|r| matches!(r, Row::Hit(..))).count();
                        let label = if p.kind.is_empty() {
                            t("search.bestOfEach")
                        } else {
                            let total = self
                                .chips(cx)
                                .into_iter()
                                .find(|(k, _, _)| *k == p.kind)
                                .map(|(_, _, n)| n)
                                .unwrap_or_default();
                            tv(
                                "search.showing",
                                &[("n", shown.into()), ("total", total.into())],
                            )
                        };
                        let newest = p.newest;
                        list.push(
                            h_flex()
                                .px_4()
                                .pt_3()
                                .pb_1()
                                .justify_between()
                                .text_xs()
                                .text_color(theme::faint())
                                .child(label)
                                .child(
                                    div()
                                        .id("search-sort")
                                        .cursor_pointer()
                                        .hover(|s| s.text_color(theme::primary()))
                                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                            if let Some(p) = this.side.palette.as_mut() {
                                                p.newest = !p.newest;
                                                p.selected = 0;
                                            }
                                            this.sync_preview(cx);
                                            cx.notify();
                                        }))
                                        .child(format!(
                                            "⇅ {}",
                                            t(if newest {
                                                "search.sort.newest"
                                            } else {
                                                "search.sort.best"
                                            })
                                        )),
                                )
                                .into_any_element(),
                        );
                    } else {
                        list.push(
                            div()
                                .px_4()
                                .pt_3()
                                .pb_1()
                                .text_xs()
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(theme::faint())
                                .child(
                                    t(if g == 1 {
                                        "palette.group.settings"
                                    } else {
                                        "palette.group.actions"
                                    })
                                    ,
                                )
                                .into_any_element(),
                        );
                    }
                }
                match row {
                    Row::Cmd(c) => {
                        if on {
                            side = Some(self.command_preview(c, cx));
                            current_cmd = Some(c.clone());
                        }
                        list.push(self.command_row(i, on, c, &rest, cx));
                    }
                    Row::Hit(kind, h) => {
                        if on && !p.asked.is_empty() {
                            side = Some(self.hit_preview(kind, h, &p.asked, cx));
                            current_message = a_message(kind, h);
                        }
                        list.push(self.hit_row(i, on, kind, h, &p.asked, cx));
                    }
                }
            }
        }
        let picking = p.picking.as_ref();
        let picking_chip = picking.map(|c| {
            div()
                .id("palette-picking")
                .flex_none()
                .max_w(px(260.))
                .truncate()
                .px_2p5()
                .py_1()
                .rounded_full()
                .border_1()
                .border_color(theme::accent().opacity(0.4))
                .bg(theme::accent().opacity(0.15))
                .text_xs()
                .text_color(theme::accent())
                .cursor_pointer()
                .on_click(
                    cx.listener(|this, _: &ClickEvent, window, cx| this.pick_back(window, cx)),
                )
                .child(format!("{} ›", c.title))
        });
        let mut keys = vec![format!("↑↓ {}", t("search.keys.move"))];
        keys.push(format!(
            "↵ {}",
            t(
                if picking.is_some() || current_cmd.as_ref().is_some_and(|c| !c.setting) {
                    "palette.keys.run"
                } else {
                    "search.keys.open"
                }
            )
        ));
        if current_message && picking.is_none() {
            keys.push(format!("⇧↵ {}", t("search.keys.find")));
        }
        if let Some(n) = current_cmd
            .as_ref()
            .and_then(|c| c.needs)
            .filter(|_| picking.is_none())
        {
            keys.push(format!(
                "⇧↵ {}",
                t(if n == Needs::Agent {
                    "palette.keys.otherAgent"
                } else {
                    "palette.keys.otherProject"
                })
            ));
        }
        if picking.is_some() {
            keys.push(format!("Esc {}", t("palette.keys.back")));
        } else {
            keys.push(format!("⇥ {}", t("palette.keys.filter")));
            keys.push(format!("> {}", t("palette.keys.commandsOnly")));
        }
        Some(
            crate::motion::scrim_in(div()
                .id("search-palette")
                .absolute()
                .inset_0()
                .bg(theme::scrim())
                .flex()
                .justify_center()
                .pt(relative(0.08))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _: &MouseDownEvent, window, cx| {
                        this.close_search(window, cx)
                    }),
                )
                .capture_key_down(cx.listener(|this, ev: &KeyDownEvent, window, cx| {
                    this.on_search_key(ev, window, cx)
                }))
                .child(
                    v_flex()
                        .id("search-card")
                        .w(px(1152.))
                        .max_w(relative(0.95))
                        .h(relative(0.8))
                        .rounded_2xl()
                        .overflow_hidden()
                        .bg(theme::card())
                        .border_1()
                        .border_color(theme::line())
                        .shadow_lg()
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .child(
                            h_flex()
                                .gap_2()
                                .p_3()
                                .border_b_1()
                                .border_color(theme::line())
                                .children(picking_chip)
                                .child(div().flex_1().child(Input::new(&p.input).cleanable(true))),
                        )
                        .when(picking.is_none(), |el| el.child(self.filters_bar(cx)))
                        .child(
                            h_flex()
                                .flex_1()
                                .min_h_0()
                                .items_start()
                                .child(
                                    div()
                                        .id("search-results")
                                        .flex_1()
                                        .min_w_0()
                                        .h_full()
                                        .overflow_y_scroll()
                                        .py_2()
                                        .children(list),
                                )
                                .when_some(side, |el, s| el.child(s)),
                        )
                        .child(
                            h_flex()
                                .flex_wrap()
                                .px_4()
                                .py_2()
                                .gap_4()
                                .border_t_1()
                                .border_color(theme::line())
                                .text_xs()
                                .text_color(theme::faint())
                                .children(keys),
                        ),
                ), "idsearch-palette-in")
                .into_any_element(),
        )
    }
}

fn note(text: String) -> AnyElement {
    div()
        .px_4()
        .py_8()
        .text_center()
        .text_sm()
        .text_color(theme::muted())
        .child(text)
        .into_any_element()
}

/// aside is the preview beside the list: where, when, the body and its buttons.
fn aside(place: String, at: String, body: AnyElement, foot: AnyElement) -> AnyElement {
    v_flex()
        .w(relative(0.42))
        .flex_none()
        .h_full()
        .border_l_1()
        .border_color(theme::line())
        .bg(theme::ink())
        .child(
            h_flex()
                .justify_between()
                .gap_3()
                .px_4()
                .py_2p5()
                .border_b_1()
                .border_color(theme::line())
                .text_xs()
                .text_color(theme::faint())
                .child(div().min_w_0().truncate().child(place))
                .when(!at.is_empty(), |el| el.child(div().flex_none().child(at))),
        )
        .child(
            div()
                .id("search-preview")
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .px_4()
                .py_4()
                .text_sm()
                .child(body),
        )
        .child(
            h_flex()
                .px_4()
                .py_2p5()
                .border_t_1()
                .border_color(theme::line())
                .child(foot),
        )
        .into_any_element()
}

fn primary(
    id: &'static str,
    label: String,
    click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .px_2p5()
        .py_1()
        .rounded_lg()
        .bg(theme::brand(500))
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .text_color(theme::on_brand())
        .cursor_pointer()
        .on_click(click)
        .child(label)
        .into_any_element()
}

fn secondary(
    id: &'static str,
    label: String,
    click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    div()
        .id(id)
        .px_2p5()
        .py_1()
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .text_xs()
        .text_color(theme::muted())
        .cursor_pointer()
        .hover(|s| s.text_color(theme::primary()))
        .on_click(click)
        .child(label)
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::{Cmd, commands_only, looks_like_verb, rank};

    fn cmd(title: &str) -> Cmd {
        Cmd {
            id: title.into(),
            setting: false,
            title: title.into(),
            keywords: String::new(),
            place: String::new(),
            needs: None,
            keys: "",
            section: String::new(),
        }
    }

    #[test]
    fn ranks_like_the_palette() {
        let c = vec![
            cmd("Record agent screen with sound"),
            cmd("Record agent screen"),
            cmd("Start agent"),
        ];
        let r = rank(c, "rec");
        assert_eq!(r[0].title, "Record agent screen");
        assert_eq!(r.len(), 2);
        assert!(commands_only("> rec").0);
        let verbs = vec!["record".to_string(), "start".to_string()];
        assert!(looks_like_verb("rec", &verbs));
        assert!(!looks_like_verb("r", &verbs));
    }
}
