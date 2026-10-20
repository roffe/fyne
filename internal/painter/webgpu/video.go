package webgpu

import (
	"unsafe"

	wgpu "github.com/roffe/gowgpu"
	"github.com/roffe/gowgpu/gputypes"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal/cache"
	paint "fyne.io/fyne/v2/internal/painter"
)

// videoParams mirrors Video in shaders/video.wgsl.
type videoParams struct {
	Quad, Inset, Params, R, G, B, HDR [4]float32
}

// gpuVideo is a VideoFrame's planes on the GPU: luma, then U and V, or the
// interleaved chroma twice, and the group binding them. Wide samples take two
// channels, low byte first, as the shader puts them back together.
type gpuVideo struct {
	planes            [3]*gpuTexture
	group             *wgpu.BindGroup
	width, height     int
	interleaved, wide bool
}

func (v *gpuVideo) release() {
	if v.group != nil {
		v.group.Release()
	}
	for i, t := range v.planes {
		if t != nil && (i == 0 || t != v.planes[i-1]) {
			t.release()
		}
	}
}

func (p *Painter) drawVideo(f *canvas.VideoFrame, pos fyne.Position, frame fyne.Size) {
	pipe := p.gpu.videoPipeline() // first, as it makes the layout the planes bind with
	if pipe == nil {
		return
	}
	v := p.videoTextures(f)
	if v == nil {
		return
	}
	quad, inset, _ := p.rectCoords(f.Size(), pos, frame, f.FillMode, paint.VideoAspect(f), 0)
	m := paint.VideoMatrix(f)
	u := videoParams{Quad: quad, Inset: inset, R: m[0], G: m[1], B: m[2], HDR: paint.VideoHDR(f)}
	u.Params[0] = float32(paint.VideoTurns(f))
	if v.interleaved {
		u.Params[1] = 1
	}
	if v.wide {
		u.Params[2] = 1
	}

	p.flushBatch() // queued quads are underneath
	offset := record(p, &u)
	pass := p.renderPass()
	if pass == nil {
		return
	}
	p.applyScissor(pass)
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, p.uniformGroup, p.dynamicOffset(offset))
	pass.SetBindGroup(1, v.group, nil)
	pass.Draw(gputypes.DrawArgs{VertexCount: 4, InstanceCount: 1})
	// The pipeline's layout is its own, and on DX12 changing layouts drops
	// every binding, so the next built-in draw binds all it needs again.
	p.pipe, p.bound, p.uniformsBound = pipe, nil, false
}

// videoTextures returns f's planes on the GPU, uploading them when they are
// new or f was refreshed since, or nil if f has no picture to draw.
func (p *Painter) videoTextures(f *canvas.VideoFrame) *gpuVideo {
	if !paint.VideoPlanes(f) {
		return nil
	}
	interleaved, wide := f.U == nil, f.Depth > 8
	id, ok := cache.GetTexture(f)
	v := p.videos[id]
	if ok && v != nil && v.width == f.Width && v.height == f.Height && v.interleaved == interleaved && v.wide == wide {
		if !cache.TakeTextureDirty(f).Empty() {
			p.uploadVideo(v, f)
		}
		return v
	}
	p.Free(f)

	if v = p.newVideo(f.Width, f.Height, interleaved, wide); v == nil {
		return nil
	}
	nextTexture++
	id = textureID(nextTexture)
	p.videos[id] = v
	cache.SetTexture(f, id, p.canvas)
	p.uploadVideo(v, f)
	return v
}

func (p *Painter) newVideo(w, h int, interleaved, wide bool) *gpuVideo {
	cw, ch := (w+1)/2, (h+1)/2
	v := &gpuVideo{width: w, height: h, interleaved: interleaved, wide: wide}
	one, two := gputypes.TextureFormatR8Unorm, gputypes.TextureFormatRG8Unorm
	if wide {
		one, two = two, gputypes.TextureFormatRGBA8Unorm
	}
	var err error
	if v.planes[0], err = p.gpu.newPlane(w, h, one); err != nil {
		logError("video plane allocation", err)
		return nil
	}
	if interleaved {
		v.planes[1], err = p.gpu.newPlane(cw, ch, two)
		v.planes[2] = v.planes[1]
	} else if v.planes[1], err = p.gpu.newPlane(cw, ch, one); err == nil {
		v.planes[2], err = p.gpu.newPlane(cw, ch, one)
	}
	if err == nil {
		v.group, err = p.gpu.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
			Layout: p.gpu.videoLayout,
			Entries: []wgpu.BindGroupEntry{
				{Binding: 0, TextureView: v.planes[0].view},
				{Binding: 1, TextureView: v.planes[1].view},
				{Binding: 2, TextureView: v.planes[2].view},
				{Binding: 3, Sampler: p.gpu.linear},
			},
		})
	}
	if err != nil {
		logError("video plane allocation", err)
		v.release()
		return nil
	}
	return v
}

func (p *Painter) uploadVideo(v *gpuVideo, f *canvas.VideoFrame) {
	cw, ch := (f.Width+1)/2, (f.Height+1)/2
	p.gpu.write(v.planes[0], f.Y, f.YStride, 0, 0, f.Width, f.Height)
	if v.interleaved {
		p.gpu.write(v.planes[1], f.UV, f.CStride, 0, 0, cw, ch)
		return
	}
	p.gpu.write(v.planes[1], f.U, f.CStride, 0, 0, cw, ch)
	p.gpu.write(v.planes[2], f.V, f.CStride, 0, 0, cw, ch)
}

// newPlane makes a texture for one plane of a video frame, which binds as part
// of the frame's group rather than one of its own.
func (g *gpu) newPlane(w, h int, format gputypes.TextureFormat) (*gpuTexture, error) {
	tex, err := g.device.CreateTexture(&wgpu.TextureDescriptor{
		Size:          wgpu.Extent3D{Width: u32(w), Height: u32(h), DepthOrArrayLayers: 1},
		MipLevelCount: 1,
		SampleCount:   1,
		Dimension:     gputypes.TextureDimension2D,
		Format:        format,
		Usage:         gputypes.TextureUsageCopyDst | gputypes.TextureUsageTextureBinding,
	})
	if err != nil {
		return nil, err
	}
	view, err := g.device.CreateTextureView(tex, nil)
	if err != nil {
		tex.Release()
		return nil, err
	}
	return &gpuTexture{tex: tex, view: view, w: w, h: h}, nil
}

// videoPipeline returns the pipeline video frames draw with, made on first use
// as few windows show any, or nil if it cannot be made.
func (g *gpu) videoPipeline() *wgpu.RenderPipeline {
	if g.video != nil || g.videoErr != nil {
		return g.video
	}
	g.video, g.videoErr = g.makeVideoPipeline()
	if g.videoErr != nil {
		logError("video pipeline", g.videoErr)
	}
	return g.video
}

func (g *gpu) makeVideoPipeline() (*wgpu.RenderPipeline, error) {
	texture := func(binding uint32) gputypes.BindGroupLayoutEntry {
		return gputypes.BindGroupLayoutEntry{
			Binding:    binding,
			Visibility: gputypes.ShaderStageFragment,
			Texture:    &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeFloat, ViewDimension: gputypes.TextureViewDimension2D},
		}
	}
	var err error
	g.videoLayout, err = g.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "fyne video",
		Entries: []gputypes.BindGroupLayoutEntry{
			texture(0), texture(1), texture(2),
			{Binding: 3, Visibility: gputypes.ShaderStageFragment, Sampler: &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeFiltering}},
		},
	})
	if err != nil {
		return nil, err
	}
	layout, err := g.device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            "fyne video",
		BindGroupLayouts: []*wgpu.BindGroupLayout{g.uniformLayout, g.videoLayout},
	})
	if err != nil {
		return nil, err
	}
	src := shaderVideo + recordsConst(unsafe.Sizeof(videoParams{}))
	module, err := g.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: "video", WGSL: src})
	if err != nil {
		return nil, err
	}
	return g.device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:     "video",
		Layout:    layout,
		Vertex:    wgpu.VertexState{Module: module, EntryPoint: "vs_main"},
		Primitive: gputypes.PrimitiveState{Topology: gputypes.PrimitiveTopologyTriangleStrip},
		Fragment: &wgpu.FragmentState{
			Module:     module,
			EntryPoint: "fs_main",
			Targets:    []gputypes.ColorTargetState{{Format: frameFormat, Blend: premultipliedAlpha, WriteMask: gputypes.ColorWriteMaskAll}},
		},
	})
}
