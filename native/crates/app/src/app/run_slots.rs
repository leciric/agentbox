//! The VM's run slots in the top bar (RunSlotsPill.tsx, internal/runslots):
//! "Run slots 2 of 3 free". An agent's run phase (the app, docker compose,
//! Playwright, its desktop, browser or emulator, recordings) takes a slot
//! and waits its turn for one while none is free; coding never waits. The
//! popover lists who holds a slot, who waits and what each project's run is
//! counted as, and a row opens its agent. Amber while agents wait. Nothing
//! while no agent runs, or from a daemon without run slots.
//!
//! Read as the app connects, every ten seconds (what the holders use moves
//! without an event), and kept by EVENT_RUN_SLOTS; an agent's own place in
//! line (Agent.runSlot) arrives with its EVENT_AGENT and is read in its
//! status line (agent_status.rs).
use agentbox_api::{Agent, ProjectRunSlots, RunSlotHolder, RunSlotWaiter, RunSlots};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::topbar::{chip, popover};
use crate::files::human_bytes;
use crate::theme;

/// The window's width from which the pill says "Run slots 2 of 3 free", not
/// just "2/3" (Tailwind's xl).
const WIDE: f32 = 1280.;

#[derive(Default)]
pub(super) struct RunSlotsState {
    /// slots is the last reading, None before the first or from a daemon
    /// that has no run slots.
    pub(super) slots: Option<RunSlots>,
}

/// shown is whether the pill is: while an agent runs, or a slot is held or
/// wanted.
fn shown(slots: &RunSlots, agents: &[Agent]) -> bool {
    agents.iter().any(|a| a.state == "running") || slots.in_use > 0 || slots.queued > 0
}

/// title_of is an agent as the popover names it: "Title · project/agent",
/// or just its ref when it has no title or isn't known.
fn title_of(agents: &[Agent], r#ref: &str) -> String {
    match agents.iter().find(|a| a.r#ref == r#ref) {
        Some(a) if !a.title.is_empty() => format!("{} · {}", a.title, r#ref),
        _ => r#ref.to_string(),
    }
}

/// clock is a moment as the user's clock reads it: 16:11.
fn clock(time: &str) -> String {
    crate::dates::local(time).map_or_else(String::new, |d| d.format("%H:%M").to_string())
}

fn counts(slots: &RunSlots) -> [(&'static str, agentbox_i18n::Value<'static>); 2] {
    [("free", slots.free.into()), ("total", slots.total.into())]
}

/// holder_detail is when a holder took its slot and when it goes unless used.
fn holder_detail(h: &RunSlotHolder) -> String {
    tv(
        "shell.runSlots.held",
        &[
            ("since", clock(&h.since).into()),
            ("until", clock(&h.expires).into()),
        ],
    )
}

/// holder_side is what a holder's machine uses now and what its slot reserves.
fn holder_side(h: &RunSlotHolder) -> String {
    tv(
        "shell.runSlots.use",
        &[
            ("use", human_bytes(h.r#use).into()),
            ("reserved", human_bytes(h.reserved).into()),
        ],
    )
}

fn waiter_detail(w: &RunSlotWaiter) -> String {
    tv(
        "shell.runSlots.waiting",
        &[("since", clock(&w.since).into())],
    )
}

fn waiter_side(w: &RunSlotWaiter) -> String {
    tv("shell.runSlots.ahead", &[("ahead", w.ahead.into())])
}

/// peak_line is what one of a project's runs is counted as: learned from
/// its runs, or the default until one is measured.
fn peak_line(p: &ProjectRunSlots) -> String {
    if p.learned {
        tv(
            "shell.runSlots.peakLearned",
            &[
                ("peak", human_bytes(p.peak).into()),
                ("runs", p.runs.into()),
            ],
        )
    } else {
        tv(
            "shell.runSlots.peakDefault",
            &[("peak", human_bytes(p.peak).into())],
        )
    }
}

/// memory_line is what is left of the memory agents may use, and what the
/// ones coding use of it.
fn memory_line(slots: &RunSlots) -> String {
    tv(
        "shell.runSlots.memory",
        &[
            ("free", human_bytes(slots.free_memory).into()),
            ("pool", human_bytes(slots.pool).into()),
            ("code", human_bytes(slots.code_use).into()),
        ],
    )
}

fn section(title: String) -> Div {
    v_flex().gap_1p5().child(
        div()
            .text_xs()
            .font_weight(FontWeight::MEDIUM)
            .text_color(theme::secondary())
            .child(title),
    )
}

/// row is an agent in the popover: its name and a side note, with what it
/// has been doing under them. Clicking it opens the agent.
fn row(
    id: (&'static str, usize),
    name: String,
    detail: String,
    side: String,
    warn: bool,
) -> Stateful<Div> {
    v_flex()
        .id(id)
        .min_w_0()
        .gap_0p5()
        .px_1p5()
        .py_1()
        .rounded_md()
        .cursor_pointer()
        .hover(|s| s.bg(theme::surface_strong()))
        .active(|s| s.bg(theme::surface_vivid()))
        .child(
            h_flex()
                .min_w_0()
                .items_baseline()
                .justify_between()
                .gap_3()
                .text_xs()
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::primary())
                        .child(name),
                )
                .child(
                    div()
                        .flex_none()
                        .text_color(if warn {
                            theme::amber(200)
                        } else {
                            theme::muted()
                        })
                        .child(side),
                ),
        )
        .child(
            div()
                .truncate()
                .text_xs()
                .text_color(theme::faint())
                .child(detail),
        )
}

impl AppView {
    /// load_run_slots reads who holds a run slot and who waits. A daemon
    /// without them answers an error, and the pill stays away.
    pub(super) fn load_run_slots(&mut self, cx: &mut Context<Self>) {
        let client = self.client.clone();
        cx.spawn(async move |this, cx| {
            let res = client.get::<RunSlots>("/v1/run-slots").await;
            this.update(cx, |this, cx| {
                if let Ok(slots) = res {
                    this.apply_run_slots(slots, cx);
                }
            })
            .ok();
        })
        .detach();
    }

    /// apply_run_slots shows slots, from a read or an event.
    pub(super) fn apply_run_slots(&mut self, slots: RunSlots, cx: &mut Context<Self>) {
        if self.run_slots.slots.as_ref() != Some(&slots) {
            self.run_slots.slots = Some(slots);
            cx.notify();
        }
    }

    /// run_slots_pill is the chip in the top bar, with its popover, between
    /// the disk's and the account's limits.
    pub(super) fn run_slots_pill(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let slots = self.run_slots.slots.as_ref()?;
        if !shown(slots, &self.side.agents) {
            return None;
        }
        let waiting = slots.queued > 0;
        let label = if self.widths.window >= WIDE {
            tv("shell.runSlots.label", &counts(slots))
        } else {
            tv("shell.runSlots.short", &counts(slots))
        };
        let amber = theme::amber(200);
        let trigger = if waiting {
            chip("run-slots", amber)
                .bg(theme::amber(400).opacity(0.1))
                .hover(|s| s.bg(theme::amber(400).opacity(0.15)))
        } else {
            chip("run-slots", theme::secondary())
                .bg(theme::surface_raised())
                .hover(|s| s.bg(theme::surface_strong()))
        }
        .child(crate::icons::of("▶"))
        .child(label);
        let slots = slots.clone();
        let agents = self.side.agents.clone();
        Some(self.with_pop(
            "run-slots",
            trigger,
            move |_, cx| {
                let open = |r: String| {
                    cx.listener(move |this, _: &ClickEvent, window, cx| {
                        this.topbar.pop = None;
                        this.open_agent_ref(r.clone(), window, cx);
                    })
                };
                let holders = if slots.holders.is_empty() {
                    vec![
                        div()
                            .text_xs()
                            .text_color(theme::muted())
                            .child(t("shell.runSlots.nobody"))
                            .into_any_element(),
                    ]
                } else {
                    slots
                        .holders
                        .iter()
                        .enumerate()
                        .map(|(i, h)| {
                            row(
                                ("run-slot-holder", i),
                                title_of(&agents, &h.agent),
                                holder_detail(h),
                                holder_side(h),
                                false,
                            )
                            .on_click(open(h.agent.clone()))
                            .into_any_element()
                        })
                        .collect()
                };
                let queue: Vec<_> = slots
                    .queue
                    .iter()
                    .enumerate()
                    .map(|(i, w)| {
                        row(
                            ("run-slot-waiter", i),
                            title_of(&agents, &w.agent),
                            waiter_detail(w),
                            waiter_side(w),
                            true,
                        )
                        .on_click(open(w.agent.clone()))
                    })
                    .collect();
                popover("run-slots-pop", 384., cx)
                    .child(
                        v_flex()
                            .gap_1()
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(if waiting { amber } else { theme::primary() })
                                    .child(tv("shell.runSlots.title", &counts(&slots))),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::secondary())
                                    .child(t("shell.runSlots.explain")),
                            ),
                    )
                    .child(section(t("shell.runSlots.holders")).children(holders))
                    .when(!queue.is_empty(), |el| {
                        el.child(section(t("shell.runSlots.queue")).children(queue))
                    })
                    .when(!slots.projects.is_empty(), |el| {
                        el.child(section(t("shell.runSlots.projects")).children(
                            slots.projects.iter().map(|p| {
                                h_flex()
                                    .min_w_0()
                                    .items_baseline()
                                    .justify_between()
                                    .gap_3()
                                    .text_xs()
                                    .child(
                                        div()
                                            .min_w_0()
                                            .truncate()
                                            .text_color(theme::secondary())
                                            .child(
                                                div()
                                                    .flex()
                                                    .gap_1()
                                                    .child(
                                                        div()
                                                            .text_color(theme::primary())
                                                            .child(p.project.clone()),
                                                    )
                                                    .child(peak_line(p)),
                                            ),
                                    )
                                    .child(div().flex_none().text_color(theme::muted()).child(tv(
                                        "shell.runSlots.fits",
                                        &[("free", p.free.into())],
                                    )))
                            }),
                        ))
                    })
                    .when(slots.pool > 0, |el| {
                        el.child(
                            div()
                                .pt_2p5()
                                .border_t_1()
                                .border_color(theme::line_faint())
                                .text_xs()
                                .text_color(theme::faint())
                                .child(memory_line(&slots)),
                        )
                    })
            },
            cx,
        ))
    }
}

#[cfg(test)]
mod tests {
    // Not `super::*`: it brings gpui's `test` macro in, which shadows #[test].
    use super::{
        Agent, ProjectRunSlots, RunSlotHolder, RunSlotWaiter, RunSlots, counts, holder_side,
        memory_line, peak_line, shown, title_of, waiter_side,
    };
    use agentbox_i18n::tv;

    fn agent(r#ref: &str, title: &str, state: &str) -> Agent {
        Agent {
            r#ref: r#ref.into(),
            title: title.into(),
            state: state.into(),
            ..Default::default()
        }
    }

    #[test]
    fn the_pill_shows_while_an_agent_runs_or_a_slot_is_held_or_wanted() {
        let idle = [agent("p/a", "", "stopped")];
        let running = [agent("p/a", "", "running")];
        let none = RunSlots {
            total: 1,
            free: 1,
            ..Default::default()
        };
        assert!(!shown(&none, &idle));
        assert!(shown(&none, &running));
        assert!(shown(
            &RunSlots {
                in_use: 1,
                ..none.clone()
            },
            &idle
        ));
        assert!(shown(
            &RunSlots {
                queued: 1,
                ..none.clone()
            },
            &idle
        ));
    }

    #[test]
    fn an_agent_is_named_by_its_title_and_ref() {
        let agents = [agent("p/agent-01", "Fix the bug", "running")];
        assert_eq!(title_of(&agents, "p/agent-01"), "Fix the bug · p/agent-01");
        assert_eq!(title_of(&agents, "p/agent-09"), "p/agent-09");
        assert_eq!(
            title_of(&[agent("p/agent-02", "", "running")], "p/agent-02"),
            "p/agent-02"
        );
    }

    #[test]
    fn the_words_are_electrons() {
        let slots = RunSlots {
            total: 3,
            free: 2,
            ..Default::default()
        };
        assert_eq!(
            tv("shell.runSlots.label", &counts(&slots)),
            "Run slots 2 of 3 free"
        );
        assert_eq!(tv("shell.runSlots.short", &counts(&slots)), "2/3");
        let w = |ahead| RunSlotWaiter {
            ahead,
            ..Default::default()
        };
        assert_eq!(waiter_side(&w(0)), "next");
        assert_eq!(waiter_side(&w(2)), "2 ahead");
        let holder = RunSlotHolder {
            reserved: 3 << 30,
            r#use: 2 << 30,
            ..Default::default()
        };
        assert_eq!(holder_side(&holder), "2.0 GiB used, 3.0 GiB reserved");
        let project = |learned, runs, peak| ProjectRunSlots {
            project: "demo".into(),
            peak,
            learned,
            runs,
            ..Default::default()
        };
        assert_eq!(
            peak_line(&project(true, 7, 3 << 30)),
            "3.0 GiB, learned from 7 runs"
        );
        assert_eq!(
            peak_line(&project(true, 1, 3 << 30)),
            "3.0 GiB, learned from 1 run"
        );
        assert_eq!(
            peak_line(&project(false, 0, 2 << 30)),
            "2.0 GiB until a run is measured"
        );
        assert_eq!(
            tv("shell.runSlots.fits", &[("free", 0i64.into())]),
            "none fit now"
        );
        assert_eq!(
            tv("shell.runSlots.fits", &[("free", 1i64.into())]),
            "1 fits now"
        );
        assert_eq!(
            tv("shell.runSlots.fits", &[("free", 3i64.into())]),
            "3 fit now"
        );
    }

    #[test]
    fn the_memory_line_says_what_is_free_of_the_pool() {
        let slots = RunSlots {
            pool: 12 << 30,
            free_memory: 3 << 30,
            code_use: 2 << 30,
            ..Default::default()
        };
        assert_eq!(
            memory_line(&slots),
            "3.0 GiB free of the 12.0 GiB agents may use; 2.0 GiB used coding"
        );
    }
}
