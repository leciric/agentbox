//! Pull requests (PullRequestsPanel.tsx, PullRequestModal.tsx, PrChip.tsx,
//! PrNumberLink.tsx, LabelRemoveButton.tsx) and v2's project header
//! (components/v2/ProjectHeader.tsx). A project's pull requests are the rail's
//! second tab beside its lead's chat — every one GitHub has, not only those
//! with an agent behind them — filtered Open, Closed & merged, All and Mine,
//! stacked ones under the one they build on, each with its labels (put on and
//! taken off in place), checks, and Merge behind a confirmation naming the
//! branch it merges into. A click opens one over the window: what it is,
//! its description and checks, and its files as a tree or a list of diffs.
//! The header over the chat is the project's breadcrumb, its pull requests'
//! counts, its settings and its menu.
use std::collections::{HashMap, HashSet};
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::{
    EditLabelsRequest, GIT_HUB_BAD_TOKEN, GIT_HUB_NO_ACCESS, GIT_HUB_NO_ACCOUNT,
    GIT_HUB_RATE_LIMITED, GitHubError, Label, MergePullRequestRequest, ProjectLabels,
    ProjectPullRequests, PullCheck, PullRequest, PullRequestDetail,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::menu::{DropdownMenu as _, PopupMenuItem};
use gpui_component::text::TextView;
use gpui_component::tooltip::Tooltip;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::merge;
use super::settings::defaults::time_ago;
use super::settings::kit::plain;
use crate::diff::DiffView;
use crate::{motion, prefs, theme, toasts};

/// Which of a project's pull requests the list shows (lib/pulls.ts PullsState).
#[derive(Clone, Copy, PartialEq)]
pub(super) enum Filter {
    Open,
    Closed,
    All,
}

impl Filter {
    fn keeps(self, state: &str) -> bool {
        match self {
            Filter::Open => state == "open",
            Filter::Closed => state != "open",
            Filter::All => true,
        }
    }
}

/// The panel's tabs beside a project's lead (RightPanel.tsx LeadSwitch).
#[derive(Clone, Copy, PartialEq, Debug)]
pub(super) enum RailTab {
    Agents,
    Pulls,
    Issues,
    Files,
    Evidence,
    Schedules,
}

#[derive(Clone, Copy, PartialEq)]
enum DetailTab {
    Description,
    Files,
}

/// A label edit GitHub hasn't answered yet, shown as though it had landed.
struct LabelEdit {
    id: u64,
    number: i64,
    add: Vec<Label>,
    remove: Vec<String>,
}

/// with_edits is a pull request's labels with the pending edits over them.
fn with_edits(labels: &[Label], number: i64, edits: &[LabelEdit]) -> Vec<Label> {
    let mut out = labels.to_vec();
    for e in edits.iter().filter(|e| e.number == number) {
        out.retain(|l| !e.remove.contains(&l.name));
        for l in &e.add {
            if !out.iter().any(|o| o.name == l.name) {
                out.push(l.clone());
            }
        }
    }
    out
}

/// github_image_hosts are where the daemon reads a description's pictures
/// from (lib/pulls.ts isGitHubImage); a picture anywhere else isn't loaded.
fn is_github_image(src: &str) -> bool {
    let Some(rest) = src.strip_prefix("https://") else {
        return false;
    };
    let host = rest
        .split(['/', '?', '#'])
        .next()
        .unwrap_or("")
        .to_lowercase();
    [
        "github.com",
        "raw.githubusercontent.com",
        "private-user-images.githubusercontent.com",
        "user-images.githubusercontent.com",
        "camo.githubusercontent.com",
        "avatars.githubusercontent.com",
        "objects.githubusercontent.com",
    ]
    .contains(&host.as_str())
}

/// image_sources are the pictures a description shows: Markdown's
/// ![alt](src) and HTML's <img src="…">.
fn image_sources(body: &str) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    let mut push = |s: &str| {
        let s = s.split_whitespace().next().unwrap_or("").to_string();
        if !s.is_empty() && !out.contains(&s) {
            out.push(s);
        }
    };
    let mut rest = body;
    while let Some(i) = rest.find("](") {
        let after = &rest[i + 2..];
        if rest[..i].contains("![")
            && let Some(end) = after.find(')')
        {
            push(&after[..end]);
        }
        rest = after;
    }
    let mut rest = body;
    while let Some(i) = rest.find("src=") {
        let after = &rest[i + 4..];
        let q = after.chars().next().unwrap_or(' ');
        if (q == '"' || q == '\'')
            && let Some(end) = after[1..].find(q)
        {
            push(&after[1..1 + end]);
        }
        rest = after;
    }
    out
}

/// enc_query escapes a query value.
fn enc_query(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

/// One pull request open over the window.
struct Viewing {
    number: i64,
    detail: Option<Result<PullRequestDetail, String>>,
    /// Its description's GitHub pictures, read through the daemon
    /// (GET …/pulls/image?src=), by their address.
    images: std::sync::Arc<std::sync::Mutex<HashMap<String, std::path::PathBuf>>>,
    tab: DetailTab,
    files: Entity<DiffView>,
}

pub(super) struct Pulls {
    /// The project these are of.
    project: String,
    data: Option<Result<ProjectPullRequests, String>>,
    filter: Filter,
    refreshing: bool,
    pub(super) tab: RailTab,
    edits: Vec<LabelEdit>,
    next_edit: u64,
    running: HashSet<i64>,
    failed: HashMap<i64, String>,
    pub(super) confirm: Option<merge::Confirm>,
    viewing: Option<Viewing>,
    /// The label picker, open on this pull request.
    pub(super) picker: Option<i64>,
    /// Where it was opened: a row of the list, or the one open over the window.
    picker_at: &'static str,
    labels: Option<Result<Vec<Label>, String>>,
    pub(super) search: Entity<InputState>,
    focus: FocusHandle,
    /// The project's path was copied, a moment ago.
    copied: bool,
}

impl Pulls {
    /// open is whether a pull request is open over the window.
    pub(super) fn open(&self) -> bool {
        self.viewing.is_some()
    }

    pub(super) fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let search =
            cx.new(|cx| InputState::new(window, cx).placeholder(t("memory.pulls.labels.search")));
        Self {
            project: String::new(),
            data: None,
            filter: Filter::Open,
            refreshing: false,
            tab: RailTab::Agents,
            edits: Vec::new(),
            next_edit: 0,
            running: HashSet::new(),
            failed: HashMap::new(),
            confirm: None,
            viewing: None,
            picker: None,
            picker_at: "",
            labels: None,
            search,
            focus: cx.focus_handle(),
            copied: false,
        }
    }

    fn list(&self) -> Option<&ProjectPullRequests> {
        self.data.as_ref().and_then(|d| d.as_ref().ok())
    }

    fn pr(&self, number: i64) -> Option<&PullRequest> {
        self.list()?
            .pull_requests
            .iter()
            .find(|p| p.number == number)
    }

    fn set_state(&mut self, number: i64, state: &str) {
        if let Some(Ok(d)) = &mut self.data
            && let Some(pr) = d.pull_requests.iter_mut().find(|p| p.number == number)
        {
            pr.state = state.into();
        }
        if let Some(v) = &mut self.viewing
            && v.number == number
            && let Some(Ok(d)) = &mut v.detail
        {
            d.state = state.into();
        }
    }
}

fn mine_key(project: &str) -> String {
    format!("pullsMine.{project}")
}

/// stack_order puts a pull request built on another's branch right after
/// it, a level deeper (lib/stacks.ts stackOrder, stackHead).
fn stack_order(prs: &[PullRequest]) -> Vec<(usize, usize, Option<i64>, Vec<i64>)> {
    fn head(p: &PullRequest) -> Option<&str> {
        (!p.fork).then_some(p.head_branch.as_str())
    }
    let mut by_head: HashMap<&str, usize> = HashMap::new();
    for (i, p) in prs.iter().enumerate() {
        if let Some(h) = head(p) {
            by_head.entry(h).or_insert(i);
        }
    }
    let parent: Vec<Option<usize>> = prs
        .iter()
        .enumerate()
        .map(|(i, p)| {
            by_head
                .get(p.base_branch.as_str())
                .copied()
                .filter(|&j| j != i)
        })
        .collect();
    let children =
        |i: usize| -> Vec<usize> { (0..prs.len()).filter(|&j| parent[j] == Some(i)).collect() };
    let mut out = Vec::new();
    let mut placed = vec![false; prs.len()];
    fn place(
        i: usize,
        depth: usize,
        prs: &[PullRequest],
        parent: &[Option<usize>],
        children: &dyn Fn(usize) -> Vec<usize>,
        placed: &mut Vec<bool>,
        out: &mut Vec<(usize, usize, Option<i64>, Vec<i64>)>,
    ) {
        if placed[i] {
            return;
        }
        placed[i] = true;
        let kids = children(i);
        out.push((
            i,
            depth,
            if depth > 0 {
                parent[i].map(|p| prs[p].number)
            } else {
                None
            },
            kids.iter().map(|&k| prs[k].number).collect(),
        ));
        for k in kids {
            place(k, depth + 1, prs, parent, children, placed, out);
        }
    }
    for i in 0..prs.len() {
        if parent[i].is_none() {
            place(i, 0, prs, &parent, &children, &mut placed, &mut out);
        }
    }
    for i in 0..prs.len() {
        place(i, 0, prs, &parent, &children, &mut placed, &mut out);
    }
    out
}

/// merge_block is why an open pull request can't be merged (lib/review.ts
/// mergeBlockReason), or None when it can; merged and closed ones aren't
/// offered at all.
pub(crate) fn merge_block(pr: &PullRequest, list: Option<&ProjectPullRequests>) -> Option<String> {
    if pr.state != "open" {
        return None;
    }
    Some(if pr.draft {
        t("review.mergeBlocked.draft")
    } else if list.is_some_and(|l| l.can_merge_known && !l.can_merge) {
        t("review.mergeBlocked.noPermission")
    } else if pr.conflict {
        tv(
            "review.mergeBlocked.conflict",
            &[("base", (&pr.base_branch).into())],
        )
    } else if pr.checks == "failing" {
        t("review.mergeBlocked.failing")
    } else {
        return None;
    })
}

/// github_sentence says why the pull requests couldn't be read (utils.ts
/// githubErrorSentence).
pub(super) fn github_sentence(e: &GitHubError) -> String {
    let account = if e.login.is_empty() {
        e.account.clone()
    } else {
        format!("{} ({})", e.account, e.login)
    };
    match e.kind.as_str() {
        GIT_HUB_NO_ACCESS => tv(
            "shell.github.noAccess",
            &[("account", (&account).into()), ("repo", (&e.repo).into())],
        ),
        GIT_HUB_BAD_TOKEN => tv("shell.github.badToken", &[("account", (&account).into())]),
        GIT_HUB_RATE_LIMITED => tv(
            "shell.github.rateLimitedSoon",
            &[("account", (&account).into())],
        ),
        GIT_HUB_NO_ACCOUNT if !e.account.is_empty() => tv(
            "shell.github.accountGone",
            &[("account", (&e.account).into())],
        ),
        GIT_HUB_NO_ACCOUNT => tv(
            "shell.github.noAccount",
            &[(
                "repo",
                (&if e.repo.is_empty() {
                    t("shell.github.thisRepository")
                } else {
                    e.repo.clone()
                })
                    .into(),
            )],
        ),
        _ => tv("shell.github.other", &[("message", (&e.message).into())]),
    }
}

/// github_fix is what to do about it (GitHubErrorFix), its two fixes as
/// links in the sentence: <another> picks another account for the project
/// (its settings), <setup> adds or saves one in Settings' accounts.
pub(super) fn github_fix(
    id: &'static str,
    e: &GitHubError,
    project: String,
    cx: &mut Context<AppView>,
) -> Option<AnyElement> {
    let key = match e.kind.as_str() {
        GIT_HUB_NO_ACCESS => "memory.pulls.fix.another",
        GIT_HUB_NO_ACCOUNT if !e.account.is_empty() => "memory.pulls.fix.another",
        GIT_HUB_NO_ACCOUNT => "memory.pulls.fix.add",
        GIT_HUB_BAD_TOKEN => "memory.pulls.fix.badToken",
        _ => return None,
    };
    let text = t(key);
    let mut row = h_flex()
        .flex_wrap()
        .px_1()
        .text_xs()
        .text_color(theme::muted());
    let mut rest = text.as_str();
    while !rest.is_empty() {
        let Some(open) = rest.find('<') else {
            row = row.child(rest.to_string());
            break;
        };
        if open > 0 {
            row = row.child(rest[..open].to_string());
        }
        let after = &rest[open + 1..];
        let Some(close) = after.find('>') else {
            row = row.child(rest[open..].to_string());
            break;
        };
        let tag = &after[..close];
        let end_tag = format!("</{tag}>");
        let inner = &after[close + 1..];
        let Some(end) = inner.find(&end_tag) else {
            row = row.child(inner.to_string());
            break;
        };
        let label = inner[..end].to_string();
        let target = if tag == "another" {
            format!("project:{project}")
        } else {
            "accounts".to_string()
        };
        row = row.child(
            link(SharedString::from(format!("{id}-{tag}")), label).on_click(cx.listener(
                move |this, _: &ClickEvent, window, cx| {
                    this.open_settings_at(target.clone(), window, cx)
                },
            )),
        );
        rest = &inner[end + end_tag.len()..];
    }
    Some(row.into_any_element())
}

/// hex is a label's colour, grey when GitHub sent something else (labelHex).
pub(super) fn hex(color: &str) -> Hsla {
    match u32::from_str_radix(color, 16) {
        Ok(v) if color.len() == 6 => rgb(v).into(),
        _ => theme::label_grey(),
    }
}

/// mix is a, with t of b in it.
pub(super) fn mix(a: Hsla, b: Hsla, t: f32) -> Hsla {
    let (a, b) = (Rgba::from(a), Rgba::from(b));
    Rgba {
        r: a.r + (b.r - a.r) * t,
        g: a.g + (b.g - a.g) * t,
        b: a.b + (b.b - a.b) * t,
        a: 1.,
    }
    .into()
}

/// Tone is a Badge's variant (Electron's ui/badge.tsx).
#[derive(Clone, Copy)]
pub(super) enum Tone {
    Default,
    Success,
    Warning,
    Danger,
    Info,
}

/// pill is Electron's Badge: a word in a ringed pill, tinted by its tone,
/// with an icon before it when `icon` names one.
pub(super) fn pill(text: impl Into<SharedString>, tone: Tone, icon: Option<&str>) -> Div {
    let (bg, fg, ring) = match tone {
        Tone::Default => (theme::surface(), theme::tertiary(), theme::line_strong()),
        Tone::Success => (
            theme::emerald(400).opacity(0.1),
            theme::emerald(300),
            theme::emerald(400).opacity(0.25),
        ),
        Tone::Warning => (
            theme::amber(400).opacity(0.1),
            theme::amber(200),
            theme::amber(400).opacity(0.25),
        ),
        Tone::Danger => (
            theme::rose(400).opacity(0.1),
            theme::rose(300),
            theme::rose(400).opacity(0.25),
        ),
        Tone::Info => (
            theme::sky(400).opacity(0.1),
            theme::sky(300),
            theme::sky(400).opacity(0.25),
        ),
    };
    h_flex()
        .flex_none()
        .gap_1p5()
        .px_2()
        .py(px(1.))
        .rounded_full()
        .border_1()
        .border_color(ring)
        .bg(bg)
        .text_size(theme::text::CAPTION)
        .font_weight(FontWeight::MEDIUM)
        .text_color(fg)
        .whitespace_nowrap()
        .when_some(icon, |el, i| el.child(crate::icons::ic(i).size(px(12.))))
        .child(text.into())
}

pub(super) fn state_badge(pr_state: &str, draft: bool) -> AnyElement {
    if draft {
        return pill(t("memory.pulls.draft"), Tone::Default, None).into_any_element();
    }
    let (key, tone) = match pr_state {
        "open" => ("memory.pulls.state.open", Tone::Info),
        "merged" => ("memory.pulls.state.merged", Tone::Success),
        "closed" => ("memory.pulls.state.closed", Tone::Default),
        s => return pill(s.to_string(), Tone::Default, None).into_any_element(),
    };
    pill(t(key), tone, None).into_any_element()
}

fn checks_badge(checks: &str) -> Option<AnyElement> {
    let (key, tone) = match checks {
        "" => return None,
        "passing" => ("memory.pulls.checks.passing", Tone::Success),
        "failing" => ("memory.pulls.checks.failing", Tone::Danger),
        "pending" => ("memory.pulls.checks.pending", Tone::Warning),
        s => {
            return Some(
                pill(
                    tv("memory.pulls.checks", &[("state", s.into())]),
                    Tone::Default,
                    None,
                )
                .into_any_element(),
            );
        }
    };
    Some(
        pill(
            tv("memory.pulls.checks", &[("state", (&t(key)).into())]),
            tone,
            None,
        )
        .into_any_element(),
    )
}

/// check_state is a check run's icon, colour and word.
fn check_state(run: &PullCheck) -> (&'static str, Hsla, &'static str) {
    if run.status != "completed" {
        return if run.status == "queued" {
            ("circle", theme::amber(400), "pulls.detail.check.queued")
        } else {
            (
                "loader-circle",
                theme::amber(400),
                "pulls.detail.check.running",
            )
        };
    }
    match run.conclusion.as_str() {
        "success" => (
            "circle-check",
            theme::emerald(400),
            "pulls.detail.check.success",
        ),
        "failure" => ("circle-x", theme::rose(400), "pulls.detail.check.failure"),
        "timed_out" => ("circle-x", theme::rose(400), "pulls.detail.check.timedOut"),
        "cancelled" => ("circle-x", theme::subtle(), "pulls.detail.check.cancelled"),
        "action_required" => (
            "circle-x",
            theme::amber(400),
            "pulls.detail.check.actionRequired",
        ),
        "skipped" => ("circle", theme::subtle(), "pulls.detail.check.skipped"),
        _ => ("circle", theme::subtle(), "pulls.detail.check.neutral"),
    }
}

/// pr_chip is a pull request as a small pill (PrChip): its number in the
/// colour of its state, and its checks' mark; a click opens it on GitHub.
pub(super) fn pr_chip(id: impl Into<ElementId>, pr: &PullRequest) -> AnyElement {
    // PrChip's tone: its fill, text, ring and hover fill, and its icon.
    let hue = |h: Hsla, fg: Hsla, ring: f32| (h.opacity(0.1), fg, h.opacity(ring), h.opacity(0.2));
    let ((bg, fg, ring, hover), icon) = match pr.state.as_str() {
        "merged" => (
            hue(theme::violet(400), theme::violet(300), 0.25),
            "git-merge",
        ),
        "closed" => (
            hue(theme::rose(400), theme::rose(300), 0.25),
            "git-pull-request",
        ),
        _ if pr.draft => (
            (
                theme::surface_raised(),
                theme::muted(),
                theme::line_strong(),
                theme::surface_vivid(),
            ),
            "git-pull-request",
        ),
        _ if pr.checks == "failing" => (
            hue(theme::rose(400), theme::rose(300), 0.3),
            "git-pull-request",
        ),
        _ => (
            hue(theme::emerald(400), theme::emerald(300), 0.25),
            "git-pull-request",
        ),
    };
    let draft = pr.draft && pr.state == "open";
    let (mark, mark_color) = match pr.checks.as_str() {
        "passing" => ("✓", theme::emerald(300)),
        "failing" => ("✕", theme::rose(300)),
        "pending" => ("•", theme::amber(300)),
        _ => ("", fg),
    };
    let state = match pr.state.as_str() {
        _ if pr.draft => t("agent.pr.draft"),
        "merged" => t("agent.pr.merged"),
        "closed" => t("agent.pr.closed"),
        _ => t("agent.pr.open"),
    };
    let mut tip = vec![format!("#{} {state}", pr.number)];
    if !pr.checks.is_empty() {
        let c = t(&format!("agent.pr.{}", pr.checks));
        tip.push(tv("agent.pr.checks", &[("checks", (&c).into())]));
    }
    if pr.conflict {
        tip.push(t("agent.pr.conflictsBase"));
    }
    tip.push(pr.title.clone());
    let tip: SharedString = tip.join(" · ").into();
    let url = pr.url.clone();
    h_flex()
        .id(id)
        .flex_none()
        .gap_1()
        .h(px(18.))
        .px_1p5()
        .rounded_full()
        .bg(bg)
        .border_1()
        .border_color(ring)
        .text_size(theme::text::CAPTION)
        .font_family(crate::theme::mono())
        .text_color(fg)
        .tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
        .when(!url.is_empty(), |el| {
            el.cursor_pointer()
                .hover(move |s| {
                    let s = s.bg(hover);
                    if draft {
                        s.text_color(theme::primary())
                    } else {
                        s
                    }
                })
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .on_click(move |_, _, cx| {
                    cx.stop_propagation();
                    cx.open_url(&url)
                })
        })
        .child(crate::icons::ic(icon).size(px(12.)).flex_none())
        .child(format!("#{}", pr.number))
        .when(!mark.is_empty(), |el| {
            el.child(div().text_color(mark_color).child(mark))
        })
        .when(pr.conflict, |el| {
            el.child(div().text_color(theme::rose(300)).child("⚠"))
        })
        .into_any_element()
}

/// number_link is a pull request's number as a link to it on GitHub
/// (PrNumberLink).
fn number_link(id: impl Into<ElementId>, pr: &PullRequest) -> AnyElement {
    let url = pr.url.clone();
    div()
        .id(id)
        .flex_none()
        .font_weight(FontWeight::NORMAL)
        .text_color(theme::subtle())
        .cursor_pointer()
        .hover(|s| s.text_color(theme::brand(300)))
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
        .on_click(move |_, _, cx| {
            cx.stop_propagation();
            cx.open_url(&url)
        })
        .child(format!("#{}", pr.number))
        .into_any_element()
}

pub(super) fn link(id: impl Into<ElementId>, text: impl Into<SharedString>) -> Stateful<Div> {
    div()
        .id(id)
        .flex_none()
        .text_color(theme::brand(300))
        .font_weight(FontWeight::MEDIUM)
        .cursor_pointer()
        .hover(|s| s.underline())
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
        .child(text.into())
}

/// notice is Electron's Notice: a tinted, ringed note with its tone's icon,
/// rose for a failure, amber for a warning, else sky's information.
pub(super) fn notice(text: impl Into<SharedString>, color: Hsla) -> Div {
    let (icon, fg, mark) = if color == theme::error() {
        ("circle-alert", theme::rose(100), theme::rose(300))
    } else if color == theme::starting() {
        ("triangle-alert", theme::amber(50), theme::amber(300))
    } else {
        ("info", theme::sky(50), theme::sky(300))
    };
    h_flex()
        .items_start()
        .gap_2p5()
        .px(px(14.))
        .py(px(10.))
        .rounded_xl()
        .border_1()
        .border_color(color.opacity(0.25))
        .bg(color.opacity(0.08))
        .text_size(theme::text::UI)
        .line_height(relative(1.6))
        .text_color(fg)
        .child(
            div()
                .flex_none()
                .pt(px(2.))
                .text_color(mark)
                .child(crate::icons::ic(icon).size(px(16.))),
        )
        .child(div().flex_1().min_w_0().child(text.into()))
}

/// merge_blocked is why Merge is greyed out, beside it (MergeBlocked).
fn merge_blocked(reason: String) -> Stateful<Div> {
    let tip: SharedString = reason.clone().into();
    div()
        .id(SharedString::from(format!("merge-blocked-{reason}")))
        .min_w_0()
        .truncate()
        .text_size(theme::text::CAPTION)
        .text_color(theme::amber(200))
        .tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
        .child(reason)
}

/// empty_state is Electron's EmptyState, on a panel: a title and a line
/// under it, centred.
pub(super) fn empty_state(title: String, body: String) -> AnyElement {
    empty_state_with(None, title, body)
}

/// empty_state_with is empty_state with its icon in a lit tile over it.
pub(super) fn empty_state_with(icon: Option<&str>, title: String, body: String) -> AnyElement {
    let id = SharedString::from(format!("empty-{title}"));
    div()
        .rounded(theme::radius::XXL)
        .border_1()
        .border_color(theme::line())
        .bg(theme::linear_panel())
        .shadow(theme::shadow_card())
        .child(motion::enter(
            v_flex()
                .items_center()
                .gap_3()
                .p_8()
                .when_some(icon, |el, i| {
                    el.child(
                        div()
                            .mb_2()
                            .size(px(56.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded(theme::radius::XXL)
                            .border_1()
                            .border_color(theme::line_strong())
                            .bg(theme::overlay())
                            .shadow(theme::glow(theme::brand(400), 1.2))
                            .text_color(theme::brand(300))
                            .child(crate::icons::ic(i).size(px(24.))),
                    )
                })
                .child(
                    div()
                        .text_size(theme::text::HEADING)
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::title())
                        .text_center()
                        .child(title),
                )
                .child(
                    div()
                        .max_w(px(448.))
                        .text_size(theme::text::BODY)
                        .line_height(relative(1.6))
                        .text_center()
                        .text_color(theme::muted())
                        .child(body),
                ),
            id,
            0,
        ))
        .into_any_element()
}

/// chip is Electron's FilterChip: a word and its count, lit while it's on.
pub(super) fn chip(id: &str, label: String, count: Option<usize>, on: bool) -> Stateful<Div> {
    h_flex()
        .id(SharedString::from(id.to_string()))
        .gap_1p5()
        .h(px(28.))
        .px_2p5()
        .rounded_lg()
        .text_size(theme::text::UI)
        .map(|el| {
            if on {
                el.bg(theme::surface_strong()).text_color(theme::title())
            } else {
                el.text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|s| s.text_color(theme::primary()).bg(theme::surface()))
            }
        })
        .child(label)
        .when_some(count, |el, n| {
            el.child(div().text_color(theme::subtle()).child(n.to_string()))
        })
}

impl AppView {
    /// load_pulls reads the open project's pull requests; fresh has the
    /// daemon read GitHub now, whatever its cache's age.
    pub(super) fn load_pulls(&mut self, fresh: bool, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone().filter(|_| !self.is_home()) else {
            return;
        };
        if self.pulls.project != project {
            self.pulls.project = project.clone();
            self.pulls.data = None;
            self.pulls.edits.clear();
            self.pulls.failed.clear();
            self.pulls.labels = None;
            self.pulls.picker = None;
        }
        if fresh {
            self.pulls.refreshing = true;
        }
        let client = self.client.clone();
        let path = format!(
            "/v1/projects/{project}/pulls{}",
            if fresh { "?refresh=1" } else { "" }
        );
        cx.spawn(async move |this, cx| {
            let res = client.get::<ProjectPullRequests>(&path).await;
            let again = res.as_ref().is_ok_and(|d| d.refreshing);
            this.update(cx, |this, cx| {
                if this.pulls.project != project {
                    return;
                }
                this.pulls.refreshing = false;
                match res {
                    Ok(d) => this.pulls.data = Some(Ok(d)),
                    // What was read stays when a re-read fails.
                    Err(e) if this.pulls.data.is_none() => {
                        this.pulls.data = Some(Err(e.to_string()))
                    }
                    Err(_) => {}
                }
                cx.notify();
            })
            .ok();
            // While GitHub is being read, ask again every few seconds, so
            // "refreshing" never outlives the read by much if its event is missed.
            if again {
                cx.background_executor().timer(Duration::from_secs(4)).await;
                this.update(cx, |this, cx| this.load_pulls(false, cx)).ok();
            }
        })
        .detach();
    }

    /// pull_counts is how many of the project's pull requests are open, and
    /// how many of its agents' work is ready to review.
    pub(super) fn pull_counts(&self) -> (usize, usize) {
        let open = self.pulls.list().map_or(0, |d| {
            d.pull_requests.iter().filter(|p| p.state == "open").count()
        });
        let ready = self.project.as_deref().map_or(0, |p| self.to_review(p));
        (open, ready)
    }

    fn mine(&self) -> bool {
        prefs::get_bool(&mine_key(&self.pulls.project), false)
    }

    /// request_merge asks before merging, naming where it merges.
    fn request_merge(&mut self, number: i64, cx: &mut Context<Self>) {
        let (Some(pr), Some(list)) = (self.pulls.pr(number).cloned(), self.pulls.list()) else {
            return;
        };
        let methods = merge::offered(&list.merge_methods);
        self.pulls.confirm = Some(merge::Confirm {
            project: self.pulls.project.clone(),
            method: merge::default_method(&methods, &self.pulls.project),
            methods,
            pr,
        });
        cx.notify();
    }

    /// ask_merge asks before merging a project's pull request from outside
    /// the list (a finished agent's card): with the project's list as read,
    /// read first when it isn't.
    pub(super) fn ask_merge(&mut self, project: String, number: i64, cx: &mut Context<Self>) {
        if self.pulls.project == project && self.pulls.pr(number).is_some() {
            self.request_merge(number, cx);
            return;
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let Ok(list) = client
                .get::<ProjectPullRequests>(&format!("/v1/projects/{project}/pulls"))
                .await
            else {
                return;
            };
            this.update(cx, |this, cx| {
                let Some(pr) = list.pull_requests.iter().find(|p| p.number == number) else {
                    return;
                };
                let methods = merge::offered(&list.merge_methods);
                this.pulls.confirm = Some(merge::Confirm {
                    method: merge::default_method(&methods, &project),
                    project: project.clone(),
                    methods,
                    pr: pr.clone(),
                });
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// merge_pull merges, shown merged at once and put back if GitHub refuses.
    fn merge_pull(&mut self, cx: &mut Context<Self>) {
        let Some(c) = self.pulls.confirm.take() else {
            return;
        };
        let (number, project) = (c.pr.number, c.project.clone());
        let before = c.pr.state.clone();
        self.pulls.running.insert(number);
        self.pulls.failed.remove(&number);
        self.pulls.set_state(number, "merged");
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, PullRequest>(
                    &format!("/v1/projects/{project}/pulls/{number}/merge"),
                    &MergePullRequestRequest {
                        method: c.method.clone(),
                        confirmed: false,
                    },
                )
                .await;
            this.update(cx, |this, cx| {
                this.pulls.running.remove(&number);
                match res {
                    Ok(pr) => {
                        toasts::Toast::new(
                            toasts::Kind::Success,
                            tv("memory.pulls.merged", &[("number", pr.number.into())]),
                        )
                        .description(pr.title.clone())
                        .show(cx);
                        this.reload_review(cx);
                    }
                    Err(e) => {
                        this.pulls.failed.insert(number, e.to_string());
                        this.pulls.set_state(number, &before);
                    }
                }
                this.load_pulls(false, cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// toggle_label puts a label on a pull request or takes it off, shown at
    /// once; a refusal puts it back and says why.
    fn toggle_label(&mut self, number: i64, label: Label, on: bool, cx: &mut Context<Self>) {
        let id = self.pulls.next_edit;
        self.pulls.next_edit += 1;
        let req = EditLabelsRequest {
            add: if on { vec![label.name.clone()] } else { vec![] },
            remove: if on { vec![] } else { vec![label.name.clone()] },
        };
        self.pulls.edits.push(LabelEdit {
            id,
            number,
            add: if on { vec![label.clone()] } else { vec![] },
            remove: if on { vec![] } else { vec![label.name.clone()] },
        });
        let client = self.client.clone();
        let project = self.pulls.project.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Vec<Label>>(
                    &format!("/v1/projects/{project}/pulls/{number}/labels"),
                    &req,
                )
                .await;
            this.update(cx, |this, cx| {
                this.pulls.edits.retain(|e| e.id != id);
                match res {
                    Ok(now) => {
                        if let Some(Ok(d)) = &mut this.pulls.data
                            && let Some(pr) =
                                d.pull_requests.iter_mut().find(|p| p.number == number)
                        {
                            pr.labels = now.clone();
                        }
                        if let Some(v) = &mut this.pulls.viewing
                            && v.number == number
                            && let Some(Ok(d)) = &mut v.detail
                        {
                            d.labels = now;
                        }
                    }
                    Err(e) => toasts::error(
                        cx,
                        tv(
                            "memory.pulls.labels.failed",
                            &[
                                ("number", number.into()),
                                ("error", (&e.to_string()).into()),
                            ],
                        ),
                    ),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// open_picker opens the label picker on a pull request, reading the
    /// repository's labels the first time.
    fn open_picker(
        &mut self,
        scope: &'static str,
        number: i64,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.pulls.picker = Some(number);
        self.pulls.picker_at = scope;
        self.pulls
            .search
            .update(cx, |s, cx| s.set_value("", window, cx));
        let f = self.pulls.search.read(cx).focus_handle(cx);
        f.focus(window, cx);
        if self.pulls.labels.as_ref().is_none_or(|l| l.is_err()) {
            self.pulls.labels = None;
            let client = self.client.clone();
            let path = format!("/v1/projects/{}/labels", self.pulls.project);
            cx.spawn(async move |this, cx| {
                let res = client.get::<ProjectLabels>(&path).await;
                this.update(cx, |this, cx| {
                    this.pulls.labels = Some(res.map(|l| l.labels).map_err(|e| e.to_string()));
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
        cx.notify();
    }

    /// picker_enter puts on (or takes off) the first label the search finds.
    pub(super) fn picker_enter(&mut self, cx: &mut Context<Self>) {
        let Some(number) = self.pulls.picker else {
            return;
        };
        if let Some(l) = self.picker_matches(cx).into_iter().next() {
            let on = !self.applied(number).iter().any(|a| a.name == l.name);
            self.toggle_label(number, l, on, cx);
        }
    }

    fn picker_matches(&self, cx: &App) -> Vec<Label> {
        let q = self.pulls.search.read(cx).value().trim().to_lowercase();
        let all = match &self.pulls.labels {
            Some(Ok(l)) => l.clone(),
            _ => return Vec::new(),
        };
        if q.is_empty() {
            return all;
        }
        let mut hits: Vec<Label> = all
            .into_iter()
            .filter(|l| {
                l.name.to_lowercase().contains(&q) || l.description.to_lowercase().contains(&q)
            })
            .collect();
        hits.sort_by_key(|l| !l.name.to_lowercase().starts_with(&q));
        hits
    }

    /// applied is a pull request's labels now, pending edits included.
    fn applied(&self, number: i64) -> Vec<Label> {
        let base = match &self.pulls.viewing {
            Some(v) if v.number == number => match &v.detail {
                Some(Ok(d)) => Some(d.labels.clone()),
                _ => None,
            },
            _ => None,
        }
        .or_else(|| self.pulls.pr(number).map(|p| p.labels.clone()))
        .unwrap_or_default();
        with_edits(&base, number, &self.pulls.edits)
    }

    /// open_pull opens one over the window, reading its detail and files.
    pub(super) fn open_pull(&mut self, number: i64, window: &mut Window, cx: &mut Context<Self>) {
        let project = self.pulls.project.clone();
        let client = self.client.clone();
        let files = cx.new(|cx| DiffView::pull(client.clone(), project.clone(), number, cx));
        let images: std::sync::Arc<std::sync::Mutex<HashMap<String, std::path::PathBuf>>> =
            Default::default();
        let found = images.clone();
        self.pulls.viewing = Some(Viewing {
            number,
            images,
            detail: None,
            tab: DetailTab::Description,
            files,
        });
        self.pulls.focus.focus(window, cx);
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<PullRequestDetail>(&format!("/v1/projects/{project}/pulls/{number}"))
                .await;
            let srcs = res
                .as_ref()
                .map(|d| image_sources(&d.body))
                .unwrap_or_default();
            for src in srcs.into_iter().filter(|s| is_github_image(s)) {
                let (client, found, this) = (client.clone(), found.clone(), this.clone());
                let path = format!("/v1/projects/{project}/pulls/image?src={}", enc_query(&src));
                cx.spawn(async move |cx| {
                    let Ok(bytes) = client
                        .raw(agentbox_api::client::Method::GET, &path, None)
                        .await
                    else {
                        return;
                    };
                    use sha2::Digest as _;
                    let name = format!("{:x}", sha2::Sha256::digest(src.as_bytes()));
                    let file =
                        std::env::temp_dir().join(format!("agentbox-pr-image-{}", &name[..16]));
                    if std::fs::write(&file, &bytes).is_ok() {
                        found.lock().unwrap().insert(src, file);
                        this.update(cx, |_, cx| cx.notify()).ok();
                    }
                })
                .detach();
            }
            this.update(cx, |this, cx| {
                if let Some(v) = &mut this.pulls.viewing
                    && v.number == number
                {
                    v.detail = Some(res.map_err(|e| e.to_string()));
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn close_pull(&mut self, cx: &mut Context<Self>) {
        self.pulls.viewing = None;
        self.pulls.picker = None;
        cx.notify();
    }

    fn open_agent_chat(&mut self, agent: String, window: &mut Window, cx: &mut Context<Self>) {
        self.pulls.viewing = None;
        self.open_chat(agent, window, cx);
    }

    /// labels_row is a pull request's labels in GitHub's colours, each with
    /// its ✕ (LabelRemoveButton), and the picker's button.
    fn labels_row(&self, scope: &'static str, number: i64, cx: &mut Context<Self>) -> AnyElement {
        let labels = self.applied(number);
        let open = self.pulls.picker == Some(number) && self.pulls.picker_at == scope;
        let none = labels.is_empty();
        h_flex()
            .flex_wrap()
            .gap_1()
            .min_w_0()
            .children(labels.into_iter().map(|l| {
                let c = hex(&l.color);
                let name = l.name.clone();
                let label = l.clone();
                let tip: SharedString = l.description.clone().into();
                h_flex()
                    .id(SharedString::from(format!(
                        "{scope}-label-{number}-{}",
                        l.name
                    )))
                    .gap_1()
                    .h(px(20.))
                    .pl_2()
                    .pr_1()
                    .rounded_full()
                    .border_1()
                    .border_color(c.opacity(0.45))
                    .bg(c.opacity(0.18))
                    .text_size(px(11.))
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(mix(c, theme::primary(), 0.4))
                    .when(!tip.is_empty(), |el| {
                        el.tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
                    })
                    .child(div().max_w(px(160.)).truncate().child(name.clone()))
                    .child(
                        div()
                            .id(SharedString::from(format!(
                                "{scope}-unlabel-{number}-{name}"
                            )))
                            .px_0p5()
                            .rounded_full()
                            .opacity(0.6)
                            .cursor_pointer()
                            .hover(|s| s.opacity(1.))
                            .tooltip({
                                let tip: SharedString =
                                    tv("memory.pulls.labels.remove", &[("name", (&name).into())])
                                        .into();
                                move |window, cx| Tooltip::new(tip.clone()).build(window, cx)
                            })
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                cx.stop_propagation();
                                this.toggle_label(number, label.clone(), false, cx)
                            }))
                            .child(crate::icons::ic("x").size(px(12.))),
                    )
            }))
            .child(
                v_flex()
                    .child(
                        h_flex()
                            .id(SharedString::from(format!("{scope}-labels-edit-{number}")))
                            .gap_1()
                            .h(px(20.))
                            .px_1p5()
                            .rounded_full()
                            .border_1()
                            .border_dashed()
                            .border_color(if open {
                                theme::line_vivid()
                            } else {
                                theme::line_strong()
                            })
                            .text_size(px(11.))
                            .text_color(if open {
                                theme::primary()
                            } else {
                                theme::subtle()
                            })
                            .cursor_pointer()
                            .hover(|s| {
                                s.border_color(theme::line_vivid())
                                    .text_color(theme::primary())
                            })
                            .tooltip({
                                let tip: SharedString = t(if none {
                                    "memory.pulls.labels.add"
                                } else {
                                    "memory.pulls.labels.edit"
                                })
                                .into();
                                move |window, cx| Tooltip::new(tip.clone()).build(window, cx)
                            })
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                cx.stop_propagation();
                                if this.pulls.picker == Some(number)
                                    && this.pulls.picker_at == scope
                                {
                                    this.pulls.picker = None;
                                    cx.notify();
                                } else {
                                    this.open_picker(scope, number, window, cx)
                                }
                            }))
                            .child("+")
                            .when(none, |el| el.child(t("memory.pulls.labels.add"))),
                    )
                    .when(open, |el| {
                        el.child(deferred(
                            anchored().snap_to_window_with_margin(px(8.)).child(
                                crate::motion::menu_in(
                                    div().child(self.render_picker(number, cx)),
                                    "label-picker-in",
                                ),
                            ),
                        ))
                    }),
            )
            .into_any_element()
    }

    /// render_picker is the repository's labels, searched, each put on or
    /// taken off by a click; it stays open for the next.
    fn render_picker(&self, number: i64, cx: &mut Context<Self>) -> AnyElement {
        let applied = self.applied(number);
        let shown = self.picker_matches(cx);
        let body = match &self.pulls.labels {
            None => h_flex()
                .px_2()
                .py_2()
                .gap_1p5()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(crate::icons::spinner())
                .child(t("memory.pulls.labels.loading"))
                .into_any_element(),
            Some(Err(e)) => div()
                .px_2()
                .py_2()
                .text_size(theme::text::CAPTION)
                .text_color(theme::rose(300))
                .child(tv(
                    "memory.pulls.labels.unavailable",
                    &[("error", e.into())],
                ))
                .into_any_element(),
            Some(Ok(all)) if shown.is_empty() => div()
                .px_2()
                .py_2()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .child(t(if all.is_empty() {
                    "memory.pulls.labels.none"
                } else {
                    "memory.pulls.labels.noMatch"
                }))
                .into_any_element(),
            Some(Ok(_)) => v_flex()
                .children(shown.into_iter().map(|l| {
                    let on = applied.iter().any(|a| a.name == l.name);
                    let label = l.clone();
                    h_flex()
                        .id(SharedString::from(format!("label-option-{}", l.name)))
                        .items_start()
                        .gap_2()
                        .px_2()
                        .py_1p5()
                        .rounded_md()
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface()))
                        .active(|s| s.bg(theme::surface_raised()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.toggle_label(number, label.clone(), !on, cx)
                        }))
                        .child(
                            div()
                                .w(px(14.))
                                .mt(px(2.))
                                .flex_none()
                                .text_color(theme::brand(300))
                                .when(on, |el| el.child(crate::icons::ic("check").size(px(14.)))),
                        )
                        .child(
                            div()
                                .mt(px(4.))
                                .flex_none()
                                .size(px(10.))
                                .rounded_full()
                                .bg(hex(&l.color)),
                        )
                        .child(
                            v_flex()
                                .min_w_0()
                                .child(
                                    div()
                                        .text_size(px(12.5))
                                        .text_color(theme::primary())
                                        .truncate()
                                        .child(l.name.clone()),
                                )
                                .when(!l.description.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .text_size(px(11.))
                                            .text_color(theme::subtle())
                                            .truncate()
                                            .child(l.description.clone()),
                                    )
                                }),
                        )
                }))
                .into_any_element(),
        };
        v_flex()
            .id("label-picker")
            .mt_2()
            .w(px(288.))
            .rounded_xl()
            .bg(theme::overlay())
            .border_1()
            .border_color(theme::line_strong())
            .shadow(theme::shadow_pop())
            .overflow_hidden()
            .occlude()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .on_mouse_down_out(cx.listener(|this, _, _, cx| {
                this.pulls.picker = None;
                cx.notify();
            }))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                if ev.keystroke.key == "escape" {
                    this.pulls.picker = None;
                    cx.stop_propagation();
                    cx.notify();
                }
            }))
            .child(
                div()
                    .p_2()
                    .border_b_1()
                    .border_color(theme::line())
                    .child(Input::new(&self.pulls.search).small()),
            )
            .child(
                div()
                    .id("label-picker-list")
                    .max_h(px(288.))
                    .overflow_y_scroll()
                    .p_1()
                    .child(body),
            )
            .into_any_element()
    }

    /// render_pulls_panel is the project's pull requests, in the rail.
    pub(super) fn render_pulls_panel(&self, cx: &mut Context<Self>) -> AnyElement {
        let wrap = |el: AnyElement| {
            div()
                .id("pulls-panel")
                // size_full, not flex_1: the panel's body sits in a plain
                // (non-flex) div, where flex_1 sizes nothing and the list
                // grows past the window instead of scrolling.
                .size_full()
                .overflow_y_scroll()
                .px_3()
                .py_3()
                .child(el)
                .into_any_element()
        };
        let data = match &self.pulls.data {
            None => {
                return wrap(
                    div()
                        .p_6()
                        .child(crate::motion::skeleton_lines("loading-pulls-1", 4))
                        .into_any_element(),
                );
            }
            Some(Err(_)) => {
                return wrap(
                    div()
                        .p_6()
                        .text_sm()
                        .text_color(theme::subtle())
                        .child(t("memory.pulls.unavailable"))
                        .into_any_element(),
                );
            }
            Some(Ok(d)) => d,
        };
        if data.github.is_empty() && data.github_error.is_none() {
            let (title, body) = if data.no_origin {
                (
                    "memory.pulls.noOrigin.title",
                    plain(&t("memory.pulls.noOrigin.body")),
                )
            } else if !data.non_git_hub_remote.is_empty() {
                (
                    "memory.pulls.nonGitHub.title",
                    plain(&tv(
                        "memory.pulls.nonGitHub.body",
                        &[("remote", (&data.non_git_hub_remote).into())],
                    )),
                )
            } else {
                (
                    "memory.pulls.notConnected.title",
                    t("memory.pulls.notConnected.body"),
                )
            };
            return wrap(empty_state_with(Some("git-pull-request"), t(title), body));
        }
        let project = self.pulls.project.clone();
        let me = data.github_login.clone();
        let mine = self.mine() && !me.is_empty();
        let by_me = |p: &&PullRequest| p.author.eq_ignore_ascii_case(&me);
        let scoped: Vec<&PullRequest> = data
            .pull_requests
            .iter()
            .filter(|p| !mine || by_me(p))
            .collect();
        let count = |f: Filter| scoped.iter().filter(|p| f.keeps(&p.state)).count();
        let filter = self.pulls.filter;
        let visible: Vec<PullRequest> = scoped
            .iter()
            .filter(|p| filter.keeps(&p.state))
            .map(|p| (*p).clone())
            .collect();
        let refreshing = data.refreshing || self.pulls.refreshing;
        let can_offer = |pr: &PullRequest| {
            pr.state == "open" && !pr.draft && (!data.can_merge_known || data.can_merge)
        };

        let mut col = v_flex().gap_3();
        if !data.github.is_empty() {
            let account = if data.github_account.is_empty() {
                None
            } else if me.is_empty() {
                Some(data.github_account.clone())
            } else {
                Some(format!("{me} ({})", data.github_account))
            };
            let p2 = project.clone();
            col = col.child(
                h_flex()
                    .flex_wrap()
                    .gap_x_1p5()
                    .px_1()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(
                        div()
                            .font_family(crate::theme::mono())
                            .text_color(theme::muted())
                            .child(data.github.clone()),
                    )
                    .when_some(account, |el, a| {
                        el.child(t("memory.pulls.as"))
                            .child(link("pulls-account", a).on_click(cx.listener(
                                move |this, _: &ClickEvent, window, cx| {
                                    this.open_settings_at(format!("project:{p2}"), window, cx)
                                },
                            )))
                    }),
            );
        }
        if let Some(e) = &data.github_error {
            let p2 = project.clone();
            col = col.child(
                v_flex()
                    .gap_1()
                    .child(notice(github_sentence(e), theme::starting()))
                    .children(github_fix("pulls-fix", e, p2, cx)),
            );
        }
        if data.can_merge_known && !data.can_merge {
            col = col.child(notice(
                tv(
                    "memory.pulls.cannotMerge",
                    &[("repo", (&data.github).into())],
                ),
                theme::sky(400),
            ));
        }
        if data.pull_requests.is_empty() {
            if data.github_error.is_none() {
                col = col.child(if data.fetched_at.is_some() {
                    empty_state_with(
                        Some("git-pull-request"),
                        t("memory.pulls.none.title"),
                        tv("memory.pulls.none.body", &[("repo", (&data.github).into())]),
                    )
                } else {
                    empty_state_with(
                        Some("git-pull-request"),
                        t("memory.pulls.reading.title"),
                        tv(
                            "memory.pulls.reading.body",
                            &[("repo", (&data.github).into())],
                        ),
                    )
                });
            }
            return wrap(col.into_any_element());
        }

        let pick = |f: Filter| {
            cx.listener(move |this: &mut AppView, _: &ClickEvent, _, cx| {
                this.pulls.filter = f;
                cx.notify();
            })
        };
        let mine_count = data
            .pull_requests
            .iter()
            .filter(|p| p.author.eq_ignore_ascii_case(&me) && filter.keeps(&p.state))
            .count();
        let mine_tip: SharedString =
            tv("memory.pulls.filter.mineTip", &[("login", (&me).into())]).into();
        let refresh_tip: SharedString = t("memory.pulls.refresh").into();
        col = col.child(
            h_flex()
                .flex_wrap()
                .gap_1()
                .child(
                    chip(
                        "pulls-open",
                        t("memory.pulls.filter.open"),
                        Some(count(Filter::Open)),
                        filter == Filter::Open,
                    )
                    .on_click(pick(Filter::Open)),
                )
                .child(
                    chip(
                        "pulls-closed",
                        t("memory.pulls.filter.closed"),
                        Some(count(Filter::Closed)),
                        filter == Filter::Closed,
                    )
                    .on_click(pick(Filter::Closed)),
                )
                .child(
                    chip(
                        "pulls-all",
                        t("memory.pulls.filter.all"),
                        Some(count(Filter::All)),
                        filter == Filter::All,
                    )
                    .on_click(pick(Filter::All)),
                )
                .when(!me.is_empty(), |el| {
                    el.child(div().mx_1().w(px(1.)).h(px(16.)).bg(theme::line_strong()))
                        .child(
                            chip(
                                "pulls-mine",
                                format!("◉ {}", t("memory.pulls.filter.mine")),
                                Some(mine_count),
                                mine,
                            )
                            .tooltip(move |window, cx| {
                                Tooltip::new(mine_tip.clone()).build(window, cx)
                            })
                            .on_click(cx.listener(
                                move |this, _: &ClickEvent, _, cx| {
                                    let key = mine_key(&this.pulls.project);
                                    prefs::set_bool(&key, !mine);
                                    cx.notify();
                                },
                            )),
                        )
                })
                .child(div().flex_1())
                .child(
                    h_flex()
                        .gap_1p5()
                        .pr_1()
                        .text_size(px(11.5))
                        .text_color(theme::faint())
                        .when(refreshing, |el| el.child(crate::icons::spinner()))
                        .child(if refreshing {
                            t("memory.pulls.refreshing")
                        } else {
                            data.fetched_at
                                .as_deref()
                                .map(|at| {
                                    tv("memory.pulls.readAgo", &[("when", (&time_ago(at)).into())])
                                })
                                .unwrap_or_default()
                        })
                        .child(
                            div()
                                .id("pulls-refresh")
                                .size(px(24.))
                                .flex()
                                .items_center()
                                .justify_center()
                                .rounded_md()
                                .text_color(theme::subtle())
                                .when(refreshing, |el| el.opacity(0.4))
                                .when(!refreshing, |el| {
                                    el.cursor_pointer()
                                        .hover(|s| {
                                            s.bg(theme::surface()).text_color(theme::primary())
                                        })
                                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                            this.load_pulls(true, cx);
                                            cx.notify();
                                        }))
                                })
                                .tooltip(move |window, cx| {
                                    Tooltip::new(refresh_tip.clone()).build(window, cx)
                                })
                                .child(crate::icons::ic("refresh-cw").size(px(14.))),
                        ),
                ),
        );
        if visible.is_empty() {
            col = col.child(
                div()
                    .px_1()
                    .text_size(theme::text::UI)
                    .text_color(theme::subtle())
                    .child(t("memory.pulls.noMatch")),
            );
        } else {
            let mut list = v_flex().gap_2();
            for (ix, depth, on, by) in stack_order(&visible) {
                let pr = &visible[ix];
                let offer = can_offer(pr);
                list = list.child(self.pull_row(pr, depth, on, by, offer, data, cx));
            }
            col = col.child(list);
        }
        wrap(col.into_any_element())
    }

    #[allow(clippy::too_many_arguments)]
    fn pull_row(
        &self,
        pr: &PullRequest,
        depth: usize,
        stacked_on: Option<i64>,
        stacked_by: Vec<i64>,
        offer: bool,
        data: &ProjectPullRequests,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let number = pr.number;
        let blocked = merge_block(pr, Some(data));
        let running = self.pulls.running.contains(&number);
        let url = pr.url.clone();
        let mut pills: Vec<AnyElement> = vec![state_badge(&pr.state, pr.draft)];
        pills.extend(checks_badge(&pr.checks));
        if let Some(on) = stacked_on {
            let tip: SharedString = tv(
                "memory.pulls.stackedOnTip",
                &[("number", on.into()), ("base", (&pr.base_branch).into())],
            )
            .into();
            pills.push(
                div()
                    .id(SharedString::from(format!("pull-stacked-{number}")))
                    .tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
                    .child(pill(
                        tv("memory.pulls.stackedOn", &[("number", on.into())]),
                        Tone::Info,
                        Some("layers"),
                    ))
                    .into_any_element(),
            );
        }
        if pr.conflict {
            pills.push(pill(t("memory.pulls.conflicts"), Tone::Danger, None).into_any_element());
        }
        if pr.review == "changes_requested" {
            pills.push(
                pill(t("memory.pulls.changesRequested"), Tone::Warning, None).into_any_element(),
            );
        }
        let agent = pr.agent.clone();
        // A meta item: its icon and what it says (the row's foot).
        let meta = |icon: &str| {
            h_flex()
                .gap_1p5()
                .child(crate::icons::ic(icon).size(px(14.)))
        };
        // Electron's .panel, rounded-2xl: a card lifting off the rail, its
        // edge brightening under the pointer.
        v_flex()
            .id(SharedString::from(format!("pull-{number}")))
            .relative()
            .ml(px(depth.min(3) as f32 * 28.))
            .px_4()
            .py(px(14.))
            .rounded(theme::radius::XXL)
            .border_1()
            .border_color(theme::line())
            .bg(theme::linear_panel())
            .shadow(theme::shadow_card())
            .cursor_pointer()
            .hover(|s| s.border_color(theme::line_strong()))
            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                this.open_pull(number, window, cx)
            }))
            .when(depth > 0, |el| {
                el.child(
                    div()
                        .absolute()
                        .top(px(16.))
                        .left(px(-24.))
                        .text_color(theme::faint())
                        .child(crate::icons::ic("corner-down-right").size(px(16.))),
                )
            })
            .child(
                h_flex()
                    .items_start()
                    .gap_3()
                    .child(
                        div()
                            .flex_none()
                            .mt(px(2.))
                            .text_color(theme::subtle())
                            .child(crate::icons::ic("git-pull-request").size(px(16.))),
                    )
                    .child(
                        v_flex()
                            .flex_1()
                            .min_w_0()
                            // The title first: two lines of it before anything beside it gives way.
                            .child(
                                h_flex()
                                    .items_start()
                                    .gap_1p5()
                                    .min_w_0()
                                    .child(super::controls::full_on_hover(
                                        div()
                                            .min_w_0()
                                            .text_size(theme::text::BODY)
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::primary())
                                            .line_clamp(2)
                                            .text_ellipsis()
                                            .child(pr.title.clone()),
                                        SharedString::from(format!("pr-title-{}", pr.number)),
                                        pr.title.clone(),
                                    ))
                                    .child(
                                        div()
                                            .id(SharedString::from(format!("pull-gh-{number}")))
                                            .flex_none()
                                            .mt(px(3.))
                                            .text_color(theme::faint())
                                            .cursor_pointer()
                                            .hover(|s| s.text_color(theme::brand(300)))
                                            .tooltip({
                                                let tip: SharedString =
                                                    t("pulls.detail.openOnGitHub").into();
                                                move |window, cx| {
                                                    Tooltip::new(tip.clone()).build(window, cx)
                                                }
                                            })
                                            .on_mouse_down(MouseButton::Left, |_, _, cx| {
                                                cx.stop_propagation()
                                            })
                                            .on_click(move |_, _, cx| {
                                                cx.stop_propagation();
                                                cx.open_url(&url)
                                            })
                                            .child(crate::icons::ic("external-link").size(px(14.))),
                                    ),
                            )
                            .child(
                                div()
                                    .truncate()
                                    .font_family(crate::theme::mono())
                                    .text_size(theme::text::CAPTION)
                                    .text_color(theme::subtle())
                                    .child(format!(
                                        "#{number} · {} → {}",
                                        pr.head_branch, pr.base_branch
                                    )),
                            )
                            .child(div().mt_1p5().child(self.labels_row("row", number, cx))),
                    )
                    .child(
                        h_flex()
                            .flex_none()
                            .max_w(relative(0.4))
                            .min_w_0()
                            .flex_wrap()
                            .justify_end()
                            .gap_1()
                            .children(pills),
                    ),
            )
            .child(
                h_flex()
                    .mt(px(10.))
                    .flex_wrap()
                    .gap_x_4()
                    .gap_y_1p5()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::muted())
                    .when(!pr.author.is_empty(), |el| {
                        el.child(
                            div()
                                .max_w(px(160.))
                                .min_w_0()
                                .truncate()
                                .child(pr.author.clone()),
                        )
                    })
                    .when(pr.additions > 0 || pr.deletions > 0, |el| {
                        el.child(
                            meta("git-branch")
                                .child(
                                    div()
                                        .text_color(theme::emerald(400))
                                        .child(format!("+{}", pr.additions)),
                                )
                                .child(
                                    div()
                                        .text_color(theme::rose(400))
                                        .child(format!("−{}", pr.deletions)),
                                ),
                        )
                    })
                    .when(pr.comments > 0, |el| {
                        el.child(meta("message-square").child(tv(
                            "memory.pulls.comments",
                            &[("count", pr.comments.into())],
                        )))
                    })
                    .when(!stacked_by.is_empty(), |el| {
                        let numbers = stacked_by
                            .iter()
                            .map(|n| format!("#{n}"))
                            .collect::<Vec<_>>()
                            .join(", ");
                        el.child(meta("layers").child(tv(
                            "memory.pulls.stackedBy",
                            &[
                                ("count", stacked_by.len().into()),
                                ("numbers", (&numbers).into()),
                            ],
                        )))
                    })
                    .when(!agent.is_empty(), |el| {
                        let a = agent.clone();
                        el.child(
                            link(
                                SharedString::from(format!("pull-agent-{number}")),
                                tv("memory.pulls.agentOf", &[("name", (&agent).into())]),
                            )
                            .on_click(cx.listener(
                                move |this, _: &ClickEvent, window, cx| {
                                    cx.stop_propagation();
                                    this.open_agent_chat(a.clone(), window, cx)
                                },
                            )),
                        )
                    })
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_color(theme::faint())
                            .child(pr.updated_at.as_deref().map(time_ago).unwrap_or_default()),
                    )
                    .when(offer, |el| {
                        el.child(
                            h_flex()
                                .min_w_0()
                                .gap_2()
                                .when_some(blocked.clone(), |el, b| el.child(merge_blocked(b)))
                                .child(
                                    div()
                                        .on_mouse_down(MouseButton::Left, |_, _, cx| {
                                            cx.stop_propagation()
                                        })
                                        .child(controls::button_busy(
                                            SharedString::from(format!("pull-merge-{number}")),
                                            t("memory.pulls.merge"),
                                            Btn::Secondary,
                                            running,
                                            blocked.is_some(),
                                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                                cx.stop_propagation();
                                                this.request_merge(number, cx)
                                            }),
                                        )),
                                ),
                        )
                    }),
            )
            .when_some(self.pulls.failed.get(&number), |el, e| {
                el.child(
                    notice(
                        tv("memory.pulls.mergeFailed", &[("error", e.into())]),
                        theme::error(),
                    )
                    .mt(px(10.)),
                )
            })
            .into_any_element()
    }

    /// render_pull is the pull request open over the window
    /// (PullRequestModal): what it is, its description and checks, or its
    /// files, and its actions.
    pub(super) fn render_pull(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let v = self.pulls.viewing.as_ref()?;
        let number = v.number;
        let listed = self.pulls.pr(number).cloned();
        let detail = v.detail.as_ref().and_then(|d| d.as_ref().ok());
        // What the list had shows at once; what's read fresh replaces it.
        let pr: PullRequest = match (detail, &listed) {
            (Some(d), _) => PullRequest {
                number: d.number,
                title: d.title.clone(),
                state: d.state.clone(),
                checks: d.checks.clone(),
                url: d.url.clone(),
                draft: d.draft,
                additions: d.additions,
                deletions: d.deletions,
                comments: d.comments,
                updated_at: d.updated_at.clone(),
                base_branch: d.base_branch.clone(),
                head_branch: d.head_branch.clone(),
                author: d.author.clone(),
                agent: d.agent.clone(),
                conflict: listed.as_ref().is_some_and(|l| l.conflict),
                review: listed
                    .as_ref()
                    .map(|l| l.review.clone())
                    .unwrap_or_default(),
                labels: d.labels.clone(),
                ..Default::default()
            },
            (None, Some(l)) => l.clone(),
            (None, None) => return None,
        };
        let data = self.pulls.list();
        let offer = pr.state == "open"
            && !pr.draft
            && data.is_none_or(|d| !d.can_merge_known || d.can_merge);
        let blocked = merge_block(&pr, data);
        let running = self.pulls.running.contains(&number);
        let into = match pr.state.as_str() {
            "merged" => "pulls.detail.intoMerged",
            "closed" => "pulls.detail.intoClosed",
            _ => "pulls.detail.into",
        };
        let author = if pr.author.is_empty() {
            "?".to_string()
        } else {
            pr.author.clone()
        };
        let tab = v.tab;
        let tab_el =
            |id: &'static str, label: String, on: bool, to: DetailTab, cx: &mut Context<Self>| {
                h_flex()
                    .id(id)
                    .gap_2()
                    .h(px(32.))
                    .px_3()
                    .rounded_lg()
                    .text_size(theme::text::UI)
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(if on { theme::title() } else { theme::muted() })
                    .when(on, |el| {
                        el.bg(theme::surface_strong()).shadow(theme::shadow_card())
                    })
                    .when(!on, |el| {
                        el.cursor_pointer()
                            .hover(|s| s.text_color(theme::primary()))
                            .active(|s| s.opacity(0.7))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                if let Some(v) = &mut this.pulls.viewing {
                                    v.tab = to;
                                    if to == DetailTab::Files {
                                        let f = v.files.read(cx).focus_handle();
                                        f.focus(window, cx);
                                    }
                                }
                                cx.notify();
                            }))
                    })
                    .child(label)
            };
        let changed = detail.map(|d| d.changed_files);
        let body = match tab {
            DetailTab::Files => div().size_full().child(v.files.clone()).into_any_element(),
            DetailTab::Description => {
                let content = match &v.detail {
                    Some(Err(e)) => notice(
                        tv("pulls.detail.unavailable", &[("error", e.into())]),
                        theme::error(),
                    )
                    .into_any_element(),
                    None => div()
                        .child(crate::motion::skeleton_lines("loading-pulls-2", 4))
                        .into_any_element(),
                    Some(Ok(d)) => v_flex()
                        .gap_6()
                        .child(if d.body.trim().is_empty() {
                            div()
                                .text_size(theme::text::UI)
                                .italic()
                                .text_color(theme::subtle())
                                .child(t("pulls.detail.noDescription"))
                                .into_any_element()
                        } else {
                            div()
                                .text_size(px(13.5))
                                .child({
                                    let images = v.images.clone();
                                    let n = images.lock().map(|m| m.len()).unwrap_or(0);
                                    TextView::markdown(
                                        SharedString::from(format!("pull-body-{number}-{n}")),
                                        d.body.clone(),
                                    )
                                    .image_source(move |uri| {
                                        match images
                                            .lock()
                                            .ok()
                                            .and_then(|m| m.get(uri.as_ref()).cloned())
                                        {
                                            Some(p) => ImageSource::from(p),
                                            None => ImageSource::from(uri.clone()),
                                        }
                                    })
                                })
                                .into_any_element()
                        })
                        .when(!d.check_runs.is_empty(), |el| {
                            el.child(
                                v_flex()
                                    .gap_2()
                                    .child(
                                        div()
                                            .text_size(theme::text::CAPTION)
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::muted())
                                            .child(t("pulls.detail.checks")),
                                    )
                                    .child(
                                        v_flex()
                                            .rounded_xl()
                                            .border_1()
                                            .border_color(theme::line_faint())
                                            .overflow_hidden()
                                            .children(d.check_runs.iter().enumerate().map(
                                                |(i, run)| {
                                                    let (mark, color, key) = check_state(run);
                                                    let url = run.url.clone();
                                                    h_flex()
                                                        .id(("pull-check", i))
                                                        .gap_2()
                                                        .px_3()
                                                        .py_1p5()
                                                        .when(i > 0, |el| {
                                                            el.border_t_1()
                                                                .border_color(theme::line_faint())
                                                        })
                                                        .text_size(px(12.5))
                                                        .child(
                                                            div()
                                                                .flex_none()
                                                                .text_color(color)
                                                                .child(crate::icons::icon(
                                                                    mark,
                                                                    crate::icons::ic(mark)
                                                                        .size(px(14.)),
                                                                )),
                                                        )
                                                        .child(
                                                            div()
                                                                .flex_1()
                                                                .min_w_0()
                                                                .truncate()
                                                                .text_color(theme::secondary())
                                                                .child(run.name.clone()),
                                                        )
                                                        .child(
                                                            div()
                                                                .flex_none()
                                                                .text_color(theme::faint())
                                                                .child(t(key)),
                                                        )
                                                        .when(!url.is_empty(), |el| {
                                                            el.child(
                                                                div()
                                                                    .id(("pull-check-gh", i))
                                                                    .flex_none()
                                                                    .p(px(2.))
                                                                    .rounded_sm()
                                                                    .text_color(theme::faint())
                                                                    .cursor_pointer()
                                                                    .hover(|s| {
                                                                        s.text_color(theme::brand(
                                                                            300,
                                                                        ))
                                                                    })
                                                                    .on_click(move |_, _, cx| {
                                                                        cx.open_url(&url)
                                                                    })
                                                                    .child(
                                                                        crate::icons::ic(
                                                                            "external-link",
                                                                        )
                                                                        .size(px(14.)),
                                                                    ),
                                                            )
                                                        })
                                                },
                                            )),
                                    ),
                            )
                        })
                        .into_any_element(),
                };
                div()
                    .id("pull-description")
                    .size_full()
                    .overflow_y_scroll()
                    .px_6()
                    .py_5()
                    .child(div().max_w(px(860.)).mx_auto().child(content))
                    .into_any_element()
            }
        };
        let agent = pr.agent.clone();
        let url = pr.url.clone();
        let header = v_flex()
            .relative()
            .flex_none()
            .gap_2()
            .pl_6()
            .pr(px(56.))
            .pt_5()
            .pb_3()
            .border_b_1()
            .border_color(theme::line_faint())
            .child(
                h_flex()
                    .items_start()
                    .gap_2()
                    .text_size(px(17.))
                    .line_height(relative(1.375))
                    .child(
                        div()
                            .flex_none()
                            .mt(px(4.))
                            .text_color(theme::subtle())
                            .child(crate::icons::ic("git-pull-request").size(px(16.))),
                    )
                    .child(
                        h_flex()
                            .flex_1()
                            .min_w_0()
                            .flex_wrap()
                            .items_start()
                            .gap_x_1p5()
                            .child(
                                div()
                                    .min_w_0()
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::title())
                                    .child(pr.title.clone()),
                            )
                            .child(number_link(("pull-detail-number", number as u64), &pr)),
                    )
                    .child(
                        div()
                            .id("pull-close")
                            .absolute()
                            .top_4()
                            .right_4()
                            .p_1()
                            .rounded_md()
                            .text_color(theme::subtle())
                            .cursor_pointer()
                            .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                            .tooltip({
                                let tip: SharedString = t("common.close").into();
                                move |window, cx| Tooltip::new(tip.clone()).build(window, cx)
                            })
                            .on_click(
                                cx.listener(|this, _: &ClickEvent, _, cx| this.close_pull(cx)),
                            )
                            .child(crate::icons::ic("x").size(px(16.))),
                    ),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_x_2()
                    .gap_y_1p5()
                    .text_size(px(12.5))
                    .text_color(theme::muted())
                    .child(state_badge(&pr.state, pr.draft))
                    .child(tv(
                        into,
                        &[
                            ("author", (&author).into()),
                            ("head", (&pr.head_branch).into()),
                            ("base", (&pr.base_branch).into()),
                        ],
                    ))
                    .when_some(pr.updated_at.as_deref(), |el, at| {
                        el.child(
                            div()
                                .text_color(theme::faint())
                                .child(format!("· {}", time_ago(at))),
                        )
                    })
                    .when(!agent.is_empty(), |el| {
                        let a = agent.clone();
                        el.child(
                            link(
                                "pull-detail-agent",
                                tv("memory.pulls.agentOf", &[("name", (&agent).into())]),
                            )
                            .on_click(cx.listener(
                                move |this, _: &ClickEvent, window, cx| {
                                    this.open_agent_chat(a.clone(), window, cx)
                                },
                            )),
                        )
                    }),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_1p5()
                    .children(checks_badge(&pr.checks))
                    .when(pr.conflict, |el| {
                        el.child(pill(t("memory.pulls.conflicts"), Tone::Danger, None))
                    })
                    .when(pr.review == "changes_requested", |el| {
                        el.child(pill(
                            t("memory.pulls.changesRequested"),
                            Tone::Warning,
                            None,
                        ))
                    })
                    .child(self.labels_row("detail", number, cx)),
            )
            .child(
                h_flex()
                    .mt_1()
                    .self_start()
                    .gap(px(2.))
                    .p_1()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::surface_faint())
                    .child(tab_el(
                        "pull-tab-description",
                        t("pulls.detail.tab.description"),
                        tab == DetailTab::Description,
                        DetailTab::Description,
                        cx,
                    ))
                    .child(
                        tab_el(
                            "pull-tab-files",
                            t("pulls.detail.tab.files"),
                            tab == DetailTab::Files,
                            DetailTab::Files,
                            cx,
                        )
                        .when_some(changed, |el, n| {
                            el.child(
                                div()
                                    .px_1p5()
                                    .rounded_full()
                                    .bg(theme::surface_raised())
                                    .text_size(px(11.))
                                    .font_weight(FontWeight::NORMAL)
                                    .text_color(theme::subtle())
                                    .child(n.to_string()),
                            )
                        }),
                    ),
            );
        let footer = h_flex()
            .flex_none()
            .flex_wrap()
            .gap_2()
            .items_center()
            .px_6()
            .py_3()
            .border_t_1()
            .border_color(theme::line_faint())
            .when_some(self.pulls.failed.get(&number), |el, e| {
                el.child(
                    notice(
                        tv("memory.pulls.mergeFailed", &[("error", e.into())]),
                        theme::error(),
                    )
                    .w_full(),
                )
            })
            .child(div().flex_1())
            .child(controls::button(
                "pull-open-github",
                format!("↗ {}", t("pulls.detail.openOnGitHub")),
                Btn::Ghost,
                false,
                move |_, _, cx| cx.open_url(&url),
            ))
            .when(offer, |el| {
                el.when_some(blocked.clone(), |el, b| el.child(merge_blocked(b)))
                    .child(controls::button_busy(
                        "pull-detail-merge",
                        t("memory.pulls.merge"),
                        Btn::Primary,
                        running,
                        blocked.is_some(),
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.request_merge(number, cx)
                        }),
                    ))
            });
        Some(
            div()
                .id("pull-modal")
                .track_focus(&self.pulls.focus)
                .absolute()
                .inset_0()
                // The backdrop takes every pointer event: nothing under it
                // hovers, clicks or scrolls while the modal is open.
                .occlude()
                .flex()
                .items_center()
                .justify_center()
                .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                    if ev.keystroke.key == "escape" {
                        if this.pulls.picker.is_some() {
                            this.pulls.picker = None;
                        } else if this.pulls.confirm.is_none() {
                            this.close_pull(cx);
                        }
                        cx.notify();
                    }
                }))
                .on_mouse_down(
                    MouseButton::Left,
                    cx.listener(|this, _, _, cx| this.close_pull(cx)),
                )
                // The scrim fades in under the card, which pops in over it.
                .child(motion::scrim_in(
                    div().absolute().inset_0().bg(theme::scrim()),
                    SharedString::from(format!("pull-scrim-{number}")),
                ))
                .child(motion::pop_in(
                    v_flex()
                        .id("pull-card")
                        .w(relative(0.94))
                        .max_w(px(1152.))
                        .h(relative(0.88))
                        .rounded(theme::radius::XXL)
                        .bg(theme::modal())
                        .border_1()
                        .border_color(theme::line_strong())
                        .shadow(theme::shadow_modal())
                        .overflow_hidden()
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .child(header)
                        .child(div().flex_1().min_h_0().child(body))
                        .child(footer),
                    SharedString::from(format!("pull-card-{number}")),
                ))
                .into_any_element(),
        )
    }

    /// render_merge_confirm is the merge's question, over everything.
    pub(super) fn render_merge_confirm(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let c = self.pulls.confirm.as_ref()?;
        Some(merge::confirm(
            c,
            Rc::new(cx.listener(|this, m: &str, _, cx| {
                if let Some(c) = &mut this.pulls.confirm {
                    c.method = m.to_string();
                }
                cx.notify();
            })),
            {
                let view = cx.entity().downgrade();
                move |_, cx: &mut App| {
                    view.update(cx, |this, cx| {
                        this.pulls.confirm = None;
                        cx.notify();
                    })
                    .ok();
                }
            },
            cx.listener(|this, _: &ClickEvent, _, cx| this.merge_pull(cx)),
        ))
    }

    /// render_rail_switch is the lead's panel's tabs (LeadSwitch): Agents,
    /// with how many; Pull requests (when the project has them on), with how
    /// many are open and how many of its agents are ready to review; Issues,
    /// Files, Evidence and Schedules. Only the lit one is named.
    pub(super) fn render_rail_switch(&self, agents: usize, cx: &mut Context<Self>) -> AnyElement {
        let roomy = self.panel_box().width >= 380.;
        let (open, ready) = self.pull_counts();
        let tab = self.pulls.tab;
        let pulls_tab = self
            .projects
            .iter()
            .find(|p| Some(&p.name) == self.project.as_ref())
            .is_some_and(|p| p.pull_requests);
        // The counts' pill: rounded-full bg-surface-strong px-1.5 10.5px subtle.
        let count = |n: String| {
            div()
                .flex_none()
                .px_1p5()
                .rounded_full()
                .bg(theme::surface_strong())
                .text_size(px(10.5))
                .line_height(px(15.75))
                .text_color(theme::subtle())
                .child(n)
        };
        let tabs: Vec<(&'static str, RailTab, &'static str, &'static str)> = [
            (
                "rail-tab-agents",
                RailTab::Agents,
                "panel.lead.agents",
                "users",
            ),
            (
                "rail-tab-pulls",
                RailTab::Pulls,
                "panel.view.pulls",
                "git-pull-request",
            ),
            (
                "rail-tab-issues",
                RailTab::Issues,
                "panel.view.issues",
                "circle-dot",
            ),
            (
                "rail-tab-files",
                RailTab::Files,
                "panel.view.files",
                "folder-tree",
            ),
            (
                "rail-tab-evidence",
                RailTab::Evidence,
                "panel.view.evidence",
                "images",
            ),
            (
                "rail-tab-schedules",
                RailTab::Schedules,
                "panel.view.schedules",
                "alarm-clock",
            ),
        ]
        .into_iter()
        .filter(|(_, to, _, _)| *to != RailTab::Pulls || pulls_tab)
        .collect();
        let mut pull_tip = vec![t("panel.view.pulls")];
        if open > 0 {
            pull_tip.push(tv("v2.header.openPulls", &[("count", open.into())]));
        }
        if ready > 0 {
            pull_tip.push(tv("v2.header.readyPulls", &[("count", ready.into())]));
        }
        let pull_tip = pull_tip.join(" · ");
        super::panel::switch_box()
            .min_w_0()
            .flex_shrink(1.)
            .children(tabs.into_iter().map(|(id, to, key, icon)| {
                let on = tab == to;
                let tip: SharedString = if to == RailTab::Pulls {
                    pull_tip.clone().into()
                } else {
                    t(key).into()
                };
                super::panel::switch_item(id, icon, on)
                    .when(on, |el| el.min_w_0().px_2p5())
                    .when(!on, |el| {
                        el.flex_none().px_2().on_click(cx.listener(
                            move |this, _: &ClickEvent, window, cx| {
                                this.pulls.tab = to;
                                if to == RailTab::Pulls {
                                    this.load_pulls(false, cx);
                                }
                                this.panel_sync(window, cx);
                                cx.notify();
                            },
                        ))
                    })
                    .when(!on || !roomy || to == RailTab::Pulls, |el| {
                        el.tooltip(move |window, cx| Tooltip::new(tip.clone()).build(window, cx))
                    })
                    // Its name when the panel has room for it: in the list's
                    // width it was squeezed to "..", so there the icon and
                    // the count say it, and the tooltip the rest.
                    .when(on && roomy, |el| {
                        el.child(div().min_w_0().truncate().child(t(key)))
                    })
                    .when(to == RailTab::Agents && agents > 0, |el| {
                        el.child(count(agents.to_string()))
                    })
                    .when(
                        to == RailTab::Pulls && open > 0 && (on || ready == 0),
                        |el| el.child(count(open.to_string())),
                    )
                    .when(to == RailTab::Pulls && ready > 0, |el| {
                        el.child(
                            div()
                                .flex_none()
                                .px_1p5()
                                .rounded_full()
                                .bg(theme::brand(500).opacity(0.15))
                                .text_size(px(10.5))
                                .line_height(px(15.75))
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(if theme::look() == theme::Look::Light {
                                    theme::brand(600)
                                } else {
                                    theme::brand(300)
                                })
                                .child(if on {
                                    tv("v2.header.ready", &[("count", ready.into())])
                                } else {
                                    ready.to_string()
                                }),
                        )
                    })
                    // The tab just picked lights up as it opens out.
                    .map(|el| {
                        if on {
                            crate::motion::lit(el, "rail-tab", &key)
                        } else {
                            el.into_any_element()
                        }
                    })
            }))
            .into_any_element()
    }

    /// render_project_header is v2's project header over its chats
    /// (ProjectCrumb and ProjectMenu): the project, copied by a click, then
    /// Lead or the agent; the chat's state; its pull requests' counts; its
    /// settings; and its ⋯.
    pub(super) fn render_project_header(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let name = self.project.clone().filter(|_| !self.is_home())?;
        let project = self.projects.iter().find(|p| p.name == name)?.clone();
        let display = if project.display_name.is_empty() {
            project.name.clone()
        } else {
            project.display_name.clone()
        };
        let initial = display
            .chars()
            .next()
            .unwrap_or('?')
            .to_uppercase()
            .to_string();
        let lead = self.open == "lead";
        // The agent by its title, as the breadcrumb names it, else its name.
        let then = if lead {
            t("v2.header.lead")
        } else {
            self.open_agent()
                .map(|a| a.title.clone())
                .filter(|t| !t.is_empty())
                .unwrap_or_else(|| self.open.clone())
        };
        let (status, working) = self
            .chat
            .as_ref()
            .map(|c| {
                let c = c.read(cx);
                (c.status_line(), c.working())
            })
            .unwrap_or_default();
        let (open, ready) = self.pull_counts();
        let root = project.root.clone();
        let root_tip: SharedString = if lead {
            project.root.clone()
        } else {
            t("v2.header.openLead")
        }
        .into();
        let copy_tip: SharedString = t("v2.header.copyPath").into();
        let settings_tip: SharedString = t("v2.header.settings").into();
        let menu_tip: SharedString = t("v2.header.chatOptions").into();
        let copy = Rc::new({
            let root = root.clone();
            cx.listener(move |this: &mut AppView, _: &ClickEvent, _, cx| {
                cx.write_to_clipboard(ClipboardItem::new_string(root.clone()));
                this.pulls.copied = true;
                cx.spawn(async move |this, cx| {
                    cx.background_executor()
                        .timer(Duration::from_millis(1500))
                        .await;
                    this.update(cx, |this, cx| {
                        this.pulls.copied = false;
                        cx.notify();
                    })
                    .ok();
                })
                .detach();
                cx.notify();
            })
        });
        let mut counts = Vec::new();
        if open > 0 {
            counts.push(tv("v2.header.openPulls", &[("count", open.into())]));
        }
        if ready > 0 {
            counts.push(tv("v2.header.readyPulls", &[("count", ready.into())]));
        }
        let menu_project = name.clone();
        let menu = Button::new("project-menu")
            .ghost()
            .compact()
            .size(px(32.))
            .rounded(gpui_component::button::ButtonRounded::Size(px(8.)))
            .text_color(theme::muted())
            .icon(crate::icons::ic("ellipsis").size(px(15.)))
            .tooltip(menu_tip)
            .dropdown_menu_with_anchor(Anchor::TopRight, {
                let view = cx.entity().downgrade();
                let root = root.clone();
                let display = display.clone();
                let pinned = self.side.pins.contains(&name);
                let others = self.projects.len() > 1;
                move |menu, window, cx| {
                    let v = view.clone();
                    let n = menu_project.clone();
                    let act =
                        move |f: fn(&mut AppView, String, &mut Window, &mut Context<AppView>)| {
                            let (v, n) = (v.clone(), n.clone());
                            move |_: &ClickEvent, window: &mut Window, cx: &mut App| {
                                let n = n.clone();
                                v.update(cx, |this, cx| f(this, n, window, cx)).ok();
                            }
                        };
                    let mut menu = menu
                        .min_w(px(220.))
                        .label(display.clone())
                        .item(
                            PopupMenuItem::new(t("chat.tab.newChat"))
                                .icon(crate::icons::ic("plus"))
                                .on_click(act(|this, n, _, cx| {
                                    this.side.dialog =
                                        Some(super::sidebar_dialogs::Dialog::NewChat {
                                            project: n,
                                        });
                                    cx.notify();
                                })),
                        )
                        .item(
                            PopupMenuItem::new(t("group.menu.fanOut"))
                                .icon(crate::icons::ic("split"))
                                .on_click(act(|this, n, window, cx| {
                                    this.open_fan_out(n, window, cx)
                                })),
                        );
                    if others {
                        menu = menu.item(
                            PopupMenuItem::new(t("v2.talk.button"))
                                .icon(crate::icons::ic("message-circle"))
                                .on_click(act(|this, n, _, cx| {
                                    this.side.dialog =
                                        Some(super::sidebar_dialogs::Dialog::Talk { project: n });
                                    cx.notify();
                                })),
                        );
                    }
                    menu = menu
                        .item(
                            PopupMenuItem::new(t("panel.view.pulls"))
                                .icon(crate::icons::ic("git-pull-request"))
                                .on_click(act(|this, _, _, cx| {
                                    this.pulls.tab = RailTab::Pulls;
                                    this.load_pulls(false, cx);
                                    cx.notify();
                                })),
                        )
                        .separator()
                        .item(
                            PopupMenuItem::new(if pinned {
                                t("v2.sidebar.unpin")
                            } else {
                                t("v2.sidebar.pin")
                            })
                            .icon(crate::icons::ic("pin"))
                            .on_click(act(|this, n, _, cx| this.toggle_pin(&n, cx))),
                        )
                        .item(
                            PopupMenuItem::new(t("v2.sidebar.projectSettings"))
                                .icon(crate::icons::ic("settings"))
                                .on_click(act(|this, n, window, cx| {
                                    this.open_settings_at(format!("project:{n}"), window, cx)
                                })),
                        );
                    let (v, n, r) = (view.clone(), menu_project.clone(), root.clone());
                    menu.submenu(t("common.more"), window, cx, move |m, _, _| {
                        let (v1, v2, v3) = (v.clone(), v.clone(), v.clone());
                        let (n1, n2, n3, r) = (n.clone(), n.clone(), n.clone(), r.clone());
                        m.item(
                            PopupMenuItem::new(t("projectIcon.change"))
                                .icon(crate::icons::ic("image"))
                                .on_click(move |_, window, cx| {
                                    let n = n1.clone();
                                    v1.update(cx, |this, cx| this.open_icon_dialog(n, window, cx))
                                        .ok();
                                }),
                        )
                        .item(
                            PopupMenuItem::new(t("project.view.openFolder"))
                                .icon(crate::icons::ic("folder-open"))
                                .on_click(move |_, _, cx| {
                                    cx.open_with_system(std::path::Path::new(&r))
                                }),
                        )
                        .item(
                            PopupMenuItem::new(t("v2.menu.agentYourself"))
                                .icon(crate::icons::ic("bot"))
                                .on_click(move |_, window, cx| {
                                    let n = n2.clone();
                                    v2.update(cx, |this, cx| this.open_new_agent(n, window, cx))
                                        .ok();
                                }),
                        )
                        .item(
                            PopupMenuItem::new(t("work.title"))
                                .icon(crate::icons::ic("list-todo"))
                                .on_click(move |_, window, cx| {
                                    let n = n3.clone();
                                    v3.update(cx, |this, cx| {
                                        this.open_work(n, String::new(), window, cx)
                                    })
                                    .ok();
                                }),
                        )
                    })
                }
            });
        let _ = (status, working, counts, settings_tip);
        let back_tip: SharedString = t("v2.header.backToLead").into();
        let crumbs = h_flex()
            .min_w_0()
            .gap(px(6.))
            // In an agent's chat, the way back to its lead's.
            .when(!lead, |el| {
                el.child(
                    div()
                        .id("back-to-lead")
                        .flex_none()
                        .ml(px(-4.))
                        .p_1()
                        .rounded_md()
                        .text_color(theme::muted())
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                        .tooltip(move |w, cx| Tooltip::new(back_tip.clone()).build(w, cx))
                        .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                            this.open_chat("lead".into(), window, cx)
                        }))
                        .child(crate::icons::ic("arrow-left").size(px(16.))),
                )
            })
            // ProjectCrumb (components/v2/ProjectHeader.tsx).
            .child(
                h_flex()
                    .id("project-crumb")
                    .min_w_0()
                    .mx(px(-4.))
                    .px_1()
                    .py_0p5()
                    .gap(px(6.))
                    .rounded_md()
                    .text_color(theme::primary())
                    .cursor_pointer()
                    .hover(|s| s.bg(theme::surface_raised()))
                    .active(|s| s.bg(theme::surface_strong()))
                    .tooltip(move |window, cx| Tooltip::new(root_tip.clone()).build(window, cx))
                    .map(|el| {
                        if lead {
                            let copy = copy.clone();
                            el.on_click(move |e, w, cx| copy(e, w, cx))
                        } else {
                            el.on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                                this.open_chat("lead".into(), window, cx)
                            }))
                        }
                    })
                    .child(
                        div()
                            .flex_none()
                            .size_5()
                            .rounded_md()
                            .bg(theme::surface_strong())
                            .text_color(theme::secondary())
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::SEMIBOLD)
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(initial),
                    )
                    .child(
                        div()
                            .max_w(px(256.))
                            .truncate()
                            .font_weight(FontWeight::MEDIUM)
                            .child(display.clone()),
                    ),
            )
            // Its copy button, seen on hover; it holds its place meanwhile.
            .child(
                div()
                    .id("project-copy")
                    .flex_none()
                    .p_0p5()
                    .rounded(px(4.))
                    .opacity(if self.pulls.copied { 1. } else { 0. })
                    .text_color(if self.pulls.copied {
                        theme::running()
                    } else {
                        theme::faint()
                    })
                    .cursor_pointer()
                    .hover(|s| s.opacity(1.).text_color(theme::primary()))
                    .tooltip(move |window, cx| Tooltip::new(copy_tip.clone()).build(window, cx))
                    .on_click(move |e, w, cx| copy(e, w, cx))
                    .child(
                        crate::icons::ic(if self.pulls.copied { "check" } else { "copy" })
                            .size(px(12.)),
                    ),
            )
            .child(
                div()
                    .flex_none()
                    .text_color(theme::faint())
                    .child(crate::icons::ic("chevron-right").size(px(14.))),
            )
            .child(
                div()
                    .min_w_0()
                    .truncate()
                    .text_size(theme::text::HEADING)
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(if lead {
                        theme::secondary()
                    } else {
                        theme::title()
                    })
                    .child(then),
            );
        // TopEndSlot: the project's ⋯ (icon-sm) and the panel's button.
        let end = h_flex()
            .flex_none()
            .gap_1()
            .child(div().flex_none().child(menu))
            .child(self.render_panel_button(cx));
        // Only the lead's chat puts them in the top bar (ProjectView's
        // InTopBar); an agent's header has its own.
        let end = lead.then(|| end.into_any_element());
        Some(self.top_bar_frame(crumbs.into_any_element(), end, cx))
    }
}

/// on_search is the label search's Enter and changes.
pub(super) fn on_search(this: &mut AppView, ev: &InputEvent, cx: &mut Context<AppView>) {
    match ev {
        InputEvent::PressEnter { .. } => this.picker_enter(cx),
        InputEvent::Change => cx.notify(),
        _ => {}
    }
}

#[cfg(test)]
mod tests {
    use super::{Label, LabelEdit, PullRequest, merge_block, stack_order, with_edits};

    fn pr(number: i64, head: &str, base: &str) -> PullRequest {
        PullRequest {
            number,
            head_branch: head.into(),
            base_branch: base.into(),
            state: "open".into(),
            ..Default::default()
        }
    }

    #[test]
    fn a_stacked_one_comes_after_its_base() {
        let prs = [
            pr(3, "c", "b"),
            pr(1, "a", "main"),
            pr(2, "b", "a"),
            pr(4, "d", "main"),
        ];
        let order: Vec<(i64, usize, Option<i64>)> = stack_order(&prs)
            .into_iter()
            .map(|(i, d, on, _)| (prs[i].number, d, on))
            .collect();
        assert_eq!(
            order,
            [(1, 0, None), (2, 1, Some(1)), (3, 2, Some(2)), (4, 0, None)]
        );
        let by: Vec<i64> = stack_order(&prs)[0].3.clone();
        assert_eq!(by, [2]);
    }

    #[test]
    fn pending_label_edits_show_over_the_labels() {
        let l = |n: &str| Label {
            name: n.into(),
            ..Default::default()
        };
        let edits = [
            LabelEdit {
                id: 0,
                number: 7,
                add: vec![l("ui")],
                remove: vec!["bug".into()],
            },
            LabelEdit {
                id: 1,
                number: 8,
                add: vec![l("x")],
                remove: vec![],
            },
        ];
        let got: Vec<String> = with_edits(&[l("bug"), l("mac")], 7, &edits)
            .into_iter()
            .map(|l| l.name)
            .collect();
        assert_eq!(got, ["mac", "ui"]);
    }

    #[test]
    fn why_merge_cant() {
        let mut p = pr(1, "a", "main");
        assert_eq!(merge_block(&p, None), None);
        p.checks = "failing".into();
        assert!(merge_block(&p, None).is_some());
        p.state = "merged".into();
        assert_eq!(merge_block(&p, None), None);
    }
}
