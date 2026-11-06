//! The top bar's status chips (TopBar.tsx), at the right of the project's
//! header and of Home's: "Finish setup" while GET /v1/setup isn't ready,
//! opening Settings → Setup, and the base image's build (ImageBuildChip.tsx),
//! whose popover follows its job's log and retries one that failed.
//!
//! Each chip reads the daemon itself, every few seconds, for as long as the
//! window is open; a popover closes on a click outside it.
use std::time::{Duration, Instant};

use agentbox_api::{BuildImageRequest, ImageStatus, Job, SetupStatus};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{Btn, button, note_in};
use crate::theme;

#[derive(Default)]
pub(super) struct TopBar {
    pub setup: Option<SetupStatus>,
    pub image: Option<ImageStatus>,
    /// pop is the chip whose popover is open.
    pub pop: Option<&'static str>,
    pop_closed: Option<(&'static str, Instant)>,
    retry_error: Option<String>,
    polling: bool,
}

/// chip is a pill in the top bar, in a tone of its own.
pub(super) fn chip(id: &'static str, color: Hsla) -> Stateful<Div> {
    h_flex()
        .id(id)
        .flex_none()
        .gap_1p5()
        .px_2p5()
        .py_0p5()
        .rounded_full()
        .border_1()
        .border_color(color.opacity(0.3))
        .bg(color.opacity(0.1))
        .text_xs()
        .font_weight(FontWeight::MEDIUM)
        .text_color(color)
        .cursor_pointer()
        .hover(move |s| s.bg(color.opacity(0.16)))
}

/// popover is a chip's card, under it, kept inside the window.
pub(super) fn popover(id: &'static str, width: f32, cx: &mut Context<AppView>) -> Stateful<Div> {
    v_flex()
        .id(id)
        .w(px(width))
        .max_w(px(width))
        .gap_3()
        .p_3()
        .rounded_lg()
        .bg(theme::card())
        .border_1()
        .border_color(theme::line())
        .shadow_lg()
        .text_color(theme::primary())
        .font_weight(FontWeight::NORMAL)
        .cursor_default()
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
        .on_click(|_, _, cx| cx.stop_propagation())
        .on_mouse_down_out(cx.listener(|this, _: &MouseDownEvent, _, cx| {
            if let Some(p) = this.topbar.pop.take() {
                this.topbar.pop_closed = Some((p, Instant::now()));
                cx.notify();
            }
        }))
}

impl AppView {
    /// watch_topbar reads what the chips show: the setup every fifteen
    /// seconds, the image every five, or every second while it builds.
    pub(super) fn watch_topbar(&mut self, cx: &mut Context<Self>) {
        if std::mem::replace(&mut self.topbar.polling, true) {
            return;
        }
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let mut tick = 0u32;
            loop {
                let image = client.get::<ImageStatus>("/v1/image").await.ok();
                let setup = if tick.is_multiple_of(15) {
                    Some(client.get::<SetupStatus>("/v1/setup").await.ok())
                } else {
                    None
                };
                let building = image.as_ref().is_some_and(|i| i.build == "building");
                let more = this.update(cx, |this, cx| {
                    this.set_image(image, cx);
                    if let Some(s) = setup {
                        this.topbar.setup = s;
                    }
                    if tick.is_multiple_of(5) {
                        this.watch_meters(tick, cx);
                    }
                    cx.notify();
                });
                if more.is_err() {
                    return;
                }
                // Every second while it builds, else every five.
                let wait = if building { 1 } else { 5 };
                for _ in 0..wait {
                    cx.background_executor().timer(Duration::from_secs(1)).await;
                    tick += 1;
                }
            }
        })
        .detach();
    }

    /// set_image takes the image's state; a build that ends says so.
    fn set_image(&mut self, image: Option<ImageStatus>, cx: &mut Context<Self>) {
        let was = self.topbar.image.as_ref().map(|i| i.build.clone());
        if let Some(i) = &image {
            if was.as_deref() == Some("building") && i.build == "idle" && i.ready {
                crate::toasts::info(cx, t("shell.imageChip.readyToast"));
            }
            if (i.build == "building" || i.build == "failed")
                && !i.job.is_empty()
                && self.topbar.pop == Some("image")
            {
                self.follow_job(i.job.clone(), cx);
            }
        }
        self.topbar.image = image;
    }

    pub(super) fn toggle_pop(&mut self, which: &'static str, cx: &mut Context<Self>) {
        // The click that closed it by landing outside isn't one that opens it.
        if self
            .topbar
            .pop_closed
            .is_some_and(|(p, at)| p == which && at.elapsed() < Duration::from_millis(400))
        {
            return;
        }
        self.topbar.pop = if self.topbar.pop == Some(which) {
            None
        } else {
            Some(which)
        };
        if which == "image"
            && let Some(job) = self.topbar.image.as_ref().map(|i| i.job.clone())
            && !job.is_empty()
        {
            self.follow_job(job, cx);
        }
        cx.notify();
    }

    fn retry_image(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        self.topbar.retry_error = None;
        cx.spawn(async move |this, cx| {
            let res = client
                .post::<_, Job>("/v1/image/build", &BuildImageRequest::default())
                .await;
            this.update(cx, |this, cx| {
                match res {
                    Ok(job) => this.follow_job(job.id, cx),
                    Err(e) => this.topbar.retry_error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// render_top_chips is the bar's right-hand side, before the page's own
    /// buttons.
    pub(super) fn render_top_chips(&self, cx: &mut Context<Self>) -> AnyElement {
        h_flex()
            .flex_none()
            .gap_2()
            .children(self.finish_setup_chip(cx))
            .children(self.image_chip(cx))
            .children(self.usage_chips(cx))
            .into_any_element()
    }

    /// render_home_bar is Home's top bar: its name, and the chips.
    pub(super) fn render_home_bar(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        if !self.is_home() {
            return None;
        }
        Some(
            h_flex()
                .flex_none()
                .h(px(44.))
                .px_4()
                .gap_2()
                .bg(theme::card())
                .border_b_1()
                .border_color(theme::line())
                .child(
                    div()
                        .text_sm()
                        .font_weight(FontWeight::MEDIUM)
                        .child(t("shell.nav.home")),
                )
                .child(div().flex_1())
                .child(self.render_top_chips(cx))
                .into_any_element(),
        )
    }

    fn finish_setup_chip(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let s = self.topbar.setup.as_ref()?;
        if s.ready || self.settings {
            return None;
        }
        Some(
            chip("finish-setup", theme::starting())
                .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                    this.open_settings_at("setup".into(), window, cx)
                }))
                .child(crate::icons::of("⚠"))
                .child(t("shell.home.finishSetup"))
                .into_any_element(),
        )
    }

    fn image_chip(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let i = self.topbar.image.as_ref()?;
        if i.job.is_empty() || (i.build != "building" && i.build != "failed") {
            return None;
        }
        let failed = i.build == "failed";
        let label = t(match () {
            _ if failed => "shell.imageChip.failed",
            _ if i.ready => "shell.imageChip.rebuilding",
            _ => "shell.imageChip.building",
        });
        let color = if failed {
            theme::error()
        } else {
            theme::accent()
        };
        let body = t(match (failed, i.ready) {
            (true, true) => "shell.imageChip.failedKeeps",
            (true, false) => "shell.imageChip.failedBody",
            (false, true) => "shell.imageChip.rebuildingBody",
            (false, false) => "shell.imageChip.buildingBody",
        });
        let open = self.topbar.pop == Some("image");
        let pop = open.then(|| {
            popover("image-pop", 520., cx)
                .child(
                    v_flex()
                        .gap_1()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(color)
                                .child(label.clone()),
                        )
                        .child(div().text_xs().text_color(theme::muted()).child(body))
                        .when(!failed && !i.step.is_empty(), |el| {
                            el.child(
                                div()
                                    .truncate()
                                    .font_family(theme::mono())
                                    .text_xs()
                                    .text_color(theme::faint())
                                    .child(i.step.clone()),
                            )
                        })
                        .when(!failed && i.waiting > 0, |el| {
                            el.child(div().text_xs().text_color(theme::muted()).child(tv(
                                "shell.imageChip.waiting",
                                &[("count", i.waiting.into())],
                            )))
                        }),
                )
                .child(self.job_progress(&i.job))
                .when_some(self.topbar.retry_error.clone(), |el, e| {
                    el.child(note_in(e, theme::error()))
                })
                .when(failed, |el| {
                    el.child(h_flex().justify_end().child(button(
                        "image-retry",
                        t("shell.imageChip.retry"),
                        Btn::Primary,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.retry_image(cx)),
                    )))
                })
        });
        Some(
            div()
                .flex_none()
                .child(
                    chip("image-chip", color)
                        .on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| this.toggle_pop("image", cx)),
                        )
                        .child(crate::icons::of(if failed { "⚠" } else { "↻" }))
                        .child(label)
                        .when(!failed && i.waiting > 0, |el| {
                            el.child(
                                div()
                                    .px_1p5()
                                    .rounded_full()
                                    .bg(color.opacity(0.2))
                                    .child(i.waiting.to_string()),
                            )
                        }),
                )
                .children(pop.map(|p| {
                    deferred(
                        anchored()
                            .offset(point(px(0.), px(28.)))
                            .snap_to_window_with_margin(px(8.))
                            .child(p),
                    )
                }))
                .into_any_element(),
        )
    }
}
