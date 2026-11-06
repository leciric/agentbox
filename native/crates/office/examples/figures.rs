//! Figures: a row of Boxies (each tool, a lead, every tone) and you mid-walk,
//! drawn by wgpu offscreen at 1440x900 into the PNG named by argv[1].
//!
//!   VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json cargo run -p agentbox-office --example figures -- out.png

use agentbox_office::figures::{self, Tool};
use agentbox_office::gpu::{self, Camera, Gpu, Instance, MeshId};
use agentbox_office::kit::Kit;
use agentbox_office::model::DeskTone;
use agentbox_office::player::Player;
use agentbox_office::rig::Rig;
use glam::{Mat4, Vec3};

const TONES: [DeskTone; 6] = [
    DeskTone::Needs,
    DeskTone::Error,
    DeskTone::Finished,
    DeskTone::Working,
    DeskTone::Idle,
    DeskTone::Stopped,
];

/// A rig uploaded: its meshes by node.
struct Placed {
    rig_meshes: Vec<(usize, MeshId)>,
}

fn upload(g: &mut Gpu, rig: &Rig, tex: &[usize]) -> Placed {
    let mut rig_meshes = Vec::new();
    for (i, n) in rig.nodes.iter().enumerate() {
        for m in &n.meshes {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            rig_meshes.push((i, g.add_mesh(&m)));
        }
    }
    Placed { rig_meshes }
}

fn draw(draws: &mut Vec<(MeshId, Vec<Instance>)>, p: &Placed, rig: &Rig, root: Mat4) {
    let world = rig.world(root);
    for &(node, id) in &p.rig_meshes {
        if world[node].1 {
            let a = rig.nodes[node].opacity;
            draws.push((id, vec![Instance::new(world[node].0, [1., 1., 1., a])]));
        }
    }
}

fn main() {
    let out = std::env::args().nth(1).expect("usage: figures <out.png>");
    let mut kit = Kit::default();
    let mut figs: Vec<(figures::Mascot, DeskTone, Mat4)> = Vec::new();
    let spacing = 2.4;
    // Back row: every tone, wearing everything (places in the room).
    for (i, tone) in TONES.into_iter().enumerate() {
        let tool = Tool::ALL[i % 5].name();
        let m = figures::mascot(
            &format!("agent-{i}"),
            Some(tool),
            false,
            Some(i as u32 + 1),
            &mut kit,
        );
        figs.push((
            m,
            tone,
            Mat4::from_translation(Vec3::new((i as f32 - 2.5) * spacing, 0., -3.5)),
        ));
    }
    // Middle row: each tool, and the lead.
    for (i, tool) in Tool::ALL.into_iter().enumerate() {
        let m = figures::mascot(
            &format!("tool-{i}"),
            Some(tool.name()),
            false,
            Some(i as u32 + 6),
            &mut kit,
        );
        figs.push((
            m,
            DeskTone::Working,
            Mat4::from_translation(Vec3::new((i as f32 - 2.5) * spacing, 0., 0.)),
        ));
    }
    figs.push((
        figures::mascot("lead", Some("claude"), true, None, &mut kit),
        DeskTone::Idle,
        Mat4::from_translation(Vec3::new(2.5 * spacing, 0., 0.)),
    ));
    // You, mid-walk, and the stand-in beside you.
    let mut player = Player::new(&mut kit).expect("player");
    let mut walker = figures::walker(&mut kit);
    let mut t = 0.;
    while t < 2.5 {
        for (m, tone, _) in &mut figs {
            m.pose(*tone, t, true, None);
        }
        player.stride(1.8, t, true);
        walker.stride(1.8, t, true);
        t += 1. / 30.;
    }

    let mut g = Gpu::new().expect("gpu");
    eprintln!("adapter: {}", g.adapter_name);
    let tex: Vec<usize> = kit
        .textures
        .iter()
        .map(|i| g.add_texture(i.width, i.height, &i.rgba))
        .collect();
    let placed: Vec<Placed> = figs
        .iter()
        .map(|(m, _, _)| upload(&mut g, &m.rig, &tex))
        .collect();
    let walker_up = upload(&mut g, &walker.rig, &tex);
    let player_ids: Vec<MeshId> = player
        .meshes
        .iter()
        .map(|m| {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            g.add_mesh(&m)
        })
        .collect();
    for (id, m) in player_ids.iter().zip(&player.meshes) {
        g.update_mesh(*id, &m.vertices);
    }
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

    let mut draws = vec![(
        floor,
        vec![Instance::new(
            Mat4::from_scale(Vec3::new(9., 1., 9.)) * Mat4::from_translation(Vec3::new(0., 0., 0.)),
            [1.; 4],
        )],
    )];
    for ((m, _, root), p) in figs.iter().zip(&placed) {
        draw(&mut draws, p, &m.rig, *root);
    }
    draw(
        &mut draws,
        &walker_up,
        &walker.rig,
        Mat4::from_translation(Vec3::new(-1.6 * spacing + 0.5, 0., 3.6))
            * Mat4::from_scale(Vec3::splat(2.)),
    );
    let you = Mat4::from_translation(Vec3::new(0.2, 0., 3.6)) * player.matrix();
    for id in &player_ids {
        draws.push((
            *id,
            vec![Instance::new(you, [1., 1., 1., player.opacity()])],
        ));
    }
    let (w, h) = (1440u32, 900u32);
    let eye = Vec3::new(0., 7.5, 15.);
    let cam = Camera {
        view_proj: Mat4::perspective_rh(34f32.to_radians(), w as f32 / h as f32, 0.1, 100.)
            * Mat4::look_at_rh(eye, Vec3::new(0., 1.6, 0.), Vec3::Y),
        eye,
    };
    let frame = g
        .render(w, h, &cam, [0.17, 0.16, 0.2], &draws)
        .expect("render");
    let mut rgba = Vec::with_capacity(frame.bgra.len());
    for p in frame.bgra.chunks(4) {
        rgba.extend([p[2], p[1], p[0], 255]);
    }
    let f = std::fs::File::create(&out).expect("create");
    let mut enc = png::Encoder::new(std::io::BufWriter::new(f), frame.width, frame.height);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header().unwrap().write_image_data(&rgba).unwrap();
    eprintln!("wrote {out}");
}
