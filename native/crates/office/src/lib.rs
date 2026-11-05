//! The Office (#668) drawn natively: a campus of projects' rooms where you
//! walk to agents and talk to them at their desks. This crate has no GPUI in
//! it: the room's layout, walking and the renderer (wgpu, offscreen); the app
//! shows its frames (crates/app/src/office).

pub mod glb;
pub mod gpu;

/// Kenney's models, bundled (CC0, assets/kenney/License.txt).
pub mod assets {
    pub const CHARACTER: &[u8] = include_bytes!("../assets/kenney/character-female-b.glb");
    pub const COLORMAP: &[u8] = include_bytes!("../assets/kenney/colormap.png");

    /// external resolves the files Kenney's models name beside them.
    pub fn external(uri: &str) -> Option<&'static [u8]> {
        uri.ends_with("colormap.png").then_some(COLORMAP)
    }
}
