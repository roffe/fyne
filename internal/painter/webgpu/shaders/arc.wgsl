// Port of arc.frag; frame.w is the stroke width, params the inner and outer
// radius and the start and end angle in degrees, radius.x the corner radius.
//
// Note: This shader operates in the unit circle coordinate system, where angles are measured from the positive X axis.

struct Record {
    shape: Shape,
}

// Computes the signed distance for a rounded arc shape
// Negative values are inside the arc, positive values are outside, and zero is on the edge
fn sd_rounded_arc(p_in: vec2<f32>, r1: f32, r2: f32, a0: f32, a1: f32, cr: f32) -> f32 {
    // center the arc for simpler calculations
    let mid_angle = (a0 + a1) / 2.0;
    let arc_span = abs(a1 - a0);

    let cs = cos(mid_angle);
    let sn = sin(mid_angle);
    let p = mat2x2<f32>(cs, -sn, sn, cs) * p_in;

    // calculate distance to a rounded box in a pseudo-polar space
    let r = length(p);

    // atan(y, x) for standard angle convention (0 degrees = right)
    let a = atan2(p.y, p.x);

    let box_half_size = vec2<f32>(arc_span * 0.5 * r, (r2 - r1) * 0.5);
    let q = vec2<f32>(a * r, r - (r1 + r2) * 0.5);

    // the inner corner radius is clamped to half of the smaller dimension:
    //   thickness (r2 - r1), to prevent inner/outer corners on the same side from overlapping
    //   inner length (arc_span * r1), to prevent the start/end inner corners from overlapping
    let inner_cr = min(cr, 0.5 * min(r2 - r1, arc_span * r1));
    // the outer corner radius is just cr
    let outer_cr = cr;

    // interpolate between inner and outer corner radius based on the radial position
    // 't' goes from 0 (inner) to 1 (outer).
    let t = sstep(-box_half_size.y, box_half_size.y, q.y);
    let effective_cr = mix(inner_cr, outer_cr, t);

    // use the standard SDF for a 2D rounded box with the effective radius
    let d = abs(q) - box_half_size + effective_cr;
    return length(max(d, vec2<f32>(0.0))) + min(max(d.x, d.y), 0.0) - effective_cr;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    let centered = centered_pos(frag_coord_gl(pos));
    let edge = u.frame.z;
    let stroke_width = u.frame.w;
    let inner_radius = u.params.x;
    let outer_radius = u.params.y;
    let start_rad = radians(u.params.z);
    let end_rad = radians(u.params.w);

    // check if the arc is a full circle (360 degrees or more)
    // the sd_rounded_arc function creates segment at the start/end angle, which is undesirable for a complete circle
    var dist: f32;
    if (abs(end_rad - start_rad) >= 2.0 * PI - 0.001) {
        // full circle
        let r = length(centered);

        if (inner_radius < 0.5) {
            // no inner radius
            dist = r - outer_radius;
        } else {
            let ring_center_radius = (inner_radius + outer_radius) * 0.5;
            let ring_thickness = (outer_radius - inner_radius) * 0.5;
            dist = abs(r - ring_center_radius) - ring_thickness;
        }
    } else {
        dist = sd_rounded_arc(centered, inner_radius, outer_radius, start_rad, end_rad, u.radius.x);
    }

    var color = u.fill_color;

    if (stroke_width > 0.0) {
        // create a mask for the fill area (inside, shrunk by stroke width)
        let fill_mask = sstep(edge, -edge, dist + stroke_width);

        // combine fill mask and colors (fill + stroke)
        color = mix(u.stroke_color, u.fill_color, fill_mask);
    }

    // smooth edges
    let alpha = sstep(edge, -edge, dist);
    return vec4<f32>(color.rgb, color.a * alpha);
}
