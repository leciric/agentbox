//! Walking (#668's walk.ts): sliding along what's in the way, and a path
//! round it. Pure, so it has tests.

use crate::layout::Rect;

#[derive(Clone, Copy, Debug, PartialEq)]
pub struct P {
    pub x: f64,
    pub z: f64,
}

pub const fn p(x: f64, z: f64) -> P {
    P { x, z }
}

fn inside(b: &Rect, p: P) -> bool {
    p.x > b.x0 && p.x < b.x1 && p.z > b.z0 && p.z < b.z1
}

/// blocked: outside the room, or inside anything in it.
pub fn blocked(boxes: &[Rect], room: &Rect, p: P) -> bool {
    p.x < room.x0 || p.x > room.x1 || p.z < room.z0 || p.z > room.z1 || boxes.iter().any(|b| inside(b, p))
}

/// slide moves p by d, each axis on its own, so you slide along a wall
/// rather than stop dead at it.
pub fn slide(boxes: &[Rect], room: &Rect, p: P, d: P) -> P {
    let mut x = p.x;
    let mut z = p.z;
    if !blocked(boxes, room, P { x: x + d.x, z }) {
        x += d.x;
    }
    if !blocked(boxes, room, P { x, z: z + d.z }) {
        z += d.z;
    }
    P { x, z }
}

/// clear: a straight walk from a to b meets nothing.
pub fn clear(boxes: &[Rect], room: &Rect, a: P, b: P) -> bool {
    clear_step(boxes, room, a, b, 0.1)
}

pub fn clear_step(boxes: &[Rect], room: &Rect, a: P, b: P, step: f64) -> bool {
    let len = (b.x - a.x).hypot(b.z - a.z);
    let n = ((len / step).ceil() as usize).max(1);
    (1..=n).all(|i| {
        let t = i as f64 / n as f64;
        !blocked(boxes, room, P { x: a.x + (b.x - a.x) * t, z: a.z + (b.z - a.z) * t })
    })
}

const CELL: f64 = 0.25;

/// path is the way from a to b round what's in it: A* on a grid of quarter
/// metres, then straightened to the fewest straight legs. Its points come
/// after a, the last b itself (or the nearest free spot to it); empty when
/// there's no way.
pub fn path(boxes: &[Rect], room: &Rect, a: P, b: P) -> Vec<P> {
    if clear(boxes, room, a, b) {
        return vec![b];
    }
    let cols = (((room.x1 - room.x0) / CELL).ceil() as i64).max(1);
    let rows = (((room.z1 - room.z0) / CELL).ceil() as i64).max(1);
    let centre = |i: i64| P { x: room.x0 + ((i % cols) as f64 + 0.5) * CELL, z: room.z0 + ((i / cols) as f64 + 0.5) * CELL };
    let cell_of = |p: P| {
        let cx = (((p.x - room.x0) / CELL).floor() as i64).clamp(0, cols - 1);
        let cz = (((p.z - room.z0) / CELL).floor() as i64).clamp(0, rows - 1);
        cz * cols + cx
    };
    let n = (cols * rows) as usize;
    let free: Vec<bool> = (0..n as i64).map(|i| !blocked(boxes, room, centre(i))).collect();
    let nearest_free = |from: i64| -> i64 {
        if free[from as usize] {
            return from;
        }
        let fx = from % cols;
        let fz = from / cols;
        for r in 1..cols.max(rows) {
            let mut best = -1;
            let mut best_d = i64::MAX;
            for dz in -r..=r {
                for dx in -r..=r {
                    if dx.abs().max(dz.abs()) != r {
                        continue;
                    }
                    let x = fx + dx;
                    let z = fz + dz;
                    if x < 0 || z < 0 || x >= cols || z >= rows || !free[(z * cols + x) as usize] {
                        continue;
                    }
                    let d = dx * dx + dz * dz;
                    if d < best_d {
                        best_d = d;
                        best = z * cols + x;
                    }
                }
            }
            if best >= 0 {
                return best;
            }
        }
        -1
    };
    let start = nearest_free(cell_of(a));
    let goal_cell = nearest_free(cell_of(b));
    if start < 0 || goal_cell < 0 {
        return Vec::new();
    }
    let goal = if !blocked(boxes, room, b) { b } else { centre(goal_cell) };

    let mut g = vec![f64::INFINITY; n];
    let mut from = vec![-1i64; n];
    let mut closed = vec![false; n];
    let gx = goal_cell % cols;
    let gz = goal_cell / cols;
    let h = |i: i64| {
        let dx = ((i % cols) - gx).abs() as f64;
        let dz = ((i / cols) - gz).abs() as f64;
        dx.max(dz) + (std::f64::consts::SQRT_2 - 1.) * dx.min(dz)
    };
    // A binary heap of (f, cell), smallest f first, ties as #668's.
    let mut heap: Vec<(f64, i64)> = Vec::new();
    let push = |heap: &mut Vec<(f64, i64)>, f: f64, i: i64| {
        heap.push((f, i));
        let mut k = heap.len() - 1;
        while k > 0 {
            let up = (k - 1) >> 1;
            if heap[up].0 <= heap[k].0 {
                break;
            }
            heap.swap(up, k);
            k = up;
        }
    };
    let pop = |heap: &mut Vec<(f64, i64)>| -> i64 {
        let top = heap[0].1;
        let last = heap.pop().unwrap();
        if !heap.is_empty() {
            heap[0] = last;
            let mut k = 0;
            loop {
                let l = k * 2 + 1;
                let r = l + 1;
                let mut m = k;
                if l < heap.len() && heap[l].0 < heap[m].0 {
                    m = l;
                }
                if r < heap.len() && heap[r].0 < heap[m].0 {
                    m = r;
                }
                if m == k {
                    break;
                }
                heap.swap(m, k);
                k = m;
            }
        }
        top
    };
    g[start as usize] = 0.;
    push(&mut heap, h(start), start);
    let mut found = false;
    while !heap.is_empty() {
        let i = pop(&mut heap);
        if closed[i as usize] {
            continue;
        }
        closed[i as usize] = true;
        if i == goal_cell {
            found = true;
            break;
        }
        let x = i % cols;
        let z = i / cols;
        for dz in -1..=1i64 {
            for dx in -1..=1i64 {
                if dx == 0 && dz == 0 {
                    continue;
                }
                let nx = x + dx;
                let nz = z + dz;
                if nx < 0 || nz < 0 || nx >= cols || nz >= rows {
                    continue;
                }
                let nn = (nz * cols + nx) as usize;
                if !free[nn] || closed[nn] {
                    continue;
                }
                // No cutting a corner between two blocked cells.
                if dx != 0 && dz != 0 && (!free[(z * cols + nx) as usize] || !free[(nz * cols + x) as usize]) {
                    continue;
                }
                let cost = g[i as usize] + if dx != 0 && dz != 0 { std::f64::consts::SQRT_2 } else { 1. };
                if cost < g[nn] {
                    g[nn] = cost;
                    from[nn] = i;
                    push(&mut heap, cost + h(nn as i64), nn as i64);
                }
            }
        }
    }
    if !found {
        return Vec::new();
    }
    let mut cells = Vec::new();
    let mut i = goal_cell;
    while i != start && i >= 0 {
        cells.push(centre(i));
        i = from[i as usize];
    }
    cells.reverse();
    if cells.is_empty() {
        // (#668's too: already in the goal's cell, with no straight way.)
        return Vec::new();
    }
    *cells.last_mut().unwrap() = goal;
    let mut out = Vec::new();
    let mut at = a;
    let mut k = 0;
    while k < cells.len() {
        let mut far = k;
        for j in (k + 1..cells.len()).rev() {
            if clear(boxes, room, at, cells[j]) {
                far = j;
                break;
            }
        }
        out.push(cells[far]);
        at = cells[far];
        k = far + 1;
    }
    out
}

/// path_length is how far a route is from a.
pub fn path_length(a: P, route: &[P]) -> f64 {
    let mut len = 0.;
    let mut at = a;
    for p in route {
        len += (p.x - at.x).hypot(p.z - at.z);
        at = *p;
    }
    len
}

/// turn turns an angle from `from` towards `to` by at most `max`, the short way.
pub fn turn(from: f64, to: f64, max: f64) -> f64 {
    use std::f64::consts::PI;
    let mut d = to - from;
    while d > PI {
        d -= PI * 2.;
    }
    while d < -PI {
        d += PI * 2.;
    }
    from + d.clamp(-max, max)
}

#[cfg(test)]
mod tests {
    use super::*;

    const ROOM: Rect = Rect { x0: 0., z0: 0., x1: 10., z1: 10. };
    const WALL: Rect = Rect { x0: 4., z0: 0., x1: 6., z1: 8. };

    #[test]
    fn a_move_into_a_box_slides_along_its_side() {
        assert_eq!(slide(&[WALL], &ROOM, p(3.9, 5.), p(0.5, 0.5)), p(3.9, 5.5));
        assert_eq!(slide(&[WALL], &ROOM, p(1., 1.), p(0.5, 0.)), p(1.5, 1.));
        // The room's walls hold too.
        assert_eq!(slide(&[], &ROOM, p(9.9, 1.), p(0.5, 0.)), p(9.9, 1.));
    }

    #[test]
    fn a_straight_line_is_clear_only_when_it_crosses_nothing() {
        assert!(clear(&[WALL], &ROOM, p(1., 9.), p(9., 9.)));
        assert!(!clear(&[WALL], &ROOM, p(1., 1.), p(9., 1.)));
    }

    #[test]
    fn a_walk_goes_round_the_wall_in_a_few_straight_legs() {
        let a = p(1., 1.);
        let b = p(9., 1.);
        let route = path(&[WALL], &ROOM, a, b);
        assert!(route.len() >= 2 && route.len() <= 4, "legs: {}", route.len());
        assert_eq!(*route.last().unwrap(), b);
        let mut at = a;
        for &q in &route {
            assert!(clear(&[WALL], &ROOM, at, q), "each leg is clear");
            at = q;
        }
        // Round the end of the wall at z = 8: about 8 down, 2 across, 8 up.
        assert!(path_length(a, &route) < 20.);
    }

    #[test]
    fn a_goal_inside_a_box_walks_to_the_free_floor_nearest_it() {
        let route = path(&[WALL], &ROOM, p(1., 1.), p(4.2, 3.));
        let end = *route.last().unwrap();
        assert!(!blocked(&[WALL], &ROOM, end));
        assert!((end.x - 4.).abs() < 0.5 && (end.z - 3.).abs() < 0.5, "{end:?}");
    }

    #[test]
    fn a_goal_nothing_reaches_is_no_walk() {
        let boxed = [Rect { x0: 7., z0: 7., x1: 10., z1: 7.5 }, Rect { x0: 7., z0: 7., x1: 7.5, z1: 10. }];
        assert_eq!(path(&boxed, &ROOM, p(1., 1.), p(9., 9.)), Vec::<P>::new());
    }

    #[test]
    fn turning_goes_the_short_way_round() {
        assert!((turn(3., -3., 0.1) - 3.1).abs() < 1e-9);
        assert_eq!(turn(0., 1., 2.), 1.);
    }
}
