//! Where things are on screen, for the tour's spotlight (components/v2/
//! Tour.tsx measures its anchors with getBoundingClientRect). An element the
//! tour may point at carries `spot(key)`, which records its bounds as the
//! frame is laid out while a tour plays; the tour reads the last frame's.
use std::cell::RefCell;
use std::collections::HashMap;

use gpui::*;

#[derive(Default)]
struct Spots {
    on: bool,
    now: HashMap<String, Bounds<Pixels>>,
    last: HashMap<String, Bounds<Pixels>>,
}

thread_local! {
    static SPOTS: RefCell<Spots> = RefCell::new(Spots::default());
}

/// measure turns the recording on or off, and starts a frame: what was
/// recorded in the last one is what `bounds` reads.
pub fn measure(on: bool) {
    SPOTS.with(|s| {
        let mut s = s.borrow_mut();
        s.on = on;
        s.last = std::mem::take(&mut s.now);
    });
}

/// bounds is where the first of keys (or one starting with a key ending
/// in '*', the topmost) was in the last frame.
pub fn bounds(keys: &[&str]) -> Option<Bounds<Pixels>> {
    SPOTS.with(|s| {
        let s = s.borrow();
        keys.iter().find_map(|k| match k.strip_suffix('*') {
            Some(prefix) => s
                .last
                .iter()
                .filter(|(n, _)| n.starts_with(prefix))
                .map(|(_, b)| *b)
                .min_by(|a, b| {
                    a.origin
                        .y
                        .partial_cmp(&b.origin.y)
                        .unwrap_or(std::cmp::Ordering::Equal)
                }),
            None => s.last.get(*k).copied(),
        })
    })
}

/// spot fills its (relative) parent and records its bounds under key while
/// a tour plays; it draws nothing.
pub fn spot(key: impl Into<String>) -> AnyElement {
    if !SPOTS.with(|s| s.borrow().on) {
        return div().into_any_element();
    }
    let key = key.into();
    canvas(
        move |b, window, _| {
            SPOTS.with(|s| {
                let mut s = s.borrow_mut();
                // Somewhere new: draw the spotlight again where it is.
                if s.last.get(&key) != Some(&b) {
                    window.refresh();
                }
                s.now.insert(key, b);
            })
        },
        |_, _, _, _| {},
    )
    .absolute()
    .top_0()
    .left_0()
    .size_full()
    .into_any_element()
}
