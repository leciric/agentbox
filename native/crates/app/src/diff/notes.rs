//! Notes on an agent's diff in the review (components/ReviewNotes.tsx,
//! lib/reviewNotes.ts): the + beside a line opens a box under it, Shift-click
//! on another line's + takes in the lines between, and what's written is a
//! draft the daemon keeps (internal/daemon/reviewnotes.go), edited or
//! removed on its line and sent with Request changes. Notes sent before stay
//! on their lines, pending until the user resolves them.
use std::collections::HashMap;

use agentbox_api::client::Method;
use agentbox_api::{AddReviewNoteRequest, Client, EditReviewNoteRequest, ReviewNote};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{InputEvent, Textarea, TextareaState};
use gpui_component::{h_flex, v_flex};

use super::{DiffLine, DiffView, LineKind};
use crate::app::controls::{self, Btn};
use crate::{theme, toasts};
use gpui_component::ActiveTheme as _;

/// A selection of a file's diff lines as a note keeps it (NoteRange).
#[derive(Clone, Debug, PartialEq)]
pub struct NoteRange {
    pub path: String,
    pub side: String,
    pub start: i64,
    pub end: i64,
    pub code: String,
}

fn is_code(l: &DiffLine) -> bool {
    matches!(l.kind, LineKind::Add | LineKind::Del | LineKind::Context)
}

/// note_range is the range rows from..=to of a file's diff make (noteRange).
pub fn note_range(path: &str, lines: &[DiffLine], from: usize, to: usize) -> Option<NoteRange> {
    let (a, b) = if from <= to { (from, to) } else { (to, from) };
    let rows: Vec<&DiffLine> = lines
        .get(a..=b.min(lines.len().saturating_sub(1)))?
        .iter()
        .filter(|l| is_code(l))
        .collect();
    if rows.is_empty() {
        return None;
    }
    let old = rows.iter().all(|l| l.kind == LineKind::Del);
    let numbers: Vec<u32> = rows
        .iter()
        .filter_map(|l| if old { l.old } else { l.new })
        .collect();
    let sign = |k: LineKind| match k {
        LineKind::Add => '+',
        LineKind::Del => '-',
        _ => ' ',
    };
    Some(NoteRange {
        path: path.to_string(),
        side: if old { "old" } else { "new" }.into(),
        start: *numbers.iter().min()? as i64,
        end: *numbers.iter().max()? as i64,
        code: rows
            .iter()
            .map(|l| format!("{}{}", sign(l.kind), l.text))
            .collect::<Vec<_>>()
            .join("\n"),
    })
}

fn line_on(l: &DiffLine, side: &str) -> Option<i64> {
    if !is_code(l) {
        return None;
    }
    if side == "old" { l.old } else { l.new }.map(i64::from)
}

/// note_at is the row a note shows under: its last line, while the diff
/// still has the code it quotes there (noteAt).
pub fn note_at(lines: &[DiffLine], note: &ReviewNote) -> Option<usize> {
    let i = lines
        .iter()
        .position(|l| line_on(l, &note.side) == Some(note.end))?;
    let quoted: Vec<&str> = note
        .code
        .split('\n')
        .filter(|l| {
            let c = l.chars().next();
            if note.side == "old" {
                c == Some('-') || c == Some(' ')
            } else {
                c == Some('+') || c == Some(' ')
            }
        })
        .map(|l| l.get(1..).unwrap_or(""))
        .collect();
    match quoted.last() {
        Some(last) if *last != lines[i].text => None,
        _ => Some(i),
    }
}

/// where_label is a note's lines in words (whereLabel).
pub fn where_label(side: &str, start: i64, end: i64) -> String {
    let one = start == end;
    let key = match (side == "old", one) {
        (true, true) => "review.notes.removedLine",
        (true, false) => "review.notes.removedLines",
        (false, true) => "review.notes.line",
        (false, false) => "review.notes.lines",
    };
    tv(key, &[("start", start.into()), ("end", end.into())])
}

pub struct Notes {
    client: Client,
    /// The agent's notes route: /v1/agents/<project>/<agent>/review-notes.
    base: String,
    pub list: Vec<ReviewNote>,
    /// The file and rows (anchor, end) a new note is written on.
    composing: Option<(String, usize, usize)>,
    editing: Option<i64>,
    input: Option<(Entity<TextareaState>, Subscription)>,
    /// Set while a drag over the + gutter widens the range being noted.
    dragging: bool,
    /// Whether Request changes' list shows the notes sent before.
    earlier_open: bool,
    saving: bool,
}

impl DiffView {
    /// with_notes lets line notes be added to an agent's diff.
    pub fn with_notes(mut self, client: Client, base: String, cx: &mut Context<Self>) -> Self {
        self.notes = Some(Notes {
            client,
            base,
            list: Vec::new(),
            composing: None,
            editing: None,
            input: None,
            dragging: false,
            earlier_open: false,
            saving: false,
        });
        self.load_notes(cx);
        self
    }

    fn load_notes(&mut self, cx: &mut Context<Self>) {
        let Some(n) = &self.notes else { return };
        let (client, base) = (n.client.clone(), n.base.clone());
        cx.spawn(async move |this, cx| {
            if let Ok(list) = client.get::<Vec<ReviewNote>>(&base).await {
                this.update(cx, |this, cx| {
                    if let Some(n) = &mut this.notes {
                        n.list = list;
                    }
                    cx.notify();
                })
                .ok();
            }
        })
        .detach();
    }

    /// drafts are the notes not sent yet, which go with Request changes.
    pub fn drafts(&self) -> Vec<ReviewNote> {
        self.notes.as_ref().map_or_else(Vec::new, |n| {
            n.list
                .iter()
                .filter(|n| n.status == "draft")
                .cloned()
                .collect()
        })
    }

    /// notes_open is whether a note's box is open (Esc closes it first).
    pub fn notes_open(&self) -> bool {
        self.notes
            .as_ref()
            .is_some_and(|n| n.composing.is_some() || n.editing.is_some())
    }

    pub fn close_note(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        // Keys go back to the diff, and through it to the review.
        if self.notes.as_ref().is_some_and(|n| n.input.is_some()) {
            self.focus.focus(window, cx);
        }
        if let Some(n) = &mut self.notes {
            n.composing = None;
            n.editing = None;
            n.input = None;
        }
        cx.notify();
    }

    fn open_box(&mut self, initial: &str, window: &mut Window, cx: &mut Context<Self>) {
        let input = cx.new(|cx| {
            TextareaState::new(window, cx)
                .auto_grow(3, 12)
                .placeholder(t("review.notes.placeholder"))
                .default_value(initial.to_string())
        });
        input.update(cx, |s, cx| s.focus(window, cx));
        let sub = cx.subscribe_in(&input, window, |this, _, ev: &InputEvent, window, cx| {
            // Ctrl or ⌘ with Enter saves it (Enter alone is a new line), as
            // the Electron app's note box.
            if let InputEvent::PressEnter {
                secondary: true, ..
            } = ev
            {
                this.save_note(window, cx);
            }
        });
        if let Some(n) = &mut self.notes {
            n.input = Some((input, sub));
        }
    }

    /// start_note opens a note on a row, or takes in the rows up to it with
    /// Shift held while one is open in the same file.
    fn start_note(
        &mut self,
        path: String,
        row: usize,
        shift: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(n) = &mut self.notes else { return };
        n.editing = None;
        match &mut n.composing {
            Some((p, _, end)) if shift && *p == path => *end = row,
            _ => {
                n.composing = Some((path, row, row));
                n.dragging = true;
                self.open_box("", window, cx);
            }
        }
        cx.notify();
    }

    /// drag_note widens the range being noted to `row` while the button
    /// that started it on the + is held (DiffNotes' drag over the gutter),
    /// and ends the drag when it is let go.
    fn drag_note(
        &mut self,
        path: &str,
        row: usize,
        held: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let Some(n) = &mut self.notes else { return };
        if !n.dragging {
            return;
        }
        if !held {
            // Let go: the box opens under the range.
            n.dragging = false;
            if let Some((input, _)) = &n.input {
                input.update(cx, |s, cx| s.focus(window, cx));
            }
            cx.notify();
            return;
        }
        if let Some((p, _, end)) = &mut n.composing
            && p == path
            && *end != row
        {
            *end = row;
            cx.notify();
        }
    }

    fn edit_note(&mut self, id: i64, window: &mut Window, cx: &mut Context<Self>) {
        let Some(n) = &mut self.notes else { return };
        let body = n
            .list
            .iter()
            .find(|x| x.id == id)
            .map(|x| x.body.clone())
            .unwrap_or_default();
        n.composing = None;
        n.editing = Some(id);
        self.open_box(&body, window, cx);
        cx.notify();
    }

    fn save_note(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(n) = &self.notes else { return };
        let Some((input, _)) = &n.input else { return };
        let body = input.read(cx).value().trim().to_string();
        if body.is_empty() || n.saving {
            return;
        }
        let (client, base) = (n.client.clone(), n.base.clone());
        let req = if let Some(id) = n.editing {
            let body = serde_json::to_vec(&EditReviewNoteRequest {
                body: Some(body),
                resolved: None,
            })
            .unwrap_or_default();
            Some((Method::PATCH, format!("{base}/{id}"), body))
        } else if let Some((path, a, b)) = &n.composing {
            let lines = self.lines_of(path);
            note_range(path, &lines, *a, *b).map(|r| {
                let body = serde_json::to_vec(&AddReviewNoteRequest {
                    path: r.path,
                    side: r.side,
                    start: r.start,
                    end: r.end,
                    code: r.code,
                    body,
                })
                .unwrap_or_default();
                (Method::POST, base.clone(), body)
            })
        } else {
            None
        };
        let Some((method, path, body)) = req else {
            return;
        };
        if let Some(n) = &mut self.notes {
            n.saving = true;
        }
        let handle = window.window_handle();
        cx.spawn(async move |this, cx| {
            let res = client.raw(method, &path, Some(body)).await;
            this.update(cx, |this, cx| {
                if let Some(n) = &mut this.notes {
                    n.saving = false;
                }
                match res.and_then(|b| Ok(serde_json::from_slice::<ReviewNote>(&b)?)) {
                    Ok(note) => {
                        if let Some(n) = &mut this.notes {
                            match n.list.iter_mut().find(|x| x.id == note.id) {
                                Some(x) => *x = note,
                                None => n.list.push(note),
                            }
                        }
                        if let Some(n) = &mut this.notes {
                            n.composing = None;
                            n.editing = None;
                            n.input = None;
                        }
                        let focus = this.focus.clone();
                        handle
                            .update(cx, |_, window, cx| focus.focus(window, cx))
                            .ok();
                    }
                    Err(e) => toasts::error(
                        cx,
                        tv("review.notes.failed", &[("error", e.to_string().into())]),
                    ),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn remove_note(&mut self, id: i64, cx: &mut Context<Self>) {
        let Some(n) = &mut self.notes else { return };
        n.list.retain(|x| x.id != id);
        let (client, path) = (n.client.clone(), format!("{}/{id}", n.base));
        cx.spawn(async move |this, cx| {
            if let Err(e) = client.raw(Method::DELETE, &path, None).await {
                this.update(cx, |this, cx| {
                    toasts::error(
                        cx,
                        tv("review.notes.failed", &[("error", e.to_string().into())]),
                    );
                    this.load_notes(cx);
                })
                .ok();
            }
        })
        .detach();
        cx.notify();
    }

    fn resolve_note(&mut self, id: i64, resolved: bool, cx: &mut Context<Self>) {
        let Some(n) = &self.notes else { return };
        let (client, path) = (n.client.clone(), format!("{}/{id}", n.base));
        let body = serde_json::to_vec(&EditReviewNoteRequest {
            body: None,
            resolved: Some(resolved),
        })
        .unwrap_or_default();
        cx.spawn(async move |this, cx| {
            let res = client.raw(Method::PATCH, &path, Some(body)).await;
            this.update(cx, |this, cx| {
                match res.and_then(|b| Ok(serde_json::from_slice::<ReviewNote>(&b)?)) {
                    Ok(note) => {
                        if let Some(n) = &mut this.notes
                            && let Some(x) = n.list.iter_mut().find(|x| x.id == note.id)
                        {
                            *x = note;
                        }
                    }
                    Err(e) => toasts::error(
                        cx,
                        tv("review.notes.failed", &[("error", e.to_string().into())]),
                    ),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// note_hooks is what a file's table shows of the notes: a click on a
    /// row's +, the rows lit for a note being written, and under rows the
    /// notes on them and the box.
    pub(super) fn note_hooks(
        &self,
        path: &str,
        lines: &[DiffLine],
        cx: &mut Context<Self>,
    ) -> Option<super::Hooks> {
        let n = self.notes.as_ref()?;
        let me = cx.entity().downgrade();
        let file = path.to_string();
        let add: super::OnAdd = std::rc::Rc::new(move |row, shift, window, cx| {
            let file = file.clone();
            me.update(cx, |this, cx| this.start_note(file, row, shift, window, cx))
                .ok();
        });
        let (me2, file2) = (cx.entity().downgrade(), path.to_string());
        let drag: super::OnDrag = std::rc::Rc::new(move |row, held, window, cx| {
            me2.update(cx, |this, cx| this.drag_note(&file2, row, held, window, cx))
                .ok();
        });
        let mut under: HashMap<usize, Vec<AnyElement>> = HashMap::new();
        for note in n.list.iter().filter(|x| x.path == path) {
            if let Some(row) = note_at(lines, note) {
                let el = if n.editing == Some(note.id) {
                    self.note_box(where_label(&note.side, note.start, note.end), true, cx)
                } else {
                    self.note_card(note, false, cx)
                };
                under.entry(row).or_default().push(el);
            }
        }
        let mut lit = None;
        if let Some((p, a, b)) = &n.composing
            && p == path
        {
            let (lo, hi) = if a <= b { (*a, *b) } else { (*b, *a) };
            lit = Some((lo, hi));
            let label = note_range(path, lines, lo, hi)
                .map(|r| where_label(&r.side, r.start, r.end))
                .unwrap_or_default();
            // Not while a drag is still taking rows in: it would push the
            // rows below away from the pointer.
            if !n.dragging {
                under
                    .entry(hi)
                    .or_default()
                    .push(self.note_box(label, false, cx));
            }
        }
        Some(super::Hooks {
            add,
            drag,
            under,
            lit,
        })
    }

    fn note_box(&self, title: String, editing: bool, cx: &mut Context<Self>) -> AnyElement {
        let input = self
            .notes
            .as_ref()
            .and_then(|n| n.input.as_ref())
            .map(|(i, _)| i.clone());
        v_flex()
            .max_w(px(640.))
            .my_1()
            .ml(px(104.))
            .gap_2()
            .p_2p5()
            .rounded_lg()
            .border_1()
            .border_color(theme::accent().opacity(0.4))
            .bg(theme::card())
            .font_family(cx.theme().font_family.clone())
            .child(div().text_xs().text_color(theme::muted()).child(title))
            .children(input.map(|i| Textarea::new(&i)))
            .child(
                h_flex()
                    .justify_end()
                    .gap_2()
                    .child(controls::button(
                        "note-cancel",
                        t("common.cancel"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, window, cx| this.close_note(window, cx)),
                    ))
                    .child(controls::button(
                        "note-save",
                        if editing {
                            t("review.notes.update")
                        } else {
                            t("review.notes.save")
                        },
                        Btn::Primary,
                        false,
                        cx.listener(|this, _: &ClickEvent, window, cx| this.save_note(window, cx)),
                    )),
            )
            .into_any_element()
    }

    /// note_card is one note; `in_list` is Request changes' list of them
    /// (NotesList), with its file and the first line it is on.
    fn note_card(&self, note: &ReviewNote, in_list: bool, cx: &mut Context<Self>) -> AnyElement {
        let (label, color) = match note.status.as_str() {
            "draft" => (t("review.notes.status.draft"), theme::starting()),
            "resolved" => (t("review.notes.status.resolved"), theme::running()),
            _ => (t("review.notes.status.pending"), theme::accent()),
        };
        let id = note.id;
        let first = in_list
            .then(|| {
                note.code
                    .lines()
                    .find(|l| !l.trim().is_empty())
                    .map(str::to_string)
            })
            .flatten();
        let place = if in_list {
            format!(
                "{} · {}",
                note.path,
                where_label(&note.side, note.start, note.end)
            )
        } else {
            where_label(&note.side, note.start, note.end)
        };
        v_flex()
            .max_w(px(640.))
            .when(!in_list, |el| el.my_1().ml(px(104.)))
            .gap_1()
            .px_2p5()
            .py_2()
            .rounded_lg()
            .border_1()
            .border_color(theme::line())
            .bg(theme::card())
            .font_family(cx.theme().font_family.clone())
            .when(note.status == "resolved", |el| el.opacity(0.75))
            .child(
                h_flex()
                    .gap_2()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(div().min_w_0().truncate().child(place))
                    .child(controls::badge(label, color))
                    .child(div().flex_1())
                    .when(note.status == "draft", |el| {
                        el.child(controls::button(
                            ("note-edit", id as u64),
                            t("review.notes.edit"),
                            Btn::Ghost,
                            false,
                            cx.listener(move |this, _: &ClickEvent, window, cx| {
                                this.edit_note(id, window, cx)
                            }),
                        ))
                    })
                    .when(note.status == "pending", |el| {
                        el.child(controls::button(
                            ("note-resolve", id as u64),
                            t("review.notes.resolve"),
                            Btn::Ghost,
                            false,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.resolve_note(id, true, cx)
                            }),
                        ))
                    })
                    .when(
                        note.status == "resolved" && note.resolved_at.is_some(),
                        |el| {
                            el.child(controls::button(
                                ("note-reopen", id as u64),
                                t("review.notes.reopen"),
                                Btn::Ghost,
                                false,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    this.resolve_note(id, false, cx)
                                }),
                            ))
                        },
                    )
                    .child(controls::button(
                        ("note-remove", id as u64),
                        t("review.notes.remove"),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| this.remove_note(id, cx)),
                    )),
            )
            .when_some(first, |el, code| {
                el.child(
                    div()
                        .truncate()
                        .font_family(theme::mono())
                        .text_xs()
                        .text_color(theme::faint())
                        .child(code),
                )
            })
            .child(
                div()
                    .text_sm()
                    .text_color(theme::primary())
                    .whitespace_normal()
                    .child(note.body.clone()),
            )
            .into_any_element()
    }

    /// notes_list is the notes in Request changes (ReviewNotes NotesList):
    /// the drafts that go with it, each editable in place, and those sent
    /// before, folded.
    fn notes_list(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let n = self.notes.as_ref()?;
        if n.list.is_empty() {
            return None;
        }
        let card = |note: &ReviewNote, cx: &mut Context<Self>| {
            if n.editing == Some(note.id) {
                self.note_box(
                    format!(
                        "{} · {}",
                        note.path,
                        where_label(&note.side, note.start, note.end)
                    ),
                    true,
                    cx,
                )
            } else {
                self.note_card(note, true, cx)
            }
        };
        let draft: Vec<_> = n.list.iter().filter(|x| x.status == "draft").collect();
        let earlier: Vec<_> = n.list.iter().filter(|x| x.status != "draft").collect();
        let heading = |s: String| {
            div()
                .text_xs()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::faint())
                .child(s.to_uppercase())
        };
        let mut out = v_flex()
            .id("review-notes-list")
            .max_h(px(320.))
            .overflow_y_scroll()
            .gap_2();
        if !draft.is_empty() {
            out = out.child(heading(tv(
                "review.notes.toSend",
                &[("count", draft.len().into())],
            )));
            for note in &draft {
                out = out.child(card(note, cx));
            }
        }
        if !earlier.is_empty() {
            let pending = earlier.iter().filter(|x| x.status == "pending").count();
            let resolved = earlier.iter().filter(|x| x.status == "resolved").count();
            let counts = [
                (pending > 0)
                    .then(|| tv("review.notes.pendingCount", &[("count", pending.into())])),
                (resolved > 0)
                    .then(|| tv("review.notes.resolvedCount", &[("count", resolved.into())])),
            ]
            .into_iter()
            .flatten()
            .collect::<Vec<_>>()
            .join(", ");
            let open = n.earlier_open;
            out = out.child(
                h_flex()
                    .id("review-notes-earlier")
                    .gap_1()
                    .cursor_pointer()
                    .child(crate::icons::of(if open { "▾" } else { "▸" }))
                    .child(heading(tv(
                        "review.notes.earlier",
                        &[("list", counts.into())],
                    )))
                    .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                        if let Some(n) = &mut this.notes {
                            n.earlier_open = !n.earlier_open;
                        }
                        cx.notify();
                    })),
            );
            if open {
                for note in &earlier {
                    out = out.child(card(note, cx));
                }
            }
        }
        Some(out.into_any_element())
    }
}

/// NotesList shows a diff's notes where Request changes is written: the
/// cards are the diff's own, so editing one there edits it here.
pub struct NotesList {
    diff: Entity<DiffView>,
    _sub: Subscription,
}

impl NotesList {
    pub fn new(diff: Entity<DiffView>, cx: &mut Context<Self>) -> Self {
        let _sub = cx.observe(&diff, |_, _, cx| cx.notify());
        Self { diff, _sub }
    }
}

impl Render for NotesList {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        self.diff
            .update(cx, |d, cx| d.notes_list(cx))
            .unwrap_or_else(|| div().into_any_element())
    }
}

#[cfg(test)]
mod tests {
    use super::{ReviewNote, note_at, note_range};
    use crate::diff::parse_patch;

    #[test]
    fn a_range_is_its_lines_on_one_side_and_finds_its_row_again() {
        let lines = parse_patch("@@ -10,3 +10,4 @@ fn a()\n keep\n-old\n+new\n+more");
        let r = note_range("a.rs", &lines, 2, 3).unwrap();
        assert_eq!((r.side.as_str(), r.start, r.end), ("new", 11, 11));
        assert_eq!(r.code, "-old\n+new");
        let only_removed = note_range("a.rs", &lines, 2, 2).unwrap();
        assert_eq!(
            (only_removed.side.as_str(), only_removed.start),
            ("old", 11)
        );
        let note = ReviewNote {
            side: r.side,
            start: r.start,
            end: r.end,
            code: r.code,
            ..Default::default()
        };
        assert_eq!(note_at(&lines, &note), Some(3));
        assert_eq!(note_range("a.rs", &lines, 0, 0), None);
    }
}
