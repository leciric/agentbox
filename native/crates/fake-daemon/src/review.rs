//! Work and review fixtures (internal/daemon/work.go, review.go, media.go):
//! the fake project's agents' work, live and past, what waits for review, and
//! the proof they saved — real files, served as GET /v1/media/{id}/file does.
//!
//! The files are in --media (default ~/abx/fake-media): `settings-before.png`,
//! `settings-after.png` and `home.png` (screenshots of the display, `ffmpeg -f
//! x11grab -frames:v 1`) and `walkthrough.mp4` (a 9 s x11grab recording, H.264
//! High, yuv420p, +faststart, as `agentbox media record` saves them). Missing
//! ones are made from FFmpeg's test source on start.
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::time::{Duration, SystemTime};

use agentbox_api::*;

/// The proof's files: (file, kind, mime).
const FILES: &[(&str, &str, &str)] = &[
    ("settings-before.png", "screenshot", "image/png"),
    ("settings-after.png", "screenshot", "image/png"),
    ("home.png", "screenshot", "image/png"),
    ("walkthrough.mp4", "recording", "video/mp4"),
];

/// The recording's length, in seconds.
const RECORDING: f64 = 9.0;

fn mins(m: u64) -> Duration {
    Duration::from_secs(m * 60)
}

/// make_files puts in dir any of the files that isn't there, from FFmpeg's
/// test source, so the fixtures always have something real to serve.
pub fn make_files(dir: &Path) {
    let _ = std::fs::create_dir_all(dir);
    for (file, kind, _) in FILES {
        let path = dir.join(file);
        if path.exists() {
            continue;
        }
        let mut cmd = std::process::Command::new("ffmpeg");
        cmd.args(["-loglevel", "error", "-f", "lavfi", "-i"]);
        if *kind == "recording" {
            cmd.args([
                &format!("testsrc2=size=1440x900:rate=15:duration={RECORDING}"),
                "-c:v",
                "libx264",
                "-pix_fmt",
                "yuv420p",
                "-movflags",
                "+faststart",
            ]);
        } else {
            cmd.args(["testsrc2=size=1440x900", "-frames:v", "1"]);
        }
        let ok = cmd.arg("-y").arg(&path).status().is_ok_and(|s| s.success());
        eprintln!("made {} from the test source: {ok}", path.display());
    }
}

/// One agent's work as the fixtures have it.
struct Fixture {
    agent: &'static str,
    title: &'static str,
    task: &'static str,
    summary: &'static str,
    status: &'static str,
    verdict: &'static str,
    past: bool,
    pr: Option<(i64, &'static str, &'static str)>,
    changes: (i64, i64, i64),
    /// How long ago it finished (or started, still at work).
    ago: u64,
    report: Option<(&'static str, &'static str, &'static [&'static str])>,
    media: &'static [&'static str],
}

const FIXTURES: &[Fixture] = &[
    Fixture {
        agent: "agent-01",
        title: "Native app (Rust + GPUI)",
        task: "Build the native app's Work page and review",
        summary: "",
        status: "working",
        verdict: "",
        past: false,
        pr: None,
        changes: (6, 812, 40),
        ago: 12,
        report: None,
        media: &[],
    },
    Fixture {
        agent: "agent-02",
        title: "Fix Windows relay reconnect",
        task: "The phone loses the relay when Windows sleeps: reconnect it",
        summary: "The relay reconnects after sleep now: it watches the WSL distro's IP and redials when it changes.\n\nI recorded the settings walkthrough and took a before/after of the page it changes.",
        status: "review",
        verdict: "",
        past: false,
        pr: Some((712, "open", "success")),
        changes: (4, 128, 31),
        ago: 25,
        report: Some((
            "done",
            "The relay redials when the distro's address changes, and the phone reconnects within 5 s of the machine waking. Tested with a stand-in daemon and a real sleep cycle.",
            &[
                "Untested on Windows 10 (only 11)",
                "The relay still needs the app open",
            ],
        )),
        media: &["pair", "recording", "home"],
    },
    Fixture {
        agent: "agent-04",
        title: "Doctor: don't flag gh before first chat",
        task: "The doctor flags gh as not installed before the first chat",
        summary: "The doctor waits for the first chat before it checks Claude Code and gh.",
        status: "review",
        verdict: "",
        past: false,
        pr: None,
        changes: (2, 34, 12),
        ago: 70,
        report: Some((
            "partial",
            "The doctor no longer flags them, but the setup page still shows a stale warning until it is reopened.",
            &["Setup's warning is stale until reopened"],
        )),
        media: &["home"],
    },
    Fixture {
        agent: "agent-05",
        title: "Phone pairing on Mac (Lima)",
        task: "Pair the phone with AgentBox on a Mac",
        summary: "Phone pairing works on a Mac: the port is forwarded from the Lima VM.",
        status: "reviewed",
        verdict: "reviewed",
        past: false,
        pr: Some((708, "open", "pending")),
        changes: (5, 210, 18),
        ago: 60 * 26,
        report: Some(("done", "Pairing works through Lima's port forward.", &[])),
        media: &["pair"],
    },
    Fixture {
        agent: "agent-09",
        title: "Token ledger per subagent",
        task: "Count tokens per subagent in the ledger",
        summary: "Each subagent's tokens are a row of their own in the ledger.",
        status: "done",
        verdict: "",
        past: true,
        pr: Some((699, "merged", "success")),
        changes: (7, 340, 95),
        ago: 60 * 30,
        report: Some(("done", "Per-subagent rows land in token_usage.", &[])),
        media: &["recording"],
    },
];

/// The fixtures' state: verdicts given and pull requests merged here.
#[derive(Default)]
pub struct Review {
    verdicts: HashMap<String, String>,
    merged: Vec<i64>,
    media_dir: PathBuf,
    /// When the fixtures were made, so their times stay put.
    epoch: Option<SystemTime>,
}

impl Review {
    pub fn new(media_dir: PathBuf) -> Self {
        make_files(&media_dir);
        Self {
            media_dir,
            epoch: Some(SystemTime::now()),
            ..Default::default()
        }
    }

    fn ago(&self, d: Duration) -> String {
        let base = self.epoch.unwrap_or_else(SystemTime::now);
        humantime::format_rfc3339_millis(base - d).to_string()
    }

    fn fingerprint(f: &Fixture) -> String {
        format!(
            "{{\"head\":\"{:x}\"}}",
            f.agent.len() * 7919 + f.ago as usize
        )
    }

    /// item is an agent's review item, as review.go's reviewItems makes it.
    fn item(&self, project: &str, f: &Fixture) -> ReviewItem {
        let r#ref = format!("{project}/{}", f.agent);
        let verdict = self
            .verdicts
            .get(&r#ref)
            .cloned()
            .unwrap_or(f.verdict.into());
        ReviewItem {
            project: project.into(),
            agent: f.agent.into(),
            r#ref: r#ref.clone(),
            title: f.title.into(),
            branch: format!(
                "agentbox/{}",
                f.title
                    .to_lowercase()
                    .replace([' ', ':', '(', ')', '\'', '+'], "-")
            ),
            ai: "claude".into(),
            summary: f.summary.into(),
            changes: Some(AgentChanges {
                files: f.changes.0,
                insertions: f.changes.1,
                deletions: f.changes.2,
                dirty: false,
            }),
            pr: f.pr.map(|(number, state, checks)| PullRequest {
                number,
                title: f.title.into(),
                state: if self.merged.contains(&number) {
                    "merged".into()
                } else {
                    state.into()
                },
                checks: checks.into(),
                url: format!("https://github.com/leciric/agentbox/pull/{number}"),
                base_branch: "main".into(),
                head_branch: r#ref.clone(),
                additions: f.changes.1,
                deletions: f.changes.2,
                ..Default::default()
            }),
            finished_at: if f.status == "working" {
                String::new()
            } else {
                self.ago(mins(f.ago))
            },
            fingerprint: if f.status == "working" {
                String::new()
            } else {
                Self::fingerprint(f)
            },
            reviewed_at: (!verdict.is_empty()).then(|| self.ago(mins(f.ago.saturating_sub(5)))),
            verdict,
            ..Default::default()
        }
    }

    /// status is a work item's status (api.Work*) now, verdicts given here included.
    fn status(&self, project: &str, f: &Fixture) -> String {
        match self
            .verdicts
            .get(&format!("{project}/{}", f.agent))
            .map(String::as_str)
        {
            Some("changes") => "working".into(),
            Some(_) => "reviewed".into(),
            None if f.pr.is_some_and(|(n, ..)| self.merged.contains(&n)) => "reviewed".into(),
            None => f.status.into(),
        }
    }

    fn work_item(&self, project: &str, f: &Fixture) -> WorkItem {
        let media = self.media(project, f.agent);
        WorkItem {
            item: self.item(project, f),
            task: f.task.into(),
            past: f.past,
            status: self.status(project, f),
            report: f.report.map(|(status, summary, open)| AgentReport {
                id: format!("r-{}", f.agent),
                project: project.into(),
                agent: f.agent.into(),
                created_at: self.ago(mins(f.ago)),
                task: f.task.into(),
                status: status.into(),
                summary: summary.into(),
                remaining_issues: open.iter().map(|s| s.to_string()).collect(),
                ..Default::default()
            }),
            screenshots: media.iter().filter(|m| m.kind == "screenshot").count() as i64,
            recordings: media.iter().filter(|m| m.kind == "recording").count() as i64,
            at: self.ago(mins(f.ago)),
        }
    }

    /// work is GET /v1/work (work.go's writeWork): ?filter= and ?q= narrow
    /// it, counts are per filter over what q matches; one page.
    pub fn work(&self, project: &str, query: &HashMap<String, String>) -> WorkPage {
        let filter = query.get("filter").map(String::as_str).unwrap_or("");
        let q = query.get("q").map(|s| s.to_lowercase()).unwrap_or_default();
        let mut page = WorkPage::default();
        for f in FIXTURES {
            let w = self.work_item(project, f);
            let text = format!("{} {} {}", f.title, f.task, w.item.branch).to_lowercase();
            if !q.is_empty() && !text.contains(&q) {
                continue;
            }
            let pr = w.item.pr.as_ref();
            let in_filter = |f: &str| match f {
                "" => true,
                "review" => w.status == "review",
                "none" => pr.is_none(),
                s => pr.is_some_and(|p| p.state == s),
            };
            for f in ["", "review", "open", "merged", "closed", "none"] {
                if in_filter(f) {
                    *page.counts.entry(f.into()).or_default() += 1;
                }
            }
            if in_filter(filter) {
                page.items.push(w);
            }
        }
        page.total = page.items.len() as i64;
        page
    }

    /// review is GET /v1/review: what waits for review, or with ?reviewed=1
    /// what was reviewed.
    pub fn review(&self, project: &str, reviewed: bool) -> ReviewPage {
        let items: Vec<ReviewItem> = FIXTURES
            .iter()
            .filter(|f| !f.past)
            .filter(|f| {
                let s = self.status(project, f);
                if reviewed {
                    s == "reviewed"
                } else {
                    s == "review"
                }
            })
            .map(|f| self.item(project, f))
            .collect();
        ReviewPage {
            total: items.len() as i64,
            items,
            ..Default::default()
        }
    }

    /// media is an agent's media (GET /v1/agents/{p}/{a}/media), with its
    /// times placed around its recording, as `agentbox media` saves them.
    pub fn media(&self, _project: &str, agent: &str) -> Vec<MediaItem> {
        let Some(f) = FIXTURES.iter().find(|f| f.agent == agent) else {
            return Vec::new();
        };
        // The recording started this long before the agent finished.
        let start = mins(f.ago) + Duration::from_secs(120);
        let during = |s: f64| start - Duration::from_secs_f64(s);
        let file = |id: &str, file: &str, created: Duration, pair: &str, side: &str| {
            let (_, kind, mime) = FILES.iter().find(|(f, ..)| *f == file).unwrap();
            let size = std::fs::metadata(self.media_dir.join(file)).map_or(0, |m| m.len());
            MediaItem {
                id: format!("{agent}-{id}"),
                agent: agent.into(),
                agent_title: f.title.into(),
                agent_gone: f.past,
                kind: kind.to_string(),
                name: id.into(),
                file: file.into(),
                mime: mime.to_string(),
                size: size as i64,
                source: "agent".into(),
                meta: MediaMeta {
                    width: 1440,
                    height: 900,
                    duration: if *kind == "recording" { RECORDING } else { 0. },
                    target: "display".into(),
                    pair: pair.into(),
                    side: side.into(),
                    ..Default::default()
                },
                created_at: self.ago(created),
                ..Default::default()
            }
        };
        let note = |id: &str, name: &str, text: &str, created: Duration| MediaItem {
            id: format!("{agent}-{id}"),
            agent: agent.into(),
            agent_title: f.title.into(),
            kind: "note".into(),
            name: name.into(),
            text: text.into(),
            source: "agent".into(),
            created_at: self.ago(created),
            ..Default::default()
        };
        let mut out = Vec::new();
        for m in f.media {
            match *m {
                "pair" => {
                    out.push(file(
                        "settings-before",
                        "settings-before.png",
                        start + Duration::from_secs(30),
                        "settings",
                        "before",
                    ));
                    out.push(file(
                        "settings-after",
                        "settings-after.png",
                        during(RECORDING) - Duration::from_secs(20),
                        "settings",
                        "after",
                    ));
                }
                "recording" => {
                    out.push(file("walkthrough", "walkthrough.mp4", start, "", ""));
                    out.push(note(
                        "note-1",
                        "Opens General",
                        "Opens General\nThe appearance and language settings show.",
                        during(1.5),
                    ));
                    out.push(note("note-2", "Notifications and Phone", "", during(3.0)));
                    out.push(note(
                        "note-3",
                        "Back to Accounts, then General",
                        "",
                        during(6.0),
                    ));
                }
                "home" => out.push(file(
                    "home",
                    "home.png",
                    during(RECORDING) - Duration::from_secs(40),
                    "",
                    "",
                )),
                _ => {}
            }
        }
        out
    }

    /// file is a media item's file and its type, by id (GET /v1/media/{id}/file):
    /// an id is the agent's name and the file's stem.
    pub fn file(&self, id: &str) -> Option<(Vec<u8>, String)> {
        let (file, _, mime) = FILES.iter().find(|(f, ..)| {
            let stem = f.rsplit_once('.').map_or(*f, |(s, _)| s);
            id.ends_with(&format!("-{stem}"))
        })?;
        Some((
            std::fs::read(self.media_dir.join(file)).ok()?,
            mime.to_string(),
        ))
    }

    pub fn is_agent(agent: &str) -> bool {
        FIXTURES.iter().any(|f| f.agent == agent)
    }

    /// verdict records POST .../review (reviewed or dismissed) or
    /// .../request-changes ("changes").
    pub fn verdict(&mut self, r#ref: &str, verdict: &str) {
        self.verdicts.insert(r#ref.into(), verdict.into());
    }

    /// merge merges a pull request (POST .../pulls/{n}/merge), and answers it.
    pub fn merge(&mut self, project: &str, number: i64) -> Option<PullRequest> {
        let f = FIXTURES
            .iter()
            .find(|f| f.pr.is_some_and(|(n, ..)| n == number))?;
        self.merged.push(number);
        self.item(project, f).pr
    }

    /// diff is an agent's diff against its base (GET .../diff), as git prints it.
    pub fn diff(&self, agent: &str) -> String {
        let f = FIXTURES.iter().find(|f| f.agent == agent);
        let title = f.map_or("", |f| f.title);
        format!(
            "diff --git a/internal/hostwsl/relay.go b/internal/hostwsl/relay.go\n\
index 3f2a1c4..9b8e7d2 100644\n\
--- a/internal/hostwsl/relay.go\n\
+++ b/internal/hostwsl/relay.go\n\
@@ -41,12 +41,19 @@ func (r *Relay) Run(ctx context.Context) error {{\n \tfor {{\n-\t\tconn, err := r.dial(ctx)\n-\t\tif err != nil {{\n-\t\t\treturn err\n-\t\t}}\n+\t\t// {title}: the distro's address changes when Windows sleeps,\n+\t\t// so a failed dial waits for the next one and tries again.\n+\t\tconn, err := r.dial(ctx)\n+\t\tif err != nil {{\n+\t\t\tif !r.waitAddress(ctx) {{\n+\t\t\t\treturn err\n+\t\t\t}}\n+\t\t\tcontinue\n+\t\t}}\n \t\tr.serve(ctx, conn)\n \t}}\n }}\n\
diff --git a/internal/hostwsl/relay_test.go b/internal/hostwsl/relay_test.go\n\
index 1a2b3c4..5d6e7f8 100644\n\
--- a/internal/hostwsl/relay_test.go\n\
+++ b/internal/hostwsl/relay_test.go\n\
@@ -88,6 +88,14 @@ func TestRelayServes(t *testing.T) {{\n \trelay.Close()\n }}\n+\n+func TestRelayRedialsWhenTheAddressChanges(t *testing.T) {{\n+\tr := newTestRelay(t)\n+\tr.setAddress(\"172.20.0.2\")\n+\tif !r.waitAddress(context.Background()) {{\n+\t\tt.Fatal(\"it gave up\")\n+\t}}\n+}}\n"
        )
    }
}
