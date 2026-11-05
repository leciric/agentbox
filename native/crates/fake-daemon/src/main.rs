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

const LOREM: &str = "The daemon owns the state, and every client is a thin one: it asks over the socket and \
shows what comes back. This reply is **streamed** a few words at a time, the way an agent's answer arrives, \
with `inline code`, lists and a block of code so the renderer has real work to do.";

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
            let items = (0..self.opts.history)
                .map(|i| history_item(agent, i))
                .collect();
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
        let body = format!("You said: *{to}*.\n\n{}", [LOREM; 8].join("\n\n"));
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
        (&Method::GET, ["v1", "agents", p, a, "chat"]) if fake.is_fake(p) => {
            json(StatusCode::OK, &fake.thread(&format!("{p}/{a}")))
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
                .await;
        });
    }
}
