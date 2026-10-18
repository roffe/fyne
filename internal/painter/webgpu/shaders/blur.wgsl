// Port of blur.frag, one direction of the separable gaussian per draw. The
// kernel rides in the uniforms rather than a texture. Mirrored by blurParams.
struct Blur {
    // clip-space x1, y1 (top), x2, y2 (bottom) of the quad rasterised
    quad: vec4<f32>,
    // x: kernel radius, y: sample scale, zw: one step along the blur direction
    params: vec4<f32>,
    // x: corner radius, yz: size in pixels
    shape: vec4<f32>,
    // weights four to a vec4, 2 * floor(radius) + 1 of them
    kernel: array<vec4<f32>, 26>,
}

@group(0) @binding(0) var<uniform> u: Blur;
@group(1) @binding(0) var tex: texture_2d<f32>;
@group(1) @binding(1) var smp: sampler;

struct VOut {
    @builtin(position) pos: vec4<f32>,
    @location(0) uv: vec2<f32>,
}

@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> VOut {
    let right = (i & 1u) != 0u;
    let bottom = (i & 2u) != 0u;
    var out: VOut;
    out.pos = vec4<f32>(select(u.quad.x, u.quad.z, right), select(u.quad.y, u.quad.w, bottom), 0.0, 1.0);
    out.uv = vec2<f32>(select(0.0, 1.0, right), select(0.0, 1.0, bottom));
    return out;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    var alpha = 1.0;
    let corner_radius = u.shape.x;
    if (corner_radius > 0.5) {
        let size = u.shape.yz;
        let p = in.uv * size;
        let half_size = size * 0.5;
        let q = abs(p - half_size) - half_size + corner_radius;
        let dist = min(max(q.x, q.y), 0.0) + length(max(q, vec2<f32>(0.0))) - corner_radius;
        alpha = 1.0 - smoothstep(-0.5, 0.5, dist);
    }

    let radius = u.params.x;
    let count = 2 * i32(radius) + 1;
    var sum = vec4<f32>(0.0);
    for (var i = 0; i < count; i++) {
        let offset = (f32(i) - radius) * u.params.y;
        sum += u.kernel[i / 4][i % 4] * textureSampleLevel(tex, smp, in.uv + u.params.zw * offset, 0.0);
    }

    return mix(textureSampleLevel(tex, smp, in.uv, 0.0), sum, alpha);
}
