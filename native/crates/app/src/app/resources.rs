//! The panel's Resources view of an agent (components/v2/AgentResources.tsx):
//! its machine's CPU, memory and disk, live, each with the last ten minutes
//! as a sparkline. The daemon samples the agent only while a window shows
//! this view: the window's presence lease asks for it (presence.rs), and the
//! samples come as agent-resources events onto the history read when it
//! opened. A sample taken while nobody watched doesn't exist, so a gap in the
//! line is time the view was closed.
use agentbox_api::{AgentResourceHistory, AgentResourcePoint, AgentResourcesEvent};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::agent::split;
use crate::files::human_bytes;
use crate::{media, theme};

/// The agent's history, as read and then grown by its events.
#[derive(Default)]
pub(super) struct Resources {
    of: String,
    history: Option<Result<AgentResourceHistory, String>>,
}

/// A stretch of samples taken without a break, each as (ms, value).
type Run = Vec<(i64, f64)>;

/// Two samples further apart than this weren't watched in between.
const GAP_MS: i64 = 6_000;

/// runs splits a series into the stretches sampled without a break, each
/// point as (ms, value), keeping only what falls in the window from start
/// (resourceLine.ts runs).
pub(super) fn runs(values: &[(i64, f64)], start: i64, window_ms: i64) -> Vec<Run> {
    let mut out: Vec<Run> = Vec::new();
    let mut prev = i64::MIN;
    for &(ms, v) in values {
        if ms < start || ms > start + window_ms {
            continue;
        }
        if out.is_empty() || ms.saturating_sub(prev) > GAP_MS {
            out.push(Vec::new());
        }
        out.last_mut().unwrap().push((ms, v));
        prev = ms;
    }
    out.retain(|r| r.len() > 1);
    out
}

/// append adds a live sample to a history, dropping what falls out of its
/// window (events.ts appendResourcePoint).
pub(super) fn append(h: &mut AgentResourceHistory, point: AgentResourcePoint) {
    let at = media::ms(&point.at);
    if h.points.last().is_some_and(|l| media::ms(&l.at) >= at) {
        return;
    }
    let cut = at - h.window_seconds * 1000;
    h.points.retain(|p| media::ms(&p.at) > cut);
    h.points.push(point);
}

fn human_rate(n: i64) -> String {
    format!("{}/s", human_bytes(n))
}

impl AppView {
    /// load_resources reads the agent's history, keeping what was shown of
    /// it until the answer comes.
    pub(super) fn load_resources(&mut self, r#ref: String, cx: &mut Context<Self>) {
        if self.resources.of != r#ref {
            self.resources.of = r#ref.clone();
            self.resources.history = None;
        }
        let (p, a) = split(&r#ref);
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client
                .get::<AgentResourceHistory>(&format!(
                    "/v1/agents/{}/{}/resources",
                    crate::files::encode(&p),
                    crate::files::encode(&a)
                ))
                .await
                .map_err(|e| e.to_string());
            this.update(cx, |this, cx| {
                if this.resources.of == r#ref {
                    this.resources.history = Some(res);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// on_resources_event puts a live sample on the history shown.
    pub(super) fn on_resources_event(&mut self, data: serde_json::Value, cx: &mut Context<Self>) {
        let Ok(ev) = serde_json::from_value::<AgentResourcesEvent>(data) else {
            return;
        };
        if ev.r#ref != self.resources.of {
            return;
        }
        if let Some(Ok(h)) = &mut self.resources.history {
            append(h, ev.point);
            cx.notify();
        }
    }

    /// reload_resources reads the history again after a reconnect, if shown.
    pub(super) fn reload_resources(&mut self, cx: &mut Context<Self>) {
        if let Some(r) = self.resources_shown() {
            self.load_resources(r, cx);
        }
    }

    pub(super) fn render_resources(&self, r: &str) -> AnyElement {
        let empty = AgentResourceHistory::default();
        let (h, failed) = match &self.resources.history {
            Some(Ok(h)) if self.resources.of == r => (h, false),
            Some(Err(_)) if self.resources.of == r => (&empty, true),
            _ => (&empty, false),
        };
        let points = &h.points;
        let last = points.last();
        let window_ms = if h.window_seconds > 0 {
            h.window_seconds * 1000
        } else {
            600_000
        };
        let end = last.map_or_else(
            || chrono::Utc::now().timestamp_millis(),
            |l| media::ms(&l.at),
        );
        let minutes = (window_ms as f64 / 60_000.).round() as i64;
        let series = |f: fn(&AgentResourcePoint) -> f64| -> Vec<(i64, f64)> {
            points.iter().map(|p| (media::ms(&p.at), f(p))).collect()
        };
        let line = |values, color| Series { values, color };
        let dash = || "—".to_string();
        let caption = if points.is_empty() && !failed {
            t("resources.waiting")
        } else {
            tv("resources.note", &[("minutes", minutes.into())])
        };
        div()
            .id("panel-resources")
            .size_full()
            .overflow_y_scroll()
            .p_3()
            .child(
                v_flex()
                    .gap_3()
                    .child(metric(Metric {
                        id: "cpu",
                        label: t("resources.cpu"),
                        value: last.map_or_else(dash, |l| format!("{:.0}%", l.cpu)),
                        detail: None,
                        series: vec![line(series(|p| p.cpu), theme::brand(300))],
                        legend: Vec::new(),
                        floor: 100.,
                        end,
                        window_ms,
                    }))
                    .child(metric(Metric {
                        id: "memory",
                        label: t("resources.memory"),
                        value: last.map_or_else(dash, |l| human_bytes(l.memory)),
                        detail: last
                            .filter(|l| l.swap > 0)
                            .map(|l| tv("resources.swap", &[("size", human_bytes(l.swap).into())])),
                        series: vec![line(series(|p| p.memory as f64), theme::emerald(300))],
                        legend: Vec::new(),
                        floor: 512. * 1024. * 1024.,
                        end,
                        window_ms,
                    }))
                    .child(metric(Metric {
                        id: "disk",
                        label: t("resources.disk"),
                        value: last.map_or_else(dash, |l| {
                            format!(
                                "{} {} · {} {}",
                                t("resources.read"),
                                human_rate(l.disk_read),
                                t("resources.write"),
                                human_rate(l.disk_write)
                            )
                        }),
                        detail: None,
                        series: vec![
                            line(series(|p| p.disk_read as f64), theme::brand(300)),
                            line(series(|p| p.disk_write as f64), theme::amber(300)),
                        ],
                        legend: vec![
                            (t("resources.read"), theme::brand(300)),
                            (t("resources.write"), theme::amber(300)),
                        ],
                        floor: 1024. * 1024.,
                        end,
                        window_ms,
                    }))
                    .child(
                        div()
                            .px_1()
                            .text_xs()
                            .text_color(theme::subtle())
                            .child(caption),
                    ),
            )
            .into_any_element()
    }
}

struct Series {
    values: Vec<(i64, f64)>,
    color: Hsla,
}

struct Metric {
    id: &'static str,
    label: String,
    value: String,
    detail: Option<String>,
    series: Vec<Series>,
    legend: Vec<(String, Hsla)>,
    floor: f64,
    end: i64,
    window_ms: i64,
}

/// metric is one measure's card: its name, its value now, and its line.
fn metric(m: Metric) -> AnyElement {
    v_flex()
        .id(SharedString::from(format!("resource-{}", m.id)))
        .rounded_xl()
        .border_1()
        .border_color(theme::line())
        .bg(theme::surface_faint())
        .px_3()
        .py_2p5()
        .child(
            h_flex()
                .min_w_0()
                // Centred in one fixed line: on a baseline the legend sat
                // 5px under the title and made its card taller.
                .h(px(20.))
                .items_center()
                .gap_2()
                .child(
                    div()
                        .text_sm()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::title())
                        .child(m.label),
                )
                .when(!m.legend.is_empty(), |el| {
                    el.child(
                        h_flex()
                            .gap_2()
                            .text_xs()
                            .text_color(theme::subtle())
                            .children(m.legend.into_iter().map(|(label, color)| {
                                h_flex()
                                    .items_center()
                                    .gap_1()
                                    .child(div().size(px(6.)).rounded_full().bg(color))
                                    .child(label)
                            })),
                    )
                })
                .child(
                    div()
                        .ml_auto()
                        .min_w_0()
                        .truncate()
                        .font_family(theme::mono())
                        .text_sm()
                        .text_color(theme::secondary())
                        .child(m.value),
                ),
        )
        // The line under it is kept when a metric has nothing to say there,
        // so the cards are one height.
        .child(
            div()
                .h(px(16.))
                .flex()
                .justify_end()
                .text_xs()
                .line_height(px(16.))
                .text_color(theme::subtle())
                .children(m.detail),
        )
        .child(trend_line(m.series, m.floor, m.end, m.window_ms))
        .into_any_element()
}

/// trend_line draws each series over the window ending at end, placed by
/// time, so a stretch nobody watched shows as a break rather than being
/// squeezed out. Its scale is the largest value shown, never under floor, so
/// an idle agent's line lies flat at the bottom instead of magnifying noise.
/// A lone series is shaded under its line.
fn trend_line(series: Vec<Series>, floor: f64, end: i64, window_ms: i64) -> impl IntoElement {
    let start = end - window_ms;
    let max = series
        .iter()
        .flat_map(|s| s.values.iter().map(|&(_, v)| v))
        .fold(floor, f64::max);
    let baseline = theme::line();
    let shade = series.len() == 1;
    let runs: Vec<(Vec<Run>, Hsla)> = series
        .iter()
        .map(|s| (runs(&s.values, start, window_ms), s.color))
        .collect();
    canvas(
        |_, _, _| {},
        move |b, _, window, _| {
            let h = f64::from(b.size.height);
            let w = f64::from(b.size.width);
            let at = |ms: i64, v: f64| {
                point(
                    b.origin.x + px((((ms - start) as f64 / window_ms as f64) * w) as f32),
                    b.origin.y + px((h - 1. - (v.max(0.) / max) * (h - 3.)) as f32),
                )
            };
            let bottom = b.origin.y + b.size.height;
            let mut base = PathBuilder::stroke(px(1.));
            base.move_to(point(b.origin.x, bottom - px(0.5)));
            base.line_to(point(b.origin.x + b.size.width, bottom - px(0.5)));
            if let Ok(p) = base.build() {
                window.paint_path(p, baseline);
            }
            for (runs, color) in &runs {
                for run in runs {
                    let pts: Vec<_> = run.iter().map(|&(ms, v)| at(ms, v)).collect();
                    if shade {
                        let mut area = PathBuilder::fill();
                        area.move_to(point(pts[0].x, bottom));
                        for p in &pts {
                            area.line_to(*p);
                        }
                        area.line_to(point(pts[pts.len() - 1].x, bottom));
                        area.close();
                        if let Ok(p) = area.build() {
                            window.paint_path(p, color.opacity(0.12));
                        }
                    }
                    let mut line = PathBuilder::stroke(px(1.5));
                    line.move_to(pts[0]);
                    for p in &pts[1..] {
                        line.line_to(*p);
                    }
                    if let Ok(p) = line.build() {
                        window.paint_path(p, *color);
                    }
                }
            }
        },
    )
    .mt_2()
    .w_full()
    .h(px(44.))
}

#[cfg(test)]
mod tests {
    use super::{append, runs};
    use agentbox_api::{AgentResourceHistory, AgentResourcePoint};

    #[test]
    fn breaks_the_line_where_nobody_watched() {
        let pts = [
            (0, 1.),
            (2_000, 2.),
            (4_000, 3.),
            (60_000, 4.),
            (62_000, 5.),
            (200_000, 6.),
        ];
        let got = runs(&pts, 0, 100_000);
        // The lone sample after the gap draws nothing; the one past the
        // window is left out.
        assert_eq!(
            got,
            vec![
                vec![(0, 1.), (2_000, 2.), (4_000, 3.)],
                vec![(60_000, 4.), (62_000, 5.)]
            ]
        );
    }

    fn at(s: &str) -> AgentResourcePoint {
        AgentResourcePoint {
            at: s.into(),
            ..Default::default()
        }
    }

    #[test]
    fn appends_samples_within_the_window() {
        let mut h = AgentResourceHistory {
            window_seconds: 600,
            points: vec![at("2026-11-06T10:00:00Z"), at("2026-11-06T10:09:00Z")],
            ..Default::default()
        };
        append(&mut h, at("2026-11-06T10:09:00Z"));
        assert_eq!(h.points.len(), 2, "a sample already had is dropped");
        append(&mut h, at("2026-11-06T10:10:30Z"));
        let left: Vec<_> = h.points.iter().map(|p| p.at.as_str()).collect();
        assert_eq!(left, ["2026-11-06T10:09:00Z", "2026-11-06T10:10:30Z"]);
    }
}
