//! What the app knows of the daemon's update check (GET /v1/update), kept
//! fresh for as long as the app runs.
//!
//! The daemon looks for a release every hour (internal/daemon/update.go) and
//! says what it found with an update event, which carries the new status. The
//! app once read the status as the sidebar first loaded and kept that copy:
//! the event only refreshed the Settings page's, so a release published while
//! the app ran never reached the sidebar's button. Now one status feeds both,
//! and it is read again on the event, on every reconnect, every few minutes,
//! and as the window comes back to the front. A read is a local request; it
//! is also how a daemon whose ticker stood still while the computer slept
//! learns its check is overdue and sends one (checkIfOverdue).

use std::time::{Duration, Instant};

use agentbox_api::UpdateStatus;
use gpui::Context;

use super::AppView;

/// EVERY is how often the status is read again with nothing else asking:
/// well inside the daemon's hour, so a release it finds shows within
/// minutes even if the event was missed.
pub(super) const EVERY: Duration = Duration::from_secs(10 * 60);

/// ON_FOCUS is how long after the last read the window coming to the front
/// reads it again: often enough that coming back to the app shows what the
/// daemon found, not so often that switching windows floods the socket.
pub(super) const ON_FOCUS: Duration = Duration::from_secs(30);

/// Why a read is being considered.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum Reason {
    /// The poll's tick: due every EVERY.
    Tick,
    /// The window came to the front: due after ON_FOCUS.
    Focus,
}

/// UpdatePoll is when the status was last asked for.
#[derive(Default)]
pub(super) struct UpdatePoll {
    asked: Option<Instant>,
    inflight: bool,
}

impl UpdatePoll {
    /// due says whether to read the status now, for reason.
    pub(super) fn due(&self, now: Instant, reason: Reason) -> bool {
        if self.inflight {
            return false;
        }
        let Some(asked) = self.asked else {
            return true;
        };
        let wait = match reason {
            Reason::Tick => EVERY,
            Reason::Focus => ON_FOCUS,
        };
        now.saturating_duration_since(asked) >= wait
    }

    fn start(&mut self, now: Instant) {
        self.asked = Some(now);
        self.inflight = true;
    }

    fn finish(&mut self) {
        self.inflight = false;
    }
}

impl AppView {
    /// load_update reads the update status into the sidebar and the
    /// Settings page alike.
    pub(super) fn load_update(&mut self, cx: &mut Context<Self>) {
        self.update_poll.start(Instant::now());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<UpdateStatus>("/v1/update").await;
            this.update(cx, |this, cx| {
                this.update_poll.finish();
                if let Ok(u) = res {
                    this.apply_update(u, cx);
                }
            })
            .ok();
        })
        .detach();
    }

    /// apply_update shows status, from a read or from an update event.
    pub(super) fn apply_update(&mut self, status: UpdateStatus, cx: &mut Context<Self>) {
        self.settings_page.update = Some(status.clone());
        self.side.update = Some(status);
        cx.notify();
    }

    /// on_update_event takes the status an update event carries, or reads it
    /// when the event's can't be read.
    pub(super) fn on_update_event(&mut self, data: serde_json::Value, cx: &mut Context<Self>) {
        match serde_json::from_value::<UpdateStatus>(data) {
            Ok(status) => {
                self.update_poll.asked = Some(Instant::now());
                self.apply_update(status, cx);
            }
            Err(_) => self.load_update(cx),
        }
    }

    /// poll_update reads the status if reason makes it due.
    pub(super) fn poll_update(&mut self, reason: Reason, cx: &mut Context<Self>) {
        if self.connected && self.update_poll.due(Instant::now(), reason) {
            self.load_update(cx);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn first_read_is_due_at_once() {
        let p = UpdatePoll::default();
        assert!(p.due(Instant::now(), Reason::Tick));
        assert!(p.due(Instant::now(), Reason::Focus));
    }

    #[test]
    fn tick_reads_again_every_few_minutes() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        p.start(t0);
        p.finish();
        assert!(!p.due(t0 + EVERY - Duration::from_secs(1), Reason::Tick));
        assert!(p.due(t0 + EVERY, Reason::Tick));
        // Well inside the daemon's hourly check.
        const { assert!(EVERY.as_secs() * 4 <= 3600) };
    }

    #[test]
    fn focus_reads_again_sooner_but_not_on_every_switch() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        p.start(t0);
        p.finish();
        assert!(!p.due(t0 + Duration::from_secs(5), Reason::Focus));
        assert!(p.due(t0 + ON_FOCUS, Reason::Focus));
        assert!(!p.due(t0 + ON_FOCUS, Reason::Tick));
    }

    #[test]
    fn one_read_at_a_time() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        p.start(t0);
        assert!(!p.due(t0 + EVERY * 2, Reason::Tick));
        p.finish();
        assert!(p.due(t0 + EVERY * 2, Reason::Tick));
    }

    #[test]
    fn an_event_counts_as_a_read() {
        // on_update_event sets asked: the next tick waits a full EVERY.
        let t0 = Instant::now();
        let p = UpdatePoll {
            asked: Some(t0),
            inflight: false,
        };
        assert!(!p.due(t0 + Duration::from_secs(60), Reason::Tick));
    }
}
