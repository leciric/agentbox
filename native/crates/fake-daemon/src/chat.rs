//! An agent's conversation, as the daemon keeps one: turns headed by your
//! messages, each settled with its result, its tool calls and thoughts
//! between, and now and then what else a chat holds — a subagent and its
//! work, pictures and a file you sent, a compaction, a notice with its
//! handoff, another project's message, a scheduled run, a Hatch page, an
//! error — and the checkpoints its last turns ended with, and finding in it
//! (GET …/chat/search).
use std::time::{Duration, SystemTime};

use agentbox_api::*;

fn at(t: SystemTime) -> String {
    humantime::format_rfc3339_millis(t).to_string()
}

fn base(n: usize) -> SystemTime {
    crate::clock() - Duration::from_secs(60 * (n as u64 + 30))
}

fn item(id: String, kind: &str, turn: &str, t: SystemTime) -> ChatItem {
    ChatItem {
        id,
        turn: turn.into(),
        kind: kind.into(),
        created_at: at(t),
        updated_at: at(t),
        ..Default::default()
    }
}

fn tool(id: String, turn: &str, t: SystemTime, kind: &str, title: &str) -> ChatItem {
    ChatItem {
        tool: Some(ChatTool {
            call_id: format!("c-{id}"),
            title: title.into(),
            kind: kind.into(),
            status: "completed".into(),
            command: if kind == "execute" {
                title.into()
            } else {
                String::new()
            },
            paths: if kind == "read" || kind == "edit" {
                vec![format!(
                    "/home/lint/work/{}",
                    title.rsplit(' ').next().unwrap_or(title)
                )]
            } else {
                Vec::new()
            },
            output: if kind == "execute" {
                "ok  \tagentbox/internal/api\t0.065s".into()
            } else {
                String::new()
            },
            ..Default::default()
        }),
        ..item(id, "tool", turn, t)
    }
}

/// user_text is what you asked in turn i.
fn user_text(i: usize) -> String {
    format!(
        "Message {i}: can you look at `internal/api` and check the {} endpoint?",
        ["chat", "fleet", "events", "projects"][i % 4]
    )
}

/// history is a conversation of n items' worth of turns, the last settled.
pub fn history(agent: &str, n: usize, answer: impl Fn(usize) -> String) -> Vec<ChatItem> {
    let start = base(n);
    let mut out: Vec<ChatItem> = Vec::new();
    let users: Vec<usize> = (0..n).step_by(2).collect();
    let last = users.len();
    for (k, &i) in users.iter().enumerate() {
        let t0 = start + Duration::from_secs(60 * i as u64);
        let tid = format!("h{i}");
        let took = 17 + (i as u64 * 7) % 140;
        let mut u = item(tid.clone(), "user", "", t0);
        u.text = user_text(i);
        u.result = Some(ChatTurnResult {
            state: if i % 50 == 26 {
                "cancelled"
            } else {
                "completed"
            }
            .into(),
            stop_reason: "end_turn".into(),
            ended_at: at(t0 + Duration::from_secs(took)),
            ..Default::default()
        });
        let left = last - k; // turns from the end
        if left == 5 {
            // A picture and a file sent with it.
            u.text = "Here's how the settings page looks now, and the log from the run.".into();
            u.images = vec![ChatImage {
                id: "img1".into(),
                mime_type: "image/png".into(),
                name: "settings.png".into(),
                size: 120_000,
            }];
            u.files = vec![ChatFile {
                id: "file1".into(),
                name: "run.log".into(),
                mime_type: "text/plain".into(),
                size: 4096,
                ..Default::default()
            }];
        }
        out.push(u);
        let t = |s: u64| t0 + Duration::from_secs(s);
        let tools = |out: &mut Vec<ChatItem>| {
            out.push(tool(
                format!("{tid}-t1"),
                &tid,
                t(2),
                "read",
                "Read internal/api/chat.go",
            ));
            out.push(tool(
                format!("{tid}-t2"),
                &tid,
                t(4),
                "execute",
                "go test ./internal/api",
            ));
        };
        match left {
            3 => {
                // A subagent and what it did.
                let sub = format!("{tid}-sub");
                out.push(ChatItem {
                    subagent: Some(ChatSubagent {
                        name: "Explore".into(),
                        task: "Find where the chat's events are sent".into(),
                        state: "completed".into(),
                    }),
                    ..item(sub.clone(), "subagent", &tid, t(3))
                });
                for (j, (k, title)) in [("search", "chatEvent"), ("read", "Read internal/daemon/chat.go")]
                    .iter()
                    .enumerate()
                {
                    let mut c = tool(format!("{sub}-{j}"), &tid, t(5 + j as u64), k, title);
                    c.parent = sub.clone();
                    out.push(c);
                }
                let mut said = item(format!("{sub}-say"), "assistant", &tid, t(9));
                said.parent = sub;
                said.text = "They go out from `internal/daemon/chat.go`, in `emitChat`.".into();
                out.push(said);
            }
            4 => {
                out.push(ChatItem {
                    text: "Checking the edit before it goes in".into(),
                    ..item(format!("{tid}-th"), "thought", &tid, t(1))
                });
                out.push(ChatItem {
                    tool: Some(ChatTool {
                        call_id: "c-edit".into(),
                        title: "Edit native/crates/app/src/chat.rs".into(),
                        kind: "edit".into(),
                        status: "completed".into(),
                        paths: vec!["native/crates/app/src/chat.rs".into()],
                        diffs: vec![ChatDiff {
                            path: "native/crates/app/src/chat.rs".into(),
                            old_text: "fn a() {}\nfn b() {}\nfn c() {}\n".into(),
                            new_text: "fn a() {}\nfn b(x: u32) {}\nfn c() {}\nfn d() {}\n".into(),
                            ..Default::default()
                        }],
                        ..Default::default()
                    }),
                    ..item(format!("{tid}-ed"), "tool", &tid, t(3))
                });
            }
            6 => {
                // A page published on Hatch.
                out.push(ChatItem {
                    tool: Some(ChatTool {
                        call_id: "c-page".into(),
                        name: "mcp__hatch__publish_page".into(),
                        title: "mcp__hatch__publish_page".into(),
                        kind: "other".into(),
                        status: "completed".into(),
                        output: "Published: https://usehatch.dev/p/O4CQYsCRRC1WKQ16 (private)".into(),
                        ..Default::default()
                    }),
                    ..item(format!("{tid}-pg"), "tool", &tid, t(6))
                });
            }
            8 => out.push(ChatItem {
                text: "The connection to the tool dropped; it carries on from where it was.".into(),
                ..item(format!("{tid}-err"), "error", &tid, t(8))
            }),
            10 => out.push(ChatItem {
                text: "Context compacted: the session carries on from a summary, saved to project memory.".into(),
                compaction: Some(ChatCompaction {
                    state: "done".into(),
                    ..Default::default()
                }),
                ..item(format!("{tid}-cmp"), "compaction", &tid, t(10))
            }),
            12 => out.push(ChatItem {
                text: "Rolled back to turn 190: the turns after it left the conversation.".into(),
                handoff: "You were working on the chat's composer. The files are as turn 190 ended; \
                          the user asked to redo the attachments."
                    .into(),
                ..item(format!("{tid}-ntc"), "notice", &tid, t(1))
            }),
            14 => out.push(ChatItem {
                text: "Can you check whether the **website** uses the same chat events? We'd like one schema.".into(),
                project_message: Some(ChatProjectMessage {
                    project: "website".into(),
                    direction: "in".into(),
                    thread: "th1".into(),
                    kind: "ask".into(),
                    topic: "chat events".into(),
                    expects_reply: true,
                    hop: 0,
                }),
                ..item(format!("{tid}-pm"), "notice", &tid, t(1))
            }),
            16 => out.push(ChatItem {
                text: "Run the nightly checks and report anything red.".into(),
                scheduled_run: Some(ChatScheduledRun {
                    schedule: "s1".into(),
                    run: "r1".into(),
                    title: "Nightly checks".into(),
                    trigger: "schedule".into(),
                    ..Default::default()
                }),
                ..item(format!("{tid}-sch"), "notice", &tid, t(1))
            }),
            _ if i + 2 == n => out.push(ChatItem {
                text: "agent-02 finished.".into(),
                finished: vec!["demo/agent-02".into()],
                ..item(format!("{tid}-fin"), "notice", &tid, t(1))
            }),
            _ if i % 8 == 0 => tools(&mut out),
            _ => {}
        }
        if i + 1 < n {
            let mut a = item(format!("h{}", i + 1), "assistant", &tid, t(took - 1));
            a.text = answer(i + 1);
            if i % 50 != 26 {
                out.push(a);
            }
        }
    }
    let _ = agent;
    out
}

/// checkpoints are the ones the last three settled turns ended with.
pub fn checkpoints(r#ref: &str, n: usize) -> Vec<Checkpoint> {
    let users: Vec<usize> = (0..n).step_by(2).collect();
    let start = base(n);
    users
        .iter()
        .rev()
        .take(3)
        .rev()
        .map(|&i| {
            let number = (i / 2 + 1) as i64;
            Checkpoint {
                id: format!("cp-{}-{number}", r#ref.replace('/', "-")),
                kind: "turn".into(),
                turn: format!("h{i}"),
                number,
                prompt: user_text(i),
                commit: format!("{:040x}", 0xc0ffee * number as u128),
                head: format!("{:040x}", 0xbeef * number as u128),
                created_at: at(start + Duration::from_secs(60 * i as u64 + 30)),
            }
        })
        .collect()
}

/// search is what holds the words, oldest first, each with its snippet.
pub fn search(items: &[ChatItem], q: &str) -> ChatSearch {
    let ql = q.to_lowercase();
    let hits = items
        .iter()
        .filter(|i| {
            !ql.is_empty() && matches!(i.kind.as_str(), "user" | "assistant" | "aside" | "notice")
        })
        .filter_map(|i| {
            let lower = i.text.to_lowercase();
            let at = lower.find(&ql)?;
            let from = i.text[..at].char_indices().rev().nth(30).map_or(0, |c| c.0);
            let end = (at + ql.len()).min(i.text.len());
            let to = i.text[end..]
                .char_indices()
                .nth(50)
                .map_or(i.text.len(), |c| end + c.0);
            Some(ChatSearchHit {
                agent: String::new(),
                id: i.id.clone(),
                kind: i.kind.clone(),
                at: Some(i.created_at.clone()),
                snippet: vec![
                    ChatSnippetPart {
                        text: i.text[from..at].to_string(),
                        r#match: false,
                    },
                    ChatSnippetPart {
                        text: i.text[at..end].to_string(),
                        r#match: true,
                    },
                    ChatSnippetPart {
                        text: i.text[end..to].to_string(),
                        r#match: false,
                    },
                ],
            })
        })
        .collect();
    ChatSearch {
        query: q.into(),
        hits,
        more: false,
    }
}
