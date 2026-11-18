//! What the window last knew of the projects it isn't showing.
//!
//! Picking a project used to clear the rail's agents and read the fleet, the
//! pull requests and the sessions again, even when the project picked was the
//! one already open: the list blanked and refilled on every click. Now picking
//! the open project is only navigation, and moving to another shows what the
//! window last had of that one (its fleet and pull requests; the sessions are
//! kept per project in `Sessions::lists` already) while the reads that follow
//! replace it in place.
//!
//! A read answers for the project it was sent for, which may no longer be the
//! one shown by the time it lands: it then goes into the cache, never over what
//! the window shows.
use std::collections::HashMap;

use agentbox_api::FleetAgent;

/// moves is whether picking `target` while `current` is open moves to another
/// project. Picking the open one doesn't: nothing is cleared or read again.
pub(super) fn moves(current: Option<&str>, target: &str) -> bool {
    current != Some(target)
}

/// select is what picking `target` does to the project open and the fleet the
/// rail shows: nothing when it is the open one (false: no reads to send), else
/// the project is `target`, the rail shows what it last had, and what it showed
/// of the one left is kept for its return (true: read them again, replacing
/// what is shown in place).
pub(super) fn select(
    kept: &mut Kept<Vec<FleetAgent>>,
    project: &mut Option<String>,
    fleet: &mut Vec<FleetAgent>,
    target: &str,
) -> bool {
    if !moves(project.as_deref(), target) {
        return false;
    }
    let left = std::mem::take(fleet);
    *fleet = kept.swap(project.as_deref(), target, left);
    *project = Some(target.to_string());
    true
}

/// Kept is one kind of per-project data (the fleet, the pull requests) as the
/// window last had it, for the projects it isn't showing.
#[derive(Debug)]
pub(super) struct Kept<T> {
    by_project: HashMap<String, T>,
}

impl<T> Default for Kept<T> {
    fn default() -> Self {
        Self {
            by_project: HashMap::new(),
        }
    }
}

impl<T> Kept<T> {
    /// keep stores what the window knows of `project`, over what it kept.
    pub(super) fn keep(&mut self, project: &str, value: T) {
        self.by_project.insert(project.to_string(), value);
    }

    /// take is what the window last knew of `project`, which it is about to
    /// show: it isn't kept meanwhile, the window has it.
    pub(super) fn take(&mut self, project: &str) -> Option<T> {
        self.by_project.remove(project)
    }

    /// swap moves from `from` (none at the start) to `to`: what was `shown` of
    /// `from` is kept, and what `to` last had is what is shown now (the default,
    /// a project not seen yet, until its read lands).
    pub(super) fn swap(&mut self, from: Option<&str>, to: &str, shown: T) -> T
    where
        T: Default,
    {
        if let Some(from) = from {
            self.keep(from, shown);
        }
        self.take(to).unwrap_or_default()
    }

    /// land is where a finished read of `project` goes, while `shown` is the
    /// project the window shows: the value back when it is that one's (to put
    /// on screen), or None when it was kept for its own project instead, so
    /// that a read for a project no longer shown can't overwrite the one that is.
    pub(super) fn land(&mut self, shown: Option<&str>, project: &str, read: T) -> Option<T> {
        if shown == Some(project) {
            Some(read)
        } else {
            self.keep(project, read);
            None
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use agentbox_api::ProjectPullRequests;

    fn agents(project: &str, names: &[&str]) -> Vec<FleetAgent> {
        names
            .iter()
            .map(|n| FleetAgent {
                r#ref: format!("{project}/{n}"),
                project: project.into(),
                name: (*n).into(),
                ..Default::default()
            })
            .collect()
    }

    fn names(fleet: &[FleetAgent]) -> Vec<&str> {
        fleet.iter().map(|a| a.r#ref.as_str()).collect()
    }

    /// The window as select_project drives it: the project shown, its fleet,
    /// and the cache.
    struct Window {
        project: Option<String>,
        fleet: Vec<FleetAgent>,
        kept: Kept<Vec<FleetAgent>>,
        /// How many times the fleet was read again (load_fleet).
        reads: u32,
    }

    impl Window {
        fn new() -> Self {
            Self {
                project: None,
                fleet: Vec::new(),
                kept: Kept::default(),
                reads: 0,
            }
        }

        /// select_project's: the reads it sends are counted.
        fn select(&mut self, name: &str) {
            if select(&mut self.kept, &mut self.project, &mut self.fleet, name) {
                self.reads += 1;
            }
        }

        /// A fleet read of `project` lands.
        fn read(&mut self, project: &str, fleet: Vec<FleetAgent>) {
            if let Some(f) = self.kept.land(self.project.as_deref(), project, fleet) {
                self.fleet = f;
            }
        }
    }

    #[test]
    fn picking_the_open_project_is_not_a_move() {
        assert!(!moves(Some("demo"), "demo"));
        assert!(moves(Some("demo"), "other"));
        assert!(moves(None, "demo"));
        // Home is a project like any other.
        assert!(!moves(Some("home"), "home"));
        assert!(moves(Some("demo"), "home"));
    }

    #[test]
    fn picking_the_open_project_keeps_its_agents_and_reads_nothing() {
        let mut w = Window::new();
        w.select("demo");
        w.read("demo", agents("demo", &["a", "b"]));
        let reads = w.reads;

        w.select("demo");
        w.select("demo");

        // Not blanked, and no read sent again.
        assert_eq!(names(&w.fleet), ["demo/a", "demo/b"]);
        assert_eq!(w.reads, reads);
        assert_eq!(w.project.as_deref(), Some("demo"));
    }

    #[test]
    fn moving_to_a_project_shows_what_it_last_had_at_once() {
        let mut w = Window::new();
        w.select("one");
        w.read("one", agents("one", &["a"]));
        w.select("two");
        // A project not seen yet starts empty, as it always did.
        assert!(w.fleet.is_empty());
        w.read("two", agents("two", &["x", "y"]));

        w.select("one");
        // Its agents are there before any read lands.
        assert_eq!(names(&w.fleet), ["one/a"]);
        w.select("two");
        assert_eq!(names(&w.fleet), ["two/x", "two/y"]);
    }

    #[test]
    fn the_read_that_follows_a_move_replaces_what_was_shown_in_place() {
        let mut w = Window::new();
        w.select("one");
        w.read("one", agents("one", &["a"]));
        w.select("two");
        w.select("one");
        assert_eq!(names(&w.fleet), ["one/a"]);

        w.read("one", agents("one", &["a", "b"]));
        assert_eq!(names(&w.fleet), ["one/a", "one/b"]);
        // What it replaced isn't kept as well: leaving keeps what is shown.
        assert!(w.kept.take("one").is_none());
    }

    #[test]
    fn a_read_for_a_project_no_longer_shown_does_not_overwrite_the_one_shown() {
        let mut w = Window::new();
        w.select("one");
        // The read of one's fleet is in flight when the user moves on.
        w.select("two");
        w.read("two", agents("two", &["x"]));

        w.read("one", agents("one", &["late"]));

        assert_eq!(names(&w.fleet), ["two/x"]);
        assert_eq!(w.project.as_deref(), Some("two"));
        // It isn't lost: it is what one shows when it is picked again.
        w.select("one");
        assert_eq!(names(&w.fleet), ["one/late"]);
    }

    #[test]
    fn a_late_read_is_newer_than_what_was_kept_when_the_project_was_left() {
        let mut w = Window::new();
        w.select("one");
        w.read("one", agents("one", &["old"]));
        w.select("two");
        w.read("one", agents("one", &["new"]));

        w.select("one");
        assert_eq!(names(&w.fleet), ["one/new"]);
    }

    #[test]
    fn leaving_a_project_keeps_what_was_shown_even_when_it_is_empty() {
        let mut w = Window::new();
        w.select("one"); // never read, or has no agents
        w.select("two");
        w.read("two", agents("two", &["x"]));
        w.select("one");
        assert!(w.fleet.is_empty());
        assert_eq!(w.project.as_deref(), Some("one"));
    }

    #[test]
    fn the_first_pick_has_nothing_to_keep() {
        let mut kept: Kept<Vec<FleetAgent>> = Kept::default();
        let shown = agents("x", &["gone"]);
        assert!(kept.swap(None, "demo", shown).is_empty());
        assert!(kept.take("x").is_none());
    }

    #[test]
    fn pull_requests_are_kept_the_same_way() {
        let list = |p: &str, github: &str| ProjectPullRequests {
            project: p.into(),
            github: github.into(),
            ..Default::default()
        };
        let mut kept: Kept<ProjectPullRequests> = Kept::default();
        // one is shown; two's read lands after the move: kept, not shown.
        assert!(
            kept.land(Some("one"), "two", list("two", "o/two"))
                .is_none()
        );
        let on_screen = kept.land(Some("one"), "one", list("one", "o/one"));
        assert_eq!(on_screen.map(|p| p.project), Some("one".into()));

        kept.keep("one", list("one", "o/one"));
        assert_eq!(kept.take("two").map(|p| p.github), Some("o/two".into()));
        assert_eq!(kept.take("one").map(|p| p.github), Some("o/one".into()));
        assert!(kept.take("one").is_none());
    }
}
