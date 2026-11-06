//! A project's Tokens (TokensPanel.tsx, the project's settings' Tokens
//! section in Electron): the ledger of what its chats and agents spent
//! (GET /v1/tokens?project=&since=), for the last 5 hours, 24 hours, 7 or
//! 30 days or all time — the plan limits of the accounts it spends against
//! (GET /v1/limits), the headline figures, the spend over time as columns,
//! and each agent's share, unfolding to its models and its last turns
//! (GET /v1/tokens/turns). Read again every fifteen seconds while open.
use std::collections::HashMap;
use std::rc::Rc;
use std::time::Duration;

use agentbox_api::{AgentTokens, ClaudeLimit, TokenBucket, TokenReport, TokenTurn};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, button, note_in};
use super::defaults::time_ago;
use super::kit::enc;
use super::kit::note;
use crate::theme;

const PERIODS: [&str; 5] = ["5h", "24h", "7d", "30d", "all"];

#[derive(Default)]
pub(super) struct Tokens {
    project: String,
    period: Option<&'static str>,
    report: Option<Result<TokenReport, String>>,
    limits: Vec<ClaudeLimit>,
    /// open is the agent unfolded, by ref.
    open: Option<String>,
    turns: HashMap<String, Result<Vec<TokenTurn>, String>>,
    polling: bool,
    /// as_table shows spend over time as a table, not the chart.
    as_table: bool,
}

fn fixed(v: f64, digits: usize) -> String {
    format!("{v:.digits$}")
}

/// human_tokens is humanTokens: 1.2K, 3.4M, 1.05B.
pub(in crate::app) fn human_tokens(n: i64) -> String {
    let f = n as f64;
    match n {
        1_000_000_000.. => format!("{}B", fixed(f / 1e9, 2)),
        1_000_000.. => format!("{}M", fixed(f / 1e6, 1)),
        1_000.. => format!("{}K", fixed(f / 1e3, 1)),
        _ => n.to_string(),
    }
}

fn usd(v: f64) -> String {
    match v {
        0. => "—".into(),
        v if v < 0.01 => "<$0.01".into(),
        v if v >= 1000. => format!("${}K", fixed(v / 1000., 1)),
        v => format!("${}", fixed(v, 2)),
    }
}

fn tps(n: f64) -> String {
    match n {
        0. => "—".into(),
        n if n >= 100. => format!("{} tok/s", n.round()),
        n => format!("{} tok/s", fixed(n, 1)),
    }
}

fn share(part: i64, whole: i64) -> String {
    if whole == 0 || part == 0 {
        return "0%".into();
    }
    let pct = part as f64 / whole as f64 * 100.;
    if pct < 1. {
        "<1%".into()
    } else {
        format!("{}%", pct.round())
    }
}

/// nice_ceiling rounds an axis maximum up to 1, 2 or 5 of a power of ten.
fn nice_ceiling(v: i64) -> i64 {
    if v <= 0 {
        return 1;
    }
    let p = 10f64.powf((v as f64).log10().floor()) as i64;
    [1, 2, 5, 10]
        .into_iter()
        .map(|m| m * p)
        .find(|c| *c >= v)
        .unwrap_or(10 * p)
}

fn millis(time: &str) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(time)
        .ok()
        .map(|d| d.timestamp_millis())
}

/// slots are the report's columns on a real time axis, the stretches it
/// has no bucket for left empty.
fn slots(r: &TokenReport) -> Vec<(i64, Option<&TokenBucket>)> {
    let step = r.bucket_seconds * 1000;
    if step <= 0 {
        return Vec::new();
    }
    let by: HashMap<i64, &TokenBucket> = r
        .buckets
        .iter()
        .filter_map(|b| millis(&b.start).map(|s| (s, b)))
        .collect();
    let floor = |ms: i64| ms.div_euclid(step) * step;
    let last = floor(millis(&r.until).unwrap_or(0));
    let first = match r.since.as_deref().and_then(millis) {
        Some(s) => floor(s),
        None => r
            .buckets
            .first()
            .and_then(|b| millis(&b.start))
            .unwrap_or(last),
    };
    let mut out = Vec::new();
    let mut at = first.max(last - 400 * step);
    while at <= last {
        out.push((at, by.get(&at).copied()));
        at += step;
    }
    out
}

/// slot_label is a column's stretch (TokensPanel.tsx slotLabel): its day,
/// or from and to, with the day when a column spans hours.
fn slot_label(start: i64, step: i64) -> String {
    use chrono::{Local, TimeZone};
    let Some(at) = Local.timestamp_millis_opt(start).single() else {
        return String::new();
    };
    if step >= 86_400 {
        return crate::dates::date(at.date_naive(), true);
    }
    let to = Local
        .timestamp_millis_opt(start + step * 1000)
        .single()
        .unwrap_or(at);
    let day = if step >= 3600 {
        format!("{}, ", crate::dates::date(at.date_naive(), true))
    } else {
        String::new()
    };
    format!("{day}{}–{}", at.format("%H:%M"), to.format("%H:%M"))
}

fn step_words(seconds: i64) -> String {
    match seconds {
        86_400.. => tv("vm.tokens.columnIs", &[("unit", "day".into())]),
        3600 => tv("vm.tokens.columnIs", &[("unit", "hour".into())]),
        3601.. => tv(
            "vm.tokens.columnIs",
            &[("unit", "hours".into()), ("n", (seconds / 3600).into())],
        ),
        _ => tv(
            "vm.tokens.columnIs",
            &[("unit", "minutes".into()), ("n", (seconds / 60).into())],
        ),
    }
}

fn kind_label(kind: &str) -> String {
    let key = format!("vm.tokens.kind.{kind}");
    if agentbox_i18n::has(&key) {
        t(&key)
    } else {
        kind.to_string()
    }
}

/// card_head is Card's header (ui/card.tsx): items-center gap-3, a size-7
/// icon box on the surface ringed in line, the title text-sm semibold and
/// what it holds text-xs subtle, an action at the end.
fn card_head(
    icon: &'static str,
    title: String,
    description: String,
    action: Option<AnyElement>,
) -> Div {
    h_flex()
        .gap_3()
        .pt_4()
        .pb_1()
        .child(
            div()
                .flex_none()
                .size_7()
                .flex()
                .items_center()
                .justify_center()
                .rounded_lg()
                .bg(theme::surface())
                .border_1()
                .border_color(theme::line())
                .text_color(theme::muted())
                .child(crate::icons::ic(icon).size(px(16.))),
        )
        .child(
            v_flex()
                .min_w_0()
                .flex_1()
                .child(
                    div()
                        .text_size(theme::text::BODY)
                        .line_height(px(20.))
                        .font_weight(FontWeight::SEMIBOLD)
                        .text_color(theme::primary())
                        .child(title),
                )
                .when(!description.is_empty(), |el| {
                    el.child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .line_height(px(16.))
                            .text_color(theme::subtle())
                            .child(description),
                    )
                }),
        )
        .children(action)
}

/// card_box is Card: its header, then its body (px-5 pt-3 pb-5).
fn card_box(icon: &'static str, title: String, description: String) -> Div {
    super::kit::panel(v_flex())
        .gap_3()
        .px_5()
        .pb_5()
        .child(card_head(icon, title, description, None))
}

/// stat is Stat: rounded-xl line-faint on surface-faint, p-3; its label
/// text-caption uppercase subtle, its value text-xl semibold, its hint two
/// lines at most of text-caption leading-snug faint.
fn stat(label: String, value: String, hint: String) -> AnyElement {
    v_flex()
        .flex_1()
        .flex_basis(px(0.))
        .min_w_0()
        .p_3()
        .rounded_xl()
        .border_1()
        .border_color(theme::line_faint())
        .bg(theme::surface_faint())
        .child(
            div()
                .text_size(theme::text::CAPTION)
                .line_height(px(18.))
                .text_color(theme::subtle())
                .child(label.to_uppercase()),
        )
        .child(
            div()
                .mt_1()
                .whitespace_nowrap()
                .text_size(theme::text::DISPLAY)
                .line_height(px(28.))
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::title())
                .child(value),
        )
        .when(!hint.is_empty(), |el| {
            el.child(
                div()
                    .mt_0p5()
                    .text_size(theme::text::CAPTION)
                    .line_height(px(16.5))
                    .line_clamp(2)
                    .text_ellipsis()
                    .text_color(theme::faint())
                    .child(hint),
            )
        })
        .into_any_element()
}

fn bar(fraction: f64, color: Hsla, h: f32) -> Div {
    div()
        .h(px(h))
        .w_full()
        .rounded_full()
        .bg(theme::surface_strong())
        .overflow_hidden()
        .child(
            div()
                .h_full()
                // Below zero: nothing to show at all.
                .w(relative(if fraction < 0. {
                    0.
                } else {
                    fraction.clamp(0.02, 1.)
                } as f32))
                .rounded_full()
                .bg(color),
        )
}

impl AppView {
    /// load_tokens reads the project's ledger for the period chosen, and the
    /// accounts' limits, and again every fifteen seconds while it's open.
    pub(super) fn load_tokens(&mut self, project: String, cx: &mut Context<Self>) {
        let tk = &mut self.settings_page.tokens;
        if tk.project != project {
            *tk = Tokens {
                project: project.clone(),
                ..Default::default()
            };
        }
        let period = tk.period.unwrap_or("5h");
        let client = self.client.clone();
        let mut path = format!("/v1/tokens?project={}", enc(&project));
        if period != "all" {
            path.push_str(&format!("&since={period}"));
        }
        let open = tk.open.clone();
        let since = (period != "all").then_some(period);
        cx.spawn(async move |this, cx| {
            let report = client
                .get::<TokenReport>(&path)
                .await
                .map_err(|e| e.to_string());
            let limits = client.get::<Vec<ClaudeLimit>>("/v1/limits").await.ok();
            let turns = match &open {
                Some(r) => Some(fetch_turns(&client, r, since).await),
                None => None,
            };
            this.update(cx, |this, cx| {
                let tk = &mut this.settings_page.tokens;
                if tk.project != project {
                    return;
                }
                tk.report = Some(report);
                if let Some(l) = limits {
                    tk.limits = l;
                }
                if let (Some(r), Some(t)) = (open, turns) {
                    tk.turns.insert(r, t);
                }
                this.poll_tokens(cx);
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn poll_tokens(&mut self, cx: &mut Context<Self>) {
        if std::mem::replace(&mut self.settings_page.tokens.polling, true) {
            return;
        }
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(Duration::from_secs(15))
                .await;
            this.update(cx, |this, cx| {
                this.settings_page.tokens.polling = false;
                let p = this.settings_page.tokens.project.clone();
                if this.settings && this.settings_page.open == format!("project:{p}") {
                    this.load_tokens(p, cx);
                }
            })
            .ok();
        })
        .detach();
    }

    fn unfold_agent(&mut self, r: String, cx: &mut Context<Self>) {
        let tk = &mut self.settings_page.tokens;
        if tk.open.as_deref() == Some(r.as_str()) {
            tk.open = None;
            cx.notify();
            return;
        }
        tk.open = Some(r.clone());
        let since = tk.period.filter(|p| *p != "all").or(Some("5h"));
        let since = if tk.period == Some("all") {
            None
        } else {
            since
        };
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let turns = fetch_turns(&client, &r, since).await;
            this.update(cx, |this, cx| {
                this.settings_page.tokens.turns.insert(r, turns);
                cx.notify();
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// tokens_card is the project's Tokens: TokensPanel scoped to it.
    pub(super) fn tokens_card(&self, project: &str, cx: &mut Context<Self>) -> AnyElement {
        let tk = &self.settings_page.tokens;
        let period = tk.period.unwrap_or("5h");
        let pick: super::super::controls::Pick<str> = Rc::new({
            let view = cx.entity().downgrade();
            let project = project.to_string();
            move |p: &str, _, cx: &mut App| {
                let Some(p) = PERIODS.into_iter().find(|x| *x == p) else {
                    return;
                };
                let project = project.clone();
                view.update(cx, |this, cx| {
                    let tk = &mut this.settings_page.tokens;
                    tk.period = Some(p);
                    tk.open = None;
                    this.load_tokens(project, cx);
                })
                .ok();
            }
        });
        // TabsList: gap-0.5 rounded-xl border-line bg-surface-faint p-1; each
        // TabsTrigger h-8 rounded-lg px-3 text-ui medium, the open one on
        // surface-strong.
        let tabs = h_flex().child(
            h_flex()
                .gap(px(2.))
                .p_1()
                .rounded_xl()
                .border_1()
                .border_color(theme::line())
                .bg(theme::surface_faint())
                .children(PERIODS.iter().map(|p| {
                    let on = *p == period;
                    let pick = pick.clone();
                    let p: &'static str = p;
                    h_flex()
                        .id(SharedString::from(format!("tokens-period-{p}")))
                        .h_8()
                        .px_3()
                        .rounded_lg()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .when(on, |el| {
                            el.bg(theme::surface_strong()).text_color(theme::title())
                        })
                        .when(!on, |el| {
                            el.text_color(theme::muted())
                                .cursor_pointer()
                                .hover(|s| s.text_color(theme::primary()))
                                .on_click(move |_, window, cx| pick(p, window, cx))
                        })
                        .child(t(&format!("vm.tokens.period.{p}")))
                })),
        );
        let body: AnyElement = match &tk.report {
            _ if tk.project != project => super::super::panel::loading("tokens"),
            None => super::super::panel::loading("tokens"),
            Some(Err(e)) => note_in(e.clone(), theme::rose(300)),
            Some(Ok(r)) if r.agents.is_empty() => super::kit::panel(v_flex())
                .items_center()
                .gap_1()
                .p_6()
                .child(
                    div()
                        .text_size(px(20.))
                        .text_color(theme::faint())
                        .child(crate::icons::of("circle-dot")),
                )
                .child(
                    div()
                        .mt_1()
                        .text_size(theme::text::UI)
                        .font_weight(FontWeight::MEDIUM)
                        .text_color(theme::secondary())
                        .child(tv("vm.tokens.nothingSpent", &[("period", period.into())])),
                )
                .child(
                    div()
                        .max_w(px(448.))
                        .text_center()
                        .text_size(theme::text::CAPTION)
                        .line_height(rems(1.5))
                        .text_color(theme::subtle())
                        .child(t("vm.tokens.emptyDescription")),
                )
                .into_any_element(),
            Some(Ok(r)) => v_flex()
                .gap_4()
                .child(self.token_headline(r))
                .child(self.spend_over_time(r, cx))
                .child(self.agents_table(r, cx))
                .into_any_element(),
        };
        v_flex()
            .gap_4()
            .child(
                h_flex().flex_wrap().gap_x_4().gap_y_2().child(tabs).child(
                    div()
                        .flex_1()
                        .min_w(px(256.))
                        .text_size(theme::text::CAPTION)
                        .line_height(relative(1.625))
                        .text_color(theme::subtle())
                        .child(t("vm.tokens.intro")),
                ),
            )
            .children(self.project_limits(project))
            .child(body)
            .into_any_element()
    }

    /// project_limits is ClaudeLimits for a project: the accounts it and its
    /// agents spend (projectLimits).
    fn project_limits(&self, project: &str) -> Option<AnyElement> {
        let tk = &self.settings_page.tokens;
        let p = self.projects.iter().find(|p| p.name == project)?;
        let resolve = |name: &str| {
            if name.is_empty() {
                tk.limits.iter().find(|l| l.default)
            } else {
                tk.limits.iter().find(|l| l.account == name)
            }
        };
        let mut names = vec![p.claude_account.as_str()];
        if self.project.as_deref() == Some(project) {
            names.extend(
                self.fleet
                    .iter()
                    .filter(|a| a.ai == "claude")
                    .map(|a| a.claude_account.as_str()),
            );
        }
        let mut shown: Vec<&ClaudeLimit> = Vec::new();
        for l in names.into_iter().filter_map(resolve) {
            if !shown.iter().any(|x| x.account == l.account) {
                shown.push(l);
            }
        }
        if shown.is_empty() {
            return None;
        }
        let now = chrono::Utc::now().timestamp_millis();
        let accounts = shown.into_iter().map(|l| {
            let windows = l.windows.iter().map(|w| {
                let used = match millis(&w.resets_at) {
                    Some(r) if r > 0 && r <= now => None,
                    _ => Some(w.utilization),
                };
                let color = match used {
                    Some(u) if u > 0.85 => theme::rose(400),
                    Some(u) if u > 0.65 => theme::amber(400),
                    _ => theme::brand(400),
                };
                v_flex()
                    .flex_1()
                    .min_w(px(200.))
                    .gap_1()
                    .text_xs()
                    .child(
                        h_flex()
                            .justify_between()
                            .child(div().text_color(theme::muted()).child(w.label.clone()))
                            .child(div().text_color(theme::secondary()).child(match used {
                                Some(u) => format!("{}%", (u * 100.).round()),
                                None => t("vm.tokens.resetSince"),
                            })),
                    )
                    // Reset since: an empty bar, not the 2% a reading gets.
                    .child(bar(used.unwrap_or(-1.), color, 6.))
                    .child(div().text_color(theme::faint()).child(match used {
                        None => tv(
                            "vm.tokens.resetNoChat",
                            &[("ago", time_ago(&w.resets_at).into())],
                        ),
                        Some(_) => tv(
                            "vm.tokens.resetsIn",
                            &[("time", until(&w.resets_at, now).into())],
                        ),
                    }))
            });
            v_flex()
                .gap_2()
                .child(
                    h_flex()
                        .gap_2()
                        .text_xs()
                        .child(
                            div()
                                .font_weight(FontWeight::MEDIUM)
                                .text_color(theme::secondary())
                                .child(l.account.clone()),
                        )
                        .when(l.default, |el| {
                            el.child(
                                div()
                                    .text_color(theme::faint())
                                    .child(t("vm.tokens.default")),
                            )
                        })
                        .child(div().flex_1())
                        .child(
                            div()
                                .text_color(theme::faint())
                                .child(tv("vm.tokens.asOf", &[("ago", time_ago(&l.at).into())])),
                        ),
                )
                .child(h_flex().flex_wrap().gap_3().children(windows))
        });
        Some(
            card_box(
                "gauge",
                t("vm.tokens.claudeLimits"),
                t("vm.tokens.claudeLimitsDescription"),
            )
            .children(accounts)
            .into_any_element(),
        )
    }

    fn token_headline(&self, r: &TokenReport) -> AnyElement {
        let fullest = r.agents.iter().max_by_key(|a| a.max_context);
        // grid-cols-2 md:grid-cols-5: five abreast in a window 768 wide.
        h_flex()
            .items_stretch()
            .gap_3()
            .child(stat(
                t("vm.tokens.tokens"),
                human_tokens(r.total),
                tv(
                    "vm.tokens.writtenByModel",
                    &[("n", human_tokens(r.output).into())],
                ),
            ))
            .child(stat(
                t("vm.tokens.estimatedCost"),
                usd(r.cost_usd),
                t("vm.tokens.costHint"),
            ))
            .child(stat(
                t("vm.tokens.avgTPS"),
                tps(r.avg_tps),
                t("vm.tokens.avgTPSHint"),
            ))
            .child(stat(
                t("vm.tokens.cacheReads"),
                share(r.cache_read, r.total),
                t("vm.tokens.cacheReadsHint"),
            ))
            .child(stat(
                t("vm.tokens.peakContext"),
                fullest.map_or("—".into(), |a| human_tokens(a.max_context)),
                fullest
                    .map(|a| {
                        tv(
                            "vm.tokens.peakContextHint",
                            &[("name", agent_name(a).into())],
                        )
                    })
                    .unwrap_or_default(),
            ))
            .into_any_element()
    }

    /// spend_over_time is SpendOverTime's chart, a column per stretch each
    /// saying what it spent, or the same as a table.
    fn spend_over_time(&self, r: &TokenReport, cx: &mut Context<Self>) -> AnyElement {
        let series = slots(r);
        let peak = nice_ceiling(
            series
                .iter()
                .map(|s| s.1.map_or(0, |b| b.total))
                .max()
                .unwrap_or(0),
        );
        let labelled: Vec<i64> = match series.len() {
            0 => Vec::new(),
            1 => vec![series[0].0],
            n => vec![series[0].0, series[(n - 1) / 2].0, series[n - 1].0],
        };
        let step = r.bucket_seconds;
        let as_table = self.settings_page.tokens.as_table;
        let toggle = button(
            "tokens-as-table",
            t(if as_table {
                "vm.tokens.showChart"
            } else {
                "vm.tokens.showTable"
            }),
            Btn::Ghost,
            false,
            cx.listener(|this, _: &ClickEvent, _, cx| {
                this.settings_page.tokens.as_table = !this.settings_page.tokens.as_table;
                cx.notify();
            }),
        );
        let head = card_head(
            "chart-column",
            t("vm.tokens.overTime"),
            step_words(step),
            Some(toggle),
        );
        if as_table {
            let cell = |text: String, w: f32, right: bool, strong: bool| {
                div()
                    .w(px(w))
                    .flex_none()
                    .when(right, |el| el.text_right())
                    .text_color(if strong {
                        theme::secondary()
                    } else {
                        theme::tertiary()
                    })
                    .child(text)
            };
            let header = h_flex()
                .px_3()
                .py_1p5()
                .gap_2()
                .bg(theme::surface())
                .text_xs()
                .font_weight(FontWeight::MEDIUM)
                .text_color(theme::muted())
                .border_b_1()
                .border_color(theme::line())
                .child(div().flex_1().min_w_0().child(t("vm.tokens.when")))
                .child(cell(t("vm.tokens.tokens"), 80., true, false))
                .child(cell(t("vm.tokens.cacheRead"), 90., true, false))
                .child(cell(t("vm.tokens.output"), 80., true, false))
                .child(cell(t("vm.tokens.avgTPS"), 80., true, false))
                .child(cell(t("vm.tokens.cost"), 70., true, false));
            let rows = series.iter().filter_map(|(start, b)| {
                let b = (*b)?;
                Some(
                    h_flex()
                        .px_3()
                        .py_1p5()
                        .gap_2()
                        .text_xs()
                        .border_t_1()
                        .border_color(theme::line_faint())
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .truncate()
                                .text_color(theme::muted())
                                .child(slot_label(*start, step)),
                        )
                        .child(cell(human_tokens(b.total), 80., true, true))
                        .child(cell(human_tokens(b.cache_read), 90., true, false))
                        .child(cell(human_tokens(b.output), 80., true, false))
                        .child(cell(tps(b.avg_tps), 80., true, false))
                        .child(cell(usd(b.cost_usd), 70., true, false)),
                )
            });
            return super::kit::panel(v_flex())
                .gap_3()
                .px_5()
                .pb_5()
                .child(head)
                .child(
                    v_flex()
                        .id("tokens-table")
                        .max_h(px(288.))
                        .overflow_y_scroll()
                        .rounded_xl()
                        .border_1()
                        .border_color(theme::line_faint())
                        .child(header)
                        .children(rows),
                )
                .into_any_element();
        }
        let columns = series.iter().map(|(start, b)| {
            let total = b.map_or(0, |b| b.total);
            let h = if total > 0 {
                (total as f64 / peak as f64).max(0.015)
            } else {
                0.
            };
            // Each column says what it spent, how much was cache, how fast
            // and what it cost, and when.
            let title = if total > 0 {
                tv("vm.tokens.nTokens", &[("n", human_tokens(total).into())])
            } else {
                t("vm.tokens.nothingSpentSlot")
            };
            let detail = b.map(|b| {
                format!(
                    "{} · {} · {}",
                    tv(
                        "vm.tokens.cacheReadsShare",
                        &[("share", share(b.cache_read, total).into())]
                    ),
                    tps(b.avg_tps),
                    usd(b.cost_usd)
                )
            });
            let when = slot_label(*start, step);
            div()
                .id(SharedString::from(format!("tokens-slot-{start}")))
                .flex_1()
                .min_w_0()
                .h_full()
                .flex()
                .items_end()
                .justify_center()
                .tooltip(move |w, cx| {
                    let (title, detail, when) = (title.clone(), detail.clone(), when.clone());
                    gpui_component::tooltip::Tooltip::element(move |_, _| {
                        v_flex()
                            .gap_0p5()
                            .child(div().font_weight(FontWeight::SEMIBOLD).child(title.clone()))
                            .when_some(detail.clone(), |el, d| {
                                el.child(div().text_color(theme::muted()).child(d))
                            })
                            .child(div().text_color(theme::faint()).child(when.clone()))
                    })
                    .build(w, cx)
                })
                .child(
                    div()
                        .w_full()
                        .max_w(px(24.))
                        .h(relative(h as f32))
                        .rounded_t(px(4.))
                        .bg(theme::brand(500))
                        .hover(|s| s.opacity(0.8)),
                )
        });
        // axisTokens: under a thousand, the number as JavaScript writes it
        // (0.5 of an empty chart's 1).
        let axis = |f: f64| {
            let v = peak as f64 * f;
            if v < 1000. {
                return format!("{v}");
            }
            human_tokens(v as i64)
                .replace(".0K", "K")
                .replace(".0M", "M")
        };
        super::kit::panel(v_flex())
            .gap_3()
            .px_5()
            .pb_5()
            .child(head)
            // h-40: a faint line at 0, ½ and the top, each with its figure
            // in a w-10 column centred on it, and the columns in what's
            // right of left-12.
            .child(
                div()
                    .relative()
                    .h(px(160.))
                    .children([0., 0.5, 1.].map(|f: f64| {
                        let y = 160. * (1. - f as f32);
                        div()
                            .absolute()
                            .left_0()
                            .right_0()
                            .top(px(y - 9.))
                            .h(px(18.))
                            .child(
                                div()
                                    .absolute()
                                    .left(px(48.))
                                    .right_0()
                                    .top(px(8.))
                                    .h(px(1.))
                                    .bg(theme::line_faint()),
                            )
                            .child(
                                div()
                                    .absolute()
                                    .left_0()
                                    .w(px(40.))
                                    .text_right()
                                    .text_size(theme::text::CAPTION)
                                    .line_height(px(18.))
                                    .text_color(theme::faint())
                                    .child(axis(f)),
                            )
                    }))
                    .child(
                        h_flex()
                            .absolute()
                            .top_0()
                            .bottom_0()
                            .left(px(48.))
                            .right_0()
                            .items_end()
                            .gap(px(2.))
                            .children(columns),
                    ),
            )
            .child(
                h_flex()
                    .ml(px(48.))
                    .mt(px(-6.))
                    .justify_between()
                    .text_size(theme::text::CAPTION)
                    .line_height(px(18.))
                    .text_color(theme::faint())
                    .children(labelled.into_iter().map(|s| {
                        slot_label(s, step)
                            .split('–')
                            .next()
                            .unwrap_or_default()
                            .to_string()
                    })),
            )
            .into_any_element()
    }

    fn agents_table(&self, r: &TokenReport, cx: &mut Context<Self>) -> AnyElement {
        let tk = &self.settings_page.tokens;
        let top = r.agents.iter().map(|a| a.total).max().unwrap_or(1).max(1);
        let head = |label: String, w: f32| div().w(px(w)).flex_none().text_right().child(label);
        let header = h_flex()
            .px_3()
            .py_1p5()
            .gap_2()
            .text_xs()
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::muted())
            .child(div().flex_1().min_w_0().child(t("vm.tokens.agent")))
            .child(head(t("vm.tokens.tokens"), 140.))
            .child(head(t("vm.tokens.cacheRead"), 56.))
            .child(head(t("vm.tokens.avgTPS"), 72.))
            .child(head(t("vm.tokens.cost"), 48.))
            .child(head(t("vm.tokens.peakContext"), 60.));
        let rows = r.agents.iter().map(|a| {
            let open = tk.open.as_deref() == Some(a.r#ref.as_str());
            let r2 = a.r#ref.clone();
            let cell = |text: String, w: f32| {
                div()
                    .w(px(w))
                    .flex_none()
                    .text_right()
                    .font_family(theme::mono())
                    .text_color(theme::muted())
                    .child(text)
            };
            let sub = if a.agent == "lead" {
                a.r#ref.clone()
            } else {
                a.agent.clone()
            };
            v_flex()
                .border_t_1()
                .border_color(theme::line_faint())
                .when(open, |el| el.bg(theme::surface()))
                .child(
                    h_flex()
                        .id(SharedString::from(format!("tokens-{}", a.r#ref)))
                        .items_start()
                        .px_3()
                        .py_2()
                        .gap_2()
                        .text_xs()
                        .cursor_pointer()
                        .hover(|s| s.bg(theme::surface_raised()))
                        .on_click(cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.unfold_agent(r2.clone(), cx)
                        }))
                        .child(
                            h_flex()
                                .flex_1()
                                .min_w_0()
                                .items_start()
                                .gap_1p5()
                                .child(
                                    div()
                                        .text_color(theme::faint())
                                        .child(crate::icons::of(if open { "▾" } else { "▸" })),
                                )
                                .child(
                                    v_flex()
                                        .flex_1()
                                        .min_w_0()
                                        .child(
                                            h_flex()
                                                .gap_1p5()
                                                .child(
                                                    div()
                                                        .truncate()
                                                        .font_weight(FontWeight::MEDIUM)
                                                        .child(agent_name(a)),
                                                )
                                                .when(!a.exists && a.agent != "lead", |el| {
                                                    el.child(
                                                        div()
                                                            .px_1()
                                                            .rounded_sm()
                                                            .bg(theme::surface_raised())
                                                            .text_color(theme::muted())
                                                            .child(t("vm.tokens.retired")),
                                                    )
                                                }),
                                        )
                                        .child(
                                            div()
                                                .truncate()
                                                .text_color(theme::faint())
                                                .child(format!("{sub} · {}", time_ago(&a.last_at))),
                                        ),
                                ),
                        )
                        .child(
                            h_flex()
                                .w(px(140.))
                                .flex_none()
                                .gap_1p5()
                                .child(
                                    div()
                                        .w(px(48.))
                                        .text_right()
                                        .font_family(theme::mono())
                                        .child(human_tokens(a.total)),
                                )
                                .child(div().w(px(48.)).child(bar(
                                    a.total as f64 / top as f64,
                                    theme::accent(),
                                    6.,
                                )))
                                .child(
                                    div()
                                        .w(px(32.))
                                        .font_family(theme::mono())
                                        .text_color(theme::faint())
                                        .child(share(a.total, r.total)),
                                ),
                        )
                        .child(cell(human_tokens(a.cache_read), 56.))
                        .child(cell(tps(a.avg_tps), 72.))
                        .child(cell(usd(a.cost_usd), 48.))
                        .child(cell(
                            if a.max_context > 0 {
                                human_tokens(a.max_context)
                            } else {
                                "—".into()
                            },
                            60.,
                        )),
                )
                .when(open, |el| el.child(self.agent_detail(a)))
        });
        card_box(
            "users",
            t("vm.tokens.byAgent"),
            t("vm.tokens.byAgentDescription"),
        )
        .child(
            v_flex()
                .rounded_xl()
                .border_1()
                .border_color(theme::line_faint())
                .overflow_hidden()
                .child(header)
                .children(rows),
        )
        .into_any_element()
    }

    /// agent_detail is AgentDetail: the turns line, the models, the last
    /// turns.
    fn agent_detail(&self, a: &AgentTokens) -> AnyElement {
        let total: i64 = a.models.iter().map(|m| m.total).sum::<i64>().max(1);
        let models = a.models.iter().map(|m| {
            v_flex()
                .flex_1()
                .min_w(px(220.))
                .gap_1()
                .text_xs()
                .child(
                    h_flex()
                        .justify_between()
                        .gap_2()
                        .child(div().truncate().font_family(theme::mono()).child(
                            if m.model.is_empty() {
                                t("vm.tokens.unnamedModel")
                            } else {
                                m.model.clone()
                            },
                        ))
                        .child(div().flex_none().text_color(theme::muted()).child(format!(
                            "{} · {}",
                            human_tokens(m.total),
                            tps(m.avg_tps)
                        ))),
                )
                .child(bar(m.total as f64 / total as f64, theme::accent(), 4.))
        });
        let turns: AnyElement = match self.settings_page.tokens.turns.get(&a.r#ref) {
            None => super::super::panel::loading("agent-tokens"),
            Some(Err(e)) => note_in(e.clone(), theme::rose(300)),
            Some(Ok(list)) if list.is_empty() => note(t("vm.tokens.noneInStretch")),
            Some(Ok(list)) => {
                let col = |text: String, w: f32| {
                    div()
                        .w(px(w))
                        .flex_none()
                        .text_right()
                        .font_family(theme::mono())
                        .child(text)
                };
                v_flex()
                    .rounded_lg()
                    .border_1()
                    .border_color(theme::line())
                    .text_xs()
                    .children(list.iter().map(|turn| {
                        let speed = if turn.generation_ms > 0 {
                            tps(turn.output as f64 / turn.generation_ms as f64 * 1000.)
                        } else {
                            "—".into()
                        };
                        h_flex()
                            .px_2p5()
                            .py_1()
                            .gap_2()
                            .border_t_1()
                            .border_color(theme::line())
                            .text_color(theme::muted())
                            .child(div().w(px(72.)).flex_none().child(time_ago(&turn.at)))
                            .child(div().w(px(96.)).flex_none().child(kind_label(&turn.kind)))
                            .child(
                                div()
                                    .flex_1()
                                    .min_w(px(72.))
                                    .overflow_hidden()
                                    .whitespace_nowrap()
                                    .font_family(theme::mono())
                                    .child(if turn.model.is_empty() {
                                        "—".to_string()
                                    } else {
                                        turn.model.clone()
                                    }),
                            )
                            .child(col(
                                if turn.total > 0 {
                                    human_tokens(turn.total)
                                } else {
                                    "—".into()
                                },
                                56.,
                            ))
                            .child(col(speed, 80.))
                            .child(col(usd(turn.cost_usd), 56.))
                            .child(col(
                                if turn.context > 0 {
                                    human_tokens(turn.context)
                                } else {
                                    "—".into()
                                },
                                56.,
                            ))
                    }))
                    .into_any_element()
            }
        };
        v_flex()
            .px_3()
            .pb_3()
            .pt_1()
            .gap_3()
            .child(div().text_xs().text_color(theme::muted()).child(tv(
                "vm.tokens.turnsLine",
                &[
                    ("turns", a.turns.into()),
                    ("output", human_tokens(a.output).into()),
                    ("cacheWrites", human_tokens(a.cache_write).into()),
                    ("input", human_tokens(a.input).into()),
                ],
            )))
            .child(
                v_flex()
                    .gap_1p5()
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(t("vm.tokens.byModel")),
                    )
                    .child(h_flex().flex_wrap().gap_x_6().gap_y_1p5().children(models)),
            )
            .child(
                v_flex()
                    .gap_1p5()
                    .child(
                        div()
                            .text_xs()
                            .text_color(theme::faint())
                            .child(t("vm.tokens.lastTurns")),
                    )
                    .child(turns),
            )
            .into_any_element()
    }
}

/// agent_name is how the ledger names one: the project's chat for its lead.
fn agent_name(a: &AgentTokens) -> String {
    if a.project == agentbox_api::HOME_PROJECT {
        t("vm.tokens.mainChat")
    } else if a.agent == "lead" {
        t("vm.tokens.projectChat")
    } else if a.title.is_empty() {
        a.agent.clone()
    } else {
        a.title.clone()
    }
}

fn until(time: &str, now: i64) -> String {
    let s = ((millis(time).unwrap_or(now) - now) / 1000).max(0);
    if s < 3600 {
        tv("common.inMinutes", &[("n", ((s + 30) / 60).max(1).into())])
    } else if s < 86400 {
        tv("common.inHours", &[("n", ((s + 1800) / 3600).into())])
    } else {
        tv("common.inDays", &[("n", ((s + 43200) / 86400).into())])
    }
}

async fn fetch_turns(
    client: &agentbox_api::Client,
    r: &str,
    since: Option<&str>,
) -> Result<Vec<TokenTurn>, String> {
    let (project, agent) = r.split_once('/').unwrap_or((r, ""));
    let mut path = format!(
        "/v1/tokens/turns?project={}&agent={}&limit=12",
        enc(project),
        enc(agent)
    );
    if let Some(s) = since {
        path.push_str(&format!("&since={s}"));
    }
    client
        .get::<Vec<TokenTurn>>(&path)
        .await
        .map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn figures_read_as_electron_writes_them() {
        assert_eq!(human_tokens(406_131), "406.1K");
        assert_eq!(human_tokens(1_250_000), "1.2M");
        assert_eq!(usd(0.6588), "$0.66");
        assert_eq!(usd(0.), "—");
        assert_eq!(tps(23.388), "23.4 tok/s");
        assert_eq!(share(1, 1000), "<1%");
        assert_eq!(nice_ceiling(140_000), 200_000);
        assert_eq!(nice_ceiling(0), 1);
    }
}
