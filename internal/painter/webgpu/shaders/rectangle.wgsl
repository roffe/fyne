// Port of rectangle.frag; frame.w is the stroke width.

struct Record {
    shape: Shape,
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    u = records[in.idx].shape;
    let pos = in.pos;
    let fc = gl_frag_coord(pos);
    let frame = u.frame.xy;
    let b = u.bounds;
    let stroke_width = u.frame.w;
    let add_shadow = u.shadow_style.x == 1.0;
    let outside = fc.x < b.x || fc.x > b.z || fc.y < frame.y - b.w || fc.y > frame.y - b.y;
    var color = u.fill_color;

    if (add_shadow) {
        let frag_pos = fc + vec2<f32>(-u.shadow.x, u.shadow.y);
        let center = vec2<f32>((b.x + b.z) * 0.5, frame.y - (b.y + b.w) * 0.5);
        // expand/contract rectangle bounds by spread on all sides
        let half_size = vec2<f32>(b.z - b.x, b.w - b.y) * 0.5 + vec2<f32>(u.shadow.w);

        let d = abs(frag_pos - center) - half_size;
        let blur = u.shadow.z;
        let distance_shadow = sstep(-blur * 0.5, blur * 0.5, length(max(d, vec2<f32>(0.0))) + min(max(d.x, d.y), 0.0));
        var shadow_alpha = u.shadow_color.a * (1.0 - distance_shadow);

        if (u.shadow_style.y == 0.0) {
            // remove shadow inside rectangle (uses original rect, not spread rect)
            let d_h = min(fc.x - b.x, b.z - fc.x);
            let d_v = min(fc.y - frame.y + b.w, frame.y - b.y - fc.y);
            shadow_alpha *= sstep(0.0, -0.5, min(d_h, d_v));
        }

        if (outside) {
            color.a = 0.0;
        }
        color = blend_shadow(color, vec4<f32>(u.shadow_color.rgb, shadow_alpha));
    }

    // discard if outside rectangle coords, necessary to draw thin stroke and mitigate inconsistent borders issue
    if (outside) {
        if (!add_shadow) {
            discard;
        }
    } else if (fc.x >= b.z - stroke_width || fc.x <= b.x + stroke_width ||
        fc.y <= frame.y - b.w + stroke_width || fc.y >= frame.y - b.y - stroke_width) {
        color = u.stroke_color;
    }

    return color;
}
