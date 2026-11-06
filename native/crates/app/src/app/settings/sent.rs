//! Two pieces Settings shows beside its own rows: what building the base
//! image downloads (ImageDownloads.tsx, from GET /v1/setup's image), under
//! the image's step, and what usage statistics would send next
//! (UsageSentDialog.tsx, GET /v1/usage-stats/pending), from "See what's
//! sent" under the switch.
use agentbox_api::{ImageBuild, UsageStatsPending};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note_in};
use super::kit::{badge, note};
use crate::theme;

/// The flag `agentbox image build` takes for an optional download.
fn flag(option: &str) -> String {
    format!("--{option}")
}

fn size(mb: i64) -> String {
    if mb < 1000 {
        format!("{mb} MB")
    } else {
        format!("{:.1} GB", mb as f64 / 1000.)
    }
}

fn wanted(image: &ImageBuild, option: &str) -> bool {
    let c = &image.components;
    match option {
        "" => true,
        "android" => c.android,
        "codex" => c.codex,
        "opencode" => c.opencode,
        "dev-caches" => c.dev_caches,
        "incus" => c.incus,
        _ => false,
    }
}

#[derive(Default)]
pub(super) struct Sent {
    /// downloads is whether the image's downloads are unfolded.
    downloads: bool,
    /// pending is the dialog: open with what the daemon said, or loading.
    pending: Option<Option<Result<UsageStatsPending, String>>>,
}

impl AppView {
    /// image_downloads is ImageDownloads: the total, unfolding to each.
    pub(super) fn image_downloads(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let image = self
            .settings_page
            .accounts
            .setup
            .as_ref()
            .map(|s| &s.image)?;
        if image.downloads.is_empty() {
            return None;
        }
        let total: i64 = image
            .downloads
            .iter()
            .filter(|d| wanted(image, &d.option))
            .map(|d| d.mb)
            .sum();
        let open = self.settings_page.sent.downloads;
        let rows = image.downloads.iter().enumerate().map(|(i, d)| {
            let on = wanted(image, &d.option);
            h_flex()
                .items_start()
                .gap_2()
                .px_3()
                .py_1p5()
                .when(i > 0, |el| {
                    el.border_t_1().border_color(theme::line_faint())
                })
                .text_size(theme::text::UI)
                .child(
                    h_flex()
                        .w(px(190.))
                        .flex_none()
                        .gap_1p5()
                        .child(
                            div()
                                .text_color(if on {
                                    theme::secondary()
                                } else {
                                    theme::faint()
                                })
                                .when(!on, |el| el.line_through())
                                .child(d.name.clone()),
                        )
                        .when(!d.option.is_empty(), |el| {
                            el.child(badge(
                                t("shell.imageDownloads.optional"),
                                if on { theme::accent() } else { theme::muted() },
                            ))
                        }),
                )
                .child(
                    div()
                        .w(px(64.))
                        .flex_none()
                        .text_right()
                        .text_color(if on { theme::muted() } else { theme::faint() })
                        .child(size(d.mb)),
                )
                .child(div().flex_1().min_w_0().text_color(theme::subtle()).child(
                    if !d.option.is_empty() && !on {
                        format!(
                            "{} {}",
                            d.purpose,
                            tv(
                                "shell.imageDownloads.turnOn",
                                &[(
                                    "command",
                                    format!("agentbox image build {}", flag(&d.option)).into(),
                                )],
                            )
                        )
                    } else {
                        d.purpose.clone()
                    },
                ))
        });
        Some(
            v_flex()
                .gap_2()
                .child(h_flex().child(button(
                    "image-downloads",
                    tv(
                        "shell.imageDownloads.summary",
                        &[("size", size(total).into())],
                    ),
                    Btn::Ghost,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| {
                        let s = &mut this.settings_page.sent;
                        s.downloads = !s.downloads;
                        cx.notify();
                    }),
                )))
                .when(open, |el| {
                    el.child(
                        v_flex()
                            .rounded_xl()
                            .border_1()
                            .border_color(theme::line())
                            .overflow_hidden()
                            .children(rows)
                            .child(
                                h_flex()
                                    .gap_2()
                                    .px_3()
                                    .py_1p5()
                                    .border_t_1()
                                    .border_color(theme::line())
                                    .bg(theme::surface_faint())
                                    .text_size(theme::text::UI)
                                    .child(
                                        div()
                                            .w(px(190.))
                                            .text_color(theme::tertiary())
                                            .child(t("shell.imageDownloads.total")),
                                    )
                                    .child(
                                        div()
                                            .w(px(64.))
                                            .text_right()
                                            .text_color(theme::tertiary())
                                            .child(size(total)),
                                    )
                                    .child(
                                        div()
                                            .flex_1()
                                            .text_color(theme::subtle())
                                            .child(format!("{}.", image.hint)),
                                    ),
                            ),
                    )
                })
                .into_any_element(),
        )
    }

    /// see_sent opens the dialog and reads what would be sent.
    pub(super) fn see_sent(&mut self, cx: &mut Context<Self>) {
        self.settings_page.sent.pending = Some(None);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<UsageStatsPending>("/v1/usage-stats/pending")
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.settings_page.sent.pending.is_some() {
                    this.settings_page.sent.pending = Some(Some(res));
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// render_usage_sent is the dialog, over the window while it's open.
    pub(in crate::app) fn render_usage_sent(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let pending = self.settings_page.sent.pending.as_ref()?;
        let payload = |title: String, body: &str, extra: Option<String>| {
            v_flex()
                .gap_1()
                .min_w_0()
                .child(
                    div()
                        .font_family(theme::mono())
                        .text_xs()
                        .text_color(theme::secondary())
                        .child(title),
                )
                .child(
                    div()
                        .px_3()
                        .py_2()
                        .rounded(theme::radius::SM)
                        .border_1()
                        .border_color(theme::line())
                        .bg(theme::sunken())
                        .font_family(theme::mono())
                        .text_size(theme::text::CAPTION)
                        .line_height(rems(1.6))
                        .text_color(theme::secondary())
                        .child(body.to_string()),
                )
                .children(extra.map(note))
        };
        let body: Vec<AnyElement> = match pending {
            None => vec![super::super::panel::loading("sent")],
            Some(Err(e)) => vec![note_in(e.clone(), theme::rose(300))],
            Some(Ok(p)) if !p.on => vec![note(t("settings.usageStats.sentOff"))],
            Some(Ok(p))
                if p.usage.is_empty()
                    && p.events.is_empty()
                    && p.health.is_empty()
                    && p.setup.is_empty() =>
            {
                vec![note(t("settings.usageStats.sentNothing"))]
            }
            Some(Ok(p)) => {
                let url = |u: &str| vec![("url", u.to_string().into())];
                let mut out = Vec::new();
                if !p.usage.is_empty() {
                    out.push(
                        payload(
                            tv("settings.usageStats.sentCounts", &url(&p.usage_url)),
                            &p.usage,
                            None,
                        )
                        .into_any_element(),
                    );
                }
                if !p.events.is_empty() {
                    let more = (p.events_waiting > 500).then(|| {
                        tv(
                            "settings.usageStats.sentMore",
                            &[("count", p.events_waiting.into())],
                        )
                    });
                    out.push(
                        payload(
                            tv("settings.usageStats.sentEvents", &url(&p.events_url)),
                            &p.events,
                            more,
                        )
                        .into_any_element(),
                    );
                }
                for (key, body) in [
                    ("settings.usageStats.sentHealth", &p.health),
                    ("settings.usageStats.sentSetup", &p.setup),
                ] {
                    if !body.is_empty() {
                        out.push(
                            payload(tv(key, &url(&p.events_url)), body, None).into_any_element(),
                        );
                    }
                }
                out
            }
        };
        let close = cx.listener(|this, _: &MouseDownEvent, _, cx| {
            this.settings_page.sent.pending = None;
            cx.notify();
        });
        Some(
            crate::motion::scrim_in(
                div()
                    .id("usage-sent")
                    .absolute()
                    .inset_0()
                    .occlude()
                    .bg(theme::scrim())
                    .flex()
                    .items_center()
                    .justify_center()
                    .on_mouse_down(MouseButton::Left, close)
                    .child(
                        v_flex()
                            .id("usage-sent-card")
                            .w(px(672.))
                            .max_w(relative(0.92))
                            .max_h(relative(0.85))
                            .gap_5()
                            .p_6()
                            .rounded(theme::radius::XXL)
                            .bg(theme::modal())
                            .border_1()
                            .border_color(theme::line_strong())
                            .shadow(theme::shadow_modal())
                            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                            .child(
                                div()
                                    .text_size(theme::text::HEADING)
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::title())
                                    .child(t("settings.usageStats.sentTitle")),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::BODY)
                                    .line_height(rems(1.5))
                                    .text_color(theme::muted())
                                    .child(t("settings.usageStats.sentDescription")),
                            )
                            .child(
                                v_flex()
                                    .id("usage-sent-body")
                                    .flex_1()
                                    .min_h_0()
                                    .overflow_y_scroll()
                                    .gap_3()
                                    .children(body),
                            )
                            .child(h_flex().justify_end().child(button(
                                "usage-sent-close",
                                t("common.close"),
                                Btn::Ghost,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.settings_page.sent.pending = None;
                                    cx.notify();
                                }),
                            ))),
                    ),
                "idusage-sent-in",
            )
            .into_any_element(),
        )
    }
}
