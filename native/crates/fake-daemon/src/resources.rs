//! The view leases and agents' live resources (internal/daemon/presence.go,
//! agentresources.go) for the fake's agents, which have no machine to
//! sample: a lease asking for an agent's resources makes it sampled every
//! two seconds, with made-up but plausible numbers (a working agent's CPU
//! and disk busier than an idle one's), each published as an
//! agent-resources event. The leases are passed on to the upstream daemon
//! too, so its own back-off sees the app.
use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use agentbox_api::*;
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
use hyper::body::Incoming;
use hyper::{Method, Request, Response, StatusCode};

use crate::{Body, Fake, bad_request, json, now};

const TTL: Duration = Duration::from_secs(45);
const EVERY: Duration = Duration::from_secs(2);
const WINDOW: Duration = Duration::from_secs(600);

#[derive(Default)]
struct State {
    leases: HashMap<String, (Instant, PresenceRequest)>,
    history: HashMap<String, Vec<AgentResourcePoint>>,
}

static STATE: Mutex<Option<State>> = Mutex::new(None);

fn with<R>(f: impl FnOnce(&mut State) -> R) -> R {
    f(STATE.lock().unwrap().get_or_insert_with(Default::default))
}

/// sampling is the agents a live lease asks resources for.
fn sampling(s: &mut State) -> Vec<String> {
    s.leases.retain(|_, (at, _)| at.elapsed() < TTL);
    let mut refs: Vec<String> = s
        .leases
        .values()
        .filter(|(_, l)| l.visible)
        .flat_map(|(_, l)| {
            l.views
                .iter()
                .filter(|v| v.resources)
                .map(|v| v.agent.clone())
        })
        .collect();
    refs.sort();
    refs.dedup();
    refs
}

pub async fn route(
    fake: &Arc<Fake>,
    req: Request<Incoming>,
) -> Result<Response<Body>, Box<Request<Incoming>>> {
    let path = req.uri().path().to_string();
    let parts: Vec<String> = path
        .trim_matches('/')
        .split('/')
        .map(crate::decode)
        .collect();
    let parts: Vec<&str> = parts.iter().map(String::as_str).collect();
    let method = req.method().clone();
    let res = match (&method, parts.as_slice()) {
        (&Method::PUT, ["v1", "presence"]) => {
            let (head, body) = req.into_parts();
            let body = body
                .collect()
                .await
                .map(|b| b.to_bytes())
                .unwrap_or_default();
            let Ok(lease) = serde_json::from_slice::<PresenceRequest>(&body) else {
                return Ok(bad_request("a presence lease in JSON".into()));
            };
            eprintln!(
                "presence {}: visible {} views {:?} power {:?}",
                lease.client, lease.visible, lease.views, lease.power
            );
            with(|s| {
                s.leases
                    .insert(lease.client.clone(), (Instant::now(), lease))
            });
            pass_on(fake, Request::from_parts(head, Full::new(body))).await;
            json(
                StatusCode::OK,
                &PresenceResponse {
                    ttl_seconds: TTL.as_secs() as i64,
                    idle: false,
                },
            )
        }
        (&Method::DELETE, ["v1", "presence", client]) => {
            eprintln!("presence {client}: given up");
            with(|s| s.leases.remove(*client));
            let (head, _) = req.into_parts();
            pass_on(fake, Request::from_parts(head, Full::new(Bytes::new()))).await;
            json(StatusCode::OK, &serde_json::json!({}))
        }
        (&Method::GET, ["v1", "presence"]) => json(
            StatusCode::OK,
            &with(|s| {
                let sampling = sampling(s);
                PresenceState {
                    leases: s
                        .leases
                        .values()
                        .map(|(_, l)| PresenceLease {
                            client: l.client.clone(),
                            kind: l.kind.clone(),
                            visible: l.visible,
                            views: l.views.clone(),
                            power: l.power.clone(),
                            ..Default::default()
                        })
                        .collect(),
                    sampling,
                    ..Default::default()
                }
            }),
        ),
        (&Method::GET, ["v1", "agents", p, a, "resources"])
            if fake.is_fake(p) && fake.fleet.lock().unwrap().iter().any(|x| x.name == *a) =>
        {
            let r = format!("{p}/{a}");
            json(
                StatusCode::OK,
                &with(|s| AgentResourceHistory {
                    sampling: sampling(s).contains(&r),
                    points: s.history.get(&r).cloned().unwrap_or_default(),
                    r#ref: r,
                    interval_seconds: EVERY.as_secs() as i64,
                    window_seconds: WINDOW.as_secs() as i64,
                }),
            )
        }
        _ => return Err(Box::new(req)),
    };
    Ok(res)
}

/// pass_on gives the upstream daemon the lease too, if there is one.
async fn pass_on(fake: &Arc<Fake>, req: Request<Full<Bytes>>) {
    if let Some(up) = &fake.opts.upstream {
        let _ = crate::proxy_full(up, req).await;
    }
}

/// sample samples the agents a lease asks for, for as long as the fake runs.
pub async fn sample(fake: Arc<Fake>) {
    let mut tick: u64 = 0;
    loop {
        tokio::time::sleep(EVERY).await;
        tick += 1;
        let refs = with(sampling);
        for r in refs {
            let busy = fake
                .fleet
                .lock()
                .unwrap()
                .iter()
                .find(|a| a.r#ref == r)
                .is_some_and(|a| a.busy || a.chat == "running");
            let point = with(|s| {
                let h = s.history.entry(r.clone()).or_default();
                let point = made_up(&r, tick, busy, h.last());
                h.push(point.clone());
                let cut = h
                    .len()
                    .saturating_sub((WINDOW.as_secs() / EVERY.as_secs()) as usize);
                h.drain(..cut);
                point
            });
            fake.emit(
                EVENT_AGENT_RESOURCES,
                &AgentResourcesEvent {
                    r#ref: r.clone(),
                    point,
                },
            );
        }
    }
}

/// made_up is the next sample of an agent: a smooth wander, busier while it
/// works, with now and then a burst (a build).
fn made_up(
    r: &str,
    tick: u64,
    busy: bool,
    last: Option<&AgentResourcePoint>,
) -> AgentResourcePoint {
    let seed = r
        .bytes()
        .fold(7u64, |h, b| h.wrapping_mul(31).wrapping_add(b as u64));
    let t = tick as f64 + (seed % 97) as f64;
    let wave = |period: f64| (t / period * std::f64::consts::TAU).sin();
    let burst = (tick + seed) % 40 < 6;
    let cpu = if busy {
        60. + 35. * wave(23.) + if burst { 140. } else { 0. }
    } else {
        3. + 2. * wave(11.).abs()
    };
    let base_mem = 700. * 1024. * 1024. + (seed % 300) as f64 * 1024. * 1024.;
    let prev_mem = last.map_or(base_mem, |l| l.memory as f64);
    let target = base_mem
        + if busy { 900. * 1024. * 1024. } else { 0. }
        + if burst { 600. * 1024. * 1024. } else { 0. };
    let memory = prev_mem + (target - prev_mem) * 0.15 + wave(7.) * 8. * 1024. * 1024.;
    let disk = |scale: f64, period: f64| {
        let v = if busy {
            scale * (0.5 + 0.5 * wave(period))
        } else {
            scale * 0.01
        };
        (v + if burst { scale * 3. } else { 0. }) as i64
    };
    AgentResourcePoint {
        at: now(),
        cpu: cpu.max(0.),
        memory: memory as i64,
        swap: if burst { 48 * 1024 * 1024 } else { 0 },
        disk_read: disk(6. * 1024. * 1024., 13.),
        disk_write: disk(2. * 1024. * 1024., 17.),
    }
}
