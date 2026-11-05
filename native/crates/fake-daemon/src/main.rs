//! A stand-in daemon for developing and measuring the native app where agents
//! can't run (no Incus). It serves a project's agents from fixtures, changes
//! their state now and then, and streams an agent's replies; with --upstream it
//! passes every other request, and the event stream, to a real daemon, so the
//! lead's chat is the real one.
//!
//!   fake-daemon --listen /t/fake.sock [--upstream ~/.local/share/agentbox/run/agentbox.sock]
//!               [--project demo] [--agents 6] [--history 400] [--rate 60]
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

type Body = BoxBody<Bytes, Infallible>;

struct Opts {
    listen: PathBuf,
    upstream: Option<PathBuf>,
    project: String,
    agents: usize,
    history: usize,
    rate: u64,
}

/// A chat: its last event's seq, its session and its items.
type Chat = (i64, ChatSession, Vec<ChatItem>);

struct Fake {
    opts: Opts,
    fleet: Mutex<Vec<FleetAgent>>,
    chats: Mutex<HashMap<String, Chat>>,
    events: broadcast::Sender<String>,
}

fn now() -> String {
    humantime::format_rfc3339_millis(std::time::SystemTime::now()).to_string()
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
            })
            .collect();
        let (events, _) = broadcast::channel(4096);
        Arc::new(Self {
            opts,
            fleet: Mutex::new(fleet),
            chats: Mutex::new(HashMap::new()),
            events,
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

    fn is_fake(&self, project: &str) -> bool {
        project == self.opts.project
    }

    fn thread(&self, agent: &str) -> ChatThread {
        let mut chats = self.chats.lock().unwrap();
        let (seq, session, items) = chats.entry(agent.into()).or_insert_with(|| {
            let mut items: Vec<ChatItem> = (0..self.opts.history)
                .map(|i| history_item(agent, i))
                .collect();
            items.extend(working_items());
            (
                0,
                ChatSession {
                    state: CHAT_READY.into(),
                    tool: "claude".into(),
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
                let a = &mut fleet[(n * 7) % len];
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

async fn handle(fake: Arc<Fake>, req: Request<Incoming>) -> Result<Response<Body>, Infallible> {
    let path = req.uri().path().to_string();
    let parts: Vec<&str> = path.trim_matches('/').split('/').collect();
    let method = req.method().clone();
    let res = match (&method, parts.as_slice()) {
        (&Method::GET, ["v1", "events"]) => events(fake.clone()).await,
        (&Method::GET, ["v1", "projects"]) if fake.opts.upstream.is_none() => {
            let p = Project {
                name: fake.opts.project.clone(),
                display_name: fake.opts.project.clone(),
                ..Default::default()
            };
            json(StatusCode::OK, &vec![p])
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
        // Every agent, as GET /v1/agents lists them (a FleetAgent is an Agent and more).
        (&Method::GET, ["v1", "agents"]) => {
            json(StatusCode::OK, &fake.fleet.lock().unwrap().clone())
        }
        (&Method::GET, ["v1", "projects", p, "fleet"]) if fake.is_fake(p) => {
            let fleet = Fleet {
                project: p.to_string(),
                agents: fake.fleet.lock().unwrap().clone(),
                ..Default::default()
            };
            json(StatusCode::OK, &fleet)
        }
        // An agent's terminal: a shell on this machine, in place of the
        // agent's tmux session (the same protocol as internal/daemon/terminal.go).
        (&Method::GET, ["v1", "agents", p, _a, "terminal"]) if fake.is_fake(p) => terminal(req),
        // An agent's desktop: this machine's own VNC server (:5900), as raw
        // RFB in WebSocket frames like internal/daemon/browser.go's vncView.
        (&Method::GET, ["v1", "agents", p, _a, "browser", "view"]) if fake.is_fake(p) => {
            vnc_view(req)
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
            let end = query
                .get("before")
                .and_then(|b| t.items.iter().position(|i| &i.id == b))
                .unwrap_or(t.items.len());
            let limit = query
                .get("limit")
                .and_then(|l| l.parse().ok())
                .filter(|&l: &usize| l > 0)
                .unwrap_or(end);
            let start = end.saturating_sub(limit);
            t.older = start > 0;
            t.items = t.items[start..end].to_vec();
            json(StatusCode::OK, &t)
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

/// vnc_view upgrades to a WebSocket and relays it to the VNC server on
/// 127.0.0.1:5900 (VNC_ADDR overrides it).
fn vnc_view(req: Request<Incoming>) -> Response<Body> {
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
    tokio::spawn(async move {
        let addr = std::env::var("VNC_ADDR").unwrap_or_else(|_| "127.0.0.1:5900".into());
        let Ok(vnc) = tokio::net::TcpStream::connect(&addr).await else {
            eprintln!("vnc: can't reach {addr}");
            return;
        };
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

fn parse() -> Opts {
    let mut o = Opts {
        listen: "/t/fake-agentbox.sock".into(),
        upstream: None,
        project: "demo".into(),
        agents: 6,
        history: 400,
        rate: 60,
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
    tokio::spawn(fake.clone().churn());
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
