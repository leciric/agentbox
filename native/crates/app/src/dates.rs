//! Dates in the app's language. The Electron app has Intl for these
//! (toLocaleDateString, Intl.DateTimeFormat); this app has no locale data, so
//! the names of months and weekdays and the order a date is written in are
//! native.* messages of its own catalogs, and chrono does the arithmetic.
use agentbox_i18n::{t, tv};
use chrono::{DateTime, Datelike as _, Local, NaiveDate};

/// weekday names a day of the week, 0 being Sunday.
pub fn weekday(d: u32, long: bool) -> String {
    t(&format!(
        "native.day.{}.{}",
        if long { "long" } else { "short" },
        d % 7
    ))
}

/// month names a month, 1 being January.
pub fn month(m: u32, long: bool) -> String {
    t(&format!(
        "native.month.{}.{}",
        if long { "long" } else { "short" },
        m.clamp(1, 12)
    ))
}

/// local reads an RFC 3339 time as the user's own.
pub fn local(iso: &str) -> Option<DateTime<Local>> {
    DateTime::parse_from_rfc3339(iso)
        .ok()
        .map(|d| d.with_timezone(&Local))
}

/// date is a day as people write it: "November 5" ("5 de novembro"), with
/// its year when it isn't this one; short months with short.
pub fn date(d: NaiveDate, short: bool) -> String {
    let m = month(d.month(), !short);
    if d.year() == Local::now().year() {
        tv(
            "native.date.dayMonth",
            &[("month", m.into()), ("day", (d.day() as i64).into())],
        )
    } else {
        tv(
            "native.date.dayMonthYear",
            &[
                ("month", m.into()),
                ("day", (d.day() as i64).into()),
                ("year", (d.year() as i64).into()),
            ],
        )
    }
}

/// day_label names a day (lib/work.ts dayLabel): today, yesterday, a
/// weekday within the week, or its date.
pub fn day_label(d: NaiveDate, today: NaiveDate) -> String {
    match (today - d).num_days() {
        0 => t("work.today"),
        1 => t("work.yesterday"),
        2..=6 => weekday(d.weekday().num_days_from_sunday(), true),
        _ => date(d, false),
    }
}

/// weekday_time is a time within the week, short: "Thu 09:00".
pub fn weekday_time(at: DateTime<Local>) -> String {
    format!(
        "{} {}",
        weekday(at.weekday().num_days_from_sunday(), false),
        at.format("%H:%M")
    )
}

/// date_time is a moment as people write it (toLocaleString): "Nov 5, 14:30",
/// with the year when it isn't this one.
pub fn date_time(at: DateTime<Local>) -> String {
    tv(
        "native.date.at",
        &[
            ("date", date(at.date_naive(), true).into()),
            ("time", at.format("%H:%M").to_string().into()),
        ],
    )
}

/// date_time_of is date_time of an RFC 3339 time, or the time as it came.
pub fn date_time_of(iso: &str) -> String {
    local(iso).map_or_else(|| iso.to_string(), date_time)
}

/// date_of is date (short months) of an RFC 3339 time, or as it came.
pub fn date_of(iso: &str) -> String {
    local(iso).map_or_else(
        || iso.get(..10).unwrap_or(iso).to_string(),
        |d| date(d.date_naive(), true),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn days_are_named_as_work_names_them() {
        let today = NaiveDate::from_ymd_opt(2026, 11, 5).unwrap(); // a Thursday
        assert_eq!(day_label(today, today), t("work.today"));
        assert_eq!(
            day_label(today.pred_opt().unwrap(), today),
            t("work.yesterday")
        );
        let monday = NaiveDate::from_ymd_opt(2026, 11, 2).unwrap();
        assert_eq!(day_label(monday, today), weekday(1, true));
        assert!(!month(11, true).starts_with("native."));
        assert!(!month(1, false).starts_with("native."));
    }
}
