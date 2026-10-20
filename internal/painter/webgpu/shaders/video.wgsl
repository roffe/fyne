// VideoFrames: a luma plane and chroma in two planes or one interleaved, turned
// and converted to RGB as they are drawn. Video is mirrored by videoParams in Go.
struct Video {
    // clip-space x1, y1 (top), x2, y2 (bottom) of the quad rasterised
    quad: vec4<f32>,
    // the picture's coordinates, as shown, at the quad's top left and bottom right
    inset: vec4<f32>,
    // x: clockwise quarter turns, y: 1 when chroma holds U and V interleaved,
    // z: 1 when samples take two channels, low byte first
    params: vec4<f32>,
    // each channel is dot(row.xyz, yuv) + row.w
    r: vec4<f32>,
    g: vec4<f32>,
    b: vec4<f32>,
    // x: 0 for SDR, 1 for PQ, 2 for HLG; y: the peak as a PQ signal; z, w: SDR
    // white and where the curve down to it starts, as fractions of y
    hdr: vec4<f32>,
}

@group(0) @binding(0) var<uniform> records: array<Video, RECORDS>;
@group(1) @binding(0) var luma: texture_2d<f32>;
@group(1) @binding(1) var chroma: texture_2d<f32>;
@group(1) @binding(2) var chroma_v: texture_2d<f32>;
@group(1) @binding(3) var smp: sampler;

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

// two bytes of a sample, as fractions of 255, to the sample as a fraction of 65535
const bytes = vec2<f32>(1.0 / 257.0, 256.0 / 257.0);

// SMPTE ST 2084's curve between signal and light, as a fraction of 10000 cd/m²
fn pq_light(e: f32) -> f32 {
    let p = pow(clamp(e, 0.0, 1.0), 1.0 / 78.84375);
    return pow(max(p - 0.8359375, 0.0) / (18.8515625 - 18.6875 * p), 1.0 / 0.1593017578125);
}

fn pq_signal(l: f32) -> f32 {
    let p = pow(max(l, 0.0), 0.1593017578125);
    return pow((0.8359375 + 18.8515625 * p) / (1.0 + 18.6875 * p), 78.84375);
}

// the light HLG's signal makes on a 1000 cd/m² screen, as a fraction of 10000
fn hlg_light(e: vec3<f32>) -> vec3<f32> {
    let scene = select((exp((e - 0.55991073) / 0.17883277) + 0.28466892) / 12.0, e * e / 3.0, e <= vec3<f32>(0.5));
    return scene * 0.1 * pow(max(dot(scene, vec3<f32>(0.2627, 0.6780, 0.0593)), 1e-6), 0.2);
}

// BT.2020 primaries to BT.709's, by columns
const to709 = mat3x3<f32>(
    vec3<f32>(1.660491, -0.124550, -0.018151),
    vec3<f32>(-0.587641, 1.132900, -0.100579),
    vec3<f32>(-0.072850, -0.008349, 1.118730),
);

// sdr tone maps e, an HDR signal, to SDR as VideoHDR in Go describes.
fn sdr(e: vec3<f32>, hdr: vec4<f32>) -> vec3<f32> {
    var l: vec3<f32>;
    if (hdr.x > 1.5) {
        l = hlg_light(e);
    } else {
        l = vec3<f32>(pq_light(e.r), pq_light(e.g), pq_light(e.b));
    }
    l = to709 * l;
    let m = max(l.r, max(l.g, l.b));
    let x = min(pq_signal(m) / hdr.y, 1.0);
    if (m > 0.0 && x > hdr.w) {
        let t = (x - hdr.w) / (1.0 - hdr.w);
        let t2 = t * t;
        let t3 = t2 * t;
        let s = (2.0 * t3 - 3.0 * t2 + 1.0) * hdr.w + (t3 - 2.0 * t2 + t) * (1.0 - hdr.w) + (3.0 * t2 - 2.0 * t3) * hdr.z;
        l *= pq_light(s * hdr.y) / m;
    }
    return pow(clamp(l * (10000.0 / 203.0), vec3<f32>(0.0), vec3<f32>(1.0)), vec3<f32>(1.0 / 2.4));
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    let u = records[in.idx];
    // from where the fragment is in the picture as shown to where it is stored
    var p = in.uv;
    let turns = u32(u.params.x);
    if (turns == 1u) {
        p = vec2<f32>(p.y, 1.0 - p.x);
    } else if (turns == 2u) {
        p = 1.0 - p;
    } else if (turns == 3u) {
        p = vec2<f32>(1.0 - p.y, p.x);
    }
    let l = textureSampleLevel(luma, smp, p, 0.0);
    let c = textureSampleLevel(chroma, smp, p, 0.0);
    let cv = textureSampleLevel(chroma_v, smp, p, 0.0);
    var yuv = vec3<f32>(l.r, c.r, select(cv.r, c.g, u.params.y > 0.5));
    if (u.params.z > 0.5) {
        yuv = vec3<f32>(dot(l.rg, bytes), dot(c.rg, bytes), select(dot(cv.rg, bytes), dot(c.ba, bytes), u.params.y > 0.5));
    }
    var rgb = vec3<f32>(dot(u.r.xyz, yuv) + u.r.w, dot(u.g.xyz, yuv) + u.g.w, dot(u.b.xyz, yuv) + u.b.w);
    rgb = clamp(rgb, vec3<f32>(0.0), vec3<f32>(1.0));
    if (u.hdr.x > 0.5) {
        rgb = sdr(rgb, u.hdr);
    }
    return vec4<f32>(rgb, 1.0);
}
