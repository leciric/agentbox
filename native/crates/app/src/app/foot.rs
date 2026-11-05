//! The sidebar foot's pages across every project: Media (media/MediaView.tsx,
//! every agent's screenshots, recordings, reports and notes, newest first,
//! by project, unseen or favorites, each opened in the app's own viewer) and Jobs (JobsView.tsx: what the daemon
//! ran, each with its log, a running one cancelled and rolled back).
use std::collections::HashMap;
use std::path::PathBuf;
use std::rc::Rc;

use agentbox_api::client::Method;
use agentbox_api::{Job, MediaItem, MediaPage};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{self, Btn, Choice};
use super::sidebar::ago;
use crate::{media, theme, toasts};

#[derive(Clone, Copy, PartialEq)]
pub enum FootPage {
    Media,
    Jobs,
}

/// What the pages read.
#[derive(Default)]
pub struct Foot {
    media: Vec<MediaItem>,
    next: String,
    project: String,
    unseen: bool,
    favorites: bool,
    /// Screenshots fetched to disk, by media id.
    files: HashMap<String, PathBuf>,
    loaded: bool,
    jobs: Vec<Job>,
    job: Option<String>,
    log: String,
    /// The item open in the viewer, over the page.
    viewing: Option<Viewing>,
}

/// ViewerClick is what a button over the viewer does.
type ViewerClick = Box<dyn Fn(&mut AppView, &mut Window, &mut Context<AppView>)>;

/// Viewing is an item shown in the app (MediaParts.tsx MediaViewer): a
/// screenshot as itself, a recording in the player, a report or a note read.
struct Viewing {
    item: MediaItem,
    path: Option<PathBuf>,
    text: Option<String>,
    error: Option<String>,
    #[cfg(feature = "video")]
    player: Option<Entity<crate::video::VideoPlayer>>,
    focus: FocusHandle,
}

const PAGE: usize = 40;

impl AppView {
    pub(super) fn open_foot_page(
        &mut self,
        page: FootPage,
        _: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.settings = false;
        self.work = None;
        self.side.page = Some(page);
        match page {
            FootPage::Media => self.load_all_media(false, cx),
            FootPage::Jobs => self.load_jobs(cx),
        }
        cx.notify();
    }

    fn load_all_media(&mut self, more: bool, cx: &mut Context<Self>) {
        let f = &self.side.foot;
        let mut q = vec![format!("limit={PAGE}")];
        if !f.project.is_empty() {
            q.push(format!("project={}", f.project));
        }
        if f.unseen {
            q.push("unseen=1".into());
        }
        if f.favorites {
            q.push("favorite=1".into());
        }
        if more && !f.next.is_empty() {
            q.push(format!("cursor={}", f.next));
        }
        let path = format!("/v1/media?{}", q.join("&"));
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<MediaPage>(&path).await;
            this.update(cx, |this, cx| {
                let f = &mut this.side.foot;
                f.loaded = true;
                match res {
                    Ok(p) => {
                        if !more {
                            f.media.clear();
                        }
                        f.media.extend(p.items);
                        f.next = p.next;
                        this.fetch_thumbs(cx);
                    }
                    Err(e) => toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// fetch_thumbs brings the screenshots' files to disk to draw them.
    fn fetch_thumbs(&mut self, cx: &mut Context<Self>) {
        for m in &self.side.foot.media {
            if m.kind != "screenshot" || self.side.foot.files.contains_key(&m.id) {
                continue;
            }
            self.side.foot.files.insert(m.id.clone(), PathBuf::new());
            let (client, item) = (self.client.clone(), m.clone());
            cx.spawn(async move |this, cx| {
                let id = item.id.clone();
                if let Ok(p) = media::fetch(client, item).await {
                    this.update(cx, |this, cx| {
                        this.side.foot.files.insert(id, p);
                        cx.notify();
                    })
                    .ok();
                }
            })
            .detach();
        }
    }

    /// open_media shows an item in the viewer over the page; its file comes
    /// from the daemon's cache on disk.
    fn open_media(&mut self, item: MediaItem, window: &mut Window, cx: &mut Context<Self>) {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let text = (!item.text.is_empty()).then(|| item.text.clone());
        self.side.foot.viewing = Some(Viewing {
            item: item.clone(),
            path: None,
            text,
            error: None,
            #[cfg(feature = "video")]
            player: None,
            focus,
        });
        cx.notify();
        let client = self.client.clone();
        let id = item.id.clone();
        cx.spawn(async move |this, cx| {
            let res = media::fetch(client, item.clone()).await;
            // A report or a note is read as text.
            let text = match &res {
                Ok(p) if (item.kind == "report" || item.kind == "note") && item.text.is_empty() => {
                    std::fs::read_to_string(p).ok()
                }
                _ => None,
            };
            this.update(cx, |this, cx| {
                let Some(v) = this.side.foot.viewing.as_mut().filter(|v| v.item.id == id) else {
                    return;
                };
                match res {
                    Ok(p) => {
                        #[cfg(feature = "video")]
                        if v.item.kind == "recording" {
                            let path = p.clone();
                            v.player = Some(cx.new(|cx| {
                                let mut pl = crate::video::VideoPlayer::new(path, cx);
                                pl.play(cx);
                                pl
                            }));
                        }
                        v.path = Some(p);
                        if text.is_some() {
                            v.text = text;
                        }
                    }
                    Err(e) => v.error = Some(e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// step_media opens the item before or after in the page's list.
    fn step_media(&mut self, by: isize, window: &mut Window, cx: &mut Context<Self>) {
        let Some(v) = &self.side.foot.viewing else {
            return;
        };
        let list = &self.side.foot.media;
        let Some(i) = list.iter().position(|m| m.id == v.item.id) else {
            return;
        };
        let j = i as isize + by;
        if j < 0 || j as usize >= list.len() {
            return;
        }
        let next = list[j as usize].clone();
        self.open_media(next, window, cx);
    }

    fn close_media(&mut self, cx: &mut Context<Self>) {
        self.side.foot.viewing = None;
        cx.notify();
    }

    /// render_viewer is the open item over the page, with its name, where
    /// it's from and when, the arrows to the next, and its own viewer.
    fn render_media_viewer(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let v = self.side.foot.viewing.as_ref()?;
        let m = &v.item;
        let list = &self.side.foot.media;
        let at = list.iter().position(|x| x.id == m.id);
        let first = at.is_none_or(|i| i == 0);
        let last = at.is_none_or(|i| i + 1 >= list.len());
        let who = if m.agent_title.is_empty() {
            m.agent_name.clone()
        } else {
            m.agent_title.clone()
        };
        let from = t(if m.source == "agent" {
            "agent.mediaTab.fromAgent"
        } else {
            "agent.mediaTab.fromYou"
        });
        let sub = [who, from, crate::dates::date_time_of(&m.created_at)]
            .into_iter()
            .filter(|s| !s.is_empty())
            .collect::<Vec<_>>()
            .join(" · ");
        let icon = |id: &'static str,
                    label: &'static str,
                    tip: String,
                    disabled: bool,
                    click: ViewerClick| {
            div()
                .id(id)
                .px_2()
                .py_1()
                .rounded_md()
                .text_color(if disabled {
                    theme::faint()
                } else {
                    theme::muted()
                })
                .when(!disabled, |el| {
                    el.cursor_pointer()
                        .hover(|s| s.bg(theme::hover()).text_color(theme::fg()))
                        .on_click(
                            cx.listener(move |this, _: &ClickEvent, w, cx| click(this, w, cx)),
                        )
                })
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                .child(label)
        };
        let body: AnyElement = if let Some(e) = v.error.as_ref().filter(|_| v.text.is_none()) {
            div()
                .text_color(theme::error())
                .child(e.clone())
                .into_any_element()
        } else if m.kind == "screenshot" {
            match &v.path {
                Some(p) => img(p.clone())
                    .size_full()
                    .object_fit(ObjectFit::Contain)
                    .into_any_element(),
                None => div()
                    .text_color(theme::faint())
                    .child(t("common.loading"))
                    .into_any_element(),
            }
        } else if m.kind == "recording" {
            self.viewer_player(v)
        } else {
            match &v.text {
                Some(text) => div()
                    .id("media-viewer-text")
                    .size_full()
                    .overflow_y_scroll()
                    .p_6()
                    .rounded_lg()
                    .bg(theme::panel())
                    .text_sm()
                    .child(gpui_component::text::TextView::markdown(
                        SharedString::from(format!("media-text-{}", m.id)),
                        text.clone(),
                    ))
                    .into_any_element(),
                None => div()
                    .text_color(theme::faint())
                    .child(t("common.loading"))
                    .into_any_element(),
            }
        };
        let path = v.path.clone();
        Some(
            v_flex()
                .id("media-viewer")
                .absolute()
                .inset_0()
                .bg(theme::bg())
                .track_focus(&v.focus)
                .on_key_down(cx.listener(|this, e: &KeyDownEvent, window, cx| {
                    match e.keystroke.key.as_str() {
                        "escape" => this.close_media(cx),
                        "left" => this.step_media(-1, window, cx),
                        "right" => this.step_media(1, window, cx),
                        _ => return,
                    }
                    cx.stop_propagation();
                }))
                .child(
                    h_flex()
                        .px_4()
                        .py_2()
                        .gap_2()
                        .border_b_1()
                        .border_color(theme::border())
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .child(
                                    div()
                                        .truncate()
                                        .text_sm()
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::fg())
                                        .child(media::readable(&m.name)),
                                )
                                .child(
                                    div()
                                        .truncate()
                                        .text_xs()
                                        .text_color(theme::faint())
                                        .child(sub),
                                ),
                        )
                        .child(icon(
                            "media-prev",
                            "‹",
                            t("agent.mediaTab.previous"),
                            first,
                            Box::new(|this, w, cx| this.step_media(-1, w, cx)),
                        ))
                        .child(icon(
                            "media-next",
                            "›",
                            t("common.next"),
                            last,
                            Box::new(|this, w, cx| this.step_media(1, w, cx)),
                        ))
                        .child(icon(
                            "media-system",
                            "↗",
                            t("agent.mediaTab.openDefault"),
                            path.is_none(),
                            Box::new(move |_, _, cx| {
                                if let Some(p) = &path {
                                    cx.open_with_system(p);
                                }
                            }),
                        ))
                        .child(icon(
                            "media-close",
                            "✕",
                            t("common.close"),
                            false,
                            Box::new(|this, _, cx| this.close_media(cx)),
                        )),
                )
                .child(
                    div()
                        .flex_1()
                        .min_h_0()
                        .p_4()
                        .flex()
                        .items_center()
                        .justify_center()
                        .child(body),
                )
                .into_any_element(),
        )
    }

    #[cfg(feature = "video")]
    fn viewer_player(&self, v: &Viewing) -> AnyElement {
        match &v.player {
            Some(p) => div().size_full().child(p.clone()).into_any_element(),
            None => div()
                .text_color(theme::faint())
                .child(t("common.loading"))
                .into_any_element(),
        }
    }

    #[cfg(not(feature = "video"))]
    fn viewer_player(&self, _v: &Viewing) -> AnyElement {
        div()
            .text_color(theme::faint())
            .child(t("native.video.unavailable"))
            .into_any_element()
    }

    fn load_jobs(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let jobs = client.get::<Vec<Job>>("/v1/jobs").await.unwrap_or_default();
            this.update(cx, |this, cx| {
                this.side.foot.jobs = jobs;
                this.side.foot.loaded = true;
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn pick_job(&mut self, id: String, cx: &mut Context<Self>) {
        self.side.foot.job = Some(id.clone());
        self.side.foot.log.clear();
        cx.notify();
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let log = client
                .raw(Method::GET, &format!("/v1/jobs/{id}/log"), None)
                .await
                .map(|b| String::from_utf8_lossy(&b).into_owned())
                .unwrap_or_else(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.side.foot.job.as_deref() == Some(id.as_str()) {
                    this.side.foot.log = log;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn cancel_job(&mut self, id: String, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .raw(Method::POST, &format!("/v1/jobs/{id}/cancel"), None)
                .await;
            this.update(cx, |this, cx| {
                if let Err(e) = res {
                    toasts::error(cx, e.to_string());
                }
                this.load_jobs(cx);
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn render_foot_page(&self, cx: &mut Context<Self>) -> AnyElement {
        match self.side.page {
            Some(FootPage::Jobs) => self.render_jobs(cx),
            _ => div()
                .relative()
                .size_full()
                .child(self.render_media_page(cx))
                .children(self.render_media_viewer(cx))
                .into_any_element(),
        }
    }

    fn render_media_page(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        let mut choices = vec![Choice::new("", t("shell.allMedia.allProjects"))];
        choices.extend(
            self.projects
                .iter()
                .map(|p| Choice::new(p.name.clone(), super::sidebar_actions::label_of(p))),
        );
        let view = cx.entity().downgrade();
        let chip = |id: &'static str, label: String, on: bool| {
            div()
                .id(id)
                .px_2p5()
                .py_1()
                .rounded_full()
                .border_1()
                .border_color(if on { theme::muted() } else { theme::border() })
                .when(on, |el| el.bg(theme::hover()))
                .text_xs()
                .text_color(if on { theme::fg() } else { theme::muted() })
                .cursor_pointer()
                .child(label)
        };
        let cards = f.media.iter().map(|m| {
            let item = m.clone();
            let file = f
                .files
                .get(&m.id)
                .filter(|p| !p.as_os_str().is_empty())
                .cloned();
            let glyph = match m.kind.as_str() {
                "recording" => "▶",
                "report" => "▤",
                "note" => "✎",
                _ => "▣",
            };
            let who = if m.agent_title.is_empty() {
                m.agent_name.clone()
            } else {
                m.agent_title.clone()
            };
            v_flex()
                .id(SharedString::from(format!("media-{}", m.id)))
                .w(px(220.))
                .rounded_lg()
                .overflow_hidden()
                .border_1()
                .border_color(theme::border())
                .bg(theme::raised())
                .cursor_pointer()
                .hover(|el| el.border_color(theme::muted()))
                .on_click(cx.listener(move |this, _: &ClickEvent, window, cx| {
                    this.open_media(item.clone(), window, cx)
                }))
                .child(
                    div()
                        .h(px(124.))
                        .bg(theme::panel())
                        .flex()
                        .items_center()
                        .justify_center()
                        .text_2xl()
                        .text_color(theme::faint())
                        .map(|el| match file {
                            Some(p) => el.child(img(p).size_full().object_fit(ObjectFit::Cover)),
                            None => el.child(glyph),
                        }),
                )
                .child(
                    v_flex()
                        .p_2()
                        .gap_0p5()
                        .child(
                            h_flex()
                                .gap_1()
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .truncate()
                                        .text_sm()
                                        .text_color(theme::fg())
                                        .child(media::readable(&m.name)),
                                )
                                .when(m.unseen, |el| {
                                    el.child(controls::badge(
                                        t("shell.allMedia.new"),
                                        theme::accent(),
                                    ))
                                })
                                .when(m.favorite, |el| el.child("★")),
                        )
                        .child(
                            div()
                                .text_xs()
                                .truncate()
                                .text_color(theme::faint())
                                .child(format!("{who} · {}", ago(&m.created_at))),
                        ),
                )
        });
        let empty = f.loaded && f.media.is_empty();
        v_flex()
            .id("media-page")
            .size_full()
            .overflow_y_scroll()
            .p_6()
            .gap_4()
            .child(
                v_flex()
                    .gap_1()
                    .child(
                        div()
                            .text_xl()
                            .font_weight(FontWeight::SEMIBOLD)
                            .child(t("shell.nav.media")),
                    )
                    .child(
                        div()
                            .text_sm()
                            .text_color(theme::muted())
                            .child(t("media.view.subtitle")),
                    ),
            )
            .child(
                h_flex()
                    .gap_2()
                    .flex_wrap()
                    .child(div().w(px(220.)).child(controls::select_at(
                        "media-project",
                        choices,
                        &f.project,
                        false,
                        Rc::new(move |v: &str, _, cx| {
                            let v = v.to_string();
                            view.update(cx, |this, cx| {
                                this.side.foot.project = v;
                                this.load_all_media(false, cx);
                            })
                            .ok();
                        }),
                        Anchor::TopLeft,
                    )))
                    .child(
                        chip("media-unseen", t("shell.allMedia.unseen"), f.unseen).on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.side.foot.unseen = !this.side.foot.unseen;
                                this.load_all_media(false, cx);
                            }),
                        ),
                    )
                    .child(
                        chip("media-fav", t("shell.allMedia.favorites"), f.favorites).on_click(
                            cx.listener(|this, _: &ClickEvent, _, cx| {
                                this.side.foot.favorites = !this.side.foot.favorites;
                                this.load_all_media(false, cx);
                            }),
                        ),
                    ),
            )
            .when(empty, |el| {
                el.child(
                    v_flex()
                        .py_12()
                        .items_center()
                        .gap_2()
                        .child(
                            div()
                                .text_color(theme::fg())
                                .child(t("project.media.emptyTitle")),
                        )
                        .child(
                            div()
                                .max_w(px(420.))
                                .text_sm()
                                .text_center()
                                .text_color(theme::muted())
                                .child(t("shell.allMedia.emptyBody")),
                        ),
                )
            })
            .child(h_flex().flex_wrap().gap_3().children(cards))
            .when(!f.next.is_empty(), |el| {
                el.child(h_flex().child(controls::button(
                    "media-more",
                    t("common.more"),
                    Btn::Secondary,
                    false,
                    cx.listener(|this, _: &ClickEvent, _, cx| this.load_all_media(true, cx)),
                )))
            })
            .into_any_element()
    }

    fn render_jobs(&self, cx: &mut Context<Self>) -> AnyElement {
        let f = &self.side.foot;
        if f.loaded && f.jobs.is_empty() {
            return v_flex()
                .size_full()
                .items_center()
                .justify_center()
                .gap_2()
                .child(
                    div()
                        .text_color(theme::fg())
                        .child(t("shell.jobs.emptyTitle")),
                )
                .child(
                    div()
                        .max_w(px(420.))
                        .text_sm()
                        .text_center()
                        .text_color(theme::muted())
                        .child(t("shell.jobs.emptyBody")),
                )
                .into_any_element();
        }
        let current = f
            .job
            .as_ref()
            .and_then(|id| f.jobs.iter().find(|j| &j.id == id));
        let card = |title: String| {
            v_flex()
                .flex_1()
                .min_w_0()
                .min_h_0()
                .rounded_xl()
                .border_1()
                .border_color(theme::border())
                .bg(theme::panel())
                .child(
                    div()
                        .px_4()
                        .py_3()
                        .text_sm()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(title),
                )
        };
        let rows = f.jobs.iter().map(|j| {
            let id = j.id.clone();
            let on = f.job.as_deref() == Some(j.id.as_str());
            h_flex()
                .id(SharedString::from(format!("job-{}", j.id)))
                .mx_2()
                .px_3()
                .py_2()
                .gap_3()
                .rounded_lg()
                .text_sm()
                .cursor_pointer()
                .when(on, |el| el.bg(theme::hover()))
                .hover(|el| el.bg(theme::raised()))
                .on_click(
                    cx.listener(move |this, _: &ClickEvent, _, cx| this.pick_job(id.clone(), cx)),
                )
                .child(status_badge(&j.status))
                .child(div().font_weight(FontWeight::MEDIUM).child(j.kind.clone()))
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::faint())
                        .child(j.target.clone()),
                )
                .child(
                    div()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(ago(&j.created_at)),
                )
                .child(
                    div()
                        .w(px(56.))
                        .text_right()
                        .text_xs()
                        .font_family(theme::MONO)
                        .text_color(theme::faint())
                        .child(duration(&j.created_at, j.finished_at.as_deref())),
                )
        });
        h_flex()
            .size_full()
            .p_6()
            .gap_4()
            .items_start()
            .child(
                card(t("shell.jobs.recent")).h_full().child(
                    div()
                        .id("jobs-list")
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .pb_2()
                        .child(v_flex().gap_0p5().children(rows)),
                ),
            )
            .child(
                card(match current {
                    Some(j) => format!("{} {}", j.kind, j.target),
                    None => t("shell.jobs.log"),
                })
                .h_full()
                .when_some(current, |el, j| {
                    let id = j.id.clone();
                    el.child(
                        h_flex()
                            .px_4()
                            .pb_2()
                            .gap_2()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(tv(
                                "shell.jobs.jobId",
                                &[
                                    ("id", (&j.id).into()),
                                    (
                                        "duration",
                                        duration(&j.created_at, j.finished_at.as_deref()).into(),
                                    ),
                                ],
                            ))
                            .child(div().flex_1())
                            .map(|el| {
                                if j.status == "running" {
                                    el.child(controls::button(
                                        "job-cancel",
                                        t("shell.jobs.cancelRollback"),
                                        Btn::Secondary,
                                        false,
                                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                                            this.cancel_job(id.clone(), cx)
                                        }),
                                    ))
                                } else {
                                    el.child(status_badge(&j.status))
                                }
                            }),
                    )
                })
                .child(
                    div()
                        .id("job-log")
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .px_4()
                        .pb_4()
                        .text_xs()
                        .font_family(theme::MONO)
                        .text_color(theme::muted())
                        .child(if f.job.is_some() {
                            f.log.clone()
                        } else {
                            t("shell.jobs.pick")
                        }),
                ),
            )
            .into_any_element()
    }
}

fn status_badge(status: &str) -> AnyElement {
    let color = match status {
        "succeeded" | "done" => theme::running(),
        "failed" => theme::error(),
        "running" | "queued" => theme::starting(),
        _ => theme::muted(),
    };
    controls::badge(status.to_string(), color)
}

/// duration is how long a job took, or has taken: 42s, 3m 5s.
fn duration(from: &str, to: Option<&str>) -> String {
    let start = media::ms(from);
    let end = to
        .map(media::ms)
        .unwrap_or_else(|| chrono::Utc::now().timestamp_millis());
    if start == 0 || end < start {
        return String::new();
    }
    let s = (end - start) / 1000;
    if s < 60 {
        format!("{s}s")
    } else if s < 3600 {
        format!("{}m {}s", s / 60, s % 60)
    } else {
        format!("{}h {}m", s / 3600, (s % 3600) / 60)
    }
}
