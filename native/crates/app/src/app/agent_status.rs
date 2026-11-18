//! How an agent reads, in words and in groups, as the Electron app reads it
//! (lib/agentStatus.ts chatLabel and rank, lib/leadFirst.ts agentSt,
//! lib/agentCounts.ts groupRows, lib/agentActions.ts): pure, so the rail, the
//! agent's header and its menu say the same thing.
use agentbox_api::{Agent, FleetAgent, MemoryHold};
use agentbox_i18n::{t, tv};

/// AgentLike is what these rules read of an agent: an Agent, or the fleet's
/// FleetAgent, which is an Agent and more.
pub trait AgentLike {
    fn state(&self) -> &str;
    fn chat(&self) -> &str;
    fn stalled(&self) -> bool;
    fn background(&self) -> bool;
    fn outcome(&self) -> &str;
    fn outcome_until(&self) -> Option<&str>;
    fn reclaimed(&self) -> &str;
    fn memory(&self) -> Option<&MemoryHold>;
    fn waiting_for_image(&self) -> bool;
    fn changes_requested(&self) -> bool;
}

macro_rules! agent_like {
    ($t:ty) => {
        impl AgentLike for $t {
            fn state(&self) -> &str {
                &self.state
            }
            fn chat(&self) -> &str {
                &self.chat
            }
            fn stalled(&self) -> bool {
                self.stalled_since.is_some()
            }
            fn background(&self) -> bool {
                !self.background.is_empty()
            }
            fn outcome(&self) -> &str {
                &self.outcome
            }
            fn outcome_until(&self) -> Option<&str> {
                self.outcome_until.as_deref()
            }
            fn reclaimed(&self) -> &str {
                &self.reclaimed
            }
            fn memory(&self) -> Option<&MemoryHold> {
                self.memory.as_ref()
            }
            fn waiting_for_image(&self) -> bool {
                self.waiting_for_image
            }
            fn changes_requested(&self) -> bool {
                !self.changes_requested.is_empty()
            }
        }
    };
}
agent_like!(Agent);
agent_like!(FleetAgent);

/// Tone is what a status line's colour says.
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Tone {
    Urgent,
    Error,
    Warning,
    Live,
    Muted,
}

/// machine_hold is the words for a machine paused or waiting for memory.
fn machine_hold(m: &MemoryHold) -> Option<String> {
    let place = (m.ahead + 1) as usize;
    match m.machine.as_str() {
        "paused" if m.swap_full => Some(tv(
            "agent.status.machinePausedSwapFull",
            &[("place", place.into())],
        )),
        "paused" => Some(tv("agent.status.machinePaused", &[("place", place.into())])),
        // Held for the user's computer, not the VM's memory: it says so.
        "waiting" if m.why == "host" => {
            Some(tv("agent.status.onHoldHost", &[("place", place.into())]))
        }
        "waiting" => Some(tv(
            "agent.status.machineWaiting",
            &[("place", place.into())],
        )),
        _ => None,
    }
}

/// memory_hold is the words for its tests or builds held back for memory, or
/// for the user's computer.
fn memory_hold(a: &impl AgentLike) -> Option<String> {
    let m = a.memory()?;
    if !m.machine.is_empty() {
        return machine_hold(m);
    }
    if a.state() != "running" {
        return None;
    }
    if m.paused > 0 {
        return Some(t(if m.swap_full {
            "agent.status.memoryPausedSwapFull"
        } else {
            "agent.status.memoryPaused"
        }));
    }
    if m.waiting > 0 && m.why == "host" {
        return Some(t("agent.status.hostWaiting"));
    }
    if m.waiting > 0 {
        return Some(t("agent.status.memoryWaiting"));
    }
    None
}

/// awaiting is a turn that ended on work it left running, whose end wakes it.
pub fn awaiting(a: &impl AgentLike) -> bool {
    a.state() == "running" && a.chat() == "ready" && a.background()
}

/// chat_label is an agent's state in a few words and their tone (chatLabel).
pub fn chat_label(a: &impl AgentLike) -> (String, Tone) {
    let (state, chat, outcome) = (a.state(), a.chat(), a.outcome());
    if state == "initializing" && a.waiting_for_image() {
        return (t("agent.status.waitingForImage"), Tone::Live);
    }
    if let Some(m) = a.memory().and_then(machine_hold) {
        return (m, Tone::Warning);
    }
    if state == "initializing" || state == "queued" {
        return (t("agent.status.initializing"), Tone::Live);
    }
    if chat == "waiting" {
        return (t("agent.status.needsYou"), Tone::Urgent);
    }
    if state == "incomplete" {
        return (t("agent.status.needsAttention"), Tone::Error);
    }
    if state == "missing" {
        return (t("agent.status.missing"), Tone::Error);
    }
    if chat == "running" && a.stalled() {
        return (t("agent.status.stalled"), Tone::Error);
    }
    if let Some(m) = memory_hold(a) {
        return (m, Tone::Warning);
    }
    if chat == "running" {
        return (t("agent.status.working"), Tone::Live);
    }
    if awaiting(a) {
        return (t("agent.status.awaiting"), Tone::Live);
    }
    if state == "running" && chat == "error" {
        return (t("agent.status.chatStopped"), Tone::Error);
    }
    if outcome == "resuming" && state != "paused" {
        return (t("agent.status.resuming"), Tone::Live);
    }
    if state == "running" && outcome == "limited" {
        let text = match a.outcome_until() {
            Some(until) => tv(
                "agent.status.limited",
                &[("time", (&clock_of(until)).into())],
            ),
            None => t("agent.status.limitedNoTime"),
        };
        return (text, Tone::Warning);
    }
    if state == "running" && chat != "starting" {
        match outcome {
            "asking" => return (t("agent.status.asking"), Tone::Urgent),
            "failed" => return (t("agent.status.failed"), Tone::Error),
            _ if a.reclaimed() == "frozen" => return (t("agent.status.reclaimed"), Tone::Muted),
            "done" => return (t("agent.status.done"), Tone::Muted),
            _ => {}
        }
    }
    if state == "running" {
        let key = if chat == "starting" {
            "agent.status.starting"
        } else {
            "agent.status.idle"
        };
        return (t(key), Tone::Muted);
    }
    if state == "paused" {
        return (t("agent.status.paused"), Tone::Muted);
    }
    if a.reclaimed() == "stopped" {
        return (t("agent.status.reclaimed"), Tone::Muted);
    }
    (t("agent.status.stopped"), Tone::Muted)
}

/// clock_of is an RFC 3339 time as the user's clock reads it: 16:11.
fn clock_of(time: &str) -> String {
    chrono::DateTime::parse_from_rfc3339(time)
        .map(|d| d.with_timezone(&chrono::Local).format("%H:%M").to_string())
        .unwrap_or_default()
}

/// St is where an agent stands for the user (leadFirst.ts AgentSt).
#[derive(Clone, Copy, PartialEq, Eq, Debug, Hash)]
pub enum St {
    Needs,
    Error,
    Review,
    Working,
    Done,
}

impl St {
    /// group is the list's group heading for it.
    pub fn group_label(self) -> String {
        t(match self {
            St::Needs => "panel.group.needs",
            St::Error => "panel.group.error",
            St::Review => "panel.group.review",
            St::Working => "panel.group.working",
            St::Done => "panel.group.idle",
        })
    }

    /// name is the state's name, for a tile's tooltip.
    pub fn name(self) -> String {
        t(match self {
            St::Needs => "v2.st.needs",
            St::Error => "v2.st.error",
            St::Review => "v2.st.review",
            St::Working => "v2.st.working",
            St::Done => "v2.st.done",
        })
    }

    /// ORDER is the groups' order: what needs you first.
    pub const ORDER: [St; 5] = [St::Needs, St::Error, St::Review, St::Working, St::Done];
}

/// agent_st reads an agent's St: asking (a question waits on you), review
/// (its work waits for a look), else from its chat_label's tone.
pub fn agent_st(a: &impl AgentLike, asking: bool, review: bool) -> St {
    let (_, tone) = chat_label(a);
    if tone == Tone::Urgent || asking {
        return St::Needs;
    }
    let chat = a.chat();
    if a.changes_requested() && matches!(chat, "running" | "starting" | "waiting") {
        return St::Working;
    }
    if a.outcome() == "limited" && chat != "running" {
        return St::Working;
    }
    if review {
        return St::Review;
    }
    if tone == Tone::Error {
        return St::Error;
    }
    if tone == Tone::Live || tone == Tone::Warning || chat == "starting" {
        return St::Working;
    }
    St::Done
}

/// rank orders the rail: what needs you, then what moves, then the paused
/// and the stopped (agentStatus.ts rank).
pub fn rank(a: &impl AgentLike) -> u8 {
    let (state, chat) = (a.state(), a.chat());
    if state == "initializing" || state == "queued" {
        return 1;
    }
    if chat == "waiting" || state == "incomplete" || state == "missing" {
        return 0;
    }
    if state == "running" && chat != "running" && a.outcome() == "asking" {
        return 0;
    }
    if state == "running" {
        return 1;
    }
    if state == "paused" {
        return 2;
    }
    3
}

/// Action is a lifecycle action an agent's state allows (agentActions.ts).
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Action {
    Pause,
    Resume,
    Start,
    Stop,
}

impl Action {
    /// path is its route's last segment: POST /v1/agents/<p>/<a>/<path>.
    pub fn path(self) -> &'static str {
        match self {
            Action::Pause => "pause",
            Action::Resume => "resume",
            Action::Start => "start",
            Action::Stop => "stop",
        }
    }

    pub fn label(self) -> String {
        t(match self {
            Action::Pause => "agent.action.pause",
            Action::Resume => "agent.action.resume",
            Action::Start => "common.start",
            Action::Stop => "common.stop",
        })
    }

    /// icon is its Lucide icon, for a menu.
    pub fn icon(self) -> &'static str {
        match self {
            Action::Pause => "pause",
            Action::Resume | Action::Start => "play",
            Action::Stop => "circle-stop",
        }
    }

    /// primary is the one that brings it back, the header's button.
    pub fn primary(self) -> bool {
        matches!(self, Action::Resume | Action::Start)
    }
}

pub fn lifecycle_actions(state: &str) -> Vec<Action> {
    let mut out = Vec::new();
    match state {
        "running" => out.push(Action::Pause),
        "paused" => out.push(Action::Resume),
        "stopped" => out.push(Action::Start),
        _ => {}
    }
    if state == "running" || state == "paused" {
        out.push(Action::Stop);
    }
    out
}

/// asleep is a machine stopped or paused, its disk kept (rightPanel machineOf).
pub fn asleep(state: &str) -> bool {
    state == "stopped" || state == "paused"
}

/// initials tell agents apart on a small tile (agentCounts.ts initials).
pub fn initials(title: &str, name: &str) -> String {
    let words: Vec<&str> = title
        .split(|c: char| !c.is_alphanumeric())
        .filter(|w| !w.is_empty())
        .collect();
    let first = |w: &str| w.chars().next().map(|c| c.to_uppercase().to_string());
    match words.as_slice() {
        [a, b, ..] => format!(
            "{}{}",
            first(a).unwrap_or_default(),
            first(b).unwrap_or_default()
        ),
        [a] => a.chars().take(2).collect::<String>().to_uppercase(),
        [] => {
            let digits: String = name
                .chars()
                .rev()
                .take_while(|c| c.is_ascii_digit())
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect();
            if digits.is_empty() {
                name.chars().take(2).collect::<String>().to_uppercase()
            } else {
                digits
                    .chars()
                    .rev()
                    .take(3)
                    .collect::<Vec<_>>()
                    .into_iter()
                    .rev()
                    .collect()
            }
        }
    }
}

/// number is an agent's number, "agent-12" → "12".
pub fn number(name: &str) -> Option<&str> {
    name.strip_prefix("agent-")
        .filter(|n| !n.is_empty() && n.chars().all(|c| c.is_ascii_digit()))
}

/// mem_short is memory in a few characters: 1.4G, 820M.
pub fn mem_short(n: i64) -> String {
    const G: f64 = 1024. * 1024. * 1024.;
    let n = n as f64;
    if n >= G {
        format!("{:.1}G", n / G)
    } else {
        format!("{}M", (n / (1024. * 1024.)).round())
    }
}

/// apply_chat_state takes where an agent's chat now stands, from a chat event,
/// into the project rail's copy of the agent: the chat state its row, tile and
/// label read (chat_label, agent_st), and whether the chat is working. It
/// answers whether anything changed. Without it the rail keeps what the last
/// fleet read said, "Needs you" included, after the request is answered.
pub fn apply_chat_state(fleet: &mut [FleetAgent], r#ref: &str, chat: &str) -> bool {
    let Some(a) = fleet.iter_mut().find(|a| a.r#ref == r#ref) else {
        return false;
    };
    let busy = chat == "running";
    if a.chat == chat && a.busy == busy {
        return false;
    }
    a.chat = chat.to_string();
    a.busy = busy;
    true
}

/// heavy is a machine using a lot: more than a core, or more than a quarter
/// of the host's memory (terminalTail.ts heavy).
pub fn heavy(cpu: Option<f64>, memory: Option<i64>, host: i64) -> bool {
    cpu.is_some_and(|c| c > 100.) || (host > 0 && memory.is_some_and(|m| m * 4 > host))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn agent(state: &str, chat: &str) -> Agent {
        Agent {
            r#ref: "p/agent-01".into(),
            name: "agent-01".into(),
            state: state.into(),
            chat: chat.into(),
            ..Default::default()
        }
    }

    #[test]
    fn a_hold_for_the_users_computer_says_so() {
        let mut builds = agent("running", "ready");
        builds.memory = Some(MemoryHold {
            waiting: 1,
            why: "host".into(),
            ..Default::default()
        });
        assert_eq!(
            memory_hold(&builds).as_deref(),
            Some("Waiting for your computer")
        );
        let mut boot = agent("stopped", "");
        boot.memory = Some(MemoryHold {
            machine: "waiting".into(),
            why: "host".into(),
            ..Default::default()
        });
        assert_eq!(
            memory_hold(&boot).as_deref(),
            Some("On hold: waiting for your computer, 1st in line")
        );
        boot.memory.as_mut().unwrap().why = "builds".into();
        assert_eq!(
            memory_hold(&boot).as_deref(),
            Some("Waiting for memory, 1st in line")
        );
    }

    #[test]
    fn states_read_as_the_electron_app_reads_them() {
        assert_eq!(chat_label(&agent("running", "running")).1, Tone::Live);
        assert_eq!(chat_label(&agent("running", "waiting")).1, Tone::Urgent);
        assert_eq!(chat_label(&agent("stopped", "")).1, Tone::Muted);
        assert_eq!(
            agent_st(&agent("running", "running"), false, false),
            St::Working
        );
        assert_eq!(agent_st(&agent("running", "ready"), true, false), St::Needs);
        assert_eq!(agent_st(&agent("stopped", ""), false, true), St::Review);
        assert_eq!(
            agent_st(&agent("running", "error"), false, false),
            St::Error
        );
        assert_eq!(agent_st(&agent("paused", ""), false, false), St::Done);
        assert!(rank(&agent("running", "waiting")) < rank(&agent("stopped", "")));
    }

    fn fleet_agent(r#ref: &str, chat: &str) -> FleetAgent {
        FleetAgent {
            r#ref: r#ref.into(),
            state: "running".into(),
            chat: chat.into(),
            ..Default::default()
        }
    }

    #[test]
    fn an_answered_request_stops_showing_needs_you() {
        let mut fleet = vec![
            fleet_agent("p/agent-01", "waiting"),
            fleet_agent("p/agent-02", "waiting"),
        ];
        assert_eq!(chat_label(&fleet[0]).1, Tone::Urgent);
        assert_eq!(agent_st(&fleet[0], false, false), St::Needs);

        // The request is answered: the chat event says it's working again.
        assert!(apply_chat_state(&mut fleet, "p/agent-01", "running"));
        assert_eq!(fleet[0].chat, "running");
        assert!(fleet[0].busy);
        assert_eq!(chat_label(&fleet[0]).1, Tone::Live);
        assert_eq!(agent_st(&fleet[0], false, false), St::Working);

        // Only that agent: the other still needs you.
        assert_eq!(fleet[1].chat, "waiting");
        assert!(!fleet[1].busy);
        assert_eq!(chat_label(&fleet[1]).1, Tone::Urgent);

        // And when its turn ends it's no longer busy, nor working.
        assert!(apply_chat_state(&mut fleet, "p/agent-01", "ready"));
        assert!(!fleet[0].busy);
        assert_ne!(agent_st(&fleet[0], false, false), St::Working);
    }

    #[test]
    fn a_chat_event_with_nothing_to_change_changes_nothing() {
        let mut fleet = vec![fleet_agent("p/agent-01", "running")];
        fleet[0].busy = true;
        // The same state again, as a streamed message's events say it.
        assert!(!apply_chat_state(&mut fleet, "p/agent-01", "running"));
        // An agent of another project, or the lead, isn't on this rail.
        assert!(!apply_chat_state(&mut fleet, "q/agent-01", "waiting"));
        assert!(!apply_chat_state(&mut fleet, "p/lead", "waiting"));
        assert_eq!(fleet[0].chat, "running");
        assert!(fleet[0].busy);
    }

    #[test]
    fn lifecycle_follows_the_state() {
        assert_eq!(
            lifecycle_actions("running"),
            vec![Action::Pause, Action::Stop]
        );
        assert_eq!(
            lifecycle_actions("paused"),
            vec![Action::Resume, Action::Stop]
        );
        assert_eq!(lifecycle_actions("stopped"), vec![Action::Start]);
        assert!(lifecycle_actions("missing").is_empty());
    }

    #[test]
    fn tiles_and_sizes() {
        assert_eq!(initials("Fix Windows relay", "agent-02"), "FW");
        assert_eq!(initials("Doctor", "agent-02"), "DO");
        assert_eq!(initials("", "agent-12"), "12");
        assert_eq!(number("agent-07"), Some("07"));
        assert_eq!(number("lead"), None);
        assert_eq!(mem_short(1536 * 1024 * 1024), "1.5G");
        assert_eq!(mem_short(820 * 1024 * 1024), "820M");
    }
}
