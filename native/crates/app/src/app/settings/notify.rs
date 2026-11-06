//! Settings → Notifications (NotificationSettings.tsx, DoNotDisturb.tsx):
//! which notices give a system notification and which a sound, Do not
//! disturb, and the chime's volume. They're this computer's, as Electron
//! keeps them in localStorage (lib/notifyPrefs.ts), so they live in
//! crate::prefs; app/alerts.rs reads them before each sound and system
//! notification.
use std::rc::Rc;

use agentbox_i18n::{t, tv};
use chrono::{Datelike as _, Local, TimeZone as _};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::super::AppView;
use super::super::controls::{Btn, Row, button, stepper, toggle};
use super::kit::note;
use super::{Entry, Group, entry, group};
use crate::{prefs, theme};

const DURATIONS: [&str; 3] = ["hour", "tomorrow", "forever"];

fn now_ms() -> i64 {
    Local::now().timestamp_millis()
}

/// dnd_end is when a Do not disturb asked for now ends (notifyPrefs.ts's
/// dndEnd): in an hour, at 8 tomorrow morning, or never.
fn dnd_end(d: &str, now: chrono::DateTime<Local>) -> i64 {
    match d {
        "forever" => prefs::DND_FOREVER,
        "hour" => now.timestamp_millis() + 3_600_000,
        _ => {
            let today = now.date_naive().and_hms_opt(8, 0, 0).unwrap_or_default();
            let mut at = Local
                .from_local_datetime(&today)
                .earliest()
                .map(|a| a.timestamp_millis())
                .unwrap_or(0);
            if at <= now.timestamp_millis() {
                at += 86_400_000;
            }
            at
        }
    }
}

/// until_text says when it ends: a time of day, with the weekday if it
/// isn't today.
fn until_text(until: i64) -> String {
    if until == prefs::DND_FOREVER {
        return t("notify.dnd.untilOff");
    }
    let Some(at) = Local.timestamp_millis_opt(until).single() else {
        return t("notify.dnd.untilOff");
    };
    let time = at.format("%H:%M").to_string();
    let when = if at.date_naive() == Local::now().date_naive() {
        time
    } else {
        format!("{} {time}", at.weekday())
    };
    tv("notify.dnd.until", &[("when", (&when).into())])
}

pub(super) fn notify_changed() -> bool {
    prefs::NOTIFY_KINDS.iter().any(|k| {
        prefs::NOTIFY_CHANNELS
            .iter()
            .any(|c| !prefs::notify_on(k, c))
    })
}

impl AppView {
    pub(super) fn notify_groups(&self) -> Vec<Group> {
        vec![group(
            "notifications",
            "settings.group.notifications.title",
            vec![
                entry(
                    "notify-dnd",
                    "notify.dnd.title",
                    "notify.dnd.description",
                    "notify.dnd.keywords",
                )
                .modified(prefs::dnd_until() > now_ms()),
                entry(
                    "notify-kinds",
                    "settings.entry.notify-kinds.label",
                    "",
                    "settings.entry.notify-kinds.keywords",
                )
                .essential()
                .modified(notify_changed()),
                entry(
                    "notify-volume",
                    "settings.reviewSound.volume",
                    "notify.volume.description",
                    "settings.entry.review-sound.keywords",
                ),
            ],
        )]
    }

    /// notify_entry draws Notifications' rows.
    pub(super) fn notify_entry(&self, e: &Entry, cx: &mut Context<Self>) -> Option<AnyElement> {
        match e.id {
            "notify-dnd" => {
                let until = prefs::dnd_until();
                let on = until > now_ms();
                let mut row = self.more_row(e, None, cx);
                row.modified = on;
                if on {
                    row.body.push(note(until_text(until)));
                }
                row.body.push(
                    h_flex()
                        .flex_wrap()
                        .gap_1p5()
                        .children(DURATIONS.iter().map(|d| {
                            let d = *d;
                            button(
                                SharedString::from(format!("dnd-{d}")),
                                t(&format!("notify.dnd.for.{d}")),
                                Btn::Secondary,
                                false,
                                cx.listener(move |_, _: &ClickEvent, _, cx| {
                                    prefs::set_dnd_until(dnd_end(d, Local::now()));
                                    cx.notify();
                                }),
                            )
                        }))
                        .when(on, |el| {
                            el.child(button(
                                "dnd-off",
                                t("notify.dnd.turnOff"),
                                Btn::Ghost,
                                false,
                                cx.listener(|_, _: &ClickEvent, _, cx| {
                                    prefs::set_dnd_until(0);
                                    cx.notify();
                                }),
                            ))
                        })
                        .into_any_element(),
                );
                Some(row.render())
            }
            "notify-kinds" => {
                let head = h_flex().gap_3().child(div().flex_1()).children(
                    prefs::NOTIFY_CHANNELS.iter().map(|c| {
                        div()
                            .w(px(80.))
                            .text_center()
                            .text_size(theme::text::CAPTION)
                            .font_weight(FontWeight::MEDIUM)
                            .text_color(theme::muted())
                            .child(t(&format!("notify.channel.{c}")))
                    }),
                );
                let rows = prefs::NOTIFY_KINDS.iter().map(|k| {
                    let k = *k;
                    h_flex()
                        .gap_3()
                        .child(
                            v_flex()
                                .flex_1()
                                .min_w_0()
                                .child(
                                    div()
                                        .text_size(theme::text::UI)
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::primary())
                                        .child(t(&format!("notify.kind.{k}.label"))),
                                )
                                .child(
                                    div()
                                        .text_size(theme::text::CAPTION)
                                        .line_height(rems(1.4))
                                        .text_color(theme::subtle())
                                        .child(t(&format!("notify.kind.{k}.description"))),
                                ),
                        )
                        .children(prefs::NOTIFY_CHANNELS.iter().map(|c| {
                            let c = *c;
                            let on = prefs::notify_on(k, c);
                            h_flex().w(px(80.)).justify_center().child(toggle(
                                SharedString::from(format!("notify-{k}-{c}")),
                                on,
                                false,
                                cx.listener(move |_, _: &ClickEvent, _, cx| {
                                    prefs::set_notify(k, c, !on);
                                    cx.notify();
                                }),
                            ))
                        }))
                });
                Some(
                    v_flex()
                        .id("notify-kinds")
                        .relative()
                        .px_5()
                        .py_4()
                        .gap_3()
                        .when(e.modified, |el| {
                            el.child(
                                div()
                                    .absolute()
                                    .left_0()
                                    .top_4()
                                    .bottom_4()
                                    .w(px(2.))
                                    .rounded_r_full()
                                    .bg(theme::brand(400)),
                            )
                        })
                        .child(head)
                        .children(rows)
                        .into_any_element(),
                )
            }
            "notify-volume" => {
                let any_sound = prefs::NOTIFY_KINDS
                    .iter()
                    .any(|k| prefs::notify_on(k, "sound"));
                let mut row: Row = self.more_row(e, None, cx);
                row.control = Some(
                    h_flex()
                        .gap_2()
                        .child(stepper(
                            "notify-volume",
                            prefs::review_volume(),
                            (0, 100, 10),
                            "%",
                            !any_sound,
                            Rc::new(cx.listener(|_, v: &i64, _, cx| {
                                prefs::set(prefs::REVIEW_VOLUME, (*v).into());
                                cx.notify();
                            })),
                        ))
                        .child(button(
                            "notify-test-sound",
                            t("settings.reviewSound.test"),
                            Btn::Secondary,
                            !any_sound,
                            move |_, _, _| {
                                if any_sound {
                                    crate::notices::chime(prefs::review_volume());
                                }
                            },
                        ))
                        .into_any_element(),
                );
                Some(row.render())
            }
            _ => None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::{Local, dnd_end, prefs};
    use chrono::TimeZone as _;

    #[test]
    fn do_not_disturb_ends_as_electron_says() {
        let now = Local.with_ymd_and_hms(2026, 3, 4, 10, 0, 0).unwrap();
        assert_eq!(dnd_end("hour", now), now.timestamp_millis() + 3_600_000);
        let eight = Local.with_ymd_and_hms(2026, 3, 5, 8, 0, 0).unwrap();
        assert_eq!(dnd_end("tomorrow", now), eight.timestamp_millis());
        let early = Local.with_ymd_and_hms(2026, 3, 4, 6, 0, 0).unwrap();
        let today = Local.with_ymd_and_hms(2026, 3, 4, 8, 0, 0).unwrap();
        assert_eq!(dnd_end("tomorrow", early), today.timestamp_millis());
        assert_eq!(dnd_end("forever", now), prefs::DND_FOREVER);
    }
}
