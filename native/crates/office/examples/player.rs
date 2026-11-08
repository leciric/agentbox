//! You, standing, walking and running, from the front, the side and the
//! Office's camera, drawn by wgpu offscreen into the PNG named by argv[1].
//! A .glb as argv[2] draws that character instead of the bundled one.
//!
//!   VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json cargo run -p agentbox-office --example player -- out.png [model.glb]

use agentbox_office::gpu::{self, Camera, Gpu, Instance, MeshId};
use agentbox_office::kit::Kit;
use agentbox_office::player::Player;
use glam::{Mat4, Vec3};

fn main() {
    let mut args = std::env::args().skip(1);
    let out = args.next().expect("usage: player <out.png> [model.glb]");
    let model = args
        .next()
        .map(|p| std::fs::read(p).expect("read the model"));
    let mut kit = Kit::default();
    let make = |kit: &mut Kit| match &model {
        Some(b) => Player::from_glb(b, kit).expect("load the model"),
        None => Player::new(kit).expect("load the player"),
    };
    // Standing, walking and running, each at three angles: front, side and
    // from behind as the Office follows you.
    let poses = [(0., 0.9), (2.8, 0.35), (6., 0.4)];
    let yaws = [
        0.4f32,
        std::f32::consts::FRAC_PI_2,
        std::f32::consts::PI + 0.4,
    ];
    let mut g = Gpu::new().expect("gpu");
    let mut players = Vec::new();
    for (speed, at) in poses {
        for _ in yaws {
            let mut p = make(&mut kit);
            let mut t = 0.;
            while t < 1.5 + at {
                p.stride(speed, t, true);
                t += 1. / 60.;
            }
            players.push(p);
        }
    }
    let tex: Vec<usize> = kit
        .textures
        .iter()
        .map(|i| g.add_texture(i.width, i.height, &i.rgba))
        .collect();
    let v = |x: f32, z: f32| gpu::Vertex {
        pos: [x, 0., z],
        normal: [0., 1., 0.],
        uv: [0., 0.],
        color: [0.93, 0.90, 0.85, 1.],
    };
    let floor = g.add_mesh(&gpu::MeshData {
        vertices: vec![v(-1., -1.), v(1., -1.), v(1., 1.), v(-1., 1.)],
        indices: vec![0, 2, 1, 0, 3, 2],
        texture: None,
        transparent: false,
    });
    let mut draws: Vec<(MeshId, Vec<Instance>)> = vec![(
        floor,
        vec![Instance::new(
            Mat4::from_scale(Vec3::new(12., 1., 9.)),
            [1.; 4],
        )],
    )];
    for (i, p) in players.iter().enumerate() {
        let (row, col) = (i / 3, i % 3);
        let at = Mat4::from_translation(Vec3::new(
            (col as f32 - 1.) * 3.2,
            0.,
            (row as f32 - 1.) * 3.,
        )) * Mat4::from_rotation_y(yaws[col])
            * p.matrix();
        for m in &p.meshes {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            let id = g.add_mesh(&m);
            draws.push((id, vec![Instance::new(at, [1., 1., 1., 1.])]));
        }
    }
    let (w, h) = (1440u32, 1100u32);
    let eye = Vec3::new(0., 9., 15.);
    let cam = Camera {
        view_proj: Mat4::perspective_rh(36f32.to_radians(), w as f32 / h as f32, 0.1, 100.)
            * Mat4::look_at_rh(eye, Vec3::new(0., 0.6, 0.), Vec3::Y),
        eye,
    };
    let frame = g
        .render(w, h, &cam, [0.17, 0.16, 0.2], &draws)
        .expect("render");
    let mut rgba = Vec::with_capacity(frame.bgra.len());
    for px in frame.bgra.chunks(4) {
        rgba.extend([px[2], px[1], px[0], 255]);
    }
    let f = std::fs::File::create(&out).expect("create the png");
    let mut enc = png::Encoder::new(std::io::BufWriter::new(f), frame.width, frame.height);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header()
        .and_then(|mut w| w.write_image_data(&rgba))
        .expect("write the png");
}
