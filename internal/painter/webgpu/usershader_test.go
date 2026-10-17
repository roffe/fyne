package webgpu

import (
	"image"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

// testShaderWGSL paints the left half of its bounds from a uniform and a
// texture, the right half from another uniform. The texture and sampler sit
// at bindings out of order, and pad pushes green past a 16 byte boundary.
const testShaderWGSL = `
@group(1) @binding(3) var logo: texture_2d<f32>;
@group(1) @binding(1) var smp: sampler;

struct Params {
    red: f32,
    pad: vec2<f32>,
    green: f32,
}
@group(2) @binding(0) var<uniform> params: Params;

@fragment
fn main(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    let uv = (pos.xy - fyne.bounds.xy) / (fyne.bounds.zw - fyne.bounds.xy);
    let blue = textureSample(logo, smp, uv).b;
    if (uv.x < 0.5) {
        return vec4<f32>(params.red, 0.0, blue, 1.0);
    }
    return vec4<f32>(0.0, params.green, 0.0, 1.0);
}
`

func TestReflectUserShader(t *testing.T) {
	l, err := reflectUserShader(shaderUserPrelude + testShaderWGSL)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.textures) != 1 || l.textures["logo"] != 3 {
		t.Errorf("textures = %v, want logo at 3", l.textures)
	}
	if len(l.samplers) != 1 || l.samplers[0] != 1 {
		t.Errorf("samplers = %v, want one at 1", l.samplers)
	}
	// pad is a vec2, aligned to 8 bytes: green lands at 16, not 12.
	if len(l.uniforms) != 2 || l.uniforms["red"] != 0 || l.uniforms["green"] != 16 {
		t.Errorf("uniforms = %v, want red at 0 and green at 16", l.uniforms)
	}

	for _, bad := range []string{
		"@group(3) @binding(0) var t: texture_2d<f32>;",
		"@group(1) @binding(0) var t: texture_2d<u32>;",
		"@group(1) @binding(0) var s: sampler_comparison;",
		"struct P { a: f32 } @group(2) @binding(1) var<uniform> p: P;",
		"struct P { a: array<vec4<f32>, 1100> } @group(2) @binding(0) var<uniform> p: P;",
	} {
		if _, err := reflectUserShader(shaderUserPrelude + bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestPainterDrawsUserShader(t *testing.T) {
	test.NewTempApp(t)
	if _, err := sharedGPU(nil); err != nil {
		t.Skip("no WebGPU adapter:", err)
	}

	blue := image.NewUniform(color.NRGBA{B: 0xff, A: 0xff})
	shader := func(red, green float32, pos fyne.Position) *canvas.Shader {
		s := canvas.NewShader("test", nil, nil)
		s.SourceWGSL = []byte(testShaderWGSL)
		s.Uniforms = map[string]float32{"red": red, "green": green, "unknown": 3}
		s.Textures = map[string]image.Image{"logo": blue}
		s.Resize(fyne.NewSize(40, 20))
		s.Move(pos)
		return s
	}
	// Two draws of one compiled shader in a frame, each with its own uniforms,
	// and one without WGSL that has to be skipped rather than drawn wrong.
	noWGSL := canvas.NewShader("glslOnly", []byte("void main() {}"), nil)
	noWGSL.Resize(fyne.NewSize(10, 10))
	noWGSL.Move(fyne.NewPos(50, 25))
	c := test.NewCanvas()
	c.SetPadded(false)
	c.SetContent(container.NewWithoutLayout(shader(1, 0.5, fyne.NewPos(10, 5)), shader(0, 1, fyne.NewPos(10, 35)), noWGSL))
	c.Resize(fyne.NewSize(60, 60))

	p := NewPainter(c, nil)
	p.Init()
	defer p.Release()
	img := paintCanvas(p, c)

	bg := theme.Color(theme.ColorNameBackground)
	for _, probe := range []struct {
		x, y int
		want color.Color
	}{
		{15, 15, color.NRGBA{R: 0xff, B: 0xff, A: 0xff}}, // red uniform, blue texture
		{45, 15, color.NRGBA{G: 0x80, A: 0xff}},          // green uniform at half
		{15, 45, color.NRGBA{B: 0xff, A: 0xff}},          // the second draw's uniforms
		{45, 45, color.NRGBA{G: 0xff, A: 0xff}},
		{5, 30, bg},  // outside both
		{55, 30, bg}, // where the GLSL-only shader is
	} {
		if d := channelDiff(img.At(probe.x, probe.y), probe.want); d > 2 {
			t.Errorf("pixel %d,%d is %v, want %v", probe.x, probe.y, img.At(probe.x, probe.y), probe.want)
		}
	}
}
