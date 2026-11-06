//! The Office (#668) drawn natively: a campus of projects' rooms where you
//! walk to agents and talk to them at their desks. This crate has no GPUI in
//! it: the room's layout, walking and the renderer (wgpu, offscreen); the app
//! shows its frames (crates/app/src/office).

// The port keeps #668's shapes: three.js-style constructors with many
// arguments, and its rng's next().
#![allow(clippy::too_many_arguments, clippy::should_implement_trait)]

pub mod canvas;
pub mod glb;
pub mod gpu;
pub mod kit;
pub mod mesh;
pub mod rig;
pub mod sim;

/// Kenney's models, bundled (CC0, assets/kenney/License.txt).
pub mod assets {
    pub const CHARACTER: &[u8] = include_bytes!("../assets/kenney/character-female-b.glb");
    pub const COLORMAP: &[u8] = include_bytes!("../assets/kenney/colormap.png");

    /// external resolves the files Kenney's models name beside them.
    pub fn external(uri: &str) -> Option<&'static [u8]> {
        uri.ends_with("colormap.png").then_some(COLORMAP)
    }
}

pub mod decor;
pub mod desks;
pub mod engine;
pub mod figures;
pub mod layout;
pub mod model;
pub mod player;
pub mod props;
pub mod textures;
pub mod walk;
