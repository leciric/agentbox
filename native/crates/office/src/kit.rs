//! What a room is built from besides geometry: its textures, in the order
//! they're uploaded (a material names one by its index here), and Kenney's
//! furniture pieces (#668's furniture.ts), loaded once.

use crate::glb::{self, Image, Model};
use crate::mesh::hex;
use glam::{Mat4, Vec3};
use std::collections::HashMap;

/// Kenney's Furniture Kit pieces #668 uses (CC0, assets/kenney/furniture/License.txt).
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum Piece {
    BookcaseOpen,
    Books,
    KitchenCabinet,
    KitchenCabinetDrawer,
    KitchenCabinetUpperDouble,
    KitchenCoffeeMachine,
    KitchenFridge,
    KitchenMicrowave,
    KitchenSink,
    LampRoundFloor,
    PlantSmall1,
    PlantSmall2,
    PlantSmall3,
    PottedPlant,
}

macro_rules! glb {
    ($f:literal) => {
        include_bytes!(concat!("../assets/kenney/furniture/", $f, ".glb"))
    };
}

impl Piece {
    pub const ALL: [Piece; 14] = [
        Piece::BookcaseOpen,
        Piece::Books,
        Piece::KitchenCabinet,
        Piece::KitchenCabinetDrawer,
        Piece::KitchenCabinetUpperDouble,
        Piece::KitchenCoffeeMachine,
        Piece::KitchenFridge,
        Piece::KitchenMicrowave,
        Piece::KitchenSink,
        Piece::LampRoundFloor,
        Piece::PlantSmall1,
        Piece::PlantSmall2,
        Piece::PlantSmall3,
        Piece::PottedPlant,
    ];

    fn bytes(self) -> &'static [u8] {
        match self {
            Piece::BookcaseOpen => glb!("bookcaseOpen"),
            Piece::Books => glb!("books"),
            Piece::KitchenCabinet => glb!("kitchenCabinet"),
            Piece::KitchenCabinetDrawer => glb!("kitchenCabinetDrawer"),
            Piece::KitchenCabinetUpperDouble => glb!("kitchenCabinetUpperDouble"),
            Piece::KitchenCoffeeMachine => glb!("kitchenCoffeeMachine"),
            Piece::KitchenFridge => glb!("kitchenFridge"),
            Piece::KitchenMicrowave => glb!("kitchenMicrowave"),
            Piece::KitchenSink => glb!("kitchenSink"),
            Piece::LampRoundFloor => glb!("lampRoundFloor"),
            Piece::PlantSmall1 => glb!("plantSmall1"),
            Piece::PlantSmall2 => glb!("plantSmall2"),
            Piece::PlantSmall3 => glb!("plantSmall3"),
            Piece::PottedPlant => glb!("pottedPlant"),
        }
    }
}

// The kit is about half size: this makes it metres.
const SCALE: f32 = 2.;

// Its materials, by name, in the room's palette: warm oak, cream, soft greens.
fn recolour(name: &str) -> Option<[f32; 3]> {
    Some(hex(match name {
        "wood" => "#e1a86d",
        "woodDark" => "#c98f58",
        "carpet" => "#9cb89a",
        "carpetWhite" => "#f3ece1",
        "plant" => "#5aa55a",
        "metal" => "#d8d2c8",
        "metalMedium" => "#b9b3aa",
        "metalDark" => "#4a4f58",
        "metalLight" => "#eeeae4",
        _ => return None,
    }))
}

/// A piece, centred on its footprint with its floor at 0, in metres.
pub struct Loaded {
    pub model: Model,
    /// Across, high and deep.
    pub w: f32,
    pub h: f32,
    pub d: f32,
}

/// Kit is the scene's textures and pieces.
#[derive(Default)]
pub struct Kit {
    pub textures: Vec<Image>,
    named: HashMap<String, usize>,
    pieces: HashMap<Piece, Loaded>,
}

impl Kit {
    pub fn new() -> Self {
        let mut k = Self::default();
        for p in Piece::ALL {
            if let Ok(m) = glb::load_with(p.bytes(), &crate::assets::external, &recolour) {
                let c = (m.min + m.max) / 2.;
                let fit = Mat4::from_scale(Vec3::splat(SCALE)) * Mat4::from_translation(Vec3::new(-c.x, -m.min.y, -c.z));
                let mut model = m;
                for mesh in &mut model.meshes {
                    for v in &mut mesh.vertices {
                        v.pos = fit.transform_point3(Vec3::from(v.pos)).into();
                    }
                }
                let size = (model.max - model.min) * SCALE;
                k.pieces.insert(p, Loaded { model, w: size.x, h: size.y, d: size.z });
            }
        }
        k
    }

    /// texture is the key of a texture by name, made the first time it's asked for.
    pub fn texture(&mut self, name: &str, make: impl FnOnce() -> Image) -> usize {
        if let Some(&i) = self.named.get(name) {
            return i;
        }
        self.textures.push(make());
        let i = self.textures.len() - 1;
        self.named.insert(name.to_string(), i);
        i
    }

    /// add is a texture that isn't shared (a sign, a screen).
    pub fn add(&mut self, img: Image) -> usize {
        self.textures.push(img);
        self.textures.len() - 1
    }

    /// piece is one of Kenney's pieces, facing +z, or None (it stays hand-built).
    pub fn piece(&self, p: Piece) -> Option<&Loaded> {
        self.pieces.get(&p)
    }

    /// piece_textures maps a piece's own images to the kit's keys (Kenney's
    /// furniture has none; its colours are its materials').
    pub fn piece_textures(&self) -> Vec<usize> {
        Vec::new()
    }
}
