//! What the fake's agents have running (internal/daemon/processes.go): GET
//! /v1/processes for every agent, GET …/processes for one, and POST
//! …/processes/{pid}/kill. The fake's machines run nothing, so each running
//! agent gets made-up trees: its AI tool's and the machine's (protected),
//! and a few of its own picked by its place in the fleet — a dev server idle
//! for most of an hour, a build holding gigabytes, a forgotten watcher — so
//! the Running view has heavy and idle trees to flag. A reading wanders a
//! little each time (none under FAKE_STILL), and a kill takes a moment, as
//! SIGTERM's grace does, before the tree is gone for good.
use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use agentbox_api::*;
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, json, still};

const MB: i64 = 1024 * 1024;

/// How long a kill takes to answer: the daemon waits for SIGTERM to work.
const KILL_TAKES: Duration = Duration::from_millis(1200);

/// What has been killed, by agent ref: the pids gone from its trees.
static KILLED: Mutex<Option<HashMap<String, HashSet<i64>>>> = Mutex::new(None);

fn killed(r: &str) -> HashSet<i64> {
    KILLED
        .lock()
        .unwrap()
        .get_or_insert_with(Default::default)
        .get(r)
        .cloned()
        .unwrap_or_default()
}

/// A tree as the fixtures write it: its root, then its children as
/// (name, command, MB).
struct Fixture {
    pid: i64,
    label: &'static str,
    kind: &'static str,
    members: &'static [(&'static str, &'static str, i64)],
    /// Share of a core while its agent works, and while it doesn't.
    cpu_busy: f64,
    cpu_idle: f64,
    /// How long ago it started, and how long it has been idle.
    age_s: i64,
    idle_s: i64,
}

const TOOL: Fixture = Fixture {
    pid: 312,
    label: "Claude Code",
    kind: PROCESS_TREE_TOOL,
    members: &[
        (
            "node",
            "node /usr/local/lib/node_modules/@agentclientprotocol/claude-agent-acp/dist/index.js",
            96,
        ),
        (
            "claude",
            "claude --output-format stream-json --verbose",
            412,
        ),
        (
            "node",
            "node /usr/local/lib/node_modules/@playwright/mcp/cli.js --browser chromium",
            88,
        ),
        ("agentbox", "agentbox mcp memory", 21),
    ],
    cpu_busy: 38.,
    cpu_idle: 0.4,
    age_s: 3 * 3600,
    idle_s: 0,
};

const SYSTEM: Fixture = Fixture {
    pid: 1,
    label: "System",
    kind: PROCESS_TREE_SYSTEM,
    members: &[
        ("systemd", "/sbin/init", 12),
        ("systemd-journal", "/usr/lib/systemd/systemd-journald", 18),
        ("tmux: server", "tmux new-session -d -s agent", 6),
        ("Xvnc", "Xvnc :1 -geometry 1600x1000 -depth 24", 64),
        ("bash", "-bash", 4),
    ],
    cpu_busy: 1.2,
    cpu_idle: 0.3,
    age_s: 3 * 3600 + 120,
    idle_s: 0,
};

/// The agents' own, each agent taking a few by its place in the fleet.
const OWN: &[Fixture] = &[
    Fixture {
        pid: 4180,
        label: "npm run dev",
        kind: PROCESS_TREE_AGENT,
        members: &[
            ("npm run dev", "npm run dev", 58),
            (
                "node",
                "node /home/lint/app/node_modules/.bin/vite --host 0.0.0.0",
                342,
            ),
            (
                "esbuild",
                "/home/lint/app/node_modules/@esbuild/linux-x64/bin/esbuild --service=0.25.0 --ping",
                31,
            ),
        ],
        cpu_busy: 0.3,
        cpu_idle: 0.2,
        age_s: 2 * 3600 + 900,
        idle_s: 47 * 60,
    },
    Fixture {
        pid: 9021,
        label: "cargo build --release -p agentbox-native",
        kind: PROCESS_TREE_AGENT,
        members: &[
            ("cargo", "cargo build --release -p agentbox-native", 96),
            (
                "rustc",
                "rustc --crate-name gpui --edition=2024 crates/gpui/src/gpui.rs",
                1630,
            ),
            (
                "rustc",
                "rustc --crate-name wgpu_core --edition=2021 src/lib.rs",
                812,
            ),
        ],
        cpu_busy: 186.,
        cpu_idle: 178.,
        age_s: 6 * 60,
        idle_s: 0,
    },
    Fixture {
        pid: 2207,
        label: "docker compose up postgres",
        kind: PROCESS_TREE_AGENT,
        members: &[
            ("docker compose", "docker compose up postgres", 44),
            ("postgres", "postgres -c fsync=off", 212),
            ("postgres", "postgres: checkpointer", 18),
            ("postgres", "postgres: walwriter", 9),
        ],
        cpu_busy: 3.1,
        cpu_idle: 0.6,
        age_s: 5 * 3600,
        idle_s: 4 * 3600,
    },
    Fixture {
        pid: 5512,
        label: "go test ./internal/...",
        kind: PROCESS_TREE_AGENT,
        members: &[
            ("go", "go test ./internal/...", 188),
            (
                "daemon.test",
                "/t/go-build2741/b001/daemon.test -test.paniconexit0 -test.timeout=10m0s",
                734,
            ),
            (
                "chat.test",
                "/t/go-build2741/b114/chat.test -test.paniconexit0 -test.timeout=10m0s",
                402,
            ),
        ],
        cpu_busy: 141.,
        cpu_idle: 120.,
        age_s: 3 * 60,
        idle_s: 0,
    },
    Fixture {
        pid: 6630,
        label: "pnpm vitest --watch",
        kind: PROCESS_TREE_AGENT,
        members: &[
            ("pnpm", "pnpm vitest --watch", 62),
            (
                "node",
                "node /home/lint/app/node_modules/vitest/vitest.mjs --watch",
                548,
            ),
        ],
        cpu_busy: 0.8,
        cpu_idle: 0.1,
        age_s: 3 * 3600,
        idle_s: 2 * 3600 + 600,
    },
    Fixture {
        pid: 7104,
        label: "chromium",
        kind: PROCESS_TREE_AGENT,
        members: &[
            (
                "chromium",
                "/usr/lib/chromium/chromium --remote-debugging-port=9222 --user-data-dir=/home/lint/.config/agentbox-browser",
                214,
            ),
            (
                "chromium",
                "/usr/lib/chromium/chromium --type=gpu-process",
                156,
            ),
            (
                "chromium",
                "/usr/lib/chromium/chromium --type=renderer --lang=en-US",
                288,
            ),
            (
                "chromium",
                "/usr/lib/chromium/chromium --type=renderer --lang=en-US",
                171,
            ),
        ],
        cpu_busy: 9.,
        cpu_idle: 1.5,
        age_s: 80 * 60,
        idle_s: 0,
    },
    Fixture {
        pid: 8840,
        label: "python3 -m http.server 8000",
        kind: PROCESS_TREE_AGENT,
        members: &[("python3", "python3 -m http.server 8000", 24)],
        cpu_busy: 0.,
        cpu_idle: 0.,
        age_s: 6 * 3600,
        idle_s: 6 * 3600,
    },
];

/// Which of OWN each agent runs, by its place in the fleet: the first holds
/// the most, one runs nothing of its own.
const PICKS: &[&[usize]] = &[&[1, 0, 5], &[3, 4], &[0, 2, 6], &[], &[5, 6], &[4]];

/// wander is a reading's drift, the same for a given second and seed, and
/// nothing under FAKE_STILL.
fn wander(seed: i64, scale: f64) -> f64 {
    if still() {
        return 0.;
    }
    let t = crate::clock()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.as_secs()) as f64;
    ((t / 7. + seed as f64) * 1.3).sin() * scale
}

fn rfc3339(ago: i64) -> String {
    humantime::format_rfc3339(crate::clock() - Duration::from_secs(ago as u64)).to_string()
}

/// tree is a fixture as the daemon would read it, without the pids killed.
fn tree(f: &Fixture, busy: bool, gone: &HashSet<i64>) -> Option<ProcessTree> {
    if gone.contains(&f.pid) {
        return None;
    }
    let members: Vec<ProcessMember> = f
        .members
        .iter()
        .enumerate()
        .map(|(i, (name, command, mb))| {
            let pid = if i == 0 { f.pid } else { f.pid + 3 + i as i64 };
            let memory = ((*mb as f64) * (1. + wander(pid, 0.03)) * MB as f64) as i64;
            ProcessMember {
                pid,
                ppid: if i == 0 { 1 } else { f.pid },
                name: (*name).into(),
                command: (*command).into(),
                memory,
            }
        })
        .filter(|m| !gone.contains(&m.pid))
        .collect();
    let memory: i64 = members.iter().map(|m| m.memory).sum();
    let protected = f.kind != PROCESS_TREE_AGENT;
    let cpu = if busy { f.cpu_busy } else { f.cpu_idle };
    let cpu = (cpu * (1. + wander(f.pid, 0.15))).max(0.);
    let idle_s = if busy && cpu > 2. { 0 } else { f.idle_s };
    Some(ProcessTree {
        pid: f.pid,
        label: f.label.into(),
        command: f.members[0].1.into(),
        kind: f.kind.into(),
        protected,
        processes: members.len() as i64,
        memory,
        rss: memory + memory / 6,
        pss: memory,
        cpu,
        cpu_seconds: f.age_s as f64 * cpu / 100.,
        started: rfc3339(f.age_s),
        idle_seconds: idle_s,
        heavy: !protected && memory >= 1 << 30,
        idle: !protected && idle_s >= 30 * 60,
        members,
    })
}

/// of is what an agent of the fleet has running, at its place there.
fn of(a: &FleetAgent, place: usize) -> AgentProcesses {
    let running = a.state == "running";
    let busy = a.busy || a.chat == "running";
    let gone = killed(&a.r#ref);
    let mut trees: Vec<ProcessTree> = Vec::new();
    if running {
        let own = PICKS[place % PICKS.len()].iter().map(|&i| &OWN[i]);
        trees = [&TOOL, &SYSTEM]
            .into_iter()
            .chain(own)
            .filter_map(|f| tree(f, busy, &gone))
            .collect();
        trees.sort_by(|x, y| y.memory.cmp(&x.memory).then(x.pid.cmp(&y.pid)));
    }
    AgentProcesses {
        r#ref: a.r#ref.clone(),
        project: a.project.clone(),
        name: a.name.clone(),
        title: a.title.clone(),
        running,
        memory: trees.iter().map(|t| t.memory).sum(),
        killable: trees
            .iter()
            .filter(|t| !t.protected)
            .map(|t| t.memory)
            .sum(),
        cpu: trees.iter().map(|t| t.cpu).sum(),
        processes: trees.iter().map(|t| t.processes).sum(),
        trees,
        error: String::new(),
    }
}

/// agents is the fake's agents and its neighbours', each at its place.
fn agents(fake: &Fake) -> Vec<(usize, FleetAgent)> {
    let mut all = fake.fleet.lock().unwrap().clone();
    all.extend(fake.neighbours());
    all.into_iter().enumerate().collect()
}

fn error(status: StatusCode, error: String, code: &str) -> Response<Body> {
    json(
        status,
        &ApiError {
            error,
            code: code.into(),
            ..Default::default()
        },
    )
}

pub async fn route(
    fake: &Arc<Fake>,
    req: Request<Incoming>,
    query: &HashMap<String, String>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let parts: Vec<String> = path
        .trim_matches('/')
        .split('/')
        .map(crate::decode)
        .collect();
    let parts: Vec<&str> = parts.iter().map(String::as_str).collect();
    let mine = |p: &str| fake.is_fake(p) || fake.neighbour(p);
    let res = match (req.method(), parts.as_slice()) {
        (&Method::GET, ["v1", "processes"]) => {
            let project = query.get("project").map(String::as_str).unwrap_or("");
            if !project.is_empty() && !mine(project) {
                return Err(Box::new(req));
            }
            let mut list: Vec<AgentProcesses> = agents(fake)
                .iter()
                .filter(|(_, a)| project.is_empty() || a.project == project)
                .map(|(i, a)| of(a, *i))
                .filter(|a| a.running)
                .collect();
            list.sort_by(|x, y| y.memory.cmp(&x.memory).then(x.r#ref.cmp(&y.r#ref)));
            json(
                StatusCode::OK,
                &ProcessList {
                    agents: list,
                    at: crate::now(),
                },
            )
        }
        (&Method::GET, ["v1", "agents", p, a, "processes"]) if mine(p) => {
            let r = format!("{p}/{a}");
            match agents(fake).into_iter().find(|(_, x)| x.r#ref == r) {
                Some((i, x)) => json(StatusCode::OK, &of(&x, i)),
                None => error(StatusCode::NOT_FOUND, format!("{r}: no such agent"), ""),
            }
        }
        (&Method::POST, ["v1", "agents", p, a, "processes", pid, "kill"]) if mine(p) => {
            let r = format!("{p}/{a}");
            let Some((i, x)) = agents(fake).into_iter().find(|(_, x)| x.r#ref == r) else {
                return Ok(error(
                    StatusCode::NOT_FOUND,
                    format!("{r}: no such agent"),
                    "",
                ));
            };
            let Ok(pid) = pid.parse::<i64>() else {
                return Ok(crate::bad_request(format!(
                    "{pid:?} isn't a process number"
                )));
            };
            let now = of(&x, i);
            if !now.running {
                return Ok(error(
                    StatusCode::CONFLICT,
                    format!("{r}: its machine isn't running"),
                    "",
                ));
            }
            // The tree it is the root of, or the member it is with what it
            // started (a member here starts nothing).
            let found = now.trees.iter().find_map(|t| {
                if t.pid == pid {
                    Some((t, t.members.clone()))
                } else {
                    t.members
                        .iter()
                        .find(|m| m.pid == pid)
                        .map(|m| (t, vec![m.clone()]))
                }
            });
            let Some((t, members)) = found else {
                return Ok(error(
                    StatusCode::NOT_FOUND,
                    format!("{r}: no process {pid}"),
                    "",
                ));
            };
            if t.protected {
                return Ok(error(
                    StatusCode::FORBIDDEN,
                    format!(
                        "{r}: process {pid} is {}'s, which the machine needs",
                        t.label
                    ),
                    ERROR_PROTECTED_PROCESS,
                ));
            }
            let label = if t.pid == pid {
                t.label.clone()
            } else {
                members[0].command.clone()
            };
            tokio::time::sleep(KILL_TAKES).await;
            {
                let mut k = KILLED.lock().unwrap();
                let gone = k
                    .get_or_insert_with(Default::default)
                    .entry(r.clone())
                    .or_default();
                gone.extend(members.iter().map(|m| m.pid));
            }
            eprintln!("processes: killed {pid} in {r} ({label})");
            json(
                StatusCode::OK,
                &KillProcessResult {
                    r#ref: r,
                    pid,
                    label,
                    processes: members.len() as i64,
                    memory: members.iter().map(|m| m.memory).sum(),
                    forced: 0,
                    left: 0,
                },
            )
        }
        _ => return Err(Box::new(req)),
    };
    Ok(res)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn agent(state: &str, chat: &str) -> FleetAgent {
        FleetAgent {
            r#ref: format!("demo/agent-{state}-{chat}"),
            project: "demo".into(),
            name: "agent-1".into(),
            state: state.into(),
            chat: chat.into(),
            ..Default::default()
        }
    }

    #[test]
    fn a_running_agent_has_its_tool_the_machine_and_its_own_biggest_first() {
        let p = of(&agent("running", "running"), 0);
        assert!(p.running);
        let kinds: Vec<&str> = p.trees.iter().map(|t| t.kind.as_str()).collect();
        assert!(kinds.contains(&PROCESS_TREE_TOOL) && kinds.contains(&PROCESS_TREE_SYSTEM));
        assert!(p.trees.windows(2).all(|w| w[0].memory >= w[1].memory));
        assert_eq!(p.memory, p.trees.iter().map(|t| t.memory).sum::<i64>());
        assert!(p.killable < p.memory);
        // The build is heavy, the dev server idle, the tool neither.
        let build = p
            .trees
            .iter()
            .find(|t| t.label.starts_with("cargo"))
            .unwrap();
        assert!(build.heavy && !build.idle && !build.protected);
        let dev = p.trees.iter().find(|t| t.label == "npm run dev").unwrap();
        assert!(dev.idle && !dev.heavy);
        let tool = p
            .trees
            .iter()
            .find(|t| t.kind == PROCESS_TREE_TOOL)
            .unwrap();
        assert!(tool.protected && !tool.heavy && !tool.idle);
    }

    #[test]
    fn a_stopped_agent_runs_nothing() {
        let p = of(&agent("stopped", ""), 0);
        assert!(!p.running && p.trees.is_empty() && p.memory == 0);
    }

    #[test]
    fn a_killed_tree_is_gone() {
        let a = agent("running", "ready");
        KILLED
            .lock()
            .unwrap()
            .get_or_insert_with(Default::default)
            .entry(a.r#ref.clone())
            .or_default()
            .insert(4180);
        let p = of(&a, 0);
        assert!(p.trees.iter().all(|t| t.pid != 4180));
        assert!(p.trees.iter().any(|t| t.label.starts_with("cargo")));
    }
}
