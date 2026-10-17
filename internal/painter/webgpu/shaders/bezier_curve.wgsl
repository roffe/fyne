// Port of bezier_curve.frag; frame.w is half the stroke width, params the
// start and end point, params2 the control points and radius.x how many of
// them there are (0: linear, 1: quadratic, 2: cubic).

struct Record {
    shape: Shape,
}

const EPS: f32 = 1e-3;

// linear_distance, quadratic_distance and helper functions are adapted from Inigo Quilez's work: https://www.shadertoy.com/view/MlKcDD
// Licensed under the MIT License.
fn linear_distance(p: vec2<f32>, a: vec2<f32>, b: vec2<f32>) -> f32 {
    let pa = p - a;
    let ba = b - a;
    return length(pa - ba * clamp(dot(pa, ba) / dot(ba, ba), 0.0, 1.0));
}

fn dot2(v: vec2<f32>) -> f32 {
    return dot(v, v);
}

fn cos_acos_3(x_in: f32) -> f32 {
    let x = sqrt(0.5 + 0.5 * x_in);
    return x * (x * (x * (x * -0.008972 + 0.039071) - 0.107074) + 0.576975) + 0.5;
}

fn quadratic_distance(pos: vec2<f32>, v0: vec2<f32>, v1: vec2<f32>, v2: vec2<f32>) -> f32 {
    let a = v1 - v0;
    let b = v0 - 2.0 * v1 + v2;
    let c = a * 2.0;
    let d = v0 - pos;

    let kk = 1.0 / dot(b, b);
    let kx = kk * dot(a, b);
    let ky = kk * (2.0 * dot(a, a) + dot(d, b)) / 3.0;
    let kz = kk * dot(d, a);

    let p = ky - kx * kx;
    let q = kx * (2.0 * kx * kx - 3.0 * ky) + kz;
    var h = q * q + 4.0 * p * p * p;

    var res: f32;

    if (h >= 0.0) {
        h = sqrt(h);
        h = select(-h, h, q < 0.0); // copysign
        let x = (h - q) / 2.0;
        let v = sign(x) * pow(abs(x), 1.0 / 3.0);
        var t = v - p / (v + EPS);

        // Newton refinement (stabilizes near cancellation)
        t -= (t * (t * t + 3.0 * p) + q) / (3.0 * t * t + 3.0 * p);

        t = clamp(t - kx, 0.0, 1.0);
        let w = d + (c + b * t) * t;
        res = dot2(w);
    } else {
        let z = sqrt(-p);
        let m = cos_acos_3(q / (p * z * 2.0));
        let n = sqrt(1.0 - m * m) * 1.732050808;

        let t = clamp(vec3<f32>(m + m, -n - m, n - m) * z - kx, vec3<f32>(0.0), vec3<f32>(1.0));
        let qx = d + (c + b * t.x) * t.x;
        let qy = d + (c + b * t.y) * t.y;
        res = min(dot2(qx), dot2(qy));
    }

    return sqrt(res);
}

fn cubic_distance(p: vec2<f32>, v0: vec2<f32>, v1: vec2<f32>, v2: vec2<f32>, v3: vec2<f32>) -> f32 {
    let approx_length = length(v1 - v0) + length(v2 - v1) + length(v3 - v2); // control polygon length
    let steps = i32(clamp(approx_length * 10.0, 16.0, 64.0)); // scale steps by length

    let a = -v0 + 3.0 * v1 - 3.0 * v2 + v3;
    let b = 3.0 * v0 - 6.0 * v1 + 3.0 * v2;
    let c = -3.0 * v0 + 3.0 * v1;
    let d = v0;

    let dt = 1.0 / f32(steps);
    var prev = d;
    var best_seg_dist = 1e3;
    var t0 = 0.0;

    for (var i = 1; i <= steps; i++) {
        let t = f32(i) * dt;
        let cur = ((a * t + b) * t + c) * t + d;

        // closest point on segment [prev, cur]
        let seg = cur - prev;
        let h = clamp(dot(p - prev, seg) / max(dot(seg, seg), EPS), 0.0, 1.0);
        let proj = prev + h * seg;
        let dseg = length(p - proj);

        if (dseg < best_seg_dist) {
            best_seg_dist = dseg;
            // map segment-local h in [0,1] to global t in [0,1]
            t0 = mix(f32(i - 1) * dt, f32(i) * dt, h);
        }

        prev = cur;
    }

    // Newton refinement from t0 on the true cubic
    var t = t0;
    for (var iter = 0; iter < 4; iter++) {
        let dpt = ((3.0 * a * t + 2.0 * b) * t) + c;
        let r = ((a * t + b) * t + c) * t + d - p;
        let f = dot(r, dpt);
        let df = dot(dpt, dpt) + dot(r, 6.0 * a * t + 2.0 * b);

        let stp = f / max(df, EPS);
        // limit step to avoid big jumps; clamp t to [0,1]
        t = clamp(t - clamp(stp, -0.5, 0.5), 0.0, 1.0);
    }

    let pref = ((a * t + b) * t + c) * t + d;
    let dnewton = length(pref - p);

    return min(dnewton, best_seg_dist);
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    // coordinates: (0.0) at rect top-left, +X right, +Y down
    let fc = frag_coord_gl(pos);
    let p = vec2<f32>(fc.x, u.frame.y - fc.y) - u.bounds.xy;
    let start = u.params.xy;
    let end = u.params.zw;

    var dist: f32;
    let count = i32(u.radius.x);
    if (count == 1) {
        dist = quadratic_distance(p, start, u.params2.xy, end);
    } else if (count == 2) {
        dist = cubic_distance(p, start, u.params2.xy, u.params2.zw, end);
    } else {
        dist = linear_distance(p, start, end);
    }

    let edge = u.frame.z;
    let stroke_half = u.frame.w;
    let alpha = 1.0 - sstep(stroke_half - edge, stroke_half + edge, dist);
    return vec4<f32>(u.stroke_color.rgb, u.stroke_color.a * alpha);
}
