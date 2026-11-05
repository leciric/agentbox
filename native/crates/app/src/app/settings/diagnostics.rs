//! Settings → Diagnostics (DiagnosticsPanel.tsx, lib/diagnostics.ts): what
//! the daemon's spans say about the last while — the slowest, the busiest
//! operations, the failures, the stalls — and the shared package cache, open
//! files, and each machine's CPU and memory, read from GET
//! /v1/diagnostics/{spans,stalls,package-cache,files,resources} as the
//! section opens and every 30 seconds while it's open.
use std::time::Duration;

use agentbox_api::{
    DiagnosticsFiles, DiagnosticsPackageCache, DiagnosticsResources, DiagnosticsSpans,
    DiagnosticsStalls, FileCount, SpanStats, TraceSpan,
};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note, note_in, segmented};
use super::defaults::{human_bytes, time_ago};
use super::kit::{card, link};
use super::{Entry, Group, group};
use crate::theme;

const PERIODS: [&str; 5] = ["15m", "30m", "1h", "6h", "24h"];
/// The resource charts' windows: (id, its length, a bar's length), in ms.
const WINDOWS: [(&str, i64, i64); 3] = [
    ("5m", 5 * 60_000, 30_000),
    ("15m", 15 * 60_000, 30_000),
    ("1h", 60 * 60_000, 60_000),
];
/// FOLDED is how many spans a table shows before "Show all".
const FOLDED: usize = 10;

#[derive(Default)]
pub(super) struct Diagnostics {
    period: String,
    window: String,
    spans: Option<Result<DiagnosticsSpans, String>>,
    stalls: Option<Result<DiagnosticsStalls, String>>,
    files: Option<Result<DiagnosticsFiles, String>>,
    cache: Option<Result<DiagnosticsPackageCache, String>>,
    resources: Option<Result<DiagnosticsResources, String>>,
    /// all holds the tables whose every row is shown.
    all: Vec<&'static str>,
    copied: Option<String>,
    polling: bool,
}

/// format_ms is diagnostics.ts's formatMs: 0.42 ms, 12 ms, 1.50 s, 2m 3s.
pub(super) fn format_ms(ms: f64) -> String {
    if ms < 1. {
        format!("{ms:.2} ms")
    } else if ms < 1000. {
        format!("{} ms", ms.round())
    } else if ms < 60_000. {
        if ms < 10_000. {
            format!("{:.2} s", ms / 1000.)
        } else {
            format!("{:.1} s", ms / 1000.)
        }
    } else {
        format!(
            "{}m {}s",
            (ms / 60_000.).floor(),
            ((ms % 60_000.) / 1000.).round()
        )
    }
}

fn attr_text(v: &serde_json::Value) -> String {
    match v {
        serde_json::Value::String(s) => s.clone(),
        v => v.to_string(),
    }
}

fn span_agent(s: &TraceSpan) -> String {
    ["agent", "ref", "project"]
        .iter()
        .find_map(|k| s.attrs.get(*k))
        .map(attr_text)
        .unwrap_or_default()
}

fn span_details(s: &TraceSpan, version: &str) -> String {
    let mut lines = vec![
        format!("span: {}", s.name),
        format!(
            "duration: {} ({} ms)",
            format_ms(s.duration_ms),
            s.duration_ms
        ),
        format!("start: {}", s.start),
    ];
    if !s.source.is_empty() {
        lines.push(format!("source: {}", s.source));
    }
    if !s.id.is_empty() {
        let parent = if s.parent.is_empty() {
            String::new()
        } else {
            format!(" (parent {})", s.parent)
        };
        lines.push(format!("id: {}{parent}", s.id));
    }
    if !s.error.is_empty() {
        lines.push(format!("error: {}", s.error));
    }
    for (k, v) in &s.attrs {
        lines.push(format!("{k}: {}", attr_text(v)));
    }
    if !version.is_empty() {
        lines.push(format!("agentbox: {version}"));
    }
    lines.join("\n")
}

/// Bucket is one bar of a chart: its average and peak, over its samples.
#[derive(Clone, Copy, Default)]
struct Bucket {
    avg: f64,
    peak: f64,
    samples: usize,
}

/// buckets puts samples taken at times into the window's bars.
fn buckets(samples: &[(i64, f64)], window_ms: i64, bucket_ms: i64, now: i64) -> Vec<Bucket> {
    let n = ((window_ms + bucket_ms - 1) / bucket_ms).max(1) as usize;
    let from = now - n as i64 * bucket_ms;
    let mut out = vec![Bucket::default(); n];
    for (at, v) in samples {
        let i = (at - from).div_euclid(bucket_ms);
        if i < 0 || i as usize >= n {
            continue;
        }
        let b = &mut out[i as usize];
        b.avg += v;
        b.peak = b.peak.max(*v);
        b.samples += 1;
    }
    for b in &mut out {
        if b.samples > 0 {
            b.avg /= b.samples as f64;
        }
    }
    out
}

fn millis(at: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(at)
        .map(|a| a.timestamp_millis())
        .unwrap_or(0)
}

fn stat(label: String, value: String, color: Option<Hsla>) -> AnyElement {
    v_flex()
        .flex_1()
        .min_w(px(120.))
        .px_4()
        .py_3()
        .gap_0p5()
        .child(div().text_xs().text_color(theme::faint()).child(label))
        .child(
            div()
                .text_lg()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(color.unwrap_or_else(theme::fg))
                .child(value),
        )
        .into_any_element()
}

fn stats_panel(stats: Vec<AnyElement>) -> AnyElement {
    h_flex()
        .flex_wrap()
        .rounded_xl()
        .border_1()
        .border_color(theme::border())
        .bg(theme::panel())
        .children(stats)
        .into_any_element()
}

fn heading(title: String, description: String) -> AnyElement {
    v_flex()
        .gap_0p5()
        .child(
            div()
                .text_sm()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::fg())
                .child(title),
        )
        .when(!description.is_empty(), |el| {
            el.child(
                div()
                    .text_xs()
                    .text_color(theme::muted())
                    .child(description),
            )
        })
        .into_any_element()
}

/// table is a table of text: a header row, then rows, the numeric columns
/// to the right.
fn table(
    id: &str,
    headers: Vec<String>,
    numeric: &[usize],
    rows: Vec<Vec<(String, Option<Hsla>)>>,
) -> AnyElement {
    // The name takes what's left; numbers are narrow, words a little wider.
    let width = |i: usize| match i {
        0 => None,
        i if numeric.contains(&i) => Some(px(60.)),
        _ => Some(px(96.)),
    };
    let cell = |i: usize, text: String, color: Hsla, bold: bool| {
        let el = div()
            .text_xs()
            .text_color(color)
            .when(bold, |el| el.font_weight(FontWeight::MEDIUM))
            .when(numeric.contains(&i), |el| el.text_right());
        match width(i) {
            None => el.flex_1().min_w_0().child(text),
            Some(w) => el.w(w).flex_none().child(text),
        }
    };
    v_flex()
        .id(SharedString::from(format!("diag-table-{id}")))
        .rounded_xl()
        .border_1()
        .border_color(theme::border())
        .bg(theme::panel())
        .child(
            h_flex()
                .gap_2()
                .px_3()
                .py_1p5()
                .border_b_1()
                .border_color(theme::border())
                .children(
                    headers
                        .into_iter()
                        .enumerate()
                        .map(|(i, h)| cell(i, h.to_uppercase(), theme::faint(), false)),
                ),
        )
        .children(rows.into_iter().map(|r| {
            h_flex().gap_2().px_3().py_1().items_start().children(
                r.into_iter().enumerate().map(|(i, (text, color))| {
                    cell(i, text, color.unwrap_or_else(theme::fg), i == 0)
                }),
            )
        }))
        .into_any_element()
}

fn empty_line(text: String) -> AnyElement {
    div()
        .px_4()
        .py_3()
        .rounded_xl()
        .border_1()
        .border_color(theme::border())
        .bg(theme::panel())
        .text_xs()
        .text_color(theme::muted())
        .child(text)
        .into_any_element()
}

/// chart is a row of bars, each its bucket's peak behind its average.
fn chart(
    label: String,
    data: &[Bucket],
    max: f64,
    format: impl Fn(f64) -> String,
    color: Hsla,
) -> AnyElement {
    let peak = data.iter().map(|b| b.peak).fold(0., f64::max);
    let max = if max > 0. { max } else { 1. };
    v_flex()
        .flex_1()
        .min_w(px(200.))
        .gap_1()
        .child(
            h_flex()
                .justify_between()
                .text_xs()
                .text_color(theme::faint())
                .child(label)
                .child(tv(
                    "diagnostics.resources.peak",
                    &[("value", (&format(peak)).into())],
                )),
        )
        .child(
            h_flex()
                .h(px(64.))
                .p_1()
                .gap(px(1.))
                .items_end()
                .rounded_md()
                .bg(theme::bg())
                .children(data.iter().map(|b| {
                    let h = |v: f64| relative(((v / max).clamp(0., 1.) as f32).max(0.03));
                    div()
                        .flex_1()
                        .h_full()
                        .relative()
                        .when(b.samples > 0, |el| {
                            el.child(
                                div()
                                    .absolute()
                                    .bottom_0()
                                    .left_0()
                                    .right_0()
                                    .h(h(b.peak))
                                    .bg(color.opacity(0.25)),
                            )
                            .child(
                                div()
                                    .absolute()
                                    .bottom_0()
                                    .left_0()
                                    .right_0()
                                    .h(h(b.avg))
                                    .bg(color.opacity(0.8)),
                            )
                        })
                })),
        )
        .into_any_element()
}

impl AppView {
    pub(super) fn diagnostics_groups(&self) -> Vec<Group> {
        vec![
            group(
                "diagnostics",
                "settings.group.diagnostics.title",
                vec![Entry {
                    id: "diagnostics",
                    label: t("settings.section.diagnostics.title"),
                    description: t("settings.section.diagnostics.description"),
                    keywords: t("settings.entry.diagnostics.keywords"),
                    essential: false,
                    modified: false,
                    scope: String::new(),
                }],
            )
            .cards(),
        ]
    }

    pub(super) fn load_diagnostics(&mut self, cx: &mut Context<Self>) {
        let d = &mut self.settings_page.diagnostics;
        if d.period.is_empty() {
            d.period = "30m".into();
        }
        if d.window.is_empty() {
            d.window = "15m".into();
        }
        let (period, window) = (d.period.clone(), d.window.clone());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let spans = client
                .get::<DiagnosticsSpans>(&format!("/v1/diagnostics/spans?since={period}"))
                .await;
            let stalls = client
                .get::<DiagnosticsStalls>(&format!("/v1/diagnostics/stalls?since={period}"))
                .await;
            let files = client
                .get::<DiagnosticsFiles>("/v1/diagnostics/files")
                .await;
            let cache = client
                .get::<DiagnosticsPackageCache>("/v1/diagnostics/package-cache")
                .await;
            let resources = client
                .get::<DiagnosticsResources>(&format!("/v1/diagnostics/resources?window={window}"))
                .await;
            this.update(cx, |this, cx| {
                let d = &mut this.settings_page.diagnostics;
                let e = |e: anyhow::Error| e.to_string();
                d.spans = Some(spans.map_err(e));
                d.stalls = Some(stalls.map_err(e));
                d.files = Some(files.map_err(e));
                d.cache = Some(cache.map_err(e));
                d.resources = Some(resources.map_err(e));
                this.poll_diagnostics(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn poll_diagnostics(&mut self, cx: &mut Context<Self>) {
        let d = &mut self.settings_page.diagnostics;
        if d.polling {
            return;
        }
        d.polling = true;
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(Duration::from_secs(30))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page.diagnostics.polling = false;
                if this.settings && this.settings_page.open == "diagnostics" {
                    this.load_diagnostics(cx);
                }
            })
            .ok();
        })
        .detach();
    }

    pub(super) fn diagnostics_card(&self, cx: &mut Context<Self>) -> AnyElement {
        let d = &self.settings_page.diagnostics;
        let spans = d.spans.as_ref().and_then(|r| r.as_ref().ok());
        let stalls = d.stalls.as_ref().and_then(|r| r.as_ref().ok());
        let stats: &[SpanStats] = spans.map(|s| s.stats.as_slice()).unwrap_or_default();
        let stall_list: &[TraceSpan] = stalls.map(|s| s.stalls.as_slice()).unwrap_or_default();
        let total: i64 = stats.iter().map(|s| s.count).sum();
        let errors: i64 = stats.iter().map(|s| s.errors).sum();
        let dots = || "…".to_string();
        let periods = PERIODS
            .iter()
            .map(|p| {
                (
                    SharedString::from(*p),
                    t(&format!("diagnostics.period.{p}")),
                )
            })
            .collect();
        let version = self
            .settings_page
            .update
            .as_ref()
            .map(|u| u.current.clone())
            .unwrap_or_default();
        let loading = d.spans.is_none();
        v_flex()
            .id("diagnostics")
            .gap_5()
            .child(
                h_flex()
                    .gap_2()
                    .child(segmented(
                        "diag-period",
                        periods,
                        Some(d.period.as_str()),
                        false,
                        std::rc::Rc::new(cx.listener(|this, v: &str, _, cx| {
                            this.settings_page.diagnostics.period = v.to_string();
                            this.load_diagnostics(cx);
                        })),
                    ))
                    .child(button(
                        "diag-refresh",
                        t("diagnostics.refresh"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.load_diagnostics(cx)),
                    )),
            )
            .when_some(d.spans.as_ref().and_then(|r| r.as_ref().err()), |el, e| {
                el.child(note_in(e.clone(), theme::error()))
            })
            .when(spans.is_some_and(|s| s.dropped > 0), |el| {
                el.child(note_in(
                    tv(
                        "diagnostics.dropped",
                        &[("count", spans.map_or(0, |s| s.dropped).into())],
                    ),
                    theme::starting(),
                ))
            })
            .when_some(d.copied.clone(), |el, c| {
                el.child(note_in(c, theme::running()))
            })
            .child(stats_panel(vec![
                stat(
                    t("diagnostics.stat.spans"),
                    spans.map_or_else(dots, |_| total.to_string()),
                    None,
                ),
                stat(
                    t("diagnostics.stat.failures"),
                    spans.map_or_else(dots, |_| errors.to_string()),
                    (errors > 0).then(theme::error),
                ),
                stat(
                    t("diagnostics.stat.operations"),
                    spans.map_or_else(dots, |_| stats.len().to_string()),
                    None,
                ),
                stat(
                    t("diagnostics.stat.stalls"),
                    stalls.map_or_else(dots, |_| stall_list.len().to_string()),
                    (!stall_list.is_empty()).then(theme::starting),
                ),
            ]))
            .child(heading(
                t("diagnostics.slowest.title"),
                t("diagnostics.slowest.description"),
            ))
            .child(self.span_table(
                "slowest",
                spans.map(|s| s.slowest.as_slice()),
                loading,
                t("diagnostics.slowest.empty"),
                false,
                &version,
                cx,
            ))
            .child(heading(
                t("diagnostics.top.title"),
                t("diagnostics.top.description"),
            ))
            .child(if stats.is_empty() {
                empty_line(if loading {
                    t("common.loading")
                } else {
                    t("diagnostics.top.empty")
                })
            } else {
                table(
                    "top",
                    ["operation", "count", "errors", "p50", "p95", "max", "total"]
                        .iter()
                        .map(|c| t(&format!("diagnostics.col.{c}")))
                        .collect(),
                    &[1, 2, 3, 4, 5, 6],
                    stats
                        .iter()
                        .map(|s| {
                            vec![
                                (s.name.clone(), None),
                                (s.count.to_string(), None),
                                (
                                    s.errors.to_string(),
                                    Some(if s.errors > 0 {
                                        theme::error()
                                    } else {
                                        theme::faint()
                                    }),
                                ),
                                (format_ms(s.p50_ms), None),
                                (format_ms(s.p95_ms), None),
                                (format_ms(s.max_ms), Some(theme::muted())),
                                (format_ms(s.total_ms), Some(theme::muted())),
                            ]
                        })
                        .collect(),
                )
            })
            .child(heading(
                t("diagnostics.failures.title"),
                t("diagnostics.failures.description"),
            ))
            .child(self.span_table(
                "failures",
                spans.map(|s| s.failures.as_slice()),
                loading,
                t("diagnostics.failures.empty"),
                true,
                &version,
                cx,
            ))
            .child(heading(
                t("diagnostics.stalls.title"),
                t("diagnostics.stalls.description"),
            ))
            .child(match &d.stalls {
                Some(Err(e)) => empty_line(e.clone()),
                _ if stall_list.is_empty() => empty_line(if d.stalls.is_none() {
                    t("common.loading")
                } else {
                    t("diagnostics.stalls.empty")
                }),
                _ => table(
                    "stalls",
                    ["kind", "operation", "detail", "duration", "when"]
                        .iter()
                        .map(|c| t(&format!("diagnostics.col.{c}")))
                        .collect(),
                    &[3],
                    stall_list
                        .iter()
                        .map(|s| {
                            let detail = s
                                .attrs
                                .iter()
                                .filter(|(k, _)| k.as_str() != "stall")
                                .map(|(k, v)| format!("{k} {}", attr_text(v)))
                                .collect::<Vec<_>>()
                                .join(" · ");
                            vec![
                                (
                                    s.attrs.get("stall").map(attr_text).unwrap_or_default(),
                                    Some(theme::starting()),
                                ),
                                (s.name.clone(), None),
                                (detail, Some(theme::muted())),
                                (format_ms(s.duration_ms), None),
                                (time_ago(&s.start), Some(theme::faint())),
                            ]
                        })
                        .collect(),
                ),
            })
            .child(self.package_cache_part())
            .child(self.open_files_part())
            .child(self.resources_part(cx))
            .into_any_element()
    }

    #[allow(clippy::too_many_arguments)]
    fn span_table(
        &self,
        id: &'static str,
        spans: Option<&[TraceSpan]>,
        loading: bool,
        empty: String,
        errors: bool,
        version: &str,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let Some(spans) = spans.filter(|s| !s.is_empty()) else {
            return empty_line(if loading { t("common.loading") } else { empty });
        };
        let all = self.settings_page.diagnostics.all.contains(&id);
        let shown = if all {
            spans.len()
        } else {
            spans.len().min(FOLDED)
        };
        let rows = spans[..shown].iter().enumerate().map(|(i, s)| {
            let name = if s.source == agentbox_api::SPAN_SOURCE_APP {
                format!("{} {}", t("diagnostics.source.app").to_uppercase(), s.name)
            } else {
                s.name.clone()
            };
            let details = span_details(s, version);
            h_flex()
                .id(SharedString::from(format!("diag-{id}-{i}")))
                .gap_2()
                .px_3()
                .py_1()
                .items_start()
                .child(
                    div()
                        .flex_1()
                        .min_w_0()
                        .text_xs()
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::fg())
                        .child(name),
                )
                .child(
                    div()
                        .w(px(120.))
                        .flex_none()
                        .text_xs()
                        .text_color(if errors {
                            theme::error()
                        } else {
                            theme::muted()
                        })
                        .truncate()
                        .child(if errors {
                            s.error.clone()
                        } else {
                            span_agent(s)
                        }),
                )
                .child(
                    div()
                        .w(px(84.))
                        .flex_none()
                        .text_right()
                        .text_xs()
                        .text_color(theme::fg())
                        .child(format_ms(s.duration_ms)),
                )
                .child(
                    div()
                        .w(px(84.))
                        .flex_none()
                        .text_xs()
                        .text_color(theme::faint())
                        .child(time_ago(&s.start)),
                )
                .child(link(
                    SharedString::from(format!("diag-copy-{id}-{i}")),
                    t("common.copy"),
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        cx.write_to_clipboard(ClipboardItem::new_string(details.clone()));
                        this.settings_page.diagnostics.copied = Some(t("common.copied"));
                        cx.notify();
                    }),
                ))
        });
        let headers = [
            "operation",
            if errors { "error" } else { "agent" },
            "duration",
            "when",
        ];
        v_flex()
            .gap_1()
            .child(
                v_flex()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::panel())
                    .child(
                        h_flex()
                            .gap_2()
                            .px_3()
                            .py_1p5()
                            .border_b_1()
                            .border_color(theme::border())
                            .text_xs()
                            .text_color(theme::faint())
                            .children(headers.iter().enumerate().map(|(i, h)| {
                                let text = t(&format!("diagnostics.col.{h}")).to_uppercase();
                                match i {
                                    0 => div().flex_1().child(text),
                                    1 => div().w(px(120.)).child(text),
                                    2 => div().w(px(84.)).text_right().child(text),
                                    _ => div().w(px(84.)).child(text),
                                }
                            }))
                            .child(div().w(px(40.))),
                    )
                    .children(rows),
            )
            .when(spans.len() > FOLDED, |el| {
                el.child(link(
                    SharedString::from(format!("diag-all-{id}")),
                    if all {
                        t("diagnostics.showFewer")
                    } else {
                        tv("diagnostics.showAll", &[("count", spans.len().into())])
                    },
                    cx.listener(move |this, _: &ClickEvent, _, cx| {
                        let a = &mut this.settings_page.diagnostics.all;
                        if let Some(i) = a.iter().position(|x| *x == id) {
                            a.remove(i);
                        } else {
                            a.push(id);
                        }
                        cx.notify();
                    }),
                ))
            })
            .into_any_element()
    }

    fn package_cache_part(&self) -> AnyElement {
        let d = &self.settings_page.diagnostics;
        let data = d.cache.as_ref().and_then(|r| r.as_ref().ok());
        let description = if data.is_some_and(|c| !c.on) {
            t("diagnostics.pkgcache.off")
        } else {
            tv(
                "diagnostics.pkgcache.description",
                &[
                    ("used", (&human_bytes(data.map_or(0, |c| c.bytes))).into()),
                    (
                        "max",
                        (&human_bytes(data.map_or(0, |c| c.max_bytes))).into(),
                    ),
                ],
            )
        };
        let parts = data.map(|c| c.parts.as_slice()).unwrap_or_default();
        v_flex()
            .gap_2()
            .child(heading(t("diagnostics.pkgcache.title"), description))
            .when_some(d.cache.as_ref().and_then(|r| r.as_ref().err()), |el, e| {
                el.child(note_in(e.clone(), theme::error()))
            })
            .child(if parts.is_empty() {
                empty_line(if d.cache.is_none() {
                    t("common.loading")
                } else {
                    t("diagnostics.pkgcache.empty")
                })
            } else {
                table(
                    "pkgcache",
                    vec![
                        t("diagnostics.pkgcache.tool"),
                        t("diagnostics.pkgcache.size"),
                    ],
                    &[1],
                    parts
                        .iter()
                        .map(|p| vec![(p.name.clone(), None), (human_bytes(p.bytes), None)])
                        .collect(),
                )
            })
            .into_any_element()
    }

    fn open_files_part(&self) -> AnyElement {
        let d = &self.settings_page.diagnostics;
        let f = d.files.as_ref().and_then(|r| r.as_ref().ok());
        let of = |c: Option<&FileCount>| match c {
            None => "…".to_string(),
            Some(c) if c.limit > 0 => tv(
                "diagnostics.files.of",
                &[
                    ("open", c.open.to_string().as_str().into()),
                    ("limit", c.limit.to_string().as_str().into()),
                ],
            ),
            Some(c) => c.open.to_string(),
        };
        let share = f.and_then(|f| f.share.as_ref());
        let used = share
            .filter(|s| s.limit > 0)
            .map_or(0., |s| s.open as f64 / s.limit as f64);
        let share_text = match (f, share) {
            (None, _) => "…".to_string(),
            (Some(_), Some(s)) => of(Some(&FileCount {
                open: s.open,
                limit: s.limit,
            })),
            (Some(_), None) => t("diagnostics.files.none"),
        };
        v_flex()
            .gap_2()
            .child(heading(
                t("diagnostics.files.title"),
                t("diagnostics.files.description"),
            ))
            .when_some(d.files.as_ref().and_then(|r| r.as_ref().err()), |el, e| {
                el.child(note_in(e.clone(), theme::error()))
            })
            .when(f.is_some_and(|f| f.waiting), |el| {
                el.child(note_in(t("diagnostics.files.waiting"), theme::starting()))
            })
            .child(stats_panel(vec![
                stat(
                    t("diagnostics.files.share"),
                    share_text,
                    if used >= 0.7 {
                        Some(theme::error())
                    } else if used >= 0.4 {
                        Some(theme::starting())
                    } else {
                        None
                    },
                ),
                stat(
                    t("diagnostics.files.daemon"),
                    of(f.map(|f| &f.daemon)),
                    None,
                ),
                stat(
                    t("diagnostics.files.system"),
                    of(f.map(|f| &f.system)),
                    None,
                ),
            ]))
            .when_some(share, |el, s| {
                el.child(note(match &s.last_trim {
                    Some(at) if s.trims > 0 => tv(
                        "diagnostics.files.trims",
                        &[
                            ("count", s.trims.to_string().as_str().into()),
                            ("when", (&time_ago(at)).into()),
                            ("pinned", s.pinned.to_string().as_str().into()),
                        ],
                    ),
                    _ => t("diagnostics.files.noTrims"),
                }))
            })
            .into_any_element()
    }

    fn resources_part(&self, cx: &mut Context<Self>) -> AnyElement {
        let d = &self.settings_page.diagnostics;
        let spec = WINDOWS
            .iter()
            .find(|w| w.0 == d.window)
            .copied()
            .unwrap_or(WINDOWS[1]);
        let data = d.resources.as_ref().and_then(|r| r.as_ref().ok());
        let bucket_ms = spec.2.max(data.map_or(0, |r| r.interval_seconds * 1000));
        let now = chrono::Utc::now().timestamp_millis();
        let vm = data.map(|r| r.vm.as_slice()).unwrap_or_default();
        let agents: Vec<_> = data
            .map(|r| r.agents.iter().filter(|a| !a.samples.is_empty()).collect())
            .unwrap_or_default();
        let mem_total = vm.last().map_or(0, |s| s.mem_total);
        let windows = WINDOWS
            .iter()
            .map(|w| {
                (
                    SharedString::from(w.0),
                    t(&format!("diagnostics.period.{}", w.0)),
                )
            })
            .collect();
        let series = |id: String,
                      name: String,
                      mono: bool,
                      cpu: Vec<(i64, f64)>,
                      mem: Vec<(i64, f64)>,
                      cpu_max: Option<f64>| {
            let last = cpu.last().zip(mem.last()).map(|(c, m)| (c.1, m.1));
            let cpu_b = buckets(&cpu, spec.1, bucket_ms, now);
            let mem_b = buckets(&mem, spec.1, bucket_ms, now);
            let cpu_max =
                cpu_max.unwrap_or_else(|| cpu_b.iter().map(|b| b.peak).fold(100., f64::max));
            let mem_max = if mem_total > 0 {
                mem_total as f64
            } else {
                mem_b.iter().map(|b| b.peak).fold(1., f64::max)
            };
            v_flex()
                .id(SharedString::from(format!("diag-series-{id}")))
                .px_4()
                .py_3()
                .gap_2()
                .child(
                    h_flex()
                        .justify_between()
                        .gap_3()
                        .child(
                            div()
                                .text_sm()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::fg())
                                .when(mono, |el| el.font_family(crate::theme::mono()).text_xs())
                                .child(name),
                        )
                        .when_some(last, |el, (c, m)| {
                            el.child(div().text_xs().text_color(theme::faint()).child(tv(
                                "diagnostics.resources.now",
                                &[
                                    ("cpu", format!("{}%", c.round()).as_str().into()),
                                    ("memory", (&human_bytes(m as i64)).into()),
                                ],
                            )))
                        }),
                )
                .child(
                    h_flex()
                        .flex_wrap()
                        .gap_3()
                        .child(chart(
                            t("diagnostics.resources.cpu"),
                            &cpu_b,
                            cpu_max,
                            |v| format!("{}%", v.round()),
                            theme::accent(),
                        ))
                        .child(chart(
                            t("diagnostics.resources.memory"),
                            &mem_b,
                            mem_max,
                            |v| human_bytes(v as i64),
                            theme::running(),
                        )),
                )
                .into_any_element()
        };
        let mut rows = Vec::new();
        if !vm.is_empty() {
            rows.push(series(
                "vm".into(),
                t("diagnostics.resources.vm"),
                false,
                vm.iter().map(|s| (millis(&s.at), s.cpu)).collect(),
                vm.iter()
                    .map(|s| (millis(&s.at), s.mem_used as f64))
                    .collect(),
                Some(100.),
            ));
        }
        for a in &agents {
            rows.push(series(
                a.r#ref.clone(),
                a.r#ref.clone(),
                true,
                a.samples.iter().map(|s| (millis(&s.at), s.cpu)).collect(),
                a.samples
                    .iter()
                    .map(|s| (millis(&s.at), s.memory as f64))
                    .collect(),
                None,
            ));
        }
        v_flex()
            .gap_2()
            .child(
                h_flex()
                    .gap_3()
                    .items_start()
                    .child(div().flex_1().min_w_0().child(heading(
                        t("diagnostics.resources.title"),
                        t("diagnostics.resources.description"),
                    )))
                    .child(segmented(
                        "diag-window",
                        windows,
                        Some(spec.0),
                        false,
                        std::rc::Rc::new(cx.listener(|this, v: &str, _, cx| {
                            this.settings_page.diagnostics.window = v.to_string();
                            this.load_diagnostics(cx);
                        })),
                    )),
            )
            .child(match &d.resources {
                Some(Err(e)) => empty_line(e.clone()),
                None => empty_line(t("common.loading")),
                Some(Ok(_)) if rows.is_empty() => card("diag-resources-empty")
                    .items_center()
                    .child(
                        div()
                            .text_sm()
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::fg())
                            .child(t("diagnostics.resources.empty")),
                    )
                    .child(note(t("diagnostics.resources.emptyHint")))
                    .into_any_element(),
                Some(Ok(_)) => v_flex()
                    .rounded_xl()
                    .border_1()
                    .border_color(theme::border())
                    .bg(theme::panel())
                    .children(rows)
                    .into_any_element(),
            })
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::{buckets, format_ms};

    #[test]
    fn durations_and_bars_read_as_diagnostics_ts() {
        assert_eq!(format_ms(0.5), "0.50 ms");
        assert_eq!(format_ms(12.4), "12 ms");
        assert_eq!(format_ms(1500.), "1.50 s");
        assert_eq!(format_ms(12_000.), "12.0 s");
        assert_eq!(format_ms(123_000.), "2m 3s");
        let b = buckets(
            &[(9_000, 10.), (9_500, 30.), (1_000, 99.)],
            4_000,
            1_000,
            10_000,
        );
        assert_eq!(b.len(), 4);
        assert_eq!(b[3].samples, 2);
        assert_eq!(b[3].avg, 20.);
        assert_eq!(b[3].peak, 30.);
    }
}
