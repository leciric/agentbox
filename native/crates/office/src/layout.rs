//! Where everything in the Office goes (#668's layout.ts), in metres on the
//! floor: x to the right, z towards the camera. The Office is a campus of rooms
//! (`campus`, below), each project's its own small diorama, or a few floors of
//! it for a big one: a room (`room_layout`) is its project's zone of pods on
//! terrazzo, its lead's counter at the front, the board on the back wall, and
//! its commons on the wood side. Pure, so it has tests, and the scene only
//! draws what this says.

use std::cmp::Ordering;
use std::f64::consts::PI;
use std::ops::Deref;

use crate::model::AreaModel;
use crate::walk::P;

/// A rectangle on the floor (TS: `Box`).
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Rect {
    pub x0: f64,
    pub z0: f64,
    pub x1: f64,
    pub z1: f64,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Spot {
    pub x: f64,
    pub z: f64,
    pub face: f64,
}

impl Spot {
    /// The spot as a point, for walking.
    pub fn at(&self) -> P {
        P {
            x: self.x,
            z: self.z,
        }
    }
}

const fn rect(x0: f64, z0: f64, x1: f64, z1: f64) -> Rect {
    Rect { x0, z0, x1, z1 }
}

const fn pt(x: f64, z: f64) -> P {
    P { x, z }
}

/// JS `Math.round`: halves go up, also below zero.
pub fn js_round(x: f64) -> f64 {
    (x + 0.5).floor()
}

#[derive(Clone, Debug, PartialEq)]
pub struct DeskPlace {
    pub id: String,
    /// The desk's centre. The agent sits behind it (at z - SEAT) facing the
    /// room, at its outer end (side: -1 the left, 1 the right): two desks make
    /// a pod, a bench pushed together, its agents at either end.
    pub x: f64,
    pub z: f64,
    pub side: i32,
    /// Where you stand to look at its monitor, across the desk from the agent.
    pub spot: Spot,
}

/// The lead's counter, its centre, and where you stand before it.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct LeadPlace {
    pub x: f64,
    pub z: f64,
    pub spot: Spot,
}

#[derive(Clone, Debug, PartialEq)]
pub struct AreaPlace {
    pub project: String,
    pub rug: Rect,
    /// The lead's counter; on its project's first floor only.
    pub lead: Option<LeadPlace>,
    pub desks: Vec<DeskPlace>,
    /// Its pods' rugs: a pod is up to four desks, two by two.
    pub pods: Vec<Rect>,
    /// The area's sign, hung over its back edge.
    pub sign: P,
}

/// Where a feature of a wall sits along it, and how wide it is.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Span {
    pub at: f64,
    pub w: f64,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct BoardPlace {
    pub x: f64,
    pub w: f64,
    pub spot: Spot,
}

#[derive(Clone, Debug, PartialEq)]
pub struct Layout {
    pub room: Rect,
    pub areas: Vec<AreaPlace>,
    pub board: BoardPlace,
    /// The wood-floored side (lobby is its old name).
    pub lobby: Rect,
    /// The wood-floored commons: its first, and each of them.
    pub wood: Rect,
    pub woods: Vec<Rect>,
    pub spawn: Spot,
    pub sofa: Rect,
    pub shelf: Rect,
    /// Everything you can't walk through, walls aside (the room's bounds are).
    pub obstacles: Vec<Rect>,
    /// Plants, at the room's corners and the areas' ends.
    pub plants: Vec<P>,
    /// Everything else it's furnished with (`foot_of` says their sizes).
    pub props: Vec<Prop>,
    /// Where framed prints hang on the back wall, along it.
    pub art: Vec<f64>,
    /// The room's features: a glass meeting room in the back left corner, a big
    /// window and a moss wall on the left wall, a brick wall on the back one,
    /// a planter dividing the lobby, a server rack in its corner.
    pub meeting: Rect,
    /// The commons' lounge, on the wood side.
    pub lounge: Rect,
    /// Which of its commons it has (0-2 a big room's, 3-5 a small one's), and
    /// which of its finishes (decor, 0-2): each project's, each floor's, its own.
    pub variant: i64,
    pub finish: i64,
    pub window: Span,
    pub moss: Span,
    pub brick: Span,
    pub planter: Rect,
    pub rack: P,
}

pub struct DeskSize {
    pub w: f64,
    pub d: f64,
    pub h: f64,
}
pub const DESK: DeskSize = DeskSize {
    w: 1.5,
    d: 0.75,
    h: 0.74,
};
/// The chair's distance behind the desk's centre.
pub const SEAT: f64 = 0.72;
pub struct CounterSize {
    pub w: f64,
    pub d: f64,
}
pub const COUNTER: CounterSize = CounterSize { w: 2.4, d: 0.7 };
const POD: i64 = 2;
// The room is a grid of cells, two rows of them, each holding one thing: a
// pod of two desks, the lead's counter, the meeting room, the kitchen, the
// lounge, a games corner or a corner of things. Pods take every other cell,
// a checkerboard, so desks are spread through the room with the commons
// between them, as the target's are, rather than desks on one side and the
// commons on the other.
const CELL_W: f64 = 5.8;
const CELL_D: f64 = 4.8;
// A small team's cells closer together: a cosy room, not a big one half bare.
const SMALL_W: f64 = 5.6;
const SMALL_D: f64 = 4.3;
const SMALL_BACK: f64 = 2.7;
const ROWS: usize = 2;
/// The left wall's strip: its window, moss, lockers and racks, and room beside them.
const PAD_L: f64 = 2.0;
const PAD_R: f64 = 0.6;
/// The back aisle, before the board.
const BACK: f64 = 3.1;
const FRONT: f64 = 0.5;
/// From a cell's back to its pod's desks.
const DESK_IN: f64 = 2.2;

/// columns is how many pods an area puts side by side: a block a little wider
/// than deep, so its room stays the target's size.
pub fn columns(pods: i64) -> i64 {
    if pods <= 1 {
        return 1;
    }
    // Three pods are two and one staggered behind them: a cluster, not a row.
    let rows = 1.max(js_round((pods as f64 / 1.2).sqrt()) as i64);
    (pods as f64 / rows as f64).ceil() as i64
}

fn expand(b: &Rect, r: f64) -> Rect {
    rect(b.x0 - r, b.z0 - r, b.x1 + r, b.z1 + r)
}
fn boxc(x: f64, z: f64, w: f64, d: f64) -> Rect {
    rect(x - w / 2., z - d / 2., x + w / 2., z + d / 2.)
}

/// An area as a room lays it out (TS: `AreaModel & { noLead, floor }`, of which
/// it reads only the desks' ids): a floor of a project past the first has no
/// lead (its counter is on the first).
#[derive(Clone, Debug, PartialEq)]
pub struct AreaIn {
    pub project: String,
    /// The desks' ids.
    pub desks: Vec<String>,
    pub no_lead: bool,
    pub floor: Option<i64>,
}

impl From<&AreaModel> for AreaIn {
    fn from(a: &AreaModel) -> Self {
        AreaIn {
            project: a.project.clone(),
            desks: a.desks.iter().map(|d| d.id.clone()).collect(),
            no_lead: false,
            floor: None,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq)]
enum CellKind {
    Pod,
    Lead,
    Meeting,
    Kitchen,
    Lounge,
    Games,
    Corner,
}

type Cell = (usize, usize);
type Kinds = Vec<Vec<Option<CellKind>>>;

fn open(kinds: &Kinds, cells: &[Cell], r: Option<usize>) -> Vec<Cell> {
    cells
        .iter()
        .copied()
        .filter(|&(c, rr)| kinds[c][rr].is_none() && (r.is_none() || Some(rr) == r))
        .collect()
}

fn fmax(it: impl Iterator<Item = f64>) -> f64 {
    it.fold(f64::NEG_INFINITY, f64::max)
}

fn add_prop(
    props: &mut Vec<Prop>,
    obstacles: &mut Vec<Rect>,
    kind: PropKind,
    x: f64,
    z: f64,
    rot: f64,
) {
    props.push(Prop { kind, x, z, rot });
    // A mat is floor: walked over.
    if kind == PropKind::Mat {
        return;
    }
    let (w, d) = foot_of(kind);
    let turned = rot.sin().abs() > 0.5;
    obstacles.push(boxc(
        x,
        z,
        if turned { d } else { w },
        if turned { w } else { d },
    ));
}

fn clear_at(obstacles: &[Rect], x: f64, z: f64, r: f64) -> bool {
    !obstacles
        .iter()
        .any(|b| x + r > b.x0 && x - r < b.x1 && z + r > b.z0 && z - r < b.z1)
}

/// room_layout lays out one room: its project's first area (a room holds one).
/// It depends only on how many desks the area has and their order, so a
/// desk's place changes only when an agent before it comes or goes.
pub fn room_layout(areas_in: &[AreaIn]) -> Layout {
    let a = areas_in.first();
    let n = a.map_or(0, |a| a.desks.len()) as i64;
    let pn = (n as f64 / POD as f64).ceil() as i64;
    let has_lead = a.is_some_and(|a| !a.no_lead);
    // A small team's room has no meeting room: it's small.
    let compact = a.is_some() && n <= 4;
    let project = a.map_or("", |a| a.project.as_str());
    let mut h: u32 = 0;
    for u in project.encode_utf16() {
        h = h.wrapping_mul(31).wrapping_add(u as u32);
    }
    // (JS shifts it with `>>`, which reads it as a signed 32-bit number.)
    let hs = h as i32 as i64;
    let floor = a.and_then(|a| a.floor).unwrap_or(1);
    let meet_in = !compact;
    // The glass meeting room at the front left, as the target's, or the back
    // left: floor to floor in turn.
    let meet_front = meet_in && ((hs >> 1) + floor) % 2 == 0;
    let cn = 2.max(
        ((pn + i64::from(has_lead) + i64::from(meet_in) + 2) as f64 / ROWS as f64).ceil() as usize,
    );
    let cw = if compact { SMALL_W } else { CELL_W };
    let cd = if compact { SMALL_D } else { CELL_D };
    let bk = if compact { SMALL_BACK } else { BACK };
    let room_w = PAD_L + cn as f64 * cw + PAD_R;
    let room_d = bk + ROWS as f64 * cd + FRONT;
    let cell_box = |c: usize, r: usize| {
        rect(
            PAD_L + c as f64 * cw,
            bk + r as f64 * cd,
            PAD_L + (c + 1) as f64 * cw,
            bk + (r + 1) as f64 * cd,
        )
    };
    let mut kinds: Kinds = vec![vec![None; ROWS]; cn];
    let mut cells: Vec<Cell> = Vec::new();
    for c in 0..cn {
        for r in 0..ROWS {
            cells.push((c, r));
        }
    }
    let mid = |list: &[Cell]| -> Option<Cell> {
        let mut v = list.to_vec();
        let centre = (cn as f64 - 1.) / 2.;
        v.sort_by(|p, q| {
            let d = (p.0 as f64 - centre).abs() - (q.0 as f64 - centre).abs();
            d.partial_cmp(&0.)
                .unwrap_or(Ordering::Equal)
                .then(p.0.cmp(&q.0))
        });
        v.first().copied()
    };
    if meet_in {
        kinds[0][if meet_front { 1 } else { 0 }] = Some(CellKind::Meeting);
    }
    // Pods on the checkerboard the meeting room isn't on, left to right; any
    // more than it holds where there's room.
    // (A small room's first pod at the back, under its board.)
    let p0 = if meet_in && !meet_front { 1 } else { 0 };
    // Fewer pods than the checkerboard has room for: spread evenly along it,
    // not bunched at one end.
    let checker: Vec<Cell> = cells
        .iter()
        .copied()
        .filter(|&(c, r)| (c + r) % 2 == p0 && kinds[c][r].is_none())
        .collect();
    let mut pod_cells: Vec<Cell> = Vec::new();
    if pn > 0 && (pn as usize) < checker.len() {
        for i in 0..pn {
            let at = if pn == 1 {
                0
            } else {
                js_round((i * (checker.len() as i64 - 1)) as f64 / (pn - 1) as f64) as usize
            };
            pod_cells.push(checker[at]);
        }
    } else {
        pod_cells.extend(checker.iter().take(pn as usize));
    }
    for &(c, r) in &cells {
        if (pod_cells.len() as i64) < pn && (c + r) % 2 != p0 && kinds[c][r].is_none() {
            pod_cells.push((c, r));
        }
    }
    pod_cells.sort_by(|p, q| p.0.cmp(&q.0).then(p.1.cmp(&q.1)));
    for &(c, r) in &pod_cells {
        kinds[c][r] = Some(CellKind::Pod);
    }
    // The lead's counter in the front row, nearest its middle: who you see first.
    let mut lead_cell: Option<Cell> = None;
    if has_lead {
        lead_cell =
            mid(&open(&kinds, &cells, Some(1))).or_else(|| mid(&open(&kinds, &cells, None)));
        if let Some((c, r)) = lead_cell {
            kinds[c][r] = Some(CellKind::Lead);
        }
    }
    // The board on the back wall over a back cell near the middle that leaves
    // the wall free: a pod's, or an open one's.
    let board_col = mid(&cells
        .iter()
        .copied()
        .filter(|&(c, r)| r == 0 && (kinds[c][r] == Some(CellKind::Pod) || kinds[c][r].is_none()))
        .collect::<Vec<_>>())
    .unwrap_or((1.min(cn - 1), 0))
    .0;
    // The kitchen against the back wall at one end, its counters on it; the
    // lounge on the back wall too where there's room, its sofa by the brick.
    // (Over four floors, with the meeting room's turn, no two alike.)
    let swap = ((hs >> 2) + (floor >> 1)) % 2 == 0;
    let back_open = |kinds: &Kinds| -> Vec<Cell> {
        let mut v: Vec<Cell> = open(kinds, &cells, Some(0))
            .into_iter()
            .filter(|&(c, _)| c != board_col)
            .collect();
        v.sort_by(|p, q| if swap { p.0.cmp(&q.0) } else { q.0.cmp(&p.0) });
        v
    };
    // (Neither ever the board's: its counters or sofa would stand before it.)
    let not_board = |kinds: &Kinds| -> Vec<Cell> {
        open(kinds, &cells, None)
            .into_iter()
            .filter(|&(c, r)| !(r == 0 && c == board_col))
            .collect()
    };
    // Floor to floor, the kitchen or the lounge has the back wall first.
    let mut kitchen_cell: Option<Cell> = None;
    let mut lounge_cell: Option<Cell> = None;
    // (Every room has its kitchen: with no other cell, an island in the
    // board's, at its front, low under the board's view.)
    let mut kitchen_island = false;
    let take_kitchen =
        |kinds: &mut Kinds, kitchen_cell: &mut Option<Cell>, kitchen_island: &mut bool| {
            *kitchen_cell = back_open(kinds)
                .first()
                .copied()
                .or_else(|| not_board(kinds).first().copied());
            if kitchen_cell.is_none() && kinds[board_col][0].is_none() {
                *kitchen_cell = Some((board_col, 0));
                *kitchen_island = true;
            }
            if let Some((c, r)) = *kitchen_cell {
                kinds[c][r] = Some(CellKind::Kitchen);
            }
        };
    let take_lounge = |kinds: &mut Kinds, lounge_cell: &mut Option<Cell>| {
        *lounge_cell = back_open(kinds)
            .first()
            .copied()
            .or_else(|| open(kinds, &cells, Some(1)).first().copied())
            .or_else(|| not_board(kinds).first().copied());
        if let Some((c, r)) = *lounge_cell {
            kinds[c][r] = Some(CellKind::Lounge);
        }
    };
    if swap && !compact {
        take_lounge(&mut kinds, &mut lounge_cell);
        take_kitchen(&mut kinds, &mut kitchen_cell, &mut kitchen_island);
    } else {
        take_kitchen(&mut kinds, &mut kitchen_cell, &mut kitchen_island);
        take_lounge(&mut kinds, &mut lounge_cell);
    }
    // The rest: a games corner, then corners of things.
    let mut games = false;
    for (c, r) in open(&kinds, &cells, None) {
        let k = if !games && !compact && c != board_col {
            CellKind::Games
        } else {
            CellKind::Corner
        };
        kinds[c][r] = Some(k);
        if k == CellKind::Games {
            games = true;
        }
    }

    let mut obstacles: Vec<Rect> = Vec::new();
    let mut plants: Vec<P> = Vec::new();
    let mut props: Vec<Prop> = Vec::new();
    let mut woods: Vec<Rect> = Vec::new();
    // Floor no cell's thing uses, which the room furnishes (below). Low says it's
    // where a close-up looks across it: nothing tall.
    let mut free: Vec<(Rect, bool)> = Vec::new();
    macro_rules! prop {
        ($kind:expr, $x:expr, $z:expr) => {
            add_prop(&mut props, &mut obstacles, $kind, $x, $z, 0.)
        };
        ($kind:expr, $x:expr, $z:expr, $rot:expr) => {
            add_prop(&mut props, &mut obstacles, $kind, $x, $z, $rot)
        };
    }

    // The pods: a bench of two desks on its rug, an agent at either end.
    let mut pods: Vec<Rect> = Vec::new();
    let mut desks: Vec<DeskPlace> = Vec::new();
    for (p, &(c, r)) in pod_cells.iter().enumerate() {
        let cb = cell_box(c, r);
        let px = (cb.x0 + cb.x1) / 2.;
        let dz = cb.z0 + DESK_IN;
        let k = POD.min(n - p as i64 * POD);
        pods.push(rect(
            px - 1.95,
            dz - 1.35,
            if k > 1 { px + 1.95 } else { px + 0.45 },
            dz + 0.75,
        ));
        for j in 0..k {
            let id = &a.expect("pods mean an area").desks[(p as i64 * POD + j) as usize];
            let side: i32 = if j != 0 { 1 } else { -1 };
            let sd = side as f64;
            let dx = px + sd * (DESK.w / 2.);
            obstacles.push(boxc(dx, dz, DESK.w, DESK.d));
            // The chair and the agent on it: you walk round, not through.
            obstacles.push(boxc(dx + sd * 0.48, dz - SEAT, 1.45, 1.3));
            // You stand beside the agent, level with it, facing it, a good step
            // out: seen from the front, clear of it either side, never over it.
            desks.push(DeskPlace {
                id: id.clone(),
                x: dx,
                z: dz,
                side,
                spot: Spot {
                    x: (room_w - PAD_R - 0.1).min(1.6f64.max(dx + sd * 2.3)),
                    z: dz - SEAT + 0.05,
                    face: -sd * (PI / 2.),
                },
            });
        }
    }
    // A front row's pod has a low planter along its front: green that a close-up
    // looks over (no trees before a desk: they'd hide it).
    for (p, &(c, r)) in pod_cells.iter().enumerate() {
        let cb = cell_box(c, r);
        if r == ROWS - 1 {
            prop!(
                PropKind::Plants,
                (cb.x0 + cb.x1) / 2. + if p % 2 == 1 { -0.8 } else { 0.8 },
                cb.z1 - 0.35
            );
        }
    }
    // The lead's counter, its lead behind it facing the room, on a mat.
    let mut lead: Option<LeadPlace> = None;
    if let Some((c, r)) = lead_cell {
        let cb = cell_box(c, r);
        let cx = (cb.x0 + cb.x1) / 2.;
        // At its cell's front: well clear of the pods behind.
        let cz = cb.z0 + 2.9;
        obstacles.push(boxc(cx, cz, COUNTER.w, COUNTER.d));
        obstacles.push(boxc(cx, cz - 0.75, 0.7, 0.6));
        props.push(Prop {
            kind: PropKind::Mat,
            x: cx,
            z: cz + 0.25,
            rot: 0.,
        });
        lead = Some(LeadPlace {
            x: cx,
            z: cz,
            spot: Spot {
                x: cx - 1.95,
                z: cz + 0.55,
                face: PI / 2.,
            },
        });
    }

    // The meeting room, behind glass, at the left wall.
    let m0 = cell_box(0, if meet_front { 1 } else { 0 });
    let meeting = if !meet_in {
        rect(0., 0., 0., 0.)
    } else if meet_front {
        rect(0., m0.z0 + 0.3, m0.x1 - 0.8, room_d)
    } else {
        rect(0., 0., m0.x1 - 0.8, m0.z0 + 2.8)
    };
    if meet_in {
        obstacles.push(rect(
            meeting.x0,
            meeting.z0 - 0.05,
            meeting.x1 + 0.05,
            meeting.z1 + 0.05,
        ));
    }

    // The kitchen: counters and a fridge against the back wall, an island before
    // them with stools; in the front row, where there's no wall, an island alone.
    let (kw, kd) = foot_of(PropKind::Kitchen);
    let (fw, fd) = foot_of(PropKind::Fridge);
    let (_, id) = foot_of(PropKind::Island);
    let mut kitchen_box: Option<Rect> = None;
    if let Some((c, r)) = kitchen_cell {
        let cb = cell_box(c, r);
        let cx = (cb.x0 + cb.x1) / 2.;
        if r == 0 && !kitchen_island {
            let fridge = cb.x1 - cb.x0 - 0.6 >= kw + fw + 0.1;
            let kx = cx - if fridge { (fw + 0.1) / 2. } else { 0. };
            prop!(PropKind::Kitchen, kx, kd / 2. + 0.02);
            if fridge {
                prop!(
                    PropKind::Fridge,
                    kx + kw / 2. + 0.1 + fw / 2.,
                    fd / 2. + 0.02
                );
            }
            prop!(PropKind::Island, cx, cb.z0 + 0.3 + id / 2.);
            kitchen_box = Some(rect(
                kx - kw / 2.,
                0.,
                if fridge {
                    kx + kw / 2. + 0.1 + fw
                } else {
                    kx + kw / 2.
                },
                1.,
            ));
        } else {
            // An island alone, with its stools, at the cell's front.
            prop!(PropKind::Island, cx, cb.z1 - 0.6 - id / 2.);
        }
        woods.push(rect(
            cb.x0 + 0.15,
            if r == 0 && !kitchen_island {
                0.
            } else {
                cb.z0 + 0.15
            },
            cb.x1 - 0.15,
            if r == 1 { room_d } else { cb.z1 - 0.15 },
        ));
    }

    // The lounge: a sectional sofa facing the room, a low table before it, a
    // lamp at its end, on a soft rug; a planter at its side, the target's.
    let mut lounge = rect(0., 0., 0., 0.);
    let mut sofa = lounge;
    let mut planter = rect(0., 0., 0.01, 0.01);
    if let Some((c, r)) = lounge_cell {
        let cb = cell_box(c, r);
        let back = r == 0;
        let lw = 3.4f64.min(cb.x1 - cb.x0 - 2.6);
        let lx0 = cb.x0 + 1.6;
        lounge = rect(
            lx0,
            if back { 0.5 } else { cb.z0 + 0.4 },
            lx0 + lw,
            if back { cb.z0 + 1.6 } else { cb.z0 + 4.4 },
        );
        sofa = rect(
            lounge.x0 + 0.3,
            lounge.z0 + 0.1,
            lounge.x1 - 0.3,
            lounge.z0 + 1.05,
        );
        obstacles.push(sofa);
        obstacles.push(rect(sofa.x0, sofa.z1, sofa.x0 + 0.95, sofa.z1 + 1.2));
        obstacles.push(boxc(
            (sofa.x0 + sofa.x1) / 2. + 0.4,
            lounge.z0 + 2.65,
            1.5,
            0.75,
        ));
        // The lamp, at the sofa's right end.
        obstacles.push(boxc(sofa.x1 + 0.3, sofa.z1 + 0.9, 0.45, 0.45));
        // (Clear of the spot at the pod next door.)
        // Short, open at both ends: the lounge a corner you walk into, not a pocket.
        let pz = if back { cb.z0 + 1.6 } else { cb.z0 + 2.4 };
        planter = rect(cb.x0 + 0.75, pz - 1.3, cb.x0 + 1.25, pz + 1.3);
        obstacles.push(planter);
        woods.push(rect(
            cb.x0 + 0.15,
            if back { 0. } else { cb.z0 + 0.15 },
            cb.x1 - 0.15,
            if back { cb.z1 - 0.15 } else { room_d },
        ));
    }

    // Games: a ping-pong table and arcade cabinets; corners: a few things on a mat.
    for &(c, r) in &cells {
        let kind = kinds[c][r];
        let cb = cell_box(c, r);
        let cx = (cb.x0 + cb.x1) / 2.;
        let cz = (cb.z0 + cb.z1) / 2.;
        if kind == Some(CellKind::Games) {
            prop!(PropKind::Pingpong, cx - 0.5, cz);
            prop!(PropKind::Arcade, cb.x1 - 1.1, cz - 0.5, -PI / 2.);
            prop!(PropKind::Arcade, cb.x1 - 1.1, cz + 0.45, -PI / 2.);
            woods.push(rect(
                cb.x0 + 0.15,
                cb.z0 + 0.15,
                cb.x1 - 0.15,
                if r == 1 { room_d } else { cb.z1 - 0.15 },
            ));
        } else if kind == Some(CellKind::Corner) {
            // Clear of the spots at the next cells' pods, either side.
            free.push((
                rect(cb.x0 + 1.0, cb.z0 + 0.5, cb.x1 - 1.0, cb.z1 - 0.5),
                c == board_col && r == 0,
            ));
            woods.push(rect(
                cb.x0 + 0.15,
                cb.z0 + 0.15,
                cb.x1 - 0.15,
                if r == 1 { room_d } else { cb.z1 - 0.15 },
            ));
        }
    }

    // The left wall: the window (past a meeting room at the back), the moss wall
    // after it, lockers, and the bookshelf and racks at its far end (or before a
    // meeting room at the front).
    let wall_from = if meet_in && !meet_front {
        meeting.z1 + 0.4
    } else {
        0.5
    };
    let wall_to = if meet_front {
        meeting.z0 - 0.3
    } else {
        room_d - 1.0
    };
    let shelf = rect(0., wall_to - 1.6, 0.4, wall_to);
    obstacles.push(shelf);
    let rack = pt(0.36, shelf.z0 - 0.5);
    obstacles.push(boxc(rack.x, rack.z, 0.65, 0.65));
    prop!(
        PropKind::Rack,
        foot_of(PropKind::Rack).1 / 2. + 0.03,
        shelf.z0 - 1.3,
        PI / 2.
    );
    let win_w = 4.6f64.min(2.2f64.max((shelf.z0 - 1.8 - wall_from) * 0.45));
    let window = Span {
        at: wall_from + win_w / 2.,
        w: win_w,
    };
    let moss_w = 3.6f64.min(1.4f64.max(shelf.z0 - 1.9 - (window.at + win_w / 2.) - 0.4));
    let moss = Span {
        at: window.at + win_w / 2. + 0.4 + moss_w / 2.,
        w: moss_w,
    };
    let mut lz = moss.at + moss_w / 2. + 1.4;
    while lz + 1.25 < shelf.z0 - 1.8 {
        prop!(
            PropKind::Lockers,
            foot_of(PropKind::Lockers).1 / 2. + 0.02,
            lz,
            PI / 2.
        );
        lz += 3.2;
    }

    // The board, on the back wall over its cell; you read it from before its
    // left end, out of its way (you step out of the picture there).
    let bc = cell_box(board_col, 0);
    let board_w = 5.2f64.min(cw - 0.8);
    let board_x = (bc.x0 + bc.x1) / 2.;
    obstacles.push(boxc(board_x, 0.2, board_w, 0.4));
    // The brick wall with its sign: behind the lounge on the back wall, or else
    // on the free back wall farthest from the board.
    let mut wall_used: Vec<(f64, f64)> = vec![(board_x - board_w / 2., board_x + board_w / 2.)];
    if meet_in && !meet_front {
        wall_used.push((0., meeting.x1));
    }
    if let Some(kb) = kitchen_box {
        wall_used.push((kb.x0 - 0.2, kb.x1 + 0.2));
    }
    let mut brick = Span {
        at: (lounge.x0 + lounge.x1) / 2.,
        w: 3.6f64.min(lounge.x1 - lounge.x0),
    };
    if !lounge_cell.is_some_and(|(_, r)| r == 0) {
        // A back cell not the board's nor the kitchen's.
        let mut cands: Vec<Rect> = cells
            .iter()
            .copied()
            .filter(|&(c, r)| {
                r == 0
                    && c != board_col
                    && kinds[c][r] != Some(CellKind::Kitchen)
                    && kinds[c][r] != Some(CellKind::Meeting)
            })
            .map(|(c, _)| cell_box(c, 0))
            .collect();
        let far = |b: &Rect| ((b.x0 + b.x1) / 2. - board_x).abs();
        cands.sort_by(|p, q| far(q).partial_cmp(&far(p)).unwrap_or(Ordering::Equal));
        brick = match cands.first() {
            Some(at) => Span {
                at: (at.x0 + at.x1) / 2.,
                w: 3.2,
            },
            None => Span {
                at: room_w - 2.2,
                w: 2.4,
            },
        };
    }
    wall_used.push((brick.at - brick.w / 2., brick.at + brick.w / 2.));
    // Framed prints and low shelves of plants where the back wall is free.
    let mut art: Vec<f64> = Vec::new();
    let mut spans: Vec<(f64, f64)> = Vec::new();
    {
        let mut used = wall_used.clone();
        used.sort_by(|p, q| p.0.partial_cmp(&q.0).unwrap_or(Ordering::Equal));
        let mut x = 0.4;
        for (u0, u1) in used {
            if u0 - x > 1.6 {
                spans.push((x, u0));
            }
            x = x.max(u1);
        }
        if room_w - 0.4 - x > 1.6 {
            spans.push((x, room_w - 0.4));
        }
    }
    for (s0, s1) in spans {
        let w = s1 - s0 - 0.6;
        let cx = (s0 + s1) / 2.;
        // A low shelf only where nothing stands before it: over a pod's or a
        // corner's back aisle.
        if w > 2.4 && clear_at(&obstacles, cx, 0.3, 0.6) {
            prop!(PropKind::Shelf, cx, foot_of(PropKind::Shelf).1 / 2. + 0.02);
        }
        if w > 4.4 {
            art.push(cx - 1.2);
            art.push(cx + 1.2);
        } else if w > 1.2 {
            art.push(cx);
        }
    }
    // A tree in the back right corner, and potted plants at the front corners,
    // where there's floor.
    if clear_at(&obstacles, room_w - 0.7, 0.7, 0.6) {
        prop!(PropKind::Tree, room_w - 0.6, 0.6);
    }
    for p in [pt(0.6, room_d - 0.6), pt(room_w - 0.6, room_d - 0.6)] {
        if clear_at(&obstacles, p.x, p.z, 0.45) {
            plants.push(p);
            obstacles.push(boxc(p.x, p.z, 0.6, 0.6));
        }
    }

    // The corners, furnished: a few things together on a mat, or couches, or
    // islands of a tree, a standing table, a printer, each with room to pass.
    let mut n_i = 0usize;
    let mut themed = 0usize;
    for (f, low) in &free {
        let w = f.x1 - f.x0;
        let d = f.z1 - f.z0;
        // A theme whose things all fit this corner, each room's in its own turn.
        let themes = if *low { LOW_THEMES } else { THEMES };
        let fits = |th: &Theme| {
            th.iter().all(|&(kind, dx, dz)| {
                let (tw, td) = foot_of(kind);
                dx.abs() + tw / 2. <= w / 2. - 0.1 && dz.abs() + td / 2. <= d / 2. - 0.1
            })
        };
        let theme = (0..themes.len())
            .map(|i| themes[(themed + h as usize + i) % themes.len()])
            .find(|th| fits(th));
        if let Some(theme) = theme {
            let cx = (f.x0 + f.x1) / 2.;
            let cz = (f.z0 + f.z1) / 2.;
            prop!(PropKind::Mat, cx, cz);
            for &(kind, dx, dz) in theme {
                prop!(kind, cx + dx, cz + dz);
            }
            themed += 1;
            continue;
        }
        let kinds2 = if *low { LOW } else { ISLANDS };
        for t in 0..kinds2.len() {
            let kind = kinds2[(n_i + t) % kinds2.len()];
            let (fw2, fd2) = foot_of(kind);
            if fw2 > w - 0.8 || fd2 > d - 0.8 {
                continue;
            }
            prop!(kind, (f.x0 + f.x1) / 2., (f.z0 + f.z1) / 2.);
            n_i += t + 1;
            break;
        }
    }

    // You read the board from beside its end, in the picture with it: its left
    // end, or its right where the left is taken; else before its left part.
    let board_spot = [
        Spot {
            x: board_x - board_w / 2. - 0.75,
            z: 1.1,
            face: PI / 2.,
        },
        Spot {
            x: board_x + board_w / 2. + 0.75,
            z: 1.1,
            face: -PI / 2.,
        },
    ]
    .into_iter()
    .find(|p| clear_at(&obstacles, p.x, p.z, 0.6))
    .unwrap_or(Spot {
        x: board_x - board_w / 2. + 0.8,
        z: 1.3,
        face: PI / 2.,
    });
    // You come in at the front, by the middle, in full view: the nearest spot
    // clear of everything, never inside a piece of furniture.
    let want = pt(room_w * 0.55, room_d - 1.3);
    let mut spawn = Spot {
        x: want.x,
        z: want.z,
        face: -0.5,
    };
    let mut around: Vec<(f64, f64, f64)> = Vec::new();
    for ix in -32..=32 {
        let dx = ix as f64 * 0.25;
        for iz in -24..=4 {
            let dz = iz as f64 * 0.25;
            around.push((want.x + dx, want.z + dz, dx * dx + dz * dz));
        }
    }
    around.sort_by(|p, q| p.2.partial_cmp(&q.2).unwrap_or(Ordering::Equal));
    // (Well clear of every spot, so you come in at nothing.)
    // In open floor, not a pocket or a pod's front: clear well round, first.
    let mut spots: Vec<Spot> = desks.iter().map(|d| d.spot).collect();
    if let Some(l) = &lead {
        spots.push(l.spot);
    }
    spots.push(board_spot);
    let off_pods = |x: f64, z: f64| {
        pods.iter()
            .all(|q| x < q.x0 - 1.2 || x > q.x1 + 1.2 || z < q.z0 - 1.2 || z > q.z1 + 1.6)
    };
    // (Along the front first, before you're anywhere else.)
    let lead_off = |x: f64, z: f64| lead.as_ref().is_none_or(|l| (x - l.x).hypot(z - l.z) > 3.2);
    for (r, front) in [(0.9, true), (1.3, false), (0.75, false)] {
        if spawn.x == want.x && spawn.z == want.z {
            let at = around.iter().find(|&&(x, z, _)| {
                (!front || z >= room_d - 2.3)
                    && x > 0.8
                    && x < room_w - 0.8
                    && z > 0.8
                    && z < room_d - 0.7
                    && clear_at(&obstacles, x, z, r)
                    && off_pods(x, z)
                    && lead_off(x, z)
                    && spots.iter().all(|q| (q.x - x).hypot(q.z - z) > 2.6)
            });
            if let Some(&(x, z, _)) = at {
                spawn = Spot { x, z, face: -0.5 };
            }
        }
    }

    let areas: Vec<AreaPlace> = match a {
        Some(a) => vec![AreaPlace {
            project: a.project.clone(),
            rug: rect(PAD_L - 0.5, bk - 0.5, room_w - PAD_R + 0.5, room_d),
            pods,
            lead,
            desks,
            sign: pt(board_x, bk),
        }],
        None => Vec::new(),
    };
    let wood = woods
        .first()
        .copied()
        .unwrap_or(rect(room_w, 0., room_w, 0.));
    let has_games = kinds.iter().flatten().any(|k| *k == Some(CellKind::Games));
    Layout {
        room: rect(0., 0., room_w, room_d),
        areas,
        board: BoardPlace {
            x: board_x,
            w: board_w,
            spot: board_spot,
        },
        lobby: wood,
        wood,
        woods,
        spawn,
        sofa,
        shelf,
        obstacles,
        plants,
        props,
        art,
        meeting,
        window,
        moss,
        brick,
        planter,
        rack,
        lounge,
        variant: if has_games {
            h as i64 % 3
        } else {
            3 + (h as i64 % 3)
        },
        finish: (h as i64 + floor - 1) % 3,
    }
}

/// The things the room is furnished with, besides desks: their footprints,
/// across (x) and deep (z) when not turned; turned a quarter, they swap.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum PropKind {
    Tree,
    Standing,
    Printer,
    Shelf,
    Booth,
    Beanbags,
    Whiteboard,
    Lockers,
    Kitchen,
    Fridge,
    Island,
    Pingpong,
    Arcade,
    Rack,
    Plants,
    Aquarium,
    Vending,
    Logo,
    Mat,
    Couch,
    Lowtable,
}

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Prop {
    pub kind: PropKind,
    pub x: f64,
    pub z: f64,
    pub rot: f64,
}

impl PropKind {
    /// FOOT: its footprint, across and deep, before PROP_SCALE.
    pub const fn foot(self) -> [f64; 2] {
        use PropKind::*;
        match self {
            Tree => [0.9, 0.9],
            Standing => [1.4, 0.8],
            Printer => [1.7, 0.7],
            Shelf => [1.8, 0.45],
            Booth => [1.3, 1.3],
            Beanbags => [2.6, 2.2],
            Whiteboard => [1.7, 0.5],
            Lockers => [2.0, 0.5],
            Kitchen => [2.8, 0.7],
            Fridge => [0.8, 0.7],
            Island => [2.4, 1.6],
            Pingpong => [2.8, 1.6],
            Arcade => [0.75, 0.75],
            Rack => [0.65, 0.65],
            Plants => [1.5, 0.9],
            Aquarium => [1.5, 0.55],
            Vending => [0.95, 0.8],
            Logo => [1.1, 1.1],
            Mat => [4.3, 2.4],
            Couch => [1.8, 0.8],
            Lowtable => [0.9, 0.5],
        }
    }
}

/// Props are a little bigger than life, as a toy's furniture is.
pub const PROP_SCALE: f64 = 1.25;
pub fn foot_of(k: PropKind) -> (f64, f64) {
    let f = k.foot();
    (f[0] * PROP_SCALE, f[1] * PROP_SCALE)
}
use PropKind::*;
// (No standing tables: from above they read as empty desks.)
const ISLANDS: &[PropKind] = &[
    Tree, Aquarium, Booth, Plants, Beanbags, Vending, Logo, Printer, Shelf, Tree, Whiteboard,
];
// What may stand before an area: nothing taller than a desk's monitor, nor
// leafy, so a close-up looks over it.
const LOW: &[PropKind] = &[Beanbags, Shelf, Printer, Plants];
// Corners of a few things together, on a mat, each within 5.4 by 3: where
// each stands from the mat's middle. (Sizes are FOOT's, PROP_SCALE larger.)
type Theme = &'static [(PropKind, f64, f64)];
const NOOK: Theme = &[(Beanbags, -0.95, 0.), (Plants, 1.65, -0.55)];
const GARDEN: Theme = &[(Plants, -1.6, -0.45), (Logo, 0.3, 0.1), (Plants, 2.1, 0.5)];
const READING: Theme = &[
    (Aquarium, -1.25, -0.75),
    (Logo, 1.3, 0.3),
    (Plants, -1.2, 0.6),
];
const COFFEE: Theme = &[
    (Vending, -1.95, -0.45),
    (Plants, 0., 0.3),
    (Tree, 1.95, -0.3),
];
const FOCUS: Theme = &[(Booth, -1.15, 0.), (Booth, 0.65, 0.), (Tree, 2.25, 0.2)];
const LOUNGE: Theme = &[(Beanbags, -1.05, 0.), (Shelf, 1.75, -0.8)];
const STUDY: Theme = &[
    (Shelf, -1.5, -0.85),
    (Plants, -1.2, 0.6),
    (Printer, 1.6, -0.6),
];
const THEMES: &[Theme] = &[COFFEE, NOOK, FOCUS, GARDEN, READING];
const LOW_THEMES: &[Theme] = &[LOUNGE, STUDY];

// The campus: each project's room, side by side as dioramas floating on the
// sky, a project of more desks than a room holds on floors of its own.
// A room holds the target's eight or so; more desks, more floors.
pub const ROOM_DESKS: usize = 8;
// Between rooms on the campus: wider front to back, where the walls rise.
const CAMPUS_GAP: f64 = 8.;
const CAMPUS_GAP_Z: f64 = 13.;
const CAMPUS_ASPECT: f64 = 1.6;
/// A project's floors stand on each other over the campus, one building, each
/// this far over the one below and a little back.
pub const FLOOR_LIFT: f64 = 8.5;
const FLOOR_STEP: f64 = 5.;
/// Where a floor past the first really is, for walking in it: far behind the
/// campus, clear of every other room (the campus shows it on its building).
const FLOOR_AWAY: f64 = 400.;

/// How far from where a room is walked in the campus shows it.
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Shown {
    pub dx: f64,
    pub dy: f64,
    pub dz: f64,
}

#[derive(Clone, Debug, PartialEq)]
pub struct RoomPlace {
    /// project#floor.
    pub key: String,
    pub project: String,
    pub floor: i64,
    pub floors: usize,
    /// Where its room sits, its floor there: what you walk in.
    pub ox: f64,
    pub oz: f64,
    pub r#box: Rect,
    /// How far from there the campus shows it: a floor past the first stands
    /// over its building's first, the rest where they are.
    pub shown: Shown,
    /// The room as `room_layout` laid it out, in its own frame (the scene draws
    /// its diorama from it, moved to ox, oz).
    pub local: Layout,
    /// Its board and where you come in, in the campus's frame.
    pub board: BoardPlace,
    pub spawn: Spot,
    /// The desks in it, and whether its project's lead is.
    pub desks: Vec<String>,
    pub lead: bool,
}

/// A room's area, in the campus's frame, and which room (TS: `AreaPlace & { room }`).
#[derive(Clone, Debug, PartialEq)]
pub struct CampusArea {
    pub area: AreaPlace,
    pub room: usize,
}

impl Deref for CampusArea {
    type Target = AreaPlace;
    fn deref(&self) -> &AreaPlace {
        &self.area
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Campus {
    /// All of it, as the campus shows it.
    pub room: Rect,
    pub rooms: Vec<RoomPlace>,
    /// Every room's areas, obstacles and plants, in the campus's frame.
    pub areas: Vec<CampusArea>,
    pub obstacles: Vec<Rect>,
    pub plants: Vec<P>,
    pub spawn: Spot,
}

fn move_box(b: &Rect, ox: f64, oz: f64) -> Rect {
    rect(b.x0 + ox, b.z0 + oz, b.x1 + ox, b.z1 + oz)
}
fn move_spot(s: &Spot, ox: f64, oz: f64) -> Spot {
    Spot {
        x: s.x + ox,
        z: s.z + oz,
        face: s.face,
    }
}

/// floors_of splits a project's desks across the floors it needs, as evenly as
/// it can, in order.
pub fn floors_of(n: usize) -> Vec<usize> {
    let floors = 1.max(n.div_ceil(ROOM_DESKS));
    let base = n / floors;
    (0..floors)
        .map(|f| base + usize::from(f < n % floors))
        .collect()
}

struct Part {
    area: AreaIn,
    floor: i64,
    floors: usize,
}

/// layout places a model's projects as a campus: each project a building, one
/// room a floor, side by side in rows. A room depends only on its own
/// project's desks, so a desk keeps its place while other projects change.
pub fn layout(areas_in: &[AreaModel]) -> Campus {
    let mut parts: Vec<Part> = Vec::new();
    for a in areas_in {
        let sizes = floors_of(a.desks.len());
        let mut at = 0;
        for (f, &n) in sizes.iter().enumerate() {
            let desks = a.desks[at..at + n].iter().map(|d| d.id.clone()).collect();
            parts.push(Part {
                area: AreaIn {
                    project: a.project.clone(),
                    desks,
                    no_lead: f > 0,
                    floor: Some(f as i64 + 1),
                },
                floor: f as i64 + 1,
                floors: sizes.len(),
            });
            at += n;
        }
    }
    let locals: Vec<Layout> = if parts.is_empty() {
        vec![room_layout(&[])]
    } else {
        parts
            .iter()
            .map(|p| room_layout(std::slice::from_ref(&p.area)))
            .collect()
    };
    // Each building's footprint: its floors' widest and deepest, the steps back
    // included.
    let mut buildings: Vec<Vec<usize>> = Vec::new();
    for i in 0..locals.len() {
        match buildings.last_mut() {
            Some(last)
                if parts.get(i).is_some_and(|p| {
                    parts
                        .get(last[0])
                        .is_some_and(|q| q.area.project == p.area.project)
                }) =>
            {
                last.push(i)
            }
            _ => buildings.push(vec![i]),
        }
    }
    let foot_w = |b: &[usize]| fmax(b.iter().map(|&i| locals[i].room.x1));
    let foot_d = |b: &[usize]| {
        fmax(
            b.iter()
                .enumerate()
                .map(|(k, &i)| locals[i].room.z1 + k as f64 * FLOOR_STEP),
        )
    };
    // Rows of buildings: as many to a row as shows them biggest in a window
    // of about the app's shape, seen as the campus's view sees them (a little
    // from the right, from above), their height counted; a wider one when it's
    // about as good.
    struct Placed {
        at: Vec<(f64, f64)>,
        w: f64,
        d: f64,
        tall: f64,
    }
    let place = |per_row: usize| -> Placed {
        let mut at = vec![(0., 0.); buildings.len()];
        let mut rows: Vec<Vec<usize>> = Vec::new();
        for b in 0..buildings.len() {
            if rows.last().is_none_or(|r| r.len() >= per_row) {
                rows.push(Vec::new());
            }
            rows.last_mut().unwrap().push(b);
        }
        let mut z = 0.;
        for row in &rows {
            let mut x = 0.;
            for &b in row {
                // Its floors back a step each from the one below.
                at[b] = (x, z);
                x += foot_w(&buildings[b]) + CAMPUS_GAP;
            }
            z += fmax(row.iter().map(|&b| foot_d(&buildings[b]))) + CAMPUS_GAP_Z;
        }
        let w = fmax(
            buildings
                .iter()
                .enumerate()
                .map(|(b, bl)| at[b].0 + foot_w(bl)),
        );
        let d = fmax(
            buildings
                .iter()
                .enumerate()
                .map(|(b, bl)| at[b].1 + foot_d(bl)),
        );
        // A row narrower than the widest is centred under it: no hole in a corner.
        for row in &rows {
            let end = fmax(row.iter().map(|&b| at[b].0 + foot_w(&buildings[b])));
            for &b in row {
                at[b] = (at[b].0 + (w - end) / 2., at[b].1);
            }
        }
        let tall = fmax(buildings.iter().map(|bl| (bl.len() - 1) as f64)) * FLOOR_LIFT;
        Placed { at, w, d, tall }
    };
    let size = |c: &Placed| {
        let pw = c.w * 0.3f64.cos() + c.d * 0.3f64.sin();
        let ph = (c.w * 0.3f64.sin() + c.d * 0.3f64.cos()) * 0.6 + 3. + c.tall * 0.8;
        (pw / CAMPUS_ASPECT).max(ph)
    };
    let mut best = place(buildings.len());
    for k in (1..buildings.len()).rev() {
        let c = place(k);
        if size(&c) < size(&best) * 0.92 {
            best = c;
        }
    }
    let mut rooms: Vec<Option<RoomPlace>> = vec![None; locals.len()];
    let mut areas: Vec<CampusArea> = Vec::new();
    let mut obstacles: Vec<Rect> = Vec::new();
    let mut plants: Vec<P> = Vec::new();
    let mut shown_boxes: Vec<Rect> = Vec::new();
    for (b, bl) in buildings.iter().enumerate() {
        let base = best.at[b];
        for (k, &i) in bl.iter().enumerate() {
            let l = &locals[i];
            let p = parts.get(i);
            // Shown: on its building, a step back for each floor below.
            let sx = base.0;
            let sz = base.1 + k as f64 * FLOOR_STEP;
            // Walked in: the first floor where it's shown, the rest far behind.
            let ox = sx;
            let oz = if k == 0 {
                sz
            } else {
                sz + FLOOR_AWAY * k as f64
            };
            shown_boxes.push(move_box(&l.room, sx, sz));
            for a in &l.areas {
                areas.push(CampusArea {
                    area: AreaPlace {
                        project: a.project.clone(),
                        rug: move_box(&a.rug, ox, oz),
                        pods: a.pods.iter().map(|q| move_box(q, ox, oz)).collect(),
                        lead: a.lead.map(|l| LeadPlace {
                            x: l.x + ox,
                            z: l.z + oz,
                            spot: move_spot(&l.spot, ox, oz),
                        }),
                        desks: a
                            .desks
                            .iter()
                            .map(|d| DeskPlace {
                                x: d.x + ox,
                                z: d.z + oz,
                                spot: move_spot(&d.spot, ox, oz),
                                ..d.clone()
                            })
                            .collect(),
                        sign: pt(a.sign.x + ox, a.sign.z + oz),
                    },
                    room: i,
                });
            }
            for q in &l.obstacles {
                obstacles.push(move_box(q, ox, oz));
            }
            for q in &l.plants {
                plants.push(pt(q.x + ox, q.z + oz));
            }
            rooms[i] = Some(RoomPlace {
                key: match p {
                    Some(p) => format!("{}#{}", p.area.project, p.floor),
                    None => "#1".into(),
                },
                project: p.map_or(String::new(), |p| p.area.project.clone()),
                floor: p.map_or(1, |p| p.floor),
                floors: p.map_or(1, |p| p.floors),
                ox,
                oz,
                r#box: move_box(&l.room, ox, oz),
                shown: Shown {
                    dx: sx - ox,
                    dy: k as f64 * FLOOR_LIFT,
                    dz: sz - oz,
                },
                local: l.clone(),
                board: BoardPlace {
                    x: l.board.x + ox,
                    w: l.board.w,
                    spot: move_spot(&l.board.spot, ox, oz),
                },
                spawn: move_spot(&l.spawn, ox, oz),
                desks: l
                    .areas
                    .iter()
                    .flat_map(|a| a.desks.iter().map(|d| d.id.clone()))
                    .collect(),
                lead: p.is_some_and(|p| !p.area.no_lead),
            });
        }
    }
    let rooms: Vec<RoomPlace> = rooms
        .into_iter()
        .map(|r| r.expect("every room is in a building"))
        .collect();
    let room = rect(
        0.,
        0.,
        fmax(shown_boxes.iter().map(|q| q.x1)),
        fmax(shown_boxes.iter().map(|q| q.z1)),
    );
    let spawn = rooms[0].spawn;
    Campus {
        room,
        rooms,
        areas,
        obstacles,
        plants,
        spawn,
    }
}

/// board_ref is a room's board, as a ref.
pub fn board_ref(r: &RoomPlace) -> String {
    format!("board:{}", r.key)
}

/// room_of is the room a ref (a desk, a lead, a board) is in.
pub fn room_of(c: &Campus, r#ref: &str) -> Option<usize> {
    if r#ref.starts_with("board:") {
        return c.rooms.iter().position(|r| board_ref(r) == r#ref);
    }
    if r#ref.ends_with("/lead") {
        return c
            .rooms
            .iter()
            .position(|r| r.lead && format!("{}/lead", r.project) == r#ref);
    }
    c.rooms
        .iter()
        .position(|r| r.desks.iter().any(|d| d == r#ref))
}

/// room_at is the room whose floor p is on.
pub fn room_at(c: &Campus, p: P) -> Option<usize> {
    room_at_margin(c, p, 0.)
}

pub fn room_at_margin(c: &Campus, p: P, margin: f64) -> Option<usize> {
    c.rooms.iter().position(|r| {
        p.x >= r.r#box.x0 - margin
            && p.x <= r.r#box.x1 + margin
            && p.z >= r.r#box.z0 - margin
            && p.z <= r.r#box.z1 + margin
    })
}

/// spot_of is where you stand to look at a desk, a lead's counter or a board.
pub fn spot_of(c: &Campus, r#ref: &str) -> Option<Spot> {
    if r#ref.starts_with("board:") {
        return c
            .rooms
            .iter()
            .find(|r| board_ref(r) == r#ref)
            .map(|r| r.board.spot);
    }
    for a in &c.areas {
        if let Some(l) = &a.lead
            && r#ref == format!("{}/lead", a.project)
        {
            return Some(l.spot);
        }
        for d in &a.desks {
            if d.id == r#ref {
                return Some(d.spot);
            }
        }
    }
    None
}

/// How near a spot you are at it (TS: nearest's default `reach`).
pub const REACH: f64 = 1.1;

/// nearest is what you're standing at: the desk, counter or board whose spot is
/// within reach of p, the closest one.
pub fn nearest(c: &Campus, p: P) -> Option<String> {
    nearest_within(c, p, REACH)
}

pub fn nearest_within(c: &Campus, p: P, reach: f64) -> Option<String> {
    let mut best: Option<String> = None;
    let mut best_d = reach * reach;
    let mut consider = |r#ref: String, s: &Spot| {
        let d = (s.x - p.x).powi(2) + (s.z - p.z).powi(2);
        if d < best_d {
            best_d = d;
            best = Some(r#ref);
        }
    };
    for r in &c.rooms {
        consider(board_ref(r), &r.board.spot);
    }
    for a in &c.areas {
        if let Some(l) = &a.lead {
            consider(format!("{}/lead", a.project), &l.spot);
        }
        for d in &a.desks {
            consider(d.id.clone(), &d.spot);
        }
    }
    best
}

/// walkable is the obstacles grown by the walker's radius, which is what a
/// point (the walker's centre) mustn't enter.
pub fn walkable(c: &Campus, radius: f64) -> Vec<Rect> {
    c.obstacles.iter().map(|b| expand(b, radius)).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::model::{DeskModel, DeskTone, LeadModel, LeadTone};
    use crate::walk::{blocked, path};

    fn area(project: &str, n: usize) -> AreaModel {
        AreaModel {
            project: project.into(),
            label: project.into(),
            hue: 200.,
            lead: LeadModel {
                tone: LeadTone::Idle,
                line: None,
            },
            desks: (0..n)
                .map(|i| DeskModel {
                    id: format!("{project}/agent-{}", i + 1),
                    project: project.into(),
                    name: format!("agent-{}", i + 1),
                    title: String::new(),
                    branch: String::new(),
                    ai: "claude".into(),
                    tone: DeskTone::Working,
                    pr: None,
                    changes: None,
                    question: None,
                    last_active: None,
                })
                .collect(),
        }
    }

    fn overlaps(a: &Rect, b: &Rect) -> bool {
        a.x0 < b.x1 && b.x0 < a.x1 && a.z0 < b.z1 && b.z0 < a.z1
    }
    fn within(outer: &Rect, inner: &Rect) -> bool {
        inner.x0 >= outer.x0 - 1e-9
            && inner.x1 <= outer.x1 + 1e-9
            && inner.z0 >= outer.z0 - 1e-9
            && inner.z1 <= outer.z1 + 1e-9
    }

    #[test]
    fn pods_go_a_little_wider_than_deep() {
        assert_eq!(
            [0, 1, 2, 3, 4, 6, 8, 12, 18].map(columns),
            [1, 1, 2, 2, 2, 3, 3, 4, 5]
        );
    }

    #[test]
    fn a_project_of_more_desks_than_a_room_holds_gets_floors_evenly() {
        let got: Vec<Vec<usize>> = [0, 1, 8, 9, 12, 25, 40]
            .iter()
            .map(|&n| floors_of(n))
            .collect();
        assert_eq!(
            got,
            vec![
                vec![0],
                vec![1],
                vec![8],
                vec![5, 4],
                vec![6, 6],
                vec![7, 6, 6, 6],
                vec![8, 8, 8, 8, 8]
            ]
        );
        let c = layout(&[area("big", 25), area("small", 3)]);
        let got: Vec<(&str, i64, usize, usize, bool)> = c
            .rooms
            .iter()
            .map(|r| (r.project.as_str(), r.floor, r.floors, r.desks.len(), r.lead))
            .collect();
        assert_eq!(
            got,
            [
                ("big", 1, 4, 7, true),
                ("big", 2, 4, 6, false),
                ("big", 3, 4, 6, false),
                ("big", 4, 4, 6, false),
                ("small", 1, 1, 3, true)
            ]
        );
        for r in &c.rooms {
            assert!(r.desks.len() <= ROOM_DESKS);
        }
    }

    fn shown(r: &RoomPlace) -> Rect {
        rect(
            r.r#box.x0 + r.shown.dx,
            r.r#box.z0 + r.shown.dz,
            r.r#box.x1 + r.shown.dx,
            r.r#box.z1 + r.shown.dz,
        )
    }

    #[test]
    fn each_room_is_a_diorama_of_its_own_the_campus_about_as_wide_as_deep_or_wider_none_overlapping()
     {
        for n in [vec![1], vec![3, 2], vec![30, 4, 2], vec![3, 7, 1, 12, 2]] {
            let areas: Vec<AreaModel> = n
                .iter()
                .enumerate()
                .map(|(i, &k)| area(&format!("p{i}"), k))
                .collect();
            let c = layout(&areas);
            let r = c.room.x1 / c.room.z1;
            assert!(r > 0.6 && r < 4.5, "{n:?}: {r:.2}");
            // Shown on the campus, each building's floors on each other; walked in, no
            // two rooms anywhere near.
            for room in &c.rooms {
                assert!(
                    within(&c.room, &shown(room)),
                    "{} is in the campus",
                    room.key
                );
            }
            for i in 0..c.rooms.len() {
                for j in i + 1..c.rooms.len() {
                    assert!(
                        !overlaps(&c.rooms[i].r#box, &c.rooms[j].r#box),
                        "{} and {}",
                        c.rooms[i].key,
                        c.rooms[j].key
                    );
                    if c.rooms[i].project != c.rooms[j].project {
                        assert!(
                            !overlaps(&shown(&c.rooms[i]), &shown(&c.rooms[j])),
                            "{} and {} shown",
                            c.rooms[i].key,
                            c.rooms[j].key
                        );
                    }
                }
            }
            // A project's floors one over another.
            for room in &c.rooms {
                assert_eq!(room.shown.dy, (room.floor - 1) as f64 * FLOOR_LIFT);
            }
            // A room is small: about the target's, never the size of a whole office.
            for room in &c.rooms {
                assert!(
                    room.r#box.x1 - room.r#box.x0 < 32. && room.r#box.z1 - room.r#box.z0 < 18.,
                    "{} is small",
                    room.key
                );
            }
        }
    }

    #[test]
    fn an_empty_office_is_still_a_room_with_a_board_and_a_commons() {
        let c = layout(&[]);
        assert_eq!(c.areas.len(), 0);
        assert_eq!(c.rooms.len(), 1);
        assert!(c.rooms[0].r#box.x1 >= 12.);
        assert!(c.spawn.z > c.rooms[0].board.spot.z);
    }

    #[test]
    fn every_desk_is_on_its_own_area_in_its_own_room_and_found_there() {
        let c = layout(&[area("a", 3), area("b", 14), area("c", 1)]);
        for a in &c.areas {
            let room = &c.rooms[a.room];
            assert!(
                within(&room.r#box, &a.rug),
                "{}'s zone is in its room",
                a.project
            );
            for d in &a.desks {
                assert!(d.x > a.rug.x0 && d.x < a.rug.x1 && d.z > a.rug.z0 && d.z < a.rug.z1);
                assert_eq!(room_of(&c, &d.id), Some(a.room));
                assert_eq!(room_at(&c, d.spot.at()), Some(a.room));
            }
        }
        assert_eq!(
            room_of(&c, "b/lead"),
            c.rooms.iter().position(|r| r.key == "b#1")
        );
        assert_eq!(room_of(&c, &board_ref(&c.rooms[2])), Some(2));
    }

    #[test]
    fn a_desk_keeps_its_place_while_another_projects_agents_come() {
        let before = layout(&[area("a", 2), area("b", 2)]);
        let after = layout(&[area("a", 2), area("b", 5)]);
        let at = |c: &Campus| {
            let d = &c.areas[0].desks[0];
            let r = &c.rooms[c.areas[0].room];
            [d.x - r.ox, d.z - r.oz].map(|v| js_round(v * 1e6) / 1e6)
        };
        assert_eq!(at(&after), at(&before));
    }

    fn footprint(p: &Prop) -> Rect {
        let (w, d) = foot_of(p.kind);
        let turned = p.rot.sin().abs() > 0.5;
        let hw = (if turned { d } else { w }) / 2.;
        let hd = (if turned { w } else { d }) / 2.;
        rect(p.x - hw, p.z - hd, p.x + hw, p.z + hd)
    }

    #[test]
    fn every_room_is_furnished_and_nothing_stands_on_anything_else() {
        // Names whose commons are each of its three kinds.
        for name in ["p", "storefront", "quill", "agentbox", "x", "yy"] {
            for n in [12usize, 9, 8, 4, 2, 1, 0] {
                for floor in [1, 2, 3, 4] {
                    let areas = if n > 0 {
                        vec![AreaIn {
                            floor: Some(floor),
                            no_lead: floor > 1,
                            ..(&area(name, n)).into()
                        }]
                    } else {
                        vec![]
                    };
                    let l = room_layout(&areas);
                    // Every cell holds something; these are the things besides the pods, the
                    // counter, the meeting room and the lounge.
                    assert!(
                        l.props.len() >= if n > 0 { 4 } else { 3 },
                        "{name} {n}: {} things",
                        l.props.len()
                    );
                    // Every room has its kitchen, its island at least.
                    assert!(
                        l.props.iter().any(|p| p.kind == PropKind::Island),
                        "{name} {n} {floor}: a kitchen"
                    );
                    // Mats are floor, walked over: what's on them mustn't overlap but may sit on one.
                    let solid: Vec<&Prop> =
                        l.props.iter().filter(|p| p.kind != PropKind::Mat).collect();
                    let feet: Vec<Rect> = solid.iter().map(|p| footprint(p)).collect();
                    for (i, f) in feet.iter().enumerate() {
                        let kind = solid[i].kind;
                        assert!(within(&l.room, f), "{n}: {kind:?} is in the room");
                        assert!(!overlaps(f, &l.sofa), "{n}: {kind:?} is clear of the sofa");
                        assert!(
                            !overlaps(f, &l.meeting),
                            "{name} {n}: {kind:?} is out of the meeting room"
                        );
                        // Nothing stands before the board.
                        assert!(
                            !overlaps(
                                f,
                                &rect(
                                    l.board.x - l.board.w / 2.,
                                    0.,
                                    l.board.x + l.board.w / 2.,
                                    1.4
                                )
                            ),
                            "{name} {n}: {kind:?} is clear of the board"
                        );
                        for a in &l.areas {
                            for pod in &a.pods {
                                assert!(
                                    !overlaps(f, pod),
                                    "{n}: {kind:?} is off {}'s pods",
                                    a.project
                                );
                            }
                            let Some(lead) = &a.lead else { continue };
                            let counter = rect(
                                lead.x - COUNTER.w / 2.,
                                lead.z - 1.2,
                                lead.x + COUNTER.w / 2.,
                                lead.z + COUNTER.d / 2.,
                            );
                            assert!(
                                !overlaps(f, &counter),
                                "{n}: {kind:?} is clear of {}'s counter",
                                a.project
                            );
                        }
                        for j in i + 1..feet.len() {
                            assert!(
                                !overlaps(f, &feet[j]),
                                "{name} {n} {floor}: {kind:?} and {:?}",
                                solid[j].kind
                            );
                        }
                    }
                }
            }
        }
    }

    fn every_spot_is_free_floor(sizes: &[usize]) {
        let areas: Vec<AreaModel> = sizes
            .iter()
            .enumerate()
            .map(|(i, &k)| area(&format!("p{i}"), k))
            .collect();
        let c = layout(&areas);
        // As the scene walks you (scene.ts's RADIUS).
        let r = 0.45;
        let boxes = walkable(&c, r);
        let mut spots: Vec<String> = c.rooms.iter().map(board_ref).collect();
        for a in &c.areas {
            if a.lead.is_some() {
                spots.push(format!("{}/lead", a.project));
            }
            spots.extend(a.desks.iter().map(|d| d.id.clone()));
        }
        for r#ref in spots {
            let s = spot_of(&c, &r#ref).unwrap_or_else(|| panic!("{}", r#ref));
            let room = &c.rooms[room_of(&c, &r#ref).unwrap()];
            let bounds = rect(
                room.r#box.x0 + r,
                room.r#box.z0 + r,
                room.r#box.x1 - r,
                room.r#box.z1 - r,
            );
            assert!(
                !blocked(&boxes, &bounds, s.at()),
                "{}'s spot is free",
                r#ref
            );
            let route = path(&boxes, &bounds, room.spawn.at(), s.at());
            assert!(!route.is_empty(), "{} can be walked to", r#ref);
            assert_eq!(*route.last().unwrap(), s.at());
            assert_eq!(
                nearest(&c, s.at()).as_deref(),
                Some(r#ref.as_str()),
                "standing at {} is at it",
                r#ref
            );
        }
        for room in &c.rooms {
            assert_eq!(nearest(&c, room.spawn.at()), None);
            let bounds = rect(
                room.r#box.x0 + r,
                room.r#box.z0 + r,
                room.r#box.x1 - r,
                room.r#box.z1 - r,
            );
            assert!(
                !blocked(&boxes, &bounds, room.spawn.at()),
                "{}'s door is free floor",
                room.key
            );
        }
    }

    #[test]
    fn every_spot_is_free_floor_and_you_can_walk_to_each_from_its_rooms_door_5_1_10() {
        every_spot_is_free_floor(&[5, 1, 10]);
    }

    #[test]
    fn every_spot_is_free_floor_and_you_can_walk_to_each_from_its_rooms_door_30_4_2() {
        every_spot_is_free_floor(&[30, 4, 2]);
    }
}
