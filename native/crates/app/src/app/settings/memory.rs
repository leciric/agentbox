//! Settings → Memory (GlobalMemoryPanel.tsx): what the user asked a chat to
//! remember for every project, which every project's searches read beside
//! its own. Nothing is added here, since a chat writes them; one that no
//! longer holds is deleted here (GET and DELETE /v1/global/memory/memories).
use agentbox_api::Memory;
use agentbox_api::client::Method;
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::input::Input;
use gpui_component::{Sizable as _, h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note_in};
use super::defaults::time_ago;
use super::kit::{ask, card, empty, enc};
use super::kit::{badge, note};
use super::{Entry, Group, group};
use crate::theme;

/// The project a memory remembered from the Home chat names (state.HomeProject).
const HOME_PROJECT: &str = "_home";

#[derive(Default)]
pub(super) struct GlobalMemory {
    list: Option<Vec<Memory>>,
    error: Option<String>,
    deleting: Option<String>,
    said: Option<String>,
}

fn kind_color(kind: &str) -> Hsla {
    match kind {
        "project" => theme::accent(),
        "decision" => theme::accent(),
        "discovery" => theme::running(),
        "issue" => theme::starting(),
        _ => theme::muted(),
    }
}

impl AppView {
    pub(super) fn memory_groups(&self) -> Vec<Group> {
        vec![
            group(
                "memory",
                "settings.group.memory.title",
                vec![Entry {
                    id: "global-memory",
                    label: t("settings.section.memory.title"),
                    description: t("settings.section.memory.description"),
                    keywords: t("settings.entry.memory.keywords"),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .described("settings.group.memory.description")
            .cards(),
        ]
    }

    pub(super) fn load_global_memory(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<Vec<Memory>>("/v1/global/memory/memories")
                .await;
            this.update(cx, |this, cx| {
                let m = &mut this.settings_page.memory;
                match res {
                    Ok(list) => {
                        m.list = Some(list);
                        m.error = None;
                    }
                    Err(e) => m.error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn delete_global_memory(&mut self, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let title = self
            .settings_page
            .memory
            .list
            .iter()
            .flatten()
            .find(|m| m.id == id)
            .map(|m| m.title.clone())
            .unwrap_or_default();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(
                    Method::DELETE,
                    &format!("/v1/global/memory/memories/{}", enc(&id)),
                    None,
                )
                .await;
            this.update(cx, |this, cx| {
                let m = &mut this.settings_page.memory;
                m.deleting = None;
                match res {
                    Ok(_) => {
                        m.said = Some(tv(
                            "memory.global.deletedToast",
                            &[("title", (&title).into())],
                        ));
                        m.error = None;
                    }
                    Err(e) => m.error = Some(e.to_string()),
                }
                this.load_global_memory(cx);
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn memory_card(&self, cx: &mut Context<Self>) -> AnyElement {
        let m = &self.settings_page.memory;
        let filter = self.field_value("memory-filter", cx).to_lowercase();
        let words: Vec<&str> = filter.split_whitespace().collect();
        let all = m.list.as_deref().unwrap_or_default();
        let items: Vec<&Memory> = all
            .iter()
            .filter(|m| {
                let hay = format!("{}\n{}", m.title, m.content).to_lowercase();
                words.iter().all(|w| hay.contains(w))
            })
            .collect();
        v_flex()
            .id("global-memories")
            .gap_2()
            .when_some(m.error.clone(), |el, e| {
                el.child(note_in(e, theme::rose(300)))
            })
            .when_some(m.said.clone(), |el, s| {
                el.child(note_in(s, theme::emerald(300)))
            })
            .when(m.list.is_none() && m.error.is_none(), |el| {
                el.child(super::super::panel::loading("memories"))
            })
            .when(!all.is_empty(), |el| {
                el.child(self.input_small("memory-filter"))
            })
            .when(!all.is_empty() && items.is_empty(), |el| {
                el.child(note(t("memory.global.noMatch")))
            })
            .when(m.list.as_ref().is_some_and(|l| l.is_empty()), |el| {
                el.child(empty(
                    "memory",
                    t("memory.global.empty.title"),
                    t("memory.global.empty.body"),
                ))
            })
            .children(items.into_iter().map(|mem| self.memory_row(mem, cx)))
            .into_any_element()
    }

    fn memory_row(&self, mem: &Memory, cx: &mut Context<Self>) -> AnyElement {
        let deleting = self.settings_page.memory.deleting.as_deref() == Some(mem.id.as_str());
        let id = mem.id.clone();
        let kind_key = format!("memory.kind.{}", mem.kind);
        let kind = if agentbox_i18n::has(&kind_key) {
            t(&kind_key)
        } else {
            mem.kind.clone()
        };
        let mut facts = vec![time_ago(&mem.created_at)];
        if mem.global && !mem.origin.is_empty() {
            facts.push(if mem.origin == HOME_PROJECT {
                t("memory.global.fromHome")
            } else {
                tv("memory.global.from", &[("project", (&mem.origin).into())])
            });
        }
        if mem.updated_at != mem.created_at && !mem.updated_at.is_empty() {
            let when = time_ago(&mem.updated_at);
            facts.push(tv("memory.updatedAgo", &[("when", (&when).into())]));
        }
        card(SharedString::from(format!("memory-{}", mem.id)))
            .px_4()
            .py_3()
            .gap_0()
            .child(
                h_flex()
                    .gap_2()
                    .items_start()
                    .child(badge(kind, kind_color(&mem.kind)))
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .text_size(theme::text::BODY)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::primary())
                            .child(mem.title.clone()),
                    )
                    .child(h_flex().gap(px(3.)).pt(px(5.)).children((1..=5).map(|n| {
                        div()
                            .size(px(6.))
                            .rounded_full()
                            .bg(if n <= mem.importance {
                                theme::accent()
                            } else {
                                theme::surface_strong()
                            })
                    })))
                    .child(button(
                        SharedString::from(format!("memory-delete-{}", mem.id)),
                        t("memory.global.delete"),
                        Btn::Ghost,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.settings_page.memory.deleting = Some(id.clone());
                            cx.notify();
                        }),
                    )),
            )
            .when(!mem.content.is_empty(), |el| {
                el.child(
                    div()
                        .mt_1p5()
                        .text_size(theme::text::UI)
                        .line_height(rems(1.5))
                        .text_color(theme::muted())
                        .child(mem.content.clone()),
                )
            })
            .child(
                div()
                    .mt_2()
                    .text_size(theme::text::CAPTION)
                    .text_color(theme::subtle())
                    .child(facts.join(" · ")),
            )
            .when(deleting, |el| {
                let id = mem.id.clone();
                el.child(ask(
                    "memory-delete",
                    tv(
                        "memory.global.deleteTitle",
                        &[("title", (&mem.title).into())],
                    ),
                    t("memory.global.deleteDescription"),
                    t("memory.global.delete"),
                    true,
                    false,
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.delete_global_memory(id.clone(), cx)
                    }),
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        this.settings_page.memory.deleting = None;
                        cx.notify();
                    }),
                ))
            })
            .into_any_element()
    }

    /// input_small is one of the page's fields, at a field's height.
    /// input_field is Electron's Input: h-9 rounded-lg in line-strong on the
    /// sunken ground, px-3, text-sm.
    pub(super) fn input_field(&self, name: &str) -> AnyElement {
        match self.settings_page.fields.get(name) {
            Some(i) => h_flex()
                .h_9()
                .px_3()
                .rounded_lg()
                .border_1()
                .border_color(theme::line_strong())
                .bg(theme::sunken())
                .text_size(theme::text::BODY)
                .child(
                    div().flex_1().min_w_0().child(
                        Input::new(i)
                            .appearance(false)
                            .with_size(gpui_component::Size::Size(px(14. / 0.875)))
                            .px_0(),
                    ),
                )
                .into_any_element(),
            None => div().into_any_element(),
        }
    }

    pub(super) fn input_small(&self, name: &str) -> AnyElement {
        match self.settings_page.fields.get(name) {
            Some(i) => Input::new(i).small().into_any_element(),
            None => div().into_any_element(),
        }
    }
}
