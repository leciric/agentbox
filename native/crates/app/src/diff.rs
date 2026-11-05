//! A diff, file by file (desktop/src/renderer/components/DiffTable.tsx and
//! PullRequestModal.tsx's Files): the files as a tree of folders with what
//! happened to each and how much, the one picked beside it as a table of
//! both line numbers, the sign and the code, tinted by what each line does;
//! or every file one under the other, the long and the generated ones folded.
//! It shows an agent's whole diff (git's text, all there at once) or a pull
//! request's files, each one's diff read from the daemon when it's opened.
use std::collections::{HashMap, HashSet};
use std::rc::Rc;

use agentbox_api::{Client, PullFile, PullFileDiff, PullRequestFiles};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::{prefs, theme};

/// What a line of a unified diff is.
#[derive(Clone, Copy, PartialEq, Debug)]
pub enum LineKind {
    Hunk,
    Add,
    Del,
    Context,
    Note,
}

/// One line of a diff, with its numbers on each side (lib/pulls.ts DiffLine).
#[derive(Clone, PartialEq, Debug)]
pub struct DiffLine {
    pub kind: LineKind,
    pub text: String,
    pub old: Option<u32>,
    pub new: Option<u32>,
}

/// parse_patch reads one file's patch, as GitHub sends it (parsePatch).
pub fn parse_patch(patch: &str) -> Vec<DiffLine> {
    let mut out: Vec<DiffLine> = Vec::new();
    let (mut old, mut new) = (0u32, 0u32);
    let line = |kind, text: &str, old, new| DiffLine {
        kind,
        text: text.to_string(),
        old,
        new,
    };
    for l in patch.split('\n') {
        if let Some((o, n)) = hunk_start(l) {
            (old, new) = (o, n);
            out.push(line(LineKind::Hunk, l, None, None));
        } else if let Some(rest) = l.strip_prefix('+') {
            out.push(line(LineKind::Add, rest, None, Some(new)));
            new += 1;
        } else if let Some(rest) = l.strip_prefix('-') {
            out.push(line(LineKind::Del, rest, Some(old), None));
            old += 1;
        } else if let Some(rest) = l.strip_prefix('\\') {
            out.push(line(LineKind::Note, rest.trim(), None, None));
        } else if !out.is_empty() {
            let rest = l.get(1..).unwrap_or("");
            out.push(line(LineKind::Context, rest, Some(old), Some(new)));
            old += 1;
            new += 1;
        }
    }
    out
}

/// hunk_start is where a hunk header (@@ -a,b +c,d @@) starts on each side.
fn hunk_start(l: &str) -> Option<(u32, u32)> {
    let rest = l.strip_prefix("@@ -")?;
    let (old, rest) = rest.split_once(" +")?;
    let (new, _) = rest.split_once(" @@")?;
    let first = |s: &str| s.split(',').next()?.parse().ok();
    Some((first(old)?, first(new)?))
}

/// split_git_diff is `git diff`'s text as files with their patches.
pub fn split_git_diff(text: &str) -> Vec<(PullFile, String)> {
    let mut out: Vec<(PullFile, String)> = Vec::new();
    let mut patch: Option<String> = None;
    let flush = |out: &mut Vec<(PullFile, String)>, patch: &mut Option<String>| {
        if let (Some(last), Some(p)) = (out.last_mut(), patch.take()) {
            let p = p.trim_end_matches('\n').to_string();
            for l in p.lines() {
                if l.starts_with('+') {
                    last.0.additions += 1;
                } else if l.starts_with('-') {
                    last.0.deletions += 1;
                }
            }
            last.0.has_diff = !p.is_empty();
            last.1 = p;
        }
    };
    for l in text.lines() {
        if let Some(rest) = l.strip_prefix("diff --git ") {
            flush(&mut out, &mut patch);
            let (a, b) = match rest.split_once(" b/") {
                Some((a, b)) => (a.trim_start_matches("a/"), b),
                None => (rest, rest),
            };
            out.push((
                PullFile {
                    path: b.to_string(),
                    previous_path: if a != b { a.to_string() } else { String::new() },
                    status: if a != b { "renamed" } else { "modified" }.into(),
                    ..Default::default()
                },
                String::new(),
            ));
            continue;
        }
        let Some((file, _)) = out.last_mut() else {
            continue;
        };
        match &mut patch {
            Some(p) => {
                p.push_str(l);
                p.push('\n');
            }
            None if l.starts_with("@@ ") => patch = Some(format!("{l}\n")),
            None if l.starts_with("new file mode") => file.status = "added".into(),
            None if l.starts_with("deleted file mode") => file.status = "removed".into(),
            None if l.starts_with("rename from ") => {
                file.status = "renamed".into();
                file.previous_path = l["rename from ".len()..].to_string();
            }
            None => {}
        }
    }
    flush(&mut out, &mut patch);
    out
}

/// What a file is to a review (lib/pulls.ts FileKind): code somebody wrote,
/// or one of the kinds that start folded.
#[derive(Clone, Copy, PartialEq, Debug)]
pub enum FileKind {
    Code,
    Generated,
    Lockfile,
    Golden,
    Binary,
    Large,
    Renamed,
}

const LOCKFILES: &[&str] = &[
    "package-lock.json",
    "pnpm-lock.yaml",
    "yarn.lock",
    "bun.lockb",
    "go.sum",
    "go.work.sum",
    "Cargo.lock",
    "poetry.lock",
    "uv.lock",
    "Pipfile.lock",
    "Gemfile.lock",
    "composer.lock",
    "flake.lock",
    "mix.lock",
    "pubspec.lock",
];

/// LARGE is how many changed lines make a diff too long to open unasked.
pub const LARGE: i64 = 400;
const SMALL: i64 = 120;
const OPEN_BY_DEFAULT: usize = 3;
/// MAX_LINES is how many lines of one file are drawn.
const MAX_LINES: usize = 3000;

fn generated(path: &str) -> bool {
    let name = path.rsplit('/').next().unwrap_or(path);
    path == "desktop/src/shared/api.ts"
        || path.ends_with("/desktop/src/shared/api.ts")
        || name == "vnc.js"
        || name == "CHANGELOG.md"
        || name.ends_with(".min.js")
        || name.ends_with(".min.css")
        || name.ends_with(".pb.go")
        || name.ends_with("_gen.go")
        || name.ends_with("_generated.go")
        || name.ends_with(".map")
        || name
            .rsplit_once('.')
            .is_some_and(|(stem, _)| stem.ends_with(".generated"))
}

pub fn file_kind(f: &PullFile) -> FileKind {
    let name = f.path.rsplit('/').next().unwrap_or(&f.path);
    let changed = f.additions + f.deletions;
    if LOCKFILES.contains(&name) {
        FileKind::Lockfile
    } else if name.ends_with(".golden")
        || f.path.contains("__snapshots__/")
        || name.ends_with(".snap")
    {
        FileKind::Golden
    } else if generated(&f.path) {
        FileKind::Generated
    } else if !f.has_diff {
        if changed > 0 {
            FileKind::Large
        } else if f.status == "renamed" {
            FileKind::Renamed
        } else {
            FileKind::Binary
        }
    } else if changed > LARGE {
        FileKind::Large
    } else {
        FileKind::Code
    }
}

fn kind_label(k: FileKind) -> Option<String> {
    Some(t(match k {
        FileKind::Code => return None,
        FileKind::Generated => "pulls.detail.kind.generated",
        FileKind::Lockfile => "pulls.detail.kind.lockfile",
        FileKind::Golden => "pulls.detail.kind.golden",
        FileKind::Binary => "pulls.detail.kind.binary",
        FileKind::Large => "pulls.detail.kind.large",
        FileKind::Renamed => "pulls.detail.kind.renamed",
    }))
}

/// initially_open is the files whose diff starts open in the list: the
/// first few small ones somebody wrote.
pub fn initially_open(files: &[PullFile]) -> HashSet<String> {
    files
        .iter()
        .filter(|f| file_kind(f) == FileKind::Code && f.additions + f.deletions <= SMALL)
        .take(OPEN_BY_DEFAULT)
        .map(|f| f.path.clone())
        .collect()
}

/// A node of the files' tree: a folder (one holding only one folder is
/// folded into it, as GitHub shows them) or a file, by its index.
#[derive(Clone, PartialEq, Debug)]
pub enum Node {
    Dir {
        name: String,
        path: String,
        children: Vec<Node>,
    },
    File {
        name: String,
        ix: usize,
    },
}

#[derive(Default)]
struct Dir {
    dirs: std::collections::BTreeMap<String, Dir>,
    files: Vec<usize>,
}

/// file_tree is the files as folders first, then files, each by name.
pub fn file_tree(files: &[PullFile]) -> Vec<Node> {
    let mut root = Dir::default();
    for (ix, f) in files.iter().enumerate() {
        let parts: Vec<&str> = f.path.split('/').collect();
        let mut dir = &mut root;
        for part in &parts[..parts.len() - 1] {
            dir = dir.dirs.entry(part.to_string()).or_default();
        }
        dir.files.push(ix);
    }
    fn build(dir: &Dir, prefix: &str, files: &[PullFile]) -> Vec<Node> {
        let mut out = Vec::new();
        for (name, sub) in &dir.dirs {
            let (mut name, mut sub) = (name.clone(), sub);
            while sub.files.is_empty() && sub.dirs.len() == 1 {
                let (only, inner) = sub.dirs.iter().next().expect("one");
                name = format!("{name}/{only}");
                sub = inner;
            }
            let path = format!("{prefix}{name}");
            out.push(Node::Dir {
                children: build(sub, &format!("{path}/"), files),
                name,
                path,
            });
        }
        let mut leaves: Vec<Node> = dir
            .files
            .iter()
            .map(|&ix| Node::File {
                name: files[ix].path.rsplit('/').next().unwrap_or("").to_string(),
                ix,
            })
            .collect();
        leaves.sort_by(|a, b| match (a, b) {
            (Node::File { name: x, .. }, Node::File { name: y, .. }) => x.cmp(y),
            _ => std::cmp::Ordering::Equal,
        });
        out.extend(leaves);
        out
    }
    build(&root, "", files)
}

/// tree_order is the files' indexes as the tree shows them, top to bottom.
pub fn tree_order(nodes: &[Node]) -> Vec<usize> {
    nodes
        .iter()
        .flat_map(|n| match n {
            Node::File { ix, .. } => vec![*ix],
            Node::Dir { children, .. } => tree_order(children),
        })
        .collect()
}

/// first_to_review is where a review starts: the tree's first file
/// somebody wrote, or its first at all.
pub fn first_to_review(files: &[PullFile]) -> Option<usize> {
    let order = tree_order(&file_tree(files));
    order
        .iter()
        .copied()
        .find(|&ix| file_kind(&files[ix]) == FileKind::Code)
        .or(order.first().copied())
}

/// How the files are shown: a tree to pick from, or every diff in a list.
#[derive(Clone, Copy, PartialEq)]
pub enum Layout {
    Tree,
    List,
}

const LAYOUT_KEY: &str = "pullsFilesView";

enum Patch {
    Loading,
    Ready(Rc<Vec<DiffLine>>),
    Failed(String),
}

enum Source {
    /// An agent's diff: every patch came with it.
    Whole,
    /// A pull request's: each patch is read when its file opens.
    Pull {
        client: Client,
        project: String,
        number: i64,
    },
}

pub struct DiffView {
    id: SharedString,
    source: Source,
    files: Option<Result<Vec<PullFile>, String>>,
    truncated: bool,
    patches: HashMap<String, Patch>,
    layout: Layout,
    selected: Option<usize>,
    folded: HashSet<String>,
    open: HashSet<String>,
    focus: FocusHandle,
    pane: ScrollHandle,
}

impl DiffView {
    fn make(id: &str, source: Source, cx: &mut Context<Self>) -> Self {
        let layout = match prefs::get(LAYOUT_KEY).as_ref().and_then(|v| v.as_str()) {
            Some("list") => Layout::List,
            _ => Layout::Tree,
        };
        Self {
            id: id.to_string().into(),
            source,
            files: None,
            truncated: false,
            patches: HashMap::new(),
            layout,
            selected: None,
            folded: HashSet::new(),
            open: HashSet::new(),
            focus: cx.focus_handle(),
            pane: ScrollHandle::new(),
        }
    }

    pub fn focus_handle(&self) -> FocusHandle {
        self.focus.clone()
    }

    /// text shows git's diff of an agent's work.
    pub fn text(id: &str, text: &str, cx: &mut Context<Self>) -> Self {
        let mut this = Self::make(id, Source::Whole, cx);
        let parsed = split_git_diff(text);
        for (f, p) in &parsed {
            this.patches
                .insert(f.path.clone(), Patch::Ready(Rc::new(parse_patch(p))));
        }
        this.set_files(parsed.into_iter().map(|(f, _)| f).collect());
        this
    }

    /// pull shows a pull request's files, read from the daemon.
    pub fn pull(client: Client, project: String, number: i64, cx: &mut Context<Self>) -> Self {
        let path = format!("/v1/projects/{project}/pulls/{number}/files");
        let mut this = Self::make(
            &format!("pull-{number}"),
            Source::Pull {
                client: client.clone(),
                project,
                number,
            },
            cx,
        );
        cx.spawn(async move |this, cx| {
            let res = client.get::<PullRequestFiles>(&path).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(f) => {
                        this.truncated = f.truncated;
                        this.set_files(f.files);
                        this.load(cx);
                    }
                    Err(e) => this.files = Some(Err(e.to_string())),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
        this.files = None;
        this
    }

    fn set_files(&mut self, files: Vec<PullFile>) {
        self.open = initially_open(&files);
        self.selected = first_to_review(&files);
        self.files = Some(Ok(files));
    }

    fn list(&self) -> &[PullFile] {
        match &self.files {
            Some(Ok(f)) => f,
            _ => &[],
        }
    }

    /// load reads the next patch the view needs: the picked file's in the
    /// tree, the open ones' in the list, one after another.
    fn load(&mut self, cx: &mut Context<Self>) {
        let Source::Pull {
            client,
            project,
            number,
        } = &self.source
        else {
            return;
        };
        if self.patches.values().any(|p| matches!(p, Patch::Loading)) {
            return;
        }
        let files = self.list();
        let next = match self.layout {
            Layout::Tree => self.selected.and_then(|ix| files.get(ix)),
            Layout::List => files.iter().find(|f| {
                self.open.contains(&f.path) && f.has_diff && !self.patches.contains_key(&f.path)
            }),
        };
        let Some(f) = next.filter(|f| f.has_diff && !self.patches.contains_key(&f.path)) else {
            return;
        };
        let file = f.path.clone();
        let path = format!(
            "/v1/projects/{project}/pulls/{number}/diff?path={}",
            enc(&file)
        );
        let client = client.clone();
        self.patches.insert(file.clone(), Patch::Loading);
        cx.spawn(async move |this, cx| {
            let res = client.get::<PullFileDiff>(&path).await;
            this.update(cx, |this, cx| {
                this.patches.insert(
                    file,
                    match res {
                        Ok(d) => Patch::Ready(Rc::new(parse_patch(&d.patch))),
                        Err(e) => Patch::Failed(e.to_string()),
                    },
                );
                this.load(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_layout(&mut self, layout: Layout, cx: &mut Context<Self>) {
        self.layout = layout;
        prefs::set(
            LAYOUT_KEY,
            serde_json::Value::String(
                if layout == Layout::List {
                    "list"
                } else {
                    "tree"
                }
                .into(),
            ),
        );
        self.load(cx);
        cx.notify();
    }

    fn select(&mut self, ix: usize, cx: &mut Context<Self>) {
        self.selected = Some(ix);
        self.pane.set_offset(point(px(0.), px(0.)));
        self.load(cx);
        cx.notify();
    }

    fn toggle_open(&mut self, path: &str, cx: &mut Context<Self>) {
        if !self.open.remove(path) {
            self.open.insert(path.to_string());
        }
        self.load(cx);
        cx.notify();
    }

    /// step moves through the tree's files with ↑ ↓.
    fn step(&mut self, by: isize, cx: &mut Context<Self>) {
        let order = tree_order(&file_tree(self.list()));
        if order.is_empty() {
            return;
        }
        let at = self
            .selected
            .and_then(|s| order.iter().position(|&ix| ix == s))
            .unwrap_or(0) as isize;
        let to = (at + by).clamp(0, order.len() as isize - 1) as usize;
        self.select(order[to], cx);
    }
}

/// enc escapes a query value.
fn enc(s: &str) -> String {
    s.bytes()
        .map(|b| match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                (b as char).to_string()
            }
            _ => format!("%{b:02X}"),
        })
        .collect()
}

fn counts(add: i64, del: i64) -> AnyElement {
    h_flex()
        .flex_none()
        .gap_1p5()
        .font_family(theme::MONO)
        .text_xs()
        .child(div().text_color(theme::running()).child(format!("+{add}")))
        .child(div().text_color(theme::error()).child(format!("−{del}")))
        .into_any_element()
}

/// summary is what a file's header says: its path, what happened to it, and
/// how much (FileSummary).
fn summary(f: &PullFile) -> Vec<AnyElement> {
    let status = match f.status.as_str() {
        "added" => Some(t("pulls.detail.status.added")),
        "removed" => Some(t("pulls.detail.status.removed")),
        "renamed" => Some(t("pulls.detail.status.renamed")),
        "copied" => Some(t("pulls.detail.status.copied")),
        _ => None,
    };
    let mut out = vec![
        div()
            .min_w_0()
            .truncate()
            .font_family(theme::MONO)
            .text_color(theme::fg())
            .child(f.path.clone())
            .into_any_element(),
    ];
    if !f.previous_path.is_empty() {
        out.push(
            div()
                .min_w_0()
                .truncate()
                .font_family(theme::MONO)
                .text_color(theme::faint())
                .child(tv(
                    "pulls.detail.renamedFrom",
                    &[("path", (&f.previous_path).into())],
                ))
                .into_any_element(),
        );
    }
    if let Some(s) = status {
        out.push(
            div()
                .flex_none()
                .text_color(theme::faint())
                .child(s)
                .into_any_element(),
        );
    }
    if let Some(k) = kind_label(file_kind(f)) {
        out.push(
            div()
                .flex_none()
                .px_1p5()
                .rounded_sm()
                .bg(theme::hover())
                .text_xs()
                .text_color(theme::muted())
                .child(k)
                .into_any_element(),
        );
    }
    out.push(div().flex_1().into_any_element());
    out.push(counts(f.additions, f.deletions));
    out
}

/// table is one file's lines (DiffTable), scrolling sideways when long.
pub fn table(id: impl Into<ElementId>, lines: &[DiffLine]) -> AnyElement {
    let num = |n: Option<u32>| {
        div()
            .flex_none()
            .w(px(44.))
            .px_2()
            .text_right()
            .text_color(theme::faint())
            .child(n.map(|n| n.to_string()).unwrap_or_default())
    };
    let shown = lines.len().min(MAX_LINES);
    let rows = lines[..shown].iter().map(|l| {
        let (bg, sign) = match l.kind {
            LineKind::Add => (theme::running().opacity(0.12), "+"),
            LineKind::Del => (theme::error().opacity(0.12), "−"),
            LineKind::Hunk => (theme::accent().opacity(0.10), ""),
            _ => (transparent_black(), " "),
        };
        let code = matches!(l.kind, LineKind::Add | LineKind::Del | LineKind::Context);
        h_flex()
            .bg(bg)
            .child(num(if code { l.old } else { None }))
            .child(num(if code { l.new } else { None }))
            .child(
                div()
                    .flex_none()
                    .w(px(16.))
                    .text_color(theme::muted())
                    .child(sign),
            )
            .child(
                div()
                    .flex_none()
                    .pr_4()
                    .whitespace_nowrap()
                    .map(|el| match l.kind {
                        LineKind::Hunk => el.text_color(theme::muted()),
                        LineKind::Note => el.text_color(theme::faint()).italic(),
                        _ => el.text_color(theme::fg()),
                    })
                    .child(if l.text.is_empty() {
                        " ".to_string()
                    } else {
                        l.text.replace('\t', "    ")
                    }),
            )
    });
    h_flex()
        .id(id)
        .w_full()
        .overflow_x_scroll()
        .bg(theme::bg())
        .border_t_1()
        .border_color(theme::border())
        .child(
            v_flex()
                .flex_none()
                .min_w_full()
                .py_1()
                .font_family(theme::MONO)
                .text_xs()
                .line_height(px(19.))
                .children(rows)
                .when(lines.len() > shown, |el| {
                    el.child(div().px_3().py_2().text_color(theme::faint()).child(tv(
                        "native.diff.moreLines",
                        &[("count", (lines.len() - shown).into())],
                    )))
                }),
        )
        .into_any_element()
}

impl DiffView {
    fn body(&self, f: &PullFile, ix: usize) -> AnyElement {
        let note = |text: String, color: Hsla| {
            div()
                .px_3()
                .py_2()
                .border_t_1()
                .border_color(theme::border())
                .text_xs()
                .text_color(color)
                .child(text)
                .into_any_element()
        };
        if !f.has_diff {
            return note(t("pulls.detail.noDiff"), theme::muted());
        }
        match self.patches.get(&f.path) {
            Some(Patch::Ready(lines)) => {
                table((SharedString::from(format!("{}-t", self.id)), ix), lines)
            }
            Some(Patch::Failed(e)) => note(
                tv("pulls.detail.diffFailed", &[("error", e.into())]),
                theme::error(),
            ),
            _ => note(t("common.loading"), theme::faint()),
        }
    }

    fn render_tree(&self, nodes: &[Node], depth: usize, cx: &mut Context<Self>) -> Vec<AnyElement> {
        let files = self.list();
        let mut out = Vec::new();
        for n in nodes {
            match n {
                Node::Dir {
                    name,
                    path,
                    children,
                } => {
                    let folded = self.folded.contains(path);
                    let p = path.clone();
                    out.push(
                        h_flex()
                            .id(SharedString::from(format!("{}-d-{path}", self.id)))
                            .gap_1p5()
                            .pl(px(12. + depth as f32 * 13.))
                            .pr_3()
                            .py_1()
                            .text_xs()
                            .text_color(theme::muted())
                            .cursor_pointer()
                            .hover(|s| s.bg(theme::raised()).text_color(theme::fg()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                if !this.folded.remove(&p) {
                                    this.folded.insert(p.clone());
                                }
                                cx.notify();
                            }))
                            .child(div().flex_none().w(px(10.)).child(if folded {
                                "▸"
                            } else {
                                "▾"
                            }))
                            .child(div().min_w_0().truncate().child(name.clone()))
                            .into_any_element(),
                    );
                    if !folded {
                        out.extend(self.render_tree(children, depth + 1, cx));
                    }
                }
                Node::File { name, ix } => {
                    let f = &files[*ix];
                    let on = self.selected == Some(*ix);
                    let tint = match f.status.as_str() {
                        "added" => theme::running(),
                        "removed" => theme::error(),
                        "renamed" => theme::accent(),
                        _ => theme::faint(),
                    };
                    let ix = *ix;
                    out.push(
                        h_flex()
                            .id(SharedString::from(format!("{}-f-{}", self.id, f.path)))
                            .gap_1p5()
                            .pl(px(12. + depth as f32 * 13. + 16.))
                            .pr_3()
                            .py_1()
                            .text_xs()
                            .cursor_pointer()
                            .when(on, |el| el.bg(theme::hover()))
                            .hover(|s| s.bg(theme::raised()))
                            .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.focus.focus(window, cx);
                                this.select(ix, cx)
                            }))
                            .child(div().flex_none().size(px(6.)).rounded_full().bg(tint))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(if on { theme::fg() } else { theme::muted() })
                                    .when(f.status == "removed", |el| el.line_through())
                                    .child(name.clone()),
                            )
                            .child(counts(f.additions, f.deletions))
                            .into_any_element(),
                    );
                }
            }
        }
        out
    }

    fn card(&self, f: &PullFile, ix: usize, header: AnyElement, open: bool) -> AnyElement {
        v_flex()
            .flex_none()
            .w_full()
            .min_w_0()
            .rounded_lg()
            .border_1()
            .border_color(theme::border())
            .overflow_hidden()
            .child(header)
            .when(open, |el| el.child(self.body(f, ix)))
            .into_any_element()
    }
}

impl Render for DiffView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let files = match &self.files {
            None => {
                return div()
                    .p_6()
                    .text_sm()
                    .text_color(theme::faint())
                    .child(t("common.loading"))
                    .into_any_element();
            }
            Some(Err(e)) => {
                return div()
                    .p_6()
                    .text_sm()
                    .text_color(theme::error())
                    .child(tv("pulls.detail.filesFailed", &[("error", e.into())]))
                    .into_any_element();
            }
            Some(Ok(f)) => f.clone(),
        };
        let (add, del) = files
            .iter()
            .fold((0, 0), |(a, d), f| (a + f.additions, d + f.deletions));
        let switch = |id: &str, label: String, on: bool, layout: Layout, cx: &mut Context<Self>| {
            div()
                .id(SharedString::from(format!("{}-{id}", self.id)))
                .px_2()
                .py_0p5()
                .rounded_md()
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .when(on, |el| el.bg(theme::hover()).text_color(theme::fg()))
                .when(!on, |el| {
                    el.text_color(theme::muted())
                        .cursor_pointer()
                        .hover(|s| s.text_color(theme::fg()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.set_layout(layout, cx)
                        }))
                })
                .child(label)
        };
        let bar = h_flex()
            .flex_none()
            .gap_2()
            .px_4()
            .py_2()
            .border_b_1()
            .border_color(theme::border())
            .child(
                div()
                    .text_xs()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::muted())
                    .child(tv("pulls.detail.files", &[("count", files.len().into())])),
            )
            .child(counts(add, del))
            .child(div().flex_1())
            .child(
                h_flex()
                    .p(px(2.))
                    .gap(px(2.))
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::border())
                    .child(switch(
                        "tree",
                        t("pulls.detail.view.tree"),
                        self.layout == Layout::Tree,
                        Layout::Tree,
                        cx,
                    ))
                    .child(switch(
                        "list",
                        t("pulls.detail.view.list"),
                        self.layout == Layout::List,
                        Layout::List,
                        cx,
                    )),
            );
        let truncated = self.truncated.then(|| {
            div()
                .px_4()
                .pt_2()
                .text_xs()
                .text_color(theme::muted())
                .child(tv(
                    "pulls.detail.filesTruncated",
                    &[("count", files.len().into())],
                ))
        });
        let main = match self.layout {
            Layout::Tree => {
                let tree = file_tree(&files);
                let picked = self.selected.and_then(|ix| files.get(ix).map(|f| (ix, f)));
                h_flex()
                    .flex_1()
                    .min_h_0()
                    .items_start()
                    .child(
                        v_flex()
                            .id(SharedString::from(format!("{}-tree", self.id)))
                            .w(px(280.))
                            .flex_none()
                            .h_full()
                            .py_2()
                            .overflow_y_scroll()
                            .border_r_1()
                            .border_color(theme::border())
                            .children(self.render_tree(&tree, 0, cx)),
                    )
                    .child(
                        div()
                            .id(SharedString::from(format!("{}-pane", self.id)))
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .p_4()
                            .overflow_y_scroll()
                            .track_scroll(&self.pane)
                            .child(match picked {
                                Some((ix, f)) => {
                                    let header = h_flex()
                                        .gap_2()
                                        .px_3()
                                        .py_2()
                                        .bg(theme::raised())
                                        .text_xs()
                                        .children(summary(f))
                                        .into_any_element();
                                    self.card(f, ix, header, true)
                                }
                                None => div()
                                    .text_sm()
                                    .text_color(theme::muted())
                                    .child(t("pulls.detail.pickFile"))
                                    .into_any_element(),
                            }),
                    )
                    .into_any_element()
            }
            Layout::List => v_flex()
                .id(SharedString::from(format!("{}-list", self.id)))
                .flex_1()
                .min_h_0()
                .px_4()
                .py_3()
                .gap_2()
                .overflow_y_scroll()
                .children(files.iter().enumerate().map(|(ix, f)| {
                    let open = self.open.contains(&f.path);
                    let path = f.path.clone();
                    let header = h_flex()
                        .id(SharedString::from(format!("{}-h-{}", self.id, f.path)))
                        .gap_2()
                        .px_3()
                        .py_2()
                        .bg(theme::raised())
                        .text_xs()
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::hover()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.toggle_open(&path, cx)
                        }))
                        .child(
                            div()
                                .flex_none()
                                .w(px(10.))
                                .text_color(theme::muted())
                                .child(if open { "▾" } else { "▸" }),
                        )
                        .children(summary(f))
                        .into_any_element();
                    self.card(f, ix, header, open)
                }))
                .into_any_element(),
        };
        v_flex()
            .id(self.id.clone())
            .track_focus(&self.focus)
            .size_full()
            .bg(theme::panel())
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                if this.layout != Layout::Tree {
                    return;
                }
                match ev.keystroke.key.as_str() {
                    "down" => this.step(1, cx),
                    "up" => this.step(-1, cx),
                    _ => return,
                }
                cx.stop_propagation();
            }))
            .child(bar)
            .children(truncated)
            .child(main)
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{
        FileKind, LineKind, Node, PullFile, file_kind, file_tree, first_to_review, initially_open,
        parse_patch, split_git_diff, tree_order,
    };

    fn pf(path: &str, add: i64, del: i64) -> PullFile {
        PullFile {
            path: path.into(),
            status: "modified".into(),
            additions: add,
            deletions: del,
            has_diff: true,
            ..Default::default()
        }
    }

    #[test]
    fn a_patch_numbers_both_sides() {
        let lines = parse_patch(
            "@@ -10,3 +10,4 @@ fn a()\n keep\n-old\n+new\n+more\n\\ No newline at end of file",
        );
        let kinds: Vec<_> = lines.iter().map(|l| l.kind).collect();
        assert_eq!(
            kinds,
            [
                LineKind::Hunk,
                LineKind::Context,
                LineKind::Del,
                LineKind::Add,
                LineKind::Add,
                LineKind::Note
            ]
        );
        assert_eq!((lines[1].old, lines[1].new), (Some(10), Some(10)));
        assert_eq!(lines[2].old, Some(11));
        assert_eq!((lines[3].new, lines[4].new), (Some(11), Some(12)));
        assert_eq!(lines[5].text, "No newline at end of file");
    }

    #[test]
    fn git_diff_splits_into_files() {
        let text = "diff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,2 @@\n x\n-y\n+z\ndiff --git a/new.txt b/new.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+hi\ndiff --git a/old.md b/moved.md\nsimilarity index 100%\nrename from old.md\nrename to moved.md\ndiff --git a/i.png b/i.png\nBinary files a/i.png and b/i.png differ\n";
        let files = split_git_diff(text);
        let got: Vec<_> = files
            .iter()
            .map(|(f, _)| {
                (
                    f.path.as_str(),
                    f.status.as_str(),
                    f.additions,
                    f.deletions,
                    f.has_diff,
                )
            })
            .collect();
        assert_eq!(
            got,
            [
                ("a.go", "modified", 1, 1, true),
                ("new.txt", "added", 1, 0, true),
                ("moved.md", "renamed", 0, 0, false),
                ("i.png", "modified", 0, 0, false),
            ]
        );
        assert_eq!(files[2].0.previous_path, "old.md");
        assert!(files[0].1.starts_with("@@ -1,2 +1,2 @@"));
    }

    #[test]
    fn kinds_and_what_starts_open() {
        assert_eq!(
            file_kind(&pf("native/Cargo.lock", 3, 0)),
            FileKind::Lockfile
        );
        assert_eq!(
            file_kind(&pf("desktop/src/shared/api.ts", 3, 0)),
            FileKind::Generated
        );
        assert_eq!(file_kind(&pf("a/b.golden", 3, 0)), FileKind::Golden);
        assert_eq!(file_kind(&pf("big.go", 300, 200)), FileKind::Large);
        let mut bin = pf("i.png", 0, 0);
        bin.has_diff = false;
        assert_eq!(file_kind(&bin), FileKind::Binary);
        let files = [
            pf("go.sum", 1, 1),
            pf("a.go", 1, 1),
            pf("b.go", 200, 1),
            pf("c.go", 1, 1),
            pf("d.go", 1, 1),
            pf("e.go", 1, 1),
        ];
        let open = initially_open(&files);
        assert_eq!(open.len(), 3);
        assert!(open.contains("a.go") && open.contains("c.go") && open.contains("d.go"));
    }

    #[test]
    fn the_tree_folds_single_folders_and_starts_on_code() {
        let files = [
            pf("desktop/src/renderer/App.tsx", 1, 1),
            pf("desktop/src/renderer/lib/a.ts", 1, 1),
            pf("Cargo.lock", 1, 1),
            pf("README.md", 1, 1),
        ];
        let tree = file_tree(&files);
        match &tree[0] {
            Node::Dir { name, .. } => assert_eq!(name, "desktop/src/renderer"),
            n => panic!("{n:?}"),
        }
        assert_eq!(tree_order(&tree), [1, 0, 2, 3]);
        assert_eq!(first_to_review(&files), Some(1));
    }
}
