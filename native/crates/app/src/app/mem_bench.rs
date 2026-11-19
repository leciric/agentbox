//! AGENTBOX_NATIVE_MEM_BENCH=<rounds>: the app's memory over a working
//! session, printed to stderr. Against fake-daemon (long histories, several
//! projects), it goes round every project `rounds` times, opening the lead's
//! chat and each agent's and scrolling each to its top (so older pages load),
//! then sits idle for AGENTBOX_NATIVE_MEM_BENCH_IDLE minutes (10) and quits.
//!
//! Each line is the process's resident memory, the C heap (glibc's mallinfo2:
//! what is handed out and what is held free), glibc's arenas, and how many
//! chats, desktops, terminals and players are alive: a count that grows from
//! round to round is a view something still holds.
//!
//!   AGENTBOX_NATIVE_MEM_BENCH=3 agentbox-native --socket /t/fake.sock
use std::time::{Duration, Instant};

use gpui::*;

use super::AppView;

impl AppView {
    pub(super) fn start_mem_bench(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(rounds) = std::env::var("AGENTBOX_NATIVE_MEM_BENCH")
            .ok()
            .map(|s| s.parse::<usize>().unwrap_or(3))
        else {
            return;
        };
        let idle = std::env::var("AGENTBOX_NATIVE_MEM_BENCH_IDLE")
            .ok()
            .and_then(|s| s.parse::<u64>().ok())
            .unwrap_or(10);
        let start = Instant::now();
        cx.spawn_in(window, async move |this, cx| {
            let wait = async |cx: &mut AsyncWindowContext, secs: f32| {
                cx.background_executor()
                    .timer(Duration::from_secs_f32(secs))
                    .await;
            };
            wait(cx, 6.).await;
            print(start, "start");
            for round in 1..=rounds {
                let Ok(projects) = this.read_with(cx, |this, _| -> Vec<String> {
                    this.projects.iter().map(|p| p.name.clone()).collect()
                }) else {
                    return;
                };
                for p in projects {
                    let ok = this.update_in(cx, |this, window, cx| {
                        this.select_project(p.clone(), window, cx)
                    });
                    if ok.is_err() {
                        return;
                    }
                    wait(cx, 2.5).await;
                    scroll_to_top(&this, cx).await;
                    let agents = this
                        .read_with(cx, |this, _| -> Vec<String> {
                            this.fleet.iter().map(|a| a.name.clone()).collect()
                        })
                        .unwrap_or_default();
                    for a in agents {
                        this.update_in(cx, |this, window, cx| this.open_chat(a, window, cx))
                            .ok();
                        wait(cx, 1.5).await;
                        scroll_to_top(&this, cx).await;
                    }
                }
                print(start, &format!("round {round}"));
            }
            for minute in 1..=idle {
                wait(cx, 60.).await;
                print(start, &format!("idle {minute}m"));
            }
            cx.update(|_, cx| crate::quit::quit(cx, "the memory bench ended"))
                .ok();
        })
        .detach();
    }
}

/// scroll_to_top scrolls the open chat up for a while, reading its older
/// pages as it reaches each top.
async fn scroll_to_top(this: &WeakEntity<AppView>, cx: &mut AsyncWindowContext) {
    this.update_in(cx, |this, window, cx| {
        if let Some(chat) = &this.chat {
            chat.update(cx, |c, cx| c.scroll_bench(-3000., 40, window, cx));
        }
    })
    .ok();
    cx.background_executor().timer(Duration::from_secs(3)).await;
}

fn print(start: Instant, step: &str) {
    let status = std::fs::read_to_string("/proc/self/status").unwrap_or_default();
    let kb = |key: &str| -> u64 {
        status
            .lines()
            .find_map(|l| l.strip_prefix(key))
            .and_then(|v| v.split_whitespace().next()?.parse().ok())
            .unwrap_or(0)
    };
    let live = crate::bench::live()
        .iter()
        .map(|(k, n)| format!("{k}={n}"))
        .collect::<Vec<_>>()
        .join(" ");
    let (count, arena_rss) = arenas();
    let (kids, kids_pss) = children();
    eprintln!(
        "mem {:>6.0}s {step:<10} rss={}M anon={}M hwm={}M threads={} {} arenas={count} ({}M resident) webkit={kids} ({}M pss) {live}",
        start.elapsed().as_secs_f64(),
        kb("VmRSS:") / 1024,
        kb("RssAnon:") / 1024,
        kb("VmHWM:") / 1024,
        kb("Threads:"),
        heap(),
        arena_rss / 1024,
        kids_pss / 1024,
    );
}

/// children is the app's child processes (WebKit's web and network
/// processes, one each per webview) and their proportional memory, in KiB.
fn children() -> (usize, u64) {
    let me = std::process::id().to_string();
    let (mut n, mut pss) = (0, 0);
    for e in std::fs::read_dir("/proc").into_iter().flatten().flatten() {
        let path = e.path();
        let Ok(stat) = std::fs::read_to_string(path.join("stat")) else {
            continue;
        };
        // pid (comm) state ppid …: comm may hold spaces, so after its ")".
        let ppid = stat
            .rsplit_once(')')
            .and_then(|(_, rest)| rest.split_whitespace().nth(1));
        if ppid != Some(me.as_str()) {
            continue;
        }
        n += 1;
        pss += std::fs::read_to_string(path.join("smaps_rollup"))
            .unwrap_or_default()
            .lines()
            .find_map(|l| l.strip_prefix("Pss:"))
            .and_then(|v| v.split_whitespace().next()?.parse::<u64>().ok())
            .unwrap_or(0);
    }
    (n, pss)
}

/// heap is glibc's C heap: bytes handed out, and held free.
#[cfg(all(target_os = "linux", target_env = "gnu"))]
fn heap() -> String {
    #[repr(C)]
    #[derive(Default)]
    struct Mallinfo2 {
        arena: usize,
        ordblks: usize,
        smblks: usize,
        hblks: usize,
        hblkhd: usize,
        usmblks: usize,
        fsmblks: usize,
        uordblks: usize,
        fordblks: usize,
        keepcost: usize,
    }
    unsafe extern "C" {
        fn mallinfo2() -> Mallinfo2;
    }
    // SAFETY: mallinfo2 only reads the allocator's own counters.
    let m = unsafe { mallinfo2() };
    format!(
        "malloc_used={}M malloc_free={}M mmapped={}M",
        (m.uordblks + m.hblkhd) >> 20,
        m.fordblks >> 20,
        m.hblkhd >> 20
    )
}

#[cfg(not(all(target_os = "linux", target_env = "gnu")))]
fn heap() -> String {
    String::new()
}

/// arenas counts glibc's thread arenas (each a 64 MiB reservation, aligned to
/// its size), and the memory resident in them, in KiB.
fn arenas() -> (usize, u64) {
    const HEAP: u64 = 64 << 20;
    let smaps = std::fs::read_to_string("/proc/self/smaps").unwrap_or_default();
    let (mut count, mut rss) = (0, 0);
    let mut inside = false;
    for line in smaps.lines() {
        let mut f = line.split_whitespace();
        let first = f.next().unwrap_or_default();
        if let Some((a, _)) = first.split_once('-')
            && let Ok(at) = u64::from_str_radix(a, 16)
            && first.contains('-')
            && line.split_whitespace().count() <= 6
        {
            let perms = f.next().unwrap_or_default();
            let anon = line.split_whitespace().count() == 5;
            inside = anon && perms == "rw-p" && at % HEAP == 0;
            if inside {
                count += 1;
            }
        } else if inside && let Some(v) = line.strip_prefix("Rss:") {
            rss += v
                .split_whitespace()
                .next()
                .and_then(|v| v.parse().ok())
                .unwrap_or(0);
        }
    }
    (count, rss)
}
