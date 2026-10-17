// Port of regular_polygon.frag; frame.w is the stroke width, params is the
// outer radius, rotation in degrees, number of sides and corner radius.

struct Record {
    shape: Shape,
}

// The signed distance from the point to the regular polygon's edge
fn regular_distance(p: vec2<f32>, r: f32, s: f32) -> f32 {
    let angle = PI / s;
    let angle_cos = cos(angle);
    let angle_sin = sin(angle);
    let angular_offset = glmod(atan2(p.x, p.y), 2.0 * angle) - angle;
    var d = length(p) * vec2<f32>(cos(angular_offset), abs(sin(angular_offset))) - r * vec2<f32>(angle_cos, angle_sin);
    d.y += clamp(-d.y, 0.0, r * angle_sin);
    return length(d) * sign(d.x);
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    let centered = rotate(radians(u.params.y)) * centered_pos(gl_frag_coord(pos));
    let edge = u.frame.z;
    let stroke_width = u.frame.w;
    let corner_radius = u.params.w;

    let dist = regular_distance(centered, u.params.x - corner_radius, floor(u.params.z)) - corner_radius;
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
