//! A posable thing: three.js's group tree, flattened. Each node has a
//! transform relative to its parent (what a pose changes every frame) and,
//! optionally, its own parts baked into meshes in its frame. The scene uploads
//! a rig's meshes once and draws each node's at its world transform.

use crate::gpu::MeshData;
use crate::mesh::{Baker, Geo, Mat};
use glam::{Mat4, Quat, Vec3};

#[derive(Default)]
pub struct Node {
    pub name: &'static str,
    pub parent: Option<usize>,
    /// Where it is in its parent, as three's position, rotation (XYZ Euler) and scale.
    pub pos: Vec3,
    pub rot: Vec3,
    pub scale: Vec3,
    pub visible: bool,
    /// Its own parts, baked in its frame (empty for a pivot).
    pub meshes: Vec<MeshData>,
    /// Its opacity, times its parts' own (a figure fading out of the way).
    pub opacity: f32,
}

impl Node {
    pub fn local(&self) -> Mat4 {
        Mat4::from_scale_rotation_translation(self.scale, Quat::from_euler(glam::EulerRot::XYZ, self.rot.x, self.rot.y, self.rot.z), self.pos)
    }
}

#[derive(Default)]
pub struct Rig {
    pub nodes: Vec<Node>,
}

/// NodeId is a node's index in its rig.
pub type NodeId = usize;

impl Rig {
    pub fn new() -> Self {
        let mut r = Self::default();
        r.add("root", None, Vec3::ZERO);
        r
    }

    /// add is a new node under parent (None for the root's level), at pos.
    pub fn add(&mut self, name: &'static str, parent: Option<NodeId>, pos: Vec3) -> NodeId {
        self.nodes.push(Node { name, parent, pos, rot: Vec3::ZERO, scale: Vec3::ONE, visible: true, meshes: Vec::new(), opacity: 1. });
        self.nodes.len() - 1
    }

    /// bake puts parts into a node: each a geometry, a material and where it
    /// is in the node's frame.
    pub fn bake(&mut self, node: NodeId, parts: &[(Geo, Mat, Mat4)]) {
        let mut b = Baker::default();
        for (g, m, t) in parts {
            b.put(g, m, *t);
        }
        self.nodes[node].meshes.extend(b.finish());
    }

    pub fn node(&mut self, id: NodeId) -> &mut Node {
        &mut self.nodes[id]
    }

    pub fn find(&self, name: &str) -> Option<NodeId> {
        self.nodes.iter().position(|n| n.name == name)
    }

    /// world is every node's transform in the world, `root` placing the rig,
    /// and whether it's shown (it and every parent visible).
    pub fn world(&self, root: Mat4) -> Vec<(Mat4, bool)> {
        let mut out: Vec<(Mat4, bool)> = Vec::with_capacity(self.nodes.len());
        for n in &self.nodes {
            // Parents come before their children (add() can't make it otherwise).
            let (pm, pv) = match n.parent {
                Some(p) => out[p],
                None => (root, true),
            };
            out.push((pm * n.local(), pv && n.visible));
        }
        out
    }
}
