//! Kenney's .glb models (#668's assets, CC0), read into the renderer's meshes.
//! A model's nodes are flattened into one mesh per texture, in metres as
//! drawn, so placing a piece is one instance.

use crate::gpu::{MeshData, Vertex};
use anyhow::{Context as _, Result};
use glam::{Mat3, Mat4, Vec3};

/// A model's flattened meshes and its bounds.
pub struct Model {
    /// One mesh per texture; `texture` is an index into `images` (or None).
    pub meshes: Vec<MeshData>,
    pub images: Vec<Image>,
    pub min: Vec3,
    pub max: Vec3,
}

pub struct Image {
    pub width: u32,
    pub height: u32,
    pub rgba: Vec<u8>,
}

/// decode_png reads a PNG into RGBA8.
pub fn decode_png(bytes: &[u8]) -> Result<Image> {
    let mut dec = png::Decoder::new(std::io::Cursor::new(bytes));
    dec.set_transformations(png::Transformations::EXPAND | png::Transformations::STRIP_16);
    let mut reader = dec.read_info()?;
    let mut buf = vec![0; reader.output_buffer_size()];
    let info = reader.next_frame(&mut buf)?;
    buf.truncate(info.buffer_size());
    let rgba = match info.color_type {
        png::ColorType::Rgba => buf,
        png::ColorType::Rgb => buf
            .chunks(3)
            .flat_map(|p| [p[0], p[1], p[2], 255])
            .collect(),
        png::ColorType::GrayscaleAlpha => buf
            .chunks(2)
            .flat_map(|p| [p[0], p[0], p[0], p[1]])
            .collect(),
        png::ColorType::Grayscale => buf.iter().flat_map(|&v| [v, v, v, 255]).collect(),
        png::ColorType::Indexed => anyhow::bail!("indexed PNG left unexpanded"),
    };
    Ok(Image {
        width: info.width,
        height: info.height,
        rgba,
    })
}

/// load reads a .glb; `external` resolves an image it names by URI (Kenney's
/// models point at Textures/colormap.png beside them).
pub fn load(bytes: &[u8], external: &dyn Fn(&str) -> Option<&'static [u8]>) -> Result<Model> {
    load_with(bytes, external, &|_| None)
}

/// load_with also recolours its materials by name (linear RGB), as #668
/// recolours Kenney's furniture to the room's palette.
pub fn load_with(
    bytes: &[u8],
    external: &dyn Fn(&str) -> Option<&'static [u8]>,
    recolour: &dyn Fn(&str) -> Option<[f32; 3]>,
) -> Result<Model> {
    let gltf = gltf::Gltf::from_slice(bytes).context("read the .glb")?;
    let blob = gltf.blob.as_deref();
    let buffers: Vec<&[u8]> = gltf
        .buffers()
        .map(|b| match b.source() {
            gltf::buffer::Source::Bin => blob.unwrap_or(&[]),
            gltf::buffer::Source::Uri(u) => external(u).unwrap_or(&[]),
        })
        .collect();
    let mut images = Vec::new();
    for img in gltf.images() {
        let png: &[u8] = match img.source() {
            gltf::image::Source::View { view, .. } => {
                let b = buffers[view.buffer().index()];
                &b[view.offset()..view.offset() + view.length()]
            }
            gltf::image::Source::Uri { uri, .. } => {
                external(uri).with_context(|| format!("image {uri}"))?
            }
        };
        images.push(decode_png(png)?);
    }
    let mut by_tex: Vec<(Option<usize>, MeshData)> = Vec::new();
    let mut min = Vec3::splat(f32::MAX);
    let mut max = Vec3::splat(f32::MIN);
    let scene = gltf
        .default_scene()
        .or_else(|| gltf.scenes().next())
        .context("no scene")?;
    let mut stack: Vec<(gltf::Node, Mat4)> = scene.nodes().map(|n| (n, Mat4::IDENTITY)).collect();
    while let Some((node, parent)) = stack.pop() {
        let local = Mat4::from_cols_array_2d(&node.transform().matrix());
        let world = parent * local;
        for c in node.children() {
            stack.push((c, world));
        }
        let Some(mesh) = node.mesh() else { continue };
        let normal_m = Mat3::from_mat4(world).inverse().transpose();
        for prim in mesh.primitives() {
            let r = prim.reader(|b| Some(buffers[b.index()]));
            let Some(pos) = r.read_positions() else {
                continue;
            };
            let pos: Vec<[f32; 3]> = pos.collect();
            let normals: Vec<[f32; 3]> = r
                .read_normals()
                .map(|n| n.collect())
                .unwrap_or_else(|| vec![[0., 1., 0.]; pos.len()]);
            let uvs: Vec<[f32; 2]> = r
                .read_tex_coords(0)
                .map(|t| t.into_f32().collect())
                .unwrap_or_else(|| vec![[0., 0.]; pos.len()]);
            let colors: Option<Vec<[f32; 4]>> =
                r.read_colors(0).map(|c| c.into_rgba_f32().collect());
            let mat = prim.material();
            let pbr = mat.pbr_metallic_roughness();
            let mut factor = pbr.base_color_factor();
            if let Some(c) = mat.name().and_then(recolour) {
                factor = [c[0], c[1], c[2], factor[3]];
            }
            let tex = pbr
                .base_color_texture()
                .map(|t| t.texture().source().index());
            let at = match by_tex.iter().position(|(t, _)| *t == tex) {
                Some(i) => i,
                None => {
                    by_tex.push((
                        tex,
                        MeshData {
                            texture: tex,
                            ..Default::default()
                        },
                    ));
                    by_tex.len() - 1
                }
            };
            let out = &mut by_tex[at].1;
            let base = out.vertices.len() as u32;
            for (i, p) in pos.iter().enumerate() {
                let wp = world.transform_point3(Vec3::from(*p));
                min = min.min(wp);
                max = max.max(wp);
                let n = (normal_m * Vec3::from(normals[i])).normalize_or_zero();
                let c = colors.as_ref().map(|c| c[i]).unwrap_or([1., 1., 1., 1.]);
                out.vertices.push(Vertex {
                    pos: wp.into(),
                    normal: n.into(),
                    uv: uvs[i],
                    color: [
                        c[0] * factor[0],
                        c[1] * factor[1],
                        c[2] * factor[2],
                        c[3] * factor[3],
                    ],
                });
            }
            match r.read_indices() {
                Some(ix) => out.indices.extend(ix.into_u32().map(|i| i + base)),
                None => out.indices.extend(base..base + pos.len() as u32),
            }
        }
    }
    Ok(Model {
        meshes: by_tex.into_iter().map(|(_, m)| m).collect(),
        images,
        min,
        max,
    })
}

// ---------------------------------------------------------------------------
// Skinned models: the player (kenney.ts), a .glb with a skeleton and its
// animation clips, posed on the CPU for any blend of clips at any times.

use glam::{Quat, Vec4};

/// A node of the skeleton's hierarchy, as authored (its rest transform).
#[derive(Clone, Debug)]
pub struct SkinNode {
    pub name: String,
    pub parent: Option<usize>,
    pub t: Vec3,
    pub r: Quat,
    pub s: Vec3,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Path {
    Translation,
    Rotation,
    Scale,
}

/// One animated property of one node: keyframes, linear or stepped.
#[derive(Clone, Debug)]
pub struct Channel {
    pub node: usize,
    pub path: Path,
    pub times: Vec<f32>,
    /// xyz (w = 0) for a translation or scale, xyzw for a rotation.
    pub values: Vec<Vec4>,
    pub step: bool,
}

#[derive(Clone, Debug)]
pub struct Clip {
    pub name: String,
    pub duration: f32,
    pub channels: Vec<Channel>,
}

/// A skin: its joints (node indices) and their inverse bind matrices.
#[derive(Clone, Debug)]
pub struct Skin {
    pub joints: Vec<usize>,
    pub ibm: Vec<Mat4>,
}

/// A skinned primitive: its bind-pose triangles and what each vertex follows.
#[derive(Clone, Debug)]
pub struct SkinnedPart {
    pub rest: MeshData,
    pub joints: Vec<[u16; 4]>,
    pub weights: Vec<[f32; 4]>,
    pub skin: usize,
    /// Its material's name, for recolouring.
    pub material: String,
}

/// How much of each clip is playing: (clip index, time in seconds, weight).
pub type Blend = (usize, f32, f32);

pub struct Skinned {
    pub parts: Vec<SkinnedPart>,
    pub skins: Vec<Skin>,
    pub nodes: Vec<SkinNode>,
    pub clips: Vec<Clip>,
    pub images: Vec<Image>,
    /// The bind pose's bounds.
    pub min: Vec3,
    pub max: Vec3,
    order: Vec<usize>,
}

fn vec4_lerp(a: Vec4, b: Vec4, k: f32) -> Vec4 {
    a + (b - a) * k
}

impl Channel {
    /// sample is the property at time t (clamped to its keys).
    fn sample(&self, t: f32) -> Vec4 {
        let n = self.times.len();
        if n == 0 {
            return Vec4::ZERO;
        }
        if t <= self.times[0] || n == 1 {
            return self.values[0];
        }
        if t >= self.times[n - 1] {
            return self.values[n - 1];
        }
        let i = self.times.partition_point(|&k| k <= t) - 1;
        if self.step {
            return self.values[i];
        }
        let k = (t - self.times[i]) / (self.times[i + 1] - self.times[i]).max(1e-9);
        if self.path == Path::Rotation {
            let a = Quat::from_vec4(self.values[i]).normalize();
            let b = Quat::from_vec4(self.values[i + 1]).normalize();
            Vec4::from(a.slerp(b, k))
        } else {
            vec4_lerp(self.values[i], self.values[i + 1], k)
        }
    }
}

impl Skinned {
    pub fn clip(&self, name: &str) -> Option<usize> {
        self.clips.iter().position(|c| c.name == name)
    }

    pub fn node(&self, name: &str) -> Option<usize> {
        self.nodes.iter().position(|n| n.name == name)
    }

    /// globals is every node's transform in the model for a blend of clips,
    /// as three's AnimationMixer mixes them: per property, the playing
    /// clips' values weighted, and what weight is left over (they sum under
    /// one) goes to the node's rest value.
    pub fn globals(&self, blend: &[Blend]) -> Vec<Mat4> {
        // Per node and property: the weighted mix so far and its total weight.
        let mut acc: Vec<[Option<(Vec4, f32)>; 3]> = vec![[None; 3]; self.nodes.len()];
        for &(ci, time, w) in blend {
            if w <= 0. {
                continue;
            }
            for ch in &self.clips[ci].channels {
                let v = ch.sample(time);
                let slot = &mut acc[ch.node][ch.path as usize];
                *slot = Some(match *slot {
                    None => (v, w),
                    Some((cur, cw)) => {
                        let k = w / (cw + w);
                        let mixed = if ch.path == Path::Rotation {
                            Vec4::from(Quat::from_vec4(cur).slerp(Quat::from_vec4(v), k))
                        } else {
                            vec4_lerp(cur, v, k)
                        };
                        (mixed, cw + w)
                    }
                });
            }
        }
        let mut globals = vec![Mat4::IDENTITY; self.nodes.len()];
        for &i in &self.order {
            let n = &self.nodes[i];
            let mix = |slot: Option<(Vec4, f32)>, rest: Vec4, rot: bool| match slot {
                None => rest,
                Some((v, w)) if w >= 1. => v,
                Some((v, w)) => {
                    if rot {
                        Vec4::from(Quat::from_vec4(rest).slerp(Quat::from_vec4(v), w))
                    } else {
                        vec4_lerp(rest, v, w)
                    }
                }
            };
            let t = mix(acc[i][0], n.t.extend(0.), false).truncate();
            let r = Quat::from_vec4(mix(acc[i][1], Vec4::from(n.r), true)).normalize();
            let s = mix(acc[i][2], n.s.extend(0.), false).truncate();
            let local = Mat4::from_scale_rotation_translation(s, r, t);
            globals[i] = match n.parent {
                Some(p) => globals[p] * local,
                None => local,
            };
        }
        globals
    }

    /// skin_into poses the parts' vertices for nodes' global transforms
    /// into `out` (one MeshData per part, made by `rest_meshes`).
    pub fn skin_into(&self, globals: &[Mat4], out: &mut [MeshData]) {
        let palettes: Vec<Vec<Mat4>> = self
            .skins
            .iter()
            .map(|s| {
                s.joints
                    .iter()
                    .zip(&s.ibm)
                    .map(|(&j, ibm)| globals[j] * *ibm)
                    .collect()
            })
            .collect();
        for (part, out) in self.parts.iter().zip(out.iter_mut()) {
            let pal = &palettes[part.skin];
            for (i, v) in out.vertices.iter_mut().enumerate() {
                let rest = &part.rest.vertices[i];
                let (p, n) = (Vec3::from(rest.pos), Vec3::from(rest.normal));
                let (mut sp, mut sn, mut total) = (Vec3::ZERO, Vec3::ZERO, 0.);
                for k in 0..4 {
                    let w = part.weights[i][k];
                    if w <= 0. {
                        continue;
                    }
                    let m = pal[part.joints[i][k] as usize];
                    sp += m.transform_point3(p) * w;
                    sn += m.transform_vector3(n) * w;
                    total += w;
                }
                if total <= 0. {
                    sp = p;
                    sn = n;
                } else {
                    sp /= total;
                }
                v.pos = sp.into();
                v.normal = sn.normalize_or_zero().into();
            }
        }
    }

    /// rest_meshes is the parts as uploaded, in the bind pose; `textures`
    /// maps the model's images to the scene's keys.
    pub fn rest_meshes(&self, textures: &[usize]) -> Vec<MeshData> {
        self.parts
            .iter()
            .map(|p| MeshData {
                texture: p.rest.texture.map(|i| textures[i]),
                ..p.rest.clone()
            })
            .collect()
    }

    /// pose poses `out` (from `rest_meshes`) for a blend of clips.
    pub fn pose(&self, blend: &[Blend], out: &mut [MeshData]) {
        let g = self.globals(blend);
        self.skin_into(&g, out);
    }
}

/// load_skinned reads a .glb with skins and animations. Its meshes are kept
/// in the bind pose, each vertex with the joints it follows.
pub fn load_skinned(
    bytes: &[u8],
    external: &dyn Fn(&str) -> Option<&'static [u8]>,
) -> Result<Skinned> {
    let gltf = gltf::Gltf::from_slice(bytes).context("read the .glb")?;
    let blob = gltf.blob.as_deref();
    let buffers: Vec<&[u8]> = gltf
        .buffers()
        .map(|b| match b.source() {
            gltf::buffer::Source::Bin => blob.unwrap_or(&[]),
            gltf::buffer::Source::Uri(u) => external(u).unwrap_or(&[]),
        })
        .collect();
    let mut images = Vec::new();
    for img in gltf.images() {
        let png: &[u8] = match img.source() {
            gltf::image::Source::View { view, .. } => {
                &buffers[view.buffer().index()][view.offset()..view.offset() + view.length()]
            }
            gltf::image::Source::Uri { uri, .. } => {
                external(uri).with_context(|| format!("image {uri}"))?
            }
        };
        images.push(decode_png(png)?);
    }
    let mut nodes: Vec<SkinNode> = gltf
        .nodes()
        .map(|n| {
            let (t, r, s) = n.transform().decomposed();
            SkinNode {
                name: n.name().unwrap_or("").to_string(),
                parent: None,
                t: Vec3::from(t),
                r: Quat::from_array(r),
                s: Vec3::from(s),
            }
        })
        .collect();
    for n in gltf.nodes() {
        for c in n.children() {
            nodes[c.index()].parent = Some(n.index());
        }
    }
    // Parents before children.
    let mut order = Vec::with_capacity(nodes.len());
    let mut done = vec![false; nodes.len()];
    while order.len() < nodes.len() {
        for i in 0..nodes.len() {
            if !done[i] && nodes[i].parent.is_none_or(|p| done[p]) {
                done[i] = true;
                order.push(i);
            }
        }
    }
    let skins: Vec<Skin> = gltf
        .skins()
        .map(|s| {
            let joints: Vec<usize> = s.joints().map(|j| j.index()).collect();
            let ibm = s
                .reader(|b| Some(buffers[b.index()]))
                .read_inverse_bind_matrices()
                .map(|m| m.map(|m| Mat4::from_cols_array_2d(&m)).collect())
                .unwrap_or_else(|| vec![Mat4::IDENTITY; joints.len()]);
            Skin { joints, ibm }
        })
        .collect();
    let mut parts = Vec::new();
    let mut min = Vec3::splat(f32::MAX);
    let mut max = Vec3::splat(f32::MIN);
    for node in gltf.nodes() {
        let (Some(mesh), Some(skin)) = (node.mesh(), node.skin()) else {
            continue;
        };
        for prim in mesh.primitives() {
            let r = prim.reader(|b| Some(buffers[b.index()]));
            let Some(pos) = r.read_positions() else {
                continue;
            };
            let pos: Vec<[f32; 3]> = pos.collect();
            let normals: Vec<[f32; 3]> = r
                .read_normals()
                .map(|n| n.collect())
                .unwrap_or_else(|| vec![[0., 1., 0.]; pos.len()]);
            let uvs: Vec<[f32; 2]> = r
                .read_tex_coords(0)
                .map(|t| t.into_f32().collect())
                .unwrap_or_else(|| vec![[0., 0.]; pos.len()]);
            let joints: Vec<[u16; 4]> = r
                .read_joints(0)
                .map(|j| j.into_u16().collect())
                .unwrap_or_else(|| vec![[0; 4]; pos.len()]);
            let weights: Vec<[f32; 4]> = r
                .read_weights(0)
                .map(|w| w.into_f32().collect())
                .unwrap_or_else(|| vec![[1., 0., 0., 0.]; pos.len()]);
            let pbr = prim.material().pbr_metallic_roughness();
            let factor = pbr.base_color_factor();
            let tex = pbr
                .base_color_texture()
                .map(|t| t.texture().source().index());
            let vertices: Vec<Vertex> = pos
                .iter()
                .enumerate()
                .map(|(i, p)| Vertex {
                    pos: *p,
                    normal: normals[i],
                    uv: uvs[i],
                    color: factor,
                })
                .collect();
            for p in &pos {
                min = min.min(Vec3::from(*p));
                max = max.max(Vec3::from(*p));
            }
            let indices: Vec<u32> = match r.read_indices() {
                Some(ix) => ix.into_u32().collect(),
                None => (0..pos.len() as u32).collect(),
            };
            parts.push(SkinnedPart {
                rest: MeshData {
                    vertices,
                    indices,
                    texture: tex,
                    transparent: false,
                },
                joints,
                weights,
                skin: skin.index(),
                material: prim.material().name().unwrap_or("").to_string(),
            });
        }
    }
    let mut clips = Vec::new();
    for a in gltf.animations() {
        let mut channels = Vec::new();
        let mut duration = 0f32;
        for ch in a.channels() {
            let r = ch.reader(|b| Some(buffers[b.index()]));
            let times: Vec<f32> = r.read_inputs().map(|i| i.collect()).unwrap_or_default();
            let path = match ch.target().property() {
                gltf::animation::Property::Translation => Path::Translation,
                gltf::animation::Property::Rotation => Path::Rotation,
                gltf::animation::Property::Scale => Path::Scale,
                gltf::animation::Property::MorphTargetWeights => continue,
            };
            let values: Vec<Vec4> = match r.read_outputs() {
                Some(gltf::animation::util::ReadOutputs::Translations(v)) => {
                    v.map(|v| Vec3::from(v).extend(0.)).collect()
                }
                Some(gltf::animation::util::ReadOutputs::Scales(v)) => {
                    v.map(|v| Vec3::from(v).extend(0.)).collect()
                }
                Some(gltf::animation::util::ReadOutputs::Rotations(v)) => {
                    v.into_f32().map(Vec4::from).collect()
                }
                _ => continue,
            };
            let interp = ch.sampler().interpolation();
            // (A cubic spline's keys are triplets; its values are the middle ones.)
            let values = if interp == gltf::animation::Interpolation::CubicSpline {
                values.chunks(3).map(|c| c[1]).collect()
            } else {
                values
            };
            duration = duration.max(times.last().copied().unwrap_or(0.));
            channels.push(Channel {
                node: ch.target().node().index(),
                path,
                times,
                values,
                step: interp == gltf::animation::Interpolation::Step,
            });
        }
        clips.push(Clip {
            name: a.name().unwrap_or("").to_string(),
            duration,
            channels,
        });
    }
    Ok(Skinned {
        parts,
        skins,
        nodes,
        clips,
        images,
        min,
        max,
        order,
    })
}
