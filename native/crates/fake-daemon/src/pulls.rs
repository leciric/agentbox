//! Pull request fixtures (internal/daemon/pulls.go, pulldetail.go,
//! mergewhengreen.go): the fake project's repository on GitHub, which the
//! real daemon here can't read. Its list, each one's detail, checks, files and
//! diffs, the repository's labels, and merges and label edits kept in memory,
//! answered as the Go handlers write them.
use std::collections::HashMap;
use std::time::{Duration, SystemTime};

use agentbox_api::*;

const REPO: &str = "leciric/agentbox";

/// The repository's labels (GET /v1/projects/{p}/labels).
const LABELS: &[(&str, &str, &str)] = &[
    ("bug", "d73a4a", "Something isn't working"),
    ("enhancement", "a2eeef", "New feature or request"),
    (
        "documentation",
        "0075ca",
        "Improvements or additions to documentation",
    ),
    ("windows", "1d76db", "Only on Windows (WSL front end)"),
    ("mac", "5319e7", "Only on a Mac (Lima)"),
    ("native", "fbca04", "The Rust + GPUI app"),
    ("ui", "c5def5", "What the user sees"),
    (
        "needs-hardware",
        "e99695",
        "Can't be checked without real hardware",
    ),
    ("good first issue", "7057ff", "Good for newcomers"),
];

/// One pull request as the fixtures have it.
struct Fixture {
    number: i64,
    title: &'static str,
    state: &'static str,
    draft: bool,
    checks: &'static str,
    head: &'static str,
    base: &'static str,
    author: &'static str,
    agent: &'static str,
    conflict: bool,
    review: &'static str,
    labels: &'static [&'static str],
    comments: i64,
    /// Minutes since it was last updated.
    ago: u64,
    body: &'static str,
    files: fn() -> Vec<File>,
}

/// A file a pull request changes, with its patch as GitHub sends it.
struct File {
    path: &'static str,
    previous: &'static str,
    status: &'static str,
    patch: String,
    /// Changed lines for a file GitHub sends no patch for.
    lines: (i64, i64),
}

const FIXTURES: &[Fixture] = &[
    Fixture {
        number: 716,
        title: "Relay: back off between redials",
        state: "open",
        draft: false,
        checks: "pending",
        head: "agentbox/relay-backoff",
        base: "agentbox/fix-windows-relay-reconnect",
        author: "leciric",
        agent: "agent-06",
        conflict: false,
        review: "review_required",
        labels: &["windows"],
        comments: 0,
        ago: 12,
        body: "Builds on #712: a relay that can't reach the distro waits 1 s, 2 s, 4 s … up to 30 s between dials instead of spinning.\n\n- `Relay.backoff` doubles per failure and resets on a served connection\n- a test with a fake clock\n",
        files: relay_backoff_files,
    },
    Fixture {
        number: 715,
        title: "Native app (Rust + GPUI)",
        state: "open",
        draft: true,
        checks: "pending",
        head: "agentbox/feat-native-gpui-app",
        base: "main",
        author: "leciric",
        agent: "agent-01",
        conflict: false,
        review: "",
        labels: &["native", "enhancement"],
        comments: 3,
        ago: 8,
        body: "## What\n\nA native desktop app in `native/`, built on GPUI (Zed's UI framework) and gpui-component, at parity with the Electron app's v2 layout.\n\n## Why\n\nThe Electron app takes 400 MB to show a chat. This one starts in under a second and stays under 120 MB.\n\n## Checked\n\n- `cargo test -p agentbox-native`\n- the review, Work and Settings against the fake daemon\n\n> Still a draft: pull requests and the files view are next.\n",
        files: native_files,
    },
    Fixture {
        number: 714,
        title: "Settings: the Dracula look",
        state: "open",
        draft: false,
        checks: "failing",
        head: "agentbox/settings-dracula-look",
        base: "main",
        author: "leciric",
        agent: "agent-03",
        conflict: true,
        review: "changes_requested",
        labels: &["ui"],
        comments: 5,
        ago: 95,
        body: "Adds Dracula to Settings › General › Look.\n\nThe lint job fails on an unused import; the branch also conflicts with `main` since #709 moved the palette.\n",
        files: dracula_files,
    },
    Fixture {
        number: 713,
        title: "docs: upgrading from 0.16 deletes the old Tasks",
        state: "open",
        draft: false,
        checks: "passing",
        head: "docs/upgrade-tasks-note",
        base: "main",
        author: "marta-dev",
        agent: "",
        conflict: false,
        review: "approved",
        labels: &["documentation"],
        comments: 1,
        ago: 60 * 5,
        body: "A note in `docs/install.md`: the 0.16 → 0.17 upgrade deletes the old Tasks, and how to keep a copy first.\n",
        files: docs_files,
    },
    Fixture {
        number: 712,
        title: "Fix Windows relay reconnect",
        state: "open",
        draft: false,
        checks: "passing",
        head: "agentbox/fix-windows-relay-reconnect",
        base: "main",
        author: "leciric",
        agent: "agent-02",
        conflict: false,
        review: "",
        labels: &["bug", "windows", "needs-hardware"],
        comments: 2,
        ago: 25,
        body: "## What\n\nThe WSL relay redials when the distro's address changes, so the phone reconnects within 5 s of the machine waking.\n\n## How it was checked\n\n- `go test ./internal/hostwsl/...`\n- a stand-in daemon and a real sleep cycle on Windows 11\n\n| | before | after |\n|---|---|---|\n| reconnect after sleep | never | 4.2 s |\n\nUntested on Windows 10.\n",
        files: relay_files,
    },
    Fixture {
        number: 708,
        title: "Phone pairing on Mac (Lima)",
        state: "open",
        draft: false,
        checks: "pending",
        head: "agentbox/phone-pairing-on-mac--lima-",
        base: "main",
        author: "leciric",
        agent: "agent-05",
        conflict: false,
        review: "",
        labels: &["mac"],
        comments: 0,
        ago: 60 * 26,
        body: "Forwards the phone port out of the Lima VM, so pairing works on a Mac.\n",
        files: lima_files,
    },
    Fixture {
        number: 702,
        title: "Experiment: drop the v1 layout early",
        state: "closed",
        draft: false,
        checks: "failing",
        head: "exp/drop-v1",
        base: "main",
        author: "marta-dev",
        agent: "",
        conflict: false,
        review: "",
        labels: &["ui"],
        comments: 4,
        ago: 60 * 50,
        body: "Superseded by #636.\n",
        files: docs_files,
    },
    Fixture {
        number: 699,
        title: "Token ledger per subagent",
        state: "merged",
        draft: false,
        checks: "passing",
        head: "agentbox/token-ledger-per-subagent",
        base: "main",
        author: "leciric",
        agent: "agent-09",
        conflict: false,
        review: "approved",
        labels: &["enhancement"],
        comments: 1,
        ago: 60 * 30,
        body: "Each subagent's tokens are a row of their own in `token_usage`.\n",
        files: ledger_files,
    },
];

fn file(path: &'static str, status: &'static str, patch: &str) -> File {
    File {
        path,
        previous: "",
        status,
        patch: patch.to_string(),
        lines: (0, 0),
    }
}

fn relay_files() -> Vec<File> {
    vec![
        file(
            "internal/hostwsl/relay.go",
            "modified",
            "@@ -12,6 +12,7 @@ import (\n \t\"context\"\n \t\"net\"\n \t\"time\"\n+\t\"sync\"\n \n \t\"github.com/leciric/agentbox/internal/hostos\"\n )\n@@ -41,12 +42,19 @@ func (r *Relay) Run(ctx context.Context) error {\n \tfor {\n-\t\tconn, err := r.dial(ctx)\n-\t\tif err != nil {\n-\t\t\treturn err\n-\t\t}\n+\t\t// The distro's address changes when Windows sleeps, so a failed\n+\t\t// dial waits for the next one and tries again.\n+\t\tconn, err := r.dial(ctx)\n+\t\tif err != nil {\n+\t\t\tif !r.waitAddress(ctx) {\n+\t\t\t\treturn err\n+\t\t\t}\n+\t\t\tcontinue\n+\t\t}\n \t\tr.serve(ctx, conn)\n \t}\n }\n@@ -88,3 +96,21 @@ func (r *Relay) dial(ctx context.Context) (net.Conn, error) {\n \td := net.Dialer{Timeout: 3 * time.Second}\n \treturn d.DialContext(ctx, \"tcp\", r.address())\n }\n+\n+// waitAddress waits for the distro's address to change, polling every\n+// second, and says whether it did before ctx ended.\n+func (r *Relay) waitAddress(ctx context.Context) bool {\n+\twas := r.address()\n+\tt := time.NewTicker(time.Second)\n+\tdefer t.Stop()\n+\tfor {\n+\t\tselect {\n+\t\tcase <-ctx.Done():\n+\t\t\treturn false\n+\t\tcase <-t.C:\n+\t\t\tif r.address() != was {\n+\t\t\t\treturn true\n+\t\t\t}\n+\t\t}\n+\t}\n+}",
        ),
        file(
            "internal/hostwsl/relay_test.go",
            "modified",
            "@@ -88,6 +88,14 @@ func TestRelayServes(t *testing.T) {\n \trelay.Close()\n }\n+\n+func TestRelayRedialsWhenTheAddressChanges(t *testing.T) {\n+\tr := newTestRelay(t)\n+\tr.setAddress(\"172.20.0.2\")\n+\tif !r.waitAddress(context.Background()) {\n+\t\tt.Fatal(\"it gave up\")\n+\t}\n+}",
        ),
        file(
            "docs/windows.md",
            "modified",
            "@@ -30,7 +30,8 @@ The phone reaches AgentBox through a relay in the app.\n \n ## When it stops\n \n-After Windows sleeps, pair the phone again.\n+After Windows sleeps the relay finds the distro's new address by itself;\n+the phone reconnects within a few seconds.\n \n The relay runs only while the app is open.\n \n\\ No newline at end of file",
        ),
        File {
            path: "docs/img/relay-after.png",
            previous: "",
            status: "added",
            patch: String::new(),
            lines: (0, 0),
        },
    ]
}

fn relay_backoff_files() -> Vec<File> {
    vec![file(
        "internal/hostwsl/relay.go",
        "modified",
        "@@ -46,6 +46,8 @@ func (r *Relay) Run(ctx context.Context) error {\n \t\tconn, err := r.dial(ctx)\n \t\tif err != nil {\n+\t\t\ttime.Sleep(r.backoff.next())\n+\t\t\tr.logf(\"relay: dial failed, retrying in %s\", r.backoff.wait)\n \t\t\tif !r.waitAddress(ctx) {\n \t\t\t\treturn err\n \t\t\t}\n@@ -53,6 +55,7 @@ func (r *Relay) Run(ctx context.Context) error {\n \t\t}\n+\t\tr.backoff.reset()\n \t\tr.serve(ctx, conn)\n \t}\n }",
    )]
}

fn native_files() -> Vec<File> {
    let mut lock = String::from(
        "@@ -1,6 +1,9 @@\n # This file is automatically @generated by Cargo.\n # It is not intended for manual editing.\n version = 4\n+\n+[[package]]\n+name = \"agentbox-native\"\n",
    );
    for i in 0..520 {
        lock.push_str(&format!(
            "+\n+[[package]]\n+name = \"crate-{i}\"\n+version = \"0.{}.{}\"\n",
            i % 9,
            i % 17
        ));
    }
    let mut rail = String::from(
        "@@ -1,4 +1,130 @@\n //! The window: the v2 layout's sidebar (projects), main (the open chat) and\n //! rail (the project's lead and agents, with their live state).\n+use std::time::Duration;\n+\n",
    );
    for i in 0..120 {
        rail.push_str(&format!(
            "+    fn step_{i}(&mut self, cx: &mut Context<Self>) {{\n+        self.at = {i};\n+        cx.notify();\n+    }}\n"
        ));
    }
    vec![
        file(
            "native/crates/app/src/app/pulls.rs",
            "added",
            "@@ -0,0 +1,24 @@\n+//! Pull requests (PullRequestsPanel.tsx, PullRequestModal.tsx): the\n+//! project's, with their checks, labels and files, and Merge.\n+use agentbox_api::{ProjectPullRequests, PullRequest};\n+use agentbox_i18n::{t, tv};\n+use gpui::*;\n+\n+/// Filter is which of them the list shows (lib/pulls.ts PullsState).\n+#[derive(Clone, Copy, PartialEq)]\n+pub enum Filter {\n+    Open,\n+    Closed,\n+    All,\n+}\n+\n+impl Filter {\n+    /// keeps says whether a pull request in state shows.\n+    pub fn keeps(self, state: &str) -> bool {\n+        match self {\n+            Filter::Open => state == \"open\",\n+            Filter::Closed => state != \"open\",\n+            Filter::All => true,\n+        }\n+    }\n+}",
        ),
        file("native/crates/app/src/app.rs", "modified", &rail),
        File {
            path: "native/crates/app/src/rail.rs",
            previous: "native/crates/app/src/side.rs",
            status: "renamed",
            patch: String::new(),
            lines: (0, 0),
        },
        file("native/Cargo.lock", "modified", &lock),
        file(
            "desktop/src/shared/api.ts",
            "modified",
            "@@ -2180,6 +2180,7 @@ export interface PullRequestFiles {\n   files: PullFile[];\n   truncated?: boolean;\n+  // generated from Go: internal/api/types.go\n }",
        ),
        File {
            path: "native/assets/icon.png",
            previous: "",
            status: "modified",
            patch: String::new(),
            lines: (0, 0),
        },
    ]
}

fn dracula_files() -> Vec<File> {
    vec![
        file(
            "native/crates/app/src/theme.rs",
            "modified",
            "@@ -8,9 +8,11 @@ use gpui::*;\n use std::sync::atomic::{AtomicU8, Ordering};\n+use std::collections::HashMap;\n \n pub enum Look {\n     Dark,\n     Light,\n+    Dracula,\n }\n@@ -74,7 +76,12 @@ pub fn bg() -> Hsla {\n-    if light() { rgb(0xffffff) } else { rgb(0x0b0b0f) }.into()\n+    match look() {\n+        Look::Light => rgb(0xffffff),\n+        Look::Dracula => rgb(0x282a36),\n+        Look::Dark => rgb(0x0b0b0f),\n+    }\n+    .into()\n }",
        ),
        file(
            "desktop/src/shared/i18n/en-US.ts",
            "modified",
            "@@ -940,6 +940,7 @@ export const enUS = {\n   'settings.look.dark': 'Dark',\n   'settings.look.light': 'Light',\n+  'settings.look.dracula': 'Dracula',\n   'settings.look.system': 'Match the system',",
        ),
    ]
}

fn docs_files() -> Vec<File> {
    vec![file(
        "docs/install.md",
        "modified",
        "@@ -52,6 +52,14 @@ agentbox vm init\n \n ## Upgrading\n \n+### From 0.16 to 0.17\n+\n+0.17 deletes the Tasks 0.16 kept: their work is in the agents' pull\n+requests and reports, which stay. To keep a copy of the list, run\n+`agentbox tasks export > tasks.json` before upgrading.\n+\n+### Any other version\n+\n The app updates itself; the CLI with `agentbox update`.",
    )]
}

fn lima_files() -> Vec<File> {
    vec![
        file(
            "internal/hostvm/lima.go",
            "modified",
            "@@ -120,6 +120,10 @@ func limaConfig(c Config) limaYAML {\n \t\tPortForwards: []portForward{\n \t\t\t{GuestSocket: c.Socket, HostSocket: c.Socket},\n+\t\t\t// The phone's port, so pairing reaches the daemon from the\n+\t\t\t// network the Mac is on.\n+\t\t\t{GuestPort: c.PhonePort, HostPort: c.PhonePort, HostIP: \"0.0.0.0\"},\n \t\t},\n \t}",
        ),
        File {
            path: "internal/hostvm/lima_phone.go",
            previous: "",
            status: "added",
            patch: "@@ -0,0 +1,9 @@\n+package hostvm\n+\n+// PhonePort is where the phone app reaches the daemon.\n+const PhonePort = 7778\n+\n+// phoneForwarded says whether the VM forwards it already.\n+func phoneForwarded(y limaYAML) bool {\n+\treturn y.has(PhonePort)\n+}".into(),
            lines: (0, 0),
        },
    ]
}

fn ledger_files() -> Vec<File> {
    vec![file(
        "internal/chat/tokens.go",
        "modified",
        "@@ -30,8 +30,9 @@ type usageRow struct {\n \tProject string\n \tAgent   string\n+\tSubagent string\n \tModel   string\n-\tInput   int64\n+\tInput    int64\n \tOutput  int64\n }",
    )]
}

/// The fixtures' state: merges, label edits and merge-when-green marks made here.
pub struct Pulls {
    epoch: SystemTime,
    merged: Vec<i64>,
    labels: HashMap<i64, Vec<String>>,
    marks: HashMap<i64, MergeWhenGreen>,
}

impl Pulls {
    pub fn new() -> Self {
        Self {
            epoch: SystemTime::now(),
            merged: Vec::new(),
            labels: HashMap::new(),
            marks: HashMap::new(),
        }
    }

    fn ago(&self, mins: u64) -> String {
        humantime::format_rfc3339_millis(self.epoch - Duration::from_secs(mins * 60)).to_string()
    }

    fn fixture(number: i64) -> Option<&'static Fixture> {
        FIXTURES.iter().find(|f| f.number == number)
    }

    fn labels_of(&self, f: &Fixture) -> Vec<Label> {
        let names: Vec<String> = self
            .labels
            .get(&f.number)
            .cloned()
            .unwrap_or_else(|| f.labels.iter().map(|s| s.to_string()).collect());
        names.iter().map(|n| label(n)).collect()
    }

    fn files(f: &Fixture) -> Vec<(PullFile, String)> {
        (f.files)()
            .into_iter()
            .map(|file| {
                let (mut add, mut del) = file.lines;
                for l in file.patch.lines() {
                    if l.starts_with('+') {
                        add += 1;
                    } else if l.starts_with('-') {
                        del += 1;
                    }
                }
                if file.patch.is_empty() && file.path.ends_with("icon.png") {
                    (add, del) = (0, 0);
                }
                (
                    PullFile {
                        path: file.path.into(),
                        previous_path: file.previous.into(),
                        status: file.status.into(),
                        additions: add,
                        deletions: del,
                        has_diff: !file.patch.is_empty(),
                    },
                    file.patch,
                )
            })
            .collect()
    }

    /// pr is one pull request as the list has it (api.PullRequest).
    pub fn pr(&self, number: i64) -> Option<PullRequest> {
        let f = Self::fixture(number)?;
        let files = Self::files(f);
        Some(PullRequest {
            number: f.number,
            title: f.title.into(),
            state: if self.merged.contains(&f.number) {
                "merged".into()
            } else {
                f.state.into()
            },
            checks: f.checks.into(),
            url: format!("https://github.com/{REPO}/pull/{}", f.number),
            draft: f.draft,
            additions: files.iter().map(|(p, _)| p.additions).sum(),
            deletions: files.iter().map(|(p, _)| p.deletions).sum(),
            comments: f.comments,
            updated_at: Some(self.ago(f.ago)),
            base_branch: f.base.into(),
            head_branch: f.head.into(),
            head_sha: format!("{:040x}", f.number as u128 * 0x9e3779b97f4a7c15),
            author: f.author.into(),
            agent: f.agent.into(),
            conflict: f.conflict,
            review: f.review.into(),
            watched: true,
            labels: self.labels_of(f),
            ..Default::default()
        })
    }

    /// list is GET /v1/projects/{p}/pulls (pulls.go's projectPullRequests).
    pub fn list(&self, project: &str) -> ProjectPullRequests {
        ProjectPullRequests {
            project: project.into(),
            github: REPO.into(),
            github_account: "default".into(),
            github_login: "leciric".into(),
            pull_requests: FIXTURES.iter().filter_map(|f| self.pr(f.number)).collect(),
            can_merge: true,
            can_merge_known: true,
            merge_methods: vec!["squash".into(), "merge".into(), "rebase".into()],
            fetched_at: Some(self.ago(1)),
            ..Default::default()
        }
    }

    /// detail is GET .../pulls/{n} (pulldetail.go's pullRequestDetail).
    pub fn detail(&self, number: i64) -> Option<PullRequestDetail> {
        let f = Self::fixture(number)?;
        let pr = self.pr(number)?;
        let runs: &[(&str, &str, &str)] = match f.checks {
            "passing" => &[
                ("go", "completed", "success"),
                ("desktop", "completed", "success"),
                ("native", "completed", "success"),
                ("macOS", "completed", "skipped"),
            ],
            "failing" => &[
                ("go", "completed", "success"),
                ("desktop / lint", "completed", "failure"),
                ("native", "completed", "timed_out"),
                ("macOS", "completed", "cancelled"),
            ],
            _ => &[
                ("go", "completed", "success"),
                ("native", "in_progress", ""),
                ("windows", "queued", ""),
            ],
        };
        Some(PullRequestDetail {
            number: pr.number,
            title: pr.title,
            state: pr.state,
            checks: pr.checks,
            url: pr.url,
            draft: pr.draft,
            additions: pr.additions,
            deletions: pr.deletions,
            comments: pr.comments,
            updated_at: pr.updated_at,
            base_branch: pr.base_branch,
            head_branch: pr.head_branch,
            fork: pr.fork,
            head_sha: pr.head_sha,
            author: pr.author,
            author_avatar: pr.author_avatar,
            agent: pr.agent,
            conflict: pr.conflict,
            review: pr.review,
            watched: pr.watched,
            labels: pr.labels,
            body: f.body.into(),
            check_runs: runs
                .iter()
                .map(|(name, status, conclusion)| PullCheck {
                    name: (*name).into(),
                    status: (*status).into(),
                    conclusion: (*conclusion).into(),
                    url: format!("https://github.com/{REPO}/actions/runs/{}", f.number * 31),
                })
                .collect(),
            changed_files: (f.files)().len() as i64,
        })
    }

    /// files is GET .../pulls/{n}/files.
    pub fn files_of(&self, number: i64) -> Option<PullRequestFiles> {
        let f = Self::fixture(number)?;
        Some(PullRequestFiles {
            files: Self::files(f).into_iter().map(|(p, _)| p).collect(),
            truncated: false,
        })
    }

    /// diff is GET .../pulls/{n}/diff?path=.
    pub fn diff(&self, number: i64, path: &str) -> Option<PullFileDiff> {
        let f = Self::fixture(number)?;
        Self::files(f)
            .into_iter()
            .find(|(p, _)| p.path == path)
            .map(|(p, patch)| PullFileDiff {
                path: p.path,
                patch,
            })
    }

    /// merge merges one (pulls.go's mergePullRequest), refusing as it does.
    pub fn merge(&mut self, number: i64) -> Result<PullRequest, String> {
        let pr = self
            .pr(number)
            .ok_or_else(|| format!("pull request #{number} not found"))?;
        if pr.draft {
            return Err(format!(
                "pull request #{number} is a draft: mark it ready for review before merging"
            ));
        }
        if pr.state != "open" {
            return Err(format!(
                "pull request #{number} is {}: nothing to merge",
                pr.state
            ));
        }
        if pr.conflict {
            return Err("GitHub refused: Pull Request is not mergeable".into());
        }
        self.merged.push(number);
        self.marks.remove(&number);
        Ok(self.pr(number).unwrap_or(pr))
    }

    /// edit_labels is POST .../pulls/{n}/labels: what's on it after.
    pub fn edit_labels(&mut self, number: i64, req: &EditLabelsRequest) -> Option<Vec<Label>> {
        let f = Self::fixture(number)?;
        let mut names: Vec<String> = self
            .labels_of(f)
            .into_iter()
            .map(|l| l.name)
            .filter(|n| !req.remove.contains(n))
            .collect();
        for n in &req.add {
            if !n.is_empty() && !names.contains(n) && !req.remove.contains(n) {
                names.push(n.clone());
            }
        }
        self.labels.insert(number, names);
        Some(self.labels_of(f))
    }

    /// all_labels is GET /v1/projects/{p}/labels.
    pub fn all_labels(&self) -> ProjectLabels {
        ProjectLabels {
            labels: LABELS.iter().map(|(n, ..)| label(n)).collect(),
        }
    }

    /// mark is POST .../merge-when-green: the mark, kept until it merges or
    /// is taken back.
    pub fn mark(&mut self, number: i64, method: &str) -> Result<MergeWhenGreen, String> {
        let pr = self
            .pr(number)
            .ok_or_else(|| format!("pull request #{number} not found"))?;
        if pr.draft {
            return Err(format!(
                "pull request #{number} is a draft: mark it ready for review before merging"
            ));
        }
        if pr.state != "open" {
            return Err(format!(
                "pull request #{number} is {}: nothing to merge",
                pr.state
            ));
        }
        let mark = MergeWhenGreen {
            number,
            method: if method.is_empty() { "merge" } else { method }.into(),
            at: self.ago(0),
            waiting: match pr.checks.as_str() {
                "failing" => "failing",
                "pending" => "checks",
                _ if pr.conflict => "conflict",
                _ => "",
            }
            .into(),
            ..Default::default()
        };
        self.marks.insert(number, mark.clone());
        Ok(mark)
    }

    pub fn unmark(&mut self, number: i64) {
        self.marks.remove(&number);
    }

    pub fn mark_of(&self, number: i64) -> Option<MergeWhenGreen> {
        self.marks.get(&number).cloned()
    }
}

fn label(name: &str) -> Label {
    let (n, color, description) = LABELS
        .iter()
        .find(|(n, ..)| *n == name)
        .copied()
        .unwrap_or((name, "ededed", ""));
    Label {
        name: n.into(),
        color: color.into(),
        description: description.into(),
    }
}
