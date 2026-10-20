package webgpu

import (
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"unsafe"

	wgpu "github.com/roffe/gowgpu"
	"github.com/roffe/gowgpu/gputypes"
	_ "github.com/roffe/gowgpu/hal/allbackends" // Vulkan, Metal or DX12, whichever the platform has, and OpenGL
)

// frameFormat is what everything is drawn into. Present converts it to
// whatever the window surface wants.
const frameFormat = gputypes.TextureFormatRGBA8Unorm

// maxLOD is the sampler's level of detail clamp, WebGPU's default.
const maxLOD = 32

// u32 converts a size, offset or count, never negative nor near 2³², for the
// GPU API.
func u32(v int) uint32 {
	if v < 0 || v > math.MaxUint32 {
		return 0
	}
	return uint32(v)
}

// u64 converts a byte size, never negative, for the GPU API.
func u64(v int) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// The vector shapes, each a fragment shader over the shared Shape uniforms.
const (
	pipeRectangle = iota
	pipeRoundRectangle
	pipeEllipse
	pipeRegularPolygon
	pipeArc
	pipeArbitraryPolygon
	pipeBezierCurve
	shapeCount
)

// shapeShaders run with shaderCommon in front of them; record is the size of
// the Record each declares.
var shapeShaders = [shapeCount]struct {
	name, src string
	record    uintptr
}{
	{"rectangle", shaderRectangle, unsafe.Sizeof(shapeParams{})},
	{"round_rectangle", shaderRoundRectangle, unsafe.Sizeof(shapeParams{})},
	{"ellipse", shaderEllipse, unsafe.Sizeof(shapeParams{})},
	{"regular_polygon", shaderRegularPolygon, unsafe.Sizeof(shapeParams{})},
	{"arc", shaderArc, unsafe.Sizeof(shapeParams{})},
	{"arbitrary_polygon", shaderArbitraryPolygon, unsafe.Sizeof(polyParams{})},
	{"bezier_curve", shaderBezierCurve, unsafe.Sizeof(shapeParams{})},
}

// recordsConst declares RECORDS, the size of an instanced shader's array of
// records: as many as one bind covers, which is where drawQuad cuts a batch.
func recordsConst(record uintptr) string {
	return fmt.Sprintf("\nconst RECORDS: u32 = %du;\n", bindWindow/record)
}

var (
	// straightAlpha is GL's BlendFunc(SRC_ALPHA, ONE_MINUS_SRC_ALPHA), which the
	// shapes, glyphs and lines are written for.
	straightAlpha = &gputypes.BlendState{
		Color: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorSrcAlpha, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd},
		Alpha: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorSrcAlpha, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd},
	}
	// premultipliedAlpha is for textures, which are uploaded premultiplied.
	premultipliedAlpha = &gputypes.BlendState{
		Color: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorOne, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd},
		Alpha: gputypes.BlendComponent{SrcFactor: gputypes.BlendFactorOne, DstFactor: gputypes.BlendFactorOneMinusSrcAlpha, Operation: gputypes.BlendOperationAdd},
	}

	glyphVertexLayout = []gputypes.VertexBufferLayout{{
		ArrayStride: glyphVertexFloats * 4,
		StepMode:    gputypes.VertexStepModeVertex,
		Attributes: []gputypes.VertexAttribute{
			{Format: gputypes.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
			{Format: gputypes.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
			{Format: gputypes.VertexFormatFloat32x4, Offset: 16, ShaderLocation: 2},
		},
	}}
	lineVertexLayout = []gputypes.VertexBufferLayout{{
		ArrayStride: lineVertexFloats * 4,
		StepMode:    gputypes.VertexStepModeVertex,
		Attributes: []gputypes.VertexAttribute{
			{Format: gputypes.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
			{Format: gputypes.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
			{Format: gputypes.VertexFormatFloat32x4, Offset: 16, ShaderLocation: 2},
			{Format: gputypes.VertexFormatFloat32x2, Offset: 32, ShaderLocation: 3},
		},
	}}
)

// gpu is the device every window paints with, and everything built on it that
// does not depend on a window. Every pipeline shares one layout: group 0 is the
// uniform records a draw reads, group 1 a texture and sampler.
type gpu struct {
	instance *wgpu.Instance
	adapter  *wgpu.Adapter
	device   *wgpu.Device
	queue    *wgpu.Queue

	uniformLayout, textureLayout *wgpu.BindGroupLayout
	layout                       *wgpu.PipelineLayout
	linear, nearest              *wgpu.Sampler

	shapes                     [shapeCount]*wgpu.RenderPipeline
	texture, blur, glyph, line *wgpu.RenderPipeline
	blits                      map[gputypes.TextureFormat]*wgpu.RenderPipeline
}

// shared is the process' one device. ponytail: never released, like the
// process' GL driver; windows only release what they created on it.
var shared struct {
	instance *wgpu.Instance
	gpu      *gpu
	choice   int // index into backendChoices of instance
}

// instance returns the instance of the backends being tried, see
// nextInstance.
func instance() (*wgpu.Instance, error) {
	if shared.instance == nil {
		inst, err := wgpu.CreateInstance(&wgpu.InstanceDescriptor{Backends: backendChoices()[shared.choice]})
		if err != nil {
			return nil, err
		}
		shared.instance = inst
	}
	return shared.instance, nil
}

// nextInstance drops the instance for that of the next backendChoices.
func nextInstance() {
	if shared.instance != nil {
		shared.instance.Release()
		shared.instance = nil
	}
	shared.choice++
}

// backendChoices are the backends instance tries in turn: the API
// FYNE_WGPU_BACKEND names (vulkan, dx12, metal or gl), or else the platform's
// own (Vulkan, Metal or DX12) and then OpenGL, for machines without them.
func backendChoices() []gputypes.Backends {
	named := map[string]gputypes.Backends{
		"vulkan": gputypes.BackendsVulkan, "dx12": gputypes.BackendsDX12,
		"metal": gputypes.BackendsMetal, "gl": gputypes.BackendsGL,
	}[strings.ToLower(os.Getenv("FYNE_WGPU_BACKEND"))]
	if named != 0 {
		return []gputypes.Backends{named}
	}
	return []gputypes.Backends{gputypes.BackendsPrimary, gputypes.BackendsGL}
}

// backendsName lists the APIs in b, as Vulkan/Metal/DX12.
func backendsName(b gputypes.Backends) string {
	var names []string
	for _, api := range []gputypes.Backend{gputypes.BackendVulkan, gputypes.BackendMetal, gputypes.BackendDX12, gputypes.BackendGL} {
		if b.Contains(api) {
			names = append(names, api.String())
		}
	}
	return strings.Join(names, "/")
}

// sharedGPU returns the device, creating it on first use on an adapter that
// can present to surface, or any adapter when surface is nil.
func sharedGPU(surface *wgpu.Surface) (*gpu, error) {
	if shared.gpu != nil {
		return shared.gpu, nil
	}
	inst, err := instance()
	if err != nil {
		return nil, err
	}
	adapter, err := inst.RequestAdapter(&wgpu.RequestAdapterOptions{CompatibleSurface: surface})
	if err != nil {
		return nil, fmt.Errorf("no WebGPU adapter: %w", err)
	}
	// Logged before the device is made: a driver or an implicit Vulkan layer
	// that crashes creating it kills the process, and a crash report should
	// still say which GPU and API it was.
	info := adapter.Info()
	log.Printf("Renderer: WebGPU %s on %s (%s)", info.Backend, info.Name, info.DeviceType)
	device, err := adapter.RequestDevice(nil)
	if err != nil {
		return nil, err
	}
	g := &gpu{instance: inst, adapter: adapter, device: device, queue: device.Queue(), blits: map[gputypes.TextureFormat]*wgpu.RenderPipeline{}}
	if err := g.init(); err != nil {
		return nil, err
	}
	shared.gpu = g
	return g, nil
}

func (g *gpu) init() (err error) {
	g.uniformLayout, err = g.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "fyne uniforms",
		Entries: []gputypes.BindGroupLayoutEntry{{
			Binding:    0,
			Visibility: gputypes.ShaderStageVertex | gputypes.ShaderStageFragment,
			Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform, MinBindingSize: bindWindow, HasDynamicOffset: true},
		}},
	})
	if err != nil {
		return err
	}
	g.textureLayout, err = g.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "fyne texture",
		Entries: []gputypes.BindGroupLayoutEntry{
			{
				Binding:    0,
				Visibility: gputypes.ShaderStageFragment,
				Texture:    &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeFloat, ViewDimension: gputypes.TextureViewDimension2D},
			},
			{
				Binding:    1,
				Visibility: gputypes.ShaderStageFragment,
				Sampler:    &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeFiltering},
			},
		},
	})
	if err != nil {
		return err
	}
	g.layout, err = g.device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label:            "fyne",
		BindGroupLayouts: []*wgpu.BindGroupLayout{g.uniformLayout, g.textureLayout},
	})
	if err != nil {
		return err
	}
	if g.linear, err = g.sampler(gputypes.FilterModeLinear); err != nil {
		return err
	}
	if g.nearest, err = g.sampler(gputypes.FilterModeNearest); err != nil {
		return err
	}

	for i, s := range shapeShaders {
		g.shapes[i], err = g.pipeline(s.name, shaderCommon+s.src+recordsConst(s.record), gputypes.PrimitiveTopologyTriangleStrip, straightAlpha, frameFormat, nil)
		if err != nil {
			return err
		}
	}
	if g.texture, err = g.pipeline("texture", shaderTexture+recordsConst(unsafe.Sizeof(texParams{})), gputypes.PrimitiveTopologyTriangleStrip, premultipliedAlpha, frameFormat, nil); err != nil {
		return err
	}
	// The blur writes its result over the pixels it read, so it replaces them.
	if g.blur, err = g.pipeline("blur", shaderBlur, gputypes.PrimitiveTopologyTriangleStrip, nil, frameFormat, nil); err != nil {
		return err
	}
	if g.glyph, err = g.pipeline("glyph", shaderGlyph, gputypes.PrimitiveTopologyTriangleList, straightAlpha, frameFormat, glyphVertexLayout); err != nil {
		return err
	}
	if g.line, err = g.pipeline("line", shaderLine, gputypes.PrimitiveTopologyTriangleList, straightAlpha, frameFormat, lineVertexLayout); err != nil {
		return err
	}
	return nil
}

// blitPipeline returns the pipeline that copies a frame onto a surface of the
// given format.
func (g *gpu) blitPipeline(format gputypes.TextureFormat) (*wgpu.RenderPipeline, error) {
	if pipe := g.blits[format]; pipe != nil {
		return pipe, nil
	}
	pipe, err := g.pipeline("blit", shaderBlit, gputypes.PrimitiveTopologyTriangleList, nil, format, nil)
	if err != nil {
		return nil, err
	}
	g.blits[format] = pipe
	return pipe, nil
}

func (g *gpu) pipeline(name, src string, topology gputypes.PrimitiveTopology, blend *gputypes.BlendState, format gputypes.TextureFormat, buffers []gputypes.VertexBufferLayout) (*wgpu.RenderPipeline, error) {
	module, err := g.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{Label: name, WGSL: src})
	if err != nil {
		return nil, fmt.Errorf("shader %s: %w", name, err)
	}
	pipe, err := g.device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:     name,
		Layout:    g.layout,
		Vertex:    wgpu.VertexState{Module: module, EntryPoint: "vs_main", Buffers: buffers},
		Primitive: gputypes.PrimitiveState{Topology: topology},
		Fragment: &wgpu.FragmentState{
			Module:     module,
			EntryPoint: "fs_main",
			Targets:    []gputypes.ColorTargetState{{Format: format, Blend: blend, WriteMask: gputypes.ColorWriteMaskAll}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("pipeline %s: %w", name, err)
	}
	return pipe, nil
}

func (g *gpu) sampler(filter gputypes.FilterMode) (*wgpu.Sampler, error) {
	return g.device.CreateSampler(&wgpu.SamplerDescriptor{
		AddressModeU: gputypes.AddressModeClampToEdge,
		AddressModeV: gputypes.AddressModeClampToEdge,
		AddressModeW: gputypes.AddressModeClampToEdge,
		MagFilter:    filter,
		MinFilter:    filter,
		MipmapFilter: gputypes.FilterModeNearest,
		LodMaxClamp:  maxLOD,
	})
}

// gpuTexture is a texture together with the bind group that samples it.
type gpuTexture struct {
	tex   *wgpu.Texture
	view  *wgpu.TextureView
	group *wgpu.BindGroup
	w, h  int
}

func (g *gpu) newTexture(w, h int, usage gputypes.TextureUsage, smp *wgpu.Sampler) (*gpuTexture, error) {
	tex, err := g.device.CreateTexture(&wgpu.TextureDescriptor{
		Size:          wgpu.Extent3D{Width: u32(w), Height: u32(h), DepthOrArrayLayers: 1},
		MipLevelCount: 1,
		SampleCount:   1,
		Dimension:     gputypes.TextureDimension2D,
		Format:        frameFormat,
		Usage:         usage | gputypes.TextureUsageTextureBinding,
	})
	if err != nil {
		return nil, err
	}
	view, err := g.device.CreateTextureView(tex, nil)
	if err != nil {
		tex.Release()
		return nil, err
	}
	group, err := g.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout: g.textureLayout,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, TextureView: view},
			{Binding: 1, Sampler: smp},
		},
	})
	if err != nil {
		view.Release()
		tex.Release()
		return nil, err
	}
	return &gpuTexture{tex: tex, view: view, group: group, w: w, h: h}, nil
}

// write uploads RGBA pixels, stride bytes to a row, with their top left at x, y.
func (g *gpu) write(t *gpuTexture, pix []uint8, stride, x, y, w, h int) {
	err := g.queue.WriteTexture(
		&wgpu.ImageCopyTexture{Texture: t.tex, Origin: wgpu.Origin3D{X: u32(x), Y: u32(y)}},
		pix,
		&wgpu.ImageDataLayout{BytesPerRow: u32(stride), RowsPerImage: u32(h)},
		&wgpu.Extent3D{Width: u32(w), Height: u32(h), DepthOrArrayLayers: 1},
	)
	if err != nil {
		logError("texture upload", err)
	}
}

func (t *gpuTexture) release() {
	t.group.Release()
	t.view.Release()
	t.tex.Release()
}
