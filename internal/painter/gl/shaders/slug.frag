// Text drawn with Eric Lengyel's Slug algorithm, ported from the reference
// shaders at github.com/EricLengyel/Slug (MIT, Copyright 2017 Eric Lengyel):
// each fragment works out its coverage from the glyph's quadratic Béziers
// rather than sampling a bitmap. The texture's layout is described in
// internal/painter/slug.go, and webgpu/shaders/slug.wgsl is the same shader.
//
// GLSL 1.10 and ES 1.00 have no integers to speak of, so texels are decoded
// and indexed in floats, exact up to 2^24, and the root code's bit table is
// spelled out as comparisons. The #version line, and for ES the extension and
// precision lines, are prepended per build.

uniform sampler2D data;
uniform float rows; // the texture's height

varying vec2 fragCoord; // glyph units, y up
varying vec4 fragColor;
varying float fragBase; // SlugSolid, -1, for a quad filled whole

const float WIDTH = 1024.0; // SlugTexWidth
const float BANDS = 8.0;    // SlugBands
const float UNITS = 65536.0; // SlugUnits + 1
const int MAX_CURVES = 256; // SlugMaxBandCurves

// pair is texel i's two 16-bit numbers, lo and hi.
vec2 pair(float i) {
    vec2 at = vec2(mod(i, WIDTH), floor(i / WIDTH));
    vec4 t = floor(texture2D(data, (at + 0.5) / vec2(WIDTH, rows)) * 255.0 + 0.5);
    return vec2(t.r + t.g * 256.0, t.b + t.a * 256.0);
}

// solve returns the x where the curve crosses y = 0, imaginary roots taken as
// the double root at its extremum. Points are whole glyph units, so a curve
// whose a.y is under half a unit is a line.
vec2 solve(vec2 p1, vec2 p2, vec2 p3) {
    vec2 a = p1 - p2 * 2.0 + p3;
    vec2 b = p1 - p2;
    float d = sqrt(max(b.y * b.y - a.y * p1.y, 0.0));
    float t1 = (b.y - d) / a.y;
    float t2 = (b.y + d) / a.y;
    if (abs(a.y) < 0.5) {
        t1 = p1.y * 0.5 / b.y;
        t2 = t1;
    }
    return vec2((a.x * t1 - b.x * 2.0) * t1 + p1.x, (a.x * t2 - b.x * 2.0) * t2 + p1.x);
}

// ray casts from rc towards +x through the band whose header is at hdr,
// returning the coverage and its weight. vert swaps x and y, for the
// vertical bands.
vec2 ray(float hdr, float base, vec2 rc, float ppe, bool vert) {
    vec2 band = pair(base + hdr);
    float cov = 0.0;
    float wgt = 0.0;
    for (int i = 0; i < MAX_CURVES; i++) {
        if (float(i) >= band.x)
            break;
        float c = base + pair(base + band.y + float(i)).x;
        vec2 p1 = pair(c);
        vec2 p2 = pair(c + 1.0);
        vec2 p3 = pair(c + 2.0);
        if (vert) {
            p1 = p1.yx;
            p2 = p2.yx;
            p3 = p3.yx;
        }
        p1 -= rc;
        p2 -= rc;
        p3 -= rc;
        // sorted by descending max x, so every curve left is behind the sample
        if (max(max(p1.x, p2.x), p3.x) * ppe < -0.5)
            break;

        // The reference's (0x2E74 >> shift) & 0x0101, shift being the signs
        // of the three y as bits 0 to 2.
        float shift = (p1.y < 0.0 ? 1.0 : 0.0) + (p2.y < 0.0 ? 2.0 : 0.0) + (p3.y < 0.0 ? 4.0 : 0.0);
        bool root1 = shift == 2.0 || shift == 4.0 || shift == 5.0 || shift == 6.0;
        bool root2 = shift == 1.0 || shift == 2.0 || shift == 3.0 || shift == 5.0;
        if (root1 || root2) {
            vec2 r = solve(p1, p2, p3) * ppe;
            if (root1) {
                cov += clamp(r.x + 0.5, 0.0, 1.0);
                wgt = max(wgt, clamp(1.0 - abs(r.x) * 2.0, 0.0, 1.0));
            }
            if (root2) {
                cov -= clamp(r.y + 0.5, 0.0, 1.0);
                wgt = max(wgt, clamp(1.0 - abs(r.y) * 2.0, 0.0, 1.0));
            }
        }
    }
    return vec2(cov, wgt);
}

void main() {
    vec2 ppe = 1.0 / fwidth(fragCoord);
    if (fragBase < 0.0) {
        gl_FragColor = fragColor;
        return;
    }
    // constant over the quad, rounded in case interpolation strays
    float base = floor(fragBase + 0.5);
    vec2 idx = clamp(floor(fragCoord * (BANDS / UNITS)), 0.0, BANDS - 1.0);
    vec2 h = ray(idx.y, base, fragCoord, ppe.x, false);
    vec2 v = ray(BANDS + idx.x, base, fragCoord.yx, ppe.y, true);
    // swapping the axes mirrors the winding, hence -v.x
    float coverage = clamp(max(abs(h.x * h.y - v.x * v.y) / max(h.y + v.y, 1.0 / 65536.0), min(abs(h.x), abs(v.x))), 0.0, 1.0);
    if (coverage <= 0.0)
        discard;

    // Straight alpha, for the blend function the shape shaders use.
    gl_FragColor = vec4(fragColor.rgb, fragColor.a * coverage);
}
