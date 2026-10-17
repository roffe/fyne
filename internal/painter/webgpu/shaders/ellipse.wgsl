// Port of ellipse.frag; frame.w is the stroke width, radius.xy the radii and
// params.x the rotation in degrees.

struct Record {
    shape: Shape,
}

fn calc_distance(p: vec2<f32>, r_in: vec2<f32>) -> f32 {
    let eps = 1e-3;
    let r = max(r_in, vec2<f32>(eps));
    let f = p / r;
    return (dot(f, f) - 1.0) / max(length(2.0 * f / r), eps);
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    let centered = rotate(radians(u.params.x)) * centered_pos(frag_coord_gl(pos));
    let edge = u.frame.z;
    let stroke_width = u.frame.w;
    let radius = u.radius.xy;

    let dist = calc_distance(centered, radius);
    var color = u.fill_color;

    if (stroke_width > 0.0) {
        let inner_radius = radius - stroke_width;
        var fill_mask = 0.0;
        if (inner_radius.x > 1.0 && inner_radius.y > 1.0) {
            // create a mask for the fill area (inside, shrunk by stroke width)
            let dist_inner = calc_distance(centered, inner_radius);
            fill_mask = sstep(edge, -edge, dist_inner);
        }

        // combine fill mask and colors (fill + stroke)
        color = mix(u.stroke_color, u.fill_color, fill_mask);
    }

    // smooth edges
    let alpha = sstep(edge, -edge, dist);
    color = vec4<f32>(color.rgb, color.a * alpha);

    if (u.shadow_style.x == 1.0) {
        // use ellipse radii by default, expand/contract by spread
        var shadow_radius = radius;
        if (u.shadow.w != 0.0) {
            shadow_radius = max(radius + u.shadow.w, vec2<f32>(0.0));
        }

        let blur = u.shadow.z;
        shadow_radius = max(shadow_radius - blur * 0.5, vec2<f32>(0.0));

        // flip the shadow offset to get the correct shadow position
        // negative offset-x value places the shadow to the left of the element. Negative offset-y value places the shadow above the element
        let offset = vec2<f32>(-u.shadow.x, u.shadow.y);
        let distance_shadow = calc_distance(centered + offset, shadow_radius);
        var shadow_alpha = u.shadow_color.a * (1.0 - sstep(-edge, blur + edge, distance_shadow));

        if (u.shadow_style.y == 0.0) {
            // remove shadow inside the ellipse
            shadow_alpha *= sstep(-2.0 * edge, 0.0, dist);
        }

        color = blend_shadow(color, vec4<f32>(u.shadow_color.rgb, shadow_alpha));
    }

    return color;
}
