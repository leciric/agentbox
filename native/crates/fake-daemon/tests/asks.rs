//! What the native app is told when an agent asks something, against the
//! fake daemon (src/asks.rs), through the app's own client: the events it
//! acts on as they happen, and what a fresh read (a reconnect, or the app
//! starting) finds still waiting.
use std::path::PathBuf;
use std::process::{Child, Command};
use std::time::{Duration, Instant};

use agentbox_api::client::Method;
use agentbox_api::*;
use tokio::sync::mpsc::UnboundedReceiver;

struct Daemon {
    child: Child,
    socket: PathBuf,
}

impl Drop for Daemon {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
        let _ = std::fs::remove_file(&self.socket);
    }
}

fn start(name: &str) -> Daemon {
    let socket = std::env::temp_dir().join(format!("fake-asks-{name}-{}.sock", std::process::id()));
    let _ = std::fs::remove_file(&socket);
    let media = std::env::temp_dir().join(format!("fake-asks-media-{}", std::process::id()));
    let child = Command::new(env!("CARGO_BIN_EXE_fake-daemon"))
        .arg("--listen")
        .arg(&socket)
        .arg("--media")
        .arg(&media)
        .env("FAKE_STILL", "1")
        .stderr(std::process::Stdio::null())
        .spawn()
        .expect("fake-daemon");
    let until = Instant::now() + Duration::from_secs(20);
    while !socket.exists() {
        assert!(Instant::now() < until, "fake-daemon didn't listen");
        std::thread::sleep(Duration::from_millis(50));
    }
    Daemon { child, socket }
}

/// connected follows the events, from the moment the stream is open.
async fn connected(c: &Client) -> UnboundedReceiver<Option<Event>> {
    let mut rx = c.events();
    let first = tokio::time::timeout(Duration::from_secs(10), rx.recv()).await;
    assert!(matches!(first, Ok(Some(None))), "no event stream");
    rx
}

/// until reads events until `want` has seen one it takes, or fails.
async fn until(
    rx: &mut UnboundedReceiver<Option<Event>>,
    what: &str,
    mut want: impl FnMut(&Event) -> bool,
) {
    let end = tokio::time::Instant::now() + Duration::from_secs(5);
    loop {
        match tokio::time::timeout_at(end, rx.recv()).await {
            Ok(Some(Some(ev))) if want(&ev) => return,
            Ok(Some(_)) => {}
            _ => panic!("no event: {what}"),
        }
    }
}

fn chat_event(ev: &Event) -> Option<ChatEvent> {
    (ev.r#type == EVENT_CHAT)
        .then(|| serde_json::from_value(ev.data.clone()).ok())
        .flatten()
}

fn pending(it: &ChatItem) -> bool {
    it.kind == "permission" && it.permission.as_ref().is_some_and(|p| p.outcome.is_empty())
}

#[tokio::test(flavor = "multi_thread")]
async fn a_permission_request_reaches_the_agents_chat_its_projects_and_a_reconnect() {
    let d = start("perm");
    let c = Client::new(&d.socket);
    let mut rx = connected(&c).await;
    let asked: serde_json::Value = c
        .post(
            "/fake/permission",
            &serde_json::json!({ "agent": "agent-02" }),
        )
        .await
        .unwrap();
    let item = asked["item"].as_str().unwrap().to_string();

    // As it happens: the request in the agent's chat, its session waiting
    // (what the sidebar and the notice go by), and the card in the lead's.
    until(&mut rx, "the request in the agent's chat", |ev| {
        chat_event(ev).is_some_and(|e| {
            e.agent == "demo/agent-02"
                && e.item.as_ref().is_some_and(|i| i.id == item && pending(i))
        })
    })
    .await;
    until(&mut rx, "the agent's session waiting", |ev| {
        chat_event(ev).is_some_and(|e| {
            e.agent == "demo/agent-02"
                && e.session.as_ref().is_some_and(|s| s.state == CHAT_WAITING)
        })
    })
    .await;
    until(&mut rx, "the card in the project's chat", |ev| {
        chat_event(ev).is_some_and(|e| {
            e.agent == "demo/lead"
                && e.item
                    .as_ref()
                    .and_then(|i| i.agent_permission.as_ref())
                    .is_some_and(|ap| {
                        ap.r#ref == "demo/agent-02"
                            && ap.item == item
                            && ap.permission.outcome.is_empty()
                    })
        })
    })
    .await;

    // A fresh client, as after a reconnect: all three are read back.
    let again = Client::new(&d.socket);
    let agents: Vec<Agent> = again.get("/v1/agents").await.unwrap();
    let a = agents.iter().find(|a| a.r#ref == "demo/agent-02").unwrap();
    assert_eq!(a.chat, CHAT_WAITING);
    let thread: ChatThread = again.get("/v1/agents/demo/agent-02/chat").await.unwrap();
    assert!(thread.items.iter().any(|i| i.id == item && pending(i)));
    assert_eq!(thread.session.state, CHAT_WAITING);
    let lead: ChatThread = again.get("/v1/projects/demo/chat").await.unwrap();
    let card = lead
        .items
        .iter()
        .find_map(|i| i.agent_permission.as_ref())
        .unwrap();
    assert_eq!(
        (card.item.as_str(), card.permission.outcome.as_str()),
        (item.as_str(), "")
    );
    assert!(lead.seq > 0, "the read's seq is behind the card's event");

    // Answered from either chat (both answer on the agent's route): it
    // settles in both, and the agent runs again.
    let answer = serde_json::to_vec(&ChatAnswerRequest {
        option_id: "allow".into(),
    })
    .unwrap();
    again
        .raw(
            Method::POST,
            &format!("/v1/agents/demo/agent-02/chat/permissions/{item}"),
            Some(answer),
        )
        .await
        .unwrap();
    until(&mut rx, "the card settled", |ev| {
        chat_event(ev).is_some_and(|e| {
            e.agent == "demo/lead"
                && e.item
                    .as_ref()
                    .and_then(|i| i.agent_permission.as_ref())
                    .is_some_and(|ap| ap.permission.outcome == "allow")
        })
    })
    .await;
    let agents: Vec<Agent> = again.get("/v1/agents").await.unwrap();
    assert_eq!(
        agents
            .iter()
            .find(|a| a.r#ref == "demo/agent-02")
            .unwrap()
            .chat,
        CHAT_RUNNING
    );
}

#[tokio::test(flavor = "multi_thread")]
async fn a_credential_request_is_told_kept_and_answered_from_its_card() {
    let d = start("cred");
    let c = Client::new(&d.socket);
    let mut rx = connected(&c).await;
    let q: Question = c
        .post(
            "/fake/request",
            &serde_json::json!({ "agent": "agent-04", "kind": CREDENTIAL_SECRET, "secretName": "OPENAI_API_KEY" }),
        )
        .await
        .unwrap();
    assert_eq!(q.status, "pending");

    // The question event (the app reads the project's questions again) and
    // the notification (the notice, its toast and the OS's).
    until(&mut rx, "the question event", |ev| {
        ev.r#type == EVENT_QUESTION && ev.data["id"] == q.id.as_str()
    })
    .await;
    until(&mut rx, "the notification", |ev| {
        ev.r#type == EVENT_NOTIFICATION
            && ev.data["question"] == q.id.as_str()
            && ev.data["kind"] == NOTIFY_QUESTION
    })
    .await;

    // Read afresh: still waiting, beside the decision the lead passed on,
    // and still unseen, for a notice missed while the app was cut off.
    let again = Client::new(&d.socket);
    let open: Vec<Question> = again.get("/v1/projects/demo/questions").await.unwrap();
    assert!(
        open.iter()
            .any(|x| x.id == q.id && x.kind == CREDENTIAL_SECRET)
    );
    assert!(
        open.iter()
            .any(|x| x.kind.is_empty() && x.status == "escalated")
    );
    let unseen: NotificationPage = again
        .get("/v1/notifications?limit=50&unseen=1")
        .await
        .unwrap();
    assert!(unseen.items.iter().any(|n| n.question == q.id));

    // The card's answer settles it: gone from what waits, and told.
    let answered: Question = again
        .post(
            &format!("/v1/projects/demo/questions/{}/credential", q.id),
            &AnswerCredentialRequest {
                value: "sk-test".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(answered.status, "answered");
    until(&mut rx, "the answer's question event", |ev| {
        ev.r#type == EVENT_QUESTION
            && ev.data["id"] == q.id.as_str()
            && ev.data["status"] == "answered"
    })
    .await;
    let open: Vec<Question> = again.get("/v1/projects/demo/questions").await.unwrap();
    assert!(!open.iter().any(|x| x.id == q.id));
    // Answered twice: refused, as the daemon refuses it.
    let twice = again
        .post::<_, Question>(
            &format!("/v1/projects/demo/questions/{}/credential", q.id),
            &AnswerCredentialRequest {
                refuse: true,
                ..Default::default()
            },
        )
        .await;
    assert!(twice.is_err());
}

#[tokio::test(flavor = "multi_thread")]
async fn a_connector_request_is_answered_once_its_sign_in_finishes() {
    let d = start("conn");
    let c = Client::new(&d.socket);
    let q: Question = c
        .post(
            "/fake/request",
            &serde_json::json!({ "agent": "agent-01", "kind": QUESTION_CONNECTOR, "connector": "notion" }),
        )
        .await
        .unwrap();
    // What the card does: add it to the project, sign in, wait, give it.
    let set = SetConnectorRequest {
        url: "https://mcp.notion.com/mcp".into(),
        ..Default::default()
    };
    c.raw(
        Method::PUT,
        "/v1/projects/demo/connectors/notion",
        Some(serde_json::to_vec(&set).unwrap()),
    )
    .await
    .unwrap();
    let r: ConnectResult = c
        .post(
            "/v1/projects/demo/connectors/notion/connect",
            &serde_json::json!({}),
        )
        .await
        .unwrap();
    assert_eq!(r.connector.status, CONNECTOR_CONNECTING);
    let end = Instant::now() + Duration::from_secs(5);
    loop {
        let list: Vec<Connector> = c.get("/v1/projects/demo/connectors").await.unwrap();
        if list
            .iter()
            .any(|x| x.name == "notion" && x.status == CONNECTOR_CONNECTED)
        {
            break;
        }
        assert!(Instant::now() < end, "the sign-in never finished");
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    let answered: Question = c
        .post(
            &format!("/v1/projects/demo/questions/{}/credential", q.id),
            &AnswerCredentialRequest {
                connector: "notion".into(),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(answered.status, "answered");
}
