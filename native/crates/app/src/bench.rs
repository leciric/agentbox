//! Timings for comparing the native app with the Electron one, printed to
//! stderr when AGENTBOX_NATIVE_BENCH is set: when the first frame and the first
//! frame with data went out, and how long a streamed chunk takes from arriving
//! to being on screen, and how far apart frames are while it streams or scrolls.
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};

static START: OnceLock<Instant> = OnceLock::new();
static ON: OnceLock<bool> = OnceLock::new();
static SAMPLES: Mutex<Vec<(&'static str, Duration)>> = Mutex::new(Vec::new());

pub fn start() {
    START.get_or_init(Instant::now);
    ON.get_or_init(|| std::env::var_os("AGENTBOX_NATIVE_BENCH").is_some());
}

pub fn on() -> bool {
    *ON.get().unwrap_or(&false)
}

/// mark prints how long after start something happened, once per name.
pub fn mark(name: &'static str) {
    static SEEN: Mutex<Vec<&'static str>> = Mutex::new(Vec::new());
    if !on() {
        return;
    }
    let mut seen = SEEN.lock().unwrap();
    if seen.contains(&name) {
        return;
    }
    seen.push(name);
    let ms = START.get().unwrap().elapsed().as_secs_f64() * 1000.0;
    eprintln!("bench {name} {ms:.1}ms");
}

pub fn sample(name: &'static str, d: Duration) {
    if on() {
        SAMPLES.lock().unwrap().push((name, d));
    }
}

/// report prints and clears the percentiles of every sample kind.
pub fn report() {
    if !on() {
        return;
    }
    let mut all = SAMPLES.lock().unwrap();
    let mut names: Vec<&str> = all.iter().map(|s| s.0).collect();
    names.sort();
    names.dedup();
    for name in names {
        let mut v: Vec<f64> = all
            .iter()
            .filter(|s| s.0 == name)
            .map(|s| s.1.as_secs_f64() * 1000.0)
            .collect();
        v.sort_by(|a, b| a.partial_cmp(b).unwrap());
        let p = |q: f64| v[((v.len() as f64 - 1.0) * q).round() as usize];
        let over = v.iter().filter(|&&x| x > 16.7).count();
        eprintln!(
            "bench {name}: n={} p50={:.1}ms p95={:.1}ms p99={:.1}ms max={:.1}ms over16.7ms={}",
            v.len(),
            p(0.5),
            p(0.95),
            p(0.99),
            v[v.len() - 1],
            over
        );
    }
    all.clear();
}
