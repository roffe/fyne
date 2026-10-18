// Port of arbitrary_polygon.frag; frame.w is the stroke width and params.x
// the number of points. Mirrored by polyParams in Go.
struct Record {
    shape: Shape,
    // the points two to a vec4, then their corner radii four to a vec4
    data: array<vec4<f32>, 12>,
}

const MAX_VERTICES: i32 = 16;
const INF: f32 = 1e10;
const EPS: f32 = 1e-3;

// rec is the fragment's record, set with u. Its points are read from there:
// a private array would be simpler, but naga (v0.19) writes one to HLSL as
// "float4[12] data", which FXC rejects.
var<private> rec: u32;

fn vertex(i: i32) -> vec2<f32> {
    let v = records[rec].data[i / 2];
    return select(v.xy, v.zw, (i & 1) == 1);
}

fn corner_radius(i: i32) -> f32 {
    return records[rec].data[8 + i / 4][i % 4];
}

// Signed distance to an arbitrary polygon with per-corner rounding.
// Based on Inigo Quilez's sdPolygon (MIT License) and the per-radius rounding technique.
fn arbitrary_polygon_distance(p: vec2<f32>, num: i32) -> f32 {
    if (num < 3) {
        return 1.0;
    }

    // Phase 1: Compute rounded corner geometry
    // For each corner, compute the inscribed arc center and the tangent points
    // on the adjacent edges. These define the straight segments between corners.
    var start_pts: array<vec2<f32>, 16>;
    var end_pts: array<vec2<f32>, 16>;
    var arc_dist = INF;
    var sign_val = 1.0;

    for (var k = 0; k < MAX_VERTICES; k++) {
        if (k >= num) {
            break;
        }

        var i = k - 2;
        if (i < 0) {
            i += num;
        }
        var j = k - 1;
        if (j < 0) {
            j += num;
        }

        let point1 = vertex(i);
        let point2 = vertex(j);
        let point3 = vertex(k);
        let radius = corner_radius(j);

        let pos = p - point2;
        let a = normalize(point1 - point2);
        let b = normalize(point3 - point2);
        let cross_ab = abs(a.x * b.y - a.y * b.x);

        // Avoid degenerate case where edges are parallel
        if (cross_ab < EPS) {
            start_pts[k] = point2;
            end_pts[k] = point2;
            continue;
        }

        let center = (a + b) * radius / cross_ab;
        let pos_shifted = pos - center;

        // Even-odd rule for arc region
        var c = radius * radius - pos_shifted.y * pos_shifted.y;
        if (c > 0.0) {
            c = sqrt(c);
            let p1 = vec2<f32>(-c, pos_shifted.y);
            let p2_arc = vec2<f32>(c, pos_shifted.y);
            let s1 = dot(p1, a);
            let s2 = dot(p1, b);
            let s3 = dot(p2_arc, a);
            let s4 = dot(p2_arc, b);
            if (pos_shifted.x < p1.x && s1 < 0.0 && s2 < 0.0) {
                sign_val = -sign_val;
            }
            if (pos_shifted.x < p2_arc.x && s4 < 0.0 && s3 < 0.0) {
                sign_val = -sign_val;
            }
        }

        // Distance to arc at this corner
        let s1_dot = dot(pos_shifted, a);
        let s2_dot = dot(pos_shifted, b);
        if (s1_dot < 0.0 && s2_dot < 0.0) {
            arc_dist = min(arc_dist, abs(length(pos_shifted) - radius));
        }

        // Tangent points on the edges
        start_pts[k] = point2 + a * dot(center, a);
        end_pts[k] = point2 + b * dot(center, b);
    }

    // Phase 2: Distance to straight edge segments between tangent points
    var edge_dist = INF;
    for (var j2 = 0; j2 < MAX_VERTICES; j2++) {
        if (j2 >= num) {
            break;
        }

        var i2 = j2 - 1;
        if (i2 < 0) {
            i2 += num;
        }

        let seg_start = end_pts[i2];
        let seg_end = start_pts[j2];
        let e = seg_end - seg_start;
        let w = p - seg_start;
        let h = clamp(dot(w, e) / dot(e, e), 0.0, 1.0);
        let d = w - e * h;
        edge_dist = min(edge_dist, dot(d, d));

        // Even-odd rule for edge segments
        if ((w.y > 0.0) != (w.y > e.y)) {
            if ((e.y * w.x < e.x * w.y) != (e.y < 0.0)) {
                sign_val = -sign_val;
            }
        }
    }

    return min(arc_dist, sqrt(edge_dist)) * sign_val;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    rec = in.idx;
    let pos = in.pos;
    // coordinates: (0.0) at rect top-left, +X right, +Y down
    let fc = frag_coord_gl(pos);
    let p = vec2<f32>(fc.x, u.frame.y - fc.y) - u.bounds.xy;
    let edge = u.frame.z;
    let stroke_width = u.frame.w;

    let dist = arbitrary_polygon_distance(p, i32(u.params.x));
    var color = u.fill_color;

    if (stroke_width > 0.0) {
        // create a mask for the fill area (inside, shrunk by stroke width)
        let fill_mask = sstep(-stroke_width + edge, -stroke_width - edge, dist);

        // combine fill mask and colors (fill + stroke)
        color = mix(u.stroke_color, u.fill_color, fill_mask);
    }

    // smooth edges
    let alpha = sstep(edge, -edge, dist);
    return vec4<f32>(color.rgb, color.a * alpha);
}
