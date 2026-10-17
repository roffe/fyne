// Copies the finished frame to the window surface, one texel per pixel. A
// render pass rather than a texture copy, because the surface format (BGRA,
// usually) differs from the frame's and need not allow copies at all.
@group(1) @binding(0) var tex: texture_2d<f32>;

@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
    // one triangle over the whole target
    let uv = vec2<f32>(f32((i << 1u) & 2u), f32(i & 2u));
    return vec4<f32>(uv * vec2<f32>(2.0, -2.0) + vec2<f32>(-1.0, 1.0), 0.0, 1.0);
}

@fragment
fn fs_main(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    return vec4<f32>(textureLoad(tex, vec2<i32>(pos.xy), 0).rgb, 1.0);
}
