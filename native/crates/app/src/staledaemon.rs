//! A daemon older than this app, still running after the app was updated
//! (desktop/src/main/staledaemon.ts). The app restarts it into its own
//! version, from the managed copy of its tool (cli.rs), but not while it runs
//! jobs, which a restart would cut short: until then the window says so and
//! offers the restart now (app/stale.rs), and the daemon is asked again every
//! RETRY whether they're done.
//!
//! Unlike the Electron app's, a daemon newer than the app isn't stale: with
//! both apps installed the newer one's tool is the managed copy (cli.rs), and
//! the older app restarting its daemon would only get the same one back.
//!
//! Tracker is the decisions alone; app/stale.rs asks the daemon and restarts
//! it.
use std::time::Duration;

use crate::cli::{comparable, newer};

/// How often a stale daemon busy with jobs is asked again.
pub const RETRY: Duration = Duration::from_secs(15);

/// Stale is what the window says about a stale daemon.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Stale {
    /// The daemon's version, and this app's.
    pub daemon: String,
    pub app: String,
    /// Running jobs, which the restart waits for; 0 when it was restarted
    /// and is still that version.
    pub jobs: usize,
}

/// is_outdated is whether a daemon of version theirs is behind this app's
/// (ours, None when the build has none). A daemon built from a checkout
/// ("dev") is whatever its developer made it.
pub fn is_outdated(theirs: &str, ours: Option<&str>) -> bool {
    ours.is_some_and(|ours| theirs != "dev" && newer(&comparable(ours), &comparable(theirs)))
}

/// Look is what to do about the daemon that just answered.
#[derive(Debug, PartialEq, Eq)]
pub enum Look {
    /// It's the app's version, or past it: nothing to do.
    Current,
    /// It's behind: ask how many jobs it runs, and say so with [`Tracker::jobs`].
    AskJobs,
    /// It's behind, and a restart already brought the same version back:
    /// restarting it again would only loop. Only the person's own restart
    /// tries again.
    GaveUp,
}

/// Tracker follows one daemon's staleness.
#[derive(Debug)]
pub struct Tracker {
    app: String,
    stale: Option<Stale>,
    /// The version of a daemon that was restarted and came back the same
    /// (its tool wasn't replaced).
    failed_from: Option<String>,
}

impl Tracker {
    pub fn new(app: &str) -> Self {
        Self {
            app: app.to_string(),
            stale: None,
            failed_from: None,
        }
    }

    /// current is what the window shows, None for a daemon that isn't stale.
    pub fn current(&self) -> Option<&Stale> {
        self.stale.as_ref()
    }

    /// look takes the version of the daemon that answered.
    pub fn look(&mut self, daemon: &str) -> Look {
        if !is_outdated(daemon, Some(&self.app)) {
            self.failed_from = None;
            self.stale = None;
            return Look::Current;
        }
        if self.failed_from.as_deref() == Some(daemon) {
            self.stale = Some(self.at(daemon, 0));
            return Look::GaveUp;
        }
        Look::AskJobs
    }

    /// jobs takes how many jobs the stale daemon runs, and answers whether
    /// to restart it now: only with none. Otherwise it's shown as stale, and
    /// looked at again in RETRY.
    pub fn jobs(&mut self, daemon: &str, running: usize) -> bool {
        if running == 0 {
            return true;
        }
        self.stale = Some(self.at(daemon, running));
        false
    }

    /// restart_now is the window's "Restart now", for a job that won't end
    /// or a person who won't wait: whatever it's doing, and even after a
    /// restart that changed nothing.
    pub fn restart_now(&mut self) {
        self.failed_from = None;
    }

    /// restarted takes the version of the daemon that answers after a
    /// restart (None when none does yet).
    pub fn restarted(&mut self, now: Option<&str>) {
        match now {
            Some(now) if is_outdated(now, Some(&self.app)) => {
                self.failed_from = Some(now.to_string());
                self.stale = Some(self.at(now, 0));
            }
            _ => {
                self.failed_from = None;
                self.stale = None;
            }
        }
    }

    fn at(&self, daemon: &str, jobs: usize) -> Stale {
        Stale {
            daemon: daemon.to_string(),
            app: self.app.clone(),
            jobs,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const OLD: &str = "0.18.0-nightly.20261104.168";
    const APP: &str = "0.18.0-nightly.20261106.179";
    const NEW: &str = "0.18.0-nightly.20261112.187";

    #[test]
    fn only_a_daemon_behind_the_app_is_outdated() {
        assert!(is_outdated(OLD, Some(APP)));
        assert!(is_outdated("0.17.2", Some(APP)));
        // The app's own spelling of its nightly.
        assert!(is_outdated(OLD, Some("0.18.0.20261106.179")));
        assert!(!is_outdated(APP, Some("0.18.0.20261106.179")));
        assert!(!is_outdated(APP, Some(APP)));
        // Newer than the app: the Electron app's, which is newer. Not ours
        // to restart.
        assert!(!is_outdated(NEW, Some(APP)));
        // A daemon from a checkout, and an app with no version of its own.
        assert!(!is_outdated("dev", Some(APP)));
        assert!(!is_outdated(OLD, None));
    }

    #[test]
    fn an_idle_stale_daemon_is_restarted_at_once() {
        let mut t = Tracker::new(APP);
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(t.jobs(OLD, 0));
        // Nothing was shown: it never had to wait.
        assert_eq!(t.current(), None);
        t.restarted(Some(APP));
        assert_eq!(t.current(), None);
        assert_eq!(t.look(APP), Look::Current);
    }

    #[test]
    fn a_busy_one_is_shown_and_asked_again_until_its_jobs_are_done() {
        let mut t = Tracker::new(APP);
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(!t.jobs(OLD, 2));
        assert_eq!(
            t.current(),
            Some(&Stale {
                daemon: OLD.into(),
                app: APP.into(),
                jobs: 2
            })
        );
        // Still busy, with one fewer.
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(!t.jobs(OLD, 1));
        assert_eq!(t.current().map(|s| s.jobs), Some(1));
        // Done: restarted, and the notice goes once the new one answers.
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(t.jobs(OLD, 0));
        t.restarted(Some(APP));
        assert_eq!(t.current(), None);
    }

    #[test]
    fn one_that_comes_back_the_same_is_not_restarted_in_a_loop() {
        let mut t = Tracker::new(APP);
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(t.jobs(OLD, 0));
        // Its tool wasn't replaced: the same version answers.
        t.restarted(Some(OLD));
        let gave_up = Stale {
            daemon: OLD.into(),
            app: APP.into(),
            jobs: 0,
        };
        assert_eq!(t.current(), Some(&gave_up));
        // However often it's looked at, it's left alone.
        for _ in 0..3 {
            assert_eq!(t.look(OLD), Look::GaveUp);
            assert_eq!(t.current(), Some(&gave_up));
        }
        // The person's own restart tries again, once.
        t.restart_now();
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(t.jobs(OLD, 0));
        t.restarted(Some(OLD));
        assert_eq!(t.look(OLD), Look::GaveUp);
        // Another stale version is another daemon: that one is tried.
        assert_eq!(t.look("0.17.2"), Look::AskJobs);
    }

    #[test]
    fn restart_now_restarts_a_busy_one() {
        let mut t = Tracker::new(APP);
        assert_eq!(t.look(OLD), Look::AskJobs);
        assert!(!t.jobs(OLD, 3));
        t.restart_now();
        t.restarted(Some(APP));
        assert_eq!(t.current(), None);
    }

    #[test]
    fn a_daemon_that_becomes_current_by_itself_clears_the_notice() {
        let mut t = Tracker::new(APP);
        t.look(OLD);
        t.jobs(OLD, 1);
        assert!(t.current().is_some());
        // The Electron app, newer, restarted it into its own version.
        assert_eq!(t.look(NEW), Look::Current);
        assert_eq!(t.current(), None);
        // And one that doesn't answer after a restart shows nothing stale:
        // the window says it's unreachable instead.
        t.look(OLD);
        t.jobs(OLD, 1);
        t.restarted(None);
        assert_eq!(t.current(), None);
    }
}
