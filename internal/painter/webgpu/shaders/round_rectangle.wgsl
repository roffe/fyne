// Port of round_rectangle.frag; frame.w is half the stroke width, params.xy
// half the rectangle size and radius the corners: top right, bottom right,
// top left, bottom left.

struct Record {
    shape: Shape,
}

// distance is calculated for a single quadrant
// returns invalid output if corner radius exceed half of the shorter edge
fn calc_distance(p: vec2<f32>, b: vec2<f32>, r: vec4<f32>) -> f32 {
    let rxy = select(r.zw, r.xy, p.x > 0.0);
    let rx = select(rxy.y, rxy.x, p.y > 0.0);

    let d = abs(p) - b + rx;
    return min(max(d.x, d.y), 0.0) + length(max(d, vec2<f32>(0.0))) - rx;
}

// distance is calculated for all necessary quadrants
// corner radius may exceed half of the shorter edge
fn calc_distance_all_quadrants(p: vec2<f32>, size: vec2<f32>, radius: vec4<f32>) -> f32 {
    let d = abs(p) - size;
    var dist = length(max(d, vec2<f32>(0.0))) + min(max(d.x, d.y), 0.0);

    // top-left corner
    let p_tl = p - vec2<f32>(radius.z - size.x, size.y - radius.z);
    if (p_tl.x < 0.0 && p_tl.y > 0.0) {
        dist = max(dist, length(p_tl) - radius.z);
    }

    // top-right corner
    let p_tr = p - vec2<f32>(size.x - radius.x, size.y - radius.x);
    if (p_tr.x > 0.0 && p_tr.y > 0.0) {
        dist = max(dist, length(p_tr) - radius.x);
    }

    // bottom-right corner
    let p_br = p - vec2<f32>(size.x - radius.y, radius.y - size.y);
    if (p_br.x > 0.0 && p_br.y < 0.0) {
        dist = max(dist, length(p_br) - radius.y);
    }

    // bottom-left corner
    let p_bl = p - vec2<f32>(radius.w - size.x, radius.w - size.y);
    if (p_bl.x < 0.0 && p_bl.y < 0.0) {
        dist = max(dist, length(p_bl) - radius.w);
    }

    return dist;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    let centered = centered_pos(frag_coord_gl(pos));
    let edge = u.frame.z;
    let stroke_half = u.frame.w;
    let size_half = u.params.xy;
    let radius = u.radius;

    let max_radius = max(max(radius.x, radius.y), max(radius.z, radius.w));
    var color = u.fill_color;
    var dist: f32;
    var alpha: f32;

    // subtract a small threshold value to avoid calling calc_distance_all_quadrants when the largest corner radius is very close to half the length of the rectangle's shortest edge
    let all_quadrants = max_radius - 0.9 > min(size_half.x, size_half.y) + stroke_half;
    if (all_quadrants) {
        // at least one corner radius is larger than half of the shorter edge
        dist = calc_distance_all_quadrants(centered, size_half + stroke_half, radius);
        alpha = 1.0 - sstep(-edge, edge, dist);

        if (stroke_half > 0.0) {
            let color_blend = 1.0 - sstep(stroke_half * 2.0 - edge, stroke_half * 2.0 + edge, abs(dist));
            color = mix(u.fill_color, u.stroke_color, color_blend);
        }
    } else {
        dist = calc_distance(centered, size_half, radius - stroke_half);
        alpha = 1.0 - sstep(stroke_half - edge, stroke_half + edge, dist);

        if (stroke_half > 0.0) {
            let color_blend = sstep(-stroke_half - edge, -stroke_half + edge, dist);
            color = mix(u.fill_color, u.stroke_color, color_blend);
        }
    }

    color = vec4<f32>(color.rgb, color.a * alpha);

    if (u.shadow_style.x == 1.0) {
        // use rectangle size by default
        var shadow_size = size_half + stroke_half;
        var shadow_radius = radius;

        let spread = u.shadow.w;
        if (spread != 0.0) {
            // expand/contract by spread, adjust radii to match
            let original_size = shadow_size;
            shadow_size = max(original_size + spread, vec2<f32>(0.0));
            let ratio_x = select(1.0, shadow_size.x / original_size.x, original_size.x > 0.0);
            let ratio_y = select(1.0, shadow_size.y / original_size.y, original_size.y > 0.0);
            // scale all corner radii proportionally, use minimum ratio so radius never exceeds the shorter adjacent edge
            shadow_radius = max(radius * min(ratio_x, ratio_y), vec4<f32>(0.0));
        }

        let blur = u.shadow.z;
        let blur_inset = blur * 0.5;
        shadow_size = max(shadow_size - blur_inset, vec2<f32>(0.0));
        shadow_radius = max(shadow_radius - blur_inset, vec4<f32>(0.0));

        // flip the shadow offset to get the correct shadow position
        // negative offset-x value places the shadow to the left of the element. Negative offset-y value places the shadow above the element
        let offset = vec2<f32>(-u.shadow.x, u.shadow.y);
        var distance_shadow: f32;
        if (all_quadrants) {
            distance_shadow = calc_distance_all_quadrants(centered + offset, shadow_size, shadow_radius);
        } else {
            distance_shadow = calc_distance(centered + offset, shadow_size, shadow_radius);
        }
        var shadow_alpha = u.shadow_color.a * (1.0 - sstep(-edge, blur + edge, distance_shadow));

        if (u.shadow_style.y == 0.0) {
            // remove shadow inside rectangle
            var d_shape = dist; // reuse the previously computed outer distance
            if (!all_quadrants) {
                d_shape = calc_distance(centered, size_half + stroke_half, radius);
            }
            shadow_alpha *= sstep(-2.0 * edge, 0.0, d_shape);
        }

        color = blend_shadow(color, vec4<f32>(u.shadow_color.rgb, shadow_alpha));
    }

    return color;
}
