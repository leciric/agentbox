//! The Office's way in (crate::office is the Office itself): its sidebar row,
//! opening and leaving it, and what it asks of the app — an agent's chat,
//! opened in place over the room.

use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::office::{OfficeEvent, OfficeView};
use crate::theme;
use agentbox_i18n::t;

impl AppView {
    /// open_office shows the Office over the whole window (made the first
    /// time, then kept: coming back is where you left it).
    pub(super) fn open_office(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.office_open = true;
        match &self.office {
            Some(o) => o.update(cx, |o, cx| o.show(true, window, cx)),
            None => {
                let client = self.client.clone();
                let o = cx.new(|cx| OfficeView::new(client, window, cx));
                let sub = cx.subscribe_in(&o, window, |this, _, ev: &OfficeEvent, window, cx| {
                    this.on_office(ev.clone(), window, cx)
                });
                self._subs.push(sub);
                self.office = Some(o);
            }
        }
        cx.notify();
    }

    fn on_office(&mut self, ev: OfficeEvent, window: &mut Window, cx: &mut Context<Self>) {
        match ev {
            OfficeEvent::Leave => {
                self.office_open = false;
                if let Some(o) = &self.office {
                    o.update(cx, |o, cx| {
                        o.set_chat(None, cx);
                        o.show(false, window, cx);
                    });
                }
            }
            OfficeEvent::Open { project, who } => {
                if self.project.as_deref() != Some(project.as_str()) {
                    self.select_project(project, window, cx);
                }
                self.open_chat(who, window, cx);
                let chat = self.chat.clone().map(AnyView::from);
                if let Some(o) = &self.office {
                    o.update(cx, |o, cx| o.set_chat(chat, cx));
                }
            }
            OfficeEvent::OpenUrl(u) => cx.open_url(&u),
        }
        cx.notify();
    }

    /// office_poke tells an open Office the daemon changed something.
    pub(super) fn office_poke(&mut self, cx: &mut Context<Self>) {
        if let (true, Some(o)) = (self.office_open, &self.office) {
            o.update(cx, |o, cx| o.poke(cx));
        }
    }

    /// render_office is the Office over everything else, while it's open.
    pub(super) fn render_office(&self) -> Option<AnyElement> {
        let o = self.office.clone().filter(|_| self.office_open)?;
        Some(div().absolute().inset_0().child(o).into_any_element())
    }

    /// office_row is the sidebar's way in (#668's OfficeRow), with how many
    /// need you; there only while Settings has the Office on.
    pub(super) fn office_row(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !crate::office::enabled() {
            return None;
        }
        // Who needs you: asks and failures, every project's (#668's officeNeeds).
        let needs: usize = self
            .projects
            .iter()
            .map(|p| {
                let [asks, failed, _, _] = self.st_counts(&p.name);
                asks + failed
            })
            .sum();
        Some(
            h_flex()
                .id("row-office")
                .mx_2()
                .px_2()
                .py_1p5()
                .gap_2()
                .rounded_md()
                .cursor_pointer()
                .when(self.office_open, |el| el.bg(theme::surface_strong()))
                .hover(|el| el.bg(theme::surface_raised()))
                .active(|s| s.bg(theme::surface_strong()))
                .on_click(
                    cx.listener(|this, _: &ClickEvent, window, cx| this.open_office(window, cx)),
                )
                .child(
                    div()
                        .flex_none()
                        .size_6()
                        .flex()
                        .items_center()
                        .justify_center()
                        .rounded_md()
                        .bg(theme::surface_raised())
                        .text_color(theme::muted())
                        .child(crate::icons::of("🪑")),
                )
                .child(
                    v_flex()
                        .min_w_0()
                        .flex_1()
                        .child(
                            div()
                                .text_sm()
                                .text_color(theme::primary())
                                .child(t("native.office.title")),
                        )
                        .child(
                            div()
                                .text_xs()
                                .text_color(theme::faint())
                                .truncate()
                                .child(t("native.office.hint.nav")),
                        ),
                )
                .when(needs > 0, |el| {
                    el.child(
                        div()
                            .flex_none()
                            .min_w_4()
                            .px_1()
                            .rounded_full()
                            .bg(gpui::rgb(0xfbbf24))
                            .text_size(px(10.))
                            .font_weight(FontWeight::SEMIBOLD)
                            .text_color(gpui::black())
                            .flex()
                            .justify_center()
                            .child(if needs > 99 {
                                "99+".to_string()
                            } else {
                                needs.to_string()
                            }),
                    )
                })
                .into_any_element(),
        )
    }
}
