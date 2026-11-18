//! What cockpit mode decides that isn't drawing, as the Electron app's
//! lib/cockpit.ts: which messages the feed shows, which projects the filter
//! keeps, what an agent shows on the stage, which agent is on it, how many
//! columns the wall takes.

use std::collections::HashSet;

use agentbox_api::ChatItem;

/// What the feed keeps of each lead's thread: its latest messages.
pub const FEED_PER_PROJECT: usize = 12;

/// FeedRow is one message of a lead's conversation in the merged feed.
#[derive(Clone, Debug)]
pub struct FeedRow {
    pub project: String,
    pub item: ChatItem,
}

/// feed_message is whether a chat item is one the feed shows: what you said
/// to a lead and what it answered, with words in it.
pub fn feed_message(it: &ChatItem) -> bool {
    (it.kind == "user" || it.kind == "assistant")
        && it.parent.is_empty()
        && !it.hidden
        && !it.text.trim().is_empty()
}

/// feed_of merges the leads' threads into one feed, oldest first, each
/// lead's last `per` messages.
pub fn feed_of<'a>(
    threads: impl IntoIterator<Item = (&'a str, &'a [ChatItem])>,
    per: usize,
) -> Vec<FeedRow> {
    let mut rows = Vec::new();
    for (project, items) in threads {
        let kept: Vec<&ChatItem> = items.iter().filter(|it| feed_message(it)).collect();
        let from = kept.len().saturating_sub(per);
        rows.extend(kept[from..].iter().map(|it| FeedRow {
            project: project.to_string(),
            item: (*it).clone(),
        }));
    }
    // By instant, not by text: RFC 3339 times with more or fewer fractional
    // digits don't sort as strings. Stable: messages at the same moment keep
    // their projects' order; one whose time can't be read goes first.
    rows.sort_by_key(|r| instant(&r.item.created_at));
    rows
}

/// instant is an RFC 3339 time as nanoseconds since 1970, for ordering.
fn instant(at: &str) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(at)
        .ok()
        .and_then(|d| d.timestamp_nanos_opt())
}

/// shown_hidden is what the filter hides as it shows it: only projects there
/// are, and none at all once that would be every one of them.
pub fn shown_hidden(all: &[String], hidden: &HashSet<String>) -> HashSet<String> {
    let there: HashSet<String> = all
        .iter()
        .filter(|n| hidden.contains(*n))
        .cloned()
        .collect();
    if there.len() < all.len() {
        there
    } else {
        HashSet::new()
    }
}

/// shown_projects is the filter's projects, in the projects' order.
pub fn shown_projects(all: &[String], hidden: &HashSet<String>) -> Vec<String> {
    let hidden = shown_hidden(all, hidden);
    all.iter()
        .filter(|n| !hidden.contains(*n))
        .cloned()
        .collect()
}

/// toggle_shown turns a project on or off, never the last one off.
pub fn toggle_shown(all: &[String], hidden: &HashSet<String>, name: &str) -> HashSet<String> {
    let mut next = shown_hidden(all, hidden);
    if !next.remove(name) && shown_projects(all, &next).iter().any(|n| n != name) {
        next.insert(name.to_string());
    }
    next
}

/// only_shown keeps just one project; on the one already alone, all again.
pub fn only_shown(all: &[String], hidden: &HashSet<String>, name: &str) -> HashSet<String> {
    let shown = shown_projects(all, hidden);
    if shown.len() == 1 && shown[0] == name {
        return HashSet::new();
    }
    all.iter().filter(|n| *n != name).cloned().collect()
}

/// Visual is what an agent shows on the stage. The native app has no iOS
/// Simulator view, so a phone is Android's.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Visual {
    Android,
    Display,
    None,
}

/// visual_of picks it: a phone the agent started before the desktop behind it.
pub fn visual_of(android_booted: bool, display: bool) -> Visual {
    if android_booted {
        Visual::Android
    } else if display {
        Visual::Display
    } else {
        Visual::None
    }
}

/// Tone is how an agent calls for you, as the Office's desks do.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Tone {
    Needs,
    Error,
    Working,
    Finished,
    Idle,
    Stopped,
}

impl Tone {
    fn rank(self) -> u8 {
        match self {
            Tone::Needs => 0,
            Tone::Error => 1,
            Tone::Working => 2,
            Tone::Finished => 3,
            Tone::Idle => 4,
            Tone::Stopped => 5,
        }
    }
    /// needy is whether it waits on you.
    pub fn needy(self) -> bool {
        matches!(self, Tone::Needs | Tone::Error)
    }
}

/// tone_of an agent: asking (a question, or its outcome), failed, working,
/// finished, asleep or idle.
pub fn tone_of(state: &str, chat: &str, outcome: &str, asks: bool) -> Tone {
    if asks || outcome == "asking" {
        Tone::Needs
    } else if chat == "error" {
        Tone::Error
    } else if state != "running" && state != "starting" {
        Tone::Stopped
    } else if chat == "running" || chat == "starting" {
        Tone::Working
    } else if outcome == "done" {
        Tone::Finished
    } else {
        Tone::Idle
    }
}

/// staged_of is the agent on the stage: the one chosen while it's still
/// there, else the first by tone (needs first), in the order given.
pub fn staged_of<'a>(desks: &'a [(String, Tone)], chosen: Option<&str>) -> Option<&'a str> {
    if let Some(c) = chosen
        && let Some((r, _)) = desks.iter().find(|(r, _)| r == c)
    {
        return Some(r);
    }
    desks
        .iter()
        .min_by_key(|(_, t)| t.rank())
        .map(|(r, _)| r.as_str())
}

/// fit_wall is how many columns the wall takes in a box w×h: the most that
/// keeps every 16:10 tile and its `chrome`-high label in view at the biggest
/// size, else as many `min`-wide ones as a row holds (the wall scrolls).
pub fn fit_wall(n: usize, w: f32, h: f32, gap: f32, chrome: f32, min: f32) -> usize {
    if n == 0 || w <= 0. {
        return 1;
    }
    let mut best = 0;
    let mut best_w = 0.;
    for cols in 1..=n {
        let tile_w = (w - gap * (cols as f32 - 1.)) / cols as f32;
        if tile_w < min {
            break;
        }
        let rows = n.div_ceil(cols) as f32;
        let tile_h = tile_w * 10. / 16. + chrome;
        if rows * tile_h + gap * (rows - 1.) <= h && tile_w > best_w {
            best = cols;
            best_w = tile_w;
        }
    }
    if best > 0 {
        best
    } else {
        (((w + gap) / (min + gap)).floor() as usize).max(1)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn item(id: &str, kind: &str, at: &str) -> ChatItem {
        ChatItem {
            id: id.into(),
            kind: kind.into(),
            text: id.into(),
            created_at: at.into(),
            ..Default::default()
        }
    }

    fn names(v: &[&str]) -> Vec<String> {
        v.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn feed_merges_leads_by_time_words_only() {
        let a = vec![
            item("a1", "user", "2026-01-01T10:00:00Z"),
            item("a-tool", "tool", "2026-01-01T10:01:00Z"),
            item("a2", "assistant", "2026-01-01T10:05:00Z"),
        ];
        let mut sub = item("b-sub", "assistant", "2026-01-01T10:03:00Z");
        sub.parent = "x".into();
        let mut empty = item("b-empty", "assistant", "2026-01-01T10:04:00Z");
        empty.text = " ".into();
        let b = vec![item("b1", "assistant", "2026-01-01T10:02:00Z"), sub, empty];
        let rows = feed_of([("a", a.as_slice()), ("b", b.as_slice())], 12);
        let ids: Vec<&str> = rows.iter().map(|r| r.item.id.as_str()).collect();
        assert_eq!(ids, ["a1", "b1", "a2"]);
        let few = vec![
            item("1", "user", "2026-01-01T10:00:01Z"),
            item("2", "assistant", "2026-01-01T10:00:02Z"),
            item("3", "user", "2026-01-01T10:00:03Z"),
        ];
        let rows = feed_of([("a", few.as_slice())], 2);
        assert_eq!(
            rows.iter().map(|r| r.item.id.as_str()).collect::<Vec<_>>(),
            ["2", "3"]
        );
    }

    #[test]
    fn feed_orders_by_instant_whatever_the_fraction_digits() {
        let a = vec![
            item("a-12", "user", "2026-01-01T10:00:01.12Z"),
            item("a-whole", "assistant", "2026-01-01T10:00:01Z"),
        ];
        let b = vec![
            item("b-123", "user", "2026-01-01T10:00:01.123Z"),
            item("b-5", "assistant", "2026-01-01T10:00:01.5Z"),
        ];
        let rows = feed_of([("a", a.as_slice()), ("b", b.as_slice())], 12);
        let ids: Vec<&str> = rows.iter().map(|r| r.item.id.as_str()).collect();
        assert_eq!(ids, ["a-whole", "a-12", "b-123", "b-5"]);
    }

    #[test]
    fn filter_keeps_one_and_only_toggles_back() {
        let all = names(&["a", "b", "c"]);
        let mut hidden = toggle_shown(&all, &HashSet::new(), "a");
        assert_eq!(shown_projects(&all, &hidden), names(&["b", "c"]));
        hidden = toggle_shown(&all, &hidden, "b");
        assert_eq!(shown_projects(&all, &hidden), names(&["c"]));
        hidden = toggle_shown(&all, &hidden, "c");
        assert_eq!(
            shown_projects(&all, &hidden),
            names(&["c"]),
            "the last one stays"
        );
        let only = only_shown(&all, &HashSet::new(), "b");
        assert_eq!(shown_projects(&all, &only), names(&["b"]));
        assert_eq!(shown_projects(&all, &only_shown(&all, &only, "b")), all);
    }

    #[test]
    fn filter_after_the_shown_project_went_away_toggles_what_it_shows() {
        // a and b hidden, c shown; then c goes away: both show.
        let hidden: HashSet<String> = ["a", "b"].iter().map(|s| s.to_string()).collect();
        let all = names(&["a", "b"]);
        assert_eq!(shown_projects(&all, &hidden), all);
        // Clicking a, shown checked, hides a, not b.
        let next = toggle_shown(&all, &hidden, "a");
        assert_eq!(shown_projects(&all, &next), names(&["b"]));
        // A project gone since it was hidden doesn't count.
        let stale: HashSet<String> = ["x", "a"].iter().map(|s| s.to_string()).collect();
        assert_eq!(
            shown_hidden(&all, &stale),
            ["a".to_string()].into_iter().collect()
        );
    }

    #[test]
    fn stage_visual_and_pick() {
        assert_eq!(visual_of(true, true), Visual::Android);
        assert_eq!(visual_of(false, true), Visual::Display);
        assert_eq!(visual_of(false, false), Visual::None);
        let desks = vec![
            ("p/1".to_string(), Tone::Working),
            ("p/2".into(), Tone::Needs),
            ("p/3".into(), Tone::Error),
        ];
        assert_eq!(staged_of(&desks, None), Some("p/2"));
        assert_eq!(staged_of(&desks, Some("p/1")), Some("p/1"));
        assert_eq!(staged_of(&desks, Some("p/gone")), Some("p/2"));
        assert_eq!(staged_of(&[], None), None);
    }

    #[test]
    fn tones() {
        assert_eq!(tone_of("running", "ready", "", true), Tone::Needs);
        assert_eq!(tone_of("running", "ready", "asking", false), Tone::Needs);
        assert_eq!(tone_of("running", "error", "", false), Tone::Error);
        assert_eq!(tone_of("stopped", "", "", false), Tone::Stopped);
        assert_eq!(tone_of("running", "running", "", false), Tone::Working);
        assert_eq!(tone_of("running", "ready", "done", false), Tone::Finished);
        assert_eq!(tone_of("running", "ready", "", false), Tone::Idle);
    }

    #[test]
    fn wall_fits_or_scrolls() {
        assert_eq!(fit_wall(1, 1600., 1100., 8., 28., 300.), 1);
        assert_eq!(fit_wall(12, 2000., 1300., 8., 28., 300.), 4);
        assert_eq!(fit_wall(40, 1000., 400., 8., 28., 300.), 3);
        assert_eq!(fit_wall(0, 1000., 400., 8., 28., 300.), 1);
    }
}
