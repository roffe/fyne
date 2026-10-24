//go:build ((gles || arm || arm64) && !android && !ios && !mobile && !darwin && !wasm && !test_web_driver) || ((android || ios || mobile) && (!wasm || !test_web_driver)) || wasm || test_web_driver

package gl

import (
	_ "embed"

	"fyne.io/fyne/v2/canvas"
)

var (
	//go:embed shaders/arbitrary_polygon_es.frag
	shaderFragArbitraryPolygon []byte
	//go:embed shaders/arc_es.frag
	shaderFragArc []byte
	//go:embed shaders/bezier_curve_es.frag
	shaderFragBezierCurve []byte
	//go:embed shaders/blur_es.frag
	shaderFragBlur []byte
	//go:embed shaders/ellipse_es.frag
	shaderFragEllipse []byte
	//go:embed shaders/glyph_es.frag
	shaderFragGlyph []byte
	//go:embed shaders/line_es.frag
	shaderFragLine []byte
	//go:embed shaders/regular_polygon_es.frag
	shaderFragPolygon []byte
	//go:embed shaders/rectangle_es.frag
	shaderFragRectangle []byte
	//go:embed shaders/round_rectangle_es.frag
	shaderFragRoundRectangle []byte
	//go:embed shaders/simple_es.frag
	shaderFragSimple []byte
	//go:embed shaders/video_es.frag
	shaderFragVideo []byte

	//go:embed shaders/glyph_es.vert
	shaderVertGlyph []byte
	//go:embed shaders/line_es.vert
	shaderVertLine []byte
	//go:embed shaders/passthrough_2d_es.vert
	shaderVertPassthrough2D []byte
	//go:embed shaders/textured_passthrough_2d_es.vert
	shaderVertTexturedPassthrough2D []byte
)

var (
	//go:embed shaders/slug.frag
	slugFrag string
	//go:embed shaders/slug.vert
	slugVert string

	// A device without highp fragments or derivatives fails to compile this,
	// which sends its text to the atlas.
	shaderFragSlug = []byte(`#version 100
#extension GL_OES_standard_derivatives : enable
#ifndef GL_FRAGMENT_PRECISION_HIGH
#error Slug needs highp floats
#endif
precision highp float;
precision highp sampler2D;
` + slugFrag)
	shaderVertSlug = []byte("#version 100\n" + slugVert)
)

// userShaderFragment returns the fragment shader source to use for the given
// shader object on this build target.
func userShaderFragment(s *canvas.Shader) []byte {
	return s.SourceES
}
