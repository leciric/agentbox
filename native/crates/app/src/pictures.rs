//! Pictures drawn with img(): screenshots, chat images, thumbnails, diagrams.
//!
//! GPUI's own img(path) and img(Arc<Image>) decode a picture once and keep it
//! for good: App's asset cache never forgets one (and an Arc<Image> made
//! again, as a chat makes its pictures each time it is opened, is another),
//! and the texture a picture is uploaded to stays in the window's atlas until
//! App::drop_image, which nothing called for them. After an hour or two of
//! reviews and chats that was gigabytes of VRAM and RAM.
//!
//! Here a picture is decoded off the main thread (down to THUMB where it is
//! shown small) and kept while it is drawn. One not drawn in the window's
//! last frame is idle: idle ones are kept up to IDLE_BYTES, and all of them
//! within BUDGET, the least recently drawn let go of first, out of RAM and
//! out of every window's atlas. One drawn again is decoded again.
//!
//! Everything here is on the main thread, as GPUI's drawing is.
use std::cell::RefCell;
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{Duration, Instant};

use gpui::*;

/// The bytes of decoded pictures kept, and of those not on screen. What is
/// on screen is kept whatever it weighs.
const BUDGET: usize = 256 << 20;
const IDLE_BYTES: usize = 64 << 20;
/// The longest side of a thumbnail, in device pixels: a 88-px-tall strip or
/// a chat's 96-px tile at 2x, covered.
pub const THUMB: u32 = 512;
/// How often idle pictures are looked for while the window draws.
const SWEEP: Duration = Duration::from_secs(1);
/// Past this much let go of in a sweep, malloc is asked to give its free
/// memory back (malloc_trim): glibc keeps a thread's freed arena otherwise,
/// and pictures decoded on background threads leave dozens of MB in each.
const GIVE_BACK: usize = 16 << 20;

/// How big a picture is decoded.
#[derive(Clone, Copy, PartialEq, Eq, Hash, Debug)]
pub enum Size {
    /// As it is.
    Whole,
    /// At most THUMB on its longer side.
    Thumb,
}

#[derive(Clone)]
enum Load {
    File(PathBuf),
    Bytes(Arc<Image>),
}

enum State {
    /// Being decoded; the views that asked, to redraw once it is.
    Loading(Vec<EntityId>),
    Ready(Arc<RenderImage>),
    Failed(ImageCacheError),
}

struct Entry {
    state: State,
    drawn: Instant,
}

#[derive(Default)]
struct Store {
    entries: HashMap<String, Entry>,
    /// When the window's last frame started (frame()), and when the
    /// pictures were last looked over.
    frame: Option<Instant>,
    swept: Option<Instant>,
    /// Pictures let go of elsewhere, for the sweep to take out of the
    /// atlases (drop_later).
    dropped: Vec<Arc<RenderImage>>,
}

thread_local! {
    static STORE: RefCell<Store> = RefCell::new(Store::default());
}

/// file is the picture at `path`.
pub fn file(path: impl AsRef<Path>, size: Size) -> ImageSource {
    let path = path.as_ref().to_path_buf();
    source(
        format!("f:{size:?}:{}", path.display()),
        Load::File(path),
        size,
    )
}

/// image is a picture held in memory; `key` names it where the same picture
/// is made again (a chat's, opened again), else it is known by its id.
pub fn image(key: Option<&str>, image: &Arc<Image>, size: Size) -> ImageSource {
    let key = match key {
        Some(k) => format!("k:{size:?}:{k}"),
        None => format!("i:{size:?}:{}", image.id),
    };
    source(key, Load::Bytes(image.clone()), size)
}

fn source(key: String, load: Load, size: Size) -> ImageSource {
    ImageSource::from(move |window: &mut Window, cx: &mut App| get(&key, &load, size, window, cx))
}

/// frame says a frame of the window starts: what isn't drawn in it is idle.
/// The root view calls it first thing in its render.
pub fn frame() {
    STORE.with_borrow_mut(|s| s.frame = Some(Instant::now()));
}

/// drop_later takes a picture a view let go of out of every window's atlas
/// at the next sweep. App::drop_image(image, None) from inside a window's
/// update misses that window (GPUI has it out of App's windows then), so
/// views that don't have their Window at hand leave it to this.
pub fn drop_later(image: Arc<RenderImage>) {
    STORE.with_borrow_mut(|s| s.dropped.push(image));
}

/// init looks the pictures over every SWEEP while the window draws, letting
/// go of idle ones past the budgets, and takes those let go of elsewhere out
/// of the atlases.
pub fn init(cx: &mut App) {
    // AGENTBOX_NATIVE_MEMLOG=1: what is kept, every few seconds on stderr
    // (the PR's measurements; GPUI says nothing of its atlases).
    let memlog = std::env::var_os("AGENTBOX_NATIVE_MEMLOG").is_some();
    cx.spawn(async move |cx| {
        let mut ticks = 0u32;
        loop {
            cx.background_executor().timer(SWEEP).await;
            ticks += 1;
            if memlog && ticks.is_multiple_of(5) {
                log_memory();
            }
            let (due, dropped) = STORE.with_borrow_mut(|s| {
                (
                    s.frame.is_some() && s.frame > s.swept,
                    std::mem::take(&mut s.dropped),
                )
            });
            cx.update(|cx| {
                let mut freed: usize = dropped.iter().map(|i| bytes(i)).sum();
                for image in dropped {
                    cx.drop_image(image, None);
                }
                if due {
                    freed += trim(cx);
                    #[cfg(target_os = "linux")]
                    {
                        freed += crate::snapshot::sweep();
                    }
                }
                if freed >= GIVE_BACK {
                    give_back(cx);
                }
            });
        }
    })
    .detach();
}

fn get(
    key: &str,
    load: &Load,
    size: Size,
    window: &mut Window,
    cx: &mut App,
) -> Option<Result<Arc<RenderImage>, ImageCacheError>> {
    let view = window.current_view();
    let (found, start) = STORE.with_borrow_mut(|s| {
        let now = Instant::now();
        match s.entries.get_mut(key) {
            Some(e) => {
                e.drawn = now;
                let found = match &mut e.state {
                    State::Ready(image) => Some(Ok(image.clone())),
                    State::Failed(err) => Some(Err(err.clone())),
                    State::Loading(views) => {
                        if !views.contains(&view) {
                            views.push(view);
                        }
                        None
                    }
                };
                (found, false)
            }
            None => {
                s.entries.insert(
                    key.to_string(),
                    Entry {
                        state: State::Loading(vec![view]),
                        drawn: now,
                    },
                );
                (None, true)
            }
        }
    });
    if start {
        decode(key.to_string(), load.clone(), size, cx);
    }
    found
}

/// decode decodes the picture on a background thread and keeps it.
fn decode(key: String, load: Load, size: Size, cx: &mut App) {
    let svg = cx.svg_renderer();
    let task = cx.background_executor().spawn(async move {
        let image = match load {
            Load::Bytes(image) => image,
            Load::File(path) => {
                let bytes = std::fs::read(&path)?;
                Arc::new(Image::from_bytes(format_of(&path, &bytes), bytes))
            }
        };
        let decoded = image.to_image_data(svg)?;
        anyhow::Ok(match size {
            Size::Whole => decoded,
            Size::Thumb => shrink(decoded, THUMB),
        })
    });
    cx.spawn(async move |cx| {
        let result: anyhow::Result<Arc<RenderImage>> = task.await;
        cx.update(|cx| arrived(&key, result, cx));
    })
    .detach();
}

fn arrived(key: &str, result: anyhow::Result<Arc<RenderImage>>, cx: &mut App) {
    let views = STORE.with_borrow_mut(|s| {
        // Let go of while it was decoded: never uploaded, nothing to drop.
        let e = s.entries.get_mut(key)?;
        let state = match result {
            Ok(image) => State::Ready(image),
            Err(err) => State::Failed(ImageCacheError::Other(Arc::new(err))),
        };
        match std::mem::replace(&mut e.state, state) {
            State::Loading(views) => Some(views),
            _ => None,
        }
    });
    for view in views.into_iter().flatten() {
        cx.notify(view);
    }
}

/// give_back has malloc return the memory freed to the system, off the main
/// thread (it walks every arena).
fn give_back(cx: &mut App) {
    #[cfg(all(target_os = "linux", target_env = "gnu"))]
    cx.background_executor()
        .spawn(async {
            unsafe extern "C" {
                fn malloc_trim(pad: usize) -> std::ffi::c_int;
            }
            // SAFETY: glibc's, thread-safe, and only returns free pages.
            unsafe { malloc_trim(0) };
        })
        .detach();
    #[cfg(not(all(target_os = "linux", target_env = "gnu")))]
    let _ = cx;
}

/// trim lets go of idle pictures past the budgets (over_budget), and
/// forgets idle failures, to be tried again if drawn again. It is the bytes
/// let go of.
fn trim(cx: &mut App) -> usize {
    let gone = STORE.with_borrow_mut(|s| {
        s.swept = s.frame;
        let frame = s.frame;
        s.entries
            .retain(|_, e| !(idle_at(frame, e.drawn) && matches!(e.state, State::Failed(_))));
        let kept = s.entries.iter().filter_map(|(key, e)| match &e.state {
            State::Ready(image) => Some((key.clone(), e.drawn, bytes(image))),
            _ => None,
        });
        over_budget(kept, frame, BUDGET, IDLE_BYTES)
            .into_iter()
            .filter_map(|key| match s.entries.remove(&key)?.state {
                State::Ready(image) => Some(image),
                _ => None,
            })
            .collect::<Vec<_>>()
    });
    let freed = gone.iter().map(|i| bytes(i)).sum();
    for image in gone {
        cx.drop_image(image, None);
    }
    freed
}

fn idle_at(frame: Option<Instant>, drawn: Instant) -> bool {
    frame.is_some_and(|f| drawn < f)
}

/// let_go_of is the pictures (key, last drawn, bytes) to let go of for those
/// kept to be within `budget` and those idle within `idle_budget`: idle ones
/// only (not drawn in the window's last frame), those drawn longest ago
/// first. snapshot.rs's previews are kept by it too.
pub fn let_go_of<K: Ord>(
    kept: impl Iterator<Item = (K, Instant, usize)>,
    budget: usize,
    idle_budget: usize,
) -> Vec<K> {
    let frame = STORE.with_borrow(|s| s.frame);
    over_budget(kept, frame, budget, idle_budget)
}

fn over_budget<K: Ord>(
    kept: impl Iterator<Item = (K, Instant, usize)>,
    frame: Option<Instant>,
    budget: usize,
    idle_budget: usize,
) -> Vec<K> {
    let (mut total, mut idle_total) = (0usize, 0usize);
    let mut idle = Vec::new();
    for (key, drawn, b) in kept {
        total += b;
        if idle_at(frame, drawn) {
            idle_total += b;
            idle.push((drawn, key, b));
        }
    }
    idle.sort_by(|a, b| a.0.cmp(&b.0).then_with(|| a.1.cmp(&b.1)));
    let mut gone = Vec::new();
    for (_, key, b) in idle {
        if total <= budget && idle_total <= idle_budget {
            break;
        }
        total -= b;
        idle_total -= b;
        gone.push(key);
    }
    gone
}

/// let_go_all lets go of every picture, whatever the budgets (the window
/// hidden to the tray); they are decoded again when drawn again.
pub fn let_go_all() {
    STORE.with_borrow_mut(|s| {
        let mut gone = Vec::new();
        // One still on its way is kept, to tell its views it arrived.
        s.entries.retain(|_, e| match &e.state {
            State::Ready(image) => {
                gone.push(image.clone());
                false
            }
            State::Loading(_) => true,
            State::Failed(_) => false,
        });
        s.dropped.extend(gone);
    });
}

fn log_memory() {
    let (n, b) = stats();
    let mb = |b: usize| b as f64 / (1 << 20) as f64;
    #[cfg(target_os = "linux")]
    let (sn, sb, views) = {
        let (sn, sb) = crate::snapshot::stats();
        (sn, sb, crate::snapshot::views())
    };
    #[cfg(not(target_os = "linux"))]
    let (sn, sb, views) = (0, 0, 0);
    eprintln!(
        "memlog pictures={n} pictures_mb={:.1} previews={sn} previews_mb={:.1} webkit_views={views}",
        mb(b),
        mb(sb)
    );
}

/// stats is the pictures kept and their bytes, for the memory log.
pub fn stats() -> (usize, usize) {
    STORE.with_borrow(|s| {
        s.entries
            .values()
            .filter_map(|e| match &e.state {
                State::Ready(i) => Some(bytes(i)),
                _ => None,
            })
            .fold((0, 0), |(n, b), x| (n + 1, b + x))
    })
}

/// bytes is what a picture holds, every frame of it.
pub fn bytes(image: &RenderImage) -> usize {
    (0..image.frame_count())
        .map(|i| {
            let s = image.size(i);
            s.width.0.max(0) as usize * s.height.0.max(0) as usize * 4
        })
        .sum()
}

/// format_of is a file's picture format, by its bytes, else its name.
fn format_of(path: &Path, bytes: &[u8]) -> ImageFormat {
    let by_name = || {
        let ext = path.extension()?.to_str()?.to_ascii_lowercase();
        ImageFormat::from_mime_type(&format!("image/{ext}"))
            .or_else(|| (ext == "svg").then_some(ImageFormat::Svg))
    };
    match image::guess_format(bytes) {
        Ok(image::ImageFormat::Png) => ImageFormat::Png,
        Ok(image::ImageFormat::Jpeg) => ImageFormat::Jpeg,
        Ok(image::ImageFormat::WebP) => ImageFormat::Webp,
        Ok(image::ImageFormat::Gif) => ImageFormat::Gif,
        Ok(image::ImageFormat::Bmp) => ImageFormat::Bmp,
        Ok(image::ImageFormat::Tiff) => ImageFormat::Tiff,
        Ok(image::ImageFormat::Ico) => ImageFormat::Ico,
        _ => by_name().unwrap_or(ImageFormat::Png),
    }
}

/// shrink is a still picture at most `max` on its longer side (an animated
/// one is kept as it is).
fn shrink(image: Arc<RenderImage>, max: u32) -> Arc<RenderImage> {
    if image.frame_count() != 1 {
        return image;
    }
    let size = image.size(0);
    let (w, h) = (size.width.0.max(1) as u32, size.height.0.max(1) as u32);
    let (nw, nh) = fit_within(w, h, max);
    if (nw, nh) == (w, h) {
        return image;
    }
    let Some(frame) = image
        .as_bytes(0)
        .and_then(|b| image::RgbaImage::from_raw(w, h, b.to_vec()))
    else {
        return image;
    };
    // BGRA, as GPUI keeps it: resizing doesn't care which channel is which.
    let small = image::imageops::thumbnail(&frame, nw, nh);
    Arc::new(RenderImage::new(vec![image::Frame::new(small)]))
}

/// fit_within is `w`×`h` scaled down to at most `max` on its longer side.
fn fit_within(w: u32, h: u32, max: u32) -> (u32, u32) {
    let long = w.max(h);
    if long <= max {
        return (w, h);
    }
    let k = max as f64 / long as f64;
    (
        ((w as f64 * k).round() as u32).max(1),
        ((h as f64 * k).round() as u32).max(1),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn only_idle_pictures_go_the_oldest_first() {
        let t0 = Instant::now();
        let at = |s: u64| t0 + Duration::from_secs(s);
        let frame = Some(at(10));
        // Three idle ones of 10 and one of 50 on screen: within 60 and 15,
        // the two drawn longest ago go.
        let kept = vec![
            ("b", at(2), 10),
            ("a", at(1), 10),
            ("c", at(3), 10),
            ("shown", at(10), 50),
        ];
        assert_eq!(
            over_budget(kept.clone().into_iter(), frame, 60, 15),
            vec!["a", "b"]
        );
        // Within both: none.
        assert!(over_budget(kept.clone().into_iter(), frame, 100, 30).is_empty());
        // What's on screen stays whatever it weighs.
        assert_eq!(
            over_budget(kept.clone().into_iter(), frame, 0, 0),
            vec!["a", "b", "c"]
        );
        // Before the first frame nothing is idle.
        assert!(over_budget(kept.into_iter(), None, 0, 0).is_empty());
    }

    #[test]
    fn thumbnails_keep_their_shape() {
        assert_eq!(fit_within(1920, 1080, 512), (512, 288));
        assert_eq!(fit_within(1080, 1920, 512), (288, 512));
        assert_eq!(fit_within(300, 200, 512), (300, 200));
        assert_eq!(fit_within(10000, 1, 512), (512, 1));
    }

    #[test]
    fn shrink_makes_a_smaller_picture() {
        let big = Arc::new(RenderImage::new(vec![image::Frame::new(
            image::RgbaImage::new(2000, 1000),
        )]));
        let small = shrink(big.clone(), 512);
        assert_eq!(small.size(0).width.0, 512);
        assert_eq!(small.size(0).height.0, 256);
        assert!(bytes(&small) * 10 < bytes(&big));
        let tiny = Arc::new(RenderImage::new(vec![image::Frame::new(
            image::RgbaImage::new(100, 50),
        )]));
        assert!(Arc::ptr_eq(&shrink(tiny.clone(), 512), &tiny));
    }

    #[test]
    fn formats_by_bytes_then_name() {
        let png = [0x89, b'P', b'N', b'G', 0x0d, 0x0a, 0x1a, 0x0a];
        assert_eq!(format_of(Path::new("x.jpg"), &png), ImageFormat::Png);
        assert_eq!(format_of(Path::new("x.svg"), b"<svg/>"), ImageFormat::Svg);
        assert_eq!(format_of(Path::new("x.jpeg"), b"??"), ImageFormat::Jpeg);
    }
}
