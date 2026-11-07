//! How each of the timeline's rows draws (Timeline.tsx, Work.tsx and the
//! cards beside them): your messages with their pictures and files, the
//! answers, the work folded into a line, subagents, notices, other
//! projects' messages, scheduled runs, an agent's permission request in the
//! lead's chat, compactions, a turn's changed files and its checkpoint's
//! menu; and the zoomed picture over all of it.
use std::sync::Arc;

use agentbox_api::{ChatDiff, ChatItem, ChatPermission};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::button::{Button, ButtonVariants as _};
use gpui_component::menu::{ContextMenuExt as _, DropdownMenu as _, PopupMenuItem};
use gpui_component::text::TextView;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::rows::{self, Row};
use super::{ChatAction, ChatView};
use crate::diff::{DiffLine, LineKind};
use crate::theme;

/// local_time is a time as the meta line shows it: 14:05.
pub fn local_time(time: &str) -> String {
    chrono::DateTime::parse_from_rfc3339(time)
        .map(|d| d.with_timezone(&chrono::Local).format("%H:%M").to_string())
        .unwrap_or_default()
}

/// glyph is an entry's Lucide icon (Work.tsx iconOf and toolIcons).
fn glyph(it: &ChatItem) -> &'static str {
    if it.kind == "thought" {
        return "brain";
    }
    if let Some(p) = &it.permission {
        let allowed = p
            .options
            .iter()
            .find(|o| o.id == p.outcome)
            .is_some_and(|o| o.kind.starts_with("allow"));
        return if allowed { "shield-check" } else { "shield-x" };
    }
    let Some(tool) = &it.tool else {
        return "wrench";
    };
    if tool.status == "failed" {
        return "circle-alert";
    }
    match tool.kind.as_str() {
        "read" => "eye",
        "edit" => "square-pen",
        "delete" => "trash",
        "move" => "arrow-right-left",
        "search" => "search",
        "execute" => "square-terminal",
        "think" => "brain",
        "fetch" => "globe",
        _ => "wrench",
    }
}

/// icon is a work row's icon: a 16px Lucide icon in a size-6 box.
fn icon(g: &'static str, color: Hsla) -> Div {
    div()
        .flex_none()
        .size_6()
        .flex()
        .items_center()
        .justify_center()
        .text_color(color)
        .child(crate::icons::ic(crate::icons::lucide(g).unwrap_or("wrench")).size(px(16.)))
}

fn chevron(open: bool) -> Div {
    div()
        .flex_none()
        .text_xs()
        .text_color(theme::faint())
        .child(
            crate::icons::ic(if open {
                "chevron-down"
            } else {
                "chevron-right"
            })
            .size(px(12.)),
        )
}

fn card(tint: Hsla) -> Div {
    v_flex()
        .w_full()
        .gap_1()
        .px_3p5()
        .py_2p5()
        .rounded_xl()
        .border_1()
        .border_color(tint.opacity(0.3))
        .bg(tint.opacity(0.06))
        .text_sm()
}

fn link(id: impl Into<ElementId>, label: String) -> Stateful<Div> {
    div()
        .id(id)
        .flex_none()
        .px_1p5()
        .py_0p5()
        .rounded_md()
        .cursor_pointer()
        .text_xs()
        .text_color(theme::muted())
        .hover(|el| el.bg(theme::surface_strong()).text_color(theme::primary()))
        .child(format!("{label}  ↗"))
}

fn mono() -> Div {
    div()
        .px_3()
        .py_2()
        .text_xs()
        .font_family(theme::mono())
        .text_color(theme::muted())
}

/// diff_lines is a tool's edit as the diff table reads it, numbered.
pub fn diff_lines(d: &ChatDiff) -> Vec<DiffLine> {
    let (mut old, mut new) = (1u32, 1u32);
    rows::line_diff(&d.old_text, &d.new_text)
        .into_iter()
        .map(|(k, text)| {
            let (kind, o, n) = match k {
                '+' => {
                    new += 1;
                    (LineKind::Add, None, Some(new - 1))
                }
                '-' => {
                    old += 1;
                    (LineKind::Del, Some(old - 1), None)
                }
                _ => {
                    old += 1;
                    new += 1;
                    (LineKind::Context, Some(old - 1), Some(new - 1))
                }
            };
            DiffLine {
                kind,
                text,
                old: o,
                new: n,
            }
        })
        .collect()
}

pub(super) fn diff_block(id: String, d: &ChatDiff) -> AnyElement {
    let (added, removed) = rows::line_counts(d);
    v_flex()
        .w_full()
        .rounded_lg()
        .border_1()
        .border_color(theme::line())
        .overflow_hidden()
        .child(
            h_flex()
                .px_3()
                .py_1()
                .gap_2()
                .text_xs()
                .font_family(theme::mono())
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::muted())
                        .child(d.path.clone()),
                )
                .child(
                    div()
                        .text_color(theme::running())
                        .child(format!("+{added}")),
                )
                .child(
                    div()
                        .text_color(theme::error())
                        .child(format!("−{removed}")),
                ),
        )
        .child(crate::diff::table(
            SharedString::from(id),
            &diff_lines(d),
            crate::highlight::lang_of(&d.path),
        ))
        .into_any_element()
}

impl ChatView {
    fn item(&self, ix: usize) -> &ChatItem {
        &self.items[ix]
    }

    pub(super) fn render_row(&self, ix: usize, view: Entity<Self>) -> AnyElement {
        let Some(r) = self.rows.get(ix) else {
            // After the rows, the messages that didn't go.
            return self.failed_row(ix - self.rows.len(), &view);
        };
        let body = match &r.row {
            Row::User(i) => self.render_user(*i, &view),
            Row::Assistant { ix, last } => self.render_assistant(*ix, *last, &view),
            Row::Work { items, live } => self.render_work(&r.key, items, *live, &view),
            Row::Fold {
                turn,
                label,
                open,
                empty,
            } => {
                let turn = turn.clone();
                let v = view.clone();
                div()
                    .w_full()
                    .mb_3()
                    .pb_2()
                    .pt_1()
                    .border_b_1()
                    .border_color(theme::line())
                    .child(
                        h_flex()
                            .id(SharedString::from(format!("fold-{turn}")))
                            .gap_1()
                            .text_sm()
                            .text_color(theme::faint())
                            .when(!*empty, |el| {
                                el.cursor_pointer()
                                    .hover(|el| el.text_color(theme::muted()))
                                    .on_click(move |_, _, cx| {
                                        v.update(cx, |this, cx| this.toggle_turn(&turn, cx))
                                    })
                            })
                            .child(label.clone())
                            .when(!*empty, |el| el.child(chevron(*open))),
                    )
                    .into_any_element()
            }
            Row::Working { since, stalled } => {
                let elapsed = |s: &str| {
                    rows::millis(s)
                        .map(|m| {
                            rows::format_duration(
                                (chrono::Utc::now().timestamp_millis() - m) as f64 / 1000.,
                            )
                        })
                        .unwrap_or_default()
                };
                div()
                    .w_full()
                    .mb_3()
                    .pb_2()
                    .pt_1()
                    .border_b_1()
                    .border_color(theme::line())
                    // Timeline's working: h-6 px-1 text-sm text-subtle.
                    .child(
                        h_flex()
                            .h_6()
                            // items-baseline: the line sits at the box's top.
                            .items_start()
                            .px_1()
                            .text_sm()
                            .line_height(px(20.))
                            .text_color(theme::subtle())
                            .child(tv(
                                "chat.timeline.working",
                                &[("elapsed", elapsed(since).into())],
                            ))
                            .when_some(stalled.as_ref(), |el, s| {
                                el.child(div().text_color(theme::error()).child(tv(
                                    "chat.timeline.stalled",
                                    &[("elapsed", elapsed(s).into())],
                                )))
                            }),
                    )
                    .into_any_element()
            }
            Row::Thinking => {
                let starting = self.session.state == "starting";
                // Timeline's thinking: min-h-7 gap-1.5 px-0.5 pb-2, a Brain
                // in a size-6 box, the words in chat-shine's subtle.
                h_flex()
                    .min_h_7()
                    .pb_2()
                    .px_0p5()
                    .gap_1p5()
                    .text_sm()
                    .line_height(px(20.))
                    .text_color(theme::subtle())
                    .child(
                        div()
                            .flex_none()
                            .size_6()
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_color(theme::subtle())
                            .child(crate::icons::ic("brain").size(px(16.))),
                    )
                    .child(if starting {
                        if self.session.detail.is_empty() {
                            tv(
                                "chat.timeline.startingTool",
                                &[("tool", super::tool_label(&self.session.tool).into())],
                            )
                        } else {
                            self.session.detail.clone()
                        }
                    } else {
                        t("chat.work.thinking")
                    })
                    .into_any_element()
            }
            Row::Woken(i) => {
                let it = self.item(*i);
                h_flex()
                    .mb_3()
                    .gap_2()
                    .text_sm()
                    .text_color(theme::faint())
                    .child(crate::icons::of("⏰"))
                    .child(if it.text.is_empty() {
                        t("chat.timeline.woken")
                    } else {
                        tv(
                            "chat.timeline.wokenBy",
                            &[(
                                "task",
                                it.text.lines().collect::<Vec<_>>().join(", ").into(),
                            )],
                        )
                    })
                    .into_any_element()
            }
            Row::Background(tasks) => h_flex()
                .mb_3()
                .gap_2()
                .text_sm()
                .text_color(theme::faint())
                .child(crate::icons::of("⌛"))
                .child(tv(
                    "chat.timeline.background",
                    &[("tasks", tasks.join(", ").into())],
                ))
                .into_any_element(),
            Row::Note(i) => self.render_note(*i, &view),
            Row::Subagent { ix, children } => self.render_subagent(*ix, children, &view),
            Row::Compaction(i) => self.render_compaction(*i),
            Row::Changes(files) => {
                let key = r.key.clone();
                let open = self.expanded.contains(&key);
                let v = view.clone();
                let k2 = key.clone();
                v_flex()
                    .mb_3()
                    .w_full()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .child(
                        h_flex()
                            .id(SharedString::from(format!("changes-{key}")))
                            .px_3()
                            .py_1p5()
                            .gap_2()
                            .cursor_pointer()
                            .text_sm()
                            .text_color(theme::muted())
                            .hover(|el| el.text_color(theme::primary()))
                            .on_click(move |_, _, cx| v.update(cx, |this, cx| this.toggle(&k2, cx)))
                            .child(chevron(open))
                            .child(tv(
                                "chat.changedFiles.count",
                                &[("count", files.len().into())],
                            ))
                            .child(div().flex_1())
                            .child(div().text_xs().text_color(theme::running()).child(format!(
                                "+{}",
                                files.iter().map(|f| f.added).sum::<usize>()
                            )))
                            .child(div().text_xs().text_color(theme::error()).child(format!(
                                "−{}",
                                files.iter().map(|f| f.removed).sum::<usize>()
                            ))),
                    )
                    .when(open, |el| {
                        el.children(files.iter().map(|f| {
                            h_flex()
                                .px_3()
                                .py_0p5()
                                .gap_2()
                                .text_xs()
                                .font_family(theme::mono())
                                .border_t_1()
                                .border_color(theme::line())
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .truncate()
                                        .text_color(theme::muted())
                                        .child(f.path.clone()),
                                )
                                .child(
                                    div()
                                        .text_color(theme::running())
                                        .child(format!("+{}", f.added)),
                                )
                                .child(
                                    div()
                                        .text_color(theme::error())
                                        .child(format!("−{}", f.removed)),
                                )
                        }))
                    })
                    .into_any_element()
            }
            Row::Turn(cp) => {
                let n = cp.number;
                let latest = n == self.latest_checkpoint();
                let (v, id) = (view.clone(), cp.id.clone());
                // TurnMarker: -mt-2 mb-3 px-1, its rule seen only on hover.
                h_flex()
                    .group("turn")
                    .mt(px(-8.))
                    .mb_3()
                    .px_1()
                    .gap_2()
                    .child(
                        div()
                            .flex_1()
                            .h(px(1.))
                            .group_hover("turn", |s| s.bg(theme::line())),
                    )
                    .child(
                        Button::new(SharedString::from(format!("turn-{n}")))
                            .ghost()
                            .compact()
                            .h_6()
                            .px_1p5()
                            .rounded(gpui_component::button::ButtonRounded::Size(px(6.)))
                            .text_size(px(12.))
                            .text_color(theme::faint())
                            .child(
                                h_flex()
                                    .gap_1()
                                    .text_size(px(12.))
                                    .line_height(px(18.))
                                    .child(tv("chat.turn.label", &[("n", n.into())]))
                                    .child(crate::icons::ic("ellipsis").size(px(14.))),
                            )
                            .dropdown_menu(move |menu, _, _| {
                                let (back, fork) = (v.clone(), v.clone());
                                let (bid, fid) = (id.clone(), id.clone());
                                menu.item(
                                    PopupMenuItem::new(if latest {
                                        format!(
                                            "{} · {}",
                                            t("chat.turn.rollBackHere"),
                                            t("chat.turn.latest")
                                        )
                                    } else {
                                        t("chat.turn.rollBackHere")
                                    })
                                    .disabled(latest)
                                    .on_click(
                                        move |_, _, cx| {
                                            back.update(cx, |_, cx| {
                                                cx.emit(ChatAction::Rollback {
                                                    checkpoint: bid.clone(),
                                                    n,
                                                })
                                            })
                                        },
                                    ),
                                )
                                .item(
                                    PopupMenuItem::new(t("chat.turn.forkHere")).on_click(
                                        move |_, _, cx| {
                                            fork.update(cx, |_, cx| {
                                                cx.emit(ChatAction::Fork {
                                                    checkpoint: fid.clone(),
                                                    n,
                                                })
                                            })
                                        },
                                    ),
                                )
                            }),
                    )
                    .into_any_element()
            }
        };
        // The match Find shows is marked.
        let found = match &r.row {
            Row::User(i) | Row::Note(i) | Row::Woken(i) | Row::Compaction(i) => {
                self.found(&self.items[*i].id)
            }
            Row::Assistant { ix, .. } | Row::Subagent { ix, .. } => self.found(&self.items[*ix].id),
            _ => false,
        };
        let row = div()
            .w_full()
            .px_6()
            .when(ix == 0, |el| el.pt_4())
            .when(found, |el| el.bg(theme::starting().opacity(0.08)))
            .child(body);
        match self.entering.get(&r.key) {
            Some(t) if t.elapsed() < std::time::Duration::from_millis(800) => {
                crate::motion::enter(row, SharedString::from(format!("arrive-{}", r.key)), 0)
                    .into_any_element()
            }
            _ => row.into_any_element(),
        }
    }

    /// message_menu is a message's right-click menu: Copy, and in a lead's
    /// session, Fork into a new session from it (MessageMenu.tsx).
    fn message_menu(&self, el: Stateful<Div>, it: &ChatItem, view: &Entity<Self>) -> AnyElement {
        if it.streaming {
            return el.into_any_element();
        }
        let (text, id, lead) = (it.text.clone(), it.id.clone(), self.is_lead());
        let v = view.clone();
        el.context_menu(move |menu, _, _| {
            let text = text.clone();
            let menu = menu.item(PopupMenuItem::new(t("chat.message.copy")).on_click(
                move |_, _, cx| cx.write_to_clipboard(ClipboardItem::new_string(text.clone())),
            ));
            if !lead {
                return menu;
            }
            let (v, id) = (v.clone(), id.clone());
            menu.separator()
                .item(
                    PopupMenuItem::new(t("chat.message.fork")).on_click(move |_, _, cx| {
                        v.update(cx, |_, cx| cx.emit(ChatAction::ForkSession(id.clone())))
                    }),
                )
        })
        .into_any_element()
    }

    /// meta is a message's time and its copy button.
    fn meta(&self, it: &ChatItem, end: bool) -> AnyElement {
        let text = it.text.clone();
        // Timeline's Meta: text-xs faint, its time and a size-6 Copy button.
        h_flex()
            .gap_1()
            .text_xs()
            .line_height(px(16.))
            // Shown while the message is pointed at (Meta's opacity).
            .text_color(transparent_black())
            .group_hover("chat-msg", |s| s.text_color(theme::faint()))
            .when(end, |el| el.justify_end().pr_1())
            .child(local_time(if it.updated_at.is_empty() {
                &it.created_at
            } else {
                &it.updated_at
            }))
            .child(
                div()
                    .id(SharedString::from(format!("copy-{}", it.id)))
                    .size_6()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_md()
                    .cursor_pointer()
                    .hover(|el| el.bg(theme::surface_raised()).text_color(theme::tertiary()))
                    .child(crate::icons::ic("copy").size(px(14.)))
                    .tooltip(|window, cx| {
                        gpui_component::tooltip::Tooltip::new(t("chat.timeline.copyMessage"))
                            .build(window, cx)
                    })
                    .on_click(move |_, _, cx| {
                        cx.write_to_clipboard(ClipboardItem::new_string(text.clone()));
                        crate::toasts::info(cx, t("common.copied"));
                    }),
            )
            .into_any_element()
    }

    fn render_user(&self, ix: usize, view: &Entity<Self>) -> AnyElement {
        let it = self.item(ix);
        let delivery = match it.delivery.as_str() {
            "waiting" | "sent" | "starting" => {
                Some((format!("chat.delivery.{}", it.delivery), theme::faint()))
            }
            "deferred" | "held" | "waking" => {
                Some((format!("chat.delivery.{}", it.delivery), theme::starting()))
            }
            "lost" => Some(("chat.delivery.lost".to_string(), theme::error())),
            _ => None,
        };
        let el = v_flex()
            .id(SharedString::from(format!("user-{}", it.id)))
            .group("chat-msg")
            .w_full()
            .items_end()
            .gap_1()
            .pt_1()
            .pb_4()
            .when(!it.to.is_empty(), |el| {
                el.child(
                    div()
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::accent())
                        .child(format!("→ {}", it.to)),
                )
            })
            .when(!it.images.is_empty(), |el| {
                el.child(
                    h_flex()
                        .gap_1p5()
                        .flex_wrap()
                        .justify_end()
                        .children(it.images.iter().map(|im| self.thumb(&it.id, im, view))),
                )
            })
            .when(!it.files.is_empty(), |el| {
                el.child(
                    h_flex()
                        .gap_1p5()
                        .flex_wrap()
                        .justify_end()
                        .children(it.files.iter().map(|f| self.file_chip(f, view))),
                )
            })
            .when(!it.text.is_empty(), |el| {
                el.child(
                    div()
                        .max_w(relative(0.85))
                        .rounded_2xl()
                        .bg(theme::surface_raised())
                        .px_3p5()
                        .py_2p5()
                        .text_sm()
                        .line_height(relative(1.625))
                        .text_color(theme::primary())
                        .child(it.text.clone()),
                )
            })
            .when_some(delivery, |el, (key, color)| {
                el.child(div().pr_1().text_xs().text_color(color).child(t(&key)))
            })
            .child(self.meta(it, true));
        self.message_menu(el, it, view)
    }

    fn thumb(&self, item: &str, im: &agentbox_api::ChatImage, view: &Entity<Self>) -> AnyElement {
        let name = if im.name.is_empty() {
            t("chat.image.default")
        } else {
            im.name.clone()
        };
        let picture = self.images.get(&im.id).cloned().flatten();
        let v = view.clone();
        div()
            .id(SharedString::from(format!("img-{item}-{}", im.id)))
            .size_24()
            .rounded_xl()
            .overflow_hidden()
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface_raised())
            .cursor_pointer()
            .hover(|el| el.border_color(theme::muted()))
            .when_some(picture.clone(), |el, p: Arc<Image>| {
                el.child(img(p).size_full().object_fit(ObjectFit::Cover))
            })
            .when(picture.is_none(), |el| {
                el.flex()
                    .items_center()
                    .justify_center()
                    .text_xs()
                    .text_color(theme::faint())
                    .child(name.clone())
            })
            .tooltip({
                let label = tv("chat.image.view", &[("name", (&name).into())]);
                move |window, cx| {
                    gpui_component::tooltip::Tooltip::new(label.clone()).build(window, cx)
                }
            })
            .on_click(move |_, window, cx| {
                if let Some(p) = picture.clone() {
                    v.update(cx, |this, cx| this.open_zoom(p, name.clone(), window, cx));
                }
            })
            .into_any_element()
    }

    fn file_chip(&self, f: &agentbox_api::ChatFile, view: &Entity<Self>) -> AnyElement {
        let v = view.clone();
        let file = f.clone();
        h_flex()
            .id(SharedString::from(format!("file-{}", f.id)))
            .h_11()
            .max_w(px(240.))
            .gap_2()
            .px_1p5()
            .pr_3()
            .rounded_xl()
            .border_1()
            .border_color(theme::line())
            .bg(theme::card())
            .cursor_pointer()
            .hover(|el| el.border_color(theme::muted()))
            .child(
                div()
                    .size_8()
                    .flex_none()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_lg()
                    .bg(theme::surface_raised())
                    .text_color(theme::muted())
                    .child(crate::icons::of("▤")),
            )
            .child(
                v_flex()
                    .min_w_0()
                    .child(
                        div()
                            .text_sm()
                            .truncate()
                            .text_color(theme::primary())
                            .child(f.name.clone()),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(crate::files::human_bytes(f.size)),
                    ),
            )
            .tooltip({
                let label = tv("chat.files.open", &[("name", (&f.name).into())]);
                move |window, cx| {
                    gpui_component::tooltip::Tooltip::new(label.clone()).build(window, cx)
                }
            })
            .on_click(move |_, _, cx| v.update(cx, |this, cx| this.open_file(file.clone(), cx)))
            .into_any_element()
    }

    /// open_file reads a file sent in the chat and opens it with the
    /// system's app for it (api.openChatFile).
    fn open_file(&mut self, f: agentbox_api::ChatFile, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let path = format!("{}/chat/files/{}", self.prefix, f.id);
        cx.spawn(async move |_, cx| {
            let res = client
                .raw(agentbox_api::client::Method::GET, &path, None)
                .await;
            cx.update(|cx| match res {
                Ok(bytes) => {
                    let dir = std::env::temp_dir().join("agentbox-chat-files").join(&f.id);
                    let name = std::path::Path::new(&f.name)
                        .file_name()
                        .map(|n| n.to_owned())
                        .unwrap_or_else(|| "file".into());
                    let file = dir.join(name);
                    match std::fs::create_dir_all(&dir).and_then(|_| std::fs::write(&file, &bytes))
                    {
                        Ok(()) => cx.open_with_system(&file),
                        Err(e) => crate::toasts::error(cx, e.to_string()),
                    }
                }
                Err(e) => crate::toasts::error(cx, e.to_string()),
            });
        })
        .detach();
    }

    fn render_assistant(&self, ix: usize, last: bool, view: &Entity<Self>) -> AnyElement {
        let it = self.item(ix);
        let el = v_flex()
            .id(SharedString::from(format!("assistant-{}", it.id)))
            .group("chat-msg")
            .w_full()
            .min_w_0()
            .px_1()
            .when(last, |el| el.pb_4())
            .when(!last, |el| el.pb_2p5())
            // Timeline's assistant text: text-sm leading-relaxed text-tertiary.
            .text_size(theme::text::BODY)
            .line_height(relative(1.625))
            .text_color(theme::tertiary())
            // A reply streaming in since it started here eases in; one already
            // streaming when the chat loaded is drawn as it is.
            .child(
                if let Some(shown) = self.shown.get(&it.id).filter(|_| it.streaming) {
                    super::stream::reveal(&it.id, &it.text, shown.clone())
                } else {
                    crate::mermaid::with_mermaid(TextView::markdown(
                        SharedString::from(format!("md-{}", it.id)),
                        it.text.clone(),
                    ))
                    .selectable(true)
                    .text_color(theme::tertiary())
                    .into_any_element()
                },
            )
            .when(last, |el| {
                el.child(div().mt_1p5().child(self.meta(it, false)))
            });
        self.message_menu(el, it, view)
    }

    fn render_work(&self, key: &str, ixs: &[usize], live: bool, view: &Entity<Self>) -> AnyElement {
        let items: Vec<&ChatItem> = ixs.iter().map(|&i| self.item(i)).collect();
        let Some(last) = items.last().copied() else {
            return div().into_any_element();
        };
        let open = self.expanded.contains(key);
        let head = |label: String, g: &'static str, shine: bool, extra: Option<String>| {
            let (v, k) = (view.clone(), key.to_string());
            h_flex()
                .id(SharedString::from(format!("work-{key}")))
                .w_full()
                .min_h_7()
                .gap_1p5()
                .px_0p5()
                .rounded_md()
                .cursor_pointer()
                .text_sm()
                .hover(|el| el.bg(theme::card()))
                .on_click(move |_, _, cx| v.update(cx, |this, cx| this.toggle(&k, cx)))
                .line_height(relative(1.625))
                .child(icon(g, theme::subtle()))
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_color(if shine {
                            theme::primary()
                        } else {
                            theme::subtle()
                        })
                        .child(label),
                )
                .when_some(extra, |el, e| {
                    el.child(
                        div()
                            .flex_none()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(e),
                    )
                })
                .child(chevron(open))
        };
        let list = || {
            v_flex()
                .ml_3()
                .mt_0p5()
                .pl_2p5()
                .border_l_1()
                .border_color(theme::line())
                .children(items.iter().map(|it| self.render_entry(it, view)))
        };
        if live && rows::is_active(last) {
            let thought = (last.kind == "thought" && !last.text.is_empty() && !open).then(|| {
                let tail: Vec<&str> = last.text.lines().filter(|l| !l.trim().is_empty()).collect();
                let tail = tail[tail.len().saturating_sub(3)..].join("\n");
                div()
                    .ml_8()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(tail)
            });
            return v_flex()
                .pb_2()
                .child(head(
                    rows::live_label(last),
                    glyph(last),
                    true,
                    (items.len() > 1).then(|| format!("+{}", items.len() - 1)),
                ))
                .when(open, |el| el.child(list()))
                .children(thought)
                .into_any_element();
        }
        if items.len() == 1 {
            return div()
                .pb_2()
                .child(self.render_entry(last, view))
                .into_any_element();
        }
        let failed = items
            .iter()
            .any(|i| i.tool.as_ref().is_some_and(|t| t.status == "failed"));
        let first_tool = items
            .iter()
            .find(|i| i.kind == "tool")
            .copied()
            .unwrap_or(items[0]);
        v_flex()
            .pb_2()
            .child(head(
                rows::work_summary(&items),
                glyph(first_tool),
                false,
                failed.then(|| "!".to_string()),
            ))
            .when(open, |el| el.child(list()))
            .into_any_element()
    }

    /// render_entry is one tool call, thought or answered request, opening to
    /// its command and output or its diffs; a Hatch page opens in the browser.
    fn render_entry(&self, it: &ChatItem, view: &Entity<Self>) -> AnyElement {
        let open = self.expanded.contains(&it.id);
        let active = rows::is_active(it);
        let failed = it.tool.as_ref().is_some_and(|t| t.status == "failed");
        let detail = self.entry_detail(it);
        let page = rows::page_url(it);
        let (v, id) = (view.clone(), it.id.clone());
        let has_detail = detail.is_some();
        let row = h_flex()
            .id(SharedString::from(format!("entry-{}", it.id)))
            .w_full()
            .min_h_7()
            .gap_1p5()
            .px_0p5()
            .rounded_md()
            .text_sm()
            .when(has_detail || page.is_some(), |el| {
                el.cursor_pointer().hover(|el| el.bg(theme::card()))
            })
            .child(icon(
                if page.is_some() { "globe" } else { glyph(it) },
                if failed {
                    theme::error()
                } else if page.is_some() {
                    theme::starting()
                } else {
                    theme::faint()
                },
            ))
            .child(
                div()
                    .min_w_0()
                    .truncate()
                    .text_color(if failed {
                        theme::error()
                    } else if active {
                        theme::primary()
                    } else {
                        theme::faint()
                    })
                    .when(page.is_some(), |el| el.underline())
                    .child(if active {
                        rows::live_label(it)
                    } else {
                        rows::entry_label(it)
                    }),
            )
            .when(
                it.tool.as_ref().is_some_and(|t| t.status == "stopped"),
                |el| {
                    el.child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(t("chat.work.stopped")),
                    )
                },
            )
            .when(has_detail, |el| el.child(chevron(open)))
            .on_click(move |_, _, cx| {
                if let Some(url) = &page
                    && !has_detail
                {
                    cx.open_url(url);
                    return;
                }
                v.update(cx, |this, cx| this.toggle(&id, cx));
            });
        v_flex()
            .w_full()
            .min_w_0()
            .child(row)
            .when_some(rows::page_url(it), |el, url| {
                el.child(
                    div().ml_7().child(
                        link(SharedString::from(format!("page-{}", it.id)), url.clone())
                            .on_click(move |_, _, cx| cx.open_url(&url)),
                    ),
                )
            })
            .when(open, |el| {
                el.children(detail.map(|d| {
                    div()
                        .ml_7()
                        .mt_0p5()
                        .mb_1p5()
                        .rounded_lg()
                        .border_1()
                        .border_color(theme::line())
                        .bg(theme::card())
                        .overflow_hidden()
                        .child(d)
                }))
            })
            .into_any_element()
    }

    fn entry_detail(&self, it: &ChatItem) -> Option<AnyElement> {
        if it.kind == "thought" {
            return (!it.text.is_empty()).then(|| {
                div()
                    .px_3()
                    .py_2()
                    .text_sm()
                    .text_color(theme::faint())
                    .child(it.text.clone())
                    .into_any_element()
            });
        }
        let tool = it.tool.as_ref()?;
        if !tool.diffs.is_empty() {
            return Some(
                v_flex()
                    .gap_1()
                    .p_1()
                    .children(
                        tool.diffs
                            .iter()
                            .enumerate()
                            .map(|(i, d)| diff_block(format!("diff-{}-{i}", it.id), d)),
                    )
                    .into_any_element(),
            );
        }
        let command =
            (tool.kind == "execute" && !tool.command.is_empty()).then(|| tool.command.clone());
        if command.is_none() && tool.output.is_empty() {
            return None;
        }
        let out: String = tool.output.chars().take(6000).collect();
        Some(
            mono()
                .when_some(command, |el, c| {
                    el.child(div().text_color(theme::primary()).child(format!("$ {c}")))
                })
                .when(!out.is_empty(), |el| el.child(out))
                .into_any_element(),
        )
    }

    fn render_subagent(&self, ix: usize, children: &[usize], view: &Entity<Self>) -> AnyElement {
        let it = self.item(ix);
        let Some(sub) = &it.subagent else {
            return div().into_any_element();
        };
        let open = self.expanded.contains(&it.id);
        let running = sub.state == "running";
        let kids: Vec<&ChatItem> = children.iter().map(|&i| self.item(i)).collect();
        let work: Vec<&ChatItem> = kids.iter().copied().filter(|i| rows::is_work(i)).collect();
        let current = if running {
            kids.iter().rev().find(|i| rows::is_active(i)).copied()
        } else {
            None
        };
        let summary = match current {
            Some(c) => rows::live_label(c),
            None if !work.is_empty() => rows::work_summary(&work),
            None if running => t("chat.subagent.starting"),
            None => t("chat.subagent.didNothing"),
        };
        let state = match sub.state.as_str() {
            "running" => div()
                .text_xs()
                .text_color(theme::faint())
                .child(t("chat.subagent.working")),
            "completed" => div()
                .text_color(theme::running())
                .child(crate::icons::of("✓")),
            "failed" => div().text_color(theme::error()).child("!"),
            _ => div()
                .text_xs()
                .text_color(theme::faint())
                .child(t("chat.work.stopped")),
        };
        let (v, id) = (view.clone(), it.id.clone());
        div()
            .pb_2()
            .child(
                v_flex()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::card())
                    .child(
                        h_flex()
                            .id(SharedString::from(format!("sub-{}", it.id)))
                            .items_start()
                            .gap_2()
                            .px_2()
                            .py_1p5()
                            .cursor_pointer()
                            .rounded_xl()
                            .hover(|el| el.bg(theme::surface_raised()))
                            .on_click(move |_, _, cx| v.update(cx, |this, cx| this.toggle(&id, cx)))
                            .child(icon("◆", theme::muted()))
                            .child(
                                v_flex()
                                    .min_w_0()
                                    .flex_1()
                                    .child(
                                        h_flex()
                                            .gap_1p5()
                                            .text_sm()
                                            .child(
                                                div()
                                                    .flex_none()
                                                    .font_weight(FontWeight::MEDIUM)
                                                    .text_color(theme::primary())
                                                    .child(sub.name.clone()),
                                            )
                                            .when(!sub.task.is_empty() && !open, |el| {
                                                el.child(
                                                    div()
                                                        .min_w_0()
                                                        .truncate()
                                                        .text_color(theme::faint())
                                                        .child(sub.task.clone()),
                                                )
                                            }),
                                    )
                                    .child(
                                        div()
                                            .text_xs()
                                            .truncate()
                                            .text_color(if running {
                                                theme::primary()
                                            } else {
                                                theme::faint()
                                            })
                                            .child(summary),
                                    ),
                            )
                            .child(state)
                            .child(chevron(open)),
                    )
                    .when(open, |el| {
                        el.child(
                            v_flex()
                                .mx_2()
                                .mb_2()
                                .pt_1p5()
                                .border_t_1()
                                .border_color(theme::line())
                                .when(!sub.task.is_empty(), |el| {
                                    el.child(
                                        div()
                                            .px_1()
                                            .mb_1p5()
                                            .text_sm()
                                            .text_color(theme::faint())
                                            .child(sub.task.clone()),
                                    )
                                })
                                .child(
                                    v_flex()
                                        .ml_3()
                                        .pl_2p5()
                                        .border_l_1()
                                        .border_color(theme::line())
                                        .when(kids.is_empty(), |el| {
                                            el.child(
                                                div()
                                                    .py_1()
                                                    .text_sm()
                                                    .text_color(theme::faint())
                                                    .child(t("chat.subagent.nothingYet")),
                                            )
                                        })
                                        .children(kids.iter().filter_map(|k| {
                                            match k.kind.as_str() {
                                                "assistant" => Some(
                                                    div()
                                                        .py_1()
                                                        .pl_1()
                                                        .text_sm()
                                                        .child(
                                                            TextView::markdown(
                                                                SharedString::from(format!(
                                                                    "md-{}",
                                                                    k.id
                                                                )),
                                                                k.text.clone(),
                                                            )
                                                            .text_color(theme::muted()),
                                                        )
                                                        .into_any_element(),
                                                ),
                                                _ if rows::is_work(k) => {
                                                    Some(self.render_entry(k, view))
                                                }
                                                _ => None,
                                            }
                                        })),
                                ),
                        )
                    }),
            )
            .into_any_element()
    }

    fn render_compaction(&self, ix: usize) -> AnyElement {
        let it = self.item(ix);
        let c = it.compaction.clone().unwrap_or_default();
        if c.state == "running" {
            return div()
                .mb_4()
                .child(
                    card(theme::muted())
                        .child(
                            h_flex().gap_2().child(crate::icons::of("⟳")).child(
                                div()
                                    .text_color(theme::primary())
                                    .child(t("chat.compaction.running")),
                            ),
                        )
                        .when(c.waiting > 0, |el| {
                            el.child(div().text_color(theme::faint()).child(tv(
                                "chat.compaction.waiting",
                                &[("count", c.waiting.into())],
                            )))
                        }),
                )
                .into_any_element();
        }
        let failed = c.state == "failed";
        div()
            .mb_4()
            .child(
                card(if failed {
                    theme::starting()
                } else {
                    theme::muted()
                })
                .child(
                    h_flex()
                        .items_start()
                        .gap_2()
                        .child(crate::icons::of(if failed { "!" } else { "▤" }))
                        .child(
                            div()
                                .min_w_0()
                                .text_color(theme::muted())
                                .child(it.text.clone()),
                        ),
                ),
            )
            .into_any_element()
    }

    fn render_note(&self, ix: usize, view: &Entity<Self>) -> AnyElement {
        let it = self.item(ix);
        if let Some(pm) = &it.project_message {
            return self.render_project_message(it, pm, view);
        }
        if let Some(run) = &it.scheduled_run {
            let mut lines: Vec<String> = Vec::new();
            if run.trigger == "manual" {
                lines.push(t("chat.scheduled.manual"));
            }
            if !run.event.is_empty() {
                lines.push(run.event.clone());
            }
            if run.late
                && let Some(due) = &run.due_at
            {
                lines.push(tv(
                    "chat.scheduled.late",
                    &[("due", local_time(due).into())],
                ));
            }
            if run.late && run.missed > 0 {
                lines.push(tv("chat.scheduled.missed", &[("count", run.missed.into())]));
            }
            if run.target == "agent" {
                lines.push(if run.agent.is_empty() {
                    t("chat.scheduled.toNewAgent")
                } else {
                    tv("chat.scheduled.toAgent", &[("agent", (&run.agent).into())])
                });
            }
            let tint = if run.late {
                theme::starting()
            } else {
                theme::accent()
            };
            return v_flex()
                .mb_4()
                .gap_1p5()
                .text_sm()
                .child(
                    h_flex()
                        .gap_2()
                        .flex_wrap()
                        .child(
                            div()
                                .px_2p5()
                                .py_0p5()
                                .rounded_full()
                                .border_1()
                                .border_color(tint.opacity(0.3))
                                .bg(tint.opacity(0.08))
                                .text_xs()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(tint)
                                .child(format!(
                                    "{}  {}",
                                    if run.trigger == "event" { "⚡" } else { "⏰" },
                                    tv(
                                        if run.trigger == "event" {
                                            "chat.scheduled.eventTitle"
                                        } else {
                                            "chat.scheduled.title"
                                        },
                                        &[("title", (&run.title).into())]
                                    )
                                )),
                        )
                        .when(!lines.is_empty(), |el| {
                            el.child(
                                div()
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(lines.join(" · ")),
                            )
                        }),
                )
                .child(
                    div().pl_3().border_l_2().border_color(theme::line()).child(
                        TextView::markdown(
                            SharedString::from(format!("md-{}", it.id)),
                            it.text.clone(),
                        )
                        .text_color(theme::muted()),
                    ),
                )
                .into_any_element();
        }
        if let Some(ap) = &it.agent_permission {
            return self.render_agent_permission(it, ap, view);
        }
        if !it.finished.is_empty() {
            // An agent's finish, as its card (EvidenceCard).
            return self.render_finished(&it.id, &it.finished, view);
        }
        if it.kind == "error" {
            return div()
                .mb_4()
                .child(
                    card(theme::error()).child(
                        h_flex()
                            .items_start()
                            .gap_2()
                            .child(div().text_color(theme::error()).child("!"))
                            .child(
                                div()
                                    .min_w_0()
                                    .text_color(theme::primary())
                                    .child(it.text.clone()),
                            ),
                    ),
                )
                .into_any_element();
        }
        if it.kind == "goal"
            && let Some(g) = &it.goal
        {
            return h_flex()
                .mb_3()
                .gap_2()
                .text_sm()
                .text_color(theme::muted())
                .child(crate::icons::of("◎"))
                .child(div().min_w_0().truncate().child(g.objective.clone()))
                .child(
                    div()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(g.status.clone()),
                )
                .into_any_element();
        }
        if it.text.is_empty() && it.handoff.is_empty() {
            return div().into_any_element();
        }
        let key = format!("handoff:{}", it.id);
        let open = self.expanded.contains(&key);
        let v = view.clone();
        div()
            .mb_4()
            .child(
                v_flex()
                    .w_full()
                    .px_3p5()
                    .py_2p5()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::card())
                    .text_sm()
                    .child(
                        h_flex()
                            .items_start()
                            .gap_2()
                            .child(div().text_color(theme::faint()).child("i"))
                            .child(
                                div()
                                    .min_w_0()
                                    .text_color(theme::muted())
                                    .child(it.text.clone()),
                            ),
                    )
                    .when(!it.handoff.is_empty(), |el| {
                        el.child(
                            h_flex()
                                .id(SharedString::from(format!("handoff-{}", it.id)))
                                .ml_6()
                                .mt_1()
                                .gap_1()
                                .cursor_pointer()
                                .text_xs()
                                .text_color(theme::faint())
                                .hover(|el| el.text_color(theme::primary()))
                                .on_click(move |_, _, cx| {
                                    v.update(cx, |this, cx| this.toggle(&key, cx))
                                })
                                .child(t("chat.timeline.handoff"))
                                .child(chevron(open)),
                        )
                        .when(open, |el| {
                            el.child(
                                mono()
                                    .ml_6()
                                    .mt_1p5()
                                    .rounded_lg()
                                    .bg(theme::surface_raised())
                                    .child(it.handoff.clone()),
                            )
                        })
                    }),
            )
            .into_any_element()
    }

    fn render_project_message(
        &self,
        it: &ChatItem,
        pm: &agentbox_api::ChatProjectMessage,
        view: &Entity<Self>,
    ) -> AnyElement {
        let home = pm.project == agentbox_api::HOME_PROJECT;
        let name = if home {
            t("chat.project.homeName")
        } else {
            pm.project.clone()
        };
        let incoming = pm.direction == "in";
        let settled = pm.kind == "settled";
        let p = || [("project", (&name).into())];
        let title = if settled {
            tv("chat.project.settled", &p())
        } else if home {
            t(if incoming {
                "chat.project.fromHome"
            } else {
                "chat.project.replyToHome"
            })
        } else if pm.kind == "reply" {
            tv(
                if incoming {
                    "chat.project.replyFrom"
                } else {
                    "chat.project.replyTo"
                },
                &p(),
            )
        } else {
            tv(
                if incoming {
                    "chat.project.from"
                } else {
                    "chat.project.to"
                },
                &p(),
            )
        };
        let tint = if settled {
            theme::running()
        } else {
            theme::accent()
        };
        let (v, project) = (view.clone(), pm.project.clone());
        div()
            .mb_4()
            .when(!incoming && !settled, |el| el.pl_8())
            .child(
                card(tint)
                    .child(
                        h_flex()
                            .gap_2()
                            .flex_wrap()
                            .child(div().text_color(tint).child(if settled {
                                "⇆"
                            } else if incoming {
                                "↙"
                            } else {
                                "↗"
                            }))
                            .child(
                                div()
                                    .min_w_0()
                                    .truncate()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(title),
                            )
                            .when(!pm.topic.is_empty() && !settled, |el| {
                                el.child(
                                    div()
                                        .px_1p5()
                                        .rounded_md()
                                        .bg(tint.opacity(0.15))
                                        .text_xs()
                                        .text_color(tint)
                                        .child(tv(
                                            "chat.project.aligning",
                                            &[("topic", (&pm.topic).into())],
                                        )),
                                )
                            })
                            .when(pm.expects_reply && pm.topic.is_empty(), |el| {
                                el.child(
                                    div()
                                        .px_1p5()
                                        .rounded_md()
                                        .bg(theme::surface_raised())
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(t("chat.project.expectsReply")),
                                )
                            })
                            .child(div().flex_1())
                            .child(
                                link(
                                    SharedString::from(format!("pm-{}", it.id)),
                                    tv("chat.project.open", &[("project", (&name).into())]),
                                )
                                .on_click(move |_, _, cx| {
                                    v.update(cx, |_, cx| {
                                        cx.emit(ChatAction::OpenProject(project.clone()))
                                    })
                                }),
                            ),
                    )
                    .when(settled, |el| {
                        el.child(div().text_xs().text_color(theme::running()).child(
                            if pm.topic.is_empty() {
                                t("chat.project.agreed")
                            } else {
                                format!("{} · {}", pm.topic, t("chat.project.agreed"))
                            },
                        ))
                    })
                    .child(
                        div().mt_1().child(
                            TextView::markdown(
                                SharedString::from(format!("md-{}", it.id)),
                                it.text.clone(),
                            )
                            .text_color(theme::primary()),
                        ),
                    ),
            )
            .into_any_element()
    }

    fn render_agent_permission(
        &self,
        it: &ChatItem,
        ap: &agentbox_api::ChatAgentPermission,
        view: &Entity<Self>,
    ) -> AnyElement {
        let p = &ap.permission;
        let state = agent_permission_state(p);
        let waiting = state == "waiting";
        let tint = match state {
            "waiting" => theme::starting(),
            "denied" => theme::error(),
            "gone" => theme::muted(),
            _ => theme::running(),
        };
        let who = if ap.title.is_empty() {
            &ap.agent
        } else {
            &ap.title
        };
        let tool = ap.tool.clone().unwrap_or_default();
        let detail = if tool.command.is_empty() {
            tool.paths.first().cloned().unwrap_or_default()
        } else {
            tool.command.clone()
        };
        let (v, r) = (view.clone(), ap.r#ref.clone());
        let (pp, pa) = ap.r#ref.split_once('/').unwrap_or((&ap.r#ref, ""));
        let path = format!("/v1/agents/{pp}/{pa}/chat/permissions/{}", ap.item);
        div()
            .mb_4()
            .child(
                card(if waiting {
                    theme::starting()
                } else {
                    theme::muted()
                })
                .child(
                    h_flex()
                        .gap_2()
                        .child(div().text_color(tint).child(match state {
                            "denied" => "✕",
                            "gone" => "○",
                            "waiting" => "!",
                            _ => "✓",
                        }))
                        .child(
                            div()
                                .min_w_0()
                                .flex_1()
                                .truncate()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(tv(
                                    "chat.agentPermission.asks",
                                    &[("agent", who.into()), ("action", (&tool.kind).into())],
                                )),
                        )
                        .child(
                            link(
                                SharedString::from(format!("ap-{}", it.id)),
                                t("chat.agentPermission.open"),
                            )
                            .on_click(move |_, _, cx| {
                                v.update(cx, |_, cx| cx.emit(ChatAction::OpenAgent(r.clone())))
                            }),
                        ),
                )
                .child(
                    div()
                        .text_color(if waiting {
                            theme::primary()
                        } else {
                            theme::muted()
                        })
                        .child(p.title.clone()),
                )
                .when(!detail.is_empty() && !p.title.contains(&detail), |el| {
                    el.child(mono().px_0().py_0().child(detail.clone()))
                })
                .when(waiting && !tool.diffs.is_empty(), |el| {
                    el.children(
                        tool.diffs
                            .iter()
                            .enumerate()
                            .map(|(i, d)| diff_block(format!("apd-{}-{i}", it.id), d)),
                    )
                })
                .when(waiting, |el| {
                    el.child(self.permission_buttons(&it.id, path.clone(), p, view, false))
                })
                .when(!waiting, |el| {
                    el.child(
                        div()
                            .text_xs()
                            .text_color(tint)
                            .child(t(&format!("chat.agentPermission.{state}"))),
                    )
                }),
            )
            .into_any_element()
    }

    /// permission_buttons answer a permission request (PermissionActions):
    /// Deny, then Allow, the usual answer, with "Always allow …" and the
    /// tool's other ways in a menu on its chevron. A plan's approval approves
    /// on the mode chosen beside Plan. With `keys`, the Y and N it answers to
    /// show on the buttons.
    pub(super) fn permission_buttons(
        &self,
        item: &str,
        path: String,
        p: &ChatPermission,
        view: &Entity<Self>,
        keys: bool,
    ) -> AnyElement {
        let sending = self.answering.contains(item);
        let planned = (!p.plan_approve.is_empty())
            .then(|| p.options.iter().find(|o| o.id == p.plan_approve))
            .flatten();
        let approve = planned.or_else(|| p.options.iter().find(|o| o.kind == "allow_once"));
        let always = if planned.is_some() {
            None
        } else {
            p.options.iter().find(|o| o.kind == "allow_always")
        };
        let others: Vec<_> = match planned {
            Some(pl) => p
                .options
                .iter()
                .filter(|o| o.kind.starts_with("allow") && o.id != pl.id)
                .cloned()
                .collect(),
            None => Vec::new(),
        };
        let deny = p
            .options
            .iter()
            .find(|o| o.kind == "reject_once")
            .or_else(|| p.options.iter().find(|o| o.kind.starts_with("reject")));
        let approve_text = match planned {
            None => t("chat.permission.allow"),
            Some(pl) if pl.mode_kind == "full_access" => t("chat.plan.approveBypass"),
            Some(_) => t("chat.plan.approve"),
        };
        let deny_text = if planned.is_some() {
            t("chat.plan.keepPlanning")
        } else {
            t("chat.permission.deny")
        };
        let agent = self.agent_name.clone();
        let action = self
            .items
            .iter()
            .rev()
            .find_map(|i| {
                i.tool
                    .as_ref()
                    .filter(|tl| !p.call_id.is_empty() && tl.call_id == p.call_id)
                    .map(|tl| tl.kind.clone())
            })
            .unwrap_or_default();
        let answer = |option: String| {
            let (v, item, path) = (view.clone(), item.to_string(), path.clone());
            move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                v.update(cx, |this, cx| {
                    this.answer(path.clone(), item.clone(), option.clone(), cx)
                })
            }
        };
        let split = always.is_some() || !others.is_empty();
        h_flex()
            .mt(px(10.))
            .flex_wrap()
            .justify_end()
            .gap_1p5()
            .when_some(deny, |el, d| {
                el.child(
                    h_flex()
                        .id(SharedString::from(format!("perm-{item}-{}", d.id)))
                        .h_7()
                        .gap_1p5()
                        .px(px(10.))
                        .rounded_lg()
                        .text_size(theme::text::UI)
                        .text_color(theme::rose(200).opacity(0.9))
                        .when(sending, |el| el.opacity(0.5))
                        .when(!sending, |el| {
                            el.cursor_pointer()
                                .hover(|el| el.bg(theme::rose(400).opacity(0.1)))
                                .on_click(answer(d.id.clone()))
                        })
                        .child(deny_text)
                        .when(keys, |el| el.child(theme::key_cap("N"))),
                )
            })
            .when_some(approve, |el, a| {
                let menu = split.then(|| {
                    let (always, others) = (always.cloned(), others.clone());
                    let (agent, action) = (agent.clone(), action.clone());
                    let (v, item, path) = (view.clone(), item.to_string(), path.clone());
                    Button::new(SharedString::from(format!("perm-{item}-more")))
                        .ghost()
                        .compact()
                        .h_7()
                        .px_1p5()
                        .rounded(gpui_component::button::ButtonRounded::None)
                        .text_color(theme::on_bright())
                        .icon(crate::icons::ic("chevron-down").size(px(14.)))
                        .tooltip(t("chat.permission.moreChoices"))
                        .dropdown_menu_with_anchor(Anchor::TopRight, move |mut menu, _, _| {
                            menu = menu.min_w(px(224.));
                            let pick = |option: String| {
                                let (v, item, path) = (v.clone(), item.clone(), path.clone());
                                move |_: &ClickEvent, _: &mut Window, cx: &mut App| {
                                    v.update(cx, |this, cx| {
                                        this.answer(path.clone(), item.clone(), option.clone(), cx)
                                    })
                                }
                            };
                            if let Some(al) = &always {
                                menu = menu.item(
                                    PopupMenuItem::new(tv(
                                        "chat.permission.alwaysFor",
                                        &[("action", (&action).into()), ("agent", (&agent).into())],
                                    ))
                                    .icon(crate::icons::ic("check"))
                                    .on_click(pick(al.id.clone())),
                                );
                            }
                            for o in &others {
                                menu = menu.item(
                                    PopupMenuItem::new(o.name.clone())
                                        .icon(crate::icons::ic("check"))
                                        .on_click(pick(o.id.clone())),
                                );
                            }
                            menu
                        })
                });
                el.child(
                    h_flex()
                        .items_stretch()
                        .child(
                            h_flex()
                                .id(SharedString::from(format!("perm-{item}-{}", a.id)))
                                .h_7()
                                .gap_1p5()
                                .px_3()
                                .map(|el| {
                                    if split {
                                        el.rounded_l_lg()
                                    } else {
                                        el.rounded_lg()
                                    }
                                })
                                .bg(theme::amber(300))
                                .text_size(theme::text::UI)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::on_bright())
                                .when(sending, |el| el.opacity(0.6))
                                .when(!sending, |el| {
                                    el.cursor_pointer()
                                        .hover(|el| el.bg(theme::amber(200)))
                                        .on_click(answer(a.id.clone()))
                                })
                                .child(if sending {
                                    t("chat.permission.sending")
                                } else {
                                    approve_text
                                })
                                .when(keys && !sending, |el| el.child(theme::key_cap("Y"))),
                        )
                        .children(menu.map(|m| {
                            div()
                                .flex()
                                .rounded_r_lg()
                                .overflow_hidden()
                                .border_l_1()
                                .border_color(black().opacity(0.2))
                                .bg(theme::amber(300))
                                .when(sending, |el| el.opacity(0.6))
                                .child(m)
                        })),
                )
            })
            .into_any_element()
    }

    /// render_zoom is a picture over everything (ZoomableImage): the wheel
    /// zooms it, dragging pans it, a double-click or 0 fits it again.
    pub(super) fn render_zoom(
        &self,
        window: &Window,
        cx: &mut Context<Self>,
    ) -> Option<AnyElement> {
        let z = self.zoom.as_ref()?;
        let (scale, pan) = (z.scale, z.pan);
        let size = window.viewport_size();
        // Over the whole window, not just the chat.
        let stage = div()
            .id("zoom")
            .track_focus(&z.focus)
            .w(size.width)
            .h(size.height)
            .bg(theme::media_shade(0.92))
            .flex()
            .flex_col()
            .occlude()
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                let Some(z) = this.zoom.as_mut() else { return };
                match ev.keystroke.key.as_str() {
                    "escape" => this.zoom = None,
                    "+" | "=" => z.scale = (z.scale * 1.25).min(8.),
                    "-" => z.scale = (z.scale / 1.25).max(1.),
                    "0" => {
                        z.scale = 1.;
                        z.pan = Point::default();
                    }
                    _ => return,
                }
                cx.stop_propagation();
                cx.notify();
            }))
            .child(
                h_flex()
                    .flex_none()
                    .px_4()
                    .py_2()
                    .gap_3()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .truncate()
                            .text_sm()
                            .text_color(theme::on_media().opacity(0.8))
                            .child(z.name.clone()),
                    )
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::on_media().opacity(0.6))
                            .child(format!("{:.0}%", scale * 100.)),
                    )
                    .child(
                        div()
                            .id("zoom-close")
                            .px_2()
                            .rounded_md()
                            .cursor_pointer()
                            .text_color(theme::on_media())
                            .hover(|el| el.bg(theme::on_media().opacity(0.1)))
                            .child(crate::icons::of("✕"))
                            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.zoom = None;
                                cx.notify();
                            })),
                    ),
            )
            .child(
                div()
                    .id("zoom-stage")
                    .flex_1()
                    .min_h_0()
                    .overflow_hidden()
                    .relative()
                    .on_scroll(cx.listener(|this, ev: &ScrollEvent, _, cx| {
                        let Some(z) = this.zoom.as_mut() else { return };
                        let dy = f32::from(ev.delta.pixel_delta(px(20.)).y);
                        z.scale = (z.scale * (1. + dy / 400.)).clamp(1., 8.);
                        if z.scale == 1. {
                            z.pan = Point::default();
                        }
                        cx.notify();
                    }))
                    .on_mouse_down(
                        MouseButton::Left,
                        cx.listener(|this, ev: &MouseDownEvent, _, cx| {
                            if let Some(z) = this.zoom.as_mut() {
                                if ev.click_count >= 2 {
                                    z.scale = if z.scale > 1. { 1. } else { 2. };
                                    z.pan = Point::default();
                                } else {
                                    z.drag = Some(ev.position);
                                }
                                cx.notify();
                            }
                        }),
                    )
                    .on_mouse_move(cx.listener(|this, ev: &MouseMoveEvent, _, cx| {
                        if let Some(z) = this.zoom.as_mut()
                            && let Some(from) = z.drag
                        {
                            if !ev.dragging() {
                                z.drag = None;
                                return;
                            }
                            z.pan += ev.position - from;
                            z.drag = Some(ev.position);
                            cx.notify();
                        }
                    }))
                    .on_mouse_up(
                        MouseButton::Left,
                        cx.listener(|this, _: &MouseUpEvent, _, _| {
                            if let Some(z) = this.zoom.as_mut() {
                                z.drag = None;
                            }
                        }),
                    )
                    .child(
                        div()
                            .absolute()
                            .top(pan.y)
                            .left(pan.x)
                            .size_full()
                            .p_6()
                            .flex()
                            .items_center()
                            .justify_center()
                            .child(
                                img(z.image.clone())
                                    .w(relative(scale))
                                    .h(relative(scale))
                                    .flex_none()
                                    .object_fit(ObjectFit::Contain),
                            ),
                    ),
            );
        Some(
            deferred(anchored().position(Point::default()).child(stage))
                .with_priority(2)
                .into_any_element(),
        )
    }
}

pub fn agent_permission_state(p: &ChatPermission) -> &'static str {
    if p.outcome.is_empty() {
        return "waiting";
    }
    match p
        .options
        .iter()
        .find(|o| o.id == p.outcome)
        .map(|o| o.kind.as_str())
    {
        None => "gone",
        Some(k) if k.starts_with("reject") => "denied",
        Some("allow_always") => "approvedAlways",
        Some(_) => "approved",
    }
}
