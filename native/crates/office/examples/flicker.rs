//! Flicker: the campus drawn again and again with the camera nudged 1 mm at
//! a time, counting the pixels that change colour sharply between one frame
//! and the next. Real motion of a millimetre barely moves anything; floors
//! fighting each other for depth, or textures shimmering, flip whole patches.
//! Prints the share of flipped pixels for the campus view and a room's, and
//! writes argv[1] (default /t/flicker.png): where they flipped, in red.
//!
//!   VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json cargo run --release -p agentbox-office --example flicker

use agentbox_office::desks::{AreaLabel, SceneInput, build};
use agentbox_office::gpu::{Camera, Gpu, Instance, MeshId};
use agentbox_office::kit::{Draw, Kit};
use agentbox_office::layout;
use agentbox_office::model::{AreaModel, DeskModel, DeskTone, LeadModel, LeadTone, OfficeModel};
use agentbox_office::sim::Sim;
use agentbox_office::textures::{LineTone, Screen, ScreenLine};
use std::collections::HashMap;

fn area(project: &str, n: usize, hue: f64, off: usize) -> AreaModel {
    let tones = [
        DeskTone::Working,
        DeskTone::Needs,
        DeskTone::Finished,
        DeskTone::Error,
        DeskTone::Idle,
        DeskTone::Stopped,
        DeskTone::Working,
    ];
    AreaModel {
        project: project.into(),
        label: project.into(),
        hue,
        lead: LeadModel {
            tone: LeadTone::Working,
            line: None,
        },
        desks: (0..n)
            .map(|i| DeskModel {
                id: format!("{project}/agent-{}", i + 1),
                project: project.into(),
                name: format!("agent-{}", i + 1),
                title: [
                    "Fix the login redirect",
                    "Paginate tasks",
                    "Dark mode",
                    "Flaky CI",
                    "Docs pass",
                    "Upgrade deps",
                ][(i + off) % 6]
                    .into(),
                branch: format!("agentbox/task-{i}"),
                ai: ["claude", "codex", "cursor", "opencode", "gemini"][(i + off) % 5].into(),
                tone: tones[(i + off) % 7],
                pr: None,
                changes: None,
                question: None,
                last_active: None,
            })
            .collect(),
    }
}

fn save(path: &str, f: &agentbox_office::gpu::Frame) -> anyhow::Result<()> {
    let mut rgba = f.bgra.clone();
    for p in rgba.chunks_mut(4) {
        p.swap(0, 2);
    }
    let mut enc = png::Encoder::new(
        std::io::BufWriter::new(std::fs::File::create(path)?),
        f.width,
        f.height,
    );
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header()?.write_image_data(&rgba)?;
    Ok(())
}

fn main() -> anyhow::Result<()> {
    let out = std::env::args().nth(1).unwrap_or_else(|| "/t/flicker.png".into());
    let (w, h) = (1440u32, 813u32);
    let areas = vec![
        area("agentbox", 5, 260., 0),
        area("storefront", 4, 28., 2),
        area("quill", 3, 170., 4),
    ];
    let input = SceneInput {
        model: OfficeModel {
            areas,
            ..Default::default()
        },
        you: "You".into(),
        ..Default::default()
    };
    let lay = layout::layout(&input.model.areas);
    let mut kit = Kit::new();
    let mut built = build(&input, &lay, &mut kit);
    let mut sim = Sim::new();
    sim.size = glam::Vec2::new(w as f32, h as f32);
    sim.set_layout(lay, built.walls(), None);
    sim.hits = built.hits.clone();
    let mut gpu = Gpu::new()?;
    eprintln!("adapter: {}", gpu.adapter_name);
    let tex: Vec<usize> = kit
        .textures
        .iter()
        .map(|i| gpu.add_texture(i.width, i.height, &i.rgba))
        .collect();
    let meshes: Vec<MeshId> = kit
        .meshes
        .iter()
        .map(|m| {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            gpu.add_mesh(&m)
        })
        .collect();
    let mut views: Vec<(&str, f32)> = vec![("campus", 4.)];
    let mut mask_out: Option<agentbox_office::gpu::Frame> = None;
    let mut first = true;
    while let Some((name, secs)) = views.first().copied() {
        views.remove(0);
        let mut t = 0.;
        while t < secs {
            sim.step(1. / 30.);
            t += 1. / 30.;
        }
        let mut draws: Vec<Draw> = Vec::new();
        built.draws(&sim, 3., false, &mut kit, &mut draws);
        let mut by: HashMap<usize, Vec<Instance>> = HashMap::new();
        for d in &draws {
            by.entry(d.mesh.0).or_default().push(Instance::new(d.at, d.tint));
        }
        let list: Vec<(MeshId, Vec<Instance>)> = by.into_iter().map(|(k, v)| (meshes[k], v)).collect();
        let c = sim.camera;
        let side = (c.target - c.eye).cross(glam::Vec3::Y).normalize();
        let mut prev: Option<Vec<u8>> = None;
        // Each pixel's last change in brightness: flicker is a change undone
        // by the next (A, B, A), where motion moves on (A, B, C).
        let mut last = vec![0i32; (w * h) as usize];
        let mut flips = 0u64;
        let mut mask = vec![0u8; (w * h) as usize];
        const N: usize = 16;
        for i in 0..N {
            let o = side * (i as f32 * 0.001);
            let view = glam::Mat4::look_at_rh(c.eye + o, c.target + o, glam::Vec3::Y);
            let cam = Camera {
                view_proj: c.proj * view,
                eye: c.eye + o,
            };
            let f = gpu.render(w, h, &cam, [0.73, 0.82, 0.88], &list)?;
            if let Some(p) = &prev {
                let lum = |px: &[u8]| px[0] as i32 * 29 + px[1] as i32 * 150 + px[2] as i32 * 77;
                for (k, (a, b)) in p.chunks(4).zip(f.bgra.chunks(4)).enumerate() {
                    let d = (lum(b) - lum(a)) / 256;
                    if d.abs() > 24 && last[k].abs() > 24 && d.signum() != last[k].signum() {
                        flips += 1;
                        mask[k] = mask[k].saturating_add(1);
                    }
                    if d.abs() > 4 {
                        last[k] = d;
                    }
                }
            }
            if i == N - 1 && first {
                let mut f2 = agentbox_office::gpu::Frame { width: f.width, height: f.height, bgra: f.bgra.clone() };
                for (k, px) in f2.bgra.chunks_mut(4).enumerate() {
                    if mask[k] > 0 {
                        px.copy_from_slice(&[0, 0, 255, 255]);
                    }
                }
                mask_out = Some(f2);
            }
            prev = Some(f.bgra);
        }
        let flickering = mask.iter().filter(|m| **m >= 3).count();
        println!(
            "{name}: {:.3}% of pixels flicker (a sharp change undone) per 1 mm step, {:.3}% 3+ times in {} steps",
            flips as f64 / ((N - 1) as f64 * (w * h) as f64) * 100.,
            flickering as f64 / (w * h) as f64 * 100.,
            N - 1
        );
        if first {
            sim.walk_to("agentbox/agent-2");
            views.push(("room", 6.));
            first = false;
        }
    }
    if let Some(f) = mask_out {
        save(&out, &f)?;
    }
    Ok(())
}
