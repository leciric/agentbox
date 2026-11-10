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

thread_local! {
    /// Where a press on the top bar began, while it may yet become a drag.
    static DRAG_FROM: std::cell::Cell<Option<Point<Pixels>>> = const { std::cell::Cell::new(None) };
    static CLIENT_DECORATIONS: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
    static MAXIMIZED: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
}

/// set_decorations is what the window is drawn with, read before each frame
/// (app.rs's render): the top bar carries the window's controls when the
/// app, not the compositor, has to draw them.
pub(super) fn set_decorations(window: &Window) {
    CLIENT_DECORATIONS.set(matches!(
        window.window_decorations(),
        Decorations::Client { .. }
    ));
    MAXIMIZED.set(window.is_maximized());
}

fn client_decorations() -> bool {
    CLIENT_DECORATIONS.get()
}
use super::controls::{Btn, button};
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
        // Not the top bar's window drag (window_controls).
        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
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

    /// render_top_bar is Electron's TopBar (components/TopBar.tsx), over the
    /// page and the panel both: the breadcrumb, then Search, the chips, and
    /// the page's ⋯ and panel button at the far right.
    pub(super) fn render_top_bar(&self, cx: &mut Context<Self>) -> AnyElement {
        use super::foot::FootPage;
        let page = if self.settings {
            Some(t("common.settings"))
        } else if self.work.is_some() {
            Some(t("work.title"))
        } else {
            match self.side.page {
                Some(FootPage::Media) => Some(t("shell.nav.media")),
                Some(FootPage::Jobs) => Some(t("shell.nav.jobs")),
                None if self.is_home() || self.project.is_none() => Some(t("shell.nav.home")),
                None => None,
            }
        };
        if page.is_none()
            && let Some(bar) = self.render_project_header(cx)
        {
            return bar;
        }
        let crumb = div()
            .min_w_0()
            .truncate()
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::primary())
            .child(page.unwrap_or_default());
        self.top_bar_frame(crumb.into_any_element(), None, cx)
    }

    /// top_bar_frame is the bar itself: min-h-14, px-5 py-2, gap-x-4, the
    /// breadcrumb's nav at text-ui taking what's left.
    pub(super) fn top_bar_frame(
        &self,
        crumbs: AnyElement,
        end: Option<AnyElement>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        h_flex()
            .id("top-bar")
            .flex_none()
            .min_h(px(56.))
            .flex_wrap()
            .gap_x(px(16.))
            .gap_y(px(6.))
            .px(px(20.))
            .py_2()
            .border_b_1()
            .border_color(theme::line())
            // Below lg (xl with the panel beside the page), the sidebar is a
            // drawer this opens (TopBar's data-open-nav: -ml-1 p-2, size-5).
            .when(!self.sidebar_docked(), |el| {
                el.child(
                    div()
                        .id("open-nav")
                        .flex_none()
                        .ml(px(-4.))
                        .p_2()
                        .rounded_lg()
                        .cursor_pointer()
                        .text_color(theme::muted())
                        .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                        .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                        .on_click(cx.listener(|this, _: &ClickEvent, _, cx| {
                            this.widths.nav = true;
                            cx.notify();
                        }))
                        .child(crate::icons::ic("menu").size_5()),
                )
            })
            .child(
                h_flex()
                    .min_w(px(128.))
                    .flex_grow(1.)
                    .flex_basis(relative(0.))
                    .gap(px(6.))
                    .text_size(theme::text::UI)
                    .child(crumbs),
            )
            .child(
                h_flex()
                    .ml_auto()
                    .min_w_0()
                    .gap_2()
                    .child(self.search_button(cx))
                    .child(self.render_top_chips(cx))
                    .children(end),
            )
            .when(client_decorations(), |el| {
                // Pressing on the bar and moving drags the window: the
                // buttons on it stop the press from reaching here, and a
                // click's jitter doesn't count.
                el.on_mouse_down(MouseButton::Left, |ev: &MouseDownEvent, _, _| {
                    DRAG_FROM.set(Some(ev.position));
                })
                .on_mouse_up(MouseButton::Left, |_, _, _| DRAG_FROM.set(None))
                .on_mouse_move(|ev: &MouseMoveEvent, window, _| {
                    // A move, not a click's jitter, drags the window.
                    if let Some(from) = DRAG_FROM.get() {
                        let d = ev.position - from;
                        if ev.pressed_button != Some(MouseButton::Left) {
                            DRAG_FROM.set(None);
                        } else if f32::from(d.x).abs() + f32::from(d.y).abs() > 4. {
                            DRAG_FROM.set(None);
                            window.start_window_move();
                        }
                    }
                })
                .child(self.window_controls(cx))
            })
            .into_any_element()
    }

    /// window_controls are minimise, maximise and close, at the top bar's
    /// end, where the compositor draws no title bar for the window (GNOME's
    /// Wayland, which has no server-side decorations): without them the
    /// window has no close button at all. Close does what the window
    /// manager's does (alerts.rs, close_window). The bar itself drags the
    /// window; what is clickable on it (these, the chips, Search) stops the
    /// press from reaching it.
    fn window_controls(&self, cx: &mut Context<Self>) -> AnyElement {
        let has_tray = self.has_tray();
        let control = |id: &'static str, icon: &'static str, tip: String| {
            let tip: SharedString = tip.into();
            div()
                .id(id)
                .flex_none()
                .flex()
                .items_center()
                .justify_center()
                .size(px(28.))
                .rounded_full()
                .text_color(theme::muted())
                .cursor_pointer()
                .hover(|s| s.bg(theme::surface_raised()).text_color(theme::primary()))
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                // Not the bar's drag.
                .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                .child(crate::icons::ic(icon).size(px(14.)))
        };
        let _ = cx;
        h_flex()
            .flex_none()
            .gap_1()
            .ml(px(-8.))
            .child(
                control("window-minimize", "minus", t("native.window.minimize"))
                    .on_click(|_, window, _| window.minimize_window()),
            )
            .child(
                control(
                    "window-maximize",
                    "square",
                    t(if MAXIMIZED.get() {
                        "native.window.restore"
                    } else {
                        "native.window.maximize"
                    }),
                )
                .on_click(|_, window, _| window.zoom_window()),
            )
            .child(
                control("window-close", "x", t("native.window.close")).on_click(
                    move |_, window, cx| {
                        super::alerts::close_window(
                            has_tray,
                            "the window's close button",
                            window,
                            cx,
                        );
                    },
                ),
            )
            .into_any_element()
    }

    /// search_button is the palette's way in for the pointer (TopBar's
    /// SearchButton), its shortcut on it.
    fn search_button(&self, cx: &mut Context<Self>) -> AnyElement {
        // TopBar's SearchButton: py-1 pl-2.5 pr-1.5, its ring inset (here a
        // border inside the padding).
        h_flex()
            .id("top-search")
            .flex_none()
            .gap_2()
            .rounded_full()
            .py(px(3.))
            .pl(px(9.))
            .pr(px(5.))
            .border_1()
            .border_color(theme::line())
            .text_color(theme::muted())
            .cursor_pointer()
            .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .on_click(cx.listener(|this, _: &ClickEvent, window, cx| this.open_search(window, cx)))
            .child(crate::icons::ic("search").size(px(14.)))
            .child(theme::fit(
                t("common.search"),
                theme::text::CAPTION,
                FontWeight::NORMAL,
                false,
            ))
            .child(
                div()
                    .px(px(6.))
                    .rounded_md()
                    .bg(theme::surface_raised())
                    .text_color(theme::subtle())
                    .child(theme::fit(
                        "Ctrl K",
                        theme::text::CAPTION,
                        FontWeight::NORMAL,
                        true,
                    )),
            )
            .into_any_element()
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
                    el.child(crate::app::controls::error_line(e))
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
                        .child(crate::icons::of(if failed { "⚠" } else { "◌" }))
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
                            .child(crate::motion::menu_in(div().child(p), "topbar-pop-in")),
                    )
                }))
                .into_any_element(),
        )
    }
}
