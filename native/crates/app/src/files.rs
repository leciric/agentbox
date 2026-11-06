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
    FileStatus, FilesChange, SQLiteQuery, WorktreeFiles,
};
use agentbox_i18n::{t, tv};
use base64::Engine as _;
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::Sizable as _;
use gpui_component::input::{Input, InputEvent, InputState};
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
    /// Find a file: its box, and every path once something is typed.
    find: Entity<InputState>,
    query: String,
    all: Option<Result<WorktreeFiles, String>>,
    /// Ignored files are listed too (a past agent's branch has none).
    ignored: bool,
    read_only: bool,
    /// The SQLite file shown: its table, offset and rows.
    sqlite: Option<(String, Result<SQLiteQuery, String>)>,
    /// The PDF shown, drawn, and the page on screen.
    pdf: Option<(String, Result<crate::viewers::PdfPages, String>)>,
    page: usize,
    /// The PDF's zoom, a share of its pages' own size; None fits the width.
    pdf_zoom: Option<f32>,
    _subs: Vec<Subscription>,
    _poll: Task<()>,
}

/// The most files Find lists.
const FIND_LIMIT: usize = 200;
/// The rows a SQLite table is read by.
const SQLITE_PAGE: i64 = 100;

impl FileBrowser {
    pub fn new(
        client: Client,
        base: String,
        r#ref: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Self {
        let find = cx.new(|cx| InputState::new(window, cx).placeholder(t("files.panel.find")));
        let sub = cx.subscribe(&find, |this: &mut Self, input, ev: &InputEvent, cx| {
            if let InputEvent::Change = ev {
                this.query = input.read(cx).value().trim().to_string();
                if !this.query.is_empty() && this.all.is_none() {
                    this.load_all(cx);
                }
                cx.notify();
            }
        });
        let read_only = base.starts_with("/v1/archived-agents/");
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
            find,
            query: String::new(),
            all: None,
            ignored: !read_only && crate::prefs::get_bool("files.ignored", false),
            read_only,
            sqlite: None,
            pdf: None,
            page: 0,
            pdf_zoom: None,
            _subs: vec![sub],
            _poll: poll,
        };
        this.list(String::new(), cx);
        this.load_status(true, cx);
        this
    }

    fn list(&mut self, path: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = format!(
            "{}/list?path={}{}",
            self.base,
            encode(&path),
            if self.ignored { "&ignored=1" } else { "" }
        );
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
                if let Ok(c) = &res {
                    if c.binary && c.sqlite && this.sqlite.as_ref().is_none_or(|(p, _)| *p != path)
                    {
                        this.load_sqlite(path.clone(), String::new(), 0, cx);
                    }
                    if c.pdf
                        && !c.data.is_empty()
                        && this.pdf.as_ref().is_none_or(|(p, _)| *p != path)
                    {
                        this.render_pdf(path.clone(), &c.data, cx);
                    }
                }
                this.image = res.as_ref().ok().and_then(image_of);
                this.content = Some(res.map_err(|e| e.to_string()));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_all(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = self.base.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<WorktreeFiles>(&url)
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                this.all = Some(res);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn load_sqlite(&mut self, path: String, table: String, offset: i64, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let url = if table.is_empty() {
            format!("{}/sqlite?path={}", self.base, encode(&path))
        } else {
            format!(
                "{}/sqlite?path={}&table={}&offset={offset}&limit={SQLITE_PAGE}",
                self.base,
                encode(&path),
                encode(&table)
            )
        };
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<SQLiteQuery>(&url)
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.selected.as_deref() == Some(path.as_str()) {
                    this.sqlite = Some((path, res));
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    fn render_pdf(&mut self, path: String, data: &str, cx: &mut Context<Self>) {
        let Ok(bytes) = base64::engine::general_purpose::STANDARD.decode(data) else {
            return;
        };
        self.page = 0;
        let (tx, rx) = tokio::sync::oneshot::channel();
        std::thread::spawn(move || {
            let _ = tx.send(crate::viewers::render_pdf(bytes));
        });
        cx.spawn(async move |this, cx| {
            let res = rx.await.unwrap_or_else(|_| Err("render".into()));
            this.update(cx, |this, cx| {
                if this.selected.as_deref() == Some(path.as_str()) {
                    this.pdf = Some((path, res));
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    fn toggle_ignored(&mut self, cx: &mut Context<Self>) {
        self.ignored = !self.ignored;
        crate::prefs::set_bool("files.ignored", self.ignored);
        let dirs: Vec<String> = self.dirs.keys().cloned().collect();
        for d in dirs {
            self.list(d, cx);
        }
        cx.notify();
    }

    /// open_file picks a file, as a link to it in a chat does too.
    pub fn open_file(&mut self, path: String, cx: &mut Context<Self>) {
        self.sqlite = None;
        self.pdf = None;
        self.pdf_zoom = None;
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
                        .when(selected, |el| el.bg(theme::surface_strong()))
                        .hover(|el| el.bg(theme::surface_raised()))
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
                                    _ => theme::primary(),
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

    /// render_found is Find's answer: the files that match, best first.
    fn render_found(&self, cx: &mut Context<Self>) -> AnyElement {
        let body: AnyElement = match &self.all {
            None => note(t("common.loading")),
            Some(Err(e)) => note(e.clone()),
            Some(Ok(all)) => {
                let found = crate::chat::match_files(&all.files, &self.query, FIND_LIMIT);
                if found.is_empty() {
                    note(t("files.panel.noMatch"))
                } else {
                    v_flex()
                        .children(found.into_iter().map(|path| {
                            let (dir, name) = match path.rsplit_once('/') {
                                Some((d, n)) => (d.to_string(), n.to_string()),
                                None => (String::new(), path.clone()),
                            };
                            let selected = self.selected.as_deref() == Some(path.as_str());
                            let p = path.clone();
                            v_flex()
                                .id(SharedString::from(format!("found-{path}")))
                                .px_2()
                                .py_0p5()
                                .rounded_sm()
                                .cursor_pointer()
                                .text_xs()
                                .when(selected, |el| el.bg(theme::surface_strong()))
                                .hover(|el| el.bg(theme::surface_raised()))
                                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.open_file(p.clone(), cx)
                                }))
                                .child(div().truncate().text_color(theme::primary()).child(name))
                                .when(!dir.is_empty(), |el| {
                                    el.child(div().truncate().text_color(theme::faint()).child(dir))
                                })
                        }))
                        .when(all.truncated, |el| {
                            el.child(note(t("files.panel.findTruncated")))
                        })
                        .into_any_element()
                }
            }
        };
        div()
            .id("files-found")
            .size_full()
            .overflow_y_scroll()
            .py_1()
            .child(body)
            .into_any_element()
    }

    /// render_sqlite is SqliteView: the tables to pick, a page of rows.
    fn render_sqlite(&self, c: &FileContent, cx: &mut Context<Self>) -> AnyElement {
        let q = match &self.sqlite {
            Some((p, Ok(q))) if *p == c.path => q.clone(),
            Some((p, Err(e))) if *p == c.path => {
                return note(tv(
                    "files.sqlite.unreadable",
                    &[("error", e.into()), ("size", (&human_bytes(c.size)).into())],
                ));
            }
            _ => return note(t("common.loading")),
        };
        if q.tables.is_empty() {
            return note(t("files.sqlite.empty"));
        }
        let path = c.path.clone();
        let tables = h_flex()
            .flex_wrap()
            .gap_1()
            .px_2()
            .py_1p5()
            .border_b_1()
            .border_color(theme::line())
            .child(
                div()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(t("files.sqlite.table")),
            )
            .children(q.tables.iter().map(|tb| {
                let label = if tb.view {
                    tv("files.sqlite.view", &[("name", (&tb.name).into())])
                } else {
                    tb.name.clone()
                };
                let (p, name) = (path.clone(), tb.name.clone());
                chip(
                    Box::leak(format!("sqlite-table-{}", tb.name).into_boxed_str()),
                    label,
                    tb.name == q.table,
                )
                .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                    this.load_sqlite(p.clone(), name.clone(), 0, cx)
                }))
            }));
        let rows: Vec<Vec<String>> = q
            .rows
            .iter()
            .map(|r| r.iter().map(crate::viewers::cell_text).collect())
            .collect();
        let (p1, p2, t1, t2) = (path.clone(), path, q.table.clone(), q.table.clone());
        let offset = q.offset;
        let more = q.total < 0 || offset + (rows.len() as i64) < q.total;
        let pager = h_flex()
            .gap_2()
            .px_2()
            .py_1()
            .text_xs()
            .text_color(theme::muted())
            .child(crate::viewers::sqlite_range(offset, rows.len(), q.total))
            .child(div().flex_1())
            .child(
                icon_button("sqlite-prev", "‹", t("files.sqlite.previous"))
                    .when(offset == 0, |el| el.opacity(0.4))
                    .when(offset > 0, |el| {
                        el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.load_sqlite(
                                p1.clone(),
                                t1.clone(),
                                (offset - SQLITE_PAGE).max(0),
                                cx,
                            )
                        }))
                    }),
            )
            .child(
                icon_button("sqlite-next", "›", t("files.sqlite.next"))
                    .when(!more, |el| el.opacity(0.4))
                    .when(more, |el| {
                        el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.load_sqlite(p2.clone(), t2.clone(), offset + SQLITE_PAGE, cx)
                        }))
                    }),
            );
        v_flex()
            .child(tables)
            .child(pager)
            .child(
                div()
                    .h(px(520.))
                    .child(crate::viewers::grid("sqlite-grid", &q.columns, &rows)),
            )
            .into_any_element()
    }

    /// render_pdf_view is PdfView: a page at a time, and the PDF opened
    /// in the system's own viewer.
    fn render_pdf_view(&self, c: &FileContent, cx: &mut Context<Self>) -> AnyElement {
        if c.too_large || c.data.is_empty() {
            return note(tv(
                "files.viewer.pdfTooLarge",
                &[("size", (&human_bytes(c.size)).into())],
            ));
        }
        let data = c.data.clone();
        let name = c.path.rsplit('/').next().unwrap_or("file.pdf").to_string();
        let outside =
            icon_button("pdf-outside", "↗", t("native.pdf.external")).on_click(move |_, _, cx| {
                let Ok(bytes) = base64::engine::general_purpose::STANDARD.decode(&data) else {
                    return;
                };
                let path = std::env::temp_dir().join(&name);
                if std::fs::write(&path, bytes).is_ok() {
                    cx.open_with_system(&path);
                }
            });
        let pages = match &self.pdf {
            Some((p, Ok(pages))) if *p == c.path => pages,
            Some((p, Err(e))) if *p == c.path => {
                return v_flex()
                    .child(h_flex().px_2().child(div().flex_1()).child(outside))
                    .child(note(tv(
                        "files.pdf.failed",
                        &[("error", e.into()), ("size", (&human_bytes(c.size)).into())],
                    )))
                    .into_any_element();
            }
            _ => return note(t("common.loading")),
        };
        let n = pages.pages.len();
        let page = self.page.min(n - 1);
        let (w, h) = pages.sizes[page];
        // The zoom steps (PdfView's), of the page's own size: it was drawn
        // at 1.5 times that. Fit is about the panel's usual width.
        const ZOOMS: [f32; 9] = [0.5, 0.67, 0.8, 1., 1.25, 1.5, 2., 3., 4.];
        let natural = w as f32 / 1.5;
        let scale = self.pdf_zoom.unwrap_or(560. / natural.max(1.));
        let zoom_by = move |step: i32| {
            if step > 0 {
                ZOOMS.iter().copied().find(|z| *z > scale + 0.001)
            } else {
                ZOOMS.iter().rev().copied().find(|z| *z < scale - 0.001)
            }
        };
        let (zin, zout) = (zoom_by(1), zoom_by(-1));
        v_flex()
            .child(
                h_flex()
                    .gap_2()
                    .px_2()
                    .py_1()
                    .border_b_1()
                    .border_color(theme::line())
                    .text_xs()
                    .text_color(theme::muted())
                    .child(
                        icon_button("pdf-prev", "‹", t("files.pdf.previous"))
                            .when(page == 0, |el| el.opacity(0.4))
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.page = this.page.saturating_sub(1);
                                cx.notify();
                            })),
                    )
                    .child(format!(
                        "{} {} {}",
                        t("files.pdf.page"),
                        page + 1,
                        tv("files.pdf.of", &[("pages", (n as i64).into())])
                    ))
                    .child(
                        icon_button("pdf-next", "›", t("files.pdf.next"))
                            .when(page + 1 >= n, |el| el.opacity(0.4))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.page = (this.page + 1).min(n - 1);
                                cx.notify();
                            })),
                    )
                    .child(div().flex_1())
                    .child(
                        icon_button("pdf-zoom-out", "−", t("files.pdf.zoomOut"))
                            .when(zout.is_none(), |el| el.opacity(0.4))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                if let Some(z) = zout {
                                    this.pdf_zoom = Some(z);
                                    cx.notify();
                                }
                            })),
                    )
                    .child(
                        div()
                            .id("pdf-fit")
                            .px_1p5()
                            .rounded_sm()
                            .cursor_pointer()
                            .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                            .when(self.pdf_zoom.is_none(), |el| {
                                el.text_color(theme::primary())
                            })
                            .tooltip(|w, cx| {
                                gpui_component::tooltip::Tooltip::new(t("files.pdf.fit"))
                                    .build(w, cx)
                            })
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.pdf_zoom = None;
                                cx.notify();
                            }))
                            .child(if self.pdf_zoom.is_none() {
                                t("files.pdf.fitShort")
                            } else {
                                format!("{}%", (scale * 100.).round())
                            }),
                    )
                    .child(
                        icon_button("pdf-zoom-in", "+", t("files.pdf.zoomIn"))
                            .when(zin.is_none(), |el| el.opacity(0.4))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                if let Some(z) = zin {
                                    this.pdf_zoom = Some(z);
                                    cx.notify();
                                }
                            })),
                    )
                    .child(outside),
            )
            .child(match self.pdf_zoom {
                None => div()
                    .p_3()
                    .bg(theme::ink())
                    .child(
                        img(pages.pages[page].clone())
                            .w_full()
                            // A page's height for the panel's usual width.
                            .h(px(560. * h as f32 / w.max(1) as f32))
                            .object_fit(ObjectFit::Contain),
                    )
                    .into_any_element(),
                Some(z) => div()
                    .id("pdf-zoomed")
                    .p_3()
                    .bg(theme::ink())
                    .overflow_x_scroll()
                    .child(
                        img(pages.pages[page].clone())
                            .flex_none()
                            .w(px(natural * z))
                            .h(px(h as f32 / 1.5 * z))
                            .object_fit(ObjectFit::Contain),
                    )
                    .into_any_element(),
            })
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
                    .when(selected, |el| el.bg(theme::surface_strong()))
                    .hover(|el| el.bg(theme::surface_raised()))
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
                            .text_color(theme::primary())
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
            .border_color(theme::line())
            .child(
                div()
                    .min_w_0()
                    .flex_1()
                    .truncate()
                    .text_xs()
                    .font_family(theme::mono())
                    .text_color(theme::primary())
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
            return diff::table(
                "file-diff-table",
                &lines,
                crate::highlight::lang_of(&c.path),
            );
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
        if c.pdf {
            return self.render_pdf_view(c, cx);
        }
        if c.binary && c.sqlite {
            return self.render_sqlite(c, cx);
        }
        if !c.binary
            && let Some(table) = crate::viewers::csv(&c.path, &c.text)
        {
            return match table {
                Ok(el) => div().h(px(560.)).child(el).into_any_element(),
                Err(why) => v_flex()
                    .child(note(why))
                    .child(self.render_text(c, cx))
                    .into_any_element(),
            };
        }
        if c.binary || c.pdf || c.sqlite {
            return note(tv(
                "files.viewer.binary",
                &[("size", (&human_bytes(c.size)).into())],
            ));
        }
        self.render_text(c, cx)
    }

    fn render_text(&self, c: &FileContent, cx: &mut Context<Self>) -> AnyElement {
        let lines: Vec<&str> = c.text.split('\n').collect();
        let shown = lines.len().min(MAX_SHOWN);
        let text: Vec<String> = lines[..shown]
            .iter()
            .map(|l| l.replace('\t', "    "))
            .collect();
        let colours =
            crate::highlight::lang_of(&c.path).map(|lang| crate::highlight::line_runs(lang, &text));
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
                        .children(text.iter().enumerate().map(|(i, l)| {
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
                                        .text_color(theme::primary())
                                        .child(if l.is_empty() {
                                            " ".into_any_element()
                                        } else {
                                            crate::highlight::styled(
                                                l.clone(),
                                                colours.as_ref().and_then(|c| c.get(i)),
                                            )
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
                    .border_color(theme::line())
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
                    .child(
                        div()
                            .flex_1()
                            .min_w(px(80.))
                            .max_w(px(260.))
                            .child(Input::new(&self.find).xsmall().cleanable(true)),
                    )
                    .child(div().flex_1())
                    .when(!self.read_only, |el| {
                        el.child(
                            icon_button(
                                "files-ignored",
                                if self.ignored { "◉" } else { "◌" },
                                t(if self.ignored {
                                    "files.panel.hideIgnored"
                                } else {
                                    "files.panel.showIgnored"
                                }),
                            )
                            .when(self.ignored, |el| el.text_color(theme::accent()))
                            .on_click(
                                cx.listener(|this, _: &ClickEvent, _, cx| this.toggle_ignored(cx)),
                            ),
                        )
                    })
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
                            .border_color(theme::line())
                            .child(if self.query.is_empty() {
                                self.render_tree(cx)
                            } else {
                                self.render_found(cx)
                            }),
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
            theme::line()
        })
        .when(on, |el| el.bg(theme::accent().opacity(0.12)))
        .text_color(if on { theme::primary() } else { theme::muted() })
        .hover(|el| el.text_color(theme::primary()))
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
        .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
        .tooltip(move |window, cx| {
            gpui_component::tooltip::Tooltip::new(tip.clone()).build(window, cx)
        })
        .child(crate::icons::of(glyph))
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
