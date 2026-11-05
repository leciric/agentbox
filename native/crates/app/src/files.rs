//! A worktree's files beside a chat (desktop/src/renderer/components/files):
//! its tree, read a folder at a time as folders open (GET …/files/list), each
//! file marked with what changed since its base (…/files/status), and the file
//! picked beside it (…/files/read): text with its line numbers, an image, or
//! what it is when it can't be shown, and for a changed file its changes
//! since its base as a diff. The daemon watches the folders listed and says
//! what moved (EventFiles): those are read again.
use std::collections::{HashMap, HashSet};
use std::sync::Arc;
use std::time::Duration;

use agentbox_api::{
    Client, FILE_ADDED, FILE_DELETED, FILE_MODIFIED, FILE_UNTRACKED, FileContent, FileList,
    FileStatus, FilesChange,
};
use agentbox_i18n::{t, tv};
use base64::Engine as _;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use crate::diff::{self, DiffLine, LineKind};
use crate::theme;

/// The most lines the viewer lays out at once.
const MAX_SHOWN: usize = 3000;

pub struct FileBrowser {
    client: Client,
    /// The routes' base: /v1/agents/<p>/<a>/files or /v1/projects/<p>/files.
    base: String,
    /// The chat's ref, as EventFiles names it.
    pub r#ref: String,
    dirs: HashMap<String, Result<FileList, String>>,
    open: HashSet<String>,
    status: Option<FileStatus>,
    selected: Option<String>,
    content: Option<Result<FileContent, String>>,
    /// The picked file shows its changes since its base.
    show_diff: bool,
    /// Only the changed files are listed, not the tree.
    changes_only: bool,
    image: Option<Arc<Image>>,
    _poll: Task<()>,
}

impl FileBrowser {
    pub fn new(client: Client, base: String, r#ref: String, cx: &mut Context<Self>) -> Self {
        // Asking for the status every half minute keeps the daemon watching.
        let poll = cx.spawn(async move |this, cx| {
            loop {
                cx.background_executor()
                    .timer(Duration::from_secs(30))
                    .await;
                if this
                    .update(cx, |this, cx| this.load_status(false, cx))
                    .is_err()
                {
                    return;
                }
            }
        });
        let mut this = Self {
            client,
            base,
            r#ref,
            dirs: HashMap::new(),
            open: HashSet::new(),
            status: None,
            selected: None,
            content: None,
            show_diff: false,
            changes_only: false,
            image: None,
            _poll: poll,
        };
        this.list(String::new(), cx);
        this.load_status(true, cx);
        this
    }

    fn list(&mut self, path: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = format!("{}/list?path={}", self.base, encode(&path));
        cx.spawn(async move |this, cx| {
            let res = client.get::<FileList>(&url).await;
            this.update(cx, |this, cx| {
                this.dirs.insert(path, res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_status(&mut self, fresh: bool, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = format!(
            "{}/status{}",
            self.base,
            if fresh { "?fresh=1" } else { "" }
        );
        cx.spawn(async move |this, cx| {
            if let Ok(s) = client.get::<FileStatus>(&url).await {
                this.update(cx, |this, cx| {
                    this.status = Some(s);
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    fn read(&mut self, path: String, full: bool, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = format!(
            "{}/read?path={}&base=1{}",
            self.base,
            encode(&path),
            if full { "&full=1" } else { "" }
        );
        cx.spawn(async move |this, cx| {
            let res = client.get::<FileContent>(&url).await;
            this.update(cx, |this, cx| {
                if this.selected.as_deref() != Some(path.as_str()) {
                    return;
                }
                this.image = res.as_ref().ok().and_then(image_of);
                this.content = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// open_file picks a file, as a link to it in a chat does too.
    pub fn open_file(&mut self, path: String, cx: &mut Context<Self>) {
        self.selected = Some(path.clone());
        self.content = None;
        self.image = None;
        self.show_diff = self
            .status
            .as_ref()
            .is_some_and(|s| s.changes.get(&path).is_some_and(|c| c == FILE_MODIFIED));
        self.read(path, false, cx);
        cx.notify();
    }

    /// apply reads again what an EventFiles says moved.
    pub fn apply(&mut self, change: &FilesChange, cx: &mut Context<Self>) {
        for d in &change.dirs {
            if self.dirs.contains_key(d) {
                self.list(d.clone(), cx);
            }
        }
        if let Some(sel) = self.selected.clone()
            && change.files.iter().any(|f| f == &sel)
        {
            self.read(sel, false, cx);
        }
        self.load_status(false, cx);
    }

    fn refresh(&mut self, cx: &mut Context<Self>) {
        let dirs: Vec<String> = self.dirs.keys().cloned().collect();
        for d in dirs {
            self.list(d, cx);
        }
        if let Some(sel) = self.selected.clone() {
            self.read(sel, false, cx);
        }
        self.load_status(true, cx);
    }

    fn change_of(&self, path: &str) -> Option<&str> {
        self.status.as_ref()?.changes.get(path).map(String::as_str)
    }

    /// changed_under is whether something below a folder changed.
    fn changed_under(&self, dir: &str) -> bool {
        let prefix = format!("{dir}/");
        self.status
            .as_ref()
            .is_some_and(|s| s.changes.keys().any(|k| k.starts_with(&prefix)))
    }

    /// rows are the tree's lines as shown: (depth, path, name, dir).
    fn rows(&self) -> Vec<(usize, String, String, bool)> {
        let mut out = Vec::new();
        self.walk("", 0, &mut out);
        out
    }

    fn walk(&self, dir: &str, depth: usize, out: &mut Vec<(usize, String, String, bool)>) {
        let Some(Ok(list)) = self.dirs.get(dir) else {
            return;
        };
        for e in &list.entries {
            out.push((depth, e.path.clone(), e.name.clone(), e.dir));
            if e.dir && self.open.contains(&e.path) {
                self.walk(&e.path, depth + 1, out);
            }
        }
    }

    fn toggle_dir(&mut self, path: String, cx: &mut Context<Self>) {
        if !self.open.remove(&path) {
            self.open.insert(path.clone());
            if !self.dirs.contains_key(&path) {
                self.list(path, cx);
            }
        }
        cx.notify();
    }

    fn render_tree(&self, cx: &mut Context<Self>) -> AnyElement {
        let root = self.dirs.get("");
        let body: AnyElement = match root {
            None => note(t("common.loading")),
            Some(Err(_)) => note(t("files.tree.unavailable")),
            Some(Ok(_)) if self.changes_only => self.render_changes(cx),
            Some(Ok(list)) => v_flex()
                .children(self.rows().into_iter().map(|(depth, path, name, dir)| {
                    let change = if dir {
                        self.changed_under(&path).then_some("dot")
                    } else {
                        self.change_of(&path)
                    };
                    let open = self.open.contains(&path);
                    let selected = self.selected.as_deref() == Some(path.as_str());
                    let p = path.clone();
                    h_flex()
                        .id(SharedString::from(format!("file-{path}")))
                        .h(px(22.))
                        .pl(px(8. + depth as f32 * 12.))
                        .pr_2()
                        .gap_1()
                        .rounded_sm()
                        .cursor_pointer()
                        .text_xs()
                        .when(selected, |el| el.bg(theme::hover()))
                        .hover(|el| el.bg(theme::raised()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            if dir {
                                this.toggle_dir(p.clone(), cx)
                            } else {
                                this.open_file(p.clone(), cx)
                            }
                        }))
                        .child(
                            div()
                                .w(px(10.))
                                .flex_none()
                                .text_color(theme::faint())
                                .child(if !dir {
                                    ""
                                } else if open {
                                    "▾"
                                } else {
                                    "▸"
                                }),
                        )
                        .child(
                            div()
                                .min_w_0()
                                .flex_1()
                                .truncate()
                                .text_color(match change {
                                    Some(c) if !dir => change_color(c),
                                    _ => theme::fg(),
                                })
                                .child(name),
                        )
                        .when_some(change, |el, c| {
                            el.child(if dir {
                                div().size(px(5.)).rounded_full().bg(theme::starting())
                            } else {
                                div()
                                    .flex_none()
                                    .text_color(change_color(c))
                                    .child(change_letter(c))
                            })
                        })
                        .into_any_element()
                }))
                .when(list.truncated, |el| el.child(note(t("files.tree.more"))))
                .into_any_element(),
        };
        div()
            .id("files-tree")
            .size_full()
            .overflow_y_scroll()
            .py_1()
            .child(body)
            .into_any_element()
    }

    /// render_changes lists only what changed, each by its path.
    fn render_changes(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(status) = &self.status else {
            return note(t("common.loading"));
        };
        v_flex()
            .children(status.changes.iter().map(|(path, c)| {
                let p = path.clone();
                let selected = self.selected.as_deref() == Some(path.as_str());
                h_flex()
                    .id(SharedString::from(format!("change-{path}")))
                    .h(px(22.))
                    .px_2()
                    .gap_1p5()
                    .rounded_sm()
                    .cursor_pointer()
                    .text_xs()
                    .when(selected, |el| el.bg(theme::hover()))
                    .hover(|el| el.bg(theme::raised()))
                    .when(!path.ends_with('/'), |el| {
                        el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.open_file(p.clone(), cx)
                        }))
                    })
                    .child(
                        div()
                            .flex_none()
                            .w(px(10.))
                            .text_color(change_color(c))
                            .child(change_letter(c)),
                    )
                    .child(
                        div()
                            .min_w_0()
                            .flex_1()
                            .truncate()
                            .font_family(theme::mono())
                            .text_color(theme::fg())
                            .child(path.clone()),
                    )
                    .into_any_element()
            }))
            .into_any_element()
    }

    fn render_viewer(&self, cx: &mut Context<Self>) -> AnyElement {
        let Some(path) = self.selected.clone() else {
            return div()
                .size_full()
                .flex()
                .items_center()
                .justify_center()
                .text_xs()
                .text_color(theme::faint())
                .child(t("files.panel.pick"))
                .into_any_element();
        };
        let change = self.change_of(&path).map(str::to_string);
        let base = self
            .status
            .as_ref()
            .map(|s| short_base(&s.base))
            .unwrap_or_default();
        let copy_path = path.clone();
        let header = h_flex()
            .flex_none()
            .h(px(30.))
            .px_2()
            .gap_1p5()
            .border_b_1()
            .border_color(theme::border())
            .child(
                div()
                    .min_w_0()
                    .flex_1()
                    .truncate()
                    .text_xs()
                    .font_family(theme::mono())
                    .text_color(theme::fg())
                    .child(path.clone()),
            )
            .when(
                change.as_deref() == Some(FILE_MODIFIED) || change.as_deref() == Some(FILE_DELETED),
                |el| {
                    el.child(
                        chip(
                            "file-diff",
                            tv("files.viewer.diff", &[("base", (&base).into())]),
                            self.show_diff,
                        )
                        .on_click(cx.listener(
                            |this, _: &ClickEvent, _, cx| {
                                this.show_diff = !this.show_diff;
                                cx.notify();
                            },
                        )),
                    )
                },
            )
            .child(
                icon_button("file-copy", "❐", t("files.viewer.copyRelative")).on_click(
                    move |_, _, cx| {
                        cx.write_to_clipboard(ClipboardItem::new_string(copy_path.clone()));
                        crate::toasts::info(cx, t("common.copied"));
                    },
                ),
            )
            .child(
                icon_button("file-close", "✕", t("files.viewer.close")).on_click(cx.listener(
                    |this, _: &ClickEvent, _, cx| {
                        this.selected = None;
                        this.content = None;
                        cx.notify();
                    },
                )),
            );
        let body: AnyElement = match &self.content {
            None => note(t("common.loading")),
            Some(Err(_)) => note(t("files.viewer.gone")),
            Some(Ok(c)) => self.render_content(c, cx),
        };
        v_flex()
            .size_full()
            .child(header)
            .child(
                div()
                    .id("file-body")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .child(body),
            )
            .into_any_element()
    }

    fn render_content(&self, c: &FileContent, cx: &mut Context<Self>) -> AnyElement {
        if self.show_diff && (!c.base_text.is_empty() || c.base_missing) {
            let lines = line_diff(&c.base_text, &c.text);
            return diff::table("file-diff-table", &lines);
        }
        if let Some(image) = &self.image {
            return div()
                .p_3()
                .child(
                    img(image.clone())
                        .max_w_full()
                        .object_fit(ObjectFit::Contain),
                )
                .into_any_element();
        }
        if c.too_large && !c.image.is_empty() {
            return note(tv(
                "files.viewer.tooLarge",
                &[("size", (&human_bytes(c.size)).into())],
            ));
        }
        if c.binary || c.pdf || c.sqlite {
            return note(tv(
                "files.viewer.binary",
                &[("size", (&human_bytes(c.size)).into())],
            ));
        }
        let lines: Vec<&str> = c.text.split('\n').collect();
        let shown = lines.len().min(MAX_SHOWN);
        let width = shown.to_string().len() as f32 * 7.5 + 16.;
        v_flex()
            .child(
                div().id("file-text").w_full().overflow_x_scroll().child(
                    v_flex()
                        .flex_none()
                        .min_w_full()
                        .py_1()
                        .font_family(theme::mono())
                        .text_xs()
                        .line_height(px(18.))
                        .children(lines[..shown].iter().enumerate().map(|(i, l)| {
                            h_flex()
                                .child(
                                    div()
                                        .flex_none()
                                        .w(px(width))
                                        .pr_2()
                                        .text_right()
                                        .text_color(theme::faint())
                                        .child((i + 1).to_string()),
                                )
                                .child(
                                    div()
                                        .flex_none()
                                        .pr_4()
                                        .whitespace_nowrap()
                                        .text_color(theme::fg())
                                        .child(if l.is_empty() {
                                            " ".to_string()
                                        } else {
                                            l.replace('\t', "    ")
                                        }),
                                )
                        })),
                ),
            )
            .when(c.truncated, |el| {
                let path = c.path.clone();
                el.child(
                    h_flex()
                        .px_3()
                        .py_2()
                        .gap_2()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(tv(
                            "files.viewer.truncated",
                            &[
                                ("shown", (&human_bytes(c.text.len() as i64)).into()),
                                ("size", (&human_bytes(c.size)).into()),
                            ],
                        ))
                        .child(
                            chip("file-load-all", t("files.viewer.loadAll"), false).on_click(
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.read(path.clone(), true, cx)
                                }),
                            ),
                        ),
                )
            })
            .into_any_element()
    }
}

impl Render for FileBrowser {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let count = self.status.as_ref().map_or(0, |s| s.changes.len());
        let base = self
            .status
            .as_ref()
            .map(|s| short_base(&s.base))
            .unwrap_or_default();
        v_flex()
            .size_full()
            .child(
                h_flex()
                    .flex_none()
                    .h(px(30.))
                    .px_2()
                    .gap_1p5()
                    .border_b_1()
                    .border_color(theme::border())
                    .child(
                        chip(
                            "files-changes",
                            tv(
                                "files.panel.changes",
                                &[("count", count.into()), ("base", (&base).into())],
                            ),
                            self.changes_only,
                        )
                        .when(count > 0, |el| {
                            el.on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.changes_only = !this.changes_only;
                                cx.notify();
                            }))
                        }),
                    )
                    .child(div().flex_1())
                    .child(
                        icon_button("files-refresh", "↻", t("files.panel.refresh"))
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.refresh(cx))),
                    ),
            )
            .child(
                h_flex()
                    .flex_1()
                    .min_h_0()
                    .child(
                        div()
                            .w(px(210.))
                            .flex_none()
                            .h_full()
                            .border_r_1()
                            .border_color(theme::border())
                            .child(self.render_tree(cx)),
                    )
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .h_full()
                            .child(self.render_viewer(cx)),
                    ),
            )
    }
}

/// image_of is an image file's picture, from its base64.
fn image_of(c: &FileContent) -> Option<Arc<Image>> {
    if c.image.is_empty() || c.data.is_empty() {
        return None;
    }
    let format = ImageFormat::from_mime_type(&c.image)?;
    let bytes = base64::engine::general_purpose::STANDARD
        .decode(c.data.as_bytes())
        .ok()?;
    Some(Arc::new(Image::from_bytes(format, bytes)))
}

fn change_letter(c: &str) -> &'static str {
    match c {
        FILE_ADDED => "A",
        FILE_DELETED => "D",
        FILE_UNTRACKED => "U",
        _ => "M",
    }
}

fn change_color(c: &str) -> Hsla {
    match c {
        FILE_ADDED | FILE_UNTRACKED => theme::running(),
        FILE_DELETED => theme::error(),
        _ => theme::starting(),
    }
}

/// short_base is a base as the header names it: a commit's first 7.
fn short_base(base: &str) -> String {
    if base.len() == 40 && base.chars().all(|c| c.is_ascii_hexdigit()) {
        base[..7].to_string()
    } else {
        base.to_string()
    }
}

fn note(text: String) -> AnyElement {
    div()
        .p_3()
        .text_xs()
        .text_color(theme::faint())
        .child(text)
        .into_any_element()
}

fn chip(id: &'static str, label: String, on: bool) -> Stateful<Div> {
    div()
        .id(id)
        .flex_none()
        .px_2()
        .py_0p5()
        .rounded_md()
        .text_xs()
        .cursor_pointer()
        .border_1()
        .border_color(if on {
            theme::accent().opacity(0.5)
        } else {
            theme::border()
        })
        .when(on, |el| el.bg(theme::accent().opacity(0.12)))
        .text_color(if on { theme::fg() } else { theme::muted() })
        .hover(|el| el.text_color(theme::fg()))
        .child(label)
}

fn icon_button(id: &'static str, glyph: &'static str, tip: String) -> Stateful<Div> {
    let tip: SharedString = tip.into();
    div()
        .id(id)
        .flex_none()
        .px_1p5()
        .rounded_md()
        .text_sm()
        .text_color(theme::muted())
        .cursor_pointer()
        .hover(|el| el.bg(theme::raised()).text_color(theme::fg()))
        .tooltip(move |window, cx| {
            gpui_component::tooltip::Tooltip::new(tip.clone()).build(window, cx)
        })
        .child(glyph)
}

/// encode escapes a query value.
pub fn encode(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        if b.is_ascii_alphanumeric() || b"-_.~/".contains(&b) {
            out.push(b as char);
        } else {
            out.push_str(&format!("%{b:02X}"));
        }
    }
    out
}

pub fn human_bytes(n: i64) -> String {
    let units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let (mut v, mut i) = (n as f64, 0);
    while v >= 1024. && i < units.len() - 1 {
        v /= 1024.;
        i += 1;
    }
    if i == 0 {
        format!("{n} B")
    } else {
        format!("{v:.1} {}", units[i])
    }
}

/// line_diff is base and text as a unified diff with three lines of context
/// round each change, for diff::table. Longer files than it can compare
/// line by line show as all removed and all added.
pub fn line_diff(base: &str, text: &str) -> Vec<DiffLine> {
    let a: Vec<&str> = if base.is_empty() {
        Vec::new()
    } else {
        base.split('\n').collect()
    };
    let b: Vec<&str> = if text.is_empty() {
        Vec::new()
    } else {
        text.split('\n').collect()
    };
    // Each line: (kind, old index, new index).
    let mut ops: Vec<(LineKind, usize, usize)> = Vec::new();
    if a.len() * b.len() > 4_000_000 {
        ops.extend((0..a.len()).map(|i| (LineKind::Del, i, 0)));
        ops.extend((0..b.len()).map(|j| (LineKind::Add, 0, j)));
    } else {
        let (n, m) = (a.len(), b.len());
        let mut lcs = vec![0u32; (n + 1) * (m + 1)];
        for i in (0..n).rev() {
            for j in (0..m).rev() {
                lcs[i * (m + 1) + j] = if a[i] == b[j] {
                    lcs[(i + 1) * (m + 1) + j + 1] + 1
                } else {
                    lcs[(i + 1) * (m + 1) + j].max(lcs[i * (m + 1) + j + 1])
                };
            }
        }
        let (mut i, mut j) = (0, 0);
        while i < n || j < m {
            if i < n && j < m && a[i] == b[j] {
                ops.push((LineKind::Context, i, j));
                i += 1;
                j += 1;
            } else if i < n && (j == m || lcs[(i + 1) * (m + 1) + j] >= lcs[i * (m + 1) + j + 1]) {
                // What went comes before what came, as git prints it.
                ops.push((LineKind::Del, i, j));
                i += 1;
            } else {
                ops.push((LineKind::Add, i, j));
                j += 1;
            }
        }
    }
    // Keep the changes and three lines round each, a hunk header per run.
    let near: Vec<bool> = (0..ops.len())
        .map(|k| {
            let lo = k.saturating_sub(3);
            let hi = (k + 3).min(ops.len().saturating_sub(1));
            (lo..=hi).any(|x| ops[x].0 != LineKind::Context)
        })
        .collect();
    let mut out = Vec::new();
    let mut in_hunk = false;
    for (k, &(kind, i, j)) in ops.iter().enumerate() {
        if !near[k] {
            in_hunk = false;
            continue;
        }
        if !in_hunk {
            out.push(DiffLine {
                kind: LineKind::Hunk,
                text: format!("@@ -{} +{} @@", i + 1, j + 1),
                old: None,
                new: None,
            });
            in_hunk = true;
        }
        let (text, old, new) = match kind {
            LineKind::Del => (a[i], Some(i as u32 + 1), None),
            LineKind::Add => (b[j], None, Some(j as u32 + 1)),
            _ => (b[j], Some(i as u32 + 1), Some(j as u32 + 1)),
        };
        out.push(DiffLine {
            kind,
            text: text.to_string(),
            old,
            new,
        });
    }
    out
}

#[cfg(test)]
mod tests {
    use super::{LineKind, encode, line_diff, short_base};

    #[test]
    fn a_line_diff_keeps_context_round_changes() {
        let base = "a\nb\nc\nd\ne\nf\ng\nh\ni";
        let text = "a\nb\nc\nd\nE\nf\ng\nh\ni";
        let d = line_diff(base, text);
        let kinds: Vec<LineKind> = d.iter().map(|l| l.kind).collect();
        assert_eq!(kinds[0], LineKind::Hunk);
        assert!(kinds.contains(&LineKind::Del) && kinds.contains(&LineKind::Add));
        let del = d.iter().find(|l| l.kind == LineKind::Del).unwrap();
        assert_eq!((del.text.as_str(), del.old), ("e", Some(5)));
        // a, the first line, is four away from the change: left out.
        assert!(!d.iter().any(|l| l.text == "a"));
        assert_eq!(encode("src/a b.rs"), "src/a%20b.rs");
        assert_eq!(
            short_base("0123456789abcdef0123456789abcdef01234567"),
            "0123456"
        );
    }
}
