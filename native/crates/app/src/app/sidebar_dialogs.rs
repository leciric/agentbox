//! The sidebar's dialogs, one at a time over the window: Add project
//! (AddProjectDialog.tsx: an existing folder or a new repository), a project's
//! icon (ProjectIcon.tsx), a section's name (new or renamed) and deleting one
//! (SidebarSections.tsx), Talk to another project (TalkToProject.tsx), Fan out
//! (groups/FanOutDialog.tsx), Start an agent yourself (NewAgentDialog.tsx,
//! its essentials) and the keyboard shortcuts (ShortcutSheet.tsx).
use std::path::PathBuf;
use std::rc::Rc;

use agentbox_api::{
    AuthStatus, FanOutRequest, FanOutResult, FanOutVariant, ProjectIcon, ProjectIconCandidate,
    SetProjectIconRequest, Settings, UpdateSectionRequest,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::{Input, InputEvent, InputState, Textarea, TextareaState};
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::{OnCancel, modal};
use super::controls::{self, Btn};
use super::sidebar::{data_image, shown_of};
use super::sidebar_actions::label_of;
use crate::chat::tool_label;
use crate::{theme, toasts};

/// The emoji Change icon suggests (ProjectIcon.tsx).
const SUGGESTED: [&str; 16] = [
    "🚀", "🧪", "📦", "🛠️", "🎨", "📱", "🌐", "🤖", "📚", "🔒", "⚡", "🐳", "🦀", "🐍", "🧩", "🗂️",
];

/// The largest picture an icon may be (SetProjectIconRequest).
const MAX_ICON: usize = 512 * 1024;

pub enum Dialog {
    AddProject(Box<super::add_project::AddProject>),
    Icon {
        project: String,
        emoji: Entity<InputState>,
        icon: Option<ProjectIcon>,
        candidates: Vec<(ProjectIconCandidate, Option<std::sync::Arc<Image>>)>,
        busy: bool,
        error: Option<String>,
    },
    SectionName {
        /// The section renamed; None makes a new one.
        section: Option<agentbox_api::Section>,
        /// A project to move into the new one.
        moving: Option<String>,
        name: Entity<InputState>,
    },
    DeleteSection {
        id: String,
        name: String,
        count: usize,
    },
    Talk {
        project: String,
    },
    FanOut {
        project: String,
        title: Entity<InputState>,
        task: Entity<TextareaState>,
        /// What each agent runs: "ai|model".
        options: Vec<(String, String)>,
        picks: Vec<String>,
        judge: bool,
        busy: bool,
        error: Option<String>,
    },
    NewAgent(Box<super::new_agent::NewAgent>),
    Keys,
    /// The project chat's New chat: its conversation cleared, confirmed.
    NewChat {
        project: String,
    },
}

impl AppView {
    /// clear_lead_chat starts the project chat anew (DELETE …/chat).
    pub(super) fn clear_lead_chat(&mut self, project: String, cx: &mut Context<Self>) {
        let (prefix, _, _) = self.lead_target(&project);
        let client = self.client.clone();
        cx.spawn(async move |_, cx| {
            if let Err(e) = client
                .raw(
                    agentbox_api::client::Method::DELETE,
                    &format!("{prefix}/chat"),
                    None,
                )
                .await
            {
                cx.update(|cx| crate::toasts::error(cx, e.to_string()));
            }
        })
        .detach();
    }

    fn close_dialog(&mut self, cx: &mut Context<Self>) {
        self.side.dialog = None;
        cx.notify();
    }

    fn dialog_input(
        &self,
        placeholder: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Entity<InputState> {
        cx.new(|cx| InputState::new(window, cx).placeholder(placeholder))
    }

    // ——— A project's icon ———

    pub(super) fn open_icon_dialog(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let emoji = self.dialog_input(t("projectIcon.emojiPlaceholder"), window, cx);
        self.side.dialog = Some(Dialog::Icon {
            project: project.clone(),
            emoji,
            icon: None,
            candidates: Vec::new(),
            busy: false,
            error: None,
        });
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let icon = client
                .get::<ProjectIcon>(&format!("/v1/projects/{project}/icon"))
                .await
                .ok();
            let found = client
                .get::<Vec<ProjectIconCandidate>>(&format!(
                    "/v1/projects/{project}/icon/candidates"
                ))
                .await
                .unwrap_or_default();
            this.update(cx, |this, cx| {
                if let Some(Dialog::Icon {
                    icon: i,
                    candidates,
                    ..
                }) = &mut this.side.dialog
                {
                    *i = icon;
                    *candidates = found
                        .into_iter()
                        .map(|c| {
                            let img = data_image(&c.image);
                            (c, img)
                        })
                        .collect();
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// set_icon chooses an icon (PUT), or with None goes back to the
    /// detected one or none (DELETE).
    fn set_icon(&mut self, req: Option<SetProjectIconRequest>, cx: &mut Context<Self>) {
        let Some(Dialog::Icon { project, busy, .. }) = &mut self.side.dialog else {
            return;
        };
        *busy = true;
        let project = project.clone();
        let client = self.client.clone();
        cx.notify();
        cx.spawn(async move |this, cx| {
            let path = format!("/v1/projects/{project}/icon");
            let res = match &req {
                Some(r) => {
                    client
                        .raw(
                            agentbox_api::client::Method::PUT,
                            &path,
                            serde_json::to_vec(r).ok(),
                        )
                        .await
                }
                None => {
                    client
                        .raw(agentbox_api::client::Method::DELETE, &path, None)
                        .await
                }
            }
            .and_then(|b| Ok(serde_json::from_slice::<ProjectIcon>(&b)?));
            this.update(cx, |this, cx| {
                match res {
                    Ok(icon) => {
                        let version = this.side.icons.get(&project).map_or(0, |(v, _)| *v);
                        this.side.icons.insert(project, (version, shown_of(&icon)));
                        this.side.dialog = None;
                    }
                    Err(e) => {
                        if let Some(Dialog::Icon { busy, error, .. }) = &mut this.side.dialog {
                            *busy = false;
                            *error = Some(format!("{}: {e}", t("projectIcon.failed")));
                        }
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn upload_icon(&mut self, cx: &mut Context<Self>) {
        let rx = pick(t("projectIcon.upload"), false, cx);
        cx.spawn(async move |this, cx| {
            let Some(path) = rx.await.ok().flatten().and_then(|p| p.into_iter().next()) else {
                return;
            };
            let bytes = std::fs::read(&path).unwrap_or_default();
            this.update(cx, |this, cx| {
                if bytes.len() > MAX_ICON {
                    if let Some(Dialog::Icon { error, .. }) = &mut this.side.dialog {
                        *error = Some(t("projectIcon.tooBig"));
                    }
                    cx.notify();
                    return;
                }
                use base64::Engine as _;
                let mime = match path.extension().and_then(|e| e.to_str()).unwrap_or("") {
                    "jpg" | "jpeg" => "image/jpeg",
                    "gif" => "image/gif",
                    "webp" => "image/webp",
                    "svg" => "image/svg+xml",
                    "ico" => "image/x-icon",
                    _ => "image/png",
                };
                let image = format!(
                    "data:{mime};base64,{}",
                    base64::engine::general_purpose::STANDARD.encode(&bytes)
                );
                this.set_icon(
                    Some(SetProjectIconRequest {
                        image,
                        ..Default::default()
                    }),
                    cx,
                );
            })
            .ok();
        })
        .detach();
    }

    // ——— Sections ———

    pub(super) fn open_section_name(
        &mut self,
        section: Option<agentbox_api::Section>,
        moving: Option<String>,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let name = self.dialog_input(t("shell.sidebar.newSectionName"), window, cx);
        if let Some(s) = &section {
            let v = s.name.clone();
            name.update(cx, |i, cx| i.set_value(v, window, cx));
        }
        name.update(cx, |s, cx| s.focus(window, cx));
        let sub = cx.subscribe(&name, |this, _, ev: &InputEvent, cx| {
            if matches!(ev, InputEvent::PressEnter { .. }) {
                this.save_section_name(cx);
            }
        });
        self._subs.push(sub);
        self.side.dialog = Some(Dialog::SectionName {
            section,
            moving,
            name,
        });
        cx.notify();
    }

    fn save_section_name(&mut self, cx: &mut Context<Self>) {
        let Some(Dialog::SectionName {
            section,
            moving,
            name,
        }) = self.side.dialog.take()
        else {
            return;
        };
        let name = name.read(cx).value().trim().to_string();
        if name.is_empty() {
            return;
        }
        match section {
            Some(s) => self.patch_section(
                &s.id,
                UpdateSectionRequest {
                    name: Some(name),
                    collapsed: None,
                },
                cx,
            ),
            None => self.add_section(name, moving, cx),
        }
        cx.notify();
    }

    // ——— Talk to another project ———

    /// talk writes, in this project's lead's composer, the start of a
    /// message to the other's lead: Ask or Align (TalkToProject.tsx).
    fn talk(
        &mut self,
        project: String,
        other: String,
        align: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.side.dialog = None;
        self.select_project(project, window, cx);
        let text = tv(
            if align {
                "v2.talk.alignText"
            } else {
                "v2.talk.askText"
            },
            &[("project", format!("@{other}").into())],
        );
        if let Some(chat) = &self.chat {
            chat.update(cx, |c, cx| {
                c.set_draft(&text, window, cx);
                c.focus_composer(window, cx);
            });
        }
    }

    // ——— Fan out ———

    pub(super) fn open_fan_out(
        &mut self,
        project: String,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        let title = self.dialog_input(t("project.newAgent.workPlaceholder"), window, cx);
        let task = cx.new(|cx| {
            TextareaState::new(window, cx)
                .auto_grow(4, 10)
                .placeholder(t("group.fanOut.taskPlaceholder"))
        });
        title.update(cx, |s, cx| s.focus(window, cx));
        self.side.dialog = Some(Dialog::FanOut {
            project,
            title,
            task,
            options: Vec::new(),
            picks: Vec::new(),
            judge: false,
            busy: false,
            error: None,
        });
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let settings = client.get::<Settings>("/v1/settings").await.ok();
            let auth = client.get::<AuthStatus>("/v1/auth").await.ok();
            let options = variants(
                settings.as_ref(),
                auth.as_ref(),
                &t("group.fanOut.defaultModel"),
            );
            this.update(cx, |this, cx| {
                if let Some(Dialog::FanOut {
                    options: o, picks, ..
                }) = &mut this.side.dialog
                {
                    *picks = default_picks(&options);
                    *o = options;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn start_fan_out(&mut self, cx: &mut Context<Self>) {
        let Some(Dialog::FanOut {
            project,
            title,
            task,
            picks,
            judge,
            busy,
            error,
            ..
        }) = &mut self.side.dialog
        else {
            return;
        };
        let req = FanOutRequest {
            project: project.clone(),
            title: title.read(cx).value().trim().to_string(),
            task: task.read(cx).value().trim().to_string(),
            variants: picks
                .iter()
                .map(|v| {
                    let (ai, model) = v.split_once('|').unwrap_or((v, ""));
                    FanOutVariant {
                        ai: ai.into(),
                        model: (!model.is_empty()).then(|| model.to_string()),
                        ..Default::default()
                    }
                })
                .collect(),
            judge: *judge,
            ..Default::default()
        };
        if req.task.is_empty() || req.variants.len() < 2 {
            return;
        }
        *busy = true;
        *error = None;
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.post::<_, FanOutResult>("/v1/groups", &req).await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(out) => {
                        this.side.dialog = None;
                        for n in out.notices {
                            toasts::info(cx, n);
                        }
                    }
                    Err(e) => {
                        if let Some(Dialog::FanOut { busy, error, .. }) = &mut this.side.dialog {
                            *busy = false;
                            *error = Some(e.to_string());
                        }
                    }
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    // ——— Drawing them ———

    pub(super) fn render_sidebar_dialog(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let d = self.side.dialog.as_ref()?;
        let view = cx.entity().downgrade();
        let cancel: OnCancel = Rc::new(move |_, cx| {
            view.update(cx, |this, cx| this.close_dialog(cx)).ok();
        });
        let field = |label: String, input: AnyElement, hint: Option<String>| -> AnyElement {
            v_flex()
                .gap_1()
                .child(
                    div()
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(label),
                )
                .child(input)
                .when_some(hint, |el, h| {
                    el.child(div().text_xs().text_color(theme::faint()).child(h))
                })
                .into_any_element()
        };
        let error = |e: &Option<String>| -> Vec<AnyElement> {
            e.iter()
                .map(|e| controls::note_in(e.clone(), theme::error()))
                .collect()
        };
        Some(match d {
            Dialog::AddProject(d) => self.render_add_project(d, cancel, cx),
            Dialog::Icon {
                project,
                emoji,
                icon,
                candidates,
                busy,
                error: err,
            } => {
                let label = self
                    .projects
                    .iter()
                    .find(|p| &p.name == project)
                    .map_or_else(|| project.clone(), label_of);
                let chosen = icon
                    .as_ref()
                    .filter(|i| i.source == "chosen")
                    .map(|i| i.emoji.clone());
                let busy = *busy;
                let mut body = vec![
                    field(
                        t("projectIcon.emoji"),
                        h_flex()
                            .flex_wrap()
                            .gap_1()
                            .children(SUGGESTED.iter().map(|e| {
                                let on = chosen.as_deref() == Some(*e);
                                let emoji = e.to_string();
                                div()
                                    .id(SharedString::from(format!("emoji-{e}")))
                                    .size_8()
                                    .flex()
                                    .items_center()
                                    .justify_center()
                                    .rounded_lg()
                                    .text_lg()
                                    .cursor_pointer()
                                    .when(on, |el| el.bg(theme::surface_strong()))
                                    .hover(|el| el.bg(theme::surface_raised()))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        if !busy {
                                            this.set_icon(
                                                Some(SetProjectIconRequest {
                                                    emoji: emoji.clone(),
                                                    ..Default::default()
                                                }),
                                                cx,
                                            )
                                        }
                                    }))
                                    .child(e.to_string())
                            }))
                            .into_any_element(),
                        None,
                    ),
                    h_flex()
                        .gap_2()
                        .child(div().flex_1().child(Input::new(emoji)))
                        .child(controls::button(
                            "icon-use-emoji",
                            t("projectIcon.useEmoji"),
                            Btn::Secondary,
                            busy || emoji.read(cx).value().trim().is_empty(),
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                let Some(Dialog::Icon { emoji, .. }) = &this.side.dialog else {
                                    return;
                                };
                                let e = emoji.read(cx).value().trim().to_string();
                                this.set_icon(
                                    Some(SetProjectIconRequest {
                                        emoji: e,
                                        ..Default::default()
                                    }),
                                    cx,
                                )
                            }),
                        ))
                        .into_any_element(),
                ];
                if !candidates.is_empty() {
                    body.push(field(
                        t("projectIcon.fromRepo"),
                        h_flex()
                            .flex_wrap()
                            .gap_2()
                            .children(candidates.iter().map(|(c, img)| {
                                let from = c.from.clone();
                                let tip: SharedString =
                                    tv("projectIcon.useFile", &[("from", (&c.from).into())]).into();
                                div()
                                    .id(SharedString::from(format!("cand-{}", c.from)))
                                    .size_10()
                                    .p_1()
                                    .rounded_lg()
                                    .border_1()
                                    .border_color(if c.chosen {
                                        theme::accent()
                                    } else {
                                        theme::line()
                                    })
                                    .cursor_pointer()
                                    .hover(|el| el.bg(theme::surface_raised()))
                                    .tooltip(move |w, cx| {
                                        gpui_component::tooltip::Tooltip::new(tip.clone())
                                            .build(w, cx)
                                    })
                                    .when_some(img.clone(), |el, i| el.child(img_of(i)))
                                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                                        this.set_icon(
                                            Some(SetProjectIconRequest {
                                                candidate: from.clone(),
                                                ..Default::default()
                                            }),
                                            cx,
                                        )
                                    }))
                            }))
                            .into_any_element(),
                        None,
                    ));
                }
                let mut actions = h_flex().gap_2().child(controls::button(
                    "icon-upload",
                    t("projectIcon.upload"),
                    Btn::Secondary,
                    busy,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.upload_icon(cx)),
                ));
                if let Some(i) = icon {
                    if i.source == "chosen" {
                        actions = actions.child(controls::button(
                            "icon-reset",
                            t(if i.has_detected {
                                "projectIcon.reset"
                            } else {
                                "projectIcon.remove"
                            }),
                            Btn::Ghost,
                            busy,
                            cx.listener(|this, _: &ClickEvent, _, cx| this.set_icon(None, cx)),
                        ));
                    } else if i.source == "detected" && !i.detected_from.is_empty() {
                        actions =
                            actions.child(div().text_xs().text_color(theme::faint()).child(tv(
                                "projectIcon.detectedFrom",
                                &[("from", (&i.detected_from).into())],
                            )));
                    }
                }
                body.push(actions.into_any_element());
                body.extend(error(err));
                modal(
                    "project-icon",
                    tv("projectIcon.title", &[("project", label.into())]),
                    Some(t("projectIcon.description")),
                    body,
                    cancel.clone(),
                    t("common.close"),
                    false,
                    false,
                    move |_, w, cx| cancel(w, cx),
                )
            }
            Dialog::SectionName { section, name, .. } => modal(
                "section-name",
                match section {
                    Some(s) => tv("shell.sidebar.renameTitle", &[("name", (&s.name).into())]),
                    None => t("shell.sidebar.newSection"),
                },
                None,
                vec![field(
                    t("shell.sidebar.sectionName"),
                    Input::new(name).into_any_element(),
                    None,
                )],
                cancel,
                t(if section.is_some() {
                    "common.rename"
                } else {
                    "common.add"
                }),
                false,
                name.read(cx).value().trim().is_empty(),
                cx.listener(|this, _: &ClickEvent, _, cx| this.save_section_name(cx)),
            ),
            Dialog::DeleteSection { id, name, count } => {
                let id = id.clone();
                modal(
                    "section-delete",
                    tv("shell.sidebar.deleteTitle", &[("name", name.into())]),
                    Some(tv(
                        "shell.sidebar.deleteBody",
                        &[("count", (*count).into())],
                    )),
                    Vec::new(),
                    cancel,
                    t("shell.sidebar.deleteSection"),
                    true,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.side.dialog = None;
                        this.delete_section(id.clone(), cx);
                    }),
                )
            }
            Dialog::Talk { project } => {
                let rows: Vec<AnyElement> = self
                    .projects
                    .iter()
                    .filter(|p| &p.name != project)
                    .map(|p| {
                        let (a, b) = (p.name.clone(), p.name.clone());
                        let (pa, pb) = (project.clone(), project.clone());
                        h_flex()
                            .gap_2()
                            .py_1()
                            .child(self.project_icon(p, 20.))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_sm()
                                    .child(label_of(p)),
                            )
                            .child(controls::button(
                                SharedString::from(format!("talk-ask-{}", p.name)),
                                t("v2.talk.ask"),
                                Btn::Secondary,
                                false,
                                cx.listener(move |this, _: &ClickEvent, w, cx| {
                                    this.talk(pa.clone(), a.clone(), false, w, cx)
                                }),
                            ))
                            .child(controls::button(
                                SharedString::from(format!("talk-align-{}", p.name)),
                                t("v2.talk.align"),
                                Btn::Secondary,
                                false,
                                cx.listener(move |this, _: &ClickEvent, w, cx| {
                                    this.talk(pb.clone(), b.clone(), true, w, cx)
                                }),
                            ))
                            .into_any_element()
                    })
                    .collect();
                modal(
                    "talk",
                    t("v2.talk.title"),
                    Some(t("v2.talk.hint")),
                    rows,
                    cancel.clone(),
                    t("common.close"),
                    false,
                    false,
                    move |_, w, cx| cancel(w, cx),
                )
            }
            Dialog::FanOut {
                project,
                title,
                task,
                options,
                picks,
                judge,
                busy,
                error: err,
            } => {
                let label = self
                    .projects
                    .iter()
                    .find(|p| &p.name == project)
                    .map_or_else(|| project.clone(), label_of);
                let choices: Vec<controls::Choice> = options
                    .iter()
                    .map(|(v, l)| controls::Choice::new(v.clone(), l.clone()))
                    .collect();
                let rows = picks.iter().enumerate().map(|(i, pick)| {
                    let letter = ((b'A' + i as u8) as char).to_string();
                    let view = cx.entity().downgrade();
                    h_flex()
                        .gap_2()
                        .child(
                            div()
                                .w_4()
                                .text_sm()
                                .text_color(theme::muted())
                                .child(letter.clone()),
                        )
                        .child(div().flex_1().child(controls::select_at(
                            &format!("fan-pick-{i}"),
                            choices.clone(),
                            pick,
                            false,
                            Rc::new(move |v: &str, _, cx| {
                                let v = v.to_string();
                                view.update(cx, |this, cx| {
                                    if let Some(Dialog::FanOut { picks, .. }) =
                                        &mut this.side.dialog
                                        && let Some(p) = picks.get_mut(i)
                                    {
                                        *p = v;
                                    }
                                    cx.notify();
                                })
                                .ok();
                            }),
                            Anchor::TopLeft,
                        )))
                        .when(picks.len() > 2, |el| {
                            el.child(controls::button(
                                SharedString::from(format!("fan-remove-{i}")),
                                "×",
                                Btn::Ghost,
                                false,
                                cx.listener(move |this, _: &ClickEvent, _, cx| {
                                    if let Some(Dialog::FanOut { picks, .. }) =
                                        &mut this.side.dialog
                                    {
                                        picks.remove(i);
                                    }
                                    cx.notify();
                                }),
                            ))
                        })
                        .into_any_element()
                });
                let n = picks.len();
                let mut agents = v_flex().gap_1p5().children(rows);
                if n < 6 {
                    agents = agents.child(h_flex().child(controls::button(
                        "fan-add",
                        format!("+ {}", t("group.fanOut.addAgent")),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| {
                            if let Some(Dialog::FanOut { picks, options, .. }) =
                                &mut this.side.dialog
                            {
                                let next = options
                                    .first()
                                    .map(|o| o.0.clone())
                                    .unwrap_or_else(|| "claude|".into());
                                picks.push(next);
                            }
                            cx.notify();
                        }),
                    )));
                }
                let judge_on = *judge;
                let mut body = vec![
                    field(
                        t("project.newAgent.projectLabel"),
                        div().text_sm().child(label).into_any_element(),
                        None,
                    ),
                    field(
                        t("group.fanOut.titleLabel"),
                        Input::new(title).into_any_element(),
                        Some(t("group.fanOut.titleHint")),
                    ),
                    field(
                        t("group.fanOut.taskLabel"),
                        Textarea::new(task).into_any_element(),
                        None,
                    ),
                    field(
                        tv("group.fanOut.agents", &[("count", n.into())]),
                        agents.into_any_element(),
                        None,
                    ),
                    h_flex()
                        .gap_3()
                        .items_start()
                        .child(controls::toggle(
                            "fan-judge",
                            judge_on,
                            false,
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                if let Some(Dialog::FanOut { judge, .. }) = &mut this.side.dialog {
                                    *judge = !*judge;
                                }
                                cx.notify();
                            }),
                        ))
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .child(div().text_sm().child(t("group.fanOut.judge")))
                                .child(
                                    div()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(t("group.fanOut.judgeHint")),
                                ),
                        )
                        .into_any_element(),
                    controls::note(t("group.fanOut.how")),
                ];
                body.extend(error(err));
                modal(
                    "fan-out",
                    t("group.fanOut.title"),
                    Some(t("group.fanOut.description")),
                    body,
                    cancel,
                    tv("group.fanOut.start", &[("count", n.into())]),
                    false,
                    *busy || task.read(cx).value().trim().is_empty() || n < 2,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.start_fan_out(cx)),
                )
            }
            Dialog::NewAgent(d) => self.render_new_agent(d, cancel, cx),
            Dialog::NewChat { project } => {
                let p = project.clone();
                let label = self
                    .projects
                    .iter()
                    .find(|x| &x.name == project)
                    .map(label_of)
                    .unwrap_or_else(|| project.clone());
                modal(
                    "new-chat",
                    t("chat.tab.newChatTitle"),
                    Some(tv(
                        "chat.tab.newChatDescription",
                        &[
                            ("scope", "project".into()),
                            ("tool", crate::chat::tool_label("").into()),
                            ("project", label.into()),
                            ("name", "".into()),
                        ],
                    )),
                    Vec::new(),
                    cancel,
                    t("chat.tab.newChat"),
                    true,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.side.dialog = None;
                        this.clear_lead_chat(p.clone(), cx);
                    }),
                )
            }
            Dialog::Keys => self.render_keys(cancel.clone(), cx),
        })
    }
}

fn img_of(i: std::sync::Arc<Image>) -> AnyElement {
    img(i).size_full().into_any_element()
}

/// The shortcut sheet (lib/shortcuts.ts sheet), with Ctrl for Mod.
/// A shortcut's keys and what they do.
type Row = (&'static [&'static str], &'static str);

const SHEET: &[(&str, &[Row])] = &[
    (
        "keys.group.app",
        &[
            (&["Ctrl", "K"], "keys.search"),
            (&["Ctrl", "N"], "keys.newTask"),
            (&["Ctrl", ","], "keys.settings"),
            (&["Ctrl", "/"], "keys.sheet"),
            (&["Alt", "←"], "keys.backToLead"),
            (&["F6"], "keys.f6"),
            (&["Shift", "F6"], "keys.f6back"),
        ],
    ),
    (
        "keys.group.palette",
        &[
            (&[">"], "keys.paletteCommands"),
            (&["Tab"], "keys.paletteFilter"),
            (&["Shift", "Enter"], "keys.paletteOther"),
            (&["Esc"], "keys.paletteBack"),
        ],
    ),
    (
        "keys.group.chat",
        &[
            (&["Enter"], "keys.send"),
            (&["Shift", "Enter"], "keys.newLine"),
            (&["↑"], "keys.recall"),
            (&["@"], "keys.mention"),
            (&["/"], "keys.commands"),
            (&["Ctrl", "F"], "keys.find"),
        ],
    ),
    (
        "keys.group.sessions",
        &[
            (&["Ctrl", "T"], "keys.sessionNew"),
            (&["Ctrl", "W"], "keys.sessionClose"),
            (&["Ctrl", "Shift", "⌫"], "keys.sessionClose"),
            (&["Ctrl", "Tab"], "keys.sessionNext"),
            (&["Ctrl", "Shift", "Tab"], "keys.sessionPrevious"),
            (&["Ctrl", "1…9"], "keys.sessionJump"),
            (&["Ctrl", "K"], "keys.sessionSearch"),
        ],
    ),
    (
        "keys.group.target",
        &[
            (&["Ctrl", "."], "keys.targetNext"),
            (&["Ctrl", "Shift", "."], "keys.targetPrevious"),
            (&["Esc"], "keys.targetLead"),
            (&["@agent"], "keys.targetMention"),
        ],
    ),
    (
        "keys.group.panel",
        &[
            (&["Ctrl", "D"], "keys.panel"),
            (&["F", "D", "T", "B", "P", "M"], "keys.launcher"),
            (&["Delete"], "keys.closeTab"),
        ],
    ),
    (
        "keys.group.review",
        &[
            (&["←", "→"], "keys.reviewSlides"),
            (&["N"], "keys.reviewNext"),
            (&["P"], "keys.reviewPrevious"),
            (&["C"], "keys.reviewChanges"),
            (&["M"], "keys.reviewMerge"),
            (&["Esc"], "keys.reviewClose"),
        ],
    ),
];

impl AppView {
    /// render_keys is v2/ShortcutSheet.tsx in ui/dialog.tsx's DialogContent
    /// (max-w-2xl, p-6, gap-5, max-h-[88vh] scrolling, its X at top-4
    /// right-4): the groups two by two, then TourButtons.
    fn render_keys(&self, cancel: OnCancel, cx: &mut Context<Self>) -> AnyElement {
        let vp = controls::viewport();
        let scroll = &self.side.keys_scroll;
        let gutter = scroll.max_offset().y > px(0.5);
        let group = |(title, rows): &(&str, &[Row])| {
            v_flex()
                .flex_1()
                .min_w_0()
                .child(
                    div()
                        .mb_2()
                        .text_size(theme::text::CAPTION)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::muted())
                        .child(t(title)),
                )
                .child(
                    v_flex()
                        .gap(px(6.))
                        .children(rows.iter().map(|(keys, label)| {
                            h_flex()
                                .items_center()
                                .justify_between()
                                .gap_3()
                                .text_size(theme::text::UI)
                                .child(
                                    div()
                                        .min_w_0()
                                        // gpui lays 19.5px lines at 19: a label of
                                        // two is Chromium's 39 with this pixel.
                                        .pb(px(1.))
                                        .text_color(theme::secondary())
                                        .child(t(label)),
                                )
                                .child(h_flex().flex_none().items_center().gap_1().children(
                                    keys.iter().map(|k| {
                                        div()
                                            .min_w_6()
                                            .rounded_md()
                                            .border_1()
                                            .border_color(theme::line_strong())
                                            .bg(theme::surface_raised())
                                            .px(px(6.))
                                            .py(px(2.))
                                            .text_center()
                                            .font_family(theme::mono())
                                            .text_size(theme::text::CAPTION)
                                            .line_height(px(18.))
                                            .text_color(theme::primary())
                                            .child(k.to_string())
                                    }),
                                ))
                        })),
                )
        };
        // grid sm:grid-cols-2 gap-x-8 gap-y-5: a row of two, each as tall as
        // its taller.
        let grid = v_flex().gap_5().children(SHEET.chunks(2).map(|pair| {
            h_flex()
                .items_start()
                .gap_8()
                .children(pair.iter().map(group))
                .when(pair.len() == 1, |el| el.child(div().flex_1()))
        }));
        // TourButtons: Take the tour, then What's new's tours. The native
        // app has the welcome tour only, which each of them plays.
        let tour_button = |id: usize, label: String, secondary: bool| {
            let me = cx.entity().downgrade();
            h_flex()
                .id(("keys-tour", id))
                .flex_none()
                .h_8()
                .px_3()
                .gap_2()
                .items_center()
                .rounded_lg()
                .text_size(theme::text::UI)
                .font_weight(FontWeight::MEDIUM)
                .cursor_pointer()
                .map(|el| {
                    if secondary {
                        el.border_1()
                            .border_color(theme::line())
                            .bg(theme::surface_raised())
                            .text_color(theme::primary())
                            .hover(|s| s.bg(theme::surface_strong()))
                            .child(crate::icons::ic("circle-question-mark").size_4())
                    } else {
                        el.text_color(theme::muted())
                            .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                    }
                })
                .on_click(move |_, _, cx| {
                    me.update(cx, |this, cx| {
                        this.side.dialog = None;
                        this.replay_tour(cx);
                    })
                    .ok();
                })
                .child(label)
        };
        const TOURS: [&str; 11] = [
            "tour.v2.name",
            "tour.reviewTour.name",
            "tour.panelTour.name",
            "tour.groupsTour.name",
            "tour.crossTour.name",
            "tour.placesTour.name",
            "tour.switchTour.name",
            "tour.compareTour.name",
            "tour.pickTour.name",
            "tour.schedulesTour.name",
            "tour.diffNotes.name",
        ];
        let tours = h_flex()
            .flex_wrap()
            .items_center()
            .gap_2()
            .border_t_1()
            .border_color(theme::line())
            .pt_4()
            .child(tour_button(0, t("tour.help.take"), true))
            .child(
                div()
                    .ml_1()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(format!("{}:", t("tour.whatsNew"))),
            )
            .children(
                TOURS
                    .iter()
                    .enumerate()
                    .map(|(i, k)| tour_button(i + 1, t(k), false)),
            );
        let back = cancel.clone();
        crate::motion::scrim_in(
            div()
                .id("keys-backdrop")
                .absolute()
                .inset_0()
                .bg(theme::scrim())
                .flex()
                .items_center()
                .justify_center()
                .occlude()
                .on_mouse_down(MouseButton::Left, move |_, w, cx| back(w, cx))
                .child(crate::motion::pop_in(
                    v_flex()
                        .id("keys-card")
                        .relative()
                        .w(vp.width - px(32.))
                        .max_w(px(672.))
                        .max_h(vp.height * 0.88)
                        .overflow_hidden()
                        .rounded(theme::radius::XXL)
                        .border_1()
                        .border_color(theme::line_strong())
                        .bg(theme::modal())
                        .shadow(vec![BoxShadow {
                            color: theme::shadow_deep(),
                            offset: point(px(0.), px(40.)),
                            blur_radius: px(120.),
                            spread_radius: px(-30.),
                            inset: false,
                        }])
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .child(
                            div()
                                .id("keys-scroll")
                                .min_h_0()
                                .overflow_y_scroll()
                                .track_scroll(scroll)
                                .p_6()
                                .when(gutter, |el| el.pr(px(24.) + controls::SCROLLBAR))
                                .child(
                                    v_flex()
                                        .gap_5()
                                        .child(
                                            v_flex()
                                                .gap(px(6.))
                                                .pr_8()
                                                .child(
                                                    div()
                                                        .text_size(theme::text::HEADING)
                                                        .line_height(px(24.))
                                                        .font_weight(FontWeight::SEMIBOLD)
                                                        .text_color(theme::title())
                                                        .child(t("keys.title")),
                                                )
                                                .child(
                                                    div()
                                                        .text_size(theme::text::BODY)
                                                        .line_height(relative(1.625))
                                                        .text_color(theme::muted())
                                                        .child(tv(
                                                            "keys.description",
                                                            &[("keys", "Ctrl /".into())],
                                                        )),
                                                ),
                                        )
                                        .child(grid)
                                        .child(tours),
                                ),
                        )
                        .child(controls::scrollbar(scroll, gutter))
                        .child(
                            div()
                                .id("keys-close")
                                .absolute()
                                .top_4()
                                .right(px(16.) + if gutter { controls::SCROLLBAR } else { px(0.) })
                                .p_1()
                                .rounded_md()
                                .cursor_pointer()
                                .text_color(theme::subtle())
                                .hover(|s| {
                                    s.bg(theme::surface_raised()).text_color(theme::primary())
                                })
                                .on_click(move |_, w, cx| cancel(w, cx))
                                .child(crate::icons::ic("x").size_4()),
                        ),
                    "keys-card-in",
                )),
            "keys-backdrop-in",
        )
        .into_any_element()
    }
}

/// variants are what a fan-out's agent can run, as "ai|model" and its
/// label (lib/groups.ts variants).
fn variants(
    settings: Option<&Settings>,
    auth: Option<&AuthStatus>,
    default: &str,
) -> Vec<(String, String)> {
    let mut out = Vec::new();
    let mut add = |ai: &str, choices: &[agentbox_api::ChatOptionChoice]| {
        if choices.is_empty() {
            out.push((format!("{ai}|"), format!("{} · {default}", tool_label(ai))));
        }
        for c in choices {
            let name = if c.name.is_empty() { &c.value } else { &c.name };
            out.push((
                format!("{ai}|{}", c.value),
                format!("{} · {name}", tool_label(ai)),
            ));
        }
    };
    let none = Vec::new();
    let s = |f: fn(&Settings) -> &Vec<agentbox_api::ChatOptionChoice>| settings.map_or(&none, f);
    if auth.is_none_or(|a| a.claude) {
        add("claude", s(|s| &s.claude_model_choices));
    }
    if auth.is_some_and(|a| a.codex) {
        out.push(("codex|".into(), tool_label("codex")));
    }
    let mut add2 = |ai: &str, choices: &[agentbox_api::ChatOptionChoice]| {
        if choices.is_empty() {
            out.push((format!("{ai}|"), format!("{} · {default}", tool_label(ai))));
        }
        for c in choices {
            let name = if c.name.is_empty() { &c.value } else { &c.name };
            out.push((
                format!("{ai}|{}", c.value),
                format!("{} · {name}", tool_label(ai)),
            ));
        }
    };
    if auth.is_some_and(|a| a.opencode) {
        add2("opencode", s(|s| &s.open_code_model_choices));
    }
    if auth.is_some_and(|a| a.cursor) {
        add2("cursor", s(|s| &s.cursor_model_choices));
    }
    if auth.is_some_and(|a| a.gemini) {
        add2("gemini", s(|s| &s.gemini_model_choices));
    }
    out
}

/// default_picks are Opus and Sonnet when on offer, else the first two.
fn default_picks(options: &[(String, String)]) -> Vec<String> {
    let claude: Vec<&String> = options
        .iter()
        .map(|o| &o.0)
        .filter(|v| v.starts_with("claude|") && *v != "claude|default" && !v.contains("fable"))
        .collect();
    let named: Vec<String> = ["opus", "sonnet"]
        .iter()
        .filter_map(|m| claude.iter().find(|v| v.as_str() == format!("claude|{m}")))
        .map(|v| v.to_string())
        .collect();
    if named.len() == 2 {
        return named;
    }
    if claude.len() >= 2 {
        return vec![claude[0].clone(), claude[1].clone()];
    }
    let first = options
        .first()
        .map_or_else(|| "claude|".to_string(), |o| o.0.clone());
    let other = options
        .iter()
        .find(|o| o.0 != first)
        .map_or_else(|| first.clone(), |o| o.0.clone());
    vec![first, other]
}

/// pick asks for files, or with folders a folder, with the system's own
/// dialog (as the composer's attach does).
pub(super) fn pick(
    title: String,
    folders: bool,
    cx: &mut App,
) -> tokio::sync::oneshot::Receiver<Option<Vec<PathBuf>>> {
    let (tx, rx) = tokio::sync::oneshot::channel();
    #[cfg(target_os = "linux")]
    {
        use gtk::prelude::*;
        let _ = cx;
        if gtk::is_initialized_main_thread() {
            let dialog = gtk::FileChooserNative::new(
                Some(&title),
                None::<&gtk::Window>,
                if folders {
                    gtk::FileChooserAction::SelectFolder
                } else {
                    gtk::FileChooserAction::Open
                },
                None,
                None,
            );
            let tx = std::cell::RefCell::new(Some(tx));
            dialog.connect_response(move |d, resp| {
                let paths = (resp == gtk::ResponseType::Accept).then(|| d.filenames());
                if let Some(tx) = tx.borrow_mut().take() {
                    let _ = tx.send(paths);
                }
                d.destroy();
            });
            dialog.show();
            KEEP.with_borrow_mut(|k| {
                k.retain(|d| d.is_visible());
                k.push(dialog);
            });
            return rx;
        }
        let _ = tx.send(None);
        rx
    }
    #[cfg(not(target_os = "linux"))]
    {
        let answer = cx.prompt_for_paths(PathPromptOptions {
            files: !folders,
            directories: folders,
            multiple: false,
            prompt: Some(title.into()),
        });
        cx.background_executor()
            .spawn(async move {
                let paths = answer.await.ok().and_then(|r| r.ok()).flatten();
                let _ = tx.send(paths);
            })
            .detach();
        rx
    }
}

#[cfg(target_os = "linux")]
thread_local! {
    static KEEP: std::cell::RefCell<Vec<gtk::FileChooserNative>> = const { std::cell::RefCell::new(Vec::new()) };
}

#[cfg(test)]
mod tests {
    use super::{ProjectIcon, data_image, default_picks, shown_of};

    #[test]
    fn default_picks_prefer_opus_and_sonnet() {
        let o = |v: &str| (v.to_string(), v.to_string());
        let opts = vec![
            o("claude|default"),
            o("claude|sonnet"),
            o("claude|opus"),
            o("codex|"),
        ];
        assert_eq!(default_picks(&opts), vec!["claude|opus", "claude|sonnet"]);
        let few = vec![o("claude|"), o("codex|")];
        assert_eq!(default_picks(&few), vec!["claude|", "codex|"]);
    }

    use crate::app::sidebar::Shown;

    #[test]
    fn shown_reads_an_emoji_first() {
        let icon = ProjectIcon {
            emoji: "🦀".into(),
            ..Default::default()
        };
        assert!(matches!(shown_of(&icon), Shown::Emoji(e) if e == "🦀"));
        assert!(data_image("data:image/png;base64,iVBORw0KGgo=").is_some());
        assert!(data_image("https://example.com/x.png").is_none());
    }
}
