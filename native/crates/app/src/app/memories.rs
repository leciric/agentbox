//! A project's Memories page: everything the project remembers, to browse
//! (search, filter by kind, importance and where it came from, open one to
//! read it), and to share with whoever else works on the project by
//! committing it to the repository (POST …/memory/share), on a branch the
//! user pushes and opens a pull request from. Memories a co-worker shared
//! come back from the daemon marked as from the repository.
use std::collections::BTreeSet;
use std::rc::Rc;

use agentbox_api::{MEMORY_SOURCE_REPOSITORY, Memory, ShareMemoriesRequest, ShareMemoriesResult};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn, Choice};
use super::settings::defaults::time_ago;
use crate::{theme, toasts};

/// What the page reads and what was picked on it.
#[derive(Default)]
pub struct MemoriesPage {
    /// The project it is about.
    project: String,
    list: Option<Result<Vec<Memory>, String>>,
    search: Option<Entity<InputState>>,
    _search: Option<Subscription>,
    q: String,
    /// The kind shown, "" for every kind.
    kind: String,
    /// The least importance shown, "" for any.
    importance: String,
    /// "" for all, "agentbox" for the project's own, "repository" for shared ones.
    source: String,
    /// The memories ticked for sharing, by id.
    selected: BTreeSet<String>,
    /// The memory open on the right.
    open: Option<String>,
    sharing: bool,
    /// What the last share did, shown until the page is left.
    shared: Option<ShareMemoriesResult>,
}

const KINDS: [&str; 5] = ["project", "decision", "discovery", "issue", "episodic"];

fn kind_label(kind: &str) -> String {
    let key = format!("memory.kind.{kind}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        kind.to_string()
    }
}

fn kind_color(kind: &str) -> Hsla {
    match kind {
        "project" | "decision" => theme::accent(),
        "discovery" => theme::running(),
        "issue" => theme::starting(),
        _ => theme::muted(),
    }
}

fn from_repo(m: &Memory) -> bool {
    m.source == MEMORY_SOURCE_REPOSITORY
}

/// shows is whether m passes the page's filters: every word of the search
/// in its title or content, its kind, its importance and its source.
fn shows(p: &MemoriesPage, words: &[String], m: &Memory) -> bool {
    if !p.kind.is_empty() && m.kind != p.kind {
        return false;
    }
    if let Ok(least) = p.importance.parse::<i64>()
        && m.importance < least
    {
        return false;
    }
    match p.source.as_str() {
        "agentbox" if from_repo(m) => return false,
        "repository" if !from_repo(m) => return false,
        _ => {}
    }
    let hay = format!("{}\n{}", m.title, m.content).to_lowercase();
    words.iter().all(|w| hay.contains(w.as_str()))
}

fn importance_dots(n: i64) -> impl IntoElement {
    h_flex().flex_none().gap(px(3.)).children((1..=5).map(move |i| {
        div().size(px(6.)).rounded_full().bg(if i <= n {
            theme::accent()
        } else {
            theme::surface_strong()
        })
    }))
}

fn repo_badge() -> AnyElement {
    h_flex()
        .flex_none()
        .gap_1()
        .px(px(6.))
        .py(px(1.))
        .rounded_full()
        .bg(theme::sky(400).opacity(0.12))
        .text_color(theme::sky(300))
        .text_size(theme::text::MICRO)
        .font_weight(FontWeight::MEDIUM)
        .child(crate::icons::ic("git-branch").size(px(11.)))
        .child(t("native.memories.fromRepository"))
        .into_any_element()
}

/// tick is a checkbox: a rounded square, filled with a check when on.
fn tick(id: impl Into<ElementId>, on: bool, on_click: impl Fn(&ClickEvent, &mut Window, &mut App) + 'static) -> AnyElement {
    div()
        .id(id)
        .flex_none()
        .size(px(16.))
        .flex()
        .items_center()
        .justify_center()
        .rounded(px(4.))
        .border_1()
        .cursor_pointer()
        .map(|el| {
            if on {
                el.bg(theme::brand(500))
                    .border_color(theme::brand(500))
                    .text_color(gpui::white())
                    .child(crate::icons::ic("check").size(px(12.)))
            } else {
                el.border_color(theme::line_vivid())
                    .hover(|el| el.border_color(theme::muted()))
            }
        })
        .on_click(on_click)
        .into_any_element()
}

impl AppView {
    /// open_memories opens the project's Memories page in the middle.
    pub(super) fn open_memories(&mut self, project: String, window: &mut Window, cx: &mut Context<Self>) {
        if self.project.as_deref() != Some(project.as_str()) {
            self.select_project(project.clone(), window, cx);
        }
        let p = &mut self.memories;
        if p.project != project {
            *p = MemoriesPage {
                project: project.clone(),
                search: p.search.take(),
                _search: p._search.take(),
                ..Default::default()
            };
        }
        p.shared = None;
        if p.search.is_none() {
            let search = cx.new(|cx| InputState::new(window, cx).placeholder(t("native.memories.search")));
            let sub = cx.subscribe(&search, |this: &mut Self, s, ev: &InputEvent, cx| {
                if matches!(ev, InputEvent::Change) {
                    this.memories.q = s.read(cx).value().trim().to_lowercase();
                    cx.notify();
                }
            });
            p.search = Some(search);
            p._search = Some(sub);
        } else if let Some(s) = &p.search {
            s.update(cx, |s, cx| s.set_value("", window, cx));
            p.q.clear();
        }
        self.open_foot_page(super::foot::FootPage::Memories, window, cx);
    }

    /// memories_crumb is the top bar's breadcrumb: the project, then Memories.
    pub(super) fn memories_crumb(&self) -> String {
        let name = &self.memories.project;
        let display = self
            .projects
            .iter()
            .find(|p| &p.name == name)
            .map(|p| if p.display_name.is_empty() { p.name.clone() } else { p.display_name.clone() })
            .unwrap_or_else(|| name.clone());
        format!("{display} › {}", t("native.memories.title"))
    }

    pub(super) fn load_memories(&mut self, cx: &mut Context<Self>) {
        let project = self.memories.project.clone();
        if project.is_empty() {
            return;
        }
        let client = self.client.clone();
        let path = format!("/v1/projects/{}/memory/memories", super::foot::enc(&project));
        cx.spawn(async move |this, cx| {
            let res = client.get::<Vec<Memory>>(&path).await;
            this.update(cx, |this, cx| {
                let p = &mut this.memories;
                if p.project != project {
                    return;
                }
                match res {
                    Ok(list) => {
                        // What was ticked or open and is gone stops being so.
                        p.selected.retain(|id| list.iter().any(|m| &m.id == id));
                        if p.open.as_ref().is_some_and(|id| !list.iter().any(|m| &m.id == id)) {
                            p.open = None;
                        }
                        if p.open.is_none() {
                            p.open = list.first().map(|m| m.id.clone());
                        }
                        p.list = Some(Ok(list));
                    }
                    Err(e) => p.list = Some(Err(e.to_string())),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn share_memories(&mut self, all: bool, cx: &mut Context<Self>) {
        let p = &mut self.memories;
        if p.sharing {
            return;
        }
        let body = ShareMemoriesRequest {
            ids: if all { Vec::new() } else { p.selected.iter().cloned().collect() },
            all,
        };
        p.sharing = true;
        let project = p.project.clone();
        let client = self.client.clone();
        let path = format!("/v1/projects/{}/memory/share", super::foot::enc(&project));
        cx.notify();
        cx.spawn(async move |this, cx| {
            let res = client.post::<_, ShareMemoriesResult>(&path, &body).await;
            this.update(cx, |this, cx| {
                let p = &mut this.memories;
                p.sharing = false;
                match res {
                    Ok(out) => {
                        p.selected.clear();
                        p.shared = Some(out);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn render_memories(&self, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.memories;
        let all: &[Memory] = match &p.list {
            Some(Ok(l)) => l,
            _ => &[],
        };
        let words: Vec<String> = p.q.split_whitespace().map(str::to_string).collect();
        let items: Vec<&Memory> = all.iter().filter(|m| shows(p, &words, m)).collect();
        let open = p.open.as_ref().and_then(|id| all.iter().find(|m| &m.id == id));
        let own_shown: Vec<String> = items.iter().filter(|m| !from_repo(m)).map(|m| m.id.clone()).collect();
        let all_ticked = !own_shown.is_empty() && own_shown.iter().all(|id| p.selected.contains(id));
        let from_repo_n = all.iter().filter(|m| from_repo(m)).count();

        let list = v_flex()
            .id("memories-list")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .px_2()
            .pt_2()
            .pb_2()
            .gap_1()
            .map(|el| match &p.list {
                None => el.child(super::panel::loading("memories-page")),
                Some(Err(e)) => el.child(div().p_3().child(controls::error_line(e.clone()))),
                Some(Ok(l)) if l.is_empty() => el.child(self.memories_empty(t("native.memories.empty.title"), t("native.memories.empty.body"))),
                Some(Ok(_)) if items.is_empty() => el.child(self.memories_empty(t("native.memories.noMatch"), String::new())),
                Some(Ok(_)) => el.children(items.iter().map(|m| self.memory_list_row(m, cx))),
            });

        let left = super::foot::jobs_card(
            Some("brain"),
            t("native.memories.title"),
            Some(tv(
                "native.memories.count",
                &[("count", all.len().into()), ("shared", from_repo_n.into())],
            )),
            None,
        )
        .flex_grow(1.)
        .flex_basis(px(0.))
        .child(self.memories_filters(cx))
        .child(self.memories_share_bar(&own_shown, all_ticked, cx))
        .child(list);

        let right = super::foot::jobs_card(
            open.map(|_| "file-text"),
            open.map(|m| m.title.clone()).unwrap_or_else(|| t("native.memories.pick")),
            None,
            None,
        )
        .flex_basis(px(0.))
        .flex_grow(1.1)
        .child(
            div()
                .id("memory-open")
                .flex_1()
                .min_h_0()
                .overflow_y_scroll()
                .px_5()
                .pt_2()
                .pb_5()
                .children(open.map(|m| self.memory_detail(m))),
        );

        v_flex()
            .size_full()
            .overflow_hidden()
            .p_6()
            .gap_4()
            .children(p.shared.as_ref().map(|s| self.share_result(s, cx)))
            .child(
                h_flex()
                    .flex_1()
                    .min_h_0()
                    .gap_4()
                    .items_stretch()
                    .child(left)
                    .child(right),
            )
            .into_any_element()
    }

    fn memories_empty(&self, title: String, body: String) -> AnyElement {
        v_flex()
            .py_10()
            .px_4()
            .gap_1()
            .items_center()
            .text_center()
            .child(div().text_color(theme::primary()).child(title))
            .when(!body.is_empty(), |el| {
                el.child(div().max_w(px(380.)).text_sm().text_color(theme::muted()).child(body))
            })
            .into_any_element()
    }

    fn memories_filters(&self, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.memories;
        let view = cx.entity().downgrade();
        let pick = move |set: fn(&mut MemoriesPage, String)| -> controls::Pick<str> {
            let view = view.clone();
            Rc::new(move |v: &str, _, cx| {
                let v = v.to_string();
                view.update(cx, |this, cx| {
                    set(&mut this.memories, v);
                    cx.notify();
                })
                .ok();
            })
        };
        let mut kinds = vec![Choice::new("", t("native.memories.kind.all"))];
        kinds.extend(KINDS.iter().map(|k| Choice::new(*k, kind_label(k))));
        let mut importance = vec![Choice::new("", t("native.memories.importance.any"))];
        importance.extend((2..=5i64).map(|n| {
            Choice::new(n.to_string(), tv("native.memories.importance.least", &[("n", n.into())]))
        }));
        h_flex()
            .flex_none()
            .flex_wrap()
            .px_4()
            .pt_2()
            .gap_2()
            .items_center()
            .child(
                div()
                    .flex_1()
                    .min_w(px(160.))
                    .children(p.search.as_ref().map(|s| Input::new(s).cleanable(true))),
            )
            .child(controls::chip_select(
                "memories-kind",
                "shapes",
                kinds,
                &p.kind,
                pick(|p, v| p.kind = v),
                Anchor::TopLeft,
            ))
            .child(controls::chip_select(
                "memories-importance",
                "signal",
                importance,
                &p.importance,
                pick(|p, v| p.importance = v),
                Anchor::TopLeft,
            ))
            .child(controls::segmented(
                "memories-source",
                vec![
                    ("".into(), t("native.memories.source.all")),
                    ("agentbox".into(), t("native.memories.source.agentbox")),
                    ("repository".into(), t("native.memories.source.repository")),
                ],
                Some(p.source.as_str()),
                false,
                pick(|p, v| p.source = v),
            ))
            .into_any_element()
    }

    fn memories_share_bar(&self, own_shown: &[String], all_ticked: bool, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.memories;
        let n = p.selected.len();
        let shown = own_shown.to_vec();
        h_flex()
            .flex_none()
            .mx_4()
            .mt_3()
            .mb_1()
            .px_3()
            .py_2()
            .gap_3()
            .items_center()
            .rounded(theme::radius::LG)
            .bg(theme::surface())
            .border_1()
            .border_color(theme::line())
            .child(tick(
                "memories-tick-all",
                all_ticked,
                cx.listener(move |this, _: &ClickEvent, _, cx| {
                    let sel = &mut this.memories.selected;
                    if shown.iter().all(|id| sel.contains(id)) {
                        for id in &shown {
                            sel.remove(id);
                        }
                    } else {
                        sel.extend(shown.iter().cloned());
                    }
                    cx.notify();
                }),
            ))
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .truncate()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::muted())
                    .child(if n == 0 {
                        t("native.memories.share.hint")
                    } else {
                        tv("native.memories.share.selected", &[("count", n.into())])
                    }),
            )
            .child(controls::button_busy(
                "memories-share-selected",
                tv("native.memories.share.selectedButton", &[("count", n.into())]),
                Btn::Primary,
                p.sharing,
                n == 0,
                cx.listener(|this, _: &ClickEvent, _, cx| this.share_memories(false, cx)),
            ))
            .child(controls::button(
                "memories-share-all",
                t("native.memories.share.all"),
                Btn::Secondary,
                p.sharing,
                cx.listener(|this, _: &ClickEvent, _, cx| this.share_memories(true, cx)),
            ))
            .into_any_element()
    }

    fn memory_list_row(&self, m: &Memory, cx: &mut Context<Self>) -> AnyElement {
        let p = &self.memories;
        let on = p.open.as_deref() == Some(m.id.as_str());
        let ticked = p.selected.contains(&m.id);
        let repo = from_repo(m);
        let (open_id, tick_id) = (m.id.clone(), m.id.clone());
        h_flex()
            .id(SharedString::from(format!("memory-row-{}", m.id)))
            .px_3()
            .py_2()
            .gap_3()
            .items_center()
            .rounded(theme::radius::XL)
            .text_size(theme::text::UI)
            .cursor_pointer()
            .when(on, |el| el.bg(theme::surface_raised()))
            .hover(|el| el.bg(theme::surface()))
            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                this.memories.open = Some(open_id.clone());
                cx.notify();
            }))
            .map(|el| {
                if repo {
                    // Already in the repository: nothing to share.
                    el.child(
                        div()
                            .flex_none()
                            .size(px(16.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .text_color(theme::faint())
                            .child(crate::icons::ic("git-branch").size(px(13.))),
                    )
                } else {
                    el.child(tick(
                        SharedString::from(format!("memory-tick-{}", m.id)),
                        ticked,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            cx.stop_propagation();
                            let sel = &mut this.memories.selected;
                            if !sel.remove(&tick_id) {
                                sel.insert(tick_id.clone());
                            }
                            cx.notify();
                        }),
                    ))
                }
            })
            .child(super::settings::kit::badge(kind_label(&m.kind), kind_color(&m.kind)))
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .truncate()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(if on { theme::title() } else { theme::secondary() })
                    .child(m.title.clone()),
            )
            .when(repo, |el| el.child(repo_badge()))
            .child(importance_dots(m.importance))
            .child(
                div()
                    .flex_none()
                    .w(px(64.))
                    .text_right()
                    .whitespace_nowrap()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(time_ago(&m.updated_at)),
            )
            .into_any_element()
    }

    fn memory_detail(&self, m: &Memory) -> AnyElement {
        let mut facts = vec![tv("native.memories.created", &[("when", time_ago(&m.created_at).into())])];
        if m.updated_at != m.created_at && !m.updated_at.is_empty() {
            let when = time_ago(&m.updated_at);
            facts.push(tv("memory.updatedAgo", &[("when", (&when).into())]));
        }
        facts.push(tv("native.memories.importance.of", &[("n", m.importance.into())]));
        v_flex()
            .gap_3()
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2()
                    .items_center()
                    .child(super::settings::kit::badge(kind_label(&m.kind), kind_color(&m.kind)))
                    .when(from_repo(m), |el| el.child(repo_badge()))
                    .child(importance_dots(m.importance))
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child(facts.join(" · ")),
                    ),
            )
            .child(
                div()
                    .text_size(px(13.5))
                    .text_color(theme::primary())
                    .child(if m.content.trim().is_empty() {
                        div()
                            .text_color(theme::subtle())
                            .child(t("native.memories.noContent"))
                            .into_any_element()
                    } else {
                        TextView::markdown(SharedString::from(format!("memory-body-{}", m.id)), m.content.clone())
                            .selectable(true)
                            .into_any_element()
                    }),
            )
            .child(
                v_flex()
                    .pt_3()
                    .gap_1()
                    .border_t_1()
                    .border_color(theme::line())
                    .text_size(theme::text::CAPTION)
                    .font_family(theme::MONO)
                    .text_color(theme::faint())
                    .child(m.id.clone())
                    .when(!m.source_path.is_empty(), |el| el.child(m.source_path.clone()))
                    .when(!m.supersedes_id.is_empty(), |el| {
                        el.child(tv("native.memories.supersedes", &[("id", (&m.supersedes_id).into())]))
                    }),
            )
            .into_any_element()
    }

    fn share_result(&self, s: &ShareMemoriesResult, cx: &mut Context<Self>) -> AnyElement {
        let title = if s.unchanged {
            tv("native.memories.shared.unchanged", &[("branch", (&s.branch).into())])
        } else {
            tv(
                "native.memories.shared.title",
                &[("count", s.files.len().into()), ("branch", (&s.branch).into())],
            )
        };
        h_flex()
            .id("memories-shared")
            .flex_none()
            .px_4()
            .py_3()
            .gap_3()
            .items_center()
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::emerald(400).opacity(0.3))
            .bg(theme::emerald(400).opacity(0.08))
            .child(
                crate::icons::ic("git-commit-horizontal")
                    .size(px(18.))
                    .text_color(theme::emerald(300)),
            )
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_1()
                    .child(
                        div()
                            .text_size(theme::text::UI)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::title())
                            .child(title),
                    )
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::muted())
                            .child(tv("native.memories.shared.body", &[("base", (&s.base).into())])),
                    )
                    .child(
                        div()
                            .mt_1()
                            .text_size(theme::text::CAPTION)
                            .font_family(theme::MONO)
                            .text_color(theme::secondary())
                            .child(s.push.clone()),
                    ),
            )
            .child(controls::copy_button(
                "memories-push-copy",
                s.push.clone(),
                t("common.copy"),
            ))
            .child(controls::button(
                "memories-shared-close",
                t("common.close"),
                Btn::Ghost,
                false,
                cx.listener(|this, _: &ClickEvent, _, cx| {
                    this.memories.shared = None;
                    cx.notify();
                }),
            ))
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn mem(id: &str, kind: &str, importance: i64, source: &str, title: &str) -> Memory {
        Memory {
            id: id.into(),
            kind: kind.into(),
            importance,
            source: source.into(),
            title: title.into(),
            ..Default::default()
        }
    }

    #[test]
    fn filters_by_words_kind_importance_and_source() {
        let ours = mem("a", "decision", 4, "", "Deploy on Fridays: never");
        let theirs = mem("b", "issue", 2, MEMORY_SOURCE_REPOSITORY, "Flaky e2e");
        let mut p = MemoriesPage::default();
        let words = |s: &str| s.split_whitespace().map(str::to_string).collect::<Vec<_>>();
        assert!(shows(&p, &words("fridays deploy"), &ours));
        assert!(!shows(&p, &words("fridays e2e"), &ours));
        p.kind = "issue".into();
        assert!(!shows(&p, &[], &ours) && shows(&p, &[], &theirs));
        p.kind.clear();
        p.importance = "3".into();
        assert!(shows(&p, &[], &ours) && !shows(&p, &[], &theirs));
        p.importance.clear();
        p.source = "repository".into();
        assert!(!shows(&p, &[], &ours) && shows(&p, &[], &theirs));
        p.source = "agentbox".into();
        assert!(shows(&p, &[], &ours) && !shows(&p, &[], &theirs));
    }
}
