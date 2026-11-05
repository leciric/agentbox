//! The campus as the native Office draws it (desks.rs): three projects of
//! twelve or so agents in mixed states, the overview, then a walk to a desk.
//! Writes argv[1] (the overview) and argv[1] with `-desk` before `.png`.
//!
//!   VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json cargo run --release -p agentbox-office --example office -- /t/office.png

use agentbox_office::desks::{AreaLabel, SceneInput, build};
use agentbox_office::gpu::{Camera, Gpu, Instance, MeshId};
use agentbox_office::kit::{Draw, Kit};
use agentbox_office::layout;
use agentbox_office::model::{AreaModel, DeskModel, DeskTone, LeadModel, LeadTone, OfficeModel};
use agentbox_office::sim::Sim;
use agentbox_office::textures::{LineTone, Screen, ScreenLine};
use std::collections::HashMap;

fn area(project: &str, n: usize, hue: f64, off: usize) -> AreaModel {
    let tones = [DeskTone::Working, DeskTone::Needs, DeskTone::Finished, DeskTone::Error, DeskTone::Idle, DeskTone::Stopped, DeskTone::Working];
    AreaModel {
        project: project.into(),
        label: project.into(),
        hue,
        lead: LeadModel { tone: LeadTone::Working, line: None },
        desks: (0..n)
            .map(|i| DeskModel {
                id: format!("{project}/agent-{}", i + 1),
                project: project.into(),
                name: format!("agent-{}", i + 1),
                title: ["Fix the login redirect", "Paginate tasks", "Dark mode", "Flaky CI", "Docs pass", "Upgrade deps"][(i + off) % 6].into(),
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
    let mut enc = png::Encoder::new(std::io::BufWriter::new(std::fs::File::create(path)?), f.width, f.height);
    enc.set_color(png::ColorType::Rgba);
    enc.set_depth(png::BitDepth::Eight);
    enc.write_header()?.write_image_data(&rgba)?;
    Ok(())
}

fn main() -> anyhow::Result<()> {
    let out = std::env::args().nth(1).unwrap_or_else(|| "office.png".into());
    let (w, h) = (1440u32, 900u32);
    let areas = vec![area("agentbox", 5, 260., 0), area("storefront", 4, 28., 2), area("quill", 3, 170., 4)];
    let mut screens = HashMap::new();
    let mut labels = HashMap::new();
    for a in &areas {
        labels.insert(a.project.clone(), AreaLabel { name: a.project.clone(), sub: String::new(), count: a.desks.len() as i64 });
        for d in &a.desks {
            screens.insert(
                d.id.clone(),
                Screen {
                    tone: d.tone,
                    head: format!("{:?}", d.tone),
                    title: d.title.clone(),
                    lines: vec![ScreenLine { text: d.branch.clone(), tone: LineTone::Code }, ScreenLine { text: "Running the tests for the list view".into(), tone: LineTone::Plain }],
                },
            );
        }
    }
    let input = SceneInput { model: OfficeModel { areas, ..Default::default() }, screens, you: "You".into(), area_labels: labels, ..Default::default() };
    let lay = layout::layout(&input.model.areas);
    let mut kit = Kit::new();
    let mut built = build(&input, &lay, &mut kit);
    let mut sim = Sim::new();
    sim.set_layout(lay, built.walls(), None);
    sim.hits = built.hits.clone();

    let mut gpu = Gpu::new()?;
    eprintln!("adapter: {}", gpu.adapter_name);
    let tex: Vec<usize> = kit.textures.iter().map(|i| gpu.add_texture(i.width, i.height, &i.rgba)).collect();
    let meshes: Vec<MeshId> = kit
        .meshes
        .iter()
        .map(|m| {
            let mut m = m.clone();
            m.texture = m.texture.map(|t| tex[t]);
            gpu.add_mesh(&m)
        })
        .collect();

    let mut shot = |sim: &mut Sim, built: &mut agentbox_office::desks::Built, kit: &mut Kit, secs: f32, path: &str| -> anyhow::Result<()> {
        let mut t = 0.;
        while t < secs {
            sim.step(1. / 30.);
            t += 1. / 30.;
        }
        let mut draws: Vec<Draw> = Vec::new();
        built.draws(sim, 3., false, kit, &mut draws);
        for k in std::mem::take(&mut kit.changed) {
            let i = &kit.textures[k];
            gpu.replace_texture(tex[k], i.width, i.height, &i.rgba);
        }
        let mut by: HashMap<usize, Vec<Instance>> = HashMap::new();
        for d in &draws {
            by.entry(d.mesh.0).or_default().push(Instance::new(d.at, d.tint));
        }
        let list: Vec<(MeshId, Vec<Instance>)> = by.into_iter().map(|(k, v)| (meshes[k], v)).collect();
        let cam = Camera { view_proj: sim.camera.view_proj(), eye: sim.camera.eye };
        let frame = gpu.render(w, h, &cam, [0.84, 0.90, 0.95], &list)?;
        save(path, &frame)?;
        eprintln!("{path}: {} draws, {} labels visible", draws.len(), built.labels(sim).iter().filter(|l| l.visible).count());
        Ok(())
    };
    shot(&mut sim, &mut built, &mut kit, 4., &out)?;
    let desk = out.replace(".png", "-desk.png");
    sim.walk_to("agentbox/agent-2");
    shot(&mut sim, &mut built, &mut kit, 6., &desk)?;
    Ok(())
}
