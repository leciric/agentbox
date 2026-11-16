//! The project's todo list (internal/daemon/todos.go), a tab of the lead's
//! panel beside Schedules, and reached from the project's row in the sidebar:
//! add one, check it off, drag it to its place, delete it, or hand it to a
//! new agent as its task. The lead keeps the same list through its todos tool;
//! the daemon's "todo" event brings what it did here.
use std::collections::HashMap;

use agentbox_api::client::Method;
use agentbox_api::{Todo, TodoCounts, TodoStart};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState};
use gpui_component::tooltip::Tooltip;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn};
use super::pulls::RailTab;
use crate::theme;
use crate::toasts::{self, Kind, Toast};

pub struct Todos {
    project: String,
    list: Option<Result<Vec<Todo>, String>>,
    /// Open todos of every project, for the sidebar.
    pub(super) counts: HashMap<String, usize>,
    add: Entity<InputState>,
    /// The todo whose text is being edited, and its box.
    editing: Option<(String, Entity<InputState>)>,
    /// Whether the done ones are listed.
    show_done: bool,
    /// The todo whose agent is being asked for.
    starting: Option<String>,
    _subs: Vec<Subscription>,
}

impl Todos {
    pub fn new(window: &mut Window, cx: &mut Context<AppView>) -> Self {
        let add = cx.new(|cx| InputState::new(window, cx).placeholder(t("native.todos.add")));
        let sub = cx.subscribe_in(&add, window, |this, _, ev: &InputEvent, window, cx| {
            if let InputEvent::PressEnter { .. } = ev {
                this.add_todo(window, cx);
            }
        });
        Self {
            project: String::new(),
            list: None,
            counts: HashMap::new(),
            add,
            editing: None,
            show_done: true,
            starting: None,
            _subs: vec![sub],
        }
    }
}

/// What is dragged when a todo is picked up by its handle.
#[derive(Clone)]
struct DragTodo {
    id: String,
    text: SharedString,
}

struct TodoPreview(SharedString);

impl Render for TodoPreview {
    fn render(&mut self, _: &mut Window, _: &mut Context<Self>) -> impl IntoElement {
        div()
            .max_w(px(280.))
            .px_3()
            .py_1p5()
            .rounded_lg()
            .bg(theme::surface_raised())
            .border_1()
            .border_color(theme::brand(400))
            .text_size(theme::text::UI)
            .text_color(theme::primary())
            .truncate()
            .child(self.0.clone())
    }
}

/// moved is ids with `id` taken out and put just before `before` (at the
/// end when `before` is None).
fn moved(ids: &[String], id: &str, before: Option<&str>) -> Vec<String> {
    if before == Some(id) {
        return ids.to_vec();
    }
    let mut out: Vec<String> = ids.iter().filter(|x| *x != id).cloned().collect();
    let at = before
        .and_then(|b| out.iter().position(|x| x == b))
        .unwrap_or(out.len());
    out.insert(at, id.to_string());
    out
}

impl AppView {
    /// open_todos shows a project's todos: its lead, with the panel on Todos.
    pub(super) fn open_todos(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.select_project(project, window, cx);
        self.pulls.tab = RailTab::Todos;
        self.panel_sync(window, cx);
        cx.notify();
    }

    pub(super) fn load_todos(&mut self, cx: &mut Context<Self>) {
        let Some(project) = self.project.clone() else {
            return;
        };
        if self.todos.project != project {
            self.todos.project = project.clone();
            self.todos.list = None;
            self.todos.editing = None;
        }
        let client = self.client.clone();
        let enc = crate::files::encode(&project);
        cx.spawn(async move |this, cx| {
            let list = client
                .get::<Vec<Todo>>(&format!("/v1/projects/{enc}/todos"))
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.todos.project == project {
                    if let Ok(l) = &list {
                        let open = l.iter().filter(|t| !t.done).count();
                        this.todos.counts.insert(project.clone(), open);
                    }
                    this.todos.list = Some(list);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// load_todo_counts is how many todos each project has open, which the
    /// sidebar shows on its row.
    pub(super) fn load_todo_counts(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let Ok(c) = client.get::<TodoCounts>("/v1/todos/counts").await else {
                return;
            };
            this.update(cx, |this, cx| {
                let counts: HashMap<String, usize> = c
                    .open
                    .into_iter()
                    .map(|(p, n)| (p, n.max(0) as usize))
                    .collect();
                if counts != this.todos.counts {
                    this.todos.counts = counts;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// on_todo_event is the daemon saying a project's list changed: by the
    /// lead, another window, or a started todo's agent being made.
    pub(super) fn on_todo_event(&mut self, project: &str, cx: &mut Context<Self>) {
        self.load_todo_counts(cx);
        if self.todos.project == project && self.todos.list.is_some() {
            self.load_todos(cx);
        }
    }

    fn todo_call(
        &mut self,
        method: Method,
        path: String,
        body: Option<serde_json::Value>,
        done: impl FnOnce(&mut AppView, Result<Vec<u8>, String>, &mut Context<AppView>) + 'static,
        cx: &mut Context<Self>,
    ) {
        let client = self.client.clone();
        let base = format!(
            "/v1/projects/{}/todos",
            crate::files::encode(&self.todos.project)
        );
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    method,
                    &format!("{base}{path}"),
                    body.map(|b| serde_json::to_vec(&b).unwrap_or_default()),
                )
                .await
                .map(|b| b.to_vec())
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                done(this, res, cx);
                this.load_todos(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn report(res: Result<Vec<u8>, String>, cx: &mut Context<AppView>) {
        if let Err(e) = res {
            toasts::error(cx, e);
        }
    }

    fn add_todo(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let text = self.todos.add.read(cx).value().trim().to_string();
        if text.is_empty() {
            return;
        }
        self.todos
            .add
            .update(cx, |s, cx| s.set_value("", window, cx));
        // Shown at once; the daemon's answer replaces it.
        if let Some(Ok(list)) = self.todos.list.as_mut() {
            list.push(Todo {
                id: format!("pending-{}", list.len()),
                project: self.todos.project.clone(),
                text: text.clone(),
                position: list.len() as i64 + 1,
                created_by: "user".into(),
                ..Default::default()
            });
        }
        self.todo_call(
            Method::POST,
            String::new(),
            Some(serde_json::json!({ "text": text })),
            |_, res, cx| Self::report(res, cx),
            cx,
        );
        cx.notify();
    }

    fn check_todo(&mut self, id: String, done: bool, cx: &mut Context<Self>) {
        if let Some(Ok(list)) = self.todos.list.as_mut()
            && let Some(t) = list.iter_mut().find(|t| t.id == id)
        {
            t.done = done;
        }
        self.todo_call(
            Method::PATCH,
            format!("/{}", crate::files::encode(&id)),
            Some(serde_json::json!({ "done": done })),
            |_, res, cx| Self::report(res, cx),
            cx,
        );
        cx.notify();
    }

    fn delete_todo(&mut self, id: String, cx: &mut Context<Self>) {
        if let Some(Ok(list)) = self.todos.list.as_mut() {
            list.retain(|t| t.id != id);
        }
        self.todo_call(
            Method::DELETE,
            format!("/{}", crate::files::encode(&id)),
            None,
            |_, res, cx| Self::report(res, cx),
            cx,
        );
        cx.notify();
    }

    /// drop_todo puts the dragged todo before the one it was dropped on, or
    /// last.
    fn drop_todo(&mut self, id: &str, before: Option<&str>, cx: &mut Context<Self>) {
        if before == Some(id) {
            return;
        }
        let Some(Ok(list)) = self.todos.list.as_mut() else {
            return;
        };
        if list.iter().any(|t| t.id.starts_with("pending-")) {
            return;
        }
        let ids: Vec<String> = list.iter().map(|t| t.id.clone()).collect();
        let order = moved(&ids, id, before);
        if order == ids {
            return;
        }
        list.sort_by_key(|t| order.iter().position(|x| *x == t.id));
        self.todo_call(
            Method::PUT,
            "/order".into(),
            Some(serde_json::json!({ "ids": order })),
            |_, res, cx| Self::report(res, cx),
            cx,
        );
        cx.notify();
    }

    fn start_todo(&mut self, id: String, cx: &mut Context<Self>) {
        self.todos.starting = Some(id.clone());
        self.todo_call(
            Method::POST,
            format!("/{}/start", crate::files::encode(&id)),
            Some(serde_json::json!({})),
            |this, res, cx| {
                this.todos.starting = None;
                match res.and_then(|b| {
                    serde_json::from_slice::<TodoStart>(&b).map_err(|e| e.to_string())
                }) {
                    Ok(_) => Toast::new(Kind::Success, t("native.todos.started")).show(cx),
                    Err(e) => toasts::error(cx, e),
                }
            },
            cx,
        );
        cx.notify();
    }

    fn edit_todo(&mut self, t: &Todo, window: &mut Window, cx: &mut Context<Self>) {
        let id = t.id.clone();
        let state = cx.new(|cx| InputState::new(window, cx).default_value(t.text.clone()));
        let sub = cx.subscribe_in(
            &state,
            window,
            move |this, s, ev: &InputEvent, _, cx| match ev {
                InputEvent::PressEnter { .. } => {
                    let text = s.read(cx).value().trim().to_string();
                    this.save_todo_text(id.clone(), text, cx);
                }
                InputEvent::Blur if this.todos.editing.as_ref().is_some_and(|(e, _)| *e == id) => {
                    let text = s.read(cx).value().trim().to_string();
                    this.save_todo_text(id.clone(), text, cx);
                }
                _ => {}
            },
        );
        state.update(cx, |s, cx| s.focus(window, cx));
        self.todos._subs.truncate(1);
        self.todos._subs.push(sub);
        self.todos.editing = Some((t.id.clone(), state));
        cx.notify();
    }

    fn save_todo_text(&mut self, id: String, text: String, cx: &mut Context<Self>) {
        self.todos.editing = None;
        let same = matches!(&self.todos.list, Some(Ok(l)) if l.iter().any(|t| t.id == id && t.text == text));
        if text.is_empty() || same {
            cx.notify();
            return;
        }
        if let Some(Ok(list)) = self.todos.list.as_mut()
            && let Some(t) = list.iter_mut().find(|t| t.id == id)
        {
            t.text = text.clone();
        }
        self.todo_call(
            Method::PATCH,
            format!("/{}", crate::files::encode(&id)),
            Some(serde_json::json!({ "text": text })),
            |_, res, cx| Self::report(res, cx),
            cx,
        );
        cx.notify();
    }

    fn cancel_todo_edit(&mut self, cx: &mut Context<Self>) {
        self.todos.editing = None;
        cx.notify();
    }

    pub(super) fn render_todos(&self, cx: &mut Context<Self>) -> AnyElement {
        let project = self
            .projects
            .iter()
            .find(|p| Some(&p.name) == self.project.as_ref())
            .map(|p| {
                if p.display_name.is_empty() {
                    p.name.clone()
                } else {
                    p.display_name.clone()
                }
            })
            .unwrap_or_default();
        let s = &self.todos;
        let (open, done): (Vec<&Todo>, Vec<&Todo>) = match &s.list {
            Some(Ok(l)) => l.iter().partition(|t| !t.done),
            _ => (Vec::new(), Vec::new()),
        };
        let title = h_flex()
            .flex_none()
            .h(px(32.))
            .px_3()
            .gap_1p5()
            .border_b_1()
            .border_color(theme::line_faint())
            .text_xs()
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::muted())
            .child(crate::icons::ic("list-todo").size(px(13.)))
            .child(t("native.todos.title"))
            .child(
                div()
                    .truncate()
                    .font_weight(FontWeight::NORMAL)
                    .text_color(theme::faint())
                    .child(format!("· {project}")),
            )
            .when(!open.is_empty(), |el| {
                el.child(
                    div()
                        .ml_auto()
                        .flex_none()
                        .font_weight(FontWeight::NORMAL)
                        .text_color(theme::subtle())
                        .child(tv("native.todos.left", &[("count", open.len().into())])),
                )
            });
        let add = h_flex()
            .id("todo-add")
            .gap_2()
            .px_3()
            .h(px(36.))
            .rounded(theme::radius::XL)
            .border_1()
            .border_color(theme::line())
            .bg(theme::surface())
            .child(
                crate::icons::ic("plus")
                    .size(px(14.))
                    .text_color(theme::subtle()),
            )
            .child(
                div().flex_1().min_w_0().child(
                    Input::new(&s.add)
                        .appearance(false)
                        .px_0()
                        .py_0()
                        .h(px(24.))
                        .text_size(theme::text::UI),
                ),
            )
            .child(controls::button(
                "todo-add-button",
                t("native.todos.addButton"),
                Btn::Secondary,
                false,
                cx.listener(|this, _: &ClickEvent, w, cx| this.add_todo(w, cx)),
            ));
        let mut body = v_flex().gap_3().p_3().child(add);
        match &s.list {
            None => body = body.child(super::panel::loading("todos")),
            Some(Err(e)) => body = body.child(controls::error_line(e.clone())),
            Some(Ok(l)) if l.is_empty() => {
                body = body.child(
                    v_flex()
                        .gap_2()
                        .px_4()
                        .py_5()
                        .rounded(theme::radius::XL)
                        .border_1()
                        .border_dashed()
                        .border_color(theme::line())
                        .child(
                            h_flex()
                                .gap_2()
                                .text_size(theme::text::BODY)
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::primary())
                                .child(
                                    crate::icons::ic("list-todo")
                                        .size_4()
                                        .text_color(theme::brand(300)),
                                )
                                .child(t("native.todos.empty.title")),
                        )
                        .child(
                            div()
                                .text_size(theme::text::UI)
                                .line_height(relative(1.5))
                                .text_color(theme::subtle())
                                .child(t("native.todos.empty.body")),
                        ),
                );
            }
            Some(Ok(_)) => {
                body = body.child(
                    v_flex()
                        .id("todos-open")
                        .min_w_0()
                        .gap_1()
                        .children(open.iter().enumerate().map(|(i, td)| {
                            crate::motion::enter(
                                div().min_w_0().child(self.todo_row(td, cx)),
                                SharedString::from(format!("todo-enter-{}", td.id)),
                                i,
                            )
                        }))
                        // Dropped below the last: last.
                        .child(
                            div()
                                .id("todo-drop-end")
                                .h(px(10.))
                                .rounded_full()
                                .drag_over::<DragTodo>(|st, _, _, _| {
                                    st.bg(theme::brand(400).opacity(0.35))
                                })
                                .on_drop(cx.listener(|this, d: &DragTodo, _, cx| {
                                    let first_done = match &this.todos.list {
                                        Some(Ok(l)) => {
                                            l.iter().find(|t| t.done).map(|t| t.id.clone())
                                        }
                                        _ => None,
                                    };
                                    this.drop_todo(&d.id, first_done.as_deref(), cx)
                                })),
                        ),
                );
                if !done.is_empty() {
                    let show = s.show_done;
                    body = body.child(
                        v_flex()
                            .gap_1()
                            .child(
                                h_flex()
                                    .id("todos-done-toggle")
                                    .gap_1()
                                    .cursor_pointer()
                                    .text_size(theme::text::CAPTION)
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::muted())
                                    .hover(|el| el.text_color(theme::primary()))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.todos.show_done = !show;
                                        cx.notify();
                                    }))
                                    .child(
                                        crate::icons::ic(if show {
                                            "chevron-down"
                                        } else {
                                            "chevron-right"
                                        })
                                        .size(px(12.)),
                                    )
                                    .child(tv(
                                        "native.todos.done",
                                        &[("count", done.len().into())],
                                    )),
                            )
                            .when(show, |el| {
                                el.children(done.iter().map(|td| self.todo_row(td, cx)))
                            }),
                    );
                }
            }
        }
        v_flex()
            .size_full()
            .child(title)
            .child(
                div()
                    .id("todos-scroll")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .child(body),
            )
            .into_any_element()
    }

    fn todo_row(&self, td: &Todo, cx: &mut Context<Self>) -> AnyElement {
        let id = td.id.clone();
        let pending = id.starts_with("pending-");
        let starting = self.todos.starting.as_deref() == Some(id.as_str());
        let making = !td.job.is_empty() && td.agent.is_empty();
        let group = SharedString::from(format!("todo-{id}"));
        let editing = self
            .todos
            .editing
            .as_ref()
            .filter(|(e, _)| *e == id)
            .map(|(_, s)| s.clone());
        let check = {
            let id = id.clone();
            let done = td.done;
            div()
                .id(SharedString::from(format!("todo-check-{id}")))
                .flex_none()
                .mt(px(2.))
                .size(px(16.))
                .rounded(px(5.))
                .border_1()
                .flex()
                .items_center()
                .justify_center()
                .cursor_pointer()
                .when(done, |el| {
                    el.bg(theme::brand(500))
                        .border_color(theme::brand(500))
                        .text_color(theme::on_brand())
                        .child(crate::icons::ic("check").size(px(11.)))
                })
                .when(!done, |el| {
                    el.border_color(theme::line_heavy())
                        .hover(|el| el.border_color(theme::brand(400)))
                })
                .when(!pending, |el| {
                    el.on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        cx.stop_propagation();
                        this.check_todo(id.clone(), !done, cx)
                    }))
                })
        };
        let text: AnyElement = match editing {
            Some(state) => div()
                .flex_1()
                .min_w_0()
                .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                    if ev.keystroke.key == "escape" {
                        this.cancel_todo_edit(cx);
                    }
                }))
                .child(
                    Input::new(&state)
                        .appearance(false)
                        .px_0()
                        .py_0()
                        .h(px(20.))
                        .text_size(theme::text::UI),
                )
                .into_any_element(),
            None => {
                let edit = td.clone();
                div()
                    .id(SharedString::from(format!("todo-text-{id}")))
                    .flex_1()
                    .min_w_0()
                    .text_size(theme::text::UI)
                    .line_height(relative(1.45))
                    .when(td.done, |el| el.line_through().text_color(theme::faint()))
                    .when(!td.done, |el| el.text_color(theme::primary()))
                    .when(!pending && !td.done, |el| {
                        el.cursor_text().on_click(cx.listener(
                            move |this, _: &ClickEvent, window, cx| {
                                this.edit_todo(&edit, window, cx)
                            },
                        ))
                    })
                    .child(td.text.clone())
                    .into_any_element()
            }
        };
        // Who has it: the agent it went to, or the agent being made for it.
        let owner: Option<AnyElement> = if !td.agent.is_empty() {
            let r#ref = format!("{}/{}", td.project, td.agent);
            Some(
                h_flex()
                    .id(SharedString::from(format!("todo-agent-{id}")))
                    .gap_1()
                    .px_1p5()
                    .rounded_full()
                    .bg(theme::brand(400).opacity(0.12))
                    .text_color(theme::brand(300))
                    .cursor_pointer()
                    .hover(|el| el.bg(theme::brand(400).opacity(0.2)))
                    .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.open_agent_ref(r#ref.clone(), window, cx)
                    }))
                    .child(crate::icons::ic("bot").size(px(11.)))
                    .child(td.agent.clone())
                    .into_any_element(),
            )
        } else if making || starting {
            Some(
                h_flex()
                    .gap_1()
                    .text_color(theme::subtle())
                    .child(crate::motion::spin(
                        crate::icons::ic("loader-circle").size(px(11.)),
                        SharedString::from(format!("todo-spin-{id}")),
                    ))
                    .child(t("native.todos.starting"))
                    .into_any_element(),
            )
        } else {
            None
        };
        let meta = owner.is_some() || td.created_by == "lead";
        let actions = {
            let (sid, did) = (id.clone(), id.clone());
            let can_start = !td.done && td.agent.is_empty() && !making && !starting && !pending;
            let start_tip: SharedString = t("native.todos.startHint").into();
            let delete_tip: SharedString = t("native.todos.delete").into();
            h_flex()
                .flex_none()
                .gap_0p5()
                .invisible()
                .group_hover(group.clone(), |el| el.visible())
                .when(can_start, |el| {
                    el.child(
                        h_flex()
                            .id(SharedString::from(format!("todo-start-{id}")))
                            .h(px(22.))
                            .px_1p5()
                            .gap_1()
                            .rounded_md()
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::brand(300))
                            .cursor_pointer()
                            .hover(|el| el.bg(theme::brand(400).opacity(0.14)))
                            .tooltip(move |w, cx| Tooltip::new(start_tip.clone()).build(w, cx))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                cx.stop_propagation();
                                this.start_todo(sid.clone(), cx)
                            }))
                            .child(crate::icons::ic("bot").size(px(12.)))
                            .child(t("native.todos.start")),
                    )
                })
                .when(!pending, |el| {
                    el.child(
                        div()
                            .id(SharedString::from(format!("todo-delete-{id}")))
                            .size(px(22.))
                            .flex()
                            .items_center()
                            .justify_center()
                            .rounded_md()
                            .text_color(theme::subtle())
                            .cursor_pointer()
                            .hover(|el| {
                                el.bg(theme::rose(400).opacity(0.12))
                                    .text_color(theme::rose(300))
                            })
                            .tooltip(move |w, cx| Tooltip::new(delete_tip.clone()).build(w, cx))
                            .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                cx.stop_propagation();
                                this.delete_todo(did.clone(), cx)
                            }))
                            .child(crate::icons::ic("trash").size(px(13.))),
                    )
                })
        };
        let handle = div()
            .id(SharedString::from(format!("todo-grip-{id}")))
            .flex_none()
            .mt(px(2.))
            .w(px(12.))
            .text_color(theme::faint())
            .invisible()
            .group_hover(group.clone(), |el| el.visible())
            .when(!pending && !td.done, |el| {
                el.cursor_grab().on_drag(
                    DragTodo {
                        id: id.clone(),
                        text: td.text.clone().into(),
                    },
                    |d: &DragTodo, _, _, cx| {
                        let text = d.text.clone();
                        cx.new(|_| TodoPreview(text))
                    },
                )
            })
            .child(crate::icons::ic("grip-vertical").size(px(12.)));
        let target = id.clone();
        h_flex()
            .id(SharedString::from(format!("todo-{id}")))
            .group(group)
            .min_w_0()
            .items_start()
            .gap_2()
            .pl_1()
            .pr_2()
            .py_1p5()
            .rounded_lg()
            .border_t_2()
            .border_color(transparent_black())
            .hover(|el| el.bg(theme::surface()))
            .when(pending, |el| el.opacity(0.6))
            .when(!td.done, |el| {
                el.drag_over::<DragTodo>(|st, _, _, _| st.border_color(theme::brand(400)))
                    .on_drop(cx.listener(move |this, d: &DragTodo, _, cx| {
                        this.drop_todo(&d.id, Some(&target), cx)
                    }))
            })
            .child(handle)
            .child(check)
            .child(
                v_flex()
                    .flex_1()
                    .min_w_0()
                    .gap_0p5()
                    .child(text)
                    .when(meta, |el| {
                        el.child(
                            h_flex()
                                .flex_wrap()
                                .gap_x_2()
                                .text_size(theme::text::MICRO)
                                .text_color(theme::subtle())
                                .children(owner)
                                .when(td.created_by == "lead", |el| {
                                    el.child(t("native.todos.byLead"))
                                }),
                        )
                    }),
            )
            .child(actions)
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::moved;

    #[test]
    fn a_dragged_todo_lands_before_the_one_it_is_dropped_on() {
        let ids: Vec<String> = ["a", "b", "c", "d"].iter().map(|s| s.to_string()).collect();
        assert_eq!(moved(&ids, "d", Some("b")), ["a", "d", "b", "c"]);
        assert_eq!(moved(&ids, "a", Some("c")), ["b", "a", "c", "d"]);
        assert_eq!(moved(&ids, "b", None), ["a", "c", "d", "b"]);
        assert_eq!(moved(&ids, "c", Some("c")), ["a", "b", "c", "d"]);
    }
}
