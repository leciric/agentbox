//! You, as Quaternius's Hoodie Character (Ultimate Animated Character Pack,
//! CC0, assets/quaternius/License.txt): a developer in a hoodie with its own
//! idle, walk and run, played at the speed you move. Drawn a little
//! stylised: its head a size up, so a face still reads from the Office's
//! camera, and its hoodie in AgentBox's violet. The hand-drawn
//! [`crate::figures::Walker`] stands in if it can't be read.
//!
//! The figure is skinned on the CPU: [`Player::stride`] poses the skeleton and
//! rewrites `meshes`' vertices, which the scene sends to the GPU each frame
//! (`Gpu::update_mesh`) and draws at [`Player::matrix`] under wherever you
//! stand.

use crate::glb::{self, Skinned};
use crate::gpu::MeshData;
use crate::kit::Kit;
use anyhow::Result;
use glam::{Mat4, Vec3};

/// How tall you stand, in metres: about a Boxie's height, a toy among toys.
pub const HEIGHT: f32 = 2.3;
/// How much bigger the head is than the model's own, and how much sturdier
/// the build (across and front to back): a toy's proportions, which read
/// from the Office's camera and sit with the Boxies, rather than a person's.
const HEAD: f32 = 1.5;
const BUILD: f32 = 1.25;
/// The hoodie's colour (linear RGB): AgentBox's violet.
const HOODIE: [f32; 3] = [0.16, 0.07, 0.42];

pub struct Player {
    skinned: Skinned,
    /// The parts as they're posed now, their `texture` the kit's key for
    /// Kenney's colormap: upload once, then `update_mesh` with each one's
    /// `vertices` after `stride`.
    pub meshes: Vec<MeshData>,
    idle: Option<usize>,
    walk: Option<usize>,
    sprint: Option<usize>,
    /// Metres per unit of the model.
    k: f32,
    base_y: f32,
    walk_cycle: f32,
    run_cycle: f32,
    last_t: Option<f32>,
    // Moving (0 standing, 1 moving), running (0 walking, 1 running), where in
    // the cycle the feet are, the idle's time, and the lean.
    move_k: f32,
    run_k: f32,
    phase: f32,
    idle_t: f32,
    last_yaw: Option<f32>,
    roll: f32,
    lean: f32,
    // The last speed, and a lean with how it's changing: forward setting
    // off, back coming to a stop.
    last_speed: f32,
    surge: f32,
    motion: bool,
    /// Which way you face (radians about y): set it before `stride`, which
    /// leans you into a turn.
    pub yaw: f32,
    opacity: f32,
}

impl Player {
    pub fn new(kit: &mut Kit) -> Result<Player> {
        Self::from_glb(crate::assets::CHARACTER, kit)
    }

    /// from_glb is you as another character: a skinned .glb with an idle, a
    /// walk and a run (or sprint) among its clips.
    pub fn from_glb(bytes: &[u8], kit: &mut Kit) -> Result<Player> {
        let mut skinned = glb::load_skinned(bytes, &crate::assets::external)?;
        let textures: Vec<usize> = skinned
            .images
            .iter()
            .enumerate()
            .map(|(i, img)| {
                kit.texture(&format!("player:{i}"), || glb::Image {
                    width: img.width,
                    height: img.height,
                    rgba: img.rgba.clone(),
                })
            })
            .collect();
        // Its walk and sprint as they are, but for their hop: the root's bounce
        // goes, so you glide over the floor rather than skip along it.
        let root = skinned
            .nodes
            .iter()
            .position(|n| n.name.eq_ignore_ascii_case("root"));
        for c in &mut skinned.clips {
            c.channels
                .retain(|ch| !(Some(ch.node) == root && ch.path == glb::Path::Translation));
        }
        // A head a size up: the clips mustn't scale it back.
        if let Some(h) = skinned.node("Head") {
            for c in &mut skinned.clips {
                c.channels
                    .retain(|ch| !(ch.node == h && ch.path == glb::Path::Scale));
            }
            skinned.nodes[h].s *= HEAD;
        }
        for part in &mut skinned.parts {
            if part.material == "Purple" {
                for v in &mut part.rest.vertices {
                    v.color = [HOODIE[0], HOODIE[1], HOODIE[2], v.color[3]];
                }
            }
        }
        let idle = find_clip(&skinned, &["idle"]);
        let walk = find_clip(&skinned, &["walk", "walking"]);
        let sprint = find_clip(&skinned, &["sprint", "run", "running"]);
        let mut meshes = skinned.rest_meshes(&textures);
        // Its size as it stands (its idle's first frame), since a model's
        // bind pose needn't be in the units it's posed in.
        if let Some(c) = idle {
            skinned.pose(&[(c, 0., 1.)], &mut meshes);
        }
        let (lo, hi) = extent(&meshes);
        let k = HEIGHT / (hi.y - lo.y).max(0.01);
        // How far each cycle carries you, in metres: two steps, each as long
        // as a foot sweeps under you. Playing it at speed over that, the
        // feet stay where they're put.
        let cycle = |c: Option<usize>, fallback: f32| {
            c.and_then(|c| stride_of(&skinned, c))
                .map(|m| m * k)
                .unwrap_or(fallback)
        };
        // (Drawn a size deeper, its steps are that much longer.)
        let walk_cycle = cycle(walk, 1.6) * BUILD;
        let run_cycle = (cycle(sprint, 2.6) * BUILD).max(walk_cycle);
        let mut p = Player {
            idle,
            walk,
            sprint,
            skinned,
            meshes,
            k,
            base_y: -lo.y * k,
            walk_cycle,
            run_cycle,
            last_t: None,
            move_k: 0.,
            run_k: 0.,
            phase: 0.,
            idle_t: 0.,
            last_yaw: None,
            roll: 0.,
            lean: 0.,
            last_speed: 0.,
            surge: 0.,
            motion: false,
            yaw: 0.,
            opacity: 1.,
        };
        p.stride(0., 0., false);
        Ok(p)
    }

    /// stride poses you for moving at `speed` (m/s) at time t (seconds):
    /// standing to walking with speed, walking to running past a brisk walk.
    pub fn stride(&mut self, speed: f32, t: f32, motion: bool) {
        let dt = match self.last_t {
            None => 0.,
            Some(l) => (t - l).clamp(0., 0.1),
        };
        self.last_t = Some(t);
        self.motion = motion;
        let ease = |from: f32, to: f32, rate: f32| {
            if motion {
                from + (to - from) * (1. - (-dt * rate).exp())
            } else {
                to
            }
        };
        self.move_k = ease(self.move_k, (speed / 0.9).min(1.), 9.);
        self.run_k = ease(self.run_k, ((speed - 3.4) / 1.4).clamp(0., 1.), 6.);
        // Both cycles share one phase, so blending them never crosses the feet.
        let cycle = self.walk_cycle + (self.run_cycle - self.walk_cycle) * self.run_k;
        if motion {
            self.phase = (self.phase + speed * dt / cycle) % 1.;
        }
        if motion {
            self.idle_t += dt;
        }
        let dur = |c: Option<usize>| c.map_or(1., |c| self.skinned.clips[c].duration.max(1e-6));
        let mut blend = Vec::new();
        if let Some(c) = self.idle {
            blend.push((c, self.idle_t % dur(self.idle), 1. - self.move_k));
        }
        if let Some(c) = self.walk {
            blend.push((
                c,
                self.phase * dur(self.walk),
                self.move_k * (1. - self.run_k),
            ));
        }
        if let Some(c) = self.sprint {
            blend.push((c, self.phase * dur(self.sprint), self.move_k * self.run_k));
        }
        self.skinned.pose(&blend, &mut self.meshes);
        // A small lean into a turn, and forward into a run.
        let mut turn_rate = 0.;
        if let Some(l) = self.last_yaw
            && dt > 0.
        {
            let d = self.yaw - l;
            turn_rate = d.sin().atan2(d.cos()) / dt;
        }
        self.last_yaw = Some(self.yaw);
        self.roll = ease(
            self.roll,
            (-turn_rate * speed * 0.012).clamp(-0.16, 0.16),
            8.,
        );
        let accel = if dt > 0. {
            (speed - self.last_speed) / dt
        } else {
            0.
        };
        self.last_speed = speed;
        self.surge = ease(self.surge, (accel * 0.015).clamp(-0.12, 0.09), 7.);
        self.lean = if motion {
            self.move_k * (0.04 + 0.08 * self.run_k) + self.surge
        } else {
            0.
        };
    }

    /// matrix places the posed meshes under you: scaled to metres, standing on
    /// the floor, leaning (three's model.rotation, Euler XYZ).
    pub fn matrix(&self) -> Mat4 {
        let roll = if self.motion { self.roll } else { 0. };
        crate::mesh::trs(
            0.,
            self.base_y,
            0.,
            self.lean,
            0.,
            roll,
            Vec3::new(self.k * BUILD, self.k, self.k * BUILD),
        )
    }

    /// fade is how much of you shows: the scene draws with this alpha as the
    /// tint's, and not at all when `visible` is false.
    pub fn fade(&mut self, opacity: f32) {
        self.opacity = opacity;
    }

    pub fn opacity(&self) -> f32 {
        self.opacity
    }

    pub fn visible(&self) -> bool {
        self.opacity > 0.2
    }
}

/// find_clip is the first clip called one of `names` (any case), alone or
/// after an armature's name ("CharacterArmature|Run").
fn find_clip(s: &Skinned, names: &[&str]) -> Option<usize> {
    names.iter().find_map(|n| {
        s.clips.iter().position(|c| {
            let last = c.name.rsplit('|').next().unwrap_or(&c.name);
            last.eq_ignore_ascii_case(n)
        })
    })
}

/// extent is the meshes' bounds, as posed.
fn extent(meshes: &[MeshData]) -> (Vec3, Vec3) {
    let mut lo = Vec3::splat(f32::MAX);
    let mut hi = Vec3::splat(f32::MIN);
    for v in meshes.iter().flat_map(|m| &m.vertices) {
        lo = lo.min(Vec3::from(v.pos));
        hi = hi.max(Vec3::from(v.pos));
    }
    (lo, hi)
}

/// stride_of is how far a clip's cycle carries the figure, in the model's
/// units: two steps, each as long as the feet get apart front to back, so
/// how far one foot goes from in front of the other to behind it.
fn stride_of(s: &Skinned, clip: usize) -> Option<f32> {
    let foot = |side: &[&str]| {
        s.nodes.iter().position(|n| {
            let l = n.name.to_ascii_lowercase();
            l.contains("foot") && side.iter().any(|x| l.ends_with(x))
        })
    };
    let (l, r) = (
        foot(&["l", ".l", "_l", "left"])?,
        foot(&["r", ".r", "_r", "right"])?,
    );
    let dur = s.clips[clip].duration;
    let (mut lo, mut hi) = (Vec3::splat(f32::MAX), Vec3::splat(f32::MIN));
    for i in 0..32 {
        let g = s.globals(&[(clip, dur * i as f32 / 32., 1.)]);
        let d = g[l].w_axis.truncate() - g[r].w_axis.truncate();
        lo = lo.min(d);
        hi = hi.max(d);
    }
    // Front to back is the way they swing furthest; across they stay apart.
    let most = (hi.x - lo.x).max(hi.z - lo.z);
    (most > 1e-4).then_some(most)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn bounds(p: &Player) -> (Vec3, Vec3) {
        let m = p.matrix();
        let mut lo = Vec3::splat(f32::MAX);
        let mut hi = Vec3::splat(f32::MIN);
        for part in &p.meshes {
            for v in &part.vertices {
                let w = m.transform_point3(Vec3::from(v.pos));
                lo = lo.min(w);
                hi = hi.max(w);
            }
        }
        (lo, hi)
    }

    fn flat(p: &Player) -> Vec<f32> {
        p.meshes
            .iter()
            .flat_map(|m| m.vertices.iter().flat_map(|v| v.pos))
            .collect()
    }

    #[test]
    fn the_player_stands_at_its_height_and_poses_by_speed() {
        let mut kit = Kit::default();
        let mut p = Player::new(&mut kit).expect("the character loads");
        // Coloured by its materials, no textures; the hoodie in violet.
        assert!(kit.textures.is_empty());
        assert!(p.meshes.iter().all(|m| !m.indices.is_empty()));
        assert!(p.meshes.iter().any(|m| m.vertices[0].color[..3] == HOODIE));
        assert!(p.idle.is_some() && p.walk.is_some() && p.sprint.is_some());
        // A cycle's stride, measured from its feet: a run's is longer.
        assert!(p.walk_cycle > 0.5 && p.walk_cycle < 3., "{}", p.walk_cycle);
        assert!(
            p.run_cycle > p.walk_cycle,
            "{} {}",
            p.run_cycle,
            p.walk_cycle
        );
        // Standing: idle.
        for i in 0..30 {
            p.stride(0., i as f32 * 0.05, true);
        }
        let idle = flat(&p);
        let (lo, hi) = bounds(&p);
        assert!((hi.y - lo.y - HEIGHT).abs() < 0.2, "{lo:?} {hi:?}");
        assert!(lo.y.abs() < 0.1, "feet on the floor: {lo:?}");
        // Walking: the legs swing.
        for i in 30..70 {
            p.stride(1.6, i as f32 * 0.05, true);
        }
        let walk = flat(&p);
        assert_eq!(idle.len(), walk.len());
        assert!(
            idle.iter().zip(&walk).any(|(a, b)| (a - b).abs() > 0.02),
            "walking looks like standing"
        );
        // Sprinting differs from walking.
        for i in 70..140 {
            p.stride(4.8, i as f32 * 0.05, true);
        }
        let run = flat(&p);
        assert!(walk.iter().zip(&run).any(|(a, b)| (a - b).abs() > 0.02));
        for v in p.meshes.iter().flat_map(|m| &m.vertices) {
            assert!(v.pos.iter().chain(&v.normal).all(|x| x.is_finite()));
        }
        p.fade(0.1);
        assert!(!p.visible());
    }

    #[test]
    fn a_still_frame_is_standing() {
        // Without motion nothing eases: the same speed and time pose alike.
        let mut kit = Kit::default();
        let mut a = Player::new(&mut kit).unwrap();
        let mut b = Player::new(&mut kit).unwrap();
        a.stride(1., 1., false);
        b.stride(1., 5., false);
        assert_eq!(flat(&a), flat(&b));
    }
}
