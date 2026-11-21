//! Run slots (internal/runslots, daemon/runslots.go) for the fake's agents:
//! FAKE_RUN_SLOTS=1 gives the VM two, held by the first two running agents,
//! with the rest of the running ones waiting in line, which GET /v1/run-slots
//! says and each agent's `runSlot` (fleet, agents events) agrees with.
//!
//! Unless FAKE_STILL is set (or FAKE_RUN_SLOTS=live says it moves anyway),
//! the front of the line takes a slot every ten seconds from the agent that held one longest, which goes to the back of
//! the line, each published as an agent event and a run-slots event, as the
//! real daemon does. Agents that stop (the churn) drop out of the line.
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{Duration, SystemTime};

use agentbox_api::*;
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json};

/// SLOTS is how many runs fit at once.
const SLOTS: usize = 2;
/// SLOT_SIZE is what one run of the demo project is counted as: its learned
/// peak, in bytes.
const SLOT_SIZE: i64 = 3_435_973_837; // 3.2 GiB
const GIB: i64 = 1 << 30;
/// POOL is what every agent's machine may use together, and CODE_USE what
/// the ones coding use of it.
const POOL: i64 = 12 * GIB;
const CODE_USE: i64 = 2_791_728_742; // 2.6 GiB
/// TURN is how often the line moves.
const TURN: Duration = Duration::from_secs(10);

/// STEP is how many turns the line has taken.
static STEP: AtomicU64 = AtomicU64::new(0);

/// enabled is whether FAKE_RUN_SLOTS is set: without it the fake has no run
/// slots, as a daemon from before them.
pub fn enabled() -> bool {
    std::env::var_os("FAKE_RUN_SLOTS").is_some()
}

/// moving is whether the line takes its turns: unless FAKE_STILL, which
/// FAKE_RUN_SLOTS=live overrides.
pub fn moving() -> bool {
    enabled() && (!crate::still() || std::env::var("FAKE_RUN_SLOTS").is_ok_and(|v| v == "live"))
}

/// base is the moment the slots' times are counted from: when the fake
/// started, or its fixed moment under FAKE_STILL.
fn base() -> SystemTime {
    static BASE: std::sync::OnceLock<SystemTime> = std::sync::OnceLock::new();
    *BASE.get_or_init(crate::clock)
}

fn at(step: u64, minutes_ago: u64, seconds_ahead: u64) -> String {
    let t = base() + TURN * step as u32 + Duration::from_secs(seconds_ahead)
        - Duration::from_secs(minutes_ago * 60);
    humantime::format_rfc3339_seconds(t).to_string()
}

/// assign gives the running agents their places: the first SLOTS, taken in
/// turns by step, hold a slot, longest first, and the rest wait in line.
/// Agents that aren't running have none.
pub fn assign(fleet: &mut [FleetAgent], step: u64) {
    let mut running: Vec<usize> = (0..fleet.len())
        .filter(|&i| fleet[i].state == "running")
        .collect();
    if !running.is_empty() {
        let by = step as usize % running.len();
        running.rotate_left(by);
    }
    for a in fleet.iter_mut() {
        a.run_slot = None;
    }
    for (place, &i) in running.iter().enumerate() {
        fleet[i].run_slot = Some(if place < SLOTS {
            // The longest holder first: it took its slot, minutes ago, one
            // turn before the next one did.
            let held = (SLOTS - 1 - place) as u64;
            RunSlot {
                state: RUN_SLOT_HOLDING.into(),
                since: at(step, 4 + 5 * held, 0),
                expires: Some(at(step, 0, 180)),
                ..Default::default()
            }
        } else {
            RunSlot {
                state: RUN_SLOT_WAITING.into(),
                ahead: (place - SLOTS) as i64,
                since: at(step, 2, (place - SLOTS) as u64 * 40),
                ..Default::default()
            }
        });
    }
}

/// view is GET /v1/run-slots for the fleet as assigned: who holds a slot,
/// who waits, and what each project's run counts as.
pub fn view(fleet: &[FleetAgent], neighbours: &[(String, usize)], project: &str) -> RunSlots {
    let mut holders: Vec<RunSlotHolder> = Vec::new();
    let mut queue: Vec<RunSlotWaiter> = Vec::new();
    for a in fleet {
        let Some(s) = &a.run_slot else { continue };
        if s.state == RUN_SLOT_HOLDING {
            let n = holders.len() as i64;
            holders.push(RunSlotHolder {
                agent: a.r#ref.clone(),
                project: a.project.clone(),
                since: s.since.clone(),
                expires: s.expires.clone().unwrap_or_default(),
                reserved: SLOT_SIZE,
                r#use: 2 * GIB - n * 600 * 1024 * 1024,
                peak: 2 * GIB + 700 * 1024 * 1024 - n * 500 * 1024 * 1024,
            });
        } else {
            queue.push(RunSlotWaiter {
                agent: a.r#ref.clone(),
                project: a.project.clone(),
                ahead: s.ahead,
                since: s.since.clone(),
            });
        }
    }
    holders.sort_by(|a, b| a.since.cmp(&b.since));
    queue.sort_by_key(|w| w.ahead);
    let in_use = holders.len() as i64;
    let free = (SLOTS as i64 - in_use).max(0);
    let mut projects = vec![ProjectRunSlots {
        project: project.into(),
        peak: SLOT_SIZE,
        learned: true,
        runs: 7,
        free,
        in_use,
        queued: queue.len() as i64,
    }];
    // The other projects, whose runs aren't measured yet.
    projects.extend(neighbours.iter().map(|(name, _)| ProjectRunSlots {
        project: name.clone(),
        peak: 2 * GIB,
        free,
        ..Default::default()
    }));
    projects.sort_by(|a, b| a.project.cmp(&b.project));
    RunSlots {
        total: in_use + free,
        free,
        in_use,
        queued: queue.len() as i64,
        slot_size: SLOT_SIZE,
        pool: POOL,
        code_use: CODE_USE,
        free_memory: POOL - CODE_USE - in_use * SLOT_SIZE,
        holders,
        queue,
        projects,
    }
}

/// current is the view of the fake's own fleet.
fn current(fake: &Fake) -> RunSlots {
    let fleet = fake.fleet.lock().unwrap().clone();
    view(&fleet, &fake.opts.neighbours, &fake.opts.project)
}

/// route answers GET /v1/run-slots, when FAKE_RUN_SLOTS is set.
pub async fn route(
    fake: &Arc<Fake>,
    req: Request<Incoming>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    if enabled() && req.method() == Method::GET && req.uri().path() == "/v1/run-slots" {
        return Ok(json(StatusCode::OK, &current(fake)));
    }
    Err(Box::new(req))
}

/// rotate moves the line every TURN, and keeps it right as agents stop and
/// start, publishing what changed.
pub async fn rotate(fake: Arc<Fake>) {
    let mut ticks = 0u64;
    loop {
        tokio::time::sleep(Duration::from_secs(2)).await;
        ticks += 1;
        if ticks.is_multiple_of(TURN.as_secs() / 2) {
            STEP.fetch_add(1, Ordering::Relaxed);
        }
        let changes: Vec<AgentChange> = {
            let mut fleet = fake.fleet.lock().unwrap();
            let before: Vec<Option<RunSlot>> = fleet.iter().map(|a| a.run_slot.clone()).collect();
            assign(&mut fleet, STEP.load(Ordering::Relaxed));
            fleet
                .iter()
                .zip(before)
                .filter(|(a, was)| a.run_slot != *was)
                .map(|(a, _)| AgentChange {
                    r#ref: a.r#ref.clone(),
                    state: a.state.clone(),
                    run_slot: a.run_slot.clone(),
                    ..Default::default()
                })
                .collect()
        };
        if changes.is_empty() {
            continue;
        }
        for c in &changes {
            fake.emit(EVENT_AGENT, c);
        }
        fake.emit(EVENT_RUN_SLOTS, &current(&fake));
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fleet(states: &[&str]) -> Vec<FleetAgent> {
        states
            .iter()
            .enumerate()
            .map(|(i, s)| FleetAgent {
                r#ref: format!("demo/agent-{:02}", i + 1),
                project: "demo".into(),
                state: (*s).into(),
                ..Default::default()
            })
            .collect()
    }

    #[test]
    fn the_first_running_agents_hold_and_the_rest_wait_in_line() {
        let mut f = fleet(&["running", "running", "stopped", "running", "running"]);
        assign(&mut f, 0);
        let state = |i: usize| f[i].run_slot.as_ref().map(|s| (s.state.as_str(), s.ahead));
        assert_eq!(state(0), Some(("holding", 0)));
        assert_eq!(state(1), Some(("holding", 0)));
        assert_eq!(state(2), None);
        assert_eq!(state(3), Some(("waiting", 0)));
        assert_eq!(state(4), Some(("waiting", 1)));
        let v = view(&f, &[], "demo");
        assert_eq!((v.total, v.free, v.in_use, v.queued), (2, 0, 2, 2));
        assert_eq!(v.holders.len(), 2);
        assert_eq!(v.queue.iter().map(|w| w.ahead).collect::<Vec<_>>(), [0, 1]);
        assert_eq!(v.queue[0].agent, "demo/agent-04");
        assert!(v.holders[0].since < v.holders[1].since, "longest first");
    }

    #[test]
    fn a_turn_gives_the_front_of_the_line_the_slot_held_longest() {
        let mut f = fleet(&["running", "running", "running", "running"]);
        assign(&mut f, 1);
        let v = view(&f, &[], "demo");
        let holders: Vec<&str> = v.holders.iter().map(|h| h.agent.as_str()).collect();
        // Agent 3, first in line, took agent 1's slot, which went to the
        // back of the line, behind agent 4.
        assert_eq!(holders, ["demo/agent-02", "demo/agent-03"]);
        assert_eq!(v.queue[0].agent, "demo/agent-04");
        assert_eq!(v.queue[1].agent, "demo/agent-01");
        // Every turn is a full circle later.
        let mut again = fleet(&["running", "running", "running", "running"]);
        assign(&mut again, 5);
        assert_eq!(
            f[1].run_slot.as_ref().map(|s| &s.state),
            again[2].run_slot.as_ref().map(|s| &s.state)
        );
    }

    #[test]
    fn fewer_agents_than_slots_leave_some_free() {
        let mut f = fleet(&["running", "stopped"]);
        assign(&mut f, 3);
        let v = view(&f, &[("storefront".into(), 3)], "demo");
        assert_eq!((v.total, v.free, v.in_use, v.queued), (2, 1, 1, 0));
        assert!(v.queue.is_empty());
        // Both projects, by name, the open one's run learned.
        let names: Vec<&str> = v.projects.iter().map(|p| p.project.as_str()).collect();
        assert_eq!(names, ["demo", "storefront"]);
        assert!(v.projects[0].learned && !v.projects[1].learned);
        assert_eq!(v.free_memory, POOL - CODE_USE - SLOT_SIZE);
    }

    #[test]
    fn nobody_running_holds_nothing() {
        let mut f = fleet(&["stopped", "paused"]);
        assign(&mut f, 0);
        assert!(f.iter().all(|a| a.run_slot.is_none()));
        let v = view(&f, &[], "demo");
        assert_eq!((v.total, v.free, v.in_use), (2, 2, 0));
    }
}
