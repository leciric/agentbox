//! An agent's page in the main column (components/AgentView.tsx as v2 draws
//! it): its header (its state in a badge that opens its info card,
//! AgentInfoCard.tsx; its pull request or branch; the action that brings it
//! back; its ⋯), the machine bar (v2/AgentMachineBar.tsx) while the panel
//! doesn't show it, and over its chat its Snapshots (SnapshotsTab.tsx); the
//! turns it can roll back to or fork from are in its chat (chat/TurnMarker.tsx). Its
//! menu (AgentContextMenu.tsx) is the same on its row and its header; its
//! dialogs (DestroyAgentDialog.tsx, ForkDialog, the confirmations) go over the
//! window. Every action is the daemon route the Electron app calls.
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::{
    Agent, AgentDisk, Checkpoint, FleetAgent, ForkRequest, Job, RestoreRequest, RetireRequest,
    RetireResult, RollbackRequest, RollbackResult, Snapshot, SnapshotRequest, UpdateAgentRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::input::{Input, InputState};
use gpui_component::menu::{DropdownMenu as _, PopupMenu, PopupMenuItem};
use gpui_component::tooltip::Tooltip;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::agent_status::{self as st, Action, Tone};
use super::controls::{self, Btn};
use super::panel::PView;
use super::pulls;
use super::settings::defaults::time_ago;
use crate::theme;
use crate::toasts::{self, Kind, Toast};

/// A menu item's action on the app.
type MenuAction = Rc<dyn Fn(&mut AppView, &mut Window, &mut Context<AppView>)>;
/// One that needs no window.
type MenuNote = Rc<dyn Fn(&mut AppView, &mut Context<AppView>)>;
/// What closes a dialog.
pub type OnCancel = Rc<dyn Fn(&mut Window, &mut App)>;

/// What goes over the agent's chat in the main column.
#[derive(Clone, Copy, PartialEq)]
pub enum Page {
    Snapshots,
}

/// A confirmation waiting for its yes.
#[derive(Clone)]
pub enum Confirm {
    Rollback { checkpoint: String, n: i64 },
    Restore { snapshot: String },
    DeleteSnapshot { name: String },
}

/// The destroy dialog's choices (DestroyAgentDialog.tsx).
#[derive(Clone)]
pub struct Destroy {
    r#ref: String,
    title: String,
    branch: String,
    force: bool,
    delete_branch: bool,
    delete_media: bool,
}

/// The fork dialog: from what, and its job once started.
#[derive(Clone)]
pub struct Fork {
    r#ref: String,
    title: String,
    description: String,
    request: ForkRequest,
    agent_title: String,
    job: Option<Job>,
    failed: bool,
}

pub struct AgentPage {
    pub page: Option<Page>,
    snapshots: Option<Result<Vec<Snapshot>, String>>,
    checkpoints: Vec<Checkpoint>,
    restoring: Option<(String, Job)>,
    /// The info card, opened from the header's badge or the menu's Info.
    pub info: Option<String>,
    /// When a click outside last closed the card: the badge's own click,
    /// right after, mustn't open it again.
    info_closed: Option<std::time::Instant>,
    disk: Option<(String, AgentDisk)>,
    pub destroy: Option<Destroy>,
    pub confirm: Option<Confirm>,
    pub fork: Option<Fork>,
    /// Renaming: the agent's ref.
    pub rename: Option<String>,
    consistent: bool,
    busy: bool,
    snapshot_name: Entity<InputState>,
    fork_title: Entity<InputState>,
    fork_name: Entity<InputState>,
    title_input: Entity<InputState>,
}

impl AgentPage {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let input = |placeholder: String, window: &mut Window, cx: &mut Context<AppView>| {
            cx.new(|cx| InputState::new(window, cx).placeholder(placeholder))
        };
        Self {
            page: None,
            snapshots: None,
            checkpoints: Vec::new(),
            restoring: None,
            info: None,
            info_closed: None,
            disk: None,
            destroy: None,
            confirm: None,
            fork: None,
            rename: None,
            consistent: false,
            busy: false,
            snapshot_name: input(t("agent.snapshots.namePlaceholder"), window, cx),
            fork_title: input(String::new(), window, cx),
            fork_name: input("agent-NN".into(), window, cx),
            title_input: input(String::new(), window, cx),
        }
    }

    pub fn busy_now(&self) -> bool {
        self.busy
    }

    /// reset forgets what was read of the agent shown before.
    pub fn reset(&mut self) {
        self.page = None;
        self.snapshots = None;
        self.checkpoints.clear();
        self.restoring = None;
        self.info = None;
    }
}

/// split is a ref's project and name.
pub fn split(r#ref: &str) -> (String, String) {
    let (p, a) = r#ref.split_once('/').unwrap_or((r#ref, ""));
    (p.to_string(), a.to_string())
}

/// ai_label is an AI tool's name.
pub fn ai_label(ai: &str) -> String {
    match ai {
        "claude" => "Claude Code".into(),
        "codex" => "Codex".into(),
        "opencode" => "OpenCode".into(),
        "cursor" => "Cursor".into(),
        "gemini" => "Gemini CLI".into(),
        other => other.to_string(),
    }
}

pub fn tone_color(tone: Tone) -> Hsla {
    match tone {
        Tone::Urgent | Tone::Warning => theme::starting(),
        Tone::Error => theme::error(),
        Tone::Live => theme::accent(),
        Tone::Muted => theme::muted(),
    }
}

/// strip_tags drops a message's markup (<code>…</code>), shown as plain text.
fn strip_tags(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut tag = false;
    for c in s.chars() {
        match c {
            '<' => tag = true,
            '>' if tag => tag = false,
            _ if !tag => out.push(c),
            _ => {}
        }
    }
    out
}

fn short_commit(c: &str) -> String {
    c.chars().take(7).collect()
}

impl AppView {
    /// open_agent is the agent whose chat is open, from the fleet.
    pub(super) fn open_agent(&self) -> Option<&FleetAgent> {
        if self.open == "lead" {
            return None;
        }
        self.fleet.iter().find(|a| a.name == self.open)
    }

    pub(super) fn agent_by_ref(&self, r#ref: &str) -> Option<&FleetAgent> {
        self.fleet.iter().find(|a| a.r#ref == r#ref)
    }

    /// replace_agent takes an action's answer, the agent as it is now.
    fn replace_agent(&mut self, a: Agent) {
        if let Some(f) = self.fleet.iter_mut().find(|f| f.r#ref == a.r#ref) {
            f.state = a.state.clone();
            f.chat = a.chat.clone();
            f.title = a.title.clone();
        }
        self.side
            .apply_agent(&a.r#ref, Some(&a.state), Some(&a.chat));
    }

    /// run_action starts, stops, pauses or resumes an agent.
    pub(super) fn run_action(&mut self, r#ref: String, action: Action, cx: &mut Context<Self>) {
        let (p, a) = split(&r#ref);
        let client = self.client.clone();
        self.agent.busy = true;
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Agent>(
                    &format!("/v1/agents/{p}/{a}/{}", action.path()),
                    &serde_json::json!({}),
                )
                .await;
            this.update(cx, |this, cx| {
                this.agent.busy = false;
                match res {
                    Ok(agent) => this.replace_agent(agent),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// retire stops an agent and frees its machine (POST …/retire, how stop).
    fn retire_agent(&mut self, agent: &FleetAgent, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let (project, name) = (agent.project.clone(), agent.name.clone());
        let title = if agent.title.is_empty() {
            agent.name.clone()
        } else {
            agent.title.clone()
        };
        cx.spawn(async move |this, cx| {
            let req = RetireRequest {
                how: "stop".into(),
                agents: vec![name],
                ..Default::default()
            };
            let res = client
                .post::<_, RetireResult>(&format!("/v1/projects/{project}/retire"), &req)
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(r) if !r.retired.is_empty() => {
                        Toast::new(
                            Kind::Info,
                            tv("agent.menu.stopped", &[("name", (&title).into())]),
                        )
                        .description(t("agent.menu.stoppedNote"))
                        .show(cx);
                    }
                    Ok(r) => {
                        let mut toast = Toast::new(
                            Kind::Info,
                            tv("agent.menu.notFree", &[("name", (&title).into())]),
                        );
                        if let Some(s) = r.skipped.first() {
                            toast = toast.description(s.reason.clone());
                        }
                        toast.show(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_fleet_now(cx);
            })
            .ok();
        })
        .detach();
    }

    /// load_fleet_now reads the open project's fleet again.
    pub(super) fn load_fleet_now(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if project == agentbox_api::HOME_PROJECT {
            return;
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(f) = client
                .get::<agentbox_api::Fleet>(&format!("/v1/projects/{project}/fleet"))
                .await
            {
                this.update(cx, |this, cx| {
                    if this.project.as_deref() == Some(project.as_str()) {
                        this.fleet = f.agents;
                        cx.notify();
                    }
                })
                .ok();
            }
        })
        .detach();
    }

    /// open_agent_view opens an agent's chat with its panel on a view.
    pub(super) fn open_agent_view(
        &mut self,
        r#ref: &str,
        view: PView,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let (p, a) = split(r#ref);
        if self.project.as_deref() != Some(p.as_str()) {
            self.select_project(p, window, cx);
        }
        if self.open != a {
            self.open_chat(a, window, cx);
        }
        self.set_view(r#ref, view, window, cx);
    }

    /// agent_menu is an agent's menu, on its row (a right-click) and its
    /// header's ⋯ (header: the header's own items too).
    pub(super) fn agent_menu(
        &self,
        agent: &FleetAgent,
        header: bool,
        cx: &mut Context<Self>,
    ) -> impl Fn(PopupMenu, &mut Window, &mut Context<PopupMenu>) -> PopupMenu + 'static {
        let view = cx.entity().downgrade();
        let agent = agent.clone();
        move |menu, window, cx| {
            let a = agent.clone();
            let r = a.r#ref.clone();
            let actions = st::lifecycle_actions(&a.state);
            let item = |label: String, f: MenuAction| {
                let view = view.clone();
                PopupMenuItem::new(label).on_click(move |_, window, cx| {
                    let f = f.clone();
                    view.update(cx, |this, cx| f(this, window, cx)).ok();
                })
            };
            let mut menu = menu.min_w(px(220.));
            if !header {
                let r1 = r.clone();
                menu = menu.item(item(
                    t("agent.menu.openTerminal"),
                    Rc::new(move |this, w, cx| this.open_agent_view(&r1, PView::Terminal, w, cx)),
                ));
                let r2 = r.clone();
                menu = menu.item(
                    item(
                        if a.state == "running" {
                            t("agent.menu.openDesktop")
                        } else {
                            format!(
                                "{} · {}",
                                t("agent.menu.openDesktop"),
                                t("agent.menu.openDesktopOff")
                            )
                        },
                        Rc::new(move |this, w, cx| {
                            this.open_agent_view(&r2, PView::Desktop, w, cx)
                        }),
                    )
                    .disabled(a.state != "running"),
                );
            }
            if let Some(pr) = a.pr.clone().filter(|p| p.number > 0) {
                let url = pr.url.clone();
                menu = menu.item(
                    PopupMenuItem::new(tv("agent.menu.openPr", &[("number", pr.number.into())]))
                        .on_click(move |_, _, cx| cx.open_url(&url)),
                );
            }
            menu = menu.separator();
            for action in actions.iter().copied().filter(|x| *x != Action::Stop) {
                if header && action.primary() {
                    continue;
                }
                let r = r.clone();
                menu = menu.item(item(
                    action.label(),
                    Rc::new(move |this, _, cx| this.run_action(r.clone(), action, cx)),
                ));
            }
            let retiring = a.clone();
            menu = menu.item(item(
                t("agent.menu.retire"),
                Rc::new(move |this, _, cx| this.retire_agent(&retiring, cx)),
            ));
            // What the daemon says retiring would leave behind, when it isn't safe.
            if !a.retire.safe && !a.retire.reason.is_empty() {
                menu = menu.item(PopupMenuItem::new(a.retire.reason.clone()).disabled(true));
            }
            if header {
                let r = r.clone();
                menu = menu.item(item(
                    t("agent.view.editTitle"),
                    Rc::new(move |this, w, cx| this.start_agent_rename(r.clone(), w, cx)),
                ));
                let wt = a.worktree.clone();
                menu = menu.item(
                    PopupMenuItem::new(t("agent.view.openWorktree"))
                        .on_click(move |_, _, cx| cx.open_with_system(std::path::Path::new(&wt))),
                );
                menu = menu.item(item(
                    t("agent.tab.snapshots"),
                    Rc::new(move |this, _, cx| this.open_snapshots(cx)),
                ));
            }
            let (stop_ref, info_ref, branch) = (r.clone(), r.clone(), a.branch.clone());
            let (ip, worktree, shell) = (
                a.ip.clone(),
                a.worktree.clone(),
                format!("agentbox shell {r}"),
            );
            let stops = actions.contains(&Action::Stop);
            let v = view.clone();
            menu = menu.submenu(t("common.more"), window, cx, move |m, _, _| {
                let mut m = m;
                let item = |label: String, f: MenuNote| {
                    let v = v.clone();
                    PopupMenuItem::new(label).on_click(move |_, _, cx| {
                        let f = f.clone();
                        v.update(cx, |this, cx| f(this, cx)).ok();
                    })
                };
                if stops {
                    let r = stop_ref.clone();
                    m = m.item(item(
                        t("common.stop"),
                        Rc::new(move |this, cx| this.run_action(r.clone(), Action::Stop, cx)),
                    ));
                }
                let r = info_ref.clone();
                m = m.item(item(
                    t("agent.menu.info"),
                    Rc::new(move |this, cx| this.open_info(r.clone(), cx)),
                ));
                let copy = |label: String, text: String, done: String| {
                    PopupMenuItem::new(label).on_click(move |_, _, cx| {
                        cx.write_to_clipboard(ClipboardItem::new_string(text.clone()));
                        toasts::info(cx, done.clone());
                    })
                };
                m = m.item(copy(
                    t("agent.menu.copyBranch"),
                    branch.clone(),
                    t("agent.menu.copiedBranch"),
                ));
                if header {
                    if !ip.is_empty() {
                        m = m.item(copy(
                            t("agent.view.copyIp"),
                            ip.clone(),
                            t("agent.view.copiedIp"),
                        ));
                    }
                    m = m.item(copy(
                        t("agent.view.copyWorktree"),
                        worktree.clone(),
                        t("agent.view.copiedWorktree"),
                    ));
                    m = m.item(copy(
                        t("agent.view.copyShell"),
                        shell.clone(),
                        t("common.copied"),
                    ));
                }
                m
            });
            let d = a.clone();
            menu.separator().item(item(
                t("agent.view.destroy"),
                Rc::new(move |this, _, cx| this.start_destroy(&d, cx)),
            ))
        }
    }

    fn start_destroy(&mut self, a: &FleetAgent, cx: &mut Context<Self>) {
        // A broken or gone machine has nothing uncommitted left to keep.
        let force = matches!(a.state.as_str(), "incomplete" | "initializing" | "missing");
        self.agent.destroy = Some(Destroy {
            r#ref: a.r#ref.clone(),
            title: if a.title.is_empty() {
                a.r#ref.clone()
            } else {
                a.title.clone()
            },
            branch: a.branch.clone(),
            force,
            delete_branch: false,
            delete_media: false,
        });
        cx.notify();
    }

    fn destroy(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(d) = self.agent.destroy.take() else {
            return;
        };
        let (p, a) = split(&d.r#ref);
        // Destroying the agent whose chat is open leaves it for the lead's.
        if self.open == a {
            self.open_chat("lead".into(), window, cx);
        }
        let client = self.client.clone();
        let name = d.title.clone();
        cx.spawn(async move |this, cx| {
            let path = format!(
                "/v1/agents/{p}/{a}?force={}&deleteBranch={}&deleteMedia={}",
                d.force, d.delete_branch, d.delete_media
            );
            let res = client
                .raw(agentbox_api::client::Method::DELETE, &path, None)
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(_) => Toast::new(
                        Kind::Success,
                        tv("agent.destroy.done", &[("name", (&name).into())]),
                    )
                    .show(cx),
                    Err(e) => Toast::new(
                        Kind::Error,
                        tv("agent.destroy.failed", &[("name", (&name).into())]),
                    )
                    .description(e.to_string())
                    .show(cx),
                }
                this.load_fleet_now(cx);
                this.load_sidebar(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn open_info(&mut self, r#ref: String, cx: &mut Context<Self>) {
        self.agent.info = Some(r#ref.clone());
        let (p, a) = split(&r#ref);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            if let Ok(d) = client
                .get::<AgentDisk>(&format!("/v1/agents/{p}/{a}/disk"))
                .await
            {
                this.update(cx, |this, cx| {
                    this.agent.disk = Some((r#ref, d));
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
        cx.notify();
    }

    fn start_agent_rename(&mut self, r#ref: String, window: &mut Window, cx: &mut Context<Self>) {
        let title = self
            .agent_by_ref(&r#ref)
            .map(|a| a.title.clone())
            .unwrap_or_default();
        self.agent
            .title_input
            .update(cx, |s, cx| s.set_value(title, window, cx));
        self.agent.rename = Some(r#ref);
        let focus = self.agent.title_input.read(cx).focus_handle(cx);
        focus.focus(window, cx);
        cx.notify();
    }

    fn rename(&mut self, cx: &mut Context<Self>) {
        let Some(r#ref) = self.agent.rename.take() else {
            return;
        };
        let title = self.agent.title_input.read(cx).value().trim().to_string();
        let (p, a) = split(&r#ref);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let req = UpdateAgentRequest {
                title: Some(title),
                ..Default::default()
            };
            let body = serde_json::to_vec(&req).unwrap_or_default();
            let res = client
                .raw(
                    agentbox_api::client::Method::PATCH,
                    &format!("/v1/agents/{p}/{a}"),
                    Some(body),
                )
                .await
                .and_then(|b| Ok(serde_json::from_slice::<Agent>(&b)?));
            this.update(cx, |this, cx| {
                match res {
                    Ok(agent) => {
                        toasts::info(
                            cx,
                            if agent.title.is_empty() {
                                t("agent.view.titleCleared")
                            } else {
                                tv("agent.view.titled", &[("title", (&agent.title).into())])
                            },
                        );
                        this.replace_agent(agent);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    pub(super) fn open_snapshots(&mut self, cx: &mut Context<Self>) {
        self.agent.page = Some(Page::Snapshots);
        self.load_snapshots(cx);
        cx.notify();
    }

    fn load_snapshots(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n) = (a.project.clone(), a.name.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let snaps = client
                .get::<Vec<Snapshot>>(&format!("/v1/agents/{p}/{n}/snapshots"))
                .await
                .map_err(|e| e.to_string());
            let checks = client
                .get::<Vec<Checkpoint>>(&format!("/v1/agents/{p}/{n}/checkpoints"))
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                this.agent.snapshots = Some(snaps);
                this.agent.checkpoints = checks;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn take_snapshot(&mut self, cx: &mut Context<Self>) {
        let Some(a) = self.open_agent() else { return };
        let (p, n) = (a.project.clone(), a.name.clone());
        let name = self.agent.snapshot_name.read(cx).value().trim().to_string();
        let req = SnapshotRequest {
            name,
            consistent: self.agent.consistent,
        };
        let client = self.client.clone();
        self.agent.busy = true;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Snapshot>(&format!("/v1/agents/{p}/{n}/snapshots"), &req)
                .await;
            this.update(cx, |this, cx| {
                this.agent.busy = false;
                match res {
                    Ok(s) => toasts::info(
                        cx,
                        tv("agent.snapshots.taken", &[("name", (&s.name).into())]),
                    ),
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                this.load_snapshots(cx);
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// confirmed does what the open confirmation asked.
    fn confirmed(&mut self, cx: &mut Context<Self>) {
        let Some(c) = self.agent.confirm.take() else {
            return;
        };
        let Some(a) = self.open_agent() else { return };
        let (p, n, name) = (a.project.clone(), a.name.clone(), a.name.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let base = format!("/v1/agents/{p}/{n}");
            match c {
                Confirm::Rollback {
                    checkpoint,
                    n: turn,
                } => {
                    let res = client
                        .post::<_, RollbackResult>(
                            &format!("{base}/rollback"),
                            &RollbackRequest { checkpoint },
                        )
                        .await;
                    this.update(cx, |this, cx| {
                        match res {
                            Ok(r) => Toast::new(
                                Kind::Info,
                                tv("chat.turn.rolledBack", &[("n", turn.into())]),
                            )
                            .description(tv(
                                "chat.turn.rolledBackSaved",
                                &[("name", (&name).into()), ("id", (&r.saved.id).into())],
                            ))
                            .show(cx),
                            Err(e) => toasts::error(cx, e.to_string()),
                        }
                        this.load_snapshots(cx);
                        // The chat lost the turns after it.
                        if let Some(chat) = &this.chat {
                            chat.update(cx, |c, cx| c.load(cx));
                        }
                    })
                    .ok();
                }
                Confirm::Restore { snapshot } => {
                    let res = client
                        .post::<_, Job>(
                            &format!("{base}/restore"),
                            &RestoreRequest {
                                snapshot: snapshot.clone(),
                            },
                        )
                        .await;
                    this.update(cx, |this, cx| {
                        match res {
                            Ok(job) => this.agent.restoring = Some((snapshot, job)),
                            Err(e) => toasts::error(cx, e.to_string()),
                        }
                        cx.notify();
                    })
                    .ok();
                }
                Confirm::DeleteSnapshot { name } => {
                    let res = client
                        .raw(
                            agentbox_api::client::Method::DELETE,
                            &format!("{base}/snapshots/{name}"),
                            None,
                        )
                        .await;
                    this.update(cx, |this, cx| {
                        if let Err(e) = res {
                            toasts::error(cx, e.to_string());
                        }
                        this.load_snapshots(cx);
                    })
                    .ok();
                }
            }
        })
        .detach();
        cx.notify();
    }

    pub(super) fn start_fork(
        &mut self,
        title: String,
        description: String,
        request: ForkRequest,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(a) = self.open_agent() else { return };
        self.agent.fork = Some(Fork {
            r#ref: a.r#ref.clone(),
            title,
            description,
            request,
            agent_title: a.title.clone(),
            job: None,
            failed: false,
        });
        for i in [&self.agent.fork_title, &self.agent.fork_name] {
            i.update(cx, |s, cx| s.set_value("", window, cx));
        }
        let focus = self.agent.fork_title.read(cx).focus_handle(cx);
        focus.focus(window, cx);
        cx.notify();
    }

    fn fork(&mut self, cx: &mut Context<Self>) {
        let Some(f) = self.agent.fork.clone() else {
            return;
        };
        let mut req = f.request.clone();
        req.title = self.agent.fork_title.read(cx).value().trim().to_string();
        req.name = self.agent.fork_name.read(cx).value().trim().to_string();
        let (p, a) = split(&f.r#ref);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let mut res = client
                .post::<_, Job>(&format!("/v1/agents/{p}/{a}/fork"), &req)
                .await;
            // A job: followed until it ends, as JobProgress does.
            while let Ok(job) = &res {
                if job.status != "running" && job.status != "queued" {
                    break;
                }
                this.update(cx, |this, cx| {
                    if let Some(f) = &mut this.agent.fork {
                        f.job = Some(job.clone());
                    }
                    cx.notify();
                })
                .ok();
                cx.background_executor().timer(Duration::from_secs(1)).await;
                res = client.get::<Job>(&format!("/v1/jobs/{}", job.id)).await;
            }
            this.update(cx, |this, cx| {
                match res {
                    Ok(job) if job.status == "succeeded" => {
                        let r = job
                            .result
                            .get("ref")
                            .and_then(|v| v.as_str())
                            .unwrap_or_default()
                            .to_string();
                        if this.agent.fork.as_ref().is_some_and(|x| x.r#ref == f.r#ref) {
                            this.agent.fork = None;
                        }
                        toasts::info(cx, tv("agent.snapshots.ready", &[("ref", (&r).into())]));
                        this.load_fleet_now(cx);
                    }
                    Ok(job) => {
                        if let Some(f) = &mut this.agent.fork {
                            f.failed = true;
                            f.job = Some(job.clone());
                        }
                        if !job.error.is_empty() {
                            toasts::error(cx, job.error);
                        }
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        if let Some(f) = &mut self.agent.fork {
            f.job = Some(Job {
                status: "running".into(),
                ..Default::default()
            });
        }
        cx.notify();
    }

    /// render_agent_header is the agent's own row under the project's header.
    pub(super) fn render_agent_header(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let a = self.open_agent()?.clone();
        let (text, tone) = st::chat_label(&a);
        let primary = st::lifecycle_actions(&a.state)
            .into_iter()
            .find(|x| x.primary());
        let r = a.r#ref.clone();
        let info_open = self.agent.info.as_deref() == Some(r.as_str());
        let menu_tip: SharedString = t("agent.view.moreActions").into();
        let branch_tip: SharedString = t("agent.view.branch").into();
        let details_tip: SharedString = t("agent.view.details").into();
        let menu = Button::new("agent-menu")
            .ghost()
            .small()
            .label("⋯")
            .tooltip(menu_tip)
            .dropdown_menu_with_anchor(Anchor::TopRight, self.agent_menu(&a, true, cx));
        let info_ref = r.clone();
        Some(
            h_flex()
                .flex_none()
                .px_4()
                .py_2()
                .gap_2()
                .bg(theme::bg())
                .child(
                    h_flex()
                        .id("agent-status")
                        .gap_1p5()
                        .px_2()
                        .py_0p5()
                        .rounded_full()
                        .border_1()
                        .border_color(tone_color(tone).opacity(0.4))
                        .bg(tone_color(tone).opacity(0.12))
                        .text_xs()
                        .text_color(tone_color(tone))
                        .cursor_pointer()
                        .tooltip(move |w, cx| Tooltip::new(details_tip.clone()).build(w, cx))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            let just_closed = this
                                .agent
                                .info_closed
                                .is_some_and(|t| t.elapsed() < Duration::from_millis(400));
                            if just_closed {
                                return;
                            }
                            if this.agent.info.is_some() {
                                this.agent.info = None;
                                cx.notify();
                            } else {
                                this.open_info(info_ref.clone(), cx);
                            }
                        }))
                        .child(
                            div()
                                .size(px(6.))
                                .rounded_full()
                                .bg(theme::state_color(&a.state)),
                        )
                        .child(text)
                        .child(div().text_color(theme::faint()).child("▾"))
                        .when(info_open, |el| {
                            el.child(deferred(
                                anchored()
                                    .offset(point(px(0.), px(26.)))
                                    .snap_to_window_with_margin(px(8.))
                                    .child(self.info_card(&a, cx)),
                            ))
                        }),
                )
                .map(|el| match a.pr.as_ref().filter(|p| p.number > 0) {
                    Some(pr) => el.child(pulls::pr_chip("agent-pr", pr)),
                    None => el.child(
                        div()
                            .id("agent-branch")
                            .max_w(px(280.))
                            .truncate()
                            .px_2()
                            .py_0p5()
                            .rounded_md()
                            .border_1()
                            .border_color(theme::border())
                            .text_xs()
                            .font_family(theme::mono())
                            .text_color(theme::muted())
                            .tooltip(move |w, cx| Tooltip::new(branch_tip.clone()).build(w, cx))
                            .child(format!("⎇ {}", a.branch)),
                    ),
                })
                .child(div().flex_1())
                .when(self.agent.busy, |el| {
                    el.child(div().text_xs().text_color(theme::faint()).child("…"))
                })
                .when_some(primary, |el, action| {
                    let r = r.clone();
                    el.child(controls::button(
                        "agent-primary",
                        format!("▶ {}", action.label()),
                        Btn::Primary,
                        self.agent.busy,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.run_action(r.clone(), action, cx)
                        }),
                    ))
                })
                .child(menu)
                .into_any_element(),
        )
    }

    /// info_card is AgentInfoCard: what isn't on its row already.
    pub(super) fn info_card(&self, a: &FleetAgent, cx: &mut Context<Self>) -> AnyElement {
        let row = |label: String, value: String, mono: bool| {
            h_flex()
                .gap_3()
                .justify_between()
                .text_xs()
                .child(div().flex_none().text_color(theme::faint()).child(label))
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::fg())
                        .when(mono, |el| el.font_family(theme::mono()))
                        .child(value),
                )
        };
        let dash = || "—".to_string();
        let account = match a.ai.as_str() {
            "claude" => a.claude_account.clone(),
            "codex" => a.codex_account.clone(),
            "cursor" => a.cursor_account.clone(),
            "gemini" => a.gemini_account.clone(),
            "opencode" => a
                .open_code_accounts
                .iter()
                .map(|(p, a)| format!("{p}: {a}"))
                .collect::<Vec<_>>()
                .join(", "),
            _ => String::new(),
        };
        let disk = self
            .agent
            .disk
            .as_ref()
            .filter(|(r, _)| r == &a.r#ref)
            .map(|(_, d)| d.clone());
        let size = |n: Option<i64>| match (n, &disk) {
            (Some(n), _) => crate::files::human_bytes(n),
            (None, None) => "…".into(),
            (None, Some(_)) => dash(),
        };
        let usage = self
            .panel
            .usage_of(&a.r#ref)
            .filter(|_| a.state == "running");
        let title = if a.title.is_empty() {
            a.name.clone()
        } else {
            a.title.clone()
        };
        let (state_text, tone) = st::chat_label(a);
        v_flex()
            .id("agent-info")
            .w(px(280.))
            .gap_1p5()
            .p_3()
            .rounded_lg()
            .bg(theme::panel())
            .border_1()
            .border_color(theme::border())
            .shadow_lg()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .on_mouse_down_out(cx.listener(|this, _: &MouseDownEvent, _, cx| {
                if this.agent.info.take().is_some() {
                    this.agent.info_closed = Some(std::time::Instant::now());
                    cx.notify();
                }
            }))
            .on_click(cx.listener(|_, _: &ClickEvent, _, cx| cx.stop_propagation()))
            .child(
                h_flex()
                    .gap_2()
                    .justify_between()
                    .child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_sm()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::fg())
                            .child(title),
                    )
                    .child(controls::badge(state_text, tone_color(tone))),
            )
            .when(!a.title.is_empty(), |el| {
                el.child(
                    div()
                        .text_xs()
                        .font_family(theme::mono())
                        .text_color(theme::faint())
                        .child(a.name.clone()),
                )
            })
            .child(row(
                t("agent.info.aiTool"),
                format!(
                    "{}{}",
                    ai_label(&a.ai),
                    if a.autonomous {
                        t("agent.view.autonomousSuffix")
                    } else {
                        String::new()
                    }
                ),
                false,
            ))
            .child(row(
                tv(
                    "agent.info.toolAccount",
                    &[("tool", (&ai_label(&a.ai)).into())],
                ),
                if account.is_empty() { dash() } else { account },
                false,
            ))
            .child(row(
                t("agent.info.githubAccount"),
                if a.github_account.is_empty() {
                    dash()
                } else {
                    a.github_account.clone()
                },
                false,
            ))
            .child(row(t("agent.info.branch"), a.branch.clone(), true))
            .child(row(t("agent.info.uptime"), time_ago(&a.created_at), false))
            .when(!a.ip.is_empty(), |el| {
                el.child(row(t("agent.info.ip"), a.ip.clone(), true))
            })
            .when_some(usage, |el, (cpu, mem)| {
                el.child(row(
                    t("agent.info.resources"),
                    format!(
                        "{}% · {}",
                        cpu.map_or("–".into(), |c| format!("{c:.0}")),
                        mem.map_or("–".into(), crate::files::human_bytes)
                    ),
                    true,
                ))
            })
            .child(row(
                t("agent.info.machineDisk"),
                size(disk.as_ref().and_then(|d| d.machine)),
                false,
            ))
            .child(row(
                t("agent.info.worktreeDisk"),
                size(disk.as_ref().and_then(|d| d.worktree)),
                false,
            ))
            .when_some(a.pr.as_ref().filter(|p| p.number > 0), |el, pr| {
                let state = if pr.draft {
                    t("agent.pr.draft")
                } else {
                    let k = format!("agent.pr.{}", pr.state);
                    if agentbox_i18n::has(&k) {
                        t(&k)
                    } else {
                        pr.state.clone()
                    }
                };
                el.child(row(
                    t("agent.info.pullRequest"),
                    format!("#{} {state}", pr.number),
                    false,
                ))
            })
            .into_any_element()
    }

    /// render_machine_bar is the strip that says the agent has a computer of
    /// its own, while the panel shows something else.
    pub(super) fn render_machine_bar(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let a = self.open_agent()?.clone();
        if self.panel_shows_agent() {
            return None;
        }
        let asleep = st::asleep(&a.state);
        let line = if asleep {
            t("desk.asleep")
        } else if self.panel.desktop_up() {
            t("desk.idle")
        } else {
            t("desk.noDesktop")
        };
        let last = self.panel.last_frame(&a.r#ref);
        let r = a.r#ref.clone();
        let current = self.panel.view_of(&a.r#ref);
        let views = [
            PView::Desktop,
            PView::Terminal,
            PView::Files,
            PView::Evidence,
        ];
        Some(
            h_flex()
                .flex_none()
                .mx_4()
                .mb_2()
                .gap_3()
                .py_1p5()
                .pl_1p5()
                .pr_2()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .child(
                    div()
                        .id("machine-peek")
                        .flex_none()
                        .w(px(104.))
                        .h(px(65.))
                        .rounded_lg()
                        .overflow_hidden()
                        .bg(theme::bg())
                        .border_1()
                        .border_color(theme::border())
                        .cursor_pointer()
                        .hover(|el| el.border_color(theme::accent()))
                        .on_click({
                            let r = r.clone();
                            cx.listener(move |this, _: &ClickEvent, w, cx| {
                                this.show_panel_view(&r, PView::Desktop, w, cx)
                            })
                        })
                        .map(|el| match last {
                            Some(p) => el.child(img(p).size_full().object_fit(ObjectFit::Cover)),
                            None => el
                                .flex()
                                .items_center()
                                .justify_center()
                                .text_color(theme::faint())
                                .child(if asleep { "⏸" } else { "▣" }),
                        }),
                )
                .child(
                    v_flex()
                        .min_w_0()
                        .flex_1()
                        .child(
                            div()
                                .truncate()
                                .text_sm()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::fg())
                                .child(tv("desk.ownComputer", &[("name", (&a.name).into())])),
                        )
                        .child(
                            div()
                                .truncate()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(line),
                        ),
                )
                .children(views.into_iter().map(|v| {
                    let r = r.clone();
                    let on = current == v;
                    h_flex()
                        .id(SharedString::from(format!("machine-view-{}", v.key())))
                        .flex_none()
                        .h(px(28.))
                        .px_2()
                        .gap_1p5()
                        .rounded_md()
                        .border_1()
                        .border_color(if on {
                            theme::accent().opacity(0.5)
                        } else {
                            theme::border()
                        })
                        .when(on, |el| el.bg(theme::accent().opacity(0.1)))
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(if on { theme::fg() } else { theme::muted() })
                        .cursor_pointer()
                        .hover(|el| el.text_color(theme::fg()).border_color(theme::accent()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, w, cx| {
                            this.show_panel_view(&r, v, w, cx)
                        }))
                        .child(v.glyph())
                        .child(v.label())
                }))
                .into_any_element(),
        )
    }

    /// render_snapshots is the Snapshots page over the agent's chat.
    pub(super) fn render_snapshots(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(a) = self.open_agent().cloned() else {
            return div().into_any_element();
        };
        let card = |title: String| {
            v_flex()
                .gap_3()
                .p_4()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .child(
                    div()
                        .text_sm()
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::fg())
                        .child(title),
                )
        };
        let take = card(t("agent.snapshots.takeTitle"))
            .child(controls::note(t("agent.snapshots.takeDescription")))
            .child(
                h_flex()
                    .gap_4()
                    .flex_wrap()
                    .items_end()
                    .child(controls::labeled(
                        t("agent.snapshots.name"),
                        div()
                            .w(px(256.))
                            .child(Input::new(&self.agent.snapshot_name).small()),
                    ))
                    .child(
                        h_flex()
                            .gap_2()
                            .child(controls::toggle(
                                "snapshot-consistent",
                                self.agent.consistent,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.agent.consistent = !this.agent.consistent;
                                    cx.notify();
                                }),
                            ))
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(theme::fg())
                                    .child(t("agent.snapshots.consistent")),
                            ),
                    )
                    .child(div().flex_1())
                    .child(controls::button(
                        "snapshot-take",
                        t("agent.snapshots.take"),
                        Btn::Primary,
                        self.agent.busy
                            || matches!(a.state.as_str(), "incomplete" | "initializing"),
                        cx.listener(|this, _: &ClickEvent, _, cx| this.take_snapshot(cx)),
                    )),
            );
        let snaps: Vec<Snapshot> = match &self.agent.snapshots {
            Some(Ok(s)) => s.clone(),
            _ => Vec::new(),
        };
        let list = card(tv(
            "agent.snapshots.title",
            &[("count", snaps.len().into())],
        ))
        .when_some(
            self.agent.snapshots.as_ref().and_then(|s| s.as_ref().err()),
            |el, e| el.child(controls::note_in(e.clone(), theme::error())),
        )
        .when(
            matches!(&self.agent.snapshots, Some(Ok(s)) if s.is_empty()),
            |el| el.child(controls::note(t("agent.snapshots.empty"))),
        )
        .children(snaps.into_iter().map(|s| {
            let (restore, fork, delete) = (s.name.clone(), s.name.clone(), s.name.clone());
            let fork_title = tv(
                "agent.snapshots.forkTitle",
                &[("name", (&format!("{}@{}", a.name, s.name)).into())],
            );
            h_flex()
                .gap_3()
                .px_3()
                .py_2()
                .rounded_lg()
                .border_1()
                .border_color(theme::border())
                .bg(theme::bg())
                .child(
                    div()
                        .size(px(9.))
                        .rounded_full()
                        .border_2()
                        .border_color(theme::accent()),
                )
                .child(
                    v_flex()
                        .min_w_0()
                        .child(
                            div()
                                .text_sm()
                                .font_family(theme::mono())
                                .text_color(theme::fg())
                                .child(s.name.clone()),
                        )
                        .child(div().text_xs().text_color(theme::muted()).child(format!(
                            "{} · {}",
                            time_ago(&s.created_at),
                            short_commit(&s.head)
                        ))),
                )
                .child(div().flex_1())
                .child(controls::button(
                    SharedString::from(format!("snap-restore-{}", s.name)),
                    t("agent.snapshots.restore"),
                    Btn::Secondary,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.agent.confirm = Some(Confirm::Restore {
                            snapshot: restore.clone(),
                        });
                        cx.notify();
                    }),
                ))
                .child(controls::button(
                    SharedString::from(format!("snap-fork-{}", s.name)),
                    t("agent.snapshots.fork"),
                    Btn::Secondary,
                    false,
                    cx.listener(move |this, _: &ClickEvent, w, cx| {
                        this.start_fork(
                            fork_title.clone(),
                            t("agent.snapshots.forkDescription"),
                            ForkRequest {
                                snapshot: fork.clone(),
                                ..Default::default()
                            },
                            w,
                            cx,
                        )
                    }),
                ))
                .child(controls::button(
                    SharedString::from(format!("snap-delete-{}", s.name)),
                    "✕",
                    Btn::Ghost,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.agent.confirm = Some(Confirm::DeleteSnapshot {
                            name: delete.clone(),
                        });
                        cx.notify();
                    }),
                ))
        }));
        v_flex()
            .size_full()
            .child(
                h_flex()
                    .flex_none()
                    .px_4()
                    .pb_2()
                    .gap_2()
                    .child(controls::button(
                        "agent-back-to-chat",
                        format!("← {}", t("agent.view.backToChat")),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.agent.page = None;
                            cx.notify();
                        }),
                    ))
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("agent.tab.snapshots")),
                    ),
            )
            .child(
                div()
                    .id("agent-snapshots")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .mx_4()
                    .mb_4()
                    .child(
                        v_flex()
                            .gap_4()
                            .child(take)
                            .when_some(self.agent.restoring.clone(), |el, (name, job)| {
                                el.child(
                                    card(tv(
                                        "agent.snapshots.restoring",
                                        &[("name", (&name).into())],
                                    ))
                                    .child(controls::note(job.status.clone())),
                                )
                            })
                            .child(list),
                    ),
            )
            .into_any_element()
    }

    /// render_agent_dialogs is whichever of the agent's dialogs is open.
    pub(super) fn render_agent_dialogs(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let cancel = |f: fn(&mut AppView)| {
            let view = cx.entity().downgrade();
            Rc::new(move |_: &mut Window, cx: &mut App| {
                view.update(cx, |this, cx| {
                    f(this);
                    cx.notify();
                })
                .ok();
            }) as OnCancel
        };
        if let Some(d) = &self.agent.destroy {
            let switch = |id: &'static str, on: bool, label: String, f: fn(&mut Destroy)| {
                h_flex()
                    .gap_3()
                    .child(controls::toggle(
                        id,
                        on,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            if let Some(d) = &mut this.agent.destroy {
                                f(d);
                            }
                            cx.notify();
                        }),
                    ))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .text_sm()
                            .text_color(theme::fg())
                            .child(label),
                    )
            };
            let body = v_flex()
                .gap_3()
                .p_3()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::bg())
                .child(switch(
                    "destroy-force",
                    d.force,
                    t("agent.destroy.discard"),
                    |d| d.force = !d.force,
                ))
                .child(switch(
                    "destroy-branch",
                    d.delete_branch,
                    strip_tags(&tv(
                        "agent.destroy.deleteBranch",
                        &[("branch", (&d.branch).into())],
                    )),
                    |d| d.delete_branch = !d.delete_branch,
                ))
                .child(switch(
                    "destroy-media",
                    d.delete_media,
                    t("agent.destroy.deleteMedia"),
                    |d| d.delete_media = !d.delete_media,
                ))
                .into_any_element();
            return Some(modal(
                "destroy",
                tv("agent.destroy.title", &[("name", (&d.title).into())]),
                Some(t("agent.destroy.description")),
                vec![body],
                cancel(|this| this.agent.destroy = None),
                t("agent.destroy.confirm"),
                true,
                false,
                cx.listener(|this, _: &ClickEvent, w, cx| this.destroy(w, cx)),
            ));
        }
        if let Some(c) = &self.agent.confirm {
            let a = self.open_agent()?;
            let name = a.name.clone();
            let agent = if a.title.is_empty() {
                a.name.clone()
            } else {
                a.title.clone()
            };
            let (title, body, ok) = match c {
                Confirm::Rollback { n, .. } => (
                    tv(
                        "chat.turn.rollBackTitle",
                        &[("name", (&name).into()), ("n", (*n).into())],
                    ),
                    tv("chat.turn.rollBackDescription", &[("name", (&name).into())]),
                    t("chat.turn.rollBack"),
                ),
                Confirm::Restore { snapshot } => (
                    tv(
                        "agent.snapshots.restoreTitle",
                        &[("agent", (&agent).into()), ("name", snapshot.into())],
                    ),
                    strip_tags(&t("agent.snapshots.restoreDescription")),
                    t("agent.snapshots.restore"),
                ),
                Confirm::DeleteSnapshot { name } => (
                    tv("agent.snapshots.deleteTitle", &[("name", name.into())]),
                    t("agent.snapshots.deleteDescription"),
                    t("common.delete"),
                ),
            };
            return Some(modal(
                "agent-confirm",
                title,
                Some(body),
                Vec::new(),
                cancel(|this| this.agent.confirm = None),
                ok,
                true,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| this.confirmed(cx)),
            ));
        }
        if let Some(f) = &self.agent.fork {
            let running = f.job.is_some() && !f.failed;
            let hint = if f.agent_title.is_empty() {
                t("agent.snapshots.optional")
            } else {
                tv(
                    "agent.snapshots.forkTitleDefault",
                    &[("title", (&f.agent_title).into())],
                )
            };
            let fields = v_flex()
                .gap_3()
                .child(controls::labeled(
                    t("agent.snapshots.forkTitleField"),
                    v_flex()
                        .gap_1()
                        .child(Input::new(&self.agent.fork_title).small())
                        .child(controls::note(hint)),
                ))
                .child(controls::labeled(
                    t("agent.snapshots.name"),
                    v_flex()
                        .gap_1()
                        .child(Input::new(&self.agent.fork_name).small())
                        .child(controls::note(t("agent.snapshots.forkNameHint"))),
                ))
                .when_some(f.job.as_ref().filter(|_| running), |el, _| {
                    el.child(controls::note(format!("{}…", t("common.loading"))))
                })
                .into_any_element();
            return Some(modal(
                "fork",
                f.title.clone(),
                Some(f.description.clone()),
                vec![fields],
                cancel(|this| this.agent.fork = None),
                if running {
                    t("agent.snapshots.keepRunning")
                } else {
                    t("agent.snapshots.fork")
                },
                false,
                false,
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    if running {
                        this.agent.fork = None;
                        cx.notify();
                    } else {
                        this.fork(cx)
                    }
                }),
            ));
        }
        if self.agent.rename.is_some() {
            let field = Input::new(&self.agent.title_input).into_any_element();
            return Some(modal(
                "rename",
                t("agent.view.agentTitle"),
                None,
                vec![field],
                cancel(|this| this.agent.rename = None),
                t("common.save"),
                false,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| this.rename(cx)),
            ));
        }
        if let Some(r) = &self.agent.info
            && self.open_agent().is_none_or(|a| &a.r#ref != r)
        {
            // Info opened from a row's menu: as a dialog, over the window.
            let a = self.agent_by_ref(r)?.clone();
            let close = cancel(|this| this.agent.info = None);
            let backdrop = close.clone();
            return Some(
                div()
                    .id("agent-info-dialog")
                    .absolute()
                    .inset_0()
                    .bg(gpui::black().opacity(0.45))
                    .flex()
                    .items_center()
                    .justify_center()
                    .on_mouse_down(MouseButton::Left, move |_, w, cx| backdrop(w, cx))
                    .child(self.info_card(&a, cx))
                    .into_any_element(),
            );
        }
        None
    }
}

/// modal is a dialog over the window: a title, what it does, its body, and
/// Cancel beside the button that does it.
#[allow(clippy::too_many_arguments)]
pub fn modal(
    id: &'static str,
    title: String,
    description: Option<String>,
    body: Vec<AnyElement>,
    on_cancel: OnCancel,
    confirm: String,
    destructive: bool,
    disabled: bool,
    on_confirm: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static,
) -> AnyElement {
    let backdrop = on_cancel.clone();
    div()
        .id(SharedString::from(format!("{id}-backdrop")))
        .absolute()
        .inset_0()
        .bg(gpui::black().opacity(0.55))
        .flex()
        .items_center()
        .justify_center()
        .on_mouse_down(MouseButton::Left, move |_, window, cx| backdrop(window, cx))
        .child(
            v_flex()
                .id(SharedString::from(format!("{id}-card")))
                .w(px(480.))
                .max_w(relative(0.92))
                .gap_4()
                .p_6()
                .rounded_xl()
                .bg(theme::panel())
                .border_1()
                .border_color(theme::border())
                .shadow_lg()
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .child(
                    div()
                        .text_lg()
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::fg())
                        .child(title),
                )
                .when_some(description, |el, d| {
                    el.child(div().text_sm().text_color(theme::muted()).child(d))
                })
                .children(body)
                .child(
                    h_flex()
                        .gap_2()
                        .justify_end()
                        .child(controls::button(
                            SharedString::from(format!("{id}-cancel")),
                            t("common.cancel"),
                            Btn::Ghost,
                            false,
                            move |_, w, cx| on_cancel(w, cx),
                        ))
                        .child(controls::button(
                            SharedString::from(format!("{id}-confirm")),
                            confirm,
                            if destructive {
                                Btn::Danger
                            } else {
                                Btn::Primary
                            },
                            disabled,
                            on_confirm,
                        )),
                ),
        )
        .into_any_element()
}
