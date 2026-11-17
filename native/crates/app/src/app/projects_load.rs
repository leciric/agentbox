//! The sidebar's read of the projects while the daemon is still coming up.
//!
//! The app asks for the projects the moment its window opens, which on a VM
//! front end is before the VM's daemon answers: the socket's forward accepts
//! the connection and drops it, and the read failed with "GET /v1/projects:
//! connection error", shown in red over "Add your first project" on every
//! launch. A read that reached no daemon is now sent again until one answers,
//! the sidebar saying AgentBox is starting meanwhile; only a daemon that
//! stays out of reach past PATIENCE (with no start of its own under way), or
//! one that refuses the read, is an error.
use std::time::Duration;

use gpui::SharedString;

/// ProjectsLoad is what the sidebar knows of the projects.
#[derive(Clone, Debug, Default, PartialEq)]
pub(super) enum ProjectsLoad {
    /// Waiting: no answer yet. The sidebar says AgentBox is starting, rather
    /// than "Add your first project", which would say there are none.
    #[default]
    Waiting,
    /// Loaded: the daemon's list is in AppView.projects, empty or not.
    Loaded,
    /// Failed: the daemon refused the read, or none answered for too long.
    Failed(SharedString),
}

/// PATIENCE is how long a read that reaches no daemon is sent again before
/// the sidebar says so, while the app isn't starting the daemon itself. A
/// start the app runs (connection.rs) has its own timeout and says why it
/// failed, so the read waits as long as it's under way.
pub(super) const PATIENCE: Duration = Duration::from_secs(30);

const FIRST_WAIT: Duration = Duration::from_millis(250);
const MOST_WAIT: Duration = Duration::from_secs(2);

/// retry_after is how long to wait before sending the read again, after its
/// `attempt`th failure (from 1), `waited` after the first: None when it's
/// time to show the error. `reachless` is a failure that never reached a
/// daemon, or one that can't answer yet (client::not_ready); `starting` is a
/// start of the daemon or its VM under way.
pub(super) fn retry_after(
    attempt: u32,
    waited: Duration,
    reachless: bool,
    starting: bool,
) -> Option<Duration> {
    if !reachless || (!starting && waited >= PATIENCE) {
        return None;
    }
    let wait = FIRST_WAIT.saturating_mul(1 << attempt.saturating_sub(1).min(8));
    Some(wait.min(MOST_WAIT))
}

/// Shown is which of its states the sidebar's project list shows.
#[derive(Clone, Debug, PartialEq)]
pub(super) enum Shown {
    /// Starting: a neutral "Starting AgentBox…".
    Starting,
    /// AddFirst: the daemon says there are no projects.
    AddFirst,
    /// List: the projects (none of the above).
    List,
    /// Error: the read failed for good; any projects read earlier stay.
    Error(SharedString),
}

/// shown is what the sidebar shows for a load and the projects read so far.
/// A list read earlier stays while a later read is waiting.
pub(super) fn shown(load: &ProjectsLoad, have_projects: bool) -> Shown {
    match load {
        ProjectsLoad::Failed(e) => Shown::Error(e.clone()),
        _ if have_projects => Shown::List,
        ProjectsLoad::Loaded => Shown::AddFirst,
        ProjectsLoad::Waiting => Shown::Starting,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_read_that_reached_no_daemon_is_sent_again_with_a_growing_wait() {
        let waits: Vec<_> = (1..=6)
            .map(|n| retry_after(n, Duration::from_secs(1), true, false))
            .collect();
        assert_eq!(
            waits,
            [250, 500, 1000, 2000, 2000, 2000].map(|ms| Some(Duration::from_millis(ms)))
        );
        // A huge attempt count doesn't overflow.
        assert_eq!(
            retry_after(u32::MAX, Duration::ZERO, true, false),
            Some(MOST_WAIT)
        );
    }

    #[test]
    fn past_patience_with_no_start_under_way_the_error_shows() {
        assert_eq!(retry_after(40, PATIENCE, true, false), None);
        assert!(retry_after(40, PATIENCE - Duration::from_millis(1), true, false).is_some());
    }

    #[test]
    fn a_start_under_way_is_waited_for_however_long_it_takes() {
        // Booting the VM takes minutes; its start says itself if it fails.
        assert!(retry_after(200, Duration::from_secs(280), true, true).is_some());
    }

    #[test]
    fn a_daemon_that_refuses_the_read_is_an_error_at_once() {
        assert_eq!(retry_after(1, Duration::ZERO, false, false), None);
        assert_eq!(retry_after(1, Duration::ZERO, false, true), None);
    }

    #[test]
    fn until_the_daemon_answers_the_sidebar_says_starting_not_add_first() {
        assert_eq!(shown(&ProjectsLoad::Waiting, false), Shown::Starting);
        assert_eq!(shown(&ProjectsLoad::Loaded, false), Shown::AddFirst);
        assert_eq!(shown(&ProjectsLoad::Loaded, true), Shown::List);
        // A reload after a reconnect keeps the list it had.
        assert_eq!(shown(&ProjectsLoad::Waiting, true), Shown::List);
        let e: SharedString = "GET /v1/projects: connection error".into();
        assert_eq!(
            shown(&ProjectsLoad::Failed(e.clone()), false),
            Shown::Error(e.clone())
        );
        assert_eq!(shown(&ProjectsLoad::Failed(e.clone()), true), Shown::Error(e));
    }
}
