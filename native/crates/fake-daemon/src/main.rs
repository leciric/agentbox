//! A stand-in daemon for developing and measuring the native app where agents
//! can't run (no Incus). It serves a project's agents from fixtures, changes
//! their state now and then, and streams an agent's replies; with --upstream it
//! passes every other request, and the event stream, to a real daemon, so the
//! lead's chat is the real one.
//!
//!   fake-daemon --listen /t/fake.sock [--upstream ~/.local/share/agentbox/run/agentbox.sock]
//!               [--project demo] [--agents 6] [--history 400] [--rate 60]
//!               [--media ~/abx/fake-media] [--worktree <a checkout>]
//!
//! FAKE_RELEASE=<release.json> (an api::UpdateRelease) answers GET /v1/update/release
//! and makes GET /v1/update offer it, for testing the app's update in place;
//! FAKE_IDLE=1 leaves no job running, which the update would wait for.
use std::collections::HashMap;
use std::convert::Infallible;
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agentbox_api::*;
use anyhow::Result;
use bytes::Bytes;
use http_body_util::{BodyExt, Full, StreamBody, combinators::BoxBody};
use hyper::body::{Frame, Incoming};
use hyper::{Method, Request, Response, StatusCode};
use hyper_util::rt::TokioIo;
use tokio::net::{UnixListener, UnixStream};
use tokio::sync::broadcast;

mod agents;
mod chat;
mod desktop;
mod notify;
mod panel;
mod pulls;
mod resources;
mod review;
mod setup;

type Body = BoxBody<Bytes, Infallible>;

struct Opts {
    listen: PathBuf,
    upstream: Option<PathBuf>,
    project: String,
    agents: usize,
    history: usize,
    rate: u64,
    media: PathBuf,
    /// The checkout the agents' worktrees show (agents.rs).
    worktree: PathBuf,
    /// More projects, each with a few agents that only stand there (no
    /// chats): --neighbours storefront:4,quill:2, for the Office's campus.
    neighbours: Vec<(String, usize)>,
    /// Projects' icons: --icons demo=🦊,quill=logo.svg (an emoji, or a
    /// picture's file). The rest have none.
    icons: Vec<(String, String)>,
}

/// A chat: its last event's seq, its session and its items.
type Chat = (i64, ChatSession, Vec<ChatItem>);

struct Fake {
    opts: Opts,
    fleet: Mutex<Vec<FleetAgent>>,
    chats: Mutex<HashMap<String, Chat>>,
    events: broadcast::Sender<String>,
    /// Work, review and their proof (review.rs).
    review: Mutex<review::Review>,
    /// The agents' machines: files, snapshots, recordings (agents.rs).
    machines: Mutex<agents::Machines>,
    /// Line notes on the agents' diffs, by agent (reviewnotes.go).
    notes: Mutex<Vec<(String, ReviewNote)>>,
    /// The account the agents' chats run on (PUT …/chat/runner).
    account: Mutex<String>,
}

/// chat_options are the model and effort an agent's chat runs with.
fn chat_options() -> Vec<ChatOption> {
    let choice = |v: &str, n: &str| ChatOptionChoice {
        value: v.into(),
        name: n.into(),
        ..Default::default()
    };
    vec![
        ChatOption {
            id: "model".into(),
            name: "Model".into(),
            category: "model".into(),
            r#type: "select".into(),
            value: "opus".into(),
            choices: vec![choice("opus", "Opus 4.1"), choice("sonnet", "Sonnet 4.5")],
            ..Default::default()
        },
        ChatOption {
            id: "context".into(),
            name: "Context window".into(),
            category: "context_window".into(),
            r#type: "select".into(),
            value: "200k".into(),
            choices: vec![choice("200k", "200k"), choice("1m", "1M")],
            ..Default::default()
        },
        ChatOption {
            id: "effort".into(),
            name: "Effort".into(),
            category: "thought_level".into(),
            r#type: "select".into(),
            value: "high".into(),
            choices: vec![choice("medium", "Medium"), choice("high", "High")],
            ..Default::default()
        },
        ChatOption {
            id: "mode".into(),
            name: "Mode".into(),
            category: "mode".into(),
            r#type: "select".into(),
            value: "default".into(),
            choices: vec![
                choice("default", "Ask before edits"),
                choice("acceptEdits", "Accept edits"),
                choice("bypassPermissions", "Bypass permissions"),
            ],
            ..Default::default()
        },
        ChatOption {
            id: CHAT_OPTION_PLAN.into(),
            name: "Plan".into(),
            r#type: "boolean".into(),
            value: "false".into(),
            ..Default::default()
        },
        ChatOption {
            id: "output_style".into(),
            name: "Output style".into(),
            r#type: "select".into(),
            value: "default".into(),
            choices: vec![
                choice("default", "Default"),
                choice("explanatory", "Explanatory"),
                choice("learning", "Learning"),
            ],
            ..Default::default()
        },
    ]
}

/// runners are the AI tools and accounts a chat can run on (GET …/chat/runners).
fn runners(tool: &str, account: &str) -> LeadRunners {
    let acc = |name: &str| LeadAccount {
        name: name.into(),
        default: name == "work",
        ..Default::default()
    };
    let runner = |tool: &str, name: &str, accounts: Vec<LeadAccount>| LeadRunner {
        tool: tool.into(),
        name: name.into(),
        ready: true,
        installed: true,
        accounts,
        ..Default::default()
    };
    LeadRunners {
        tool: tool.into(),
        account: account.into(),
        runners: vec![
            runner("claude", "Claude Code", vec![acc("work"), acc("personal")]),
            runner("codex", "Codex", vec![]),
            runner("opencode", "OpenCode", vec![]),
        ],
        ..Default::default()
    }
}

/// clock is the fixtures' now: the time, or with FAKE_STILL one instant two
/// days before the fake started, so every "ago" and "for" reads the same in
/// two clients a minute apart (days change slower than screenshots are taken).
pub(crate) fn clock() -> std::time::SystemTime {
    static START: std::sync::OnceLock<std::time::SystemTime> = std::sync::OnceLock::new();
    if still() {
        *START.get_or_init(|| std::time::SystemTime::now() - Duration::from_secs(2 * 86400))
    } else {
        std::time::SystemTime::now()
    }
}

/// still says FAKE_STILL is set: fixtures that never change on their own.
fn still() -> bool {
    std::env::var_os("FAKE_STILL").is_some()
}

fn now() -> String {
    humantime::format_rfc3339_millis(crate::clock()).to_string()
}

const TITLES: &[&str] = &[
    "Native app (Rust + GPUI)",
    "Fix Windows relay reconnect",
    "Review modal redesign",
    "Doctor: don't flag gh before first chat",
    "Phone pairing on Mac (Lima)",
    "First run: VM in the background",
    "Token ledger per subagent",
    "Search palette: memories",
];

const DIAGRAM: &str = "```mermaid\nsequenceDiagram\n  participant App\n  participant Daemon\n  participant Claude\n  App->>Daemon: POST /chat/messages\n  Daemon->>Claude: prompt (ACP)\n  Claude-->>Daemon: chunks\n  Daemon-->>App: SSE chat events\n```";

const PREVIEW: &str = "```html preview\n<div style=\"padding:16px\"><b>Tokens this week</b><div style=\"display:flex;gap:8px;align-items:flex-end;height:150px;margin-top:12px\">\
<div style=\"flex:1;height:40%;background:var(--chart-1)\"></div><div style=\"flex:1;height:75%;background:var(--chart-2)\"></div>\
<div style=\"flex:1;height:55%;background:var(--chart-3)\"></div><div style=\"flex:1;height:90%;background:var(--chart-1)\"></div>\
<div style=\"flex:1;height:30%;background:var(--chart-4)\"></div></div><p style=\"color:var(--muted-foreground)\">Rendered in a webview, offline.</p></div>\n```";

const LOREM: &str = "The daemon owns the state, and every client is a thin one: it asks over the socket and \
shows what comes back. This reply is **streamed** a few words at a time, the way an agent's answer arrives, \
with `inline code`, lists and a block of code so the renderer has real work to do.";

/// working_items end a chat the way a turn in progress does: a plan, tool
/// calls (one with output), and a permission request waiting for an answer.
fn working_items() -> Vec<ChatItem> {
    let item = |id: &str, kind: &str| ChatItem {
        id: id.into(),
        turn: "tw".into(),
        kind: kind.into(),
        created_at: now(),
        updated_at: now(),
        ..Default::default()
    };
    let plan = |c: &str, s: &str| ChatPlanEntry {
        content: c.into(),
        status: s.into(),
    };
    vec![
        ChatItem {
            plan: vec![
                plan("Read internal/api's chat types", "completed"),
                plan("Render tool calls and permissions", "in_progress"),
                plan("Check it against the daemon", "pending"),
            ],
            ..item("w-plan", "plan")
        },
        ChatItem {
            tool: Some(ChatTool {
                call_id: "c1".into(),
                title: "go test ./internal/api".into(),
                kind: "execute".into(),
                status: "completed".into(),
                command: "go test ./internal/api".into(),
                output: "ok  \tagentbox/internal/api\t0.065s".into(),
                ..Default::default()
            }),
            ..item("w-tool1", "tool")
        },
        ChatItem {
            tool: Some(ChatTool {
                call_id: "c2".into(),
                title: "Edit native/crates/app/src/chat.rs".into(),
                kind: "edit".into(),
                status: "completed".into(),
                diffs: vec![ChatDiff {
                    path: "native/crates/app/src/chat.rs".into(),
                    old_text: "a\nb\n".into(),
                    new_text: "a\nb\nc\nd\n".into(),
                    ..Default::default()
                }],
                ..Default::default()
            }),
            ..item("w-tool2", "tool")
        },
        ChatItem {
            permission: Some(ChatPermission {
                call_id: "c3".into(),
                title: "Run `rm -rf target/`".into(),
                detail: "Claude Code asks to run a command".into(),
                options: ["allow_once", "allow_always", "reject_once"]
                    .iter()
                    .map(|k| ChatPermissionOption {
                        id: k.to_string(),
                        name: k.replace('_', " "),
                        kind: k.to_string(),
                        ..Default::default()
                    })
                    .collect(),
                ..Default::default()
            }),
            ..item("w-perm", "permission")
        },
    ]
}

fn history_item(agent: &str, i: usize) -> ChatItem {
    let kind = if i.is_multiple_of(2) {
        "user"
    } else {
        "assistant"
    };
    let text = if kind == "user" {
        format!(
            "Message {i}: can you look at `internal/api` and check the {} endpoint?",
            ["chat", "fleet", "events", "projects"][i % 4]
        )
    } else {
        format!(
            "Done with step {i}.\n\n- read `{agent}`'s worktree\n- ran `go test ./internal/api`\n- **{} passed**\n\n```go\nfunc (s *Server) fleet(w http.ResponseWriter, r *http.Request) error {{\n\treturn writeJSON(w, 200, s.list(r))\n}}\n```\n\n{LOREM}",
            i * 3 + 1
        )
    };
    let text = if i % 10 == 9 {
        format!("{text}\n\n{DIAGRAM}")
    } else {
        text
    };
    let text = if i % 10 == 7 {
        format!("{text}\n\n{PREVIEW}")
    } else {
        text
    };
    ChatItem {
        id: format!("h{i}"),
        turn: format!("t{}", i / 2),
        kind: kind.into(),
        text,
        created_at: now(),
        updated_at: now(),
        ..Default::default()
    }
}

impl Fake {
    fn new(opts: Opts) -> Arc<Self> {
        let fleet = (0..opts.agents)
            .map(|i| {
                let mut a = {
                    let name = format!("agent-{:02}", i + 1);
                    FleetAgent {
                        r#ref: format!("{}/{}", opts.project, name),
                        project: opts.project.clone(),
                        name: name.clone(),
                        title: TITLES[i % TITLES.len()].into(),
                        ai: "claude".into(),
                        branch: format!(
                            "agentbox/{}",
                            TITLES[i % TITLES.len()]
                                .to_lowercase()
                                .replace([' ', ':', '(', ')', '\''], "-")
                        ),
                        state: if i % 3 == 2 {
                            "stopped".into()
                        } else {
                            "running".into()
                        },
                        interface: "chat".into(),
                        busy: i % 3 == 0,
                        created_at: now(),
                        ..Default::default()
                    }
                };
                agents::fleet_extras(&mut a, &opts.worktree, i);
                a
            })
            .collect();
        let (events, _) = broadcast::channel(4096);
        let review = Mutex::new(review::Review::new(opts.media.clone()));
        let machines = Mutex::new(agents::Machines::new(opts.worktree.clone()));
        Arc::new(Self {
            machines,
            notes: Mutex::new(Vec::new()),
            review,
            opts,
            fleet: Mutex::new(fleet),
            chats: Mutex::new(HashMap::new()),
            events,
            account: Mutex::new("work".into()),
        })
    }

    fn emit<T: serde::Serialize>(&self, ty: &str, data: &T) {
        let ev = Event {
            r#type: ty.into(),
            time: now(),
            data: serde_json::to_value(data).unwrap(),
        };
        let _ = self.events.send(format!(
            "event: {ty}\ndata: {}\n\n",
            serde_json::to_string(&ev).unwrap()
        ));
    }

    /// fleet_with_pulls is the fleet, each agent with its pull request (the
    /// newest one its branch has, as the daemon's sessionPRs finds it).
    fn fleet_with_pulls(&self) -> Vec<FleetAgent> {
        let list = self.review.lock().unwrap().pulls().list(&self.opts.project);
        let mut fleet = self.fleet.lock().unwrap().clone();
        for a in &mut fleet {
            a.pr = list
                .pull_requests
                .iter()
                .find(|p| p.agent == a.name)
                .cloned();
        }
        fleet
    }

    /// neighbours are the other projects' agents (--neighbours): in every
    /// state a desk shows, and on every AI tool.
    fn neighbours(&self) -> Vec<FleetAgent> {
        const AIS: [&str; 5] = ["claude", "codex", "gemini", "opencode", "cursor"];
        let mut out = Vec::new();
        for (pi, (project, n)) in self.opts.neighbours.iter().enumerate() {
            for i in 0..*n {
                let k = i + pi * 3;
                let name = format!("agent-{}", i + 1);
                let (state, chat, outcome) = match k % 5 {
                    0 => ("running", "running", ""),
                    1 => ("running", "ready", "asking"),
                    2 => ("running", "ready", ""),
                    3 => ("stopped", "", ""),
                    _ => ("running", "running", ""),
                };
                out.push(FleetAgent {
                    r#ref: format!("{project}/{name}"),
                    project: project.clone(),
                    name,
                    title: TITLES[(k + 5) % TITLES.len()].into(),
                    ai: AIS[k % AIS.len()].into(),
                    branch: format!("agentbox/{project}-{}", i + 1),
                    state: state.into(),
                    chat: chat.into(),
                    outcome: outcome.into(),
                    interface: "chat".into(),
                    created_at: now(),
                    ..Default::default()
                });
            }
        }
        out
    }

    /// icon is a project's icon from --icons, when it has one.
    fn icon(&self, project: &str) -> Option<ProjectIcon> {
        use base64::Engine as _;
        let (_, icon) = self.opts.icons.iter().find(|(p, _)| p == project)?;
        let path = std::path::Path::new(icon);
        let mime = match path.extension().and_then(|e| e.to_str()) {
            Some("svg") => "image/svg+xml",
            Some("png") => "image/png",
            Some("jpg" | "jpeg") => "image/jpeg",
            Some("webp") => "image/webp",
            _ => "",
        };
        Some(match std::fs::read(path) {
            Ok(bytes) if !mime.is_empty() => ProjectIcon {
                source: "chosen".into(),
                image: format!(
                    "data:{mime};base64,{}",
                    base64::engine::general_purpose::STANDARD.encode(bytes)
                ),
                ..Default::default()
            },
            _ => ProjectIcon {
                source: "chosen".into(),
                emoji: icon.clone(),
                ..Default::default()
            },
        })
    }

    fn neighbour(&self, project: &str) -> bool {
        self.opts.neighbours.iter().any(|(p, _)| p == project)
    }

    fn is_fake(&self, project: &str) -> bool {
        project == self.opts.project
    }

    fn thread(&self, agent: &str) -> ChatThread {
        let mut chats = self.chats.lock().unwrap();
        let (seq, session, items) = chats.entry(agent.into()).or_insert_with(|| {
            let mut items =
                chat::history(agent, self.opts.history, |i| history_item(agent, i).text);
            // The turn in progress, headed by its message.
            items.push(ChatItem {
                id: "tw".into(),
                kind: "user".into(),
                text: "Now render the tool calls and the permission requests.".into(),
                created_at: humantime::format_rfc3339_millis(
                    crate::clock() - Duration::from_secs(40),
                )
                .to_string(),
                updated_at: now(),
                ..Default::default()
            });
            items.extend(working_items());
            (
                0,
                ChatSession {
                    state: CHAT_READY.into(),
                    tool: "claude".into(),
                    options: chat_options(),
                    context_used: 84_200,
                    context_size: 200_000,
                    ..Default::default()
                },
                items,
            )
        });
        ChatThread {
            agent: agent.into(),
            seq: *seq,
            session: session.clone(),
            items: items.clone(),
            ..Default::default()
        }
    }

    fn chat_event(
        &self,
        agent: &str,
        f: impl FnOnce(&mut ChatEvent, &mut ChatSession, &mut Vec<ChatItem>),
    ) {
        self.thread(agent);
        let ev = {
            let mut chats = self.chats.lock().unwrap();
            let (seq, session, items) = chats.get_mut(agent).unwrap();
            *seq += 1;
            let mut ev = ChatEvent {
                agent: agent.into(),
                seq: *seq,
                ..Default::default()
            };
            f(&mut ev, session, items);
            ev
        };
        self.emit(EVENT_CHAT, &ev);
    }

    /// reply streams an answer to a message, a few words per tick.
    async fn reply(self: Arc<Self>, agent: String, to: String) {
        let id = format!("r{}", rand_id());
        self.chat_event(&agent, |ev, s, _| {
            s.state = CHAT_RUNNING.into();
            ev.session = Some(s.clone());
        });
        self.set_busy(&agent, true);
        let item = ChatItem {
            id: id.clone(),
            turn: id.clone(),
            kind: "assistant".into(),
            text: String::new(),
            streaming: true,
            created_at: now(),
            updated_at: now(),
            ..Default::default()
        };
        self.chat_event(&agent, |ev, _, items| {
            items.push(item.clone());
            ev.item = Some(item);
        });
        let body = format!(
            "You said: *{to}*.\n\n{}\n\n{DIAGRAM}\n\n{}",
            [LOREM; 3].join("\n\n"),
            [LOREM; 3].join("\n\n")
        );
        let words: Vec<&str> = body.split_inclusive(' ').collect();
        let tick = Duration::from_millis(1000 / self.opts.rate.max(1));
        for chunk in words.chunks(2) {
            tokio::time::sleep(tick).await;
            let text: String = chunk.concat();
            self.chat_event(&agent, |ev, _, items| {
                if let Some(it) = items.iter_mut().find(|i| i.id == id) {
                    it.text.push_str(&text);
                }
                ev.append = Some(ChatAppend {
                    id: id.clone(),
                    text,
                });
            });
        }
        self.chat_event(&agent, |ev, s, items| {
            if let Some(it) = items.iter_mut().find(|i| i.id == id) {
                it.streaming = false;
                ev.item = Some(it.clone());
            }
            s.state = CHAT_READY.into();
            ev.session = Some(s.clone());
        });
        self.set_busy(&agent, false);
    }

    fn set_busy(&self, agent: &str, busy: bool) {
        let mut fleet = self.fleet.lock().unwrap();
        if let Some(a) = fleet.iter_mut().find(|a| a.r#ref == agent) {
            a.busy = busy;
        }
    }

    /// churn starts and stops agents now and then, so the app shows live state.
    async fn churn(self: Arc<Self>) {
        let mut n = 0usize;
        loop {
            tokio::time::sleep(Duration::from_secs(4)).await;
            n += 1;
            let change = {
                let mut fleet = self.fleet.lock().unwrap();
                if fleet.is_empty() {
                    continue;
                }
                let len = fleet.len();
                // The first two keep still, for showing their actions.
                if len < 3 {
                    continue;
                }
                let a = &mut fleet[2 + (n * 7) % (len - 2)];
                a.state = match a.state.as_str() {
                    "running" => "paused",
                    "paused" => "stopped",
                    _ => "running",
                }
                .into();
                AgentChange {
                    r#ref: a.r#ref.clone(),
                    state: a.state.clone(),
                    ..Default::default()
                }
            };
            self.emit(EVENT_AGENT, &change);
        }
    }
}

fn rand_id() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_nanos() as u64
}

/// fake_jobs are what the Jobs page lists.
fn fake_jobs() -> Vec<Job> {
    let at = |mins: u64| {
        humantime::format_rfc3339_seconds(crate::clock() - Duration::from_secs(mins * 60))
            .to_string()
    };
    vec![
        Job {
            id: "j-0006".into(),
            kind: "create".into(),
            target: "demo/agent-06".into(),
            // FAKE_IDLE: none running, which an update in place waits for.
            status: if std::env::var_os("FAKE_IDLE").is_some() {
                "succeeded".into()
            } else {
                "running".into()
            },
            created_at: at(1),
            ai: "claude".into(),
            ..Default::default()
        },
        Job {
            id: "j-0005".into(),
            kind: "snapshot".into(),
            target: "demo/agent-02".into(),
            status: "succeeded".into(),
            created_at: at(42),
            finished_at: Some(at(41)),
            ..Default::default()
        },
        Job {
            id: "j-0004".into(),
            kind: "create".into(),
            target: "demo/agent-03".into(),
            status: "failed".into(),
            error: "the base image is missing".into(),
            created_at: at(95),
            finished_at: Some(at(94)),
            ..Default::default()
        },
    ]
}

/// fake_tokens is a token report: what each chat spent, and its buckets.
fn fake_tokens(project: &str) -> TokenReport {
    let at = |ago: u64| {
        humantime::format_rfc3339_seconds(crate::clock() - Duration::from_secs(ago)).to_string()
    };
    let chats = [
        (
            "lead", "claude", "", 412_000, 18_500, 3_100_000, 240_000, 4.12,
        ),
        (
            "agent-01", "claude", TITLES[0], 286_000, 41_200, 2_400_000, 310_000, 3.38,
        ),
        (
            "agent-02", "codex", TITLES[1], 158_000, 22_900, 940_000, 0, 1.06,
        ),
    ];
    let agents: Vec<AgentTokens> = chats
        .iter()
        .enumerate()
        .map(
            |(i, (agent, ai, title, input, output, read, write, cost))| {
                let total = input + output + read + write;
                AgentTokens {
                    project: project.to_string(),
                    agent: agent.to_string(),
                    r#ref: format!("{project}/{agent}"),
                    ai: ai.to_string(),
                    title: title.to_string(),
                    exists: true,
                    turns: 40 - 12 * i as i64,
                    max_context: 182_000 - 40_000 * i as i64,
                    last_at: at(600 + 900 * i as u64),
                    input: *input,
                    output: *output,
                    cache_read: *read,
                    cache_write: *write,
                    total,
                    cost_usd: *cost,
                    models: vec![ModelTokens {
                        model: if *ai == "codex" {
                            "gpt-5.1-codex"
                        } else {
                            "claude-opus-4-1"
                        }
                        .into(),
                        input: *input,
                        output: *output,
                        cache_read: *read,
                        cache_write: *write,
                        total,
                        cost_usd: *cost,
                        ..Default::default()
                    }],
                    ..Default::default()
                }
            },
        )
        .collect();
    let sum = |f: fn(&AgentTokens) -> i64| agents.iter().map(f).sum::<i64>();
    let buckets = (0..10u64)
        .map(|b| {
            let w = [3, 5, 2, 8, 6, 9, 4, 7, 10, 5][b as usize];
            TokenBucket {
                start: at(1800 * (10 - b)),
                input: 8_000 * w,
                output: 900 * w,
                cache_read: 60_000 * w,
                cache_write: 4_000 * w,
                total: 72_900 * w,
                cost_usd: 0.09 * w as f64,
                ..Default::default()
            }
        })
        .collect();
    TokenReport {
        since: Some(at(5 * 3600)),
        until: at(0),
        input: sum(|a| a.input),
        output: sum(|a| a.output),
        cache_read: sum(|a| a.cache_read),
        cache_write: sum(|a| a.cache_write),
        total: sum(|a| a.total),
        cost_usd: agents.iter().map(|a| a.cost_usd).sum(),
        agents,
        buckets,
        bucket_seconds: 1800,
        ..Default::default()
    }
}

fn json<T: serde::Serialize>(status: StatusCode, v: &T) -> Response<Body> {
    Response::builder()
        .status(status)
        .header("content-type", "application/json")
        .body(Full::new(Bytes::from(serde_json::to_vec(v).unwrap())).boxed())
        .unwrap()
}

fn not_found(path: &str) -> Response<Body> {
    json(
        StatusCode::NOT_FOUND,
        &ApiError {
            error: format!("{path}: not in the fake daemon"),
            ..Default::default()
        },
    )
}

fn bad_request(error: String) -> Response<Body> {
    json(
        StatusCode::BAD_REQUEST,
        &ApiError {
            error,
            ..Default::default()
        },
    )
}

/// pulls_change is the pulls event the daemon sends when a project's list
/// changed (api.PullsChange).
fn pulls_change(project: &str) -> PullsChange {
    PullsChange {
        project: project.into(),
        github: "leciric/agentbox".into(),
        fetched_at: now(),
        ..Default::default()
    }
}

/// decode undoes a query value's percent-escapes.
fn decode(s: &str) -> String {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        if b[i] == b'%'
            && i + 2 < b.len()
            && let Ok(v) = u8::from_str_radix(&s[i + 1..i + 3], 16)
        {
            out.push(v);
            i += 3;
        } else {
            out.push(b[i]);
            i += 1;
        }
    }
    String::from_utf8_lossy(&out).into_owned()
}

async fn handle(fake: Arc<Fake>, req: Request<Incoming>) -> Result<Response<Body>, Infallible> {
    let path = req.uri().path().to_string();
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    let method = req.method().clone();
    let query: HashMap<String, String> = req
        .uri()
        .query()
        .unwrap_or("")
        .split('&')
        .filter_map(|kv| {
            kv.split_once('=')
                .map(|(k, v)| (k.to_string(), v.replace('+', " ")))
        })
        .collect();
    let fake_agent = |p: &str, a: &str| fake.is_fake(p) && review::Review::is_agent(a);
    let pick_up = upstream_pick_up(fake.clone());
    let req = match panel::route(&fake, req, &query, pick_up).await {
        Ok(res) => return Ok(res),
        Err(req) => *req,
    };
    let req = match notify::route(&fake, req, &query).await {
        Ok(res) => return Ok(res),
        Err(req) => *req,
    };
    let req = match setup::route(&fake, req, &query).await {
        Ok(res) => return Ok(res),
        Err(req) => *req,
    };
    let req = match agents::route(&fake, req, &query).await {
        Ok(res) => return Ok(res),
        Err(req) => *req,
    };
    let req = match resources::route(&fake, req).await {
        Ok(res) => return Ok(res),
        Err(req) => *req,
    };
    let res = match (&method, parts.as_slice()) {
        (&Method::GET, ["v1", "events"]) => events(fake.clone()).await,
        // On its own, it's the daemon too: up, and set up with defaults.
        (&Method::GET, ["v1", "version"]) if fake.opts.upstream.is_none() => {
            json(StatusCode::OK, &serde_json::json!({ "version": "fake", "api": 1 }))
        }
        // FAKE_APPEARANCE=light|dark|dracula picks the theme both apps wear.
        (&Method::GET, ["v1", "theme"]) if fake.opts.upstream.is_none() => {
            let appearance = std::env::var("FAKE_APPEARANCE").unwrap_or_else(|_| "dark".into());
            let mode = if appearance == "light" { "light" } else { "dark" };
            json(
                StatusCode::OK,
                &Theme { appearance, mode: mode.into(), ..Default::default() },
            )
        }
        (&Method::GET, ["v1", "settings"]) if fake.opts.upstream.is_none() => {
            // A fresh installation's, as the real daemon answered it
            // (settings.go's currentSettings), past its first run.
            let v: serde_json::Value =
                serde_json::from_str(include_str!("../fixtures/settings.json")).unwrap();
            json(StatusCode::OK, &v)
        }
        // Set up, as setup.go says when everything required is there, so
        // that Settings opens on its tabs rather than a loading error.
        (&Method::GET, ["v1", "setup"]) if fake.opts.upstream.is_none() => {
            let c = |id: &str, title: &str, status: &str, required: bool, detail: &str| SetupCheck {
                id: id.into(),
                title: title.into(),
                status: status.into(),
                required,
                detail: detail.into(),
                ..Default::default()
            };
            json(
                StatusCode::OK,
                &SetupStatus {
                    ready: true,
                    checks: vec![
                        c("incus", "Incus", "ok", true, "Incus 6.0"),
                        c("host", "User mapping", "ok", true, ""),
                        c("image", "Base image", "ok", true, "base-v42"),
                        c("storage", "Storage pool", "ok", false, "btrfs or zfs: agents are created with an instant snapshot"),
                        c("claude", "Claude Code login", "ok", false, ""),
                        c("codex", "Codex login", "optional", false, "optional: agents can't run Codex yet"),
                        c("opencode", "OpenCode login", "optional", false, ""),
                        c("cursor", "Cursor sign-in", "optional", false, ""),
                        c("gemini", "Gemini CLI sign-in", "optional", false, ""),
                        c("preview", "Preview URLs", "ok", false, ""),
                    ],
                    ..Default::default()
                },
            )
        }
        // What the project's chats spent (TokensPanel, Settings → Tokens):
        // the lead and two agents, in half-hour buckets up to the fixtures'
        // now.
        (&Method::GET, ["v1", "tokens"]) if fake.opts.upstream.is_none() => {
            json(StatusCode::OK, &fake_tokens(&fake.opts.project))
        }
        (&Method::GET, ["v1", "tokens", "turns"]) if fake.opts.upstream.is_none() => {
            json(StatusCode::OK, &Vec::<TokenTurn>::new())
        }
        // Signed in to Claude Code with one account, as the limits say; no
        // other tool yet (auth.go's AuthStatus).
        (&Method::GET, ["v1", "auth"]) if fake.opts.upstream.is_none() => json(
            StatusCode::OK,
            &AuthStatus {
                claude: true,
                claude_accounts: vec![ClaudeAccount {
                    name: "default".into(),
                    default: true,
                    saved_at: now(),
                    ..Default::default()
                }],
                ..Default::default()
            },
        ),
        (&Method::GET, ["v1", "projects"]) if fake.opts.upstream.is_none() => {
            let p = Project {
                name: fake.opts.project.clone(),
                display_name: fake.opts.project.clone(),
                ..Default::default()
            };
            let mut all = vec![p];
            for (name, _) in &fake.opts.neighbours {
                all.push(Project { name: name.clone(), display_name: name.clone(), ..Default::default() });
            }
            for p in &mut all {
                if fake.icon(&p.name).is_some() {
                    p.icon_version = 1;
                }
            }
            json(StatusCode::OK, &all)
        }
        (&Method::GET, ["v1", "projects", p, "icon"]) if fake.icon(p).is_some() => {
            json(StatusCode::OK, &fake.icon(p).unwrap())
        }
        // An open question from an agent, for the sidebar's "needs you".
        (&Method::GET, ["v1", "projects", p, "questions"]) if fake.is_fake(p) => json(
            StatusCode::OK,
            &vec![Question {
                id: "q1".into(),
                project: p.to_string(),
                agent: "agent-03".into(),
                r#ref: format!("{p}/agent-03"),
                question: "Should the reminders page paginate?".into(),
                status: "open".into(),
                created_at: now(),
                ..Default::default()
            }],
        ),
        // A release to update the app to (FAKE_RELEASE, a JSON
        // api::UpdateRelease whose files a local server serves), for
        // testing the app's update in place; the daemon's update check
        // offers it.
        (&Method::GET, ["v1", "update", "release"]) if fake_release().is_some() => {
            json(StatusCode::OK, &fake_release().unwrap())
        }
        (&Method::GET, ["v1", "update"]) if fake_release().is_some() => {
            let r = fake_release().unwrap();
            json(
                StatusCode::OK,
                &UpdateStatus {
                    current: std::env::var("FAKE_CURRENT").unwrap_or_else(|_| "0.17.0".into()),
                    enabled: true,
                    channel: "stable".into(),
                    available: Some(UpdateAvailable {
                        version: r.version,
                        url: r.url,
                    }),
                    checked_at: Some(now()),
                    ..Default::default()
                },
            )
        }
        // The base image's state, which Settings → Setup shows: the real
        // daemon asks Incus for its snapshot, and has none here.
        (&Method::GET, ["v1", "image"]) => json(
            StatusCode::OK,
            &ImageStatus {
                ready: true,
                snapshot: "base-v42".into(),
                build: "idle".into(),
                ..Default::default()
            },
        ),
        // The stopped agents Settings → Agents would remove (agent.go's
        // StoppedMachines): the real daemon needs Incus to say. The first
        // two stopped agents lose nothing; the rest are kept.
        (&Method::GET, ["v1", "stopped-machines"]) => {
            let fleet = fake.fleet.lock().unwrap().clone();
            let machines = fleet
                .iter()
                .filter(|a| a.state == "stopped")
                .enumerate()
                .map(|(i, a)| StoppedMachine {
                    r#ref: a.r#ref.clone(),
                    project: a.project.clone(),
                    name: a.name.clone(),
                    title: a.title.clone(),
                    branch: a.branch.clone(),
                    stopped_since: a.created_at.clone(),
                    machine: Some((i as i64 + 1) * 1_300_000_000),
                    remove: if i < 2 {
                        "nothing lost".into()
                    } else {
                        String::new()
                    },
                    keep: if i < 2 {
                        String::new()
                    } else {
                        "stopped recently".into()
                    },
                })
                .collect();
            json(StatusCode::OK, &StoppedMachines { days: 7, machines })
        }
        (&Method::POST, ["v1", "stopped-machines", "remove"]) => {
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let asked: RemoveStoppedRequest = serde_json::from_slice(&body).unwrap_or_default();
            json(
                StatusCode::OK,
                &RemoveStoppedResult {
                    removed: asked.agents,
                    kept: Vec::new(),
                },
            )
        }
        // Work, review and proof (review.rs): the real daemon has no agents
        // to have done any. Every project's is the fake project's.
        (&Method::GET, ["v1", "work"]) => {
            let p = query
                .get("project")
                .cloned()
                .unwrap_or(fake.opts.project.clone());
            json(
                StatusCode::OK,
                &fake.review.lock().unwrap().work(&p, &query),
            )
        }
        (&Method::GET, ["v1", "projects", p, "work"]) if fake.is_fake(p) => {
            json(StatusCode::OK, &fake.review.lock().unwrap().work(p, &query))
        }
        (&Method::GET, ["v1", "review"]) => json(
            StatusCode::OK,
            &fake
                .review
                .lock()
                .unwrap()
                .review(&fake.opts.project, query.contains_key("reviewed")),
        ),
        (&Method::GET, ["v1", "projects", p, "review"]) if fake.is_fake(p) => json(
            StatusCode::OK,
            &fake
                .review
                .lock()
                .unwrap()
                .review(p, query.contains_key("reviewed")),
        ),
        (&Method::GET, ["v1", "agents", p, a, "media"]) if fake_agent(p, a) => {
            json(StatusCode::OK, &fake.review.lock().unwrap().media(p, a))
        }
        // A past agent's media is the project's, under its name.
        (&Method::GET, ["v1", "projects", p, "media"])
            if query.get("agent").is_some_and(|a| fake_agent(p, a)) =>
        {
            let a = &query["agent"];
            json(StatusCode::OK, &fake.review.lock().unwrap().media(p, a))
        }
        // Every project's media (the Media page), a page at a time.
        (&Method::GET, ["v1", "media"]) if query.contains_key("limit") => {
            let all = fake.review.lock().unwrap().all_media(&fake.opts.project);
            let items = all
                .into_iter()
                .filter(|m| media_in(m, &query, true))
                .filter(|m| !query.contains_key("favorite") || m.favorite)
                .filter(|m| !query.contains_key("unseen") || m.unseen)
                .collect();
            json(StatusCode::OK, &MediaPage { items, next: String::new() })
        }
        // What the Media page's filters count (media_list.go mediaCounts):
        // the scope's items, by kind, agent and project, and those the
        // kind and search keep.
        (&Method::GET, ["v1", "media", "counts"]) => {
            let all = fake.review.lock().unwrap().all_media(&fake.opts.project);
            let mut c = MediaCounts::default();
            for m in all.iter().filter(|m| media_in(m, &query, false)) {
                c.total += 1;
                c.bytes += m.size;
                *c.kinds.entry(m.kind.clone()).or_default() += 1;
                c.favorites += m.favorite as i64;
                c.unseen += m.unseen as i64;
                if media_in(m, &query, true) {
                    c.matching += 1;
                    c.matching_bytes += m.size;
                }
                match c.agents.iter_mut().find(|a| a.agent == m.agent) {
                    Some(a) => {
                        a.count += 1;
                        a.bytes += m.size;
                    }
                    None => c.agents.push(MediaAgentCount {
                        agent: m.agent.clone(),
                        name: m.agent_name.clone(),
                        title: m.agent_title.clone(),
                        gone: m.agent_gone,
                        count: 1,
                        bytes: m.size,
                    }),
                }
            }
            if c.total > 0 {
                c.projects.push(MediaProjectCount {
                    project: fake.opts.project.clone(),
                    count: c.total,
                });
            }
            json(StatusCode::OK, &c)
        }
        // The jobs the fake agents' creation and a snapshot ran (the Jobs page).
        (&Method::GET, ["v1", "jobs"]) => json(StatusCode::OK, &fake_jobs()),
        (&Method::GET, ["v1", "jobs", id, "log"]) => Response::builder()
            .header("content-type", "text/plain; charset=utf-8")
            .body(Full::new(Bytes::from(format!(
                "job {id}\n→ creating the machine\n→ cloning the worktree on its branch\n→ starting Claude Code\n✓ done\n"
            ))).boxed())
            .unwrap(),
        (&Method::GET, ["v1", "media", id, "file"])
            if fake.review.lock().unwrap().file(id).is_some() =>
        {
            let (bytes, mime) = fake.review.lock().unwrap().file(id).unwrap();
            Response::builder()
                .header("content-type", mime)
                .header("cache-control", "private, max-age=31536000, immutable")
                .body(Full::new(Bytes::from(bytes)).boxed())
                .unwrap()
        }
        (&Method::GET, ["v1", "agents" | "archived-agents", p, a, "diff"]) if fake_agent(p, a) => {
            Response::builder()
                .header("content-type", "text/plain; charset=utf-8")
                .body(Full::new(Bytes::from(fake.review.lock().unwrap().diff(a))).boxed())
                .unwrap()
        }
        (&Method::GET, ["v1", "agents", p, a, "review-notes"]) if fake_agent(p, a) => {
            let notes: Vec<ReviewNote> = fake
                .notes
                .lock()
                .unwrap()
                .iter()
                .filter(|(who, _)| who == a)
                .map(|(_, n)| n.clone())
                .collect();
            json(StatusCode::OK, &notes)
        }
        (&Method::POST, ["v1", "agents", p, a, "review-notes"]) if fake_agent(p, a) => {
            let a = a.to_string();
            let body = req.into_body().collect().await.map(|b| b.to_bytes()).unwrap_or_default();
            let r: AddReviewNoteRequest = serde_json::from_slice(&body).unwrap_or_default();
            let mut notes = fake.notes.lock().unwrap();
            let note = ReviewNote {
                id: notes.len() as i64 + 1,
                path: r.path,
                side: r.side,
                start: r.start,
                end: r.end,
                code: r.code,
                body: r.body,
                status: "draft".into(),
                created_at: "2026-11-05T12:00:00Z".into(),
                ..Default::default()
            };
            notes.push((a, note.clone()));
            json(StatusCode::OK, &note)
        }
        (&Method::PATCH, ["v1", "agents", p, a, "review-notes", id]) if fake_agent(p, a) => {
            let (a, id) = (a.to_string(), id.parse::<i64>().unwrap_or(0));
            let body = req.into_body().collect().await.map(|b| b.to_bytes()).unwrap_or_default();
            let r: EditReviewNoteRequest = serde_json::from_slice(&body).unwrap_or_default();
            let mut notes = fake.notes.lock().unwrap();
            match notes.iter_mut().find(|(who, n)| *who == a && n.id == id) {
                Some((_, n)) => {
                    if let Some(b) = r.body {
                        n.body = b;
                    }
                    if let Some(on) = r.resolved {
                        n.status = if on { "resolved" } else { "pending" }.into();
                        n.resolved_at = on.then(|| "2026-11-05T12:00:00Z".into());
                    }
                    json(StatusCode::OK, &n.clone())
                }
                None => json(StatusCode::NOT_FOUND, &serde_json::json!({"error": "no such note"})),
            }
        }
        (&Method::DELETE, ["v1", "agents", p, a, "review-notes", id]) if fake_agent(p, a) => {
            let (a, id) = (a.to_string(), id.parse::<i64>().unwrap_or(0));
            fake.notes.lock().unwrap().retain(|(who, n)| !(*who == a && n.id == id));
            json(StatusCode::OK, &serde_json::json!({}))
        }
        (&Method::GET, ["v1", "skills"]) | (&Method::GET, ["v1", "projects", _, "skills"]) => {
            let skill = |name: &str, description: &str| Skill {
                name: name.into(),
                description: description.into(),
                enabled: true,
                active: Some(true),
                user_invocable: true,
                ..Default::default()
            };
            json(
                StatusCode::OK,
                &vec![
                    skill("review-pr", "Review a pull request and leave comments"),
                    skill("write-tests", "Write the missing tests for a change"),
                    skill("release-notes", "Draft the release notes from merged PRs"),
                ],
            )
        }
        (&Method::GET, ["v1", "projects", p, "artifacts"]) if fake.is_fake(p) => json(
            StatusCode::OK,
            &Artifacts {
                connector: "hatch".into(),
                artifacts: vec![Artifact {
                    id: "O4CQYsCRRC1WKQ16".into(),
                    title: "Settings: before and after".into(),
                    url: "https://usehatch.dev/p/O4CQYsCRRC1WKQ16".into(),
                    agent: format!("{p}/agent-02"),
                    agents: vec![format!("{p}/agent-02")],
                    version: 1,
                    permanent: true,
                    created_at: "2026-11-05T10:00:00Z".into(),
                    updated_at: "2026-11-05T10:30:00Z".into(),
                    ..Default::default()
                }],
            },
        ),
        // A page's HTML as artifactPage serves it: framed under the
        // daemon's policy (artifacts.go defaultPagePolicy).
        (&Method::GET, ["v1", "projects", p, "artifacts", _, "page"]) if fake.is_fake(p) => {
            Response::builder()
                .status(StatusCode::OK)
                .header("content-type", "text/html; charset=utf-8")
                .header(
                    "content-security-policy",
                    "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; sandbox allow-scripts allow-popups",
                )
                .header("cache-control", "no-store")
                .body(Full::new(Bytes::from_static(FAKE_PAGE.as_bytes())).boxed())
                .unwrap()
        }
        (&Method::POST, ["v1", "agents", p, a, "review" | "request-changes"])
            if fake_agent(p, a) =>
        {
            let changes = parts[4] == "request-changes";
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let verdict = if changes {
                "changes".to_string()
            } else {
                serde_json::from_slice::<ReviewRequest>(&body)
                    .unwrap_or_default()
                    .verdict
            };
            fake.review
                .lock()
                .unwrap()
                .verdict(&format!("{p}/{a}"), &verdict);
            fake.emit(EVENT_REVIEW, &p.to_string());
            if changes {
                // A TellResult: the message, as it went into the agent's chat.
                json(
                    StatusCode::ACCEPTED,
                    &ChatItem {
                        id: format!("u{}", rand_id()),
                        kind: "user".into(),
                        created_at: now(),
                        ..Default::default()
                    },
                )
            } else {
                Response::builder()
                    .status(StatusCode::NO_CONTENT)
                    .body(Full::new(Bytes::new()).boxed())
                    .unwrap()
            }
        }
        // Pull requests (pulls.rs): the real daemon here has no GitHub.
        (&Method::GET, ["v1", "projects", p, "pulls"]) if fake.is_fake(p) => {
            json(StatusCode::OK, &fake.review.lock().unwrap().pulls().list(p))
        }
        (&Method::GET, ["v1", "projects", p, "labels"]) if fake.is_fake(p) => json(
            StatusCode::OK,
            &fake.review.lock().unwrap().pulls().all_labels(),
        ),
        (&Method::GET, ["v1", "projects", p, "pulls", "image"]) if fake.is_fake(p) => {
            match fake.review.lock().unwrap().file("x-settings-after") {
                Some((bytes, mime)) => Response::builder()
                    .header("content-type", mime)
                    .body(Full::new(Bytes::from(bytes)).boxed())
                    .unwrap(),
                None => not_found(&path),
            }
        }
        (&Method::GET, ["v1", "projects", p, "pulls", n, rest @ ..])
            if fake.is_fake(p) && n.parse::<i64>().is_ok() =>
        {
            let n: i64 = n.parse().unwrap();
            let review = fake.review.lock().unwrap();
            let pulls = review.pulls();
            let found = match rest {
                [] => pulls.detail(n).map(|d| json(StatusCode::OK, &d)),
                ["files"] => pulls.files_of(n).map(|f| json(StatusCode::OK, &f)),
                ["diff"] => {
                    let path = decode(query.get("path").map(String::as_str).unwrap_or(""));
                    pulls.diff(n, &path).map(|d| json(StatusCode::OK, &d))
                }
                _ => None,
            };
            found.unwrap_or_else(|| not_found(&path))
        }
        (&Method::POST, ["v1", "projects", p, "pulls", n, "merge"]) if fake.is_fake(p) => {
            let n: i64 = n.parse().unwrap_or(0);
            let res = fake.review.lock().unwrap().pulls_mut().merge(n);
            match res {
                Ok(pr) => {
                    fake.emit(EVENT_REVIEW, &p.to_string());
                    fake.emit(EVENT_PULLS, &pulls_change(p));
                    json(StatusCode::OK, &pr)
                }
                Err(e) => bad_request(e),
            }
        }
        (&Method::POST, ["v1", "projects", p, "pulls", n, "labels"]) if fake.is_fake(p) => {
            let n: i64 = n.parse().unwrap_or(0);
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let edit = serde_json::from_slice::<EditLabelsRequest>(&body).unwrap_or_default();
            let res = fake
                .review
                .lock()
                .unwrap()
                .pulls_mut()
                .edit_labels(n, &edit);
            match res {
                Some(labels) => {
                    fake.emit(EVENT_PULLS, &pulls_change(p));
                    json(StatusCode::OK, &labels)
                }
                None => not_found(&path),
            }
        }
        (&Method::POST, ["v1", "projects", p, "pulls", n, "merge-when-green"])
            if fake.is_fake(p) =>
        {
            let n: i64 = n.parse().unwrap_or(0);
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let mark = serde_json::from_slice::<MergeWhenGreenRequest>(&body).unwrap_or_default();
            let res = {
                let mut review = fake.review.lock().unwrap();
                let res = review.pulls_mut().mark(n, &mark.method);
                if res.is_ok() && !mark.agent.is_empty() {
                    review.verdict(&format!("{p}/{}", mark.agent), "reviewed");
                }
                res
            };
            match res {
                Ok(m) => {
                    fake.emit(EVENT_REVIEW, &p.to_string());
                    json(StatusCode::OK, &m)
                }
                Err(e) => bad_request(e),
            }
        }
        (&Method::DELETE, ["v1", "projects", p, "pulls", n, "merge-when-green"])
            if fake.is_fake(p) =>
        {
            let n: i64 = n.parse().unwrap_or(0);
            fake.review.lock().unwrap().pulls_mut().unmark(n);
            fake.emit(EVENT_REVIEW, &p.to_string());
            Response::builder()
                .status(StatusCode::NO_CONTENT)
                .body(Full::new(Bytes::new()).boxed())
                .unwrap()
        }
        // Every agent, as GET /v1/agents lists them (a FleetAgent is an Agent and more).
        (&Method::GET, ["v1", "agents"]) => {
            let mut all = fake.fleet_with_pulls();
            all.extend(fake.neighbours());
            json(StatusCode::OK, &all)
        }
        (&Method::GET, ["v1", "projects", p, "fleet"]) if fake.neighbour(p) => {
            let agents = fake.neighbours().into_iter().filter(|a| a.project == *p).collect();
            json(StatusCode::OK, &Fleet { project: p.to_string(), agents, ..Default::default() })
        }
        (&Method::GET, ["v1", "projects", p, "fleet"]) if fake.is_fake(p) => {
            let fleet = Fleet {
                project: p.to_string(),
                agents: fake.fleet_with_pulls(),
                ..Default::default()
            };
            json(StatusCode::OK, &fleet)
        }
        // An agent's terminal: a shell on this machine, in place of the
        // agent's tmux session (the same protocol as internal/daemon/terminal.go).
        (&Method::GET, ["v1", "agents", p, _a, "terminal"]) if fake.is_fake(p) => terminal(req),
        // An agent's desktop: this machine's own VNC server (desktop.rs), as
        // raw RFB in WebSocket frames like internal/daemon/browser.go's vncView.
        (&Method::GET, ["v1", "agents", p, a, "browser", "view"]) if fake.is_fake(p) => {
            vnc_view(req, &format!("{p}/{a}")).await
        }
        // On its own, each lead has one session, main, ready.
        (&Method::GET, ["v1", "projects", p, "sessions"]) if fake.opts.upstream.is_none() => json(
            StatusCode::OK,
            &vec![LeadSession {
                project: p.to_string(),
                id: "main".into(),
                r#ref: format!("{p}/lead"),
                topic: "Main".into(),
                chat: CHAT_READY.into(),
                status: "idle".into(),
                created_at: now(),
                active_at: now(),
                ..Default::default()
            }],
        ),
        (&Method::GET, ["v1", "projects", p, "lead"] | ["v1", "projects", p, "sessions", _, "lead"])
            if fake.opts.upstream.is_none() =>
        {
            json(
                StatusCode::OK,
                &ProjectChat {
                    project: p.to_string(),
                    session: "main".into(),
                    r#ref: format!("{p}/lead"),
                    started: true,
                    chat: CHAT_READY.into(),
                    ..Default::default()
                },
            )
        }
        // On its own, the leads' chats too: Home's empty, a project's a
        // short conversation (the same in every client that asks).
        (&Method::GET, ["v1", "projects", p, "chat"] | ["v1", "projects", p, "sessions", _, "chat"])
            if fake.opts.upstream.is_none() =>
        {
            let items = if *p == HOME_PROJECT {
                Vec::new()
            } else {
                (0..4).map(|i| history_item("lead", i)).collect()
            };
            json(
                StatusCode::OK,
                &ChatThread {
                    agent: format!("{p}/lead"),
                    session: ChatSession {
                        state: CHAT_READY.into(),
                        tool: "claude".into(),
                        options: chat_options(),
                        ..Default::default()
                    },
                    items,
                    ..Default::default()
                },
            )
        }
        (&Method::GET, ["v1", "agents", p, a, "chat"]) if fake.is_fake(p) => {
            // A page, as internal/daemon/chat.go reads one: limit= items
            // ending before= an item (the latest page without it).
            let query: HashMap<String, String> = req
                .uri()
                .query()
                .unwrap_or("")
                .split('&')
                .filter_map(|kv| {
                    kv.split_once('=')
                        .map(|(k, v)| (k.to_string(), v.to_string()))
                })
                .collect();
            let mut t = fake.thread(&format!("{p}/{a}"));
            let n = t.items.len();
            let limit = query
                .get("limit")
                .and_then(|l| l.parse().ok())
                .filter(|&l: &usize| l > 0);
            // around= is limit items each side of one.
            let (start, end) = match query
                .get("around")
                .and_then(|id| t.items.iter().position(|i| &i.id == id))
            {
                Some(at) => {
                    let side = limit.unwrap_or(50);
                    (at.saturating_sub(side), (at + side + 1).min(n))
                }
                None => {
                    let end = query
                        .get("before")
                        .and_then(|b| t.items.iter().position(|i| &i.id == b))
                        .unwrap_or(n);
                    (end.saturating_sub(limit.unwrap_or(end)), end)
                }
            };
            t.older = start > 0;
            t.newer = end < n;
            t.items = t.items[start..end].to_vec();
            json(StatusCode::OK, &t)
        }
        (&Method::GET, ["v1", "agents", p, a, "chat", "search"]) if fake.is_fake(p) => {
            let t = fake.thread(&format!("{p}/{a}"));
            let q = decode(query.get("q").map(String::as_str).unwrap_or(""));
            json(StatusCode::OK, &chat::search(&t.items, &q))
        }
        (&Method::GET, ["v1", "agents", p, _a, "chat", "images", _id]) if fake.is_fake(p) => {
            match std::fs::read(fake.opts.media.join("settings-after.png")) {
                Ok(b) => Response::builder()
                    .header("content-type", "image/png")
                    .body(Full::new(Bytes::from(b)).boxed())
                    .unwrap(),
                Err(_) => not_found(&path),
            }
        }
        (&Method::GET, ["v1", "agents", p, _a, "chat", "files", _id]) if fake.is_fake(p) => {
            Response::builder()
                .header("content-type", "text/plain")
                .body(
                    Full::new(Bytes::from(
                        "go test ./internal/api\nok  \tagentbox/internal/api\t0.065s\n",
                    ))
                    .boxed(),
                )
                .unwrap()
        }
        // Answering a permission request records the outcome, as the daemon does.
        (&Method::POST, ["v1", "agents", p, a, "chat", "permissions", item]) if fake.is_fake(p) => {
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let answer: ChatAnswerRequest = serde_json::from_slice(&body).unwrap_or_default();
            let item = item.to_string();
            fake.chat_event(&format!("{p}/{a}"), |ev, _, items| {
                if let Some(it) = items.iter_mut().find(|i| i.id == item)
                    && let Some(perm) = it.permission.as_mut()
                {
                    perm.outcome = answer.option_id.clone();
                    ev.item = Some(it.clone());
                }
            });
            Response::builder()
                .status(StatusCode::NO_CONTENT)
                .body(Full::new(Bytes::new()).boxed())
                .unwrap()
        }
        (&Method::GET, ["v1", "agents", p, a, "chat", "runners"]) if fake.is_fake(p) => {
            let t = fake.thread(&format!("{p}/{a}"));
            let account = fake.account.lock().unwrap().clone();
            json(StatusCode::OK, &runners(&t.session.tool, &account))
        }
        (&Method::PUT, ["v1", "agents", p, a, "chat", "runner"]) if fake.is_fake(p) => {
            let agent = format!("{p}/{a}");
            let body = req.into_body().collect().await.map(|b| b.to_bytes()).unwrap_or_default();
            let r: LeadRunnerRequest = serde_json::from_slice(&body).unwrap_or_default();
            *fake.account.lock().unwrap() = r.account;
            fake.chat_event(&agent, |ev, s, _| {
                s.tool = r.tool;
                ev.session = Some(s.clone());
            });
            json(StatusCode::OK, &serde_json::json!({}))
        }
        // A setting chosen in the composer, the session sent back as the daemon does.
        (&Method::PUT, ["v1", "agents", p, a, "chat", "options", id]) if fake.is_fake(p) => {
            let agent = format!("{p}/{a}");
            let body = req.into_body().collect().await.map(|b| b.to_bytes()).unwrap_or_default();
            let r: ChatOptionRequest = serde_json::from_slice(&body).unwrap_or_default();
            let id = id.to_string();
            fake.chat_event(&agent, |ev, s, _| {
                if let Some(o) = s.options.iter_mut().find(|o| o.id == id) {
                    o.value = r.value;
                }
                ev.session = Some(s.clone());
            });
            json(StatusCode::OK, &serde_json::json!({}))
        }
        (&Method::POST, ["v1", "agents", p, a, "chat", "messages"]) if fake.is_fake(p) => {
            let agent = format!("{p}/{a}");
            let body = req
                .into_body()
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let msg: ChatMessageRequest = serde_json::from_slice(&body).unwrap_or_default();
            let item = ChatItem {
                id: format!("u{}", rand_id()),
                turn: String::new(),
                kind: "user".into(),
                text: msg.text.clone(),
                created_at: now(),
                updated_at: now(),
                ..Default::default()
            };
            fake.chat_event(&agent, |ev, _, items| {
                items.push(item.clone());
                ev.item = Some(item.clone());
            });
            tokio::spawn(fake.clone().reply(agent, msg.text));
            json(StatusCode::OK, &item)
        }
        _ => match &fake.opts.upstream {
            Some(up) => proxy(up, req).await.unwrap_or_else(|e| {
                json(
                    StatusCode::BAD_GATEWAY,
                    &ApiError {
                        error: e.to_string(),
                        ..Default::default()
                    },
                )
            }),
            None => not_found(&path),
        },
    };
    Ok(res)
}

/// events is the upstream's event stream, if any, with the fake's own mixed in.
async fn events(fake: Arc<Fake>) -> Response<Body> {
    let (tx, rx) = tokio::sync::mpsc::channel::<Bytes>(1024);
    let _ = tx.send(Bytes::from_static(b": connected\n\n")).await;
    {
        let fleet = fake.fleet.lock().unwrap().clone();
        for a in fleet {
            fake.emit(
                EVENT_AGENT,
                &AgentChange {
                    r#ref: a.r#ref,
                    state: a.state,
                    ..Default::default()
                },
            );
        }
    }
    let mut own = fake.events.subscribe();
    let tx2 = tx.clone();
    tokio::spawn(async move {
        while let Ok(s) = own.recv().await {
            if tx2.send(Bytes::from(s)).await.is_err() {
                return;
            }
        }
    });
    if let Some(up) = fake.opts.upstream.clone() {
        tokio::spawn(async move {
            let Ok(mut sender) = connect(&up).await else {
                return;
            };
            let req = Request::get("/v1/events")
                .header("host", "agentbox")
                .body(Full::new(Bytes::new()))
                .unwrap();
            let Ok(res) = sender.send_request(req).await else {
                return;
            };
            let mut body = res.into_body();
            while let Some(Ok(frame)) = body.frame().await {
                if let Ok(data) = frame.into_data()
                    && tx.send(data).await.is_err()
                {
                    return;
                }
            }
        });
    }
    let stream = futures_stream(rx);
    Response::builder()
        .header("content-type", "text/event-stream")
        .body(BodyExt::boxed(StreamBody::new(stream)))
        .unwrap()
}

fn futures_stream(
    rx: tokio::sync::mpsc::Receiver<Bytes>,
) -> impl futures_core::Stream<Item = Result<Frame<Bytes>, Infallible>> + Send + 'static {
    struct S(tokio::sync::mpsc::Receiver<Bytes>);
    impl futures_core::Stream for S {
        type Item = Result<Frame<Bytes>, Infallible>;
        fn poll_next(
            mut self: std::pin::Pin<&mut Self>,
            cx: &mut std::task::Context<'_>,
        ) -> std::task::Poll<Option<Self::Item>> {
            self.0.poll_recv(cx).map(|o| o.map(|b| Ok(Frame::data(b))))
        }
    }
    S(rx)
}

/// vnc_view upgrades to a WebSocket and relays it to the VNC server
/// (desktop.rs), or refuses as the daemon does while it isn't running.
async fn vnc_view(req: Request<Incoming>, r#ref: &str) -> Response<Body> {
    use futures_util::{SinkExt as _, StreamExt as _};
    use tokio::io::{AsyncReadExt as _, AsyncWriteExt as _};
    use tokio_tungstenite::tungstenite::{Message, handshake::derive_accept_key, protocol::Role};

    let Some(key) = req.headers().get("sec-websocket-key").cloned() else {
        return json(
            StatusCode::BAD_REQUEST,
            &ApiError {
                error: "not a WebSocket".into(),
                ..Default::default()
            },
        );
    };
    let Some(vnc) = desktop::connect().await else {
        return bad_request(desktop::refusal(r#ref));
    };
    tokio::spawn(async move {
        let Ok(upgraded) = hyper::upgrade::on(req).await else {
            return;
        };
        let ws = tokio_tungstenite::WebSocketStream::from_raw_socket(
            TokioIo::new(upgraded),
            Role::Server,
            None,
        )
        .await;
        let (mut sink, mut source) = ws.split();
        let (mut rd, mut wr) = vnc.into_split();
        let up = async {
            let mut buf = vec![0u8; 64 * 1024];
            while let Ok(n) = rd.read(&mut buf).await {
                if n == 0
                    || sink
                        .send(Message::Binary(buf[..n].to_vec().into()))
                        .await
                        .is_err()
                {
                    break;
                }
            }
        };
        let down = async {
            while let Some(Ok(m)) = source.next().await {
                if let Message::Binary(b) = m
                    && wr.write_all(&b).await.is_err()
                {
                    break;
                }
            }
        };
        tokio::select! {
            _ = up => {}
            _ = down => {}
        }
    });
    Response::builder()
        .status(StatusCode::SWITCHING_PROTOCOLS)
        .header("connection", "Upgrade")
        .header("upgrade", "websocket")
        .header("sec-websocket-accept", derive_accept_key(key.as_bytes()))
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}

/// terminal upgrades to a WebSocket and runs a shell in a PTY behind it:
/// binary frames are its bytes both ways, text frames a TerminalResize.
fn terminal(req: Request<Incoming>) -> Response<Body> {
    use futures_util::{SinkExt as _, StreamExt as _};
    use portable_pty::{CommandBuilder, PtySize, native_pty_system};
    use tokio_tungstenite::tungstenite::{Message, handshake::derive_accept_key, protocol::Role};

    let Some(key) = req.headers().get("sec-websocket-key").cloned() else {
        return json(
            StatusCode::BAD_REQUEST,
            &ApiError {
                error: "not a WebSocket".into(),
                ..Default::default()
            },
        );
    };
    let query: HashMap<String, String> = req
        .uri()
        .query()
        .unwrap_or("")
        .split('&')
        .filter_map(|kv| {
            kv.split_once('=')
                .map(|(k, v)| (k.to_string(), v.to_string()))
        })
        .collect();
    let size = |k: &str, d: u16| query.get(k).and_then(|v| v.parse().ok()).unwrap_or(d);
    let (cols, rows) = (size("cols", 120), size("rows", 32));
    tokio::spawn(async move {
        let Ok(upgraded) = hyper::upgrade::on(req).await else {
            return;
        };
        let ws = tokio_tungstenite::WebSocketStream::from_raw_socket(
            TokioIo::new(upgraded),
            Role::Server,
            None,
        )
        .await;
        let pty = match native_pty_system().openpty(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        }) {
            Ok(p) => p,
            Err(e) => {
                eprintln!("pty: {e}");
                return;
            }
        };
        let mut cmd =
            CommandBuilder::new(std::env::var("SHELL").unwrap_or_else(|_| "/bin/bash".into()));
        cmd.env("TERM", "xterm-256color");
        if let Some(home) = std::env::var_os("HOME") {
            cmd.cwd(home);
        }
        let Ok(mut child) = pty.slave.spawn_command(cmd) else {
            return;
        };
        let mut reader = pty.master.try_clone_reader().unwrap();
        let mut writer = pty.master.take_writer().unwrap();
        let (mut sink, mut source) = ws.split();
        let (out_tx, mut out_rx) = tokio::sync::mpsc::unbounded_channel::<Vec<u8>>();
        std::thread::spawn(move || {
            let mut buf = [0u8; 32 * 1024];
            while let Ok(n) = std::io::Read::read(&mut reader, &mut buf) {
                if n == 0 || out_tx.send(buf[..n].to_vec()).is_err() {
                    break;
                }
            }
        });
        let send = async {
            while let Some(b) = out_rx.recv().await {
                if sink.send(Message::Binary(b.into())).await.is_err() {
                    break;
                }
            }
            let _ = sink.close().await;
        };
        let master = std::sync::Mutex::new(pty.master); // Send, not Sync
        let recv = async {
            while let Some(Ok(m)) = source.next().await {
                match m {
                    Message::Binary(b) => {
                        if std::io::Write::write_all(&mut writer, &b).is_err() {
                            break;
                        }
                    }
                    Message::Text(t) => {
                        if let Ok(r) = serde_json::from_str::<TerminalResize>(&t) {
                            let _ = master.lock().unwrap().resize(PtySize {
                                rows: r.rows,
                                cols: r.cols,
                                pixel_width: 0,
                                pixel_height: 0,
                            });
                        }
                    }
                    Message::Close(_) => break,
                    _ => {}
                }
            }
        };
        tokio::select! {
            _ = send => {}
            _ = recv => {}
        }
        let _ = child.kill();
    });
    Response::builder()
        .status(StatusCode::SWITCHING_PROTOCOLS)
        .header("connection", "Upgrade")
        .header("upgrade", "websocket")
        .header("sec-websocket-accept", derive_accept_key(key.as_bytes()))
        .body(Full::new(Bytes::new()).boxed())
        .unwrap()
}

type Sender = hyper::client::conn::http1::SendRequest<Full<Bytes>>;

async fn connect(socket: &PathBuf) -> Result<Sender> {
    let stream = UnixStream::connect(socket).await?;
    let (sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
    tokio::spawn(conn);
    Ok(sender)
}

/// upstream_pick_up is whether the real daemon's project picks up issues
/// (PATCH /v1/projects/{p} goes to it).
async fn upstream_pick_up(fake: Arc<Fake>) -> bool {
    let Some(up) = fake.opts.upstream.clone() else {
        return false;
    };
    let get = async {
        let mut sender = connect(&up).await?;
        let res = sender
            .send_request(Request::get("/v1/projects").body(Full::new(Bytes::new()))?)
            .await?;
        let body = res.into_body().collect().await?.to_bytes();
        anyhow::Ok(serde_json::from_slice::<Vec<Project>>(&body)?)
    };
    get.await
        .ok()
        .and_then(|ps| ps.into_iter().find(|p| p.name == fake.opts.project))
        .is_some_and(|p| p.pick_up_issues)
}

/// fake_release is the release FAKE_RELEASE names, if any.
fn fake_release() -> Option<UpdateRelease> {
    let path = std::env::var_os("FAKE_RELEASE")?;
    serde_json::from_slice(&std::fs::read(path).ok()?).ok()
}

async fn proxy(up: &PathBuf, req: Request<Incoming>) -> Result<Response<Body>> {
    let (parts, body) = req.into_parts();
    let body = body.collect().await?.to_bytes();
    let mut sender = connect(up).await?;
    let what = format!("{} {}", parts.method, parts.uri);
    let res = sender
        .send_request(Request::from_parts(parts, Full::new(body)))
        .await?;
    if !res.status().is_success() {
        eprintln!("upstream {what}: {}", res.status());
    }
    let (parts, body) = res.into_parts();
    let body = body.collect().await?.to_bytes();
    Ok(Response::from_parts(parts, Full::new(body).boxed()))
}

/// proxy_full is proxy for a request whose body is already read.
async fn proxy_full(up: &PathBuf, req: Request<Full<Bytes>>) -> Result<Response<Body>> {
    let mut sender = connect(up).await?;
    let res = sender.send_request(req).await?;
    let (parts, body) = res.into_parts();
    let body = body.collect().await?.to_bytes();
    Ok(Response::from_parts(parts, Full::new(body).boxed()))
}

fn parse() -> Opts {
    let mut o = Opts {
        listen: "/t/fake-agentbox.sock".into(),
        upstream: None,
        project: "demo".into(),
        agents: 6,
        history: 400,
        rate: 60,
        media: std::env::var_os("HOME")
            .map(PathBuf::from)
            .unwrap_or_default()
            .join("abx/fake-media"),
        worktree: std::env::current_dir().unwrap_or_default(),
        neighbours: Vec::new(),
        icons: Vec::new(),
    };
    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        let mut v = || args.next().expect("a value");
        match a.as_str() {
            "--listen" => o.listen = v().into(),
            "--upstream" => o.upstream = Some(v().into()),
            "--project" => o.project = v(),
            "--agents" => o.agents = v().parse().unwrap(),
            "--history" => o.history = v().parse().unwrap(),
            "--rate" => o.rate = v().parse().unwrap(),
            "--media" => o.media = v().into(),
            "--worktree" => o.worktree = v().into(),
            "--neighbours" => {
                o.neighbours = v()
                    .split(',')
                    .filter_map(|p| {
                        let (name, n) = p.split_once(':')?;
                        Some((name.to_string(), n.parse().ok()?))
                    })
                    .collect()
            }
            "--icons" => {
                o.icons = v()
                    .split(',')
                    .filter_map(|p| {
                        let (name, icon) = p.split_once('=')?;
                        Some((name.to_string(), icon.to_string()))
                    })
                    .collect()
            }
            _ => panic!("unknown flag {a}"),
        }
    }
    o
}

#[tokio::main]
async fn main() -> Result<()> {
    let opts = parse();
    let _ = std::fs::remove_file(&opts.listen);
    let listener = UnixListener::bind(&opts.listen)?;
    eprintln!(
        "fake daemon on {} (upstream {:?})",
        opts.listen.display(),
        opts.upstream
    );
    let fake = Fake::new(opts);
    // FAKE_STILL=1 keeps the fixtures still: no agent starts or stops, no
    // files change, for screenshots that must match (native/scripts/parity.sh).
    if !still() {
        tokio::spawn(fake.clone().churn());
        tokio::spawn(agents::touch(fake.clone()));
        tokio::spawn(resources::sample(fake.clone()));
    }
    tokio::spawn(panel::serve_preview());
    loop {
        let (stream, _) = listener.accept().await?;
        let fake = fake.clone();
        tokio::spawn(async move {
            let svc = hyper::service::service_fn(move |req| handle(fake.clone(), req));
            let _ = hyper::server::conn::http1::Builder::new()
                .serve_connection(TokioIo::new(stream), svc)
                .with_upgrades()
                .await;
        });
    }
}

/// The Hatch page agent-02 published, as Hatch would serve its HTML.
const FAKE_PAGE: &str = r#"<!doctype html><html><head><meta charset="utf-8"><title>Settings: before and after</title>
<style>body{margin:0;font:15px system-ui,sans-serif;color:#1c1c22;background:#fafafa}main{max-width:720px;margin:0 auto;padding:40px 24px}
h1{font-size:26px;margin:0 0 6px}p{color:#55555f;line-height:1.5}.grid{display:grid;grid-template-columns:1fr 1fr;gap:16px;margin-top:24px}
.card{background:#fff;border:1px solid #e4e4ea;border-radius:12px;padding:16px}.card b{display:block;margin-bottom:8px}
.bar{height:10px;border-radius:5px;background:#7c8cff;margin:6px 0}.bar.o{background:#f87171;width:70%}.bar.n{background:#34d399;width:40%}</style></head>
<body><main><h1>Settings: before and after</h1><p>The Settings page now loads in 40% of the time it took, and the language picker remembers its choice.</p>
<div class="grid"><div class="card"><b>Before</b><div class="bar o"></div><p>1.8 s to first paint</p></div>
<div class="card"><b>After</b><div class="bar n"></div><p>0.7 s to first paint</p></div></div>
<p id="js">Scripts are off.</p><script>document.getElementById('js').textContent='Scripts run in the page, sandboxed.'</script></main></body></html>"#;

/// media_in is whether GET /v1/media's ?project= and ?agent= keep an item,
/// and with `narrow` its ?only= kind and ?q= words too.
fn media_in(m: &MediaItem, query: &HashMap<String, String>, narrow: bool) -> bool {
    let (project, name) = m.agent.split_once('/').unwrap_or(("", &m.agent));
    let get = |k: &str| query.get(k).map(String::as_str).unwrap_or("");
    (get("project").is_empty() || get("project") == project)
        && (get("agent").is_empty() || get("agent") == name)
        && (!narrow || get("only").is_empty() || get("only") == m.kind)
        && (!narrow
            || get("q").split_whitespace().all(|w| {
                let w = w.to_lowercase();
                m.name.to_lowercase().contains(&w)
                    || m.file.to_lowercase().contains(&w)
                    || m.text.to_lowercase().contains(&w)
            }))
}
