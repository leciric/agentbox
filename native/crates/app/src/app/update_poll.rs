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
//! is also how a daemon whose last check is overdue by the VM's clock sends
//! one (checkIfOverdue).
//!
//! That clock can't be relied on after the computer slept: the daemon's
//! ticker stands still with the VM, and on Linux the VM's wall clock is put
//! right only when systemd-timesyncd next polls, up to about half an hour
//! later (Lima puts a Mac's right within seconds). The app runs on the
//! computer itself, so it notices the sleep (its wall clock ran on while its
//! monotonic one stood still) and asks for the latest release at once
//! (GET /v1/update/release, which makes a newer one the daemon's offer).

use std::time::{Duration, Instant, SystemTime};

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

/// SLEPT is how far the wall clock must run ahead of the monotonic one
/// between two ticks to count as the computer having slept: far more than
/// NTP ever slews or steps a clock that is roughly right.
pub(super) const SLEPT: Duration = Duration::from_secs(2 * 60);

/// Why a read is being considered.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(super) enum Reason {
    /// The poll's tick: due every EVERY.
    Tick,
    /// The window came to the front: due after ON_FOCUS.
    Focus,
}

/// UpdatePoll is when the status was last asked for, and which answer is
/// the newest.
#[derive(Default)]
pub(super) struct UpdatePoll {
    asked: Option<Instant>,
    /// The token of the read that is out, if one is.
    inflight: Option<u64>,
    /// generation counts what changed the status: each read as it starts, and each
    /// event. A read's answer is shown only if nothing came after it started,
    /// so a read that was out when an event arrived can't undo the event.
    generation: u64,
    /// The last tick, by both clocks, for noticing a sleep.
    tick: Option<(Instant, SystemTime)>,
}

impl UpdatePoll {
    /// due says whether to read the status now, for reason.
    pub(super) fn due(&self, now: Instant, reason: Reason) -> bool {
        if self.inflight.is_some() {
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

    /// start is a read going out: the token its answer comes back with.
    pub(super) fn start(&mut self, now: Instant) -> u64 {
        self.asked = Some(now);
        self.generation += 1;
        self.inflight = Some(self.generation);
        self.generation
    }

    /// finish is the read with token answering: whether its answer is still
    /// the newest.
    pub(super) fn finish(&mut self, token: u64) -> bool {
        if self.inflight == Some(token) {
            self.inflight = None;
        }
        token == self.generation
    }

    /// event is an update event's status being shown: it counts as a read,
    /// and as newer than any read still out.
    pub(super) fn event(&mut self, now: Instant) {
        self.asked = Some(now);
        self.generation += 1;
    }

    /// slept says whether the computer slept since the last tick: the wall
    /// clock ran on at least SLEPT more than the monotonic one, which stands
    /// still in sleep (Instant on Linux and macOS).
    pub(super) fn slept(&mut self, now: Instant, wall: SystemTime) -> bool {
        let last = self.tick.replace((now, wall));
        let Some((then, then_wall)) = last else {
            return false;
        };
        let mono = now.saturating_duration_since(then);
        wall.duration_since(then_wall)
            .is_ok_and(|w| w.saturating_sub(mono) >= SLEPT)
    }
}

impl AppView {
    /// load_update reads the update status into the sidebar and the
    /// Settings page alike.
    pub(super) fn load_update(&mut self, cx: &mut Context<Self>) {
        let token = self.update_poll.start(Instant::now());
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<UpdateStatus>("/v1/update").await;
            this.update(cx, |this, cx| {
                if this.update_poll.finish(token)
                    && let Ok(u) = res
                {
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
                self.update_poll.event(Instant::now());
                self.apply_update(status, cx);
            }
            Err(_) => self.load_update(cx),
        }
    }

    /// poll_update reads the status if reason makes it due, and after a
    /// sleep asks for the latest release first.
    pub(super) fn poll_update(&mut self, reason: Reason, cx: &mut Context<Self>) {
        let now = Instant::now();
        if reason == Reason::Tick && self.update_poll.slept(now, SystemTime::now()) {
            self.check_after_sleep(cx);
            return;
        }
        if self.connected && self.update_poll.due(now, reason) {
            self.load_update(cx);
        }
    }

    /// check_after_sleep asks the daemon for the latest release now, rather
    /// than when its own clock says an hour has gone by, then reads what it
    /// makes of it. A failure is as quiet as the daemon's own check.
    fn check_after_sleep(&mut self, cx: &mut Context<Self>) {
        crate::quit::log(format_args!(
            "agentbox-native: the computer slept; checking for a release"
        ));
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let _ = client
                .get::<agentbox_api::UpdateRelease>("/v1/update/release")
                .await;
            this.update(cx, |this, cx| this.load_update(cx)).ok();
        })
        .detach();
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
        let r = p.start(t0);
        assert!(p.finish(r));
        assert!(!p.due(t0 + EVERY - Duration::from_secs(1), Reason::Tick));
        assert!(p.due(t0 + EVERY, Reason::Tick));
        // Well inside the daemon's hourly check.
        const { assert!(EVERY.as_secs() * 4 <= 3600) };
    }

    #[test]
    fn focus_reads_again_sooner_but_not_on_every_switch() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        let r = p.start(t0);
        p.finish(r);
        assert!(!p.due(t0 + Duration::from_secs(5), Reason::Focus));
        assert!(p.due(t0 + ON_FOCUS, Reason::Focus));
        assert!(!p.due(t0 + ON_FOCUS, Reason::Tick));
    }

    #[test]
    fn one_read_at_a_time() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        let r = p.start(t0);
        assert!(!p.due(t0 + EVERY * 2, Reason::Tick));
        p.finish(r);
        assert!(p.due(t0 + EVERY * 2, Reason::Tick));
    }

    #[test]
    fn an_event_counts_as_a_read() {
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        p.event(t0);
        assert!(!p.due(t0 + Duration::from_secs(60), Reason::Tick));
        assert!(p.due(t0 + EVERY, Reason::Tick));
    }

    #[test]
    fn a_read_out_when_an_event_comes_is_dropped() {
        // The read goes out, the daemon finds a release and says so, then
        // the read's older answer arrives: it must not undo the event.
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        let r = p.start(t0);
        p.event(t0 + Duration::from_millis(5));
        assert!(!p.finish(r), "the stale read was shown");
        // It still ends: the next tick can read again.
        assert!(p.due(t0 + EVERY * 2, Reason::Tick));
    }

    #[test]
    fn of_two_reads_only_the_later_is_shown() {
        // A reconnect's read overtaking the tick's.
        let t0 = Instant::now();
        let mut p = UpdatePoll::default();
        let first = p.start(t0);
        let second = p.start(t0);
        assert!(!p.finish(first));
        assert!(
            !p.due(t0 + EVERY * 2, Reason::Tick),
            "the later read is still out"
        );
        assert!(p.finish(second));
        assert!(p.due(t0 + EVERY * 2, Reason::Tick));
    }

    #[test]
    fn a_read_with_nothing_after_it_is_shown() {
        let mut p = UpdatePoll::default();
        p.event(Instant::now());
        let r = p.start(Instant::now());
        assert!(p.finish(r));
    }

    #[test]
    fn notices_the_computer_sleeping() {
        let t0 = Instant::now();
        let w0 = SystemTime::now();
        let mut p = UpdatePoll::default();
        assert!(!p.slept(t0, w0), "the first tick has nothing to compare");
        // Awake: both clocks run together, give or take NTP.
        let (t1, w1) = (
            t0 + Duration::from_secs(1),
            w0 + Duration::from_millis(1_050),
        );
        assert!(!p.slept(t1, w1));
        // A night's sleep: 8 hours on the wall, 150 ms of monotonic time.
        let (t2, w2) = (
            t1 + Duration::from_millis(150),
            w1 + Duration::from_secs(8 * 3600),
        );
        assert!(p.slept(t2, w2));
        // Once: the next tick is awake again.
        assert!(!p.slept(t2 + Duration::from_secs(1), w2 + Duration::from_secs(1)));
    }

    #[test]
    fn a_clock_set_back_or_nudged_is_not_sleep() {
        let t0 = Instant::now();
        let w0 = SystemTime::now();
        let mut p = UpdatePoll::default();
        p.slept(t0, w0);
        // Set back an hour: not a sleep.
        assert!(!p.slept(t0 + Duration::from_secs(1), w0 - Duration::from_secs(3600)));
        // Stepped forward under SLEPT by NTP: not a sleep either.
        let t = t0 + Duration::from_secs(2);
        assert!(!p.slept(t, w0 - Duration::from_secs(3600) + Duration::from_secs(60)));
    }
}
