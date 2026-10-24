// Text drawn with Eric Lengyel's Slug algorithm, ported from the reference
// shaders at github.com/EricLengyel/Slug (MIT, Copyright 2017 Eric Lengyel):
// each fragment works out its coverage from the glyph's quadratic Béziers
// rather than sampling a bitmap. The texture's layout is described in
// internal/painter/slug.go, and gl/shaders/slug.frag is the same shader.
//
// The texture is RGBA8 unorm, read through the group 1 every pipeline shares,
// so switching between this and the other batches keeps every binding.
@group(1) @binding(0) var data: texture_2d<f32>;

const WIDTH: u32 = 1024u;   // SlugTexWidth
const BANDS: u32 = 8u;      // SlugBands
const UNITS: f32 = 65536.0; // SlugUnits + 1
const SOLID: u32 = 0xFFFFFFFFu; // the base of a SlugSolid quad

struct VIn {
    @location(0) pos: vec2<f32>,
    // glyph units, y up
    @location(1) coord: vec2<f32>,
    @location(2) color: vec4<f32>,
    // SlugSolid, -1, for a quad filled whole
    @location(3) base: f32,
}

struct VOut {
    @builtin(position) pos: vec4<f32>,
    @location(0) coord: vec2<f32>,
    @location(1) color: vec4<f32>,
    @location(2) @interpolate(flat) base: u32,
}

@vertex
fn vs_main(v: VIn) -> VOut {
    var out: VOut;
    out.pos = vec4<f32>(v.pos, 0.0, 1.0);
    out.coord = v.coord;
    out.color = v.color;
    out.base = select(u32(v.base), SOLID, v.base < 0.0);
    return out;
}

// pair is texel i's two 16-bit numbers, lo and hi.
fn pair(i: u32) -> vec2<u32> {
    let t = vec4<u32>(round(textureLoad(data, vec2<i32>(i32(i % WIDTH), i32(i / WIDTH)), 0) * 255.0));
    return vec2<u32>(t.x | (t.y << 8u), t.z | (t.w << 8u));
}

fn point(i: u32) -> vec2<f32> {
    return vec2<f32>(pair(i));
}

// root_code says which roots of a sample relative curve change the winding
// number, in bits 0 and 8, from the signs of its control points' y.
fn root_code(y1: f32, y2: f32, y3: f32) -> u32 {
    let i1 = bitcast<u32>(y1) >> 31u;
    let i2 = bitcast<u32>(y2) >> 30u;
    let i3 = bitcast<u32>(y3) >> 29u;
    var shift = (i2 & 2u) | (i1 & ~2u);
    shift = (i3 & 4u) | (shift & ~4u);
    return (0x2E74u >> shift) & 0x0101u;
}

// solve returns the x where the curve crosses y = 0, imaginary roots taken as
// the double root at its extremum. Points are whole glyph units, so a curve
// whose a.y is under half a unit is a line.
fn solve(p1: vec2<f32>, p2: vec2<f32>, p3: vec2<f32>) -> vec2<f32> {
    let a = p1 - p2 * 2.0 + p3;
    let b = p1 - p2;
    let d = sqrt(max(b.y * b.y - a.y * p1.y, 0.0));
    var t1 = (b.y - d) / a.y;
    var t2 = (b.y + d) / a.y;
    if (abs(a.y) < 0.5) {
        t1 = p1.y * 0.5 / b.y;
        t2 = t1;
    }
    return vec2<f32>((a.x * t1 - b.x * 2.0) * t1 + p1.x, (a.x * t2 - b.x * 2.0) * t2 + p1.x);
}

// ray casts from rc towards +x through the band whose header is at hdr,
// returning the coverage and its weight. vert swaps x and y, for the
// vertical bands.
fn ray(hdr: u32, base: u32, rc: vec2<f32>, ppe: f32, vert: bool) -> vec2<f32> {
    let band = pair(base + hdr);
    var cov = 0.0;
    var wgt = 0.0;
    for (var i = 0u; i < band.x; i = i + 1u) {
        let c = base + pair(base + band.y + i).x;
        var p1 = point(c);
        var p2 = point(c + 1u);
        var p3 = point(c + 2u);
        if (vert) {
            p1 = p1.yx;
            p2 = p2.yx;
            p3 = p3.yx;
        }
        p1 = p1 - rc;
        p2 = p2 - rc;
        p3 = p3 - rc;
        // sorted by descending max x, so every curve left is behind the sample
        if (max(max(p1.x, p2.x), p3.x) * ppe < -0.5) {
            break;
        }
        let code = root_code(p1.y, p2.y, p3.y);
        if (code != 0u) {
            let r = solve(p1, p2, p3) * ppe;
            if ((code & 1u) != 0u) {
                cov = cov + clamp(r.x + 0.5, 0.0, 1.0);
                wgt = max(wgt, clamp(1.0 - abs(r.x) * 2.0, 0.0, 1.0));
            }
            if (code > 1u) {
                cov = cov - clamp(r.y + 0.5, 0.0, 1.0);
                wgt = max(wgt, clamp(1.0 - abs(r.y) * 2.0, 0.0, 1.0));
            }
        }
    }
    return vec2<f32>(cov, wgt);
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    let ppe = 1.0 / fwidth(in.coord);
    if (in.base == SOLID) {
        return in.color;
    }
    let idx = vec2<u32>(clamp(floor(in.coord * (f32(BANDS) / UNITS)), vec2<f32>(0.0), vec2<f32>(f32(BANDS - 1u))));
    let h = ray(idx.y, in.base, in.coord, ppe.x, false);
    let v = ray(BANDS + idx.x, in.base, in.coord.yx, ppe.y, true);
    // swapping the axes mirrors the winding, hence -v.x
    let coverage = clamp(max(abs(h.x * h.y - v.x * v.y) / max(h.y + v.y, 1.0 / 65536.0), min(abs(h.x), abs(v.x))), 0.0, 1.0);
    if (coverage <= 0.0) {
        discard;
    }
    return vec4<f32>(in.color.rgb, in.color.a * coverage);
}
