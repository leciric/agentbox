//! A headless check of the room diorama: builds one room (decor.rs, props.rs),
//! draws it with wgpu from #668's overview angle (3/4, above the front right)
//! and writes a PNG.
//!
//! cargo run --release -p agentbox-office --example room -- /t/room.png [desks=4] [dist]

use agentbox_office::decor::diorama;
use agentbox_office::gpu::{Camera, Gpu, Instance, MeshId};
use agentbox_office::kit::Kit;
use agentbox_office::layout::{AreaIn, room_layout};
use glam::{Mat4, Vec3};

fn main() -> anyhow::Result<()> {
    let args: Vec<String> = std::env::args().collect();
    let out = args.get(1).cloned().unwrap_or_else(|| "room.png".into());
    let desks: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(4);
    let (w, h) = (1440u32, 900u32);

    let l = room_layout(&[AreaIn { project: "agentbox".into(), desks: (0..desks).map(|i| format!("d{i}")).collect(), no_lead: false, floor: None }]);
    let mut kit = Kit::new();
    let dio = diorama(&l, "agentbox", Some(260.), &mut kit);

    let mut gpu = Gpu::new()?;
    eprintln!("adapter: {}", gpu.adapter_name);
    // Textures in kit order; a mesh's `texture` is a kit key, mapped to the gpu's.
    let tex: Vec<usize> = kit.textures.iter().map(|i| gpu.add_texture(i.width, i.height, &i.rgba)).collect();
    let meshes: Vec<MeshId> = dio
        .solid
        .finish()
        .into_iter()
        .map(|mut m| {
            m.texture = m.texture.map(|t| tex[t]);
            gpu.add_mesh(&m)
        })
        .collect();
    let draws: Vec<(MeshId, Vec<Instance>)> = meshes.iter().map(|m| (*m, vec![Instance::new(Mat4::IDENTITY, [1.; 4])])).collect();

    // The overview's camera: yaw 0.4, pitch 0.62, 24 degrees.
    let (rw, rd) = (l.room.x1 as f32, l.room.z1 as f32);
    let target = Vec3::new(rw / 2., 0.8, rd / 2.);
    let dist: f32 = args.get(3).and_then(|s| s.parse().ok()).unwrap_or(rw.max(rd) * 1.7);
    let (yaw, pitch) = (0.4f32, 0.62f32);
    let eye = target + Vec3::new(yaw.sin() * pitch.cos(), pitch.sin(), yaw.cos() * pitch.cos()) * dist;
    let cam = Camera { view_proj: Mat4::perspective_rh(24f32.to_radians(), w as f32 / h as f32, 0.5, 200.) * Mat4::look_at_rh(eye, target, Vec3::Y), eye };
    let frame = gpu.render(w, h, &cam, [0.84, 0.90, 0.95], &draws)?;

    let mut rgba = frame.bgra.clone();
    for p in rgba.chunks_mut(4) {
        p.swap(0, 2);
    }
    let file = std::io::BufWriter::new(std::fs::File::create(&out)?);
    let mut enc = png::Encoder::new(file, frame.width, frame.height);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header()?.write_image_data(&rgba)?;
    eprintln!("room {rw:.1} x {rd:.1}, {desks} desks -> {out}");
    Ok(())
}
