package webgpu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"math"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/naga"
	"github.com/gogpu/naga/ir"
	wgpu "github.com/roffe/gowgpu"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// canvas.Shader support, to the contract documented on Shader.SourceWGSL. A
// shader is compiled once per Name and kept like the built-in pipelines; its
// textures upload once and again only when an image is replaced. Where the
// textures and uniforms go is read out of naga's IR of the source, so the
// shader declares them in plain WGSL and naga does the layout.

// userParams mirrors Fyne in shaders/user_prelude.wgsl.
type userParams struct {
	Bounds, Frame, Quad [4]float32
}

// userShader is one canvas.Shader compiled for this painter.
type userShader struct {
	pipeline  *wgpu.RenderPipeline
	texLayout *wgpu.BindGroupLayout
	userLayout
	data   []byte                 // the group 2 struct; unset members keep their value, as in GL
	images map[string]userTexture // per texture variable, what was uploaded for it
	group  *wgpu.BindGroup        // group 1, rebuilt when an image changes
}

type userTexture struct {
	src image.Image
	tex *gpuTexture
}

// userLayout is what a shader declares in the groups Fyne fills for it.
type userLayout struct {
	textures map[string]uint32 // texture_2d<f32> variable -> group 1 binding
	samplers []uint32          // group 1 sampler bindings
	uniforms map[string]uint32 // f32 member of the group 2 struct -> offset
	span     uint32            // size of the group 2 struct
}

func (p *Painter) drawShader(s *canvas.Shader, pos fyne.Position, frame fyne.Size) {
	us := p.userShader(s)
	if us == nil {
		return
	}
	group := us.bindGroup(p, s)
	if group == nil {
		return
	}
	for name, v := range s.Uniforms {
		if offset, ok := us.uniforms[name]; ok {
			binary.LittleEndian.PutUint32(us.data[offset:], math.Float32bits(v))
		}
	}

	quad, bounds := p.vecRectCoords(pos, s, frame, 0, canvas.Shadow{})
	fw, fh := p.scaleFrameSize(frame)
	x1, x2, y1, y2 := p.scaleRectCoords(bounds[0], bounds[2], bounds[1], bounds[3])
	u := userParams{Bounds: [4]float32{x1, y1, x2, y2}, Frame: [4]float32{fw, fh}, Quad: quad}

	p.flushBatch()            // queued quads are underneath
	p.reserve(2 * bindWindow) // both records have to reach the GPU in the same submit
	fyneOffset := record(p, &u)
	userOffset := p.recordBytes(us.data)
	pass := p.renderPass()
	if pass == nil {
		return
	}
	p.applyScissor(pass)
	pass.SetPipeline(us.pipeline)
	pass.SetBindGroup(0, p.uniformGroup(fyneOffset), nil)
	pass.SetBindGroup(1, group, nil)
	pass.SetBindGroup(2, p.uniformGroup(userOffset), nil)
	pass.Draw(gputypes.DrawArgs{VertexCount: 4, InstanceCount: 1})
	// This pipeline's layout is its own, and on DX12 changing layouts drops
	// every binding, so the next built-in draw binds all it needs again.
	p.pipe, p.bound, p.uniformsBound = us.pipeline, nil, false
}

// userShader returns s compiled, or nil when it cannot be drawn. A failure is
// remembered, so it is logged once rather than every frame.
func (p *Painter) userShader(s *canvas.Shader) *userShader {
	if us, ok := p.userShaders[s.Name]; ok {
		return us
	}
	us, err := p.compileUserShader(s)
	if err != nil {
		fyne.LogError("WebGPU cannot draw shader "+s.Name, err)
	}
	p.userShaders[s.Name] = us
	return us
}

func (p *Painter) compileUserShader(s *canvas.Shader) (*userShader, error) {
	if len(s.SourceWGSL) == 0 {
		return nil, errors.New("it has no SourceWGSL")
	}
	src := shaderUserPrelude + string(s.SourceWGSL)
	layout, err := reflectUserShader(src)
	if err != nil {
		return nil, err
	}

	var entries []gputypes.BindGroupLayoutEntry
	for _, binding := range layout.textures {
		entries = append(entries, gputypes.BindGroupLayoutEntry{
			Binding:    binding,
			Visibility: gputypes.ShaderStageFragment,
			Texture:    &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeFloat, ViewDimension: gputypes.TextureViewDimension2D},
		})
	}
	for _, binding := range layout.samplers {
		entries = append(entries, gputypes.BindGroupLayoutEntry{
			Binding:    binding,
			Visibility: gputypes.ShaderStageFragment,
			Sampler:    &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeFiltering},
		})
	}
	dev := p.gpu.device
	texLayout, err := dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{Label: s.Name, Entries: entries})
	if err != nil {
		return nil, err
	}
	// Group 2 has the layout of group 0, so it binds the same buffer.
	pipeLayout, err := dev.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            s.Name,
		BindGroupLayouts: []*wgpu.BindGroupLayout{p.gpu.uniformLayout, texLayout, p.gpu.uniformLayout},
	})
	if err != nil {
		return nil, err
	}
	module, err := dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: s.Name, WGSL: src})
	if err != nil {
		return nil, err
	}
	pipe, err := dev.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:     s.Name,
		Layout:    pipeLayout,
		Vertex:    wgpu.VertexState{Module: module, EntryPoint: "fyne_vertex"},
		Primitive: gputypes.PrimitiveState{Topology: gputypes.PrimitiveTopologyTriangleStrip},
		Fragment: &wgpu.FragmentState{
			Module:     module,
			EntryPoint: "main",
			Targets:    []gputypes.ColorTargetState{{Format: frameFormat, Blend: straightAlpha, WriteMask: gputypes.ColorWriteMaskAll}},
		},
	})
	if err != nil {
		return nil, err
	}
	return &userShader{
		pipeline:   pipe,
		texLayout:  texLayout,
		userLayout: layout,
		data:       make([]byte, layout.span),
		images:     map[string]userTexture{},
	}, nil
}

// bindGroup returns group 1 for the images s holds now, uploading any that is
// new or replaced since the last draw. A texture variable without an image
// samples transparent black.
func (us *userShader) bindGroup(p *Painter, s *canvas.Shader) *wgpu.BindGroup {
	changed := us.group == nil
	for name := range us.textures {
		img, cached := s.Textures[name], us.images[name]
		if img == cached.src {
			continue
		}
		changed = true
		if cached.tex != nil {
			p.retire(cached.tex.release)
		}
		var tex *gpuTexture
		if img != nil {
			tex = p.imageTexture(img, p.gpu.linear)
		}
		us.images[name] = userTexture{src: img, tex: tex}
	}
	if !changed {
		return us.group
	}

	entries := make([]wgpu.BindGroupEntry, 0, len(us.textures)+len(us.samplers))
	for name, binding := range us.textures {
		view := p.empty.view
		if tex := us.images[name].tex; tex != nil {
			view = tex.view
		}
		entries = append(entries, wgpu.BindGroupEntry{Binding: binding, TextureView: view})
	}
	for _, binding := range us.samplers {
		entries = append(entries, wgpu.BindGroupEntry{Binding: binding, Sampler: p.gpu.linear})
	}
	group, err := p.gpu.device.CreateBindGroup(&wgpu.BindGroupDescriptor{Layout: us.texLayout, Entries: entries})
	if err != nil {
		logError("shader textures", err)
		return nil
	}
	if us.group != nil {
		p.retire(us.group.Release)
	}
	us.group = group
	return group
}

func (us *userShader) release() {
	for _, t := range us.images {
		if t.tex != nil {
			t.tex.release()
		}
	}
	if us.group != nil {
		us.group.Release()
	}
	us.pipeline.Release()
	us.texLayout.Release()
}

// reflectUserShader reads where src wants its textures, samplers and uniforms
// out of naga's IR, which also lays the uniform struct out. Anything bound
// that Fyne would not fill is an error, rather than a draw reading garbage.
func reflectUserShader(src string) (userLayout, error) {
	ast, err := naga.Parse(src)
	if err != nil {
		return userLayout{}, err
	}
	mod, err := naga.LowerWithSource(ast, src)
	if err != nil {
		return userLayout{}, err
	}

	l := userLayout{textures: map[string]uint32{}, uniforms: map[string]uint32{}}
	for _, v := range mod.GlobalVariables {
		b := v.Binding
		if b == nil {
			continue
		}
		switch t := mod.Types[v.Type].Inner.(type) {
		case ir.ImageType:
			if b.Group != 1 || t.Dim != ir.Dim2D || t.Class != ir.ImageClassSampled || t.SampledKind != ir.ScalarFloat || t.Arrayed || t.Multisampled {
				return l, fmt.Errorf("%s: textures have to be texture_2d<f32> in @group(1)", v.Name)
			}
			l.textures[v.Name] = b.Binding
		case ir.SamplerType:
			if b.Group != 1 || t.Comparison {
				return l, fmt.Errorf("%s: samplers have to be sampler in @group(1)", v.Name)
			}
			l.samplers = append(l.samplers, b.Binding)
		case ir.StructType:
			if b.Group == 0 && v.Name == "fyne" {
				continue // the prelude's
			}
			if b.Group != 2 || b.Binding != 0 || v.Space != ir.SpaceUniform {
				return l, fmt.Errorf("%s: uniforms have to be a var<uniform> at @group(2) @binding(0)", v.Name)
			}
			if t.Span > bindWindow {
				return l, fmt.Errorf("%s: uniforms take %d bytes, at most %d fit", v.Name, t.Span, bindWindow)
			}
			l.span = t.Span
			for _, m := range t.Members {
				if s, ok := mod.Types[m.Type].Inner.(ir.ScalarType); ok && s.Kind == ir.ScalarFloat && s.Width == 4 {
					l.uniforms[m.Name] = m.Offset
				}
			}
		default:
			return l, fmt.Errorf("%s: @group(%d) @binding(%d) is not anything Fyne binds", v.Name, b.Group, b.Binding)
		}
	}
	return l, nil
}
