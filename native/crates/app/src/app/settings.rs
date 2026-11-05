//! Settings (components/SettingsView.tsx), so far its General section's
//! language: which the daemon keeps (PATCH /v1/settings), so the app, the
//! Electron app and the phone app all speak it.
use agentbox_i18n::{LANGUAGES, t};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use crate::theme;

impl AppView {
    fn set_language(&mut self, tag: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let client = self.client.clone();
        agentbox_i18n::set_language(tag);
        window.refresh();
        cx.spawn(async move |this, cx| {
            let body = serde_json::to_vec(&serde_json::json!({ "language": tag })).unwrap();
            let res = client
                .raw(
                    agentbox_api::client::Method::PATCH,
                    "/v1/settings",
                    Some(body),
                )
                .await;
            this.update(cx, |this, cx| {
                this.error = res.err().map(|e| e.to_string().into());
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn render_settings(&self, cx: &mut Context<Self>) -> AnyElement {
        let current = agentbox_i18n::language();
        v_flex()
            .id("settings")
            .size_full()
            .overflow_y_scroll()
            .bg(theme::bg())
            .px_8()
            .py_6()
            .gap_6()
            .child(
                div()
                    .text_xl()
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(theme::fg())
                    .child(t("common.settings")),
            )
            .child(
                v_flex()
                    .gap_1()
                    .child(
                        div()
                            .text_color(theme::fg())
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(t("settings.section.general.title")),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("settings.section.general.description")),
                    ),
            )
            .child(
                v_flex()
                    .gap_3()
                    .p_4()
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::panel())
                    .child(
                        v_flex()
                            .gap_1()
                            .child(
                                div()
                                    .text_sm()
                                    .text_color(theme::fg())
                                    .child(t("common.language")),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::muted())
                                    .child(t("common.languageDescription")),
                            ),
                    )
                    .child(
                        h_flex()
                            .gap_2()
                            .children(LANGUAGES.iter().map(|(tag, name)| {
                                let on = *tag == current;
                                div()
                                    .id(SharedString::from(format!("lang-{tag}")))
                                    .px_3()
                                    .py_1p5()
                                    .rounded_md()
                                    .cursor_pointer()
                                    .border_1()
                                    .border_color(if on {
                                        theme::accent()
                                    } else {
                                        theme::border()
                                    })
                                    .when(on, |el| el.bg(theme::hover()))
                                    .text_sm()
                                    .text_color(theme::fg())
                                    .child(*name)
                                    .on_click(cx.listener(
                                        move |this, _: &ClickEvent, window, cx| {
                                            this.set_language(tag, window, cx)
                                        },
                                    ))
                            })),
                    ),
            )
            .into_any_element()
    }
}
