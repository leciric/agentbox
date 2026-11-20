//! The C heap's arenas, and giving freed memory back to the system.
//!
//! glibc's malloc gives each thread that allocates at the same moment as
//! another an arena of its own, up to eight per core, each a 64 MiB
//! reservation: GPUI's executor threads, tokio's, the video decoders,
//! WebKitGTK's and Mesa's. Memory freed in an arena mostly stays in it, so a
//! burst (a long chat read, a review's recording, a switch of projects) kept
//! the process as big as its peak, spread over a dozen arenas.
//!
//! init caps the arenas (unless MALLOC_ARENA_MAX or GLIBC_TUNABLES say
//! otherwise), and a thread trims the heap, handing back the pages freed,
//! when asked to (after something big was let go, `trim_soon`) and every
//! half a minute while much is held free.
#[cfg(all(target_os = "linux", target_env = "gnu"))]
mod glibc {
    use std::sync::OnceLock;
    use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, channel};
    use std::time::Duration;

    /// The arenas kept: the main one and one more for the threads allocating
    /// beside it. The app's allocations are a UI's, not a server's, so
    /// waiting on an arena's lock now and then costs nothing seen.
    const ARENAS: i32 = 2;

    /// How often the heap is looked at, and how much it must hold free (in
    /// its arenas, past what malloc keeps on its own) to be trimmed.
    const EVERY: Duration = Duration::from_secs(30);
    const FREE_OVER: usize = 16 << 20;

    /// After `trim_soon`, how long to wait for what is being let go (a view's
    /// tasks and textures) to be freed before trimming.
    const SETTLE: Duration = Duration::from_secs(2);

    const M_ARENA_MAX: i32 = -8;

    // As glibc lays it out; only some fields are read.
    #[repr(C)]
    #[allow(dead_code)]
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
        fn mallopt(param: i32, value: i32) -> i32;
        fn malloc_trim(pad: usize) -> i32;
        fn mallinfo2() -> Mallinfo2;
    }

    static ASK: OnceLock<Sender<()>> = OnceLock::new();

    pub fn init() {
        let set_by_user = std::env::var_os("MALLOC_ARENA_MAX").is_some()
            || std::env::var("GLIBC_TUNABLES").is_ok_and(|t| t.contains("arena_max"));
        if !set_by_user {
            // SAFETY: before any other thread starts; mallopt only sets a
            // parameter of the allocator.
            unsafe { mallopt(M_ARENA_MAX, ARENAS) };
        }
        let (tx, rx) = channel();
        if ASK.set(tx).is_ok() {
            let _ = std::thread::Builder::new()
                .name("malloc-trim".into())
                .spawn(move || trimmer(rx));
        }
    }

    pub fn trim_soon() {
        if let Some(tx) = ASK.get() {
            let _ = tx.send(());
        }
    }

    fn trimmer(rx: Receiver<()>) {
        loop {
            let asked = match rx.recv_timeout(EVERY) {
                Ok(()) => true,
                Err(RecvTimeoutError::Timeout) => false,
                Err(RecvTimeoutError::Disconnected) => return,
            };
            if asked {
                std::thread::sleep(SETTLE);
                // Asks made meanwhile are answered by this one trim.
                while rx.try_recv().is_ok() {}
            }
            // SAFETY: mallinfo2 reads the allocator's counters; malloc_trim
            // gives free pages back, under each arena's own lock.
            let free = unsafe { mallinfo2() }.fordblks;
            if asked || free > FREE_OVER {
                unsafe { malloc_trim(0) };
            }
        }
    }
}

/// init goes first in main, before any thread starts.
pub fn init() {
    #[cfg(all(target_os = "linux", target_env = "gnu"))]
    glibc::init();
}

/// trim_soon asks for the heap to be trimmed in a moment: something big (a
/// chat, a project's views) is being let go.
pub fn trim_soon() {
    #[cfg(all(target_os = "linux", target_env = "gnu"))]
    glibc::trim_soon();
}
