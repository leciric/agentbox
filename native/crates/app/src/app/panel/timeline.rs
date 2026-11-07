//! An agent's Timeline down the right-hand panel (components/timeline/
//! Timeline.tsx TimelinePanel, lib/timeline.ts): the player and the track
//! pinned on top, the steps listed under them along a rail. The playhead
//! starts at the first recording or screenshot (firstLook), and Play skims
//! the track; clicking a step moves the playhead to it.
use std::time::Duration;

use agentbox_api::{MediaItem, Timeline, TimelineStep};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::{AppView, clock, is_capture, loading, note, split};
use crate::media;
use crate::theme;

/// Placed is a step on the track (lib/timeline.ts Placed): where it sits
/// along it, and when it happened.
#[derive(Clone, Copy)]
struct Placed {
    from: f32,
    to: f32,
    start: i64,
    end: i64,
}

/// spanOf: a step's length on the track grows with the log of its time, so
/// a long edit doesn't swamp the quick steps around it.
fn span_of(secs: f32) -> f32 {
    20. + 60. * (1. + secs.max(0.) / 60.).log2()
}

fn media_end(m: &MediaItem) -> i64 {
    media::ms(&m.created_at) + (m.meta.duration * 1000.) as i64
}

fn layout(tl: &Timeline) -> (Vec<Placed>, f32) {
    let mut at = 0.;
    let steps = tl
        .steps
        .iter()
        .map(|s| {
            let start = media::ms(&s.start);
            let mut end = start.max(media::ms(&s.end));
            for m in &s.media {
                end = end.max(media_end(m));
            }
            let p = Placed {
                from: at,
                to: at + span_of((end - start) as f32 / 1000.),
                start,
                end,
            };
            at = p.to;
            p
        })
        .collect();
    (steps, at)
}

fn placed_at(track: &[Placed], x: f32) -> usize {
    track.iter().rposition(|p| p.from <= x).unwrap_or(0)
}

fn clock_at(track: &[Placed], x: f32) -> i64 {
    let Some(p) = track.get(placed_at(track, x)) else {
        return 0;
    };
    let f = ((x - p.from) / (p.to - p.from)).clamp(0., 1.);
    p.start + (f * (p.end - p.start) as f32) as i64
}

fn track_at(track: &[Placed], ms: i64) -> f32 {
    let Some(p) = track.iter().rev().find(|p| p.start <= ms) else {
        return 0.;
    };
    if p.end <= p.start {
        return p.from;
    }
    let f = ((ms - p.start) as f32 / (p.end - p.start) as f32).min(1.);
    p.from + f * (p.to - p.from)
}

/// first_look is where a timeline opens: its first recording or screenshot.
fn first_look(tl: &Timeline, track: &[Placed]) -> f32 {
    tl.steps
        .iter()
        .find_map(|s| s.media.iter().find(|m| is_capture(m)))
        .map_or(0., |m| track_at(track, media::ms(&m.created_at)))
}

/// clock_time is toLocaleTimeString's 2-digit hour and minute ("03:44 PM").
fn clock_time(ms: i64) -> String {
    chrono::DateTime::from_timestamp_millis(ms)
        .map(|d| {
            d.with_timezone(&chrono::Local)
                .format("%I:%M %p")
                .to_string()
        })
        .unwrap_or_default()
}

/// step_kind is a step's kind, as the timeline names them.
fn step_kind(kind: &str) -> &'static str {
    match kind {
        "task" => "task",
        "read" => "read",
        "plan" => "plan",
        "edit" => "edit",
        "test" => "test",
        "desktop" => "desktop",
        "page" => "page",
        "pr" => "pr",
        _ => "run",
    }
}

fn kind_icon(kind: &str) -> &'static str {
    match kind {
        "task" => "message-square",
        "read" => "search",
        "plan" => "list-todo",
        "edit" => "file-code",
        "test" => "flask-conical",
        "desktop" => "monitor-play",
        "page" => "file-text",
        "pr" => "git-pull-request",
        _ => "square-terminal",
    }
}

fn rgb(v: u32) -> Hsla {
    rgba((v << 8) | 0xff).into()
}

/// kind_tone is a kind's hue on the track and the lit step (kindTone).
fn kind_tone(kind: &str) -> Hsla {
    match kind {
        "task" => rgb(0xa1a1aa),
        "read" => rgb(0x64748b),
        "plan" => theme::sky(400),
        "edit" => theme::violet(400),
        "test" => theme::emerald(400),
        "desktop" => theme::amber(400),
        "page" => rgb(0xf472b6),
        "pr" => theme::brand(400),
        _ => rgb(0x06b6d4),
    }
}

/// kind_text is a kind's name's colour (kindText).
fn kind_text(kind: &str) -> Hsla {
    match kind {
        "task" => theme::secondary(),
        "read" => rgb(0x94a3b8),
        "plan" => theme::sky(400),
        "edit" => theme::violet(400),
        "test" => theme::emerald(400),
        "desktop" => theme::amber(400),
        "page" => rgb(0xf472b6),
        "pr" => theme::brand(400),
        _ => rgb(0x22d3ee),
    }
}

/// step_title says what a step did (useStepTitle).
fn step_title(s: &TimelineStep, kind: &str) -> String {
    let files = &s.paths;
    match kind {
        "task" => s.title.clone(),
        "read" if !s.title.is_empty() => s.title.clone(),
        "read" if !files.is_empty() => tv("timeline.title.read", &[("count", files.len().into())]),
        "read" => t("timeline.title.search"),
        "plan" => t("timeline.title.plan"),
        "edit" if !files.is_empty() => {
            let first = files[0].rsplit('/').next().unwrap_or(&files[0]).to_string();
            tv(
                "timeline.title.edit",
                &[
                    ("file", (&first).into()),
                    ("count", (files.len() - 1).into()),
                ],
            )
        }
        "edit" => t("timeline.title.editNone"),
        "test" => t(match s.status.as_str() {
            "failed" => "timeline.title.testFailed",
            "running" => "timeline.title.testRunning",
            _ => "timeline.title.testPassed",
        }),
        "desktop" if s.title.is_empty() => t("timeline.title.desktop"),
        "page" if s.title.is_empty() => t("timeline.title.page"),
        "pr" => t("timeline.title.pr"),
        _ if s.title.is_empty() => t("timeline.title.run"),
        _ => s.title.clone(),
    }
}

/// ring is Tailwind's ring-1: a 1px line drawn outside the box.
fn ring(color: Hsla) -> Vec<BoxShadow> {
    vec![BoxShadow {
        color,
        offset: point(px(0.), px(0.)),
        blur_radius: px(0.),
        spread_radius: px(1.),
        inset: false,
    }]
}

/// The skim speed of Play, in track units a second (Timeline.tsx's skim).
const SKIM: f32 = 12.;

impl AppView {
    pub(in crate::app) fn load_timeline(&mut self, r#ref: String, cx: &mut Context<Self>) {
        let (p, a) = split(&r#ref);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<Timeline>(&format!("/v1/agents/{p}/{a}/timeline"))
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                let fresh = this
                    .panel
                    .timeline
                    .as_ref()
                    .is_none_or(|(of, _)| of != &r#ref);
                if let Ok(tl) = &res {
                    if fresh {
                        let (track, _) = layout(tl);
                        this.panel.tl_x = first_look(tl, &track);
                        this.panel.tl_playing = false;
                    }
                    this.fetch_timeline_media(tl, cx);
                }
                this.panel.timeline = Some((r#ref, res));
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// fetch_timeline_media brings the timeline's screenshots and
    /// recordings into the cache, for the player to show.
    fn fetch_timeline_media(&mut self, tl: &Timeline, cx: &mut Context<Self>) {
        let wanted: Vec<MediaItem> = tl
            .steps
            .iter()
            .flat_map(|s| s.media.iter().filter(|m| is_capture(m)).cloned())
            .take(24)
            .collect();
        for m in wanted {
            if self.panel.files_cache.contains_key(&m.id)
                || !self.panel.fetching.insert(m.id.clone())
            {
                continue;
            }
            let client = self.client.clone();
            cx.spawn(async move |this, cx| {
                let id = m.id.clone();
                let res = media::fetch(client, m).await;
                this.update(cx, |this, cx| {
                    this.panel.fetching.remove(&id);
                    if let Ok(p) = res {
                        this.make_poster(&id, &p, cx);
                        this.panel.files_cache.insert(id, p);
                    }
                    cx.notify();
                })
                .ok();
            })
            .detach();
        }
    }

    /// play_timeline skims the playhead along the track until it ends or
    /// is paused (usePlayhead).
    fn play_timeline(&mut self, cx: &mut Context<Self>) {
        let Some((_, Ok(tl))) = &self.panel.timeline else {
            return;
        };
        let (_, length) = layout(tl);
        if self.panel.tl_playing {
            self.panel.tl_playing = false;
            cx.notify();
            return;
        }
        if self.panel.tl_x >= length {
            self.panel.tl_x = 0.;
        }
        self.panel.tl_playing = true;
        cx.spawn(async move |this, cx| {
            let tick = Duration::from_millis(33);
            loop {
                cx.background_executor().timer(tick).await;
                let more = this
                    .update(cx, |this, cx| {
                        if !this.panel.tl_playing {
                            return false;
                        }
                        let next = this.panel.tl_x + SKIM * tick.as_secs_f32();
                        this.panel.tl_x = next.min(length);
                        if next >= length {
                            this.panel.tl_playing = false;
                        }
                        cx.notify();
                        this.panel.tl_playing
                    })
                    .unwrap_or(false);
                if !more {
                    break;
                }
            }
        })
        .detach();
    }

    /// save_share saves what the agent did as one page (ShareButton): the
    /// user picks where, and the daemon's page is copied there.
    fn save_share(&mut self, r: &str, cx: &mut Context<Self>) {
        let (p, a) = split(r);
        let path = format!("/v1/agents/{p}/{a}/share");
        let name = format!(
            "{}.html",
            r.chars()
                .map(|c| if c.is_ascii_alphanumeric() || c == '_' || c == '-' {
                    c
                } else {
                    '-'
                })
                .collect::<String>()
        );
        let dir = dirs_downloads();
        let rx = cx.prompt_for_new_path(&dir, Some(&name));
        let client = self.client.clone();
        cx.spawn(async move |_, cx| {
            let Ok(Ok(Some(to))) = rx.await else { return };
            let res = match client.fetch(&path).await {
                Ok((200, _, body)) => std::fs::write(&to, &body).map_err(|e| e.to_string()),
                Ok((code, _, body)) => Err(format!("{code}: {}", String::from_utf8_lossy(&body))),
                Err(e) => Err(e.to_string()),
            };
            cx.update(|cx| match res {
                Ok(()) => {
                    let file = to
                        .file_name()
                        .map(|f| f.to_string_lossy().to_string())
                        .unwrap_or_default();
                    let dir = to.parent().map(|d| d.to_path_buf());
                    let toast = crate::toasts::Toast::new(
                        crate::toasts::Kind::Success,
                        tv("share.saved", &[("name", (&file).into())]),
                    )
                    .description(t("share.savedDetail"));
                    match dir {
                        Some(d) => toast
                            .action(t("share.show"), move |_, cx| {
                                cx.open_with_system(&d);
                            })
                            .show(cx),
                        None => toast.show(cx),
                    }
                }
                Err(e) => crate::toasts::Toast::new(crate::toasts::Kind::Error, t("share.failed"))
                    .description(e)
                    .show(cx),
            });
        })
        .detach();
    }

    /// render_timeline is TimelinePanel under its ViewTitle (RightPanel.tsx).
    pub(in crate::app) fn render_timeline(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let share_ref = r.to_string();
        // ViewTitle: h-8 border-b px-3 12px medium secondary, its icon
        // 14px subtle; the compact ShareButton (ghost, size-6) at its end.
        let title = h_flex()
            .flex_none()
            .h_8()
            .gap_1p5()
            .px_3()
            .border_b_1()
            .border_color(theme::line_faint())
            .text_size(px(12.))
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::secondary())
            .child(
                crate::icons::ic("chart-no-axes-gantt")
                    .size(px(14.))
                    .text_color(theme::subtle()),
            )
            .child(t("panel.view.timeline"))
            .child(
                div()
                    .id("timeline-share")
                    .ml_auto()
                    .size_6()
                    .flex()
                    .items_center()
                    .justify_center()
                    .rounded_lg()
                    .text_color(theme::muted())
                    .cursor_pointer()
                    .hover(|el| el.bg(theme::surface_raised()).text_color(theme::primary()))
                    .tooltip(|w, cx| {
                        gpui_component::tooltip::Tooltip::new(t("share.title")).build(w, cx)
                    })
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.save_share(&share_ref, cx)
                    }))
                    .child(crate::icons::ic("share-2").size(px(16.))),
            );
        let body = self.render_timeline_body(r, cx);
        v_flex()
            .size_full()
            .min_h_0()
            .child(title)
            .child(body)
            .into_any_element()
    }

    fn render_timeline_body(&self, r: &str, cx: &mut Context<Self>) -> AnyElement {
        let Some((of, res)) = &self.panel.timeline else {
            return loading("panel");
        };
        if of != r {
            return loading("panel");
        }
        let tl = match res {
            Ok(tl) => tl,
            Err(_) => return note(t("timeline.failedToLoad")),
        };
        if tl.steps.is_empty() {
            return note(t("timeline.empty"));
        }
        let (track, length) = layout(tl);
        let x = self.panel.tl_x.min(length);
        let active = placed_at(&track, x);
        let n = track.len();

        // The player: at most two fifths of the window's height tall, 16:10,
        // as wide as the panel's padding box allows (mx-auto max-w-[calc(40vh*1.6)]).
        let viewport = super::super::controls::viewport();
        let inner = self.panel_box().width - 1. - 24.;
        let pw = inner.min(f32::from(viewport.height) * 0.4 * 1.6).max(0.);
        let ph = pw * 10. / 16.;
        let player = self.timeline_player(tl, &track, x, active, pw, ph);

        // PlayButton: size-7 round, surface-strong, title; a 14px Play.
        let playing = self.panel.tl_playing;
        let play = div()
            .id("timeline-play")
            .flex_none()
            .size_7()
            .rounded_full()
            .flex()
            .items_center()
            .justify_center()
            .bg(theme::surface_strong())
            .text_color(theme::title())
            .cursor_pointer()
            .hover(|el| el.bg(theme::brand(500)).text_color(white()))
            .on_click(cx.listener(|this, _: &ClickEvent, _, cx| this.play_timeline(cx)))
            .child(
                crate::icons::ic(if playing { "pause" } else { "play" })
                    .size(px(14.))
                    .when(!playing, |i| i.ml(px(1.))),
            );

        // Scrubber: a segment per step in its kind's hue (the lit one full,
        // the rest at 45%), the playhead a 2px line with a 10px dot.
        let l = if length > 0. { length } else { 1. };
        let scrub_w = (pw.max(inner) - 28. - 8.).max(1.);
        let scrubber = div()
            .id("timeline-scrubber")
            .relative()
            .flex_1()
            .min_w_0()
            .h_5()
            .cursor_pointer()
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(move |this, e: &MouseDownEvent, _, cx| {
                    // The scrubber starts after the panel's left padding, the
                    // play button and the gap.
                    let left = this.widths.window - this.panel_box().width + 1. + 12. + 28. + 8.;
                    let f = ((f32::from(e.position.x) - left) / scrub_w).clamp(0., 1.);
                    this.panel.tl_x = f * l;
                    cx.notify();
                }),
            )
            .child(
                h_flex()
                    .absolute()
                    .left_0()
                    .right_0()
                    .top(px(6.))
                    .h_2()
                    .gap(px(1.))
                    .overflow_hidden()
                    .rounded(px(4.))
                    .children(track.iter().enumerate().map(|(i, p)| {
                        let s = &tl.steps[i];
                        let tone = if s.status == "failed" {
                            rgb(0xf43f5e)
                        } else {
                            kind_tone(step_kind(&s.kind))
                        };
                        div()
                            .h_full()
                            .w(relative((p.to - p.from) / l))
                            .bg(if i == active {
                                tone
                            } else {
                                tone.opacity(0.45)
                            })
                    })),
            )
            .children(tl.steps.iter().flat_map(|s| {
                s.media
                    .iter()
                    .filter(|m| m.kind == "recording")
                    .map(|m| {
                        let from = track_at(&track, media::ms(&m.created_at));
                        let to = track_at(&track, media_end(m));
                        div()
                            .absolute()
                            .bottom_0()
                            .h(px(2.))
                            .rounded(px(4.))
                            .bg(theme::rose(400))
                            .left(relative(from / l))
                            .w(relative((to - from) / l))
                    })
                    .collect::<Vec<_>>()
            }))
            .child(
                div()
                    .absolute()
                    .top_0()
                    .bottom_0()
                    .left(relative(x / l))
                    .ml(px(-1.))
                    .w(px(2.))
                    .rounded(px(4.))
                    .bg(theme::title())
                    .child(
                        div()
                            .absolute()
                            .left(px(-4.))
                            .top(px(-4.))
                            .size(px(10.))
                            .rounded_full()
                            .bg(theme::title()),
                    ),
            );

        let top = v_flex()
            .flex_none()
            .gap_2()
            .p_3()
            .border_b_1()
            .border_color(theme::line_faint())
            .child(h_flex().justify_center().child(player))
            .child(h_flex().items_center().gap_2().child(play).child(scrubber));

        // The steps, along a rail: each a 28px round icon (lit in its hue
        // under the playhead) and its title, time and meta.
        let rows = tl
            .steps
            .iter()
            .zip(track.iter())
            .enumerate()
            .map(|(i, (s, p))| {
                let kind = step_kind(&s.kind);
                let on = i == active;
                let from = p.from;
                let failed = s.status == "failed";
                let secs = (p.end - p.start) as f32 / 1000.;
                // ring-1 is drawn outside the circle and not under it: a
                // bordered box a pixel larger all round, with nothing behind
                // its middle (GPUI paints a shadow under the whole box, which
                // shows through the translucent surface).
                let mark = div()
                    .flex_none()
                    .size(px(30.))
                    .m(px(-1.))
                    .rounded_full()
                    .border_1()
                    .border_color(if on {
                        gpui::transparent_black()
                    } else {
                        theme::line()
                    })
                    .child(
                        div()
                            .size_7()
                            .rounded_full()
                            .flex()
                            .items_center()
                            .justify_center()
                            .map(|el| {
                                if on {
                                    el.bg(kind_tone(kind)).text_color(rgb(0x000000))
                                } else {
                                    el.bg(theme::surface_raised()).text_color(if failed {
                                        theme::rose(400)
                                    } else {
                                        theme::subtle()
                                    })
                                }
                            })
                            .child(crate::icons::ic(kind_icon(kind)).size(px(14.))),
                    );
                let status = match s.status.as_str() {
                    "failed" => Some((t("timeline.failed"), theme::rose(400))),
                    "passed" => Some((t("timeline.passed"), theme::emerald(400))),
                    "running" => Some((t("timeline.running"), theme::emerald(400))),
                    _ => None,
                };
                // Timeline's Meta: spans gap-1.5, text-caption, each as wide
                // as Chromium sets it.
                let cap = theme::text::CAPTION;
                let fit = |text: String| theme::fit(text, cap, FontWeight::NORMAL, false);
                let meta = h_flex()
                    .min_w_0()
                    .gap_1p5()
                    .text_size(cap)
                    .line_height(px(18.))
                    .text_color(theme::faint())
                    .child(fit(t(&format!("timeline.kind.{kind}"))).text_color(kind_text(kind)))
                    .when(secs >= 1., |el| {
                        el.child(fit(format!("· {}", clock(secs as f64))))
                    })
                    .when(kind == "edit" && (s.added > 0 || s.removed > 0), |el| {
                        // One span, "· +a −r", its numbers in their colours.
                        let (add, del) = (format!("+{}", s.added), format!("−{}", s.removed));
                        let text = format!("· {add} {del}");
                        let a0 = "· ".len();
                        let d0 = a0 + add.len() + 1;
                        let w = theme::fit_width(&text, cap, FontWeight::NORMAL, false);
                        let colour = |c: Hsla| HighlightStyle {
                            color: Some(c),
                            ..Default::default()
                        };
                        el.child(
                            div()
                                .flex_none()
                                .whitespace_nowrap()
                                .when_some(w, |el, w| el.w(px(w)))
                                .child(StyledText::new(text.clone()).with_highlights([
                                    (a0..a0 + add.len(), colour(theme::emerald(400))),
                                    (d0..text.len(), colour(theme::rose(400))),
                                ])),
                        )
                    })
                    .when_some(status, |el, (text, color)| {
                        el.child(fit(format!("· {text}")).text_color(color))
                    });
                let title = div()
                    .min_w_0()
                    .flex_1()
                    .truncate()
                    .text_color(if on {
                        theme::title()
                    } else {
                        theme::secondary()
                    })
                    .map(|el| {
                        if kind == "run" {
                            el.font_family(theme::mono()).text_size(px(12.))
                        } else {
                            el.text_size(px(13.))
                        }
                    })
                    .child(step_title(s, kind));
                div()
                    .id(("timeline-step", i))
                    .relative()
                    .flex()
                    .gap(px(10.))
                    .pb_2()
                    // py-1, a 19.5px title, an 18px meta and pb-2: Chromium keeps
                    // the half pixel, which adds up down the list, and GPUI's
                    // layout takes whole ones: 54 and 53 in turn.
                    .min_h(px(if i % 2 == 0 { 54. } else { 53. }))
                    .cursor_pointer()
                    .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                        this.panel.tl_x = from;
                        cx.notify();
                    }))
                    .when(i + 1 < n, |el| {
                        el.child(
                            div()
                                .absolute()
                                .left(px(13.))
                                .top(px(28.))
                                .bottom_0()
                                .w(px(1.))
                                .bg(theme::line()),
                        )
                    })
                    .child(mark)
                    .child(
                        v_flex()
                            .min_w_0()
                            .flex_1()
                            .rounded_lg()
                            .px_2()
                            .py_1()
                            .when(on, |el| el.bg(theme::surface()))
                            .child(
                                h_flex().items_baseline().gap_2().child(title).child(
                                    theme::fit_tabular(
                                        clock_time(p.start),
                                        theme::text::CAPTION,
                                        FontWeight::NORMAL,
                                    )
                                    .text_color(theme::faint()),
                                ),
                            )
                            .child(meta),
                    )
            });
        // The list scrolls with Chromium's thin scrollbar taking its gutter.
        let scroll = &self.panel.tl_scroll;
        let gutter = scroll.max_offset().y > px(0.5);
        let list = div()
            .relative()
            .flex_1()
            .min_h_0()
            .child(
                div()
                    .id("panel-timeline")
                    .size_full()
                    .overflow_y_scroll()
                    .track_scroll(scroll)
                    .px_3()
                    .py_2()
                    .when(gutter, |el| {
                        el.pr(px(12.) + super::super::controls::SCROLLBAR)
                    })
                    .children(rows)
                    .when(tl.working, |el| {
                        el.child(
                            h_flex()
                                .gap(px(10.))
                                .pl(px(6.))
                                .text_size(px(12.))
                                .text_color(theme::subtle())
                                .child(div().size_2().rounded_full().bg(theme::emerald(400)))
                                .child(t("timeline.working")),
                        )
                    }),
            )
            .child(super::super::controls::scrollbar(scroll, gutter));
        v_flex()
            .flex_1()
            .min_h_0()
            .child(top)
            .child(list)
            .into_any_element()
    }

    /// timeline_player is the frame at the playhead (Player): a recording's
    /// first frame, a screenshot, or the step's own text, with the clock and
    /// the step's title over a shade at its foot.
    fn timeline_player(
        &self,
        tl: &Timeline,
        track: &[Placed],
        x: f32,
        active: usize,
        w: f32,
        h: f32,
    ) -> AnyElement {
        let s = &tl.steps[active];
        let kind = step_kind(&s.kind);
        let now = clock_at(track, x);
        let recording = s.media.iter().find(|m| {
            m.kind == "recording"
                && m.meta.duration > 0.
                && now >= media::ms(&m.created_at)
                && now <= media_end(m)
        });
        let shots: Vec<&MediaItem> = s.media.iter().filter(|m| m.kind == "screenshot").collect();
        let shot = shots
            .iter()
            .rev()
            .find(|m| media::ms(&m.created_at) <= now)
            .or(shots.first())
            .copied();
        // A recording shows its first frame: the panel's video player is
        // the Evidence view's.
        let frame: AnyElement = if let Some(img_el) = recording
            .and_then(|m| self.panel.posters.get(&m.id).cloned())
            .map(|p| {
                img(ImageSource::Render(p))
                    .size_full()
                    .object_fit(ObjectFit::Contain)
            }) {
            img_el.into_any_element()
        } else if let Some(path) = shot.and_then(|m| self.panel.files_cache.get(&m.id).cloned()) {
            img(path)
                .size_full()
                .object_fit(ObjectFit::Contain)
                .into_any_element()
        } else if recording.is_some() || shot.is_some() {
            div().into_any_element()
        } else {
            // StepText: the step as a terminal would show it, in a font of
            // clamp(8px, 1.6cqw, 13px), leading-relaxed, padded 4%/3% of the width.
            let size = (w * 0.016).clamp(8., 13.);
            div()
                .absolute()
                .inset_0()
                .bg(rgb(0x0e0e14))
                .font_family(theme::mono())
                .text_size(px(size))
                .line_height(relative(1.625))
                .text_color(rgb(0xd4d4d8))
                .px(px(w * 0.04))
                .py(px(w * 0.03))
                .overflow_hidden()
                .child(
                    v_flex()
                        .gap_0p5()
                        .child(
                            div()
                                .truncate()
                                .text_color(kind_text(kind))
                                .child(format!("● {}", step_title(s, kind))),
                        )
                        .when(kind == "read", |el| {
                            el.children(s.paths.iter().take(12).map(|p| {
                                div().truncate().text_color(rgb(0x71717a)).child(p.clone())
                            }))
                        })
                        .children(s.lines.iter().map(|l| {
                            let tone = if kind == "edit" && l.starts_with("---") {
                                Some(rgb(0x71717a))
                            } else if kind == "edit" && l.starts_with('+') {
                                Some(rgb(0x6ee7b7))
                            } else if (kind == "edit" && l.starts_with('-'))
                                || (kind == "test"
                                    && (l.contains("FAIL")
                                        || l.contains("failed") && !l.contains(" 0 failed")))
                            {
                                Some(rgb(0xfda4af))
                            } else if kind == "test"
                                && (l.starts_with("ok")
                                    || l.starts_with("PASS")
                                    || l.contains("passed"))
                            {
                                Some(rgb(0x6ee7b7))
                            } else {
                                None
                            };
                            div()
                                .when_some(tone, |el, c| el.text_color(c))
                                .child(l.clone())
                        })),
                )
                .into_any_element()
        };
        let duration = recording.map(|m| m.meta.duration).unwrap_or(0.);
        let offset = recording.map_or(0., |m| {
            ((now - media::ms(&m.created_at)) as f64 / 1000.).max(0.)
        });
        div()
            .relative()
            .w(px(w))
            .h(px(h))
            .flex_none()
            .overflow_hidden()
            .rounded_lg()
            .bg(rgb(0x000000))
            .shadow(ring(theme::line()))
            .child(frame)
            .child(
                h_flex()
                    .absolute()
                    .left_0()
                    .right_0()
                    .bottom_0()
                    .gap_2()
                    .px(px(10.))
                    .pb(px(6.))
                    .pt_6()
                    .text_size(px(12.))
                    .line_height(px(18.))
                    .text_color(white().opacity(0.85))
                    .bg(linear_gradient(
                        0.,
                        linear_color_stop(black().opacity(0.75), 0.),
                        linear_color_stop(black().opacity(0.), 1.),
                    ))
                    .when(recording.is_some(), |el| {
                        el.child(
                            h_flex()
                                .gap_1()
                                .child(div().size_1p5().rounded_full().bg(rgb(0xf43f5e)))
                                .child(t("timeline.rec")),
                        )
                    })
                    .child(clock_time(now))
                    .child(
                        div()
                            .min_w_0()
                            .truncate()
                            .text_color(white().opacity(0.6))
                            .child(step_title(s, kind)),
                    )
                    .when(recording.is_some(), |el| {
                        el.child(div().ml_auto().flex_none().child(format!(
                            "{} / {}",
                            clock(offset),
                            clock(duration)
                        )))
                    }),
            )
            .into_any_element()
    }
}

/// dirs_downloads is where a saved page goes first: ~/Downloads.
fn dirs_downloads() -> std::path::PathBuf {
    let home = std::env::var_os("HOME")
        .or_else(|| std::env::var_os("USERPROFILE"))
        .map(std::path::PathBuf::from)
        .unwrap_or_default();
    let d = home.join("Downloads");
    if d.is_dir() { d } else { home }
}
