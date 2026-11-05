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
        png::ColorType::Rgb => buf.chunks(3).flat_map(|p| [p[0], p[1], p[2], 255]).collect(),
        png::ColorType::GrayscaleAlpha => buf.chunks(2).flat_map(|p| [p[0], p[0], p[0], p[1]]).collect(),
        png::ColorType::Grayscale => buf.iter().flat_map(|&v| [v, v, v, 255]).collect(),
        png::ColorType::Indexed => anyhow::bail!("indexed PNG left unexpanded"),
    };
    Ok(Image { width: info.width, height: info.height, rgba })
}

/// load reads a .glb; `external` resolves an image it names by URI (Kenney's
/// models point at Textures/colormap.png beside them).
pub fn load(bytes: &[u8], external: &dyn Fn(&str) -> Option<&'static [u8]>) -> Result<Model> {
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
            gltf::image::Source::Uri { uri, .. } => external(uri).with_context(|| format!("image {uri}"))?,
        };
        images.push(decode_png(png)?);
    }
    let mut by_tex: Vec<(Option<usize>, MeshData)> = Vec::new();
    let mut min = Vec3::splat(f32::MAX);
    let mut max = Vec3::splat(f32::MIN);
    let scene = gltf.default_scene().or_else(|| gltf.scenes().next()).context("no scene")?;
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
            let Some(pos) = r.read_positions() else { continue };
            let pos: Vec<[f32; 3]> = pos.collect();
            let normals: Vec<[f32; 3]> = r.read_normals().map(|n| n.collect()).unwrap_or_else(|| vec![[0., 1., 0.]; pos.len()]);
            let uvs: Vec<[f32; 2]> = r.read_tex_coords(0).map(|t| t.into_f32().collect()).unwrap_or_else(|| vec![[0., 0.]; pos.len()]);
            let colors: Option<Vec<[f32; 4]>> = r.read_colors(0).map(|c| c.into_rgba_f32().collect());
            let mat = prim.material();
            let pbr = mat.pbr_metallic_roughness();
            let factor = pbr.base_color_factor();
            let tex = pbr.base_color_texture().map(|t| t.texture().source().index());
            let at = match by_tex.iter().position(|(t, _)| *t == tex) {
                Some(i) => i,
                None => {
                    by_tex.push((tex, MeshData { texture: tex, ..Default::default() }));
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
                    color: [c[0] * factor[0], c[1] * factor[1], c[2] * factor[2], c[3] * factor[3]],
                });
            }
            match r.read_indices() {
                Some(ix) => out.indices.extend(ix.into_u32().map(|i| i + base)),
                None => out.indices.extend(base..base + pos.len() as u32),
            }
        }
    }
    Ok(Model { meshes: by_tex.into_iter().map(|(_, m)| m).collect(), images, min, max })
}
