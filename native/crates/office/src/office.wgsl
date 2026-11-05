// The Office's one material: a texture (white when there's none) times the
// vertex's colour and the copy's tint, lit by the sky and the ground (a
// hemisphere, as three.js's HemisphereLight) and a low sun, as #668's room.
struct Globals {
  view_proj: mat4x4<f32>,
  sun: vec4<f32>,
  sky: vec4<f32>,
  ground: vec4<f32>,
  sun_color: vec4<f32>,
  eye: vec4<f32>,
  fog: vec4<f32>,
};
@group(0) @binding(0) var<uniform> g: Globals;
@group(1) @binding(0) var tex: texture_2d<f32>;
@group(1) @binding(1) var samp: sampler;

struct VIn {
  @location(0) pos: vec3<f32>,
  @location(1) normal: vec3<f32>,
  @location(2) uv: vec2<f32>,
  @location(3) color: vec4<f32>,
  @location(4) m0: vec4<f32>,
  @location(5) m1: vec4<f32>,
  @location(6) m2: vec4<f32>,
  @location(7) m3: vec4<f32>,
  @location(8) tint: vec4<f32>,
};
struct VOut {
  @builtin(position) clip: vec4<f32>,
  @location(0) normal: vec3<f32>,
  @location(1) uv: vec2<f32>,
  @location(2) color: vec4<f32>,
};

@vertex fn vs(v: VIn) -> VOut {
  let m = mat4x4<f32>(v.m0, v.m1, v.m2, v.m3);
  let world = m * vec4<f32>(v.pos, 1.0);
  var o: VOut;
  o.clip = g.view_proj * world;
  o.normal = (m * vec4<f32>(v.normal, 0.0)).xyz;
  o.uv = v.uv;
  // A copy's tint scales its opacity, keeping the glow mark (alpha + 2).
  let glow = v.color.a > 1.5;
  let a = select(v.color.a, v.color.a - 2.0, glow) * v.tint.a;
  o.color = vec4<f32>(v.color.rgb * v.tint.rgb, a + select(0.0, 2.0, glow));
  return o;
}

@fragment fn fs(i: VOut) -> @location(0) vec4<f32> {
  // Alpha over 1.5 marks something lit by itself (a screen, a lamp): its
  // opacity is what's over 2.
  let glow = i.color.a > 1.5;
  let a = select(i.color.a, i.color.a - 2.0, glow);
  let base = textureSample(tex, samp, i.uv) * vec4<f32>(i.color.rgb, a);
  if (glow) {
    return base;
  }
  let n = normalize(i.normal);
  let hemi = mix(g.ground.rgb, g.sky.rgb, n.y * 0.5 + 0.5);
  let lit = hemi + g.sun_color.rgb * max(dot(n, g.sun.xyz), 0.0);
  return vec4<f32>(base.rgb * lit, base.a);
}
