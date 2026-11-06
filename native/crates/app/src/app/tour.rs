//! What the window shows once, over everything: the welcome tour on a first
//! start (components/v2/Tour.tsx, lib/tours.ts' welcomeTour) and What's new
//! after an update (WhatsNewDialog.tsx, lib/whatsnew.ts).
//!
//! The tour is the welcome's six steps, each a spotlight on what it points
//! at in the real window (crate::spot measures it), the rest dimmed and the
//! card beside it, or the card in the middle with nothing to point at; a
//! click outside the card ends it. It plays once, after the first run when there is one,
//! and this computer remembers it was seen (crate::prefs), as Electron's
//! localStorage does. What's new is the release notes, from CHANGELOG.md as
//! the app was built, from the daemon's version down; it opens by itself
//! when that version changed since the last start, never on the first.
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::text::TextView;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::controls::{Btn, button};
use crate::{prefs, theme};

const STEPS: [&str; 6] = ["welcome", "sidebar", "lead", "cards", "review", "help"];

/// anchor_of is what a step points at (lib/tours.ts' anchors), the first
/// one on screen: a project's row, the chat's composer, the Help button.
fn anchor_of(step: &str) -> Option<&'static [&'static str]> {
    match step {
        "sidebar" => Some(&["project:*", "sidebar-nav"]),
        "lead" | "cards" => Some(&["composer"]),
        "help" => Some(&["help"]),
        _ => None,
    }
}

/// place_card puts the tour's card beside its target (lib/tours.ts
/// placeCard): right of it, else left, else below, else above; in the
/// middle with none.
fn place_card(
    target: Option<Bounds<Pixels>>,
    card: Size<Pixels>,
    vp: Size<Pixels>,
) -> Point<Pixels> {
    let (gap, margin) = (px(14.), px(12.));
    let clamp = |v: Pixels, lo: Pixels, hi: Pixels| v.min(hi.max(lo)).max(lo);
    let centre_x = (vp.width - card.width) / 2.;
    let Some(t) = target else {
        return point(
            centre_x.max(margin),
            ((vp.height - card.height) / 2.).max(margin),
        );
    };
    let right = t.origin.x + t.size.width;
    let bottom = t.origin.y + t.size.height;
    let along_y = clamp(t.origin.y, margin, vp.height - card.height - margin);
    let along_x = clamp(
        t.origin.x + t.size.width / 2. - card.width / 2.,
        margin,
        vp.width - card.width - margin,
    );
    if vp.width >= px(640.) {
        if right + gap + card.width <= vp.width - margin {
            return point(right + gap, along_y);
        }
        if t.origin.x - gap - card.width >= margin {
            return point(t.origin.x - gap - card.width, along_y);
        }
        if bottom + gap + card.height <= vp.height - margin {
            return point(along_x, bottom + gap);
        }
        if t.origin.y - gap - card.height >= margin {
            return point(along_x, t.origin.y - gap - card.height);
        }
    }
    let low = t.origin.y + t.size.height / 2. > vp.height / 2.;
    point(
        centre_x.max(margin),
        if low {
            margin
        } else {
            (vp.height - card.height - margin).max(margin)
        },
    )
}
/// TOUR keeps how the welcome tour ended: "seen" or "dismissed".
const TOUR: &str = "tour";
/// NEWS is the version What's new was last shown for.
const NEWS: &str = "whatsNewSeen";
const CHANGELOG: &str = include_str!("../../../../../CHANGELOG.md");

#[derive(Default)]
pub(super) struct Onboarding {
    /// tour is the step playing.
    tour: Option<usize>,
    /// news is the version What's new is open for.
    news: Option<String>,
    /// focus takes the tour's keys; focused is whether it has been given it.
    focus: Option<FocusHandle>,
    focused: bool,
}

/// markup turns a step's <b> and <k> into Markdown.
fn markup(text: &str) -> String {
    text.replace("<b>", "**")
        .replace("</b>", "**")
        .replace("<k>", "`")
        .replace("</k>", "`")
}

/// sections are the changelog's releases from version down (sectionsFrom),
/// at most a few: every one when the version isn't in it (a build from
/// main).
fn sections(version: &str) -> Vec<(String, String)> {
    let mut out: Vec<(String, String)> = Vec::new();
    for line in CHANGELOG.lines() {
        if let Some(rest) = line.strip_prefix("## [") {
            let v = rest.split(']').next().unwrap_or_default().to_string();
            out.push((v, String::new()));
        } else if let Some((_, body)) = out.last_mut() {
            body.push_str(line);
            body.push('\n');
        }
    }
    let released: Vec<_> = out.into_iter().filter(|(v, _)| v != "Unreleased").collect();
    let at = released.iter().position(|(v, _)| v == version).unwrap_or(0);
    released.into_iter().skip(at).take(3).collect()
}

impl AppView {
    /// on_first_answer is the daemon answering for the first time: the
    /// first run, else the tour or What's new.
    pub(super) fn on_first_answer(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.check_first_run(window, cx);
    }

    /// after_first_run is the app's own start once there's no first run to
    /// show: the welcome tour on a first start, What's new after an update.
    pub(super) fn after_first_run(&mut self, cx: &mut Context<Self>) {
        let version = self.conn.version.clone().unwrap_or_default();
        let seen = prefs::get(NEWS).and_then(|v| v.as_str().map(str::to_string));
        if prefs::get(TOUR).is_none() {
            self.start_tour(cx);
        } else if seen.as_ref().is_some_and(|s| *s != version) && !version.is_empty() {
            self.onboarding.news = Some(version.clone());
        }
        if !version.is_empty() {
            prefs::set(NEWS, version.into());
        }
        cx.notify();
    }

    pub(super) fn onboarding_open(&self) -> bool {
        self.onboarding.tour.is_some() || self.onboarding.news.is_some()
    }

    pub(super) fn start_tour(&mut self, cx: &mut Context<Self>) {
        if prefs::get(TOUR).is_none() {
            self.onboarding.tour = Some(0);
            self.onboarding.focus = Some(cx.focus_handle());
            self.onboarding.focused = false;
            cx.notify();
        }
    }

    /// replay_tour plays the tour again, from Help.
    pub(super) fn replay_tour(&mut self, cx: &mut Context<Self>) {
        self.onboarding.tour = Some(0);
        self.onboarding.focus = Some(cx.focus_handle());
        self.onboarding.focused = false;
        cx.notify();
    }

    /// show_whats_new opens What's new for this version, from Help.
    pub(super) fn show_whats_new(&mut self, version: String, cx: &mut Context<Self>) {
        self.onboarding.news = Some(version);
        cx.notify();
    }

    fn tour_to(&mut self, step: Option<usize>, cx: &mut Context<Self>) {
        match step {
            Some(i) if i < STEPS.len() => {
                self.onboarding.tour = Some(i);
                // A step about the chat shows the chat (a step's go: 'lead'):
                // Settings, Work or a foot page give way to it.
                if matches!(STEPS[i], "lead" | "cards") {
                    self.settings = false;
                    self.work = None;
                    self.side.page = None;
                }
            }
            Some(_) => {
                self.onboarding.tour = None;
                prefs::set(TOUR, "seen".into());
            }
            None => {
                self.onboarding.tour = None;
                prefs::set(TOUR, "dismissed".into());
            }
        }
        cx.notify();
    }

    /// tour_key is ←, → and Esc while the tour plays.
    pub(super) fn tour_key(&mut self, ev: &KeyDownEvent, cx: &mut Context<Self>) -> bool {
        let Some(i) = self.onboarding.tour else {
            return false;
        };
        match ev.keystroke.key.as_str() {
            "right" | "enter" => self.tour_to(Some(i + 1), cx),
            "left" => self.tour_to(Some(i.saturating_sub(1)), cx),
            "escape" => self.tour_to(None, cx),
            _ => return false,
        }
        true
    }

    /// focus_tour gives a tour that just started the keyboard.
    pub(super) fn focus_tour(&mut self, window: &mut Window, cx: &mut App) {
        let o = &mut self.onboarding;
        if o.tour.is_some()
            && !o.focused
            && let Some(h) = &o.focus
        {
            window.focus(h, cx);
            o.focused = true;
        }
    }

    pub(super) fn onboarding_touring(&self) -> bool {
        self.onboarding.tour.is_some()
    }

    pub(super) fn render_onboarding(
        &self,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> Option<AnyElement> {
        if let Some(i) = self.onboarding.tour {
            return Some(self.render_tour(i, window, cx));
        }
        let version = self.onboarding.news.clone()?;
        Some(self.render_whats_new(&version, cx))
    }

    fn render_tour(&self, i: usize, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let step = STEPS[i];
        let last = i + 1 == STEPS.len();
        let card = v_flex()
            .id("tour-card")
            .w(px(400.))
            .max_w(relative(0.92))
            .gap_3()
            .p_5()
            .rounded_xl()
            .bg(theme::card())
            .border_1()
            .border_color(theme::accent().opacity(0.5))
            .shadow_lg()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .child(div().text_xs().text_color(theme::accent()).child(tv(
                "tour.progress",
                &[("step", (i + 1).into()), ("total", STEPS.len().into())],
            )))
            .child(
                div()
                    .text_base()
                    .font_weight(FontWeight::SEMIBOLD)
                    .child(t(&format!("tour.{step}.title"))),
            )
            .child(
                div()
                    .text_sm()
                    .text_color(theme::muted())
                    .child(TextView::markdown(
                        SharedString::from(format!("tour-body-{step}")),
                        markup(&t(&format!("tour.{step}.body"))),
                    )),
            )
            .child(
                h_flex()
                    .gap_2()
                    .child(button(
                        "tour-skip",
                        t("tour.skip"),
                        Btn::Ghost,
                        false,
                        cx.listener(|this, _: &ClickEvent, _, cx| this.tour_to(None, cx)),
                    ))
                    .child(div().flex_1())
                    .when(i > 0, |el| {
                        el.child(button(
                            "tour-back",
                            t("common.back"),
                            Btn::Secondary,
                            false,
                            cx.listener(move |this, _: &ClickEvent, _, cx| {
                                this.tour_to(Some(i - 1), cx)
                            }),
                        ))
                    })
                    .child(button(
                        "tour-next",
                        if last {
                            t("tour.finish")
                        } else {
                            t("common.next")
                        },
                        Btn::Primary,
                        false,
                        cx.listener(move |this, _: &ClickEvent, _, cx| {
                            this.tour_to(Some(i + 1), cx)
                        }),
                    )),
            );
        // A spotlight on what the step points at, the rest dimmed, and the
        // card beside it (placeCard); with nothing to point at, in the middle.
        let vp = window.viewport_size();
        let pad = px(6.);
        let hole = anchor_of(step).and_then(crate::spot::bounds).map(|b| {
            Bounds::new(
                point(b.origin.x - pad, b.origin.y - pad),
                size(b.size.width + pad * 2., b.size.height + pad * 2.),
            )
        });
        let at = place_card(hole, size(px(400.), px(300.)), vp);
        let skip = |el: Div| {
            el.on_mouse_down(
                MouseButton::Left,
                cx.listener(|this, _: &MouseDownEvent, _, cx| this.tour_to(None, cx)),
            )
        };
        let dim = theme::scrim();
        let shade = |x: Pixels, y: Pixels, w: Pixels, h: Pixels| {
            skip(
                div()
                    .absolute()
                    .left(x)
                    .top(y)
                    .w(w.max(px(0.)))
                    .h(h.max(px(0.)))
                    .bg(dim),
            )
        };
        let mut layer = div()
            .id("tour")
            .when_some(self.onboarding.focus.as_ref(), |el, h| el.track_focus(h))
            .on_key_down(cx.listener(|this, ev: &KeyDownEvent, _, cx| {
                if this.tour_key(ev, cx) {
                    cx.stop_propagation();
                }
            }))
            .absolute()
            .inset_0();
        match hole {
            Some(h) => {
                let (l, t_, r, b) = (
                    h.origin.x,
                    h.origin.y,
                    h.origin.x + h.size.width,
                    h.origin.y + h.size.height,
                );
                layer = layer
                    .child(shade(px(0.), px(0.), vp.width, t_))
                    .child(shade(px(0.), b, vp.width, vp.height - b))
                    .child(shade(px(0.), t_, l, h.size.height))
                    .child(shade(r, t_, vp.width - r, h.size.height))
                    .child(
                        div()
                            .absolute()
                            .left(l)
                            .top(t_)
                            .w(h.size.width)
                            .h(h.size.height)
                            .rounded_xl()
                            .border_2()
                            .border_color(theme::accent().opacity(0.7)),
                    );
            }
            None => layer = layer.child(skip(div().absolute().inset_0().bg(dim))),
        }
        layer
            .child(div().absolute().left(at.x).top(at.y).child(card))
            .into_any_element()
    }

    fn render_whats_new(&self, version: &str, cx: &mut Context<Self>) -> AnyElement {
        let list = sections(version);
        let close = cx.listener(|this, _: &MouseDownEvent, _, cx| {
            this.onboarding.news = None;
            cx.notify();
        });
        div()
            .id("whats-new")
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
                    .id("whats-new-card")
                    .w(px(576.))
                    .max_w(relative(0.92))
                    .max_h(relative(0.85))
                    .gap_4()
                    .p_5()
                    .rounded_xl()
                    .bg(theme::card())
                    .border_1()
                    .border_color(theme::line())
                    .shadow_lg()
                    .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
                    .child(
                        h_flex()
                            .gap_2()
                            .child(
                                v_flex()
                                    .flex_1()
                                    .child(
                                        div()
                                            .text_lg()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .child(t("shell.whatsNew.title")),
                                    )
                                    .child(div().text_sm().text_color(theme::muted()).child(tv(
                                        "shell.whatsNew.version",
                                        &[("version", version.into())],
                                    ))),
                            )
                            .child(button(
                                "whats-new-close",
                                t("common.close"),
                                Btn::Ghost,
                                false,
                                cx.listener(|this, _: &ClickEvent, _, cx| {
                                    this.onboarding.news = None;
                                    cx.notify();
                                }),
                            )),
                    )
                    .child(
                        v_flex()
                            .id("whats-new-body")
                            .flex_1()
                            .min_h_0()
                            .overflow_y_scroll()
                            .gap_6()
                            .when(list.is_empty(), |el| {
                                el.child(
                                    div()
                                        .text_sm()
                                        .text_color(theme::faint())
                                        .child(t("shell.whatsNew.empty")),
                                )
                            })
                            .children(list.into_iter().map(|(v, body)| {
                                v_flex()
                                    .gap_1()
                                    .child(
                                        div()
                                            .font_family(theme::mono())
                                            .text_xs()
                                            .font_weight(FontWeight::SEMIBOLD)
                                            .text_color(theme::faint())
                                            .child(v.clone()),
                                    )
                                    .child(div().text_sm().child(TextView::markdown(
                                        SharedString::from(format!("news-{v}")),
                                        body,
                                    )))
                            })),
                    ),
            )
            .into_any_element()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn the_changelog_reads_from_the_version_down() {
        let all = sections("no-such-version");
        assert!(!all.is_empty());
        let from = sections(&all[1].0);
        assert_eq!(from[0].0, all[1].0);
        assert_eq!(markup("<b>a</b> <k>b</k>"), "**a** `b`");
    }
}
