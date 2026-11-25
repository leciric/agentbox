//! The Running view (components/RunningProcesses.tsx, lib/running.ts): what
//! each agent's machine still has running, as the daemon groups it in trees
//! (internal/agent/processes.go), so a dev server with what it started is one
//! row. Each tree the daemon doesn't protect has a Kill, confirmed in place;
//! the AI tool's own and the machine's are shown locked, so an agent's
//! figures add up. The dialog opens from the sidebar's Running button, with its
//! count (foot in sidebar_actions.rs), and from the top bar's machine popover
//! (usage.rs), on the open project; each agent's Resources view (resources.rs)
//! shows its own list. Those are read again every few seconds while they're
//! shown: the daemon measures CPU between two readings, and sends no events
//! for it. The button's count is read every half minute.
use std::collections::{HashMap, HashSet};
use std::time::Duration;

use agentbox_api::client::{Method, message_of};
use agentbox_api::{
    AgentProcesses, KillProcessResult, PROCESS_TREE_SYSTEM, PROCESS_TREE_TOOL, ProcessList,
    ProcessTree,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::split;
use super::controls::{self, long_tip};
use crate::files::{encode, human_bytes};
use crate::{icons, theme, toasts};

/// How often a list on screen is read again (refreshEvery).
const REFRESH_EVERY: Duration = Duration::from_secs(4);

/// How little a tree holds to be folded away under "N small ones" (smallTree):
/// a shell, a sleep, a watcher's helper. A flagged one never is.
pub(super) const SMALL_TREE: i64 = 32 << 20;

/// Where a list is drawn: the dialog, or an agent's Resources view. Each
/// keeps its own rows' state, as the Electron app's two lists do.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub(super) enum Place {
    Dialog,
    Panel,
}

#[derive(Default)]
pub(super) struct Running {
    /// The dialog is open, on `project`'s agents (None: every project's,
    /// opened from Home), or on every project's once widened (`all`).
    pub open: bool,
    pub project: Option<String>,
    pub all: bool,
    /// Each scope's list as last read, by project ("" every project).
    lists: HashMap<String, Result<ProcessList, String>>,
    /// The scopes being read.
    reading: HashSet<String>,
    /// One agent's, for its Resources view, and whether it's being read.
    agent: Option<(String, Result<AgentProcesses, String>)>,
    agent_reading: Option<String>,
    /// The agent the Resources view last asked for.
    agent_of: String,
    /// The rows asking whether to kill, by place, agent and pid.
    asking: HashSet<(Place, String, i64)>,
    /// The trees being killed, by agent and pid, wherever they're shown.
    killing: HashSet<(String, i64)>,
    /// The rows showing their processes.
    members: HashSet<(Place, String, i64)>,
    /// The agents whose small trees are shown.
    small: HashSet<(Place, String)>,
    scroll: ScrollHandle,
    poll: Option<Task<()>>,
}

impl Running {
    /// scope is the project the dialog's list is of, "" for every one.
    fn scope(&self) -> String {
        match (&self.project, self.all) {
            (Some(p), false) => p.clone(),
            _ => String::new(),
        }
    }
}

/// split_trees puts the trees worth a row first, the biggest first as the
/// daemon sorted them, and the small ones apart (splitTrees). The machine's
/// own tree comes last, whatever it holds: it can't be killed, so it's
/// context, not a choice.
pub(super) fn split_trees(trees: &[ProcessTree]) -> (Vec<&ProcessTree>, Vec<&ProcessTree>) {
    let (mut shown, mut small) = (Vec::new(), Vec::new());
    let mut system = None;
    for tree in trees {
        if tree.kind == PROCESS_TREE_SYSTEM {
            system = Some(tree);
        } else if tree.memory < SMALL_TREE && !tree.heavy && !tree.idle && !tree.protected {
            small.push(tree);
        } else {
            shown.push(tree);
        }
    }
    shown.extend(system);
    (shown, small)
}

/// running_count is how many things a list shows that can be killed
/// (runningCount): the trees worth a row the daemon doesn't protect, across
/// its agents' running machines. It's the count on the sidebar's Running
/// button, so it counts what the dialog lists above its "small ones", and
/// never the AI tool's own or the machine's.
pub(super) fn running_count(list: &ProcessList) -> usize {
    list.agents
        .iter()
        .filter(|a| a.running && a.error.is_empty())
        .map(|a| {
            split_trees(&a.trees)
                .0
                .iter()
                .filter(|t| !t.protected)
                .count()
        })
        .sum()
}

/// rows is what a list shows: the trees worth a row, and the small ones too
/// once asked for, before the last (TreeList).
fn rows<'a>(
    shown: &[&'a ProcessTree],
    small: &[&'a ProcessTree],
    show_small: bool,
) -> Vec<&'a ProcessTree> {
    if !show_small {
        return shown.to_vec();
    }
    match shown.split_last() {
        Some((last, rest)) => rest.iter().chain(small).chain([last]).copied().collect(),
        None => small.to_vec(),
    }
}

/// span is a stretch of time the way the Running view says it: 45s, 12m,
/// 2h 14m, 3d.
pub(super) fn span(seconds: i64) -> String {
    let s = seconds.max(0);
    if s < 60 {
        return format!("{s}s");
    }
    if s < 3600 {
        return format!("{}m", s / 60);
    }
    if s < 48 * 3600 {
        let (h, m) = (s / 3600, (s % 3600) / 60);
        return if m > 0 {
            format!("{h}h {m}m")
        } else {
            format!("{h}h")
        };
    }
    format!("{}d", s / 86400)
}

/// up_for is how long a tree has run, from when it started (upFor).
pub(super) fn up_for(started: &str, now_ms: i64) -> Option<String> {
    let at = chrono::DateTime::parse_from_rfc3339(started).ok()?;
    let at = at.timestamp_millis();
    (at > 0).then(|| span((now_ms - at) / 1000))
}

/// cpu_percent is a tree's CPU as the view says it: one decimal under 10%,
/// none above, in the user's language (formatNumber's maximumFractionDigits).
pub(super) fn cpu_percent(cpu: f64) -> String {
    let s = if cpu < 10. {
        let s = format!("{cpu:.1}");
        s.strip_suffix(".0").map(str::to_string).unwrap_or(s)
    } else {
        format!("{cpu:.0}")
    };
    if agentbox_i18n::language().starts_with("pt") {
        s.replace('.', ",")
    } else {
        s
    }
}

/// without_tree is a list as it is once a tree was killed, before the next
/// reading says so: the tree gone, and its agent's totals down by it.
pub(super) fn without_tree(list: &mut ProcessList, r: &str, pid: i64) {
    for a in list.agents.iter_mut().filter(|a| a.r#ref == r) {
        agent_without_tree(a, pid);
    }
}

pub(super) fn agent_without_tree(a: &mut AgentProcesses, pid: i64) {
    let Some(i) = a.trees.iter().position(|t| t.pid == pid) else {
        return;
    };
    let tree = a.trees.remove(i);
    a.memory -= tree.memory;
    if !tree.protected {
        a.killable -= tree.memory;
    }
    a.cpu -= tree.cpu;
    a.processes -= tree.processes;
}

/// tree_label is a tree's name in the list: the machine's is "System".
fn tree_label(tree: &ProcessTree) -> String {
    if tree.kind == PROCESS_TREE_SYSTEM {
        t("running.system")
    } else {
        tree.label.clone()
    }
}

fn agent_name(a: &AgentProcesses) -> String {
    if a.title.is_empty() {
        a.name.clone()
    } else {
        a.title.clone()
    }
}

fn tip(text: String) -> impl Fn(&mut Window, &mut App) -> AnyView + 'static {
    let text: SharedString = text.into();
    move |window, cx| Tooltip::new(text.clone()).build(window, cx)
}

/// small_button is Electron's Button at h-7 with caption text: the row's
/// Kill and its confirmation's.
fn small_button(id: impl Into<ElementId>, kind: controls::Btn) -> Stateful<Div> {
    let el = h_flex()
        .id(id)
        .flex_none()
        .h(px(28.))
        .px_2()
        .gap_1p5()
        .items_center()
        .justify_center()
        .rounded_lg()
        .text_size(theme::text::CAPTION)
        .font_weight(FontWeight::MEDIUM)
        .cursor_pointer()
        .tab_index(0)
        .focus_visible(|s| s.shadow(controls::focus_ring()));
    match kind {
        controls::Btn::Danger => el
            .px_2p5()
            .bg(theme::destructive(0))
            .text_color(gpui::white())
            .hover(|s| s.bg(theme::destructive(1)))
            .active(|s| s.bg(theme::destructive(2))),
        controls::Btn::Ghost => el
            .px_2p5()
            .text_color(theme::muted())
            .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
            .active(|s| s.bg(theme::surface_strong())),
        _ => el
            .text_color(theme::rose(300))
            .hover(|s| {
                s.bg(theme::rose(500).opacity(0.1))
                    .text_color(theme::rose(200))
            })
            .active(|s| s.bg(theme::rose(500).opacity(0.2))),
    }
}

impl AppView {
    /// open_running opens the Running view, on a project's agents or, with
    /// none, on every project's (openRunning).
    pub(super) fn open_running(&mut self, project: Option<String>, cx: &mut Context<Self>) {
        let r = &mut self.running;
        r.open = true;
        r.project = project;
        r.all = false;
        self.read_running(cx);
        self.poll_running(cx);
        cx.notify();
    }

    /// close_running closes it, forgetting what its rows were asking.
    pub(super) fn close_running(&mut self, cx: &mut Context<Self>) {
        let r = &mut self.running;
        r.open = false;
        r.all = false;
        r.asking.retain(|(p, _, _)| *p != Place::Dialog);
        r.members.retain(|(p, _, _)| *p != Place::Dialog);
        r.small.retain(|(p, _)| *p != Place::Dialog);
        cx.notify();
    }

    fn set_running_scope(&mut self, all: bool, cx: &mut Context<Self>) {
        if self.running.all != all {
            self.running.all = all;
            self.read_running(cx);
            cx.notify();
        }
    }

    /// count_scope is the project the button's count is of, the open one's
    /// or, on Home, "" for every project's: the scope the dialog opens on.
    pub(super) fn count_scope(&self) -> String {
        self.project
            .clone()
            .filter(|_| !self.is_home())
            .unwrap_or_default()
    }

    /// running_badge is the count on the sidebar's Running button, from the
    /// list last read for its scope: 0 until there is one.
    pub(super) fn running_badge(&self) -> usize {
        match self.running.lists.get(&self.count_scope()) {
            Some(Ok(list)) => running_count(list),
            _ => 0,
        }
    }

    /// read_running_count reads the list for the button's count: every half
    /// minute, and at once for a scope with no list yet (another project's).
    pub(super) fn read_running_count(&mut self, every: bool, cx: &mut Context<Self>) {
        let scope = self.count_scope();
        if every || !self.running.lists.contains_key(&scope) {
            self.read_running_scope(scope, cx);
        }
    }

    /// read_running reads the dialog's list, unless it is being read.
    fn read_running(&mut self, cx: &mut Context<Self>) {
        self.read_running_scope(self.running.scope(), cx);
    }

    /// read_running_scope reads a scope's list ("" every project's), unless it
    /// is being read.
    fn read_running_scope(&mut self, scope: String, cx: &mut Context<Self>) {
        if !self.running.reading.insert(scope.clone()) {
            return;
        }
        let path = if scope.is_empty() {
            "/v1/processes".to_string()
        } else {
            format!("/v1/processes?project={}", encode(&scope))
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<ProcessList>(&path)
                .await
                .map_err(|e| message_of(&e));
            this.update(cx, |this, cx| {
                this.running.reading.remove(&scope);
                this.running.lists.insert(scope, res);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// read_agent_processes reads one agent's list for its Resources view,
    /// keeping what was shown of it until the answer comes.
    pub(super) fn read_agent_processes(&mut self, r: String, cx: &mut Context<Self>) {
        if self.running.agent.as_ref().is_some_and(|(of, _)| *of != r) {
            self.running.agent = None;
        }
        self.running.agent_of = r.clone();
        if self.running.agent_reading.as_deref() == Some(r.as_str()) {
            return;
        }
        self.running.agent_reading = Some(r.clone());
        self.poll_running(cx);
        let (p, a) = split(&r);
        let path = format!("/v1/agents/{}/{}/processes", encode(&p), encode(&a));
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<AgentProcesses>(&path)
                .await
                .map_err(|e| message_of(&e));
            this.update(cx, |this, cx| {
                if this.running.agent_reading.as_deref() == Some(r.as_str()) {
                    this.running.agent_reading = None;
                }
                // A late answer for an agent no longer shown is dropped.
                if this.running.agent_of == r {
                    this.running.agent = Some((r, res));
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// poll_running reads what's shown again every few seconds: the dialog
    /// while it's open, the agent's list while its Resources view shows.
    fn poll_running(&mut self, cx: &mut Context<Self>) {
        if self.running.poll.is_some() {
            return;
        }
        self.running.poll = Some(cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor().timer(REFRESH_EVERY).await;
                let alive = this.update(cx, |this, cx| {
                    if this.running.open {
                        this.read_running(cx);
                    }
                    if let Some(r) = this.resources_shown() {
                        this.read_agent_processes(r, cx);
                    }
                });
                if alive.is_err() {
                    break;
                }
            }
        }));
    }

    /// kill_tree kills a tree and what it started, after the row's
    /// confirmation; every list showing it loses it at once.
    fn kill_tree(
        &mut self,
        place: Place,
        agent: &AgentProcesses,
        tree: &ProcessTree,
        cx: &mut Context<Self>,
    ) {
        let (r, pid) = (agent.r#ref.clone(), tree.pid);
        if !self.running.killing.insert((r.clone(), pid)) {
            return;
        }
        let label = tree_label(tree);
        let who = agent_name(agent);
        let (p, a) = split(&r);
        let path = format!(
            "/v1/agents/{}/{}/processes/{pid}/kill",
            encode(&p),
            encode(&a)
        );
        let client = self.client.clone();
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::POST, &path, None)
                .await
                .and_then(|b| Ok(serde_json::from_slice::<KillProcessResult>(&b)?));
            this.update(cx, |this, cx| {
                let run = &mut this.running;
                run.killing.remove(&(r.clone(), pid));
                run.asking.remove(&(place, r.clone(), pid));
                match res {
                    Ok(k) => {
                        toasts::Toast::new(
                            toasts::Kind::Success,
                            tv(
                                "running.killed",
                                &[
                                    ("label", k.label.into()),
                                    ("memory", human_bytes(k.memory).into()),
                                    ("agent", who.into()),
                                ],
                            ),
                        )
                        .show(cx);
                        for list in run.lists.values_mut().flatten() {
                            without_tree(list, &r, pid);
                        }
                        if let Some((of, Ok(a))) = &mut run.agent
                            && *of == r
                        {
                            agent_without_tree(a, pid);
                        }
                        if run.open {
                            this.read_running(cx);
                        }
                        if let Some(shown) = this.resources_shown() {
                            this.read_agent_processes(shown, cx);
                        }
                    }
                    Err(e) => toasts::error(
                        cx,
                        tv(
                            "running.killFailed",
                            &[("label", label.into()), ("error", message_of(&e).into())],
                        ),
                    ),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// render_running is the dialog, while it's open.
    pub(super) fn render_running(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let run = &self.running;
        if !run.open {
            return None;
        }
        let scope = run.scope();
        let list = run.lists.get(&scope);
        let agents: &[AgentProcesses] = match list {
            Some(Ok(l)) => &l.agents,
            _ => &[],
        };
        let biggest = agents
            .iter()
            .flat_map(|a| a.trees.iter().map(|t| t.memory))
            .max()
            .unwrap_or(0)
            .max(1);
        let caption = |text: String, color: Hsla| {
            div()
                .py_6()
                .text_size(theme::text::UI)
                .text_color(color)
                .child(text)
                .into_any_element()
        };
        let body = match list {
            None => h_flex()
                .py_6()
                .gap_2()
                .text_size(theme::text::UI)
                .text_color(theme::muted())
                .child(icons::spin(icons::ic(icons::SPINNER).size(px(16.))))
                .child(t("running.loading"))
                .into_any_element(),
            Some(Err(e)) => div()
                .py_2()
                .text_size(theme::text::UI)
                .text_color(theme::rose(300))
                .child(e.clone())
                .into_any_element(),
            Some(Ok(_)) if agents.is_empty() => caption(
                if scope.is_empty() {
                    t("running.none")
                } else {
                    t("running.noneProject")
                },
                theme::muted(),
            ),
            Some(Ok(_)) => v_flex()
                .min_w_0()
                .gap_4()
                .children(agents.iter().map(|a| {
                    v_flex()
                        .min_w_0()
                        .gap_1p5()
                        .child(self.agent_heading(a, scope.is_empty(), cx))
                        .child(self.tree_list(Place::Dialog, a, biggest, cx))
                }))
                .into_any_element(),
        };
        let scope_tabs = run.project.as_ref().map(|_| {
            let tab = |id: &'static str, on: bool, label: String, all: bool| {
                div()
                    .id(id)
                    .px_2p5()
                    .py_1()
                    .rounded_md()
                    .cursor_pointer()
                    .when(on, |el| {
                        el.bg(theme::surface_strong())
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                    })
                    .when(!on, |el| {
                        el.text_color(theme::muted())
                            .hover(|s| s.text_color(theme::primary()))
                    })
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.set_running_scope(all, cx)
                    }))
                    .child(label)
            };
            h_flex()
                .flex_none()
                .self_start()
                .gap_0p5()
                .p_0p5()
                .rounded_lg()
                .bg(theme::surface_raised())
                .text_size(theme::text::CAPTION)
                .child(tab(
                    "running-scope-project",
                    !run.all,
                    t("running.scopeProject"),
                    false,
                ))
                .child(tab(
                    "running-scope-all",
                    run.all,
                    t("running.scopeAll"),
                    true,
                ))
        });
        let card = div()
            .id("running-dialog-body")
            .relative()
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .track_scroll(&run.scroll)
            .bg(theme::modal())
            .child(
                v_flex()
                    .p_6()
                    .gap_4()
                    .child(
                        v_flex()
                            .gap_1p5()
                            .pr_8()
                            .child(
                                div()
                                    .text_size(theme::text::HEADING)
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::title())
                                    .child(t("running.title")),
                            )
                            .child(
                                div()
                                    .text_sm()
                                    .line_height(rems(1.6))
                                    .text_color(theme::muted())
                                    .child(t("running.description")),
                            ),
                    )
                    .children(scope_tabs)
                    .child(body)
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child(t("running.note")),
                    ),
            )
            .child(
                div()
                    .id("running-close")
                    .absolute()
                    .top_4()
                    .right_4()
                    .p_1()
                    .rounded_md()
                    .text_color(theme::subtle())
                    .cursor_pointer()
                    .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                    .tooltip(tip(t("common.close")))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.close_running(cx)))
                    .child(icons::ic("x").size(px(16.))),
            );
        // Over the window as its other dialogs are (agent::modal), not
        // deferred as controls::window_modal is: a kill's toast is drawn
        // over it, as the Electron app's are over its dialogs.
        Some(
            crate::motion::scrim_in(
                div()
                    .id("running-backdrop")
                    .absolute()
                    .inset_0()
                    .bg(theme::scrim())
                    .flex()
                    .items_center()
                    .justify_center()
                    .occlude()
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, _: &MouseDownEvent, _, cx| this.close_running(cx)),
                    )
                    .child(crate::motion::pop_in(
                        div()
                            .id("running-card")
                            .w(px(672.))
                            .max_w(relative(0.92))
                            .max_h(relative(0.88))
                            .flex()
                            .flex_col()
                            .rounded(theme::radius::XXL)
                            .bg(theme::card())
                            .border_1()
                            .border_color(theme::line_strong())
                            .shadow(theme::shadow_modal())
                            .overflow_hidden()
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .child(card),
                        "running-card-in",
                    )),
                "running-scrim-in",
            )
            .into_any_element(),
        )
    }

    /// agent_heading is an agent's line over its trees: its name, which
    /// opens it, and what its trees hold.
    fn agent_heading(
        &self,
        a: &AgentProcesses,
        show_project: bool,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let r = a.r#ref.clone();
        h_flex()
            .min_w_0()
            .flex_wrap()
            .items_baseline()
            .gap_x_2()
            .gap_y_0p5()
            .pb_1p5()
            .border_b_1()
            .border_color(theme::line_faint())
            .child(
                div()
                    .id(SharedString::from(format!("running-agent-{r}")))
                    .min_w_0()
                    .truncate()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::primary())
                    .cursor_pointer()
                    .hover(|s| s.underline())
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.close_running(cx);
                        this.open_agent_ref(r.clone(), window, cx);
                    }))
                    .child(agent_name(a)),
            )
            .when(show_project || !a.title.is_empty(), |el| {
                el.child(
                    div()
                        .truncate()
                        .font_family(theme::mono())
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::faint())
                        .child(if show_project {
                            a.r#ref.clone()
                        } else {
                            a.name.clone()
                        }),
                )
            })
            .when(a.running, |el| {
                el.child(
                    h_flex()
                        .ml_auto()
                        .flex_none()
                        .items_baseline()
                        .gap_2()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::muted())
                        .child(
                            div()
                                .font_family(theme::mono())
                                .font_features(theme::tabular_features())
                                .child(totals(a)),
                        )
                        .when(a.killable > 0, |el| {
                            el.child(div().text_color(theme::faint()).child(tv(
                                "running.killable",
                                &[("memory", human_bytes(a.killable).into())],
                            )))
                        }),
                )
            })
            .into_any_element()
    }

    /// render_agent_processes is an agent's own list, under its Resources
    /// view's lines (AgentProcessList).
    pub(super) fn render_agent_processes(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let read = self
            .running
            .agent
            .as_ref()
            .filter(|(of, _)| of == r)
            .map(|(_, res)| res);
        let running = match read {
            Some(Ok(a)) if a.running => Some(totals(a)),
            _ => None,
        };
        let body = match read {
            None => h_flex()
                .gap_2()
                .text_size(theme::text::CAPTION)
                .text_color(theme::muted())
                .child(icons::spin(icons::ic(icons::SPINNER).size(px(14.))))
                .child(t("running.loading"))
                .into_any_element(),
            Some(Err(e)) => div()
                .text_size(theme::text::CAPTION)
                .text_color(theme::rose(300))
                .child(e.clone())
                .into_any_element(),
            Some(Ok(a)) => {
                let biggest = a.trees.iter().map(|t| t.memory).max().unwrap_or(0).max(1);
                self.tree_list(Place::Panel, a, biggest, cx)
            }
        };
        v_flex()
            .id("resource-running")
            .min_w_0()
            .gap_2()
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface_faint())
            .px_3()
            .py_2p5()
            .child(
                h_flex()
                    .min_w_0()
                    .items_baseline()
                    .gap_2()
                    .child(
                        div()
                            .text_size(theme::text::UI)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::title())
                            .child(t("running.title")),
                    )
                    .children(running.map(|s| {
                        div()
                            .ml_auto()
                            .truncate()
                            .font_family(theme::mono())
                            .font_features(theme::tabular_features())
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::muted())
                            .child(s)
                    })),
            )
            .child(body)
            .into_any_element()
    }

    /// tree_list is an agent's trees, the small ones folded away until asked
    /// for (TreeList).
    fn tree_list(
        &self,
        place: Place,
        a: &AgentProcesses,
        biggest: i64,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let note = |text: String, color: Hsla| {
            div()
                .text_size(theme::text::CAPTION)
                .text_color(color)
                .child(text)
                .into_any_element()
        };
        if !a.error.is_empty() {
            return note(
                tv("running.couldntRead", &[("error", a.error.clone().into())]),
                theme::rose(300),
            );
        }
        if !a.running {
            return note(t("running.notRunning"), theme::muted());
        }
        let (shown, small) = split_trees(&a.trees);
        let key = (place, a.r#ref.clone());
        let show_small = self.running.small.contains(&key);
        let small_memory: i64 = small.iter().map(|t| t.memory).sum();
        let toggle = (!small.is_empty()).then(|| {
            let key = key.clone();
            div()
                .id(SharedString::from(format!(
                    "running-small-{place:?}-{}",
                    a.r#ref
                )))
                .self_start()
                .px_1()
                .text_size(theme::text::CAPTION)
                .text_color(theme::brand(400))
                .cursor_pointer()
                .hover(|s| s.underline())
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                    if !this.running.small.remove(&key) {
                        this.running.small.insert(key.clone());
                    }
                    cx.notify();
                }))
                .child(if show_small {
                    t("running.hideSmall")
                } else {
                    tv(
                        "running.small",
                        &[
                            ("count", small.len().into()),
                            ("memory", human_bytes(small_memory).into()),
                        ],
                    )
                })
        });
        v_flex()
            .min_w_0()
            .gap_1()
            .children(
                rows(&shown, &small, show_small)
                    .into_iter()
                    .map(|tree| self.tree_row(place, a, tree, biggest, cx)),
            )
            .children(toggle)
            .into_any_element()
    }

    /// tree_row is one tree: its name, what it holds, how busy it is and for
    /// how long, Heavy and Idle when it is, and its Kill, which asks first.
    fn tree_row(
        &self,
        place: Place,
        a: &AgentProcesses,
        tree: &ProcessTree,
        biggest: i64,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let run = &self.running;
        let id = format!("{place:?}-{}-{}", a.r#ref, tree.pid);
        let row_key = (place, a.r#ref.clone(), tree.pid);
        let asking = run.asking.contains(&row_key);
        let killing = run.killing.contains(&(a.r#ref.clone(), tree.pid));
        let open = run.members.contains(&row_key);
        let single = place == Place::Panel;
        let label = tree_label(tree);
        let now = chrono::Utc::now().timestamp_millis();
        let mut facts = vec![
            tv("running.processes", &[("count", tree.processes.into())]),
            tv("running.cpu", &[("percent", cpu_percent(tree.cpu).into())]),
        ];
        if let Some(up) = up_for(&tree.started, now) {
            facts.push(tv("running.up", &[("span", up.into())]));
        }
        let chevron = if tree.members.len() > 1 {
            let key = row_key.clone();
            div()
                .id(SharedString::from(format!("running-members-{id}")))
                .flex_none()
                .ml(px(-4.))
                .p_0p5()
                .rounded_sm()
                .text_color(theme::faint())
                .cursor_pointer()
                .hover(|s| s.bg(theme::surface_strong()).text_color(theme::primary()))
                .tooltip(tip(if open {
                    t("running.hideMembers")
                } else {
                    t("running.showMembers")
                }))
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                    if !this.running.members.remove(&key) {
                        this.running.members.insert(key.clone());
                    }
                    cx.notify();
                }))
                .child(
                    icons::ic(if open {
                        "chevron-down"
                    } else {
                        "chevron-right"
                    })
                    .size(px(14.)),
                )
                .into_any_element()
        } else {
            div().flex_none().w_4().into_any_element()
        };
        let command: SharedString = tree.command.clone().into();
        let action = if tree.protected {
            let why = if tree.kind == PROCESS_TREE_TOOL {
                t("running.protectedTool")
            } else {
                t("running.protectedSystem")
            };
            h_flex()
                .id(SharedString::from(format!("running-protected-{id}")))
                .gap_1()
                .text_size(theme::text::CAPTION)
                .text_color(theme::faint())
                .tooltip(move |w, cx| long_tip(why.clone().into(), w, cx))
                .child(icons::ic("lock").size(px(12.)))
                .child(t("running.protected"))
                .into_any_element()
        } else if killing {
            h_flex()
                .gap_1p5()
                .text_size(theme::text::CAPTION)
                .text_color(theme::rose(200))
                .child(icons::spin(icons::ic(icons::SPINNER).size(px(14.))))
                .child(t("running.killing"))
                .into_any_element()
        } else if asking {
            div().into_any_element()
        } else {
            let key = row_key.clone();
            small_button(
                SharedString::from(format!("running-kill-{id}")),
                controls::Btn::DangerQuiet,
            )
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                this.running.asking.insert(key.clone());
                cx.notify();
            }))
            .child(icons::ic("skull").size(px(14.)))
            .child(t("running.kill"))
            .into_any_element()
        };
        let confirm = (asking && !killing).then(|| {
            let (agent, tree_c) = (a.clone(), tree.clone());
            let key = row_key.clone();
            h_flex()
                .flex_wrap()
                .gap_2()
                .pl_6()
                .pt_0p5()
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::rose(200))
                        .child(tv("running.killAsk", &[("count", tree.processes.into())])),
                )
                .child(
                    small_button(
                        SharedString::from(format!("running-kill-yes-{id}")),
                        controls::Btn::Danger,
                    )
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.kill_tree(place, &agent, &tree_c, cx)
                    }))
                    .child(t("running.kill")),
                )
                .child(
                    small_button(
                        SharedString::from(format!("running-kill-no-{id}")),
                        controls::Btn::Ghost,
                    )
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.running.asking.remove(&key);
                        cx.notify();
                    }))
                    .child(t("common.cancel")),
                )
        });
        let fill = (tree.memory as f32 / biggest as f32).clamp(0.02, 1.);
        let bar = if tree.heavy {
            theme::amber(400)
        } else if tree.protected {
            theme::label_grey()
        } else {
            theme::brand(400)
        };
        let chip = |id: String, text: String, why: String, fg: Hsla, bg: Hsla| {
            div()
                .id(SharedString::from(id))
                .flex_none()
                .px_1p5()
                .rounded_full()
                .bg(bg)
                .text_color(fg)
                .tooltip(move |w, cx| long_tip(why.clone().into(), w, cx))
                .child(text)
        };
        let idle = span(tree.idle_seconds);
        v_flex()
            .id(SharedString::from(format!("running-tree-{id}")))
            .min_w_0()
            .gap_1()
            .rounded_lg()
            .px_2()
            .py_1p5()
            .border_1()
            .map(|el| {
                if asking {
                    el.bg(theme::rose(500).opacity(0.1))
                        .border_color(theme::rose(400).opacity(0.3))
                } else {
                    el.border_color(transparent_black())
                        .hover(|s| s.bg(theme::surface_raised()))
                }
            })
            .child(
                h_flex()
                    .min_w_0()
                    .items_center()
                    .gap_2()
                    .child(chevron)
                    .child(
                        div()
                            .id(SharedString::from(format!("running-label-{id}")))
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_size(theme::text::UI)
                            .map(|el| {
                                if tree.protected {
                                    el.text_color(theme::muted())
                                } else {
                                    el.font_family(theme::mono()).text_color(theme::primary())
                                }
                            })
                            .tooltip(move |w, cx| long_tip(command.clone(), w, cx))
                            .child(label),
                    )
                    .child(
                        div()
                            .flex_none()
                            .font_family(theme::mono())
                            .font_features(theme::tabular_features())
                            .text_size(theme::text::UI)
                            .text_color(if tree.heavy {
                                theme::amber(200)
                            } else {
                                theme::secondary()
                            })
                            .child(human_bytes(tree.memory)),
                    )
                    .child(
                        h_flex()
                            .flex_none()
                            .justify_end()
                            .gap_1p5()
                            .when(!single, |el| el.min_w(px(88.)))
                            .child(action),
                    ),
            )
            .child(
                h_flex()
                    .min_w_0()
                    .flex_wrap()
                    .items_center()
                    .gap_x_2()
                    .gap_y_1()
                    .pl_6()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::muted())
                    .child(
                        div()
                            .flex_none()
                            .w_16()
                            .h_1()
                            .rounded_full()
                            .overflow_hidden()
                            .bg(theme::surface_strong())
                            .child(div().h_full().w(relative(fill)).rounded_full().bg(bar)),
                    )
                    .child(div().min_w_0().truncate().child(facts.join(" · ")))
                    .when(tree.heavy, |el| {
                        el.child(chip(
                            format!("running-heavy-{id}"),
                            t("running.heavy"),
                            tv(
                                "running.heavyTip",
                                &[("memory", human_bytes(tree.memory).into())],
                            ),
                            theme::amber(200),
                            theme::amber(400).opacity(0.1),
                        ))
                    })
                    .when(tree.idle, |el| {
                        el.child(chip(
                            format!("running-idle-{id}"),
                            tv("running.idle", &[("span", idle.clone().into())]),
                            tv("running.idleTip", &[("span", idle.clone().into())]),
                            theme::sky(300),
                            theme::sky(400).opacity(0.1),
                        ))
                    }),
            )
            .children(confirm)
            .when(open, |el| {
                el.child(
                    v_flex()
                        .min_w_0()
                        .gap_0p5()
                        .pl_6()
                        .children(tree.members.iter().map(|m| {
                            h_flex()
                                .min_w_0()
                                .items_baseline()
                                .gap_2()
                                .font_family(theme::mono())
                                .font_features(theme::tabular_features())
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::faint())
                                .child(div().flex_none().w_12().child(m.pid.to_string()))
                                .child(div().flex_1().min_w_0().truncate().child(m.command.clone()))
                                .child(div().flex_none().child(human_bytes(m.memory)))
                        })),
                )
            })
            .into_any_element()
    }
}

/// totals is what an agent's trees hold, and in how many processes.
fn totals(a: &AgentProcesses) -> String {
    tv(
        "running.agentTotals",
        &[
            ("memory", human_bytes(a.memory).into()),
            ("count", a.processes.into()),
        ],
    )
}

#[cfg(test)]
mod tests {
    // Not super::*: gpui's own `test` attribute would come with it.
    use super::{
        agent_without_tree, cpu_percent, rows, running_count, span, split_trees, up_for,
        without_tree,
    };
    use agentbox_api::{AgentProcesses, ProcessList, ProcessTree};

    fn tree(pid: i64, kind: &str, mb: i64) -> ProcessTree {
        ProcessTree {
            pid,
            kind: kind.into(),
            label: format!("t{pid}"),
            memory: mb << 20,
            processes: 1,
            cpu: 1.,
            protected: kind != "agent",
            ..Default::default()
        }
    }

    fn pids(trees: &[&ProcessTree]) -> Vec<i64> {
        trees.iter().map(|t| t.pid).collect()
    }

    #[test]
    fn the_machines_tree_comes_last_and_small_ones_fold_away() {
        let mut idle_small = tree(5, "agent", 4);
        idle_small.idle = true;
        let trees = vec![
            tree(1, "system", 900),
            tree(2, "agent", 600),
            tree(3, "tool", 10),
            tree(4, "agent", 8),
            idle_small,
        ];
        let (shown, small) = split_trees(&trees);
        // The tool's is protected, and a flagged one is never small.
        assert_eq!(pids(&shown), vec![2, 3, 5, 1]);
        assert_eq!(pids(&small), vec![4]);
        assert_eq!(pids(&rows(&shown, &small, false)), vec![2, 3, 5, 1]);
        assert_eq!(pids(&rows(&shown, &small, true)), vec![2, 3, 5, 4, 1]);
    }

    #[test]
    fn spans_read_as_the_view_says_them() {
        assert_eq!(span(-3), "0s");
        assert_eq!(span(45), "45s");
        assert_eq!(span(12 * 60 + 5), "12m");
        assert_eq!(span(2 * 3600 + 14 * 60), "2h 14m");
        assert_eq!(span(5 * 3600), "5h");
        assert_eq!(span(3 * 86400 + 5), "3d");
        let now = chrono::DateTime::parse_from_rfc3339("2026-11-20T21:00:00Z")
            .unwrap()
            .timestamp_millis();
        assert_eq!(up_for("2026-11-20T20:13:00Z", now).as_deref(), Some("47m"));
        assert_eq!(up_for("", now), None);
        assert_eq!(up_for("0001-01-01T00:00:00Z", now), None);
    }

    #[test]
    fn cpu_has_a_decimal_only_under_ten() {
        // In whichever language the tests run: pt-BR writes 0,4.
        let cpu = |v| cpu_percent(v).replace(',', ".");
        assert_eq!(cpu(0.44), "0.4");
        assert_eq!(cpu(3.0), "3");
        assert_eq!(cpu(186.4), "186");
    }

    #[test]
    fn the_badge_counts_the_trees_worth_a_row_that_can_be_killed() {
        let mut flagged = tree(4, "agent", 3);
        flagged.idle = true;
        let agent = |name: &str, trees: Vec<ProcessTree>| AgentProcesses {
            r#ref: format!("demo/{name}"),
            running: true,
            trees,
            ..Default::default()
        };
        let a = agent(
            "a",
            vec![
                tree(1, "agent", 900),  // a dev server
                tree(2, "tool", 600),   // the AI tool's: never counted
                tree(3, "agent", 4),    // a small one, folded away
                flagged,                // small but flagged: listed, so counted
                tree(5, "system", 300), // the machine's
            ],
        );
        let mut off = agent("c", vec![tree(7, "agent", 200)]);
        off.running = false;
        let mut broken = agent("d", vec![tree(8, "agent", 200)]);
        broken.error = "boom".into();
        let mut list = ProcessList {
            agents: vec![a, agent("b", vec![tree(6, "agent", 200)]), off, broken],
            ..Default::default()
        };
        assert_eq!(running_count(&list), 3);
        // A kill takes it off the badge before the next reading says so.
        without_tree(&mut list, "demo/a", 1);
        assert_eq!(running_count(&list), 2);
        assert_eq!(running_count(&ProcessList::default()), 0);
    }

    #[test]
    fn a_killed_tree_leaves_every_list_with_its_totals() {
        let a = AgentProcesses {
            r#ref: "demo/agent-1".into(),
            running: true,
            trees: vec![tree(2, "agent", 600), tree(3, "tool", 100)],
            memory: 700 << 20,
            killable: 600 << 20,
            cpu: 2.,
            processes: 2,
            ..Default::default()
        };
        let other = AgentProcesses {
            r#ref: "demo/agent-2".into(),
            ..a.clone()
        };
        let mut list = ProcessList {
            agents: vec![a, other],
            ..Default::default()
        };
        without_tree(&mut list, "demo/agent-1", 2);
        let a = &list.agents[0];
        assert_eq!(pids(&a.trees.iter().collect::<Vec<_>>()), vec![3]);
        assert_eq!((a.memory, a.killable, a.processes), (100 << 20, 0, 1));
        assert_eq!(list.agents[1].trees.len(), 2);
        // A tree already gone changes nothing.
        let mut again = list.agents[0].clone();
        agent_without_tree(&mut again, 2);
        assert_eq!(again, list.agents[0]);
    }
}
