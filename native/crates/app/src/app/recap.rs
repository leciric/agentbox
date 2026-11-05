//! Your week (components/RecapView.tsx, lib/recap.ts): what the agents did
//! in seven days (GET /v1/recap), a week at a time back from this one —
//! their hours of work and how much of it while you were away, the agents,
//! pull requests, lines, streak, longest agent and busiest project, each day
//! and each hour, and tokens and cost by model. Opened from Work's "Your week"
//! over the page. The card to share (a PNG drawn on a canvas) isn't here.
use agentbox_api::{Client, Recap};
use agentbox_i18n::{t, tv};
use chrono::{Datelike as _, Duration, NaiveDate};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::controls::{self, Btn};
use crate::theme;

pub enum RecapEvent {
    Close,
}

pub struct RecapView {
    client: Client,
    focus: FocusHandle,
    weeks: i64,
    recap: Option<Result<Recap, String>>,
}

impl EventEmitter<RecapEvent> for RecapView {}

/// duration is work as a person says it: 42h, 3.5h, 45m.
pub fn duration(seconds: i64) -> String {
    let hours = seconds as f64 / 3600.;
    if hours >= 10. {
        format!("{}h", hours.round())
    } else if hours >= 1. {
        format!("{}h", (hours * 10.).round() / 10.)
    } else {
        format!("{}m", (seconds as f64 / 60.).round())
    }
}

/// compact is a count short enough for a tile: 940, 12.4k, 1.2M.
pub fn compact(n: i64) -> String {
    let f = n as f64;
    if n.abs() >= 1_000_000 {
        format!("{:.1}M", f / 1e6)
    } else if n.abs() >= 10_000 {
        format!("{}k", (f / 1e3).round())
    } else if n.abs() >= 1000 {
        format!("{:.1}k", f / 1e3)
    } else {
        n.to_string()
    }
}

/// model_label is a model as people call it: claude-opus-4-5 is Opus 4.5.
fn model_label(model: &str) -> String {
    let Some(rest) = model.strip_prefix("claude-") else {
        return model.to_string();
    };
    let mut parts = rest.split('-');
    let Some(family) = parts
        .next()
        .filter(|f| f.chars().all(|c| c.is_ascii_lowercase()))
    else {
        return model.to_string();
    };
    let nums: Vec<&str> = parts
        .take_while(|p| p.chars().all(|c| c.is_ascii_digit()) && p.len() <= 2)
        .collect();
    if nums.is_empty() {
        return model.to_string();
    }
    let mut c = family.chars();
    let name: String = c
        .next()
        .map(|f| f.to_ascii_uppercase())
        .into_iter()
        .chain(c)
        .collect();
    format!("{name} {}", nums.join("."))
}

fn cost(usd: f64) -> String {
    if usd >= 100. {
        format!("${}", usd.round())
    } else {
        format!("${usd:.2}")
    }
}

impl RecapView {
    pub fn new(client: Client, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let focus = cx.focus_handle();
        focus.focus(window, cx);
        let mut this = Self {
            client,
            focus,
            weeks: 0,
            recap: None,
        };
        this.load(cx);
        this
    }

    fn load(&mut self, cx: &mut Context<Self>) {
        let offset = chrono::Local::now().offset().local_minus_utc() / 60;
        let path = format!("/v1/recap?offset={offset}&weeks={}", self.weeks);
        let client = self.client.clone();
        let weeks = self.weeks;
        self.recap = None;
        cx.spawn(async move |this, cx| {
            let res = client.get::<Recap>(&path).await;
            this.update(cx, |this, cx| {
                if this.weeks == weeks {
                    this.recap = Some(res.map_err(|e| e.to_string()));
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    fn go(&mut self, by: i64, cx: &mut Context<Self>) {
        self.weeks = (self.weeks + by).max(0);
        self.load(cx);
    }

    /// range is the week's dates: Oct 18 – Oct 24.
    fn range(r: &Recap) -> String {
        let (Some(start), Some(end)) = (crate::dates::local(&r.start), crate::dates::local(&r.end))
        else {
            return String::new();
        };
        let last = end - Duration::hours(12);
        format!(
            "{} – {}",
            crate::dates::date(start.date_naive(), true),
            crate::dates::date(last.date_naive(), true)
        )
    }
}

fn stat(label: String, value: String, sub: String) -> AnyElement {
    v_flex()
        .flex_1()
        .min_w(px(150.))
        .gap_1()
        .p_3()
        .rounded_lg()
        .border_1()
        .border_color(theme::border())
        .bg(theme::raised())
        .child(div().text_xs().text_color(theme::muted()).child(label))
        .child(
            div()
                .text_xl()
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::fg())
                .truncate()
                .child(value),
        )
        .child(
            div()
                .text_xs()
                .text_color(theme::faint())
                .truncate()
                .child(sub),
        )
        .into_any_element()
}

fn heading(text: String) -> Div {
    div()
        .text_sm()
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::fg())
        .child(text)
}

impl RecapView {
    fn body(&self, r: &Recap) -> AnyElement {
        if r.work_seconds == 0 && r.agents == 0 && r.tokens == 0 {
            return div()
                .py_8()
                .text_sm()
                .text_color(theme::muted())
                .child(t("recap.empty"))
                .into_any_element();
        }
        let share = if r.work_seconds > 0 {
            (r.away_seconds as f64 / r.work_seconds as f64 * 100.).round() as i64
        } else {
            0
        };
        let longest = r.longest.as_ref();
        let busiest = r.busiest.as_ref();
        let most_day = r
            .days
            .iter()
            .map(|d| d.work_seconds)
            .max()
            .unwrap_or(0)
            .max(1);
        let most_hour = r.hours.iter().copied().max().unwrap_or(0).max(1);
        let peak = r
            .hours
            .iter()
            .enumerate()
            .filter(|(_, s)| **s > 0)
            .max_by_key(|(_, s)| **s)
            .map(|(h, _)| format!("{h:02}:00"));
        let days =
            r.days.iter().map(|d| {
                let tip = tv(
                    "recap.dayTip",
                    &[
                        ("work", duration(d.work_seconds).into()),
                        ("away", duration(d.away_seconds).into()),
                        ("count", d.agents.into()),
                    ],
                );
                let name = NaiveDate::parse_from_str(&d.date, "%Y-%m-%d")
                    .map(|n| crate::dates::weekday(n.weekday().num_days_from_sunday(), false))
                    .unwrap_or_default();
                let with_you = (d.work_seconds - d.away_seconds).max(0) as f32 / most_day as f32;
                let away = d.away_seconds as f32 / most_day as f32;
                v_flex()
                    .id(SharedString::from(format!("recap-day-{}", d.date)))
                    .flex_1()
                    .items_center()
                    .gap_1()
                    .tooltip(move |w, cx| {
                        gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                    })
                    .child(div().text_xs().text_color(theme::faint()).child(
                        if d.work_seconds > 0 {
                            duration(d.work_seconds)
                        } else {
                            String::new()
                        },
                    ))
                    .child(
                        v_flex()
                            .w(px(28.))
                            .h(px(110.))
                            .justify_end()
                            .child(
                                div()
                                    .w_full()
                                    .h(relative(away))
                                    .rounded_t_sm()
                                    .bg(theme::accent()),
                            )
                            .child(
                                div()
                                    .w_full()
                                    .h(relative(with_you))
                                    .bg(theme::accent().opacity(0.4)),
                            ),
                    )
                    .child(div().text_xs().text_color(theme::muted()).child(name))
            });
        let hours = r.hours.iter().enumerate().map(|(h, s)| {
            div()
                .id(("recap-hour", h))
                .flex_1()
                .h(px(28.))
                .rounded_sm()
                .bg(theme::accent())
                .opacity(if *s > 0 {
                    0.18 + 0.82 * *s as f32 / most_hour as f32
                } else {
                    0.06
                })
        });
        v_flex()
            .gap_5()
            .child(
                v_flex()
                    .gap_1()
                    .child(
                        h_flex()
                            .items_end()
                            .gap_2()
                            .child(
                                div()
                                    .text_3xl()
                                    .font_weight(FontWeight::BOLD)
                                    .text_color(theme::fg())
                                    .child(duration(r.work_seconds)),
                            )
                            .child(
                                div()
                                    .text_color(theme::muted())
                                    .child(t("recap.card.agentWork")),
                            ),
                    )
                    .child(
                        div()
                            .id("recap-away")
                            .text_sm()
                            .text_color(theme::accent())
                            .tooltip(|w, cx| {
                                gpui_component::tooltip::Tooltip::new(t("recap.awayHint"))
                                    .build(w, cx)
                            })
                            .child(tv(
                                "recap.awayYou",
                                &[
                                    ("hours", duration(r.away_seconds).into()),
                                    ("share", share.into()),
                                ],
                            )),
                    ),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_3()
                    .child(stat(
                        t("recap.agents"),
                        compact(r.agents),
                        tv("recap.projects", &[("count", r.projects.into())]),
                    ))
                    .child(stat(
                        t("recap.prs"),
                        compact(r.prs_opened),
                        tv("recap.merged", &[("count", r.prs_merged.into())]),
                    ))
                    .child(stat(
                        t("recap.lines"),
                        compact(r.insertions + r.deletions),
                        format!("+{} −{}", compact(r.insertions), compact(r.deletions)),
                    ))
                    .child(stat(
                        t("recap.streak"),
                        tv("recap.days", &[("count", r.streak.into())]),
                        tv("recap.best", &[("count", r.best_streak.into())]),
                    ))
                    .child(stat(
                        t("recap.longest"),
                        longest.map_or("—".into(), |l| duration(l.work_seconds)),
                        longest.map_or(String::new(), |l| {
                            if l.title.is_empty() {
                                l.agent.clone()
                            } else {
                                l.title.clone()
                            }
                        }),
                    ))
                    .child(stat(
                        t("recap.busiest"),
                        busiest.map_or("—".into(), |b| b.project.clone()),
                        busiest.map_or(String::new(), |b| {
                            tv(
                                "recap.busiestSub",
                                &[
                                    ("hours", duration(b.work_seconds).into()),
                                    ("count", b.agents.into()),
                                ],
                            )
                        }),
                    )),
            )
            .child(
                v_flex()
                    .gap_2()
                    .child(
                        h_flex()
                            .gap_3()
                            .child(heading(t("recap.eachDay")))
                            .child(div().flex_1())
                            .child(legend(theme::accent(), t("recap.whileAway")))
                            .child(legend(theme::accent().opacity(0.4), t("recap.withYou"))),
                    )
                    .child(h_flex().gap_2().items_end().children(days)),
            )
            .child(
                v_flex()
                    .gap_2()
                    .child(
                        h_flex()
                            .child(heading(t("recap.byHour")))
                            .child(div().flex_1())
                            .when_some(peak, |el, p| {
                                el.child(
                                    div()
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(tv("recap.peak", &[("hour", p.into())])),
                                )
                            }),
                    )
                    .child(h_flex().gap(px(3.)).children(hours)),
            )
            .child(
                v_flex()
                    .gap_2()
                    .child(
                        h_flex()
                            .child(heading(t("recap.models")))
                            .child(div().flex_1())
                            .child(div().text_xs().text_color(theme::muted()).child(tv(
                                "recap.tokens",
                                &[("tokens", compact(r.tokens).into())],
                            ))),
                    )
                    .when(r.models.is_empty(), |el| {
                        el.child(
                            div()
                                .text_sm()
                                .text_color(theme::faint())
                                .child(t("recap.noTokens")),
                        )
                    })
                    .children(r.models.iter().map(|m| {
                        h_flex()
                            .gap_2()
                            .text_sm()
                            .child(
                                div()
                                    .flex_1()
                                    .min_w_0()
                                    .truncate()
                                    .text_color(theme::fg())
                                    .child(model_label(&m.model)),
                            )
                            .child(div().text_color(theme::muted()).child(format!(
                                "{} · {}",
                                compact(m.tokens),
                                if m.cost_usd > 0. {
                                    cost(m.cost_usd)
                                } else {
                                    "—".into()
                                }
                            )))
                    }))
                    .when(r.cost_usd > 0., |el| {
                        el.child(
                            h_flex()
                                .gap_2()
                                .items_end()
                                .child(
                                    div()
                                        .text_xl()
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::fg())
                                        .child(cost(r.cost_usd)),
                                )
                                .child(
                                    div()
                                        .text_sm()
                                        .text_color(theme::muted())
                                        .child(t("recap.spent")),
                                )
                                .when(r.estimated_usd > 0., |el| {
                                    el.child(controls::badge(
                                        if r.estimated_usd >= r.cost_usd * 0.9999 {
                                            t("recap.spendEstimated")
                                        } else {
                                            t("recap.spendPartly")
                                        },
                                        theme::starting(),
                                    ))
                                }),
                        )
                    })
                    .when(r.unpriced_tokens > 0, |el| {
                        el.child(div().text_xs().text_color(theme::faint()).child(tv(
                            "recap.unpriced",
                            &[("tokens", compact(r.unpriced_tokens).into())],
                        )))
                    }),
            )
            .into_any_element()
    }
}

fn legend(color: Hsla, label: String) -> AnyElement {
    h_flex()
        .gap_1()
        .text_xs()
        .text_color(theme::muted())
        .child(div().size_2().rounded_sm().bg(color))
        .child(label)
        .into_any_element()
}

impl Render for RecapView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let range = match &self.recap {
            Some(Ok(r)) => Self::range(r),
            _ => String::new(),
        };
        div()
            .id("recap")
            .track_focus(&self.focus)
            .absolute()
            .inset_0()
            .bg(gpui::black().opacity(0.55))
            .flex()
            .items_center()
            .justify_center()
            .on_key_down(cx.listener(|_, ev: &KeyDownEvent, _, cx| {
                if ev.keystroke.key == "escape" {
                    cx.emit(RecapEvent::Close);
                    cx.stop_propagation();
                }
            }))
            .on_mouse_down(
                MouseButton::Left,
                cx.listener(|_, _, _, cx| cx.emit(RecapEvent::Close)),
            )
            .child(
                v_flex()
                    .id("recap-card")
                    .w(px(820.))
                    .max_w(relative(0.94))
                    .max_h(relative(0.92))
                    .overflow_y_scroll()
                    .gap_4()
                    .p_6()
                    .rounded_xl()
                    .bg(theme::panel())
                    .border_1()
                    .border_color(theme::border())
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        h_flex()
                            .gap_2()
                            .items_start()
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_2xl()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::fg())
                                            .child(t("recap.title")),
                                    )
                                    .child(
                                        div()
                                            .text_sm()
                                            .text_color(theme::muted())
                                            .child(t("recap.description")),
                                    ),
                            )
                            .child(controls::button(
                                "recap-prev",
                                "‹",
                                Btn::Ghost,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| this.go(1, cx)),
                            ))
                            .child(div().pt_1().text_sm().text_color(theme::fg()).child(range))
                            .child(controls::button(
                                "recap-next",
                                "›",
                                Btn::Ghost,
                                self.weeks == 0,
                                cx.listener(|this, _: &ClickEvent, _, cx| this.go(-1, cx)),
                            ))
                            .child(controls::button(
                                "recap-close",
                                "✕",
                                Btn::Ghost,
                                false,
                                cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(RecapEvent::Close)),
                            )),
                    )
                    .child(match &self.recap {
                        None => div()
                            .py_8()
                            .text_sm()
                            .text_color(theme::faint())
                            .child(t("common.loading"))
                            .into_any_element(),
                        Some(Err(e)) => div()
                            .text_sm()
                            .text_color(theme::error())
                            .child(e.clone())
                            .into_any_element(),
                        Some(Ok(r)) => self.body(r),
                    }),
            )
    }
}

#[cfg(test)]
mod tests {
    use super::{compact, duration, model_label};

    #[test]
    fn numbers_read_as_recap_ts() {
        assert_eq!(duration(42 * 3600), "42h");
        assert_eq!(duration(3 * 3600 + 1800), "3.5h");
        assert_eq!(duration(45 * 60), "45m");
        assert_eq!(compact(940), "940");
        assert_eq!(compact(12_400), "12k");
        assert_eq!(compact(1_200_000), "1.2M");
        assert_eq!(model_label("claude-opus-4-5"), "Opus 4.5");
        assert_eq!(model_label("claude-sonnet-5-20261001"), "Sonnet 5");
        assert_eq!(model_label("gpt-5"), "gpt-5");
    }
}
