// Port of simple.frag: images, rasters, gradients and whole-run text, all
// uploaded premultiplied. Consecutive draws of one texture are one instanced
// draw, see common.wgsl. Tex is mirrored by texParams in Go.
struct Tex {
    // clip-space x1, y1 (top), x2, y2 (bottom) of the quad rasterised
    quad: vec4<f32>,
    // texture coordinates at the quad's top left and bottom right
    inset: vec4<f32>,
    // x: alpha, y: corner radius in pixels, zw: size of the image in pixels
    params: vec4<f32>,
}

@group(0) @binding(0) var<uniform> records: array<Tex, RECORDS>;
@group(1) @binding(0) var tex: texture_2d<f32>;
@group(1) @binding(1) var smp: sampler;

struct VOut {
    @builtin(position) pos: vec4<f32>,
    @location(0) uv: vec2<f32>,
    @location(1) @interpolate(flat) idx: u32,
}

@vertex
fn vs_main(@builtin(vertex_index) i: u32, @builtin(instance_index) idx: u32) -> VOut {
    let u = records[idx];
    let right = (i & 1u) != 0u;
    let bottom = (i & 2u) != 0u;
    var out: VOut;
    out.pos = vec4<f32>(select(u.quad.x, u.quad.z, right), select(u.quad.y, u.quad.w, bottom), 0.0, 1.0);
    out.uv = vec2<f32>(select(u.inset.x, u.inset.z, right), select(u.inset.y, u.inset.w, bottom));
    out.idx = idx;
    return out;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    let u = records[in.idx];
    let sampled = textureSampleLevel(tex, smp, in.uv, 0.0);
    let corner_radius = u.params.y;
    var frag_alpha = 1.0;
    if (corner_radius > 0.5) {
        // normalize texture coords from [insetMin, insetMax] to [0, 1]
        // this makes the rounding calculation work correctly for cropped/covered images
        let normalized = (in.uv - u.inset.xy) / (u.inset.zw - u.inset.xy);

        let size = u.params.zw;
        let p = normalized * size;
        let half_size = size * 0.5;
        let dist = length(max(abs(p - half_size) - half_size + corner_radius, vec2<f32>(0.0))) - corner_radius;
        frag_alpha = 1.0 - smoothstep(-1.0, 1.0, dist);
    }

    let color = sampled * u.params.x * frag_alpha;
    if (color.a < 0.01) {
        discard;
    }
    return color;
}
