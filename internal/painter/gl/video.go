package gl

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal/cache"
	paint "fyne.io/fyne/v2/internal/painter"
)

// GL_LUMINANCE and GL_LUMINANCE_ALPHA: the one and two channel formats of
// GL 2.1 and ES 2.0, which not every binding names.
const (
	luminance      = 0x1909
	luminanceAlpha = 0x190A
)

// glVideo is a VideoFrame's planes as textures: luma, then U and V, or the
// interleaved chroma as luminance and alpha. Wide samples take two channels,
// low byte first, as luminance and alpha, or red and green then blue and
// alpha. ES 2.0 cannot upload rows with a stride, so each texture is as wide
// as its plane's rows are long.
type glVideo struct {
	planes            [3]Texture
	widths            [3]int // of the textures, in texels
	width, height     int
	interleaved, wide bool
}

func (p *painter) drawVideo(f *canvas.VideoFrame, pos fyne.Position, frame fyne.Size) {
	prog := p.videoProgram()
	v := p.videoTextures(f)
	if prog == nil || v == nil {
		return
	}
	points, _, _ := p.rectCoords(f.Size(), pos, frame, f.FillMode, paint.VideoAspect(f), 0)
	p.useProgram(prog.ref)
	p.updateBuffer(prog.buff, points[:])
	p.UpdateVertexArray(*prog, attrVertex, coordinateSize2D, coordinateSize2DWithTexture, 0)
	p.UpdateVertexArray(*prog, attrVertexTextureCoordinates, coordinateSize2D, coordinateSize2DWithTexture, coordinateSize2D)

	interleaved, wide := float32(0), float32(0)
	if v.interleaved {
		interleaved = 1
	}
	if v.wide {
		wide = 1
	}
	cw := float32((f.Width + 1) / 2)
	m := paint.VideoMatrix(f)
	hdr := paint.VideoHDR(f)
	p.SetUniform1f(*prog, "turns", float32(paint.VideoTurns(f)))
	p.SetUniform1f(*prog, "interleaved", interleaved)
	p.SetUniform1f(*prog, "wide", wide)
	p.SetUniform4f(*prog, "hdr", hdr[0], hdr[1], hdr[2], hdr[3])
	p.SetUniform2f(*prog, "lumaFit", float32(f.Width)/float32(v.widths[0]), (float32(f.Width)-0.5)/float32(v.widths[0]))
	p.SetUniform2f(*prog, "chromaFit", cw/float32(v.widths[1]), (cw-0.5)/float32(v.widths[1]))
	p.SetUniform4f(*prog, "rowR", m[0][0], m[0][1], m[0][2], m[0][3])
	p.SetUniform4f(*prog, "rowG", m[1][0], m[1][1], m[1][2], m[1][3])
	p.SetUniform4f(*prog, "rowB", m[2][0], m[2][1], m[2][2], m[2][3])
	for i, name := range []string{"luma", "chroma", "chromaV"} {
		tex := v.planes[i]
		if v.interleaved && i == 2 {
			tex = v.planes[1]
		}
		p.ctx.ActiveTexture(texture0 + uint32(i))
		p.ctx.BindTexture(texture2D, tex)
		p.SetUniform1i(*prog, name, int32(i))
	}
	p.ctx.ActiveTexture(texture0) // the unit every other draw binds to
	p.ctx.BlendFunc(one, oneMinusSrcAlpha)
	p.ctx.DrawArrays(triangleStrip, 0, vertexCountRectangle)
	p.logError()
}

// videoProgram returns the program video frames draw with, compiled on first
// use as few windows show any, or nil if it does not compile.
func (p *painter) videoProgram() *programState {
	if p.video == nil && !p.videoFailed {
		ref, err := p.createProgram(shaderVertTexturedPassthrough2D, shaderFragVideo)
		if err != nil {
			fyne.LogError("GL cannot draw video", err)
			p.videoFailed = true
			return nil
		}
		p.video = &programState{
			ref:        ref,
			buff:       p.createBuffer(coordinatesSizeRectangleWithTexture),
			uniforms:   make(map[string]*uniformState),
			attributes: make(map[string]Attribute),
		}
	}
	return p.video
}

// videoTextures returns f's planes as textures, uploading them when they are
// new or f was refreshed since, or nil if f has no picture to draw.
func (p *painter) videoTextures(f *canvas.VideoFrame) *glVideo {
	if !paint.VideoPlanes(f) {
		return nil
	}
	interleaved, wide := f.U == nil, f.Depth > 8
	_, ok := cache.GetTexture(f)
	v := p.videos[f]
	if ok && v != nil && v.width == f.Width && v.height == f.Height && v.interleaved == interleaved && v.wide == wide {
		if !cache.TakeTextureDirty(f).Empty() {
			p.uploadVideo(v, f)
		}
		return v
	}
	p.freeTexture(f)

	v = &glVideo{width: f.Width, height: f.Height, interleaved: interleaved, wide: wide}
	planes := 3
	if interleaved {
		planes = 2
	}
	for i := range planes {
		v.planes[i] = p.newTexture(canvas.ImageScaleSmooth)
	}
	p.uploadVideo(v, f)
	if p.videos == nil {
		p.videos = map[*canvas.VideoFrame]*glVideo{}
	}
	p.videos[f] = v
	cache.SetTexture(f, cache.TextureType(v.planes[0]), p.canvas)
	return v
}

func (p *painter) uploadVideo(v *glVideo, f *canvas.VideoFrame) {
	ch := (f.Height + 1) / 2
	one, two, bpp := uint32(luminance), uint32(luminanceAlpha), 1
	if v.wide {
		one, two, bpp = luminanceAlpha, colorFormatRGBA, 2
	}
	p.uploadPlane(v, 0, f.Y, f.YStride, f.Height, one, bpp)
	if v.interleaved {
		p.uploadPlane(v, 1, f.UV, f.CStride, ch, two, 2*bpp)
		return
	}
	p.uploadPlane(v, 1, f.U, f.CStride, ch, one, bpp)
	p.uploadPlane(v, 2, f.V, f.CStride, ch, one, bpp)
}

// uploadPlane puts rows of a plane, stride bytes apart, in its texture. GL
// reads whole rows four byte aligned, so others go through p.pixels padded.
func (p *painter) uploadPlane(v *glVideo, i int, data []byte, stride, rows int, format uint32, bpp int) {
	if stride%4 != 0 || len(data) < stride*rows {
		padded := (stride + 3) &^ 3
		if cap(p.pixels) < padded*rows {
			p.pixels = make([]uint8, padded*rows)
		}
		buf := p.pixels[:padded*rows]
		for y := range rows {
			copy(buf[y*padded:y*padded+stride], data[y*stride:min(len(data), (y+1)*stride)])
		}
		data, stride = buf, padded
	}
	w := stride / bpp
	p.ctx.ActiveTexture(texture0)
	p.ctx.BindTexture(texture2D, v.planes[i])
	if v.widths[i] == w {
		p.ctx.TexSubImage2D(texture2D, 0, 0, 0, w, rows, format, unsignedByte, data[:stride*rows])
	} else {
		p.ctx.TexImage2D(texture2D, 0, w, rows, format, unsignedByte, data[:stride*rows])
		v.widths[i] = w
	}
	p.logError()
}

// freeVideo deletes the chroma textures of obj if it is a video frame, leaving
// luma, the one in the cache, to freeTexture.
func (p *painter) freeVideo(obj fyne.CanvasObject) {
	f, _ := obj.(*canvas.VideoFrame)
	v := p.videos[f]
	if v == nil {
		return
	}
	delete(p.videos, f)
	chroma := v.planes[1:]
	if v.interleaved {
		chroma = chroma[:1]
	}
	for _, t := range chroma {
		p.ctx.DeleteTexture(t)
	}
}
