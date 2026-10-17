// Prepended to a canvas.Shader's SourceWGSL, see its documentation. Mirrored
// by userParams in Go.
struct Fyne {
    // the object's x1, y1, x2, y2 in pixels from the top left of the frame
    bounds: vec4<f32>,
    // xy: the size of the output frame in pixels
    frame: vec4<f32>,
    // clip-space x1, y1 (top), x2, y2 (bottom) of the quad rasterised
    quad: vec4<f32>,
}

@group(0) @binding(0) var<uniform> fyne: Fyne;

@vertex
fn fyne_vertex(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
    let x = select(fyne.quad.x, fyne.quad.z, (i & 1u) != 0u);
    let y = select(fyne.quad.y, fyne.quad.w, (i & 2u) != 0u);
    return vec4<f32>(x, y, 0.0, 1.0);
}
