package webgpu

import _ "embed"

var (
	//go:embed shaders/arbitrary_polygon.wgsl
	shaderArbitraryPolygon string
	//go:embed shaders/arc.wgsl
	shaderArc string
	//go:embed shaders/bezier_curve.wgsl
	shaderBezierCurve string
	//go:embed shaders/blit.wgsl
	shaderBlit string
	//go:embed shaders/blur.wgsl
	shaderBlur string
	//go:embed shaders/common.wgsl
	shaderCommon string
	//go:embed shaders/ellipse.wgsl
	shaderEllipse string
	//go:embed shaders/glyph.wgsl
	shaderGlyph string
	//go:embed shaders/line.wgsl
	shaderLine string
	//go:embed shaders/rectangle.wgsl
	shaderRectangle string
	//go:embed shaders/regular_polygon.wgsl
	shaderRegularPolygon string
	//go:embed shaders/round_rectangle.wgsl
	shaderRoundRectangle string
	//go:embed shaders/texture.wgsl
	shaderTexture string
	//go:embed shaders/user_prelude.wgsl
	shaderUserPrelude string
	//go:embed shaders/video.wgsl
	shaderVideo string
)
