//! burst opens a lead session the way the app does, many times at once:
//! every request it sends together, against a socket (AGENTBOX_SOCKET), and
//! says how many failed and why. `cargo run -p agentbox-api --example burst
//! -- <project> <session> [rounds]`.
use agentbox_api::Client;

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let project = args.get(1).cloned().unwrap_or_else(|| "demo".into());
    let session = args.get(2).cloned().unwrap_or_else(|| "main".into());
    let rounds: usize = args.get(3).and_then(|n| n.parse().ok()).unwrap_or(10);
    let client = Client::new(agentbox_api::client::default_socket());
    let base = format!("/v1/projects/{project}/sessions/{session}");
    let paths = vec![
        format!("{base}/chat?limit=20"),
        format!("{base}/checkpoints"),
        format!("{base}/output"),
        format!("/v1/projects/{project}/sessions"),
        format!("/v1/projects/{project}/sessions?closed=true"),
        format!("/v1/projects/{project}/media"),
        format!("/v1/projects/{project}/artifacts"),
        format!("/v1/projects/{project}/review?limit=200"),
        "/v1/projects".to_string(),
        "/v1/agents".to_string(),
        "/v1/version".to_string(),
        "/v1/jobs".to_string(),
    ];
    let rt = tokio::runtime::Runtime::new().unwrap();
    let (mut ok, mut failed) = (0, 0);
    let mut why = std::collections::BTreeMap::<String, usize>::new();
    for _ in 0..rounds {
        let results = rt.block_on(async {
            let mut tasks = Vec::new();
            for _ in 0..3 {
                for p in &paths {
                    let (c, p) = (client.clone(), p.clone());
                    tasks.push(tokio::spawn(async move {
                        c.raw(agentbox_api::client::Method::GET, &p, None).await
                    }));
                }
            }
            let mut out = Vec::new();
            for t in tasks {
                out.push(t.await.unwrap());
            }
            out
        });
        for r in results {
            match r {
                Ok(_) => ok += 1,
                Err(e) if agentbox_api::client::unreachable(&e) => {
                    failed += 1;
                    *why.entry(format!("{e:#}")).or_default() += 1;
                }
                Err(_) => ok += 1, // the daemon answered
            }
        }
    }
    println!("{ok} answered, {failed} never reached the daemon");
    for (w, n) in why {
        println!("  {n}× {w}");
    }
}
