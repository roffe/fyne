// Prepended to every vector shape shader. Consecutive draws of one shape are
// one instanced draw: instance i reads records[i], where each shader declares
// what a Record holds and Go appends RECORDS, how many fit one bind.
//
// The fragment maths is a line by line port of the GLSL shaders in
// internal/painter/gl/shaders, which work in GL window coordinates (origin
// bottom left, y up); gl_frag_coord converts to those so the port stays literal.

// Shape is one shape's uniforms, mirrored by shapeParams in Go.
struct Shape {
    // clip-space x1, y1 (top), x2, y2 (bottom) of the quad rasterised
    quad: vec4<f32>,
    // xy: frame size in pixels, z: edge softness, w: stroke width (half of it
    // for the round rectangle and bezier curve)
    frame: vec4<f32>,
    // the shape's x1, y1, x2, y2 in frame pixels, y down
    bounds: vec4<f32>,
    fill_color: vec4<f32>,
    stroke_color: vec4<f32>,
    radius: vec4<f32>,
    // per shape, see each shader
    params: vec4<f32>,
    params2: vec4<f32>,
    shadow_color: vec4<f32>,
    // xy: offset, z: blur radius, w: spread
    shadow: vec4<f32>,
    // x: 1 when a shadow is drawn, y: its variant
    shadow_style: vec4<f32>,
}

@group(0) @binding(0) var<uniform> records: array<Record, RECORDS>;

// u is the fragment's shape, loaded by each fs_main before anything else.
var<private> u: Shape;

const PI: f32 = 3.141592653589793;

struct VOut {
    @builtin(position) pos: vec4<f32>,
    @location(0) @interpolate(flat) idx: u32,
}

@vertex
fn vs_main(@builtin(vertex_index) i: u32, @builtin(instance_index) idx: u32) -> VOut {
    let q = records[idx].shape.quad;
    var out: VOut;
    out.pos = vec4<f32>(select(q.x, q.z, (i & 1u) != 0u), select(q.y, q.w, (i & 2u) != 0u), 0.0, 1.0);
    out.idx = idx;
    return out;
}

// gl_frag_coord is gl_FragCoord.xy for a fragment position.
fn gl_frag_coord(pos: vec4<f32>) -> vec2<f32> {
    return vec2<f32>(pos.x, u.frame.y - pos.y);
}

// sstep is GLSL's smoothstep written out, because the shapes fade with
// reversed edges, which WGSL's smoothstep leaves undefined.
fn sstep(e0: f32, e1: f32, x: f32) -> f32 {
    let t = clamp((x - e0) / (e1 - e0), 0.0, 1.0);
    return t * t * (3.0 - 2.0 * t);
}

// glmod is GLSL's mod, which floors where WGSL's % truncates.
fn glmod(x: f32, y: f32) -> f32 {
    return x - y * floor(x / y);
}

fn rotate(a: f32) -> mat2x2<f32> {
    let s = sin(-a);
    let c = cos(-a);
    return mat2x2<f32>(c, -s, s, c);
}

// centered_pos is the fragment relative to the centre of the shape's bounds.
fn centered_pos(fc: vec2<f32>) -> vec2<f32> {
    let b = u.bounds;
    let rect = vec4<f32>(b.x, b.z, u.frame.y - b.w, u.frame.y - b.y);
    return fc - vec2<f32>(rect.x + rect.y, rect.z + rect.w) * 0.5;
}

fn blend_shadow(color: vec4<f32>, shadow: vec4<f32>) -> vec4<f32> {
    let alpha = color.a + shadow.a * (1.0 - color.a);
    if (alpha <= 0.0) {
        return vec4<f32>(0.0); // GL divides by zero here
    }
    return vec4<f32>((color.rgb * color.a + shadow.rgb * shadow.a * (1.0 - color.a)) / alpha, alpha);
}
