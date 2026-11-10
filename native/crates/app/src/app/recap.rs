//! Your week (components/RecapView.tsx, lib/recap.ts): what the agents did
//! in seven days (GET /v1/recap), a week at a time back from this one —
//! their hours of work and how much of it while you were away, the agents,
//! pull requests, lines, streak, longest agent and busiest project, each day
//! and each hour, and tokens and cost by model. Opened from Work's "Your week"
//! over the page, with the card to share the week (recap_card.rs), copied or
//! saved as a PNG.
use std::sync::Arc;

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
    /// The share card's options, and the card drawn for them.
    card_opts: super::recap_card::Opts,
    card: Option<(super::recap_card::Opts, Arc<Image>)>,
    /// "copied" or "saved", for a moment after.
    done: Option<&'static str>,
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
pub(super) fn model_label(model: &str) -> String {
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
            card_opts: super::recap_card::Opts {
                square: false,
                names: false,
            },
            card: None,
            done: None,
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
                    this.card = None;
                    this.draw_card(cx);
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
        cx.notify();
    }

    /// draw_card draws the share card for the options set, off the main
    /// thread (drawCard).
    fn draw_card(&mut self, cx: &mut Context<Self>) {
        let Some(Ok(r)) = &self.recap else { return };
        let (r, o, range) = (r.clone(), self.card_opts, Self::range(r));
        cx.spawn(async move |this, cx| {
            let png = cx
                .background_spawn(async move { super::recap_card::png(&r, o, &range) })
                .await;
            this.update(cx, |this, cx| {
                match png {
                    Ok(bytes) if this.card_opts == o => {
                        this.card = Some((o, Arc::new(Image::from_bytes(ImageFormat::Png, bytes))));
                    }
                    Ok(_) => {}
                    Err(e) => crate::toasts::error(cx, e.to_string()),
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    fn set_card(&mut self, o: super::recap_card::Opts, cx: &mut Context<Self>) {
        if self.card_opts != o {
            self.card_opts = o;
            self.draw_card(cx);
            cx.notify();
        }
    }

    fn flash(&mut self, what: &'static str, cx: &mut Context<Self>) {
        self.done = Some(what);
        cx.notify();
        cx.spawn(async move |this, cx| {
            cx.background_executor()
                .timer(std::time::Duration::from_millis(1800))
                .await;
            this.update(cx, |this, cx| {
                if this.done == Some(what) {
                    this.done = None;
                    cx.notify();
                }
            })
            .ok();
        })
        .detach();
    }

    /// copy_card puts the card on the clipboard as a PNG.
    fn copy_card(&mut self, cx: &mut Context<Self>) {
        let Some((_, img)) = &self.card else { return };
        cx.write_to_clipboard(ClipboardItem::new_image(img));
        // GPUI's X11 clipboard holds text only: there xclip, where it is
        // installed, holds the PNG.
        #[cfg(target_os = "linux")]
        if std::env::var_os("WAYLAND_DISPLAY").is_none() {
            use std::io::Write as _;
            if let Ok(mut child) = std::process::Command::new("xclip")
                .args(["-selection", "clipboard", "-t", "image/png", "-i"])
                .stdin(std::process::Stdio::piped())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .spawn()
            {
                if let Some(mut stdin) = child.stdin.take() {
                    let _ = stdin.write_all(img.bytes());
                }
                std::thread::spawn(move || child.wait());
            }
        }
        self.flash("copied", cx);
    }

    /// save_card asks where, then writes the card's PNG there.
    fn save_card(&mut self, cx: &mut Context<Self>) {
        let (Some((o, img)), Some(Ok(r))) = (&self.card, &self.recap) else {
            return;
        };
        let name = super::recap_card::file_name(r, o.square);
        let bytes = img.bytes().to_vec();
        let dir = std::env::var_os("HOME")
            .or_else(|| std::env::var_os("USERPROFILE"))
            .map(std::path::PathBuf::from)
            .unwrap_or_default();
        let rx = cx.prompt_for_new_path(&dir, Some(&name));
        cx.spawn(async move |this, cx| {
            let Ok(Ok(Some(path))) = rx.await else { return };
            let res = std::fs::write(&path, bytes);
            this.update(cx, |this, cx| match res {
                Ok(()) => this.flash("saved", cx),
                Err(e) => crate::toasts::error(cx, e.to_string()),
            })
            .ok();
        })
        .detach();
    }

    /// render_share is Share your week (RecapView ShareCard): the card's
    /// shape and names, the card, and Copy image and Save PNG.
    fn render_share(&self, cx: &mut Context<Self>) -> AnyElement {
        let o = self.card_opts;
        let card = self
            .card
            .as_ref()
            .filter(|(at, _)| *at == o)
            .map(|(_, i)| i.clone());
        let (w, h) = super::recap_card::size(o.square);
        let max_w = if o.square { 544. } else { 896. };
        panel()
            .gap_4()
            .child(
                h_flex()
                    .flex_wrap()
                    .items_center()
                    .gap_3()
                    .child(
                        v_flex()
                            .min_w_0()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_1p5()
                                    .text_size(theme::text::HEADING)
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::title())
                                    .child(
                                        crate::icons::ic("sparkles")
                                            .size_4()
                                            .text_color(theme::brand(400)),
                                    )
                                    .child(t("recap.share.title")),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::UI)
                                    .text_color(theme::muted())
                                    .child(t("recap.share.description")),
                            ),
                    )
                    .child(div().flex_1())
                    .child(controls::segmented(
                        "recap-shape",
                        vec![
                            ("landscape".into(), t("recap.share.landscape")),
                            ("square".into(), t("recap.share.square")),
                        ],
                        Some(if o.square { "square" } else { "landscape" }),
                        false,
                        std::rc::Rc::new(cx.listener(move |this, v: &str, _, cx| {
                            this.set_card(
                                super::recap_card::Opts {
                                    square: v == "square",
                                    ..this.card_opts
                                },
                                cx,
                            )
                        })),
                    ))
                    .child(
                        h_flex()
                            .gap_2()
                            .px_2()
                            .py_1()
                            .text_size(theme::text::UI)
                            .text_color(theme::secondary())
                            .child(controls::toggle(
                                "recap-names",
                                o.names,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.set_card(
                                        super::recap_card::Opts {
                                            names: !this.card_opts.names,
                                            ..this.card_opts
                                        },
                                        cx,
                                    )
                                }),
                            ))
                            .child(t("recap.share.names")),
                    ),
            )
            .child(
                div()
                    .flex()
                    .justify_center()
                    .p_4()
                    .rounded(theme::radius::XXL)
                    .border_1()
                    .border_color(theme::line())
                    .bg(theme::surface())
                    .child(
                        div()
                            .w_full()
                            .max_w(px(max_w))
                            .aspect_ratio(w as f32 / h as f32)
                            .rounded_xl()
                            .overflow_hidden()
                            .bg(theme::surface_strong())
                            .shadow(theme::shadow_modal())
                            .when_some(card, |el, c| {
                                el.child(img(c).size_full().object_fit(ObjectFit::Contain))
                            }),
                    ),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .items_center()
                    .gap_2()
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::subtle())
                            .child(if o.names {
                                t("recap.share.namesOn")
                            } else {
                                t("recap.share.namesOff")
                            }),
                    )
                    .child(controls::button(
                        "recap-copy",
                        if self.done == Some("copied") {
                            t("recap.share.copied")
                        } else {
                            t("recap.share.copy")
                        },
                        Btn::Secondary,
                        self.card.is_none(),
                        cx.listener(|this, _: &ClickEvent, _, cx| this.copy_card(cx)),
                    ))
                    .child(controls::button(
                        "recap-save",
                        if self.done == Some("saved") {
                            t("recap.share.saved")
                        } else {
                            t("recap.share.save")
                        },
                        Btn::Primary,
                        self.card.is_none(),
                        cx.listener(|this, _: &ClickEvent, _, cx| this.save_card(cx)),
                    )),
            )
            .into_any_element()
    }

    fn go(&mut self, by: i64, cx: &mut Context<Self>) {
        self.weeks = (self.weeks + by).max(0);
        self.load(cx);
    }

    /// range is the week's dates: Oct 18 – Oct 24.
    pub(super) fn range(r: &Recap) -> String {
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

/// panel is Electron's Panel: the `.panel` ground, rounded-2xl, p-5.
fn panel() -> Div {
    v_flex()
        .min_w_0()
        .p_5()
        .rounded(theme::radius::XXL)
        .border_1()
        .border_color(theme::line())
        .bg(theme::linear_panel())
        .shadow(theme::shadow_card())
}

/// stat is one of the hero's tiles (RecapView Stat): an icon and a label, the
/// number, and what it's made of under it.
fn stat(icon: &str, label: String, value: String, sub: impl IntoElement) -> AnyElement {
    v_flex()
        .flex_1()
        .min_w(px(150.))
        .px_3p5()
        .py_3()
        .rounded(theme::radius::XXL)
        .border_1()
        .border_color(theme::line())
        .bg(theme::surface())
        .child(
            h_flex()
                .gap_1p5()
                .min_w_0()
                .text_size(theme::text::CAPTION)
                .font_weight(FontWeight::SEMIBOLD)
                .text_color(theme::muted())
                .child(
                    crate::icons::ic(icon)
                        .size_3p5()
                        .flex_none()
                        .text_color(theme::brand(400)),
                )
                .child(div().min_w_0().truncate().child(label)),
        )
        .child(
            div()
                .mt_1()
                .text_size(theme::text::DISPLAY)
                .font_weight(FontWeight::BOLD)
                .text_color(theme::title())
                .truncate()
                .child(value),
        )
        .child(
            div()
                .text_size(theme::text::CAPTION)
                .text_color(theme::subtle())
                .truncate()
                .child(sub),
        )
        .into_any_element()
}

fn heading(text: String) -> Div {
    div()
        .text_size(theme::text::UI)
        .font_weight(FontWeight::SEMIBOLD)
        .text_color(theme::title())
        .child(text)
}

impl RecapView {
    fn body(&self, r: &Recap) -> AnyElement {
        if r.work_seconds == 0 && r.agents == 0 && r.tokens == 0 {
            return panel()
                .items_center()
                .gap_2()
                .px_6()
                .py_16()
                .child(
                    crate::icons::ic("moon")
                        .size_6()
                        .text_color(theme::brand(400)),
                )
                .child(
                    div()
                        .text_size(theme::text::BODY)
                        .text_color(theme::muted())
                        .child(t("recap.empty")),
                )
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
        let most_tokens = r.models.iter().map(|m| m.tokens).max().unwrap_or(0).max(1);
        let peak = r
            .hours
            .iter()
            .enumerate()
            .filter(|(_, s)| **s > 0)
            .max_by_key(|(_, s)| **s)
            .map(|(h, _)| format!("{h:02}:00"));
        let today = r.days.len().saturating_sub(1);
        let days = r.days.iter().enumerate().map(|(i, d)| {
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
            let with_you = (d.work_seconds - d.away_seconds).max(0) as f32;
            let away = d.away_seconds as f32;
            let tall = (d.work_seconds as f32 / most_day as f32).max(0.);
            v_flex()
                .id(SharedString::from(format!("recap-day-{}", d.date)))
                .flex_1()
                .h_full()
                .items_center()
                .justify_end()
                .gap_1p5()
                .tooltip(move |w, cx| {
                    gpui_component::tooltip::Tooltip::new(tip.clone()).build(w, cx)
                })
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .text_color(theme::subtle())
                        .child(if d.work_seconds > 0 {
                            duration(d.work_seconds)
                        } else {
                            String::new()
                        }),
                )
                .child(
                    v_flex()
                        .w_full()
                        .max_w(px(44.))
                        .h(px(118. * tall))
                        .min_h(px(4.))
                        .gap(px(2.))
                        .overflow_hidden()
                        .rounded_t(theme::radius::MD)
                        .rounded_b(px(2.))
                        .when(d.work_seconds == 0, |el| {
                            el.child(div().h_1().bg(theme::surface_strong()))
                        })
                        .when(with_you > 0., |el| {
                            el.child(
                                div()
                                    .w_full()
                                    .flex_1()
                                    .flex_basis(relative(with_you / (with_you + away)))
                                    .bg(theme::brand(300)),
                            )
                        })
                        .when(away > 0., |el| {
                            el.child(
                                div()
                                    .w_full()
                                    .flex_1()
                                    .flex_basis(relative(away / (with_you + away)))
                                    .bg(theme::brand(500)),
                            )
                        }),
                )
                .child(
                    div()
                        .text_size(theme::text::CAPTION)
                        .map(|el| {
                            if i == today {
                                el.font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::primary())
                            } else {
                                el.text_color(theme::muted())
                            }
                        })
                        .child(name),
                )
        });
        let hours = r.hours.iter().enumerate().map(|(h, s)| {
            div()
                .id(("recap-hour", h))
                .flex_1()
                .h(px(28.))
                .rounded(theme::radius::SM)
                .bg(theme::brand(500))
                .opacity(if *s > 0 {
                    0.18 + 0.82 * *s as f32 / most_hour as f32
                } else {
                    0.06
                })
        });
        let lines = h_flex()
            .gap_1()
            .child(
                div()
                    .text_color(theme::emerald(300))
                    .child(format!("+{}", compact(r.insertions))),
            )
            .child(
                div()
                    .text_color(theme::rose(300))
                    .child(format!("−{}", compact(r.deletions))),
            );
        let hero = v_flex()
            .relative()
            .overflow_hidden()
            .gap_6()
            .p_6()
            .rounded(px(24.))
            .border_1()
            .border_color(theme::line())
            .bg(gpui::linear_gradient(
                135.,
                gpui::linear_color_stop(theme::brand(500).opacity(0.16), 0.),
                gpui::linear_color_stop(theme::brand(700).opacity(0.04), 1.),
            ))
            .shadow(theme::glow(theme::brand(500), 0.6))
            .child(
                v_flex()
                    .min_w_0()
                    .child(
                        h_flex()
                            .flex_wrap()
                            .items_baseline()
                            .gap_x_3()
                            .child(
                                div()
                                    .text_size(px(64.))
                                    .line_height(relative(1.))
                                    .font_weight(FontWeight::EXTRA_BOLD)
                                    .text_color(theme::title())
                                    .child(duration(r.work_seconds)),
                            )
                            .child(
                                div()
                                    .text_size(theme::text::HEADING)
                                    .font_weight(FontWeight::SEMIBOLD)
                                    .text_color(theme::primary())
                                    .child(t("recap.card.agentWork")),
                            ),
                    )
                    .child(
                        h_flex()
                            .id("recap-away")
                            .mt_2()
                            .gap_2()
                            .text_size(theme::text::HEADING)
                            .text_color(theme::brand(300))
                            .tooltip(|w, cx| {
                                gpui_component::tooltip::Tooltip::new(t("recap.awayHint"))
                                    .build(w, cx)
                            })
                            .child(
                                crate::icons::ic("moon")
                                    .size_4()
                                    .flex_none()
                                    .text_color(theme::amber(200)),
                            )
                            .child(tv(
                                "recap.awayYou",
                                &[
                                    ("hours", duration(r.away_seconds).into()),
                                    ("share", share.into()),
                                ],
                            )),
                    )
                    .when(r.cost_usd > 0., |el| {
                        el.child(
                            h_flex()
                                .mt_5()
                                .flex_wrap()
                                .items_baseline()
                                .gap_x_2p5()
                                .gap_y_1()
                                .child(
                                    div()
                                        .text_size(px(36.))
                                        .font_weight(FontWeight::BOLD)
                                        .text_color(theme::title())
                                        .child(cost(r.cost_usd)),
                                )
                                .child(
                                    div()
                                        .text_size(theme::text::HEADING)
                                        .font_weight(FontWeight::SEMIBOLD)
                                        .text_color(theme::primary())
                                        .child(t("recap.spent")),
                                )
                                .when(r.estimated_usd > 0., |el| {
                                    el.child(
                                        div()
                                            .px_2()
                                            .py_0p5()
                                            .rounded_full()
                                            .border_1()
                                            .border_color(theme::amber(200).opacity(0.3))
                                            .bg(theme::amber(200).opacity(0.1))
                                            .text_size(theme::text::CAPTION)
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::amber(200))
                                            .child(if r.estimated_usd >= r.cost_usd * 0.9999 {
                                                t("recap.spendEstimated")
                                            } else {
                                                t("recap.spendPartly")
                                            }),
                                    )
                                }),
                        )
                    })
                    .when(r.unpriced_tokens > 0, |el| {
                        el.child(
                            div()
                                .mt_1()
                                .text_size(theme::text::UI)
                                .text_color(theme::subtle())
                                .child(tv(
                                    "recap.unpriced",
                                    &[("tokens", compact(r.unpriced_tokens).into())],
                                )),
                        )
                    }),
            )
            .child(
                h_flex()
                    .flex_wrap()
                    .gap_2p5()
                    .child(stat(
                        "bot",
                        t("recap.agents"),
                        compact(r.agents),
                        tv("recap.projects", &[("count", r.projects.into())]),
                    ))
                    .child(stat(
                        "git-pull-request",
                        t("recap.prs"),
                        compact(r.prs_opened),
                        tv("recap.merged", &[("count", r.prs_merged.into())]),
                    ))
                    .child(stat(
                        "file-diff",
                        t("recap.lines"),
                        compact(r.insertions + r.deletions),
                        lines,
                    ))
                    .child(stat(
                        "zap",
                        t("recap.streak"),
                        tv("recap.days", &[("count", r.streak.into())]),
                        tv("recap.best", &[("count", r.best_streak.into())]),
                    ))
                    .child(stat(
                        "clock",
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
                        "git-merge",
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
            );
        let activity = panel()
            .child(
                h_flex()
                    .mb_3()
                    .flex_wrap()
                    .gap_x_4()
                    .gap_y_1()
                    .child(heading(t("recap.eachDay")))
                    .child(div().flex_1())
                    .child(
                        h_flex()
                            .gap_3()
                            .child(legend(theme::brand(500), t("recap.whileAway")))
                            .child(legend(theme::brand(300), t("recap.withYou"))),
                    ),
            )
            .child(h_flex().h(px(176.)).gap_2().items_end().children(days))
            .child(
                h_flex()
                    .mt_6()
                    .mb_2()
                    .gap_3()
                    .child(heading(t("recap.byHour")))
                    .child(div().flex_1())
                    .when_some(peak, |el, p| {
                        el.child(
                            div()
                                .text_size(theme::text::CAPTION)
                                .text_color(theme::muted())
                                .child(tv("recap.peak", &[("hour", p.into())])),
                        )
                    }),
            )
            .child(h_flex().gap(px(3.)).children(hours));
        let models = panel()
            .child(
                h_flex()
                    .mb_3()
                    .gap_3()
                    .child(heading(t("recap.models")))
                    .child(div().flex_1())
                    .child(
                        div()
                            .text_size(theme::text::CAPTION)
                            .text_color(theme::muted())
                            .child(tv("recap.tokens", &[("tokens", compact(r.tokens).into())])),
                    ),
            )
            .when(r.models.is_empty(), |el| {
                el.child(
                    div()
                        .text_size(theme::text::UI)
                        .text_color(theme::subtle())
                        .child(t("recap.noTokens")),
                )
            })
            .child(v_flex().gap_3().children(r.models.iter().map(|m| {
                let width = (m.tokens as f32 / most_tokens as f32).max(0.02);
                v_flex()
                    .min_w_0()
                    .child(
                        h_flex()
                            .gap_2()
                            .justify_between()
                            .text_size(theme::text::UI)
                            .child(
                                div()
                                    .min_w_0()
                                    .truncate()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::primary())
                                    .child(model_label(&m.model)),
                            )
                            .child(div().flex_none().text_color(theme::muted()).child(format!(
                                "{} · {}",
                                compact(m.tokens),
                                if m.cost_usd > 0. {
                                    cost(m.cost_usd)
                                } else {
                                    "—".into()
                                }
                            ))),
                    )
                    .child(
                        div()
                            .mt_1p5()
                            .h(px(6.))
                            .rounded_full()
                            .bg(theme::surface_strong())
                            .child(div().h_full().w(relative(width)).rounded_full().bg(
                                gpui::linear_gradient(
                                    90.,
                                    gpui::linear_color_stop(theme::brand(600), 0.),
                                    gpui::linear_color_stop(theme::brand(400), 1.),
                                ),
                            )),
                    )
            })));
        v_flex()
            .gap_4()
            .child(crate::motion::enter(div().child(hero), "recap-hero-in", 0))
            .child(crate::motion::enter(
                h_flex()
                    .flex_wrap()
                    .items_start()
                    .gap_4()
                    .child(
                        div()
                            .flex_1()
                            .flex_basis(px(380.))
                            .min_w_0()
                            .child(activity),
                    )
                    .child(div().flex_1().flex_basis(px(300.)).min_w_0().child(models)),
                "recap-panels-in",
                1,
            ))
            .into_any_element()
    }
}

fn legend(color: Hsla, label: String) -> AnyElement {
    h_flex()
        .gap_1p5()
        .text_size(theme::text::CAPTION)
        .text_color(theme::muted())
        .child(div().size_2().rounded(px(2.)).bg(color))
        .child(label)
        .into_any_element()
}

impl Render for RecapView {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let range = match &self.recap {
            Some(Ok(r)) => Self::range(r),
            _ => String::new(),
        };
        crate::motion::scrim_in(div()
            .id("recap")
            .track_focus(&self.focus)
            .absolute()
            .inset_0()
            .bg(theme::scrim())
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
            .child(crate::motion::pop_in(
                v_flex()
                    .id("recap-card")
                    .w(px(960.))
                    .max_w(relative(0.94))
                    .max_h(relative(0.92))
                    .overflow_y_scroll()
                    .gap_4()
                    .px_8()
                    .py_7()
                    .rounded(theme::radius::XXL)
                    .bg(theme::modal())
                    .border_1()
                    .border_color(theme::line_strong())
                    .shadow(theme::shadow_modal())
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        h_flex()
                            .flex_wrap()
                            .gap_3()
                            .items_end()
                            .mb_2()
                            .child(
                                v_flex()
                                    .flex_1()
                                    .min_w_0()
                                    .gap_1()
                                    .child(
                                        div()
                                            .text_size(theme::text::DISPLAY)
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::title())
                                            .child(t("recap.title")),
                                    )
                                    .child(
                                        div()
                                            .text_size(theme::text::BODY)
                                            .text_color(theme::muted())
                                            .child(t("recap.description")),
                                    ),
                            )
                            .child(
                                h_flex()
                                    .gap_1()
                                    .p_1()
                                    .rounded_xl()
                                    .border_1()
                                    .border_color(theme::line())
                                    .bg(theme::surface_raised())
                                    .child(controls::button(
                                        "recap-prev",
                                        "‹",
                                        Btn::Ghost,
                                        false,
                                        cx.listener(|this, _: &ClickEvent, _, cx| this.go(1, cx)),
                                    ))
                                    .child(
                                        div()
                                            .min_w(px(144.))
                                            .px_1()
                                            .text_center()
                                            .text_size(theme::text::UI)
                                            .font_weight(FontWeight::MEDIUM)
                                            .text_color(theme::secondary())
                                            .child(if range.is_empty() { "…".to_string() } else { range }),
                                    )
                                    .child(controls::button(
                                        "recap-next",
                                        "›",
                                        Btn::Ghost,
                                        self.weeks == 0,
                                        cx.listener(|this, _: &ClickEvent, _, cx| this.go(-1, cx)),
                                    )),
                            )
                            .child(controls::button(
                                "recap-close",
                                "✕",
                                Btn::Ghost,
                                false,
                                cx.listener(|_, _: &ClickEvent, _, cx| cx.emit(RecapEvent::Close)),
                            )),
                    )
                    .child(match &self.recap {
                        None => v_flex()
                            .gap_4()
                            .child(
                                div()
                                    .rounded(px(24.))
                                    .overflow_hidden()
                                    .child(crate::motion::skeleton("recap-skel-hero", relative(1.), px(192.))),
                            )
                            .child(
                                div()
                                    .rounded(theme::radius::XXL)
                                    .overflow_hidden()
                                    .child(crate::motion::skeleton("recap-skel-days", relative(1.), px(288.))),
                            )
                            .into_any_element(),
                        Some(Err(e)) => controls::error_line(e.clone()),
                        Some(Ok(r)) => self.body(r),
                    })
                    .when(matches!(&self.recap, Some(Ok(r)) if r.work_seconds > 0 || r.agents > 0 || r.tokens > 0), |el| {
                        el.child(crate::motion::enter(
                            div().child(self.render_share(cx)),
                            "recap-share-in",
                            2,
                        ))
                    }),
                "recap-card-pop",
            ))
    , "idrecap-in")
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
