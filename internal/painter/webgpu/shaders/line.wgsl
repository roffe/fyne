// Port of line.vert/frag. Lines are batched, so colour, half width and
// feather arrive per vertex; they are the same across a line's vertices.
struct VIn {
    @location(0) pos: vec2<f32>,
    @location(1) normal: vec2<f32>,
    @location(2) color: vec4<f32>,
    @location(3) style: vec2<f32>, // half width, feather
}

struct VOut {
    @builtin(position) pos: vec4<f32>,
    @location(0) delta: vec2<f32>,
    @location(1) color: vec4<f32>,
    @location(2) style: vec2<f32>,
}

@vertex
fn vs_main(v: VIn) -> VOut {
    var out: VOut;
    out.delta = v.normal * v.style.x;
    out.color = v.color;
    out.style = v.style;
    out.pos = vec4<f32>(v.pos + out.delta, 0.0, 1.0);
    return out;
}

@fragment
fn fs_main(in: VOut) -> @location(0) vec4<f32> {
    let line_width = in.style.x;
    let feather = in.style.y;
    let dist = length(in.delta);

    if (feather == 0.0 || dist <= line_width - feather) {
        return in.color;
    }
    return vec4<f32>(in.color.rgb, mix(in.color.a, 0.0, (dist - (line_width - feather)) / feather));
}
