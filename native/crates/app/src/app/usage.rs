//! The top bar's meters (TopBar.tsx's ClaudeUsage, DiskGuardPill.tsx and
//! MachineStatus.tsx's bubble, D85): how much of the plan's limits the
//! account in use has spent, the disk AgentBox writes to when it's short,
//! and how the machine is doing.
//!
//! - Claude's (or Codex's) limits, from GET /v1/limits(?tool=codex): the
//!   account what's open spends — the agent's, the project's chat's (what
//!   its composer chose), or on Home the machine's default (pickMeter) —
//!   with its five-hour window's countdown and share, and the week's. Its
//!   popover has every window as a bar, the warning past 85%, every other
//!   account's reading and when the reading was taken. A tool that reports
//!   no limits says so quietly.
//! - The disk (GET /v1/disk): a pill only when it's low or full, its
//!   popover naming the disk, what fills it and the floor kept free.
//! - The machine (GET /v1/usage, GET /v1/agents): one bubble saying how
//!   things are, the worst first, its popover with the connection, memory,
//!   CPU, disk and agents. The VM's own power (pause, resize, free memory)
//!   is Electron's main process's, and isn't here.
use agentbox_api::{Agent, ClaudeLimit, ClaudeLimitWindow, DiskGuard, HostUsage, Usage};
use agentbox_i18n::{t, tv};
use gpui::prelude::FluentBuilder as _;
use gpui::*;
use gpui_component::{h_flex, v_flex};

use super::AppView;
use super::settings::defaults::time_ago;
use super::topbar::{chip, popover};
use crate::files::human_bytes;
use crate::theme;

/// memoryFull is the share past which memory reads as tight.
const MEMORY_FULL: f64 = 85.;

#[derive(Default)]
pub(super) struct Meters {
    limits: Vec<ClaudeLimit>,
    codex: Vec<ClaudeLimit>,
    disk: Option<DiskGuard>,
    host: Option<HostUsage>,
    agents: Vec<Agent>,
}

fn claude() -> Hsla {
    theme::claude()
}

fn now_ms() -> i64 {
    chrono::Utc::now().timestamp_millis()
}

fn millis(time: &str) -> Option<i64> {
    chrono::DateTime::parse_from_rfc3339(time)
        .ok()
        .map(|d| d.timestamp_millis())
}

/// window_now is a window's share used, or None once it has reset since
/// the reading (windowNow).
fn window_now(w: &ClaudeLimitWindow, now: i64) -> Option<f64> {
    match millis(&w.resets_at) {
        Some(r) if r > 0 && r <= now => None,
        _ => Some(w.utilization),
    }
}

/// window_left is the time a window has left, to the minute (windowLeft).
fn window_left(resets_at: &str, now: i64) -> Option<String> {
    let r = millis(resets_at).filter(|r| *r > now)?;
    let minutes = (r - now) / 60_000;
    if minutes < 1 {
        return Some(t("shell.top.leftLessThanMinute"));
    }
    let (h, m) = (minutes / 60, minutes % 60);
    Some(match (h, m) {
        (0, m) => tv("shell.top.leftMinutes", &[("m", m.into())]),
        (h, 0) => tv("shell.top.leftHours", &[("h", h.into())]),
        (h, m) => tv(
            "shell.top.leftHoursMinutes",
            &[("h", h.into()), ("m", m.into())],
        ),
    })
}

/// time_until is how long until a time, roughly (timeUntil).
fn time_until(time: &str, now: i64) -> String {
    let s = ((millis(time).unwrap_or(now) - now) / 1000).max(0);
    if s < 3600 {
        tv("common.inMinutes", &[("n", ((s + 30) / 60).max(1).into())])
    } else if s < 86400 {
        tv("common.inHours", &[("n", ((s + 1800) / 3600).into())])
    } else {
        tv("common.inDays", &[("n", ((s + 43200) / 86400).into())])
    }
}

/// tone is how worrying a share is (limitTone): 0 fine, 1 warn, 2 high.
fn tone(f: Option<f64>) -> u8 {
    match f {
        Some(f) if f > 0.85 => 2,
        Some(f) if f > 0.65 => 1,
        _ => 0,
    }
}

fn tone_color(tone: u8, fine: Hsla) -> Hsla {
    match tone {
        2 => theme::error(),
        1 => theme::starting(),
        _ => fine,
    }
}

fn percent(f: Option<f64>) -> String {
    match f {
        Some(f) => format!("{}%", (f.clamp(0., 1.) * 100.).round()),
        None => "—".into(),
    }
}

fn tool_name(tool: &str) -> &'static str {
    match tool {
        "codex" => "Codex",
        "opencode" => "OpenCode",
        "cursor" => "Cursor",
        "gemini" => "Gemini CLI",
        _ => "Claude",
    }
}

/// OpenAgent is what the meter needs of the open agent.
struct OpenAgent<'a> {
    ai: &'a str,
    title: &'a str,
    name: &'a str,
    claude: &'a str,
    codex: &'a str,
}

/// Pick is the account the meter shows (MeterPick).
struct Pick {
    tool: String,
    reading: Option<ClaudeLimit>,
    whose: String,
}

impl Meters {
    /// host is the machine's last reading (GET /v1/usage).
    pub(super) fn host(&self) -> Option<HostUsage> {
        self.host.clone()
    }
}

impl AppView {
    /// watch_meters reads the meters: the limits and the disk every thirty
    /// seconds, the machine every five.
    pub(super) fn watch_meters(&mut self, tick: u32, cx: &mut Context<Self>) {
        let client = self.client.clone();
        let slow = tick.is_multiple_of(30);
        cx.spawn(async move |this, cx| {
            let usage = client.get::<Usage>("/v1/usage").await.ok();
            let agents = client.get::<Vec<Agent>>("/v1/agents").await.ok();
            // Home's Status tab lists the recent jobs (HomeView's useQuery).
            let jobs = client.get::<Vec<agentbox_api::Job>>("/v1/jobs").await.ok();
            let slow = if slow {
                Some((
                    client.get::<Vec<ClaudeLimit>>("/v1/limits").await.ok(),
                    client
                        .get::<Vec<ClaudeLimit>>("/v1/limits?tool=codex")
                        .await
                        .ok(),
                    client.get::<DiskGuard>("/v1/disk").await.ok(),
                ))
            } else {
                None
            };
            this.update(cx, |this, cx| {
                let m = &mut this.meters;
                m.host = usage.map(|u| u.host);
                if let Some(j) = jobs {
                    this.home_status.jobs = j;
                }
                let m = &mut this.meters;
                if let Some(a) = agents {
                    m.agents = a;
                }
                if let Some((limits, codex, disk)) = slow {
                    if let Some(l) = limits {
                        m.limits = l;
                    }
                    if let Some(c) = codex {
                        m.codex = c;
                    }
                    m.disk = disk;
                }
                cx.notify();
            })
            .ok();
        })
        .detach();
    }

    /// pick_meter is pickMeter: the open agent's account, else the open
    /// chat's, else the project's, else the default.
    fn pick_meter(&self, cx: &App) -> Pick {
        let m = &self.meters;
        let by = |list: &[ClaudeLimit], tool: &str, name: &str, whose: String| Pick {
            tool: tool.into(),
            reading: if name.is_empty() {
                list.iter().find(|l| l.default).cloned()
            } else {
                list.iter().find(|l| l.account == name).cloned()
            },
            whose: if name.is_empty() {
                t("vm.usageMeter.defaultAccount")
            } else {
                whose
            },
        };
        let project = self
            .project
            .as_ref()
            .filter(|_| !self.is_home())
            .and_then(|p| self.projects.iter().find(|x| &x.name == p));
        // The open agent as every agent's list has it (GET /v1/agents, as
        // TopBar.tsx finds it among them all), else as its project's.
        let open_ref = (self.open != "lead" && !self.open.starts_with("lead."))
            .then(|| self.project.as_ref().map(|p| format!("{p}/{}", self.open)))
            .flatten();
        let every = open_ref
            .as_ref()
            .and_then(|r| m.agents.iter().find(|a| &a.r#ref == r));
        let open = every.map(|a| {
            (
                a.ai.as_str(),
                a.title.as_str(),
                a.name.as_str(),
                a.claude_account.as_str(),
                a.codex_account.as_str(),
            )
        });
        let open = open.or_else(|| {
            self.open_agent().map(|a| {
                (
                    a.ai.as_str(),
                    a.title.as_str(),
                    a.name.as_str(),
                    a.claude_account.as_str(),
                    a.codex_account.as_str(),
                )
            })
        });
        if let Some((ai, title, name, claude, codex)) =
            open.filter(|(ai, ..)| !ai.is_empty() && *ai != "none")
        {
            let a = OpenAgent {
                ai,
                title,
                name,
                claude,
                codex,
            };
            let name = if a.title.is_empty() { a.name } else { a.title };
            let whose = tv("vm.usageMeter.accountOf", &[("name", name.into())]);
            return match a.ai {
                "codex" => by(&m.codex, "codex", a.codex, whose),
                "claude" => by(&m.limits, "claude", a.claude, whose),
                other => Pick {
                    tool: other.into(),
                    reading: None,
                    whose,
                },
            };
        }
        let whose = project
            .map(|p| {
                let label = if p.display_name.is_empty() {
                    &p.name
                } else {
                    &p.display_name
                };
                tv("vm.usageMeter.accountOf", &[("name", label.into())])
            })
            .unwrap_or_default();
        let lead = (self.open == "lead")
            .then(|| {
                self.chat
                    .as_ref()
                    .and_then(|c| c.read(cx).lead_runners().cloned())
            })
            .flatten();
        if let Some(lead) = &lead {
            if lead.tool == "codex" {
                let name = lead
                    .tool_accounts
                    .get("codex")
                    .cloned()
                    .filter(|n| !n.is_empty())
                    .or_else(|| project.map(|p| p.codex_account.clone()))
                    .unwrap_or_default();
                return by(&m.codex, "codex", &name, whose);
            }
            if lead.tool != "claude" && !lead.tool.is_empty() {
                return Pick {
                    tool: lead.tool.clone(),
                    reading: None,
                    whose,
                };
            }
            if !lead.account.is_empty() {
                let whose = if whose.is_empty() {
                    tv(
                        "vm.usageMeter.accountOf",
                        &[("name", t("chat.home.agentTitle").into())],
                    )
                } else {
                    whose
                };
                return by(&m.limits, "claude", &lead.account, whose);
            }
        }
        match project {
            Some(p) => by(&m.limits, "claude", &p.claude_account, whose),
            None => by(&m.limits, "claude", "", String::new()),
        }
    }

    /// usage_chips are the meters, in the bar's order: the disk, the
    /// account's limits, the machine.
    pub(super) fn usage_chips(&self, cx: &mut Context<Self>) -> Vec<AnyElement> {
        let mut out = Vec::new();
        out.extend(self.disk_pill(cx));
        out.extend(self.claude_usage(cx));
        out.push(self.machine_bubble(cx));
        out
    }

    fn with_pop(
        &self,
        which: &'static str,
        trigger: Stateful<Div>,
        pop: impl FnOnce(&Self, &mut Context<Self>) -> Stateful<Div>,
        cx: &mut Context<Self>,
    ) -> AnyElement {
        let open = self.topbar.pop == Some(which);
        let pop = open.then(|| pop(self, cx));
        div()
            .flex_none()
            .child(trigger.on_click(
                cx.listener(move |this, _: &ClickEvent, _, cx| this.toggle_pop(which, cx)),
            ))
            .children(pop.map(|p| {
                deferred(
                    anchored()
                        .offset(point(px(0.), px(28.)))
                        .snap_to_window_with_margin(px(8.))
                        .child(p),
                )
            }))
            .into_any_element()
    }

    fn claude_usage(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let now = now_ms();
        let pick = self.pick_meter(cx);
        let codex = pick.tool == "codex";
        let account = pick.reading.as_ref();
        let five = account.and_then(|a| {
            a.windows
                .iter()
                .find(|w| w.name == "five_hour")
                .or(a.windows.first())
        });
        let (Some(account), Some(five)) = (account, five) else {
            // A tool with no limits says so; Claude with no reading hides.
            if codex || matches!(pick.tool.as_str(), "opencode" | "cursor" | "gemini") {
                let tool = tool_name(&pick.tool);
                return Some(
                    h_flex()
                        .flex_none()
                        .gap_1p5()
                        .px_2p5()
                        .py_0p5()
                        .rounded_full()
                        .border_1()
                        .border_color(theme::line())
                        .text_xs()
                        .text_color(theme::faint())
                        .child(if codex {
                            tv("shell.top.noReading", &[("tool", tool.into())])
                        } else {
                            t("shell.top.noLimits")
                        })
                        .into_any_element(),
                );
            }
            return None;
        };
        let week = account.windows.iter().find(|w| w.name == "seven_day");
        let five_now = window_now(five, now);
        let week_now = week.and_then(|w| window_now(w, now));
        let five_left = five_now.and_then(|_| window_left(&five.resets_at, now));
        let worst = tone(Some(five_now.unwrap_or(0.).max(week_now.unwrap_or(0.))));
        let base = if codex { theme::muted() } else { claude() };
        let ring = if worst == 2 { theme::error() } else { base };
        let name = if !account.account.is_empty() && account.account != "default" {
            account.account.clone()
        } else {
            tool_name(&pick.tool).to_string()
        };
        let trigger = h_flex()
            .id("claude-meter")
            .flex_none()
            .gap_1p5()
            .pl_2()
            .pr_2p5()
            .py_0p5()
            .rounded_full()
            .border_1()
            .border_color(ring.opacity(0.35))
            .bg(ring.opacity(0.1))
            .hover(move |s| s.bg(ring.opacity(0.16)))
            .cursor_pointer()
            .text_xs()
            .child(div().text_color(base).child(crate::icons::of(if codex {
                "◎"
            } else {
                "✳"
            })))
            .child(
                div()
                    .font_weight(FontWeight::MEDIUM)
                    .text_color(theme::primary())
                    .child(name),
            )
            .child(
                div().text_color(theme::muted()).child(
                    five_left
                        .clone()
                        .unwrap_or_else(|| t("shell.top.fiveHourShort")),
                ),
            )
            .child(
                div()
                    .font_family(theme::mono())
                    .font_weight(FontWeight::SEMIBOLD)
                    .text_color(tone_color(tone(five_now), theme::primary()))
                    .child(percent(five_now)),
            )
            .when(week.is_some(), |el| {
                el.child(
                    div()
                        .text_color(theme::muted())
                        .child(t("shell.top.weekShort")),
                )
                .child(
                    div()
                        .font_family(theme::mono())
                        .text_color(tone_color(tone(week_now), theme::primary()))
                        .child(percent(week_now)),
                )
            });
        let account = account.clone();
        let five = five.clone();
        let whose = pick.whose.clone();
        let others: Vec<ClaudeLimit> = if codex {
            &self.meters.codex
        } else {
            &self.meters.limits
        }
        .iter()
        .filter(|l| l.account != account.account)
        .cloned()
        .collect();
        Some(self.with_pop(
            "claude",
            trigger,
            move |_, cx| {
                let bar = if codex { theme::accent() } else { claude() };
                let left = five_left
                    .clone()
                    .unwrap_or_else(|| time_until(&five.resets_at, now));
                popover("claude-pop", 320., cx)
                    .child(
                        v_flex()
                            .gap_0p5()
                            .child(
                                h_flex()
                                    .gap_2()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .child(
                                        div().text_color(base).child(crate::icons::of(if codex {
                                            "◎"
                                        } else {
                                            "✳"
                                        })),
                                    )
                                    .child(tv(
                                        if codex {
                                            "shell.top.codexTitle"
                                        } else {
                                            "shell.top.claudeTitle"
                                        },
                                        &[("account", account.account.clone().into())],
                                    )),
                            )
                            .child(div().text_xs().text_color(theme::muted()).child(tv(
                                if codex {
                                    "shell.top.codexWhose"
                                } else {
                                    "shell.top.claudeWhose"
                                },
                                &[("whose", whose.clone().into())],
                            ))),
                    )
                    .children(account.windows.iter().map(|w| limit_row(w, now, bar)))
                    .when(five_now.is_some_and(|f| f > 0.85), |el| {
                        el.child(
                            div()
                                .px_2p5()
                                .py_2()
                                .rounded_lg()
                                .bg(theme::error().opacity(0.1))
                                .border_1()
                                .border_color(theme::error().opacity(0.25))
                                .text_xs()
                                .text_color(theme::primary())
                                .child(tv(
                                    if codex {
                                        "shell.top.codexAtLimit"
                                    } else {
                                        "shell.top.claudeAtLimit"
                                    },
                                    &[("left", left.clone().into())],
                                )),
                        )
                    })
                    .when(!others.is_empty(), |el| {
                        el.child(
                            v_flex()
                                .gap_1()
                                .pt_2p5()
                                .border_t_1()
                                .border_color(theme::line())
                                .text_xs()
                                .child(
                                    div()
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::muted())
                                        .child(t("shell.top.otherAccounts")),
                                )
                                .children(others.iter().map(|l| other_account(l, now))),
                        )
                    })
                    .child(div().text_xs().text_color(theme::faint()).child(tv(
                        "shell.top.asOf",
                        &[("ago", time_ago(&account.at).into())],
                    )))
            },
            cx,
        ))
    }

    fn disk_pill(&self, cx: &mut Context<Self>) -> Option<AnyElement> {
        let guard = self.meters.disk.as_ref()?;
        if guard.level == "ok" || guard.disks.is_empty() {
            return None;
        }
        let worst = guard
            .disks
            .iter()
            .filter(|d| d.level != "ok")
            .min_by(|a, b| {
                let r = |d: &agentbox_api::DiskGuardDisk| d.free as f64 / d.floor.max(1) as f64;
                r(a).total_cmp(&r(b))
            })?
            .clone();
        let disk = worst
            .label
            .split(", ")
            .next()
            .unwrap_or_default()
            .to_string();
        let full = guard.level == "full";
        let wh = tv(
            "shell.diskGuard.where",
            &[
                ("disk", disk.clone().into()),
                ("free", human_bytes(worst.free).into()),
            ],
        );
        let (label, title, body) = if full {
            let paused = if guard.paused.is_empty() {
                String::new()
            } else {
                format!(
                    " {}",
                    tv(
                        "shell.diskGuard.pausedAgents",
                        &[("count", guard.paused.len().into())]
                    )
                )
            };
            let mut body = tv(
                "shell.diskGuard.fullBody",
                &[
                    ("where", wh.into()),
                    ("floor", human_bytes(worst.floor).into()),
                    ("paused", paused.into()),
                ],
            );
            if worst.advice.is_empty() {
                body = format!("{body} {}", t("shell.diskGuard.destroyHint"));
            }
            (
                t("shell.diskGuard.fullLabel"),
                tv("shell.diskGuard.fullTitle", &[("disk", disk.into())]),
                body,
            )
        } else {
            (
                t("shell.diskGuard.lowLabel"),
                tv("shell.diskGuard.lowTitle", &[("disk", disk.into())]),
                tv(
                    "shell.diskGuard.lowBody",
                    &[
                        ("where", wh.into()),
                        ("floor", human_bytes(worst.floor).into()),
                    ],
                ),
            )
        };
        let color = if full {
            theme::error()
        } else {
            theme::starting()
        };
        let trigger = chip("disk-pill", color)
            .child(crate::icons::of("⚠"))
            .child(label);
        let guard = guard.clone();
        Some(self.with_pop(
            "disk",
            trigger,
            move |_, cx| {
                popover("disk-pop", 384., cx)
                    .child(
                        v_flex()
                            .gap_1()
                            .child(
                                div()
                                    .text_sm()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(color)
                                    .child(title.clone()),
                            )
                            .child(
                                div()
                                    .text_xs()
                                    .text_color(theme::muted())
                                    .child(body.clone()),
                            )
                            .when(!worst.advice.is_empty(), |el| {
                                el.child(
                                    div()
                                        .text_xs()
                                        .text_color(theme::muted())
                                        .child(worst.advice.replace('`', "")),
                                )
                            }),
                    )
                    .children(guard.disks.iter().map(disk_row))
                    .when(!guard.paused.is_empty(), |el| {
                        el.child(
                            v_flex()
                                .gap_1()
                                .text_xs()
                                .child(
                                    div()
                                        .font_weight(FontWeight::MEDIUM)
                                        .text_color(theme::muted())
                                        .child(t("shell.diskPill.pausedForIt")),
                                )
                                .child(
                                    div()
                                        .font_family(theme::mono())
                                        .text_color(theme::muted())
                                        .child(guard.paused.join(", ")),
                                ),
                        )
                    })
                    .child(
                        h_flex()
                            .justify_between()
                            .gap_3()
                            .pt_2p5()
                            .border_t_1()
                            .border_color(theme::line())
                            .text_xs()
                            .child(
                                div()
                                    .text_color(theme::faint())
                                    .child(t("shell.diskPill.nothingStopped")),
                            )
                            .child(
                                div()
                                    .id("disk-settings")
                                    .px_2()
                                    .py_1()
                                    .rounded_md()
                                    .font_weight(FontWeight::MEDIUM)
                                    .text_color(theme::accent())
                                    .cursor_pointer()
                                    .hover(|s| s.bg(theme::surface_strong()))
                                    .on_click(cx.listener(|this, _: &ClickEvent, window, cx| {
                                        this.topbar.pop = None;
                                        this.open_settings_at("resources".into(), window, cx)
                                    }))
                                    .child(t("shell.diskPill.settings")),
                            ),
                    )
            },
            cx,
        ))
    }

    /// machine_bubble is MachineStatus' bubble: how things are, in words.
    fn machine_bubble(&self, cx: &mut Context<Self>) -> AnyElement {
        let host = self.meters.host.clone().filter(|h| h.mem_total > 0);
        let memory = host.as_ref().map(|h| {
            (
                h.mem_used,
                h.mem_total,
                h.mem_used as f64 / h.mem_total as f64 * 100.,
            )
        });
        let starting = self.conn.starting.is_some();
        let (kind, color) = match () {
            _ if starting => ("starting", theme::accent()),
            _ if !self.connected => ("offline", theme::error()),
            _ if memory.is_some_and(|m| m.2 > MEMORY_FULL) => ("memory", theme::starting()),
            _ => ("running", theme::running()),
        };
        let running = self
            .meters
            .agents
            .iter()
            .filter(|a| a.state == "running")
            .count();
        let working = self
            .meters
            .agents
            .iter()
            .filter(|a| a.state == "running" && a.chat == "running")
            .count();
        let sentence = match kind {
            "starting" => t("shell.machine.starting"),
            "offline" => t("shell.machine.offline"),
            "memory" => tv("shell.machine.memory", &[("vm", "no".into())]),
            _ => tv(
                "shell.machine.running",
                &[("vm", "no".into()), ("count", running.into())],
            ),
        };
        let trigger = h_flex()
            .id("machine-status")
            .flex_none()
            .gap_2()
            .px_2p5()
            .py_0p5()
            .rounded_full()
            .border_1()
            .border_color(if kind == "running" {
                theme::line()
            } else {
                color.opacity(0.4)
            })
            .when(kind != "running", |el| el.bg(color.opacity(0.1)))
            .hover(|s| s.bg(theme::surface_strong()))
            .cursor_pointer()
            .text_xs()
            .child(div().size(px(6.)).rounded_full().bg(color))
            // All well, the dot says so; the words wait for a wide window.
            .when(kind != "running" || self.widths.window >= 1536., |el| {
                el.child(div().text_color(theme::muted()).child(sentence.clone()))
            });
        let connected = self.connected;
        let error = self.conn.error.clone();
        self.with_pop(
            "machine",
            trigger,
            move |_, cx| {
                let check = |label: String, value: String, tone: Hsla| {
                    h_flex()
                        .gap_2()
                        .min_w_0()
                        .text_xs()
                        .child(div().flex_none().size(px(6.)).rounded_full().bg(tone))
                        .child(div().flex_none().text_color(theme::muted()).child(label))
                        .child(div().flex_1())
                        .child(
                            div()
                                .truncate()
                                .font_family(theme::mono())
                                .text_color(if tone == theme::running() {
                                    theme::primary()
                                } else {
                                    tone
                                })
                                .child(value),
                        )
                };
                let ok = theme::running();
                let warn = theme::starting();
                let state = if connected {
                    "connected"
                } else {
                    "disconnected"
                };
                popover("machine-pop", 320., cx)
                    .child(div().text_sm().font_weight(FontWeight::MEDIUM).child(
                        if kind == "running" {
                            t("shell.machine.allGood")
                        } else {
                            sentence.clone()
                        },
                    ))
                    .child(
                        v_flex()
                            .gap_1p5()
                            .child(check(
                                t("shell.machine.connection"),
                                tv("shell.machine.connectionState", &[("state", state.into())]),
                                if connected { ok } else { theme::error() },
                            ))
                            .when_some(memory.filter(|_| connected), |el, (used, total, pct)| {
                                el.child(check(
                                    t("shell.machine.memoryLabel"),
                                    tv(
                                        "shell.top.usedOf",
                                        &[
                                            ("used", human_bytes(used).into()),
                                            ("size", human_bytes(total).into()),
                                        ],
                                    ),
                                    if pct > MEMORY_FULL { warn } else { ok },
                                ))
                            })
                            .when_some(host.clone().filter(|_| connected), |el, h| {
                                let disk = (h.pool_total > 0).then(|| {
                                    check(
                                        t("shell.machine.diskLabel"),
                                        tv(
                                            "shell.top.usedOf",
                                            &[
                                                ("used", human_bytes(h.pool_used).into()),
                                                ("size", human_bytes(h.pool_total).into()),
                                            ],
                                        ),
                                        if h.pool_used as f64 / h.pool_total as f64 * 100.
                                            > MEMORY_FULL
                                        {
                                            warn
                                        } else {
                                            ok
                                        },
                                    )
                                });
                                el.child(check(
                                    t("shell.machine.cpuLabel"),
                                    tv(
                                        "shell.top.cpuOf",
                                        &[
                                            ("percent", (h.cpu.round() as i64).into()),
                                            ("count", h.cores.into()),
                                        ],
                                    ),
                                    if h.cpu > MEMORY_FULL { warn } else { ok },
                                ))
                                .children(disk)
                                .child(check(
                                    t("shell.machine.agentsLabel"),
                                    tv(
                                        "shell.machine.agentsValue",
                                        &[("running", running.into()), ("working", working.into())],
                                    ),
                                    ok,
                                ))
                            }),
                    )
                    .when_some(error.clone().filter(|_| !connected), |el, e| {
                        el.child(div().text_xs().text_color(theme::error()).child(e))
                    })
                    .when(kind == "memory", |el| {
                        el.child(
                            div()
                                .text_xs()
                                .text_color(theme::muted())
                                .child(t("shell.machine.memoryHint")),
                        )
                    })
            },
            cx,
        )
    }
}

/// limit_row is LimitRow: a window's share as a bar, and when it resets.
fn limit_row(w: &ClaudeLimitWindow, now: i64, bar: Hsla) -> AnyElement {
    let used = window_now(w, now);
    let pct = used.map_or(0., |u| u.clamp(0., 1.) * 100.);
    let tn = tone(used);
    let left = if w.name == "five_hour" {
        window_left(&w.resets_at, now)
    } else {
        None
    }
    .unwrap_or_else(|| time_until(&w.resets_at, now));
    v_flex()
        .gap_1()
        .child(
            h_flex()
                .justify_between()
                .gap_3()
                .text_xs()
                .child(div().text_color(theme::muted()).child(w.label.clone()))
                .child(match used {
                    None => div()
                        .font_family(theme::mono())
                        .text_color(theme::faint())
                        .child(tv(
                            "shell.top.resetAgo",
                            &[("ago", time_ago(&w.resets_at).into())],
                        )),
                    Some(_) => h_flex()
                        .font_family(theme::mono())
                        .child(
                            div()
                                .font_weight(FontWeight::SEMIBOLD)
                                .text_color(tone_color(tn, theme::primary()))
                                .child(tv(
                                    "shell.top.percentUsed",
                                    &[("percent", (pct.round() as i64).into())],
                                )),
                        )
                        .child(div().text_color(theme::faint()).child(format!(
                            " · {}",
                            tv("shell.top.resetsIn", &[("left", left.into())])
                        ))),
                }),
        )
        .child(
            div()
                .h(px(6.))
                .w_full()
                .rounded_full()
                .bg(theme::surface_raised())
                .overflow_hidden()
                .when(used.is_some(), |el| {
                    el.child(
                        div()
                            .h_full()
                            .w(relative((pct.max(2.) / 100.) as f32))
                            .rounded_full()
                            .bg(tone_color(tn, bar)),
                    )
                }),
        )
        .into_any_element()
}

/// other_account is one more account's latest reading, on a line.
fn other_account(l: &ClaudeLimit, now: i64) -> AnyElement {
    let figures: Vec<String> = l
        .windows
        .iter()
        .filter(|w| w.name == "five_hour" || w.name == "seven_day")
        .map(|w| {
            let label = if w.name == "five_hour" {
                t("shell.top.fiveHourShort")
            } else {
                t("shell.top.weekShort")
            };
            format!("{label} {}", percent(window_now(w, now)))
        })
        .collect();
    let name = if l.default {
        format!("{} ({})", l.account, t("common.default").to_lowercase())
    } else {
        l.account.clone()
    };
    h_flex()
        .justify_between()
        .gap_3()
        .min_w_0()
        .child(
            div()
                .min_w_0()
                .truncate()
                .text_color(theme::muted())
                .child(name),
        )
        .child(
            div()
                .flex_none()
                .font_family(theme::mono())
                .text_color(theme::faint())
                .child(figures.join(" · ")),
        )
        .into_any_element()
}

/// disk_row is DiskList's row: what's free, as a bar with the floor marked.
fn disk_row(d: &agentbox_api::DiskGuardDisk) -> AnyElement {
    let used = if d.total > 0 {
        (1. - d.free as f64 / d.total as f64).clamp(0., 1.)
    } else {
        0.
    };
    let floor_at = if d.total > 0 {
        (1. - d.floor as f64 / d.total as f64).clamp(0., 1.)
    } else {
        1.
    };
    let color = match d.level.as_str() {
        "full" => theme::error(),
        "low" => theme::starting(),
        _ => theme::accent(),
    };
    v_flex()
        .gap_1()
        .child(
            h_flex()
                .justify_between()
                .gap_3()
                .text_xs()
                .child(
                    div()
                        .min_w_0()
                        .truncate()
                        .text_color(theme::muted())
                        .child(d.label.clone()),
                )
                .child(
                    div()
                        .flex_none()
                        .font_family(theme::mono())
                        .text_color(if d.level == "ok" {
                            theme::muted()
                        } else {
                            color
                        })
                        .child(tv(
                            "shell.diskPill.free",
                            &[("size", human_bytes(d.free).into())],
                        )),
                ),
        )
        .child(
            div()
                .relative()
                .h(px(6.))
                .w_full()
                .rounded_full()
                .bg(theme::surface_raised())
                .child(
                    div()
                        .h_full()
                        .w(relative(used.max(0.02) as f32))
                        .rounded_full()
                        .bg(color),
                )
                .child(
                    div()
                        .absolute()
                        .top_0()
                        .bottom_0()
                        .left(relative(floor_at as f32))
                        .w(px(1.))
                        .bg(theme::error()),
                ),
        )
        .child(div().text_xs().text_color(theme::faint()).child(tv(
            "shell.diskPill.keeps",
            &[
                ("floor", human_bytes(d.floor).into()),
                ("total", human_bytes(d.total).into()),
            ],
        )))
        .into_any_element()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[::core::prelude::v1::test]
    fn windows_read_as_electron_reads_them() {
        let now = millis("2026-11-05T12:00:00Z").unwrap();
        let w = ClaudeLimitWindow {
            name: "five_hour".into(),
            utilization: 0.21,
            resets_at: "2026-11-05T14:58:00Z".into(),
            ..Default::default()
        };
        assert_eq!(window_now(&w, now), Some(0.21));
        assert_eq!(window_left(&w.resets_at, now).as_deref(), Some("2h 58m"));
        let past = ClaudeLimitWindow {
            resets_at: "2026-11-05T11:00:00Z".into(),
            ..w.clone()
        };
        assert_eq!(window_now(&past, now), None);
        assert_eq!(percent(Some(0.456)), "46%");
        assert_eq!(tone(Some(0.9)), 2);
        assert_eq!(tone(Some(0.7)), 1);
    }
}
