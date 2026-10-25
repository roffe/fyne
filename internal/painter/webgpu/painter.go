// Package webgpu is a Fyne painter on WebGPU, through github.com/roffe/gowgpu
// (Vulkan, Metal or DirectX 12 underneath, called through cgo). It draws what
// the GL painter draws, with the same shaders ported to WGSL.
//
// Everything is drawn into an offscreen texture the size of the output, which
// Present copies to the window surface and Capture reads back. Draws are
// recorded into a render pass and only reach the GPU at a submit, so their
// uniforms and batched vertices collect in CPU arenas that are written to
// their buffers once, right before the submit that reads them.
package webgpu

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"sync"
	"time"
	"unsafe"

	wgpu "github.com/roffe/gowgpu"
	"github.com/roffe/gowgpu/gputypes"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/internal"
	"fyne.io/fyne/v2/internal/build"
	"fyne.io/fyne/v2/internal/cache"
	paint "fyne.io/fyne/v2/internal/painter"
	"fyne.io/fyne/v2/internal/scale"
	"fyne.io/fyne/v2/theme"
)

const (
	// bindWindow is how much of the uniform buffer one bind covers: the
	// records of an instanced batch, or a single draw's. 16 KiB is what every
	// backend guarantees, GLES 3 included.
	bindWindow = 16 << 10
	// bindAlign is where every backend takes uniform offsets: WebGPU's
	// minUniformBufferOffsetAlignment.
	bindAlign = 256
	// copyRowAlign is how buffer rows of a texture copy are aligned:
	// WebGPU's COPY_BYTES_PER_ROW_ALIGNMENT.
	copyRowAlign = 256
	// arenaSize is the uniform and the starting vertex buffer size. A frame
	// that needs more is submitted in parts: 4 MiB holds the records of
	// 20000 shapes, where 1 MiB made such a frame four submits.
	arenaSize = 4 << 20
)

// Painter draws a Fyne canvas with WebGPU.
type Painter struct {
	canvas fyne.Canvas
	target *wgpu.SurfaceTargetUnsafe
	gpu    *gpu

	surface       *wgpu.Surface
	surfaceFormat gputypes.TextureFormat
	presentMode   gputypes.PresentMode
	alphaMode     gputypes.CompositeAlphaMode
	configured    image.Point // the size the surface is configured at
	// needsRepaint, behindSize and behindFrames track a swapchain that took
	// another size than the frame, see checkExtent.
	needsRepaint bool
	behindSize   image.Point
	behindFrames int

	frame              *gpuTexture // what everything is drawn into
	width, height      int
	pixScale, texScale float32

	enc        *wgpu.CommandEncoder
	pass       *wgpu.RenderPassEncoder
	clear      bool // the next pass starts by clearing to clearColor
	clearColor gputypes.Color
	clip       image.Rectangle // in frame pixels, when clipping
	clipping   bool
	scissor    image.Rectangle      // what the pass is set to, see applyScissor
	pipe       *wgpu.RenderPipeline // bound in pass
	bound      *wgpu.BindGroup      // group 1 bound in pass
	// uniformsBound is whether group 0 is bound in pass, at any offset
	uniformsBound bool
	// vertexPipe is the pipeline vertexBuf was bound under in pass, nil if
	// not yet. Batches pick their vertices with FirstVertex rather than
	// rebinding it at an offset, but gowgpu's GL backend sets the vertex
	// attributes from the pipeline current at the bind, so a pipeline
	// change rebinds: a line batch drawn with the glyph layout read past the
	// buffer and crashed llvmpipe.
	vertexPipe *wgpu.RenderPipeline

	// The instanced draw being collected: batchN records of one pipeline and
	// texture, back to back in uniforms from batchStart.
	batchPipe          *wgpu.RenderPipeline
	batchTex           *wgpu.BindGroup
	batchStart, batchN int

	uniforms, vertices    []byte // this submission's, see submit
	uniformBuf, vertexBuf *wgpu.Buffer
	uniformGroup          *wgpu.BindGroup // the bind window of uniformBuf, see bind
	offset                [1]uint32       // the dynamic offset of a bind, see dynamicOffset
	empty                 *gpuTexture     // bound for draws that sample nothing
	textures              map[cache.TextureType]*gpuTexture
	textWindows           map[*canvas.Text]textWindow // runs too wide for one texture, see drawTextWindow
	maxTextureSize        int
	videos                map[cache.TextureType]*gpuVideo // registered in the cache like textures
	garbage               []func()                        // releases waiting for the next submit
	blurSnap              *gpuTexture
	atlas                 paint.GlyphAtlas
	atlasTex              *gpuTexture
	quadScratch           []paint.GlyphQuad
	slug                  paint.SlugGlyphs
	slugQuads             []paint.SlugQuad
	slugPending           []float32 // queued Slug text, slugVertexFloats a vertex
	slugTex               *gpuTexture
	pixels                []uint8                // updateImageTexture's upload scratch
	glyphPending          []float32              // queued quads, glyphVertexFloats a vertex
	linePending           []float32              // queued lines, lineVertexFloats a vertex
	userShaders           map[string]*userShader // by Shader.Name, nil if it failed
	warnDevice            sync.Once
}

// nextTexture names textures in the cache, see textureID. Shared by every
// painter so that a lookup in the wrong one misses instead of finding another
// canvas' texture.
var nextTexture uint32

// NewPainter returns a painter for c that presents to the window target names,
// or one that only draws offscreen, for Capture, when target is nil.
func NewPainter(c fyne.Canvas, target *wgpu.SurfaceTargetUnsafe) *Painter {
	p := &Painter{canvas: c, target: target, textures: map[cache.TextureType]*gpuTexture{}, textWindows: map[*canvas.Text]textWindow{}, videos: map[cache.TextureType]*gpuVideo{}, userShaders: map[string]*userShader{}}
	p.SetFrameBufferScale(1)
	return p
}

// Init connects to the GPU and, for a window, its surface. Without a GPU API
// that can draw the window there is nothing this painter can do, so failure
// panics, naming what each API it tried said.
func (p *Painter) Init() {
	if err := p.TryInit(); err != nil {
		panic("fyne: no graphics API can draw this window:\n" + err.Error())
	}
}

// TryInit is Init returning its failure, for a driver with another painter
// to fall back to. Release frees what it made on the way.
func (p *Painter) TryInit() (err error) {
	if p.gpu, err = p.connect(); err != nil {
		return err
	}
	if p.surface != nil {
		p.chooseSurfaceFormat()
	}

	dev := p.gpu.device
	p.maxTextureSize = int(dev.Limits().MaxTextureDimension2D)
	if p.uniformBuf, err = dev.CreateBuffer(&wgpu.BufferDescriptor{Label: "fyne uniforms", Size: arenaSize, Usage: gputypes.BufferUsageUniform | gputypes.BufferUsageCopyDst}); err != nil {
		return err
	}
	if p.vertexBuf, err = dev.CreateBuffer(&wgpu.BufferDescriptor{Label: "fyne vertices", Size: arenaSize, Usage: gputypes.BufferUsageVertex | gputypes.BufferUsageCopyDst}); err != nil {
		return err
	}
	// One bind group covers the whole arena: a bind picks its window with a
	// dynamic offset, which every backend takes at bindAlign.
	p.uniformGroup, err = dev.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Layout:  p.gpu.uniformLayout,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: p.uniformBuf, Size: bindWindow}},
	})
	if err != nil {
		return err
	}
	p.empty, err = p.gpu.newTexture(1, 1, 0, p.gpu.nearest)
	return err
}

// chooseSurfaceFormat picks a surface format the frame copies to unchanged
// and a present mode that never blocks for long.
func (p *Painter) chooseSurfaceFormat() {
	caps := p.gpu.adapter.GetSurfaceCapabilities(p.surface)
	if caps == nil || len(caps.Formats) == 0 {
		p.surfaceFormat, p.presentMode, p.alphaMode = gputypes.TextureFormatBGRA8Unorm, gputypes.PresentModeFifo, gputypes.CompositeAlphaModeAuto
		return
	}

	// ponytail: an sRGB-only surface would get the frame gamma encoded twice;
	// a non-sRGB view of it is the fix if one ever turns up.
	p.surfaceFormat = caps.Formats[0]
	for _, f := range caps.Formats {
		if f == gputypes.TextureFormatBGRA8Unorm || f == gputypes.TextureFormatRGBA8Unorm {
			p.surfaceFormat = f
			break
		}
	}

	// Mailbox never blocks, which leaves frame pacing to the driver loop as
	// with GL. FIFO on Wayland blocks for as long as the window is hidden, so
	// there tearing is the lesser evil.
	want := []gputypes.PresentMode{gputypes.PresentModeMailbox}
	if build.IsWayland {
		want = append(want, gputypes.PresentModeImmediate)
	}
	p.presentMode = gputypes.PresentModeFifo // the one every surface has
	for _, m := range want {
		if slices.Contains(caps.PresentModes, m) {
			p.presentMode = m
			break
		}
	}

	p.alphaMode = gputypes.CompositeAlphaModeAuto
	if slices.Contains(caps.AlphaModes, gputypes.CompositeAlphaModeOpaque) {
		p.alphaMode = gputypes.CompositeAlphaModeOpaque
	}
}

// Capture reads back the last frame painted.
func (p *Painter) Capture(c fyne.Canvas) image.Image {
	// Rounded up like the canvas sizes its own capture: PixelCoordinateForPosition
	// truncates, losing the last row and column of a fractional size.
	w, h := scale.ToScreenCoordinate(c, c.Size().Width), scale.ToScreenCoordinate(c, c.Size().Height)
	w, h = min(w, p.width), min(h, p.height)
	img := image.NewRGBA(image.Rect(0, 0, max(w, 0), max(h, 0)))
	if w > 0 && h > 0 && p.frame != nil {
		if err := p.readFrame(img); err != nil {
			logError("capture", err)
		}
	}
	return img
}

// readFrame copies the top left of the frame, as much as fits, into img.
func (p *Painter) readFrame(img *image.RGBA) error {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	p.flushBatch()
	p.submit()

	stride := (w*4 + copyRowAlign - 1) &^ (copyRowAlign - 1)
	size := u64(stride * h)
	buf, err := p.gpu.device.CreateBuffer(&wgpu.BufferDescriptor{Size: size, Usage: gputypes.BufferUsageMapRead | gputypes.BufferUsageCopyDst})
	if err != nil {
		return err
	}
	defer buf.Release()
	enc := p.encoder()
	if enc == nil {
		return nil
	}
	enc.CopyTextureToBuffer(p.frame.tex, buf, []wgpu.BufferTextureCopy{{
		BufferLayout: wgpu.ImageDataLayout{BytesPerRow: u32(stride), RowsPerImage: u32(h)},
		TextureBase:  wgpu.ImageCopyTexture{Texture: p.frame.tex},
		Size:         wgpu.Extent3D{Width: u32(w), Height: u32(h), DepthOrArrayLayers: 1},
	}})
	p.submit()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := buf.Map(ctx, wgpu.MapModeRead, 0, size); err != nil {
		return err
	}
	rng, err := buf.MappedRange(0, size)
	if err == nil {
		data := rng.Bytes()
		for y := range h {
			row := img.Pix[y*img.Stride : y*img.Stride+w*4]
			copy(row, data[y*stride:])
			// Composited against the window background already, but blending
			// leaves alpha below 255 under shadows and antialiased edges.
			for x := 3; x < len(row); x += 4 {
				row[x] = 0xff
			}
		}
	}
	return errors.Join(err, buf.Unmap())
}

// Clear starts a frame on the theme background.
func (p *Painter) Clear() {
	// Anything recorded or queued so far is about to be covered.
	p.glyphPending, p.linePending = p.glyphPending[:0], p.linePending[:0]
	p.endPass()
	p.clear = true
	r, g, b, a := theme.Color(theme.ColorNameBackground).RGBA()
	p.clearColor = gputypes.Color{R: float64(r) / 0xffff, G: float64(g) / 0xffff, B: float64(b) / 0xffff, A: float64(a) / 0xffff}
}

// Free releases the texture held for obj. Shaders own nothing to free.
func (p *Painter) Free(obj fyne.CanvasObject) {
	if t, ok := obj.(*canvas.Text); ok {
		delete(p.textWindows, t)
	}
	id, ok := cache.GetTexture(obj)
	if !ok {
		return
	}
	p.freeTexture(id)
	cache.DeleteTexture(obj)
}

// FlushGlyphs draws everything queued and submits all recorded work. The atlas
// calls it before it reuses space and the canvas at the end of a frame. A glyph
// upload lands at the next submit, ahead of everything that submit draws, so
// the draws that sampled the old contents have to be submitted first.
func (p *Painter) FlushGlyphs() {
	p.flushBatch()
	p.submit()
}

// MaxTextureSize returns the largest texture side the device takes.
func (p *Painter) MaxTextureSize() int {
	return p.maxTextureSize
}

// Paint draws obj, but not its children, at pos.
func (p *Painter) Paint(obj fyne.CanvasObject, pos fyne.Position, frame fyne.Size, clip *internal.ClipItem) {
	if !obj.Visible() {
		return
	}

	size := obj.Size()
	var clipPos fyne.Position
	clipSize := frame
	if clip != nil {
		clipPos, clipSize = clip.Rect()
	}
	if pos.Y > clipPos.Y+clipSize.Height || pos.Y+size.Height < clipPos.Y ||
		pos.X > clipPos.X+clipSize.Width || pos.X+size.Width < clipPos.X {
		return
	}

	p.drawObject(obj, pos, frame, clip)
}

// Present shows the frame painted since Clear in the window, in place of the
// SwapBuffers a window without a GL context does not have.
func (p *Painter) Present() {
	p.flushBatch()
	p.submit()
	if p.surface == nil || p.frame == nil {
		return
	}
	err := p.drawToSurface()
	if errors.Is(err, wgpu.ErrSurfaceOutdated) {
		// X11 Vulkan outdates the swapchain on every resize step: this frame
		// was dropped, so paint another at the new size.
		p.needsRepaint = true
	} else if err != nil {
		logError("present", err)
	}
}

// drawToSurface draws the frame into the surface's next texture and shows it.
func (p *Painter) drawToSurface() error {
	pipe, err := p.gpu.blitPipeline(p.surfaceFormat)
	if err != nil {
		return err
	}
	tex, suboptimal := p.acquire()
	if tex == nil {
		return nil
	}
	if suboptimal {
		p.configured = image.Point{} // present this one, configure afresh for the next
	}
	view, err := tex.CreateView(nil)
	if err != nil {
		p.surface.DiscardTexture()
		return err
	}
	defer view.Release()

	enc := p.encoder()
	if enc == nil {
		p.surface.DiscardTexture()
		return nil
	}
	pass, err := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{View: view, LoadOp: gputypes.LoadOpClear, StoreOp: gputypes.StoreOpStore}},
	})
	if err != nil {
		p.surface.DiscardTexture()
		return err
	}
	pass.SetPipeline(pipe)
	pass.SetBindGroup(0, p.uniformGroup, p.dynamicOffset(0))
	pass.SetBindGroup(1, p.frame.group, nil)
	pass.Draw(gputypes.DrawArgs{VertexCount: 3, InstanceCount: 1})
	endErr := pass.End()
	p.submit()
	if err := p.surface.Present(tex); err != nil {
		p.configured = image.Point{}
		return err
	}
	return endErr
}

// acquire returns the surface texture to present into, configuring the
// surface first when the frame's size changed or the old configuration
// stopped working.
func (p *Painter) acquire() (*wgpu.SurfaceTexture, bool) {
	size := image.Pt(p.width, p.height)
	for range 2 {
		if p.configured != size {
			err := p.surface.Configure(p.gpu.device, &wgpu.SurfaceConfiguration{
				Width:       u32(p.width),
				Height:      u32(p.height),
				Format:      p.surfaceFormat,
				Usage:       gputypes.TextureUsageRenderAttachment,
				PresentMode: p.presentMode,
				AlphaMode:   p.alphaMode,
			})
			if err != nil {
				logError("surface configuration", err)
				return nil, false
			}
			p.configured = size
			p.checkExtent(size)
		}
		tex, suboptimal, err := p.surface.GetCurrentTexture()
		if err == nil {
			return tex, suboptimal
		}
		p.configured = image.Point{} // outdated or lost, try a fresh configuration
	}
	return nil, false
}

// connect makes the window's surface, unless AttachSurface has, and gets the
// shared device on an adapter that can present to it. Until a device exists
// that goes down backendChoices: a machine without Vulkan, Metal or DX12 (an
// old GPU, a VM) gets OpenGL.
func (p *Painter) connect() (*gpu, error) {
	var errs []error
	for {
		g, err := p.connectOnce()
		if err == nil {
			return g, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", backendsName(backendChoices()[shared.choice]), err))
		if shared.gpu != nil || shared.choice+1 == len(backendChoices()) {
			return nil, errors.Join(errs...)
		}
		if p.surface != nil { // made on the instance about to go
			p.surface.Release()
			p.surface = nil
		}
		nextInstance()
	}
}

func (p *Painter) connectOnce() (*gpu, error) {
	if p.target != nil && p.surface == nil {
		inst, err := instance()
		if err != nil {
			return nil, err
		}
		if p.surface, err = inst.CreateSurfaceUnsafe(*p.target); err != nil {
			return nil, err
		}
	}
	return sharedGPU(p.surface)
}

// maxBehindFrames is how many frames in a row acquire reconfigures a surface
// that keeps an extent other than the frame's, before it settles for it.
const maxBehindFrames = 30

// checkExtent looks at the swapchain size the surface took for size. Vulkan
// on Android takes the window's current size, which mid-rotation is still the
// old one while the frame already has the new: the frame would be stretched
// onto it until the next size change. So the next frame configures again,
// and needsRepaint asks the driver for that frame.
//
// ponytail: a surface that never matches stops being retried after
// maxBehindFrames, a platform that does that would need an extent-sized frame.
func (p *Painter) checkExtent(size image.Point) {
	if size != p.behindSize {
		p.behindSize, p.behindFrames = size, 0
	}
	w, h := p.surface.ActualExtent()
	p.needsRepaint = (int(w) != size.X || int(h) != size.Y) && p.behindFrames < maxBehindFrames
	if p.needsRepaint {
		p.configured = image.Point{}
		p.behindFrames++
	}
}

// NeedsRepaint reports whether the last Present went to a surface that has not
// caught up with the frame's size, so another frame should be painted soon even
// if nothing changed.
func (p *Painter) NeedsRepaint() bool {
	return p.needsRepaint
}

// Release frees the window surface and everything the painter put on the GPU.
// The native window has to outlive it.
func (p *Painter) Release() {
	p.endPass()
	if p.enc != nil {
		p.enc.DiscardEncoding()
		p.enc = nil
	}
	for id := range p.textures {
		p.freeTexture(id)
	}
	for id := range p.videos {
		p.freeTexture(id)
	}
	clear(p.textWindows)
	for _, release := range p.garbage {
		release()
	}
	for _, us := range p.userShaders {
		if us != nil {
			us.release()
		}
	}
	for _, t := range []*gpuTexture{p.frame, p.atlasTex, p.blurSnap, p.empty, p.slugTex} {
		if t != nil {
			t.release()
		}
	}
	p.garbage, p.userShaders, p.frame, p.atlasTex, p.blurSnap, p.empty = nil, nil, nil, nil, nil, nil
	for _, b := range []*wgpu.Buffer{p.uniformBuf, p.vertexBuf} {
		if b != nil {
			b.Release()
		}
	}
	if p.uniformGroup != nil {
		p.uniformGroup.Release()
	}
	p.uniformBuf, p.vertexBuf, p.uniformGroup = nil, nil, nil
	if p.surface != nil {
		p.surface.Release()
		p.surface = nil
	}
}

// SetFrameBufferScale tells us when we have more than 1 framebuffer pixel for each output pixel.
func (p *Painter) SetFrameBufferScale(fbScale float32) {
	p.texScale = fbScale
	p.pixScale = p.canvas.Scale() * fbScale
}

// SetOutputSize sizes the frame, in pixels.
func (p *Painter) SetOutputSize(width, height int) {
	if width == p.width && height == p.height && p.frame != nil {
		return
	}
	p.flushBatch()
	p.submit() // anything recorded belongs to the old frame
	if p.frame != nil {
		p.retire(p.frame.release)
		p.frame = nil
	}
	p.width, p.height = width, height
	if width <= 0 || height <= 0 || p.gpu == nil {
		return // minimised: nothing to draw into until it comes back
	}
	frame, err := p.gpu.newTexture(width, height, gputypes.TextureUsageRenderAttachment|gputypes.TextureUsageCopySrc, p.gpu.nearest)
	if err != nil {
		logError("frame allocation", err)
		return
	}
	p.frame = frame
}

// StartClipping clips what follows to the given area of the canvas.
func (p *Painter) StartClipping(pos fyne.Position, size fyne.Size) {
	x, y := p.textureScale(pos.X), p.textureScale(pos.Y)
	w, h := max(p.textureScale(size.Width), 0), max(p.textureScale(size.Height), 0)
	p.setClip(image.Rect(int(x), int(y), int(x+w), int(y+h)), true)
}

// StopClipping stops clipping paint actions.
func (p *Painter) StopClipping() {
	p.setClip(p.clip, false)
}

// setClip changes the clip. What is queued is drawn under the old one first,
// unless the area drawn in stays the same, which leaves the batch going. The
// scissor itself waits for a draw: a UI pushes and pops many clips that
// nothing is drawn in, and every command costs gogpu allocations.
func (p *Painter) setClip(r image.Rectangle, clipping bool) {
	if cut(p.width, p.height, r, clipping) != p.clipRect() {
		p.flushBatch()
	}
	p.clip, p.clipping = r, clipping
}

// clipRect is the area of the frame drawn in.
func (p *Painter) clipRect() image.Rectangle {
	return cut(p.width, p.height, p.clip, p.clipping)
}

// cut is the part of a w by h frame drawn in, clipping to r or not.
func cut(w, h int, r image.Rectangle, clipping bool) image.Rectangle {
	frame := image.Rect(0, 0, w, h)
	if !clipping {
		return frame
	}
	return frame.Intersect(r)
}

// applyScissor sets the pass' scissor to the clip, unless it already is.
func (p *Painter) applyScissor(pass *wgpu.RenderPassEncoder) {
	if r := p.clipRect(); r != p.scissor {
		pass.SetScissorRect(gputypes.ScissorRect{X: u32(r.Min.X), Y: u32(r.Min.Y), Width: u32(r.Dx()), Height: u32(r.Dy())})
		p.scissor = r
	}
}

// anyUniforms is the offset a draw that reads no uniforms binds at: any
// record already bound will do, so batches skip the bind.
const anyUniforms = ^uint32(0)

// bind readies the pass for a draw by pipe with the uniform record at offset,
// sampling tex, setting only what changed. The built-in pipelines share one
// layout, so bindings outlive a change between them.
func (p *Painter) bind(pass *wgpu.RenderPassEncoder, pipe *wgpu.RenderPipeline, offset uint32, tex *wgpu.BindGroup) {
	p.applyScissor(pass)
	if p.pipe != pipe {
		pass.SetPipeline(pipe)
		p.pipe = pipe
	}
	if offset != anyUniforms || !p.uniformsBound {
		if offset == anyUniforms {
			offset = 0
		}
		pass.SetBindGroup(0, p.uniformGroup, p.dynamicOffset(offset))
		p.uniformsBound = true
	}
	if tex == nil {
		if p.bound != nil {
			return // whatever is bound will do for a draw that samples nothing
		}
		tex = p.empty.group
	}
	if tex != p.bound {
		pass.SetBindGroup(1, tex, nil)
		p.bound = tex
	}
}

// dynamicOffset returns offset as the dynamic offset of a bind of the uniform
// window, in the painter's scratch: the backends read it during the call.
func (p *Painter) dynamicOffset(offset uint32) []uint32 {
	p.offset[0] = offset
	return p.offset[:]
}

// drawQuad queues one quad of pipe with the uniforms u, sampling tex. A run of
// them with one pipeline and texture is a single instanced draw, each quad
// reading its own record: gogpu allocates for every command recorded, so a
// command per quad cost more than the drawing did.
func drawQuad[T any](p *Painter, pipe *wgpu.RenderPipeline, u *T, tex *wgpu.BindGroup) {
	b := unsafe.Slice((*byte)(unsafe.Pointer(u)), unsafe.Sizeof(*u)) //nolint:gosec // G103: uniforms go to the GPU as bytes
	if p.batchN > 0 && (pipe != p.batchPipe || tex != p.batchTex || (p.batchN+1)*len(b) > bindWindow) {
		p.flushInstances()
	}
	if p.batchN == 0 {
		p.flushQuads() // queued glyphs and lines are underneath
		p.flushLines()
		p.flushSlug()
		p.batchPipe, p.batchTex, p.batchStart = pipe, tex, p.uniformSpace()
	}
	p.uniforms = append(p.uniforms, b...)
	p.batchN++
}

// flushInstances draws the run drawQuad collected.
func (p *Painter) flushInstances() {
	n := p.batchN
	if n == 0 {
		return
	}
	p.batchN = 0
	pass := p.renderPass()
	if pass == nil {
		return
	}
	p.bind(pass, p.batchPipe, u32(p.batchStart), p.batchTex)
	pass.Draw(gputypes.DrawArgs{VertexCount: 4, InstanceCount: u32(n)})
}

// drawOne draws one quad of pipe with the uniforms u straight away, for
// pipelines that are not instanced.
func drawOne[T any](p *Painter, pipe *wgpu.RenderPipeline, u *T, tex *wgpu.BindGroup) {
	p.flushBatch() // queued quads are underneath
	offset := record(p, u)
	pass := p.renderPass()
	if pass == nil {
		return
	}
	p.bind(pass, pipe, offset, tex)
	pass.Draw(gputypes.DrawArgs{VertexCount: 4, InstanceCount: 1})
}

// record queues one draw's uniforms, returning their offset in the uniform
// buffer. Nothing can come between it and the draw, which might submit.
func record[T any](p *Painter, u *T) uint32 {
	return p.recordBytes(unsafe.Slice((*byte)(unsafe.Pointer(u)), unsafe.Sizeof(*u))) //nolint:gosec // G103: uniforms go to the GPU as bytes
}

func (p *Painter) recordBytes(b []byte) uint32 {
	offset := p.uniformSpace()
	p.uniforms = append(p.uniforms, b...)
	return u32(offset)
}

// uniformSpace returns where the next uniforms go: an offset a bind can start
// at, with a whole bind window of buffer after it. It submits first if the
// buffer has no such room left, so nothing may be queued when it is called.
func (p *Painter) uniformSpace() int {
	p.reserve(bindWindow)
	start := alignBind(len(p.uniforms))
	p.uniforms = slices.Grow(p.uniforms, start+bindWindow-len(p.uniforms))[:start]
	return start
}

// reserve submits first unless n bytes fit from the next bind offset on, so
// that everything one draw records shares a submit.
func (p *Painter) reserve(n int) {
	if alignBind(len(p.uniforms))+n > arenaSize {
		p.submit()
	}
}

// alignBind rounds up to the 256 bytes every backend takes uniform offsets at.
func alignBind(n int) int {
	return (n + bindAlign - 1) &^ (bindAlign - 1)
}

// vertexSpace makes room for n bytes of batched vertices of stride bytes,
// returning where: a multiple of stride, for Draw's FirstVertex to point at.
func (p *Painter) vertexSpace(n, stride int) int {
	offset := (len(p.vertices) + stride - 1) / stride * stride
	if u64(offset+n) > p.vertexBuf.Size() {
		p.submit()
		offset = 0
		if u64(n) > p.vertexBuf.Size() {
			// One batch larger than the whole buffer: grow it, everything
			// recorded against the old one having just been submitted.
			buf, err := p.gpu.device.CreateBuffer(&wgpu.BufferDescriptor{Label: "fyne vertices", Size: u64(n) * 2, Usage: gputypes.BufferUsageVertex | gputypes.BufferUsageCopyDst})
			if err != nil {
				logError("vertex buffer", err)
				return -1
			}
			p.vertexBuf.Release()
			p.vertexBuf = buf
		}
	}
	p.vertices = append(p.vertices, make([]byte, offset+n-len(p.vertices))...)
	return offset
}

func (p *Painter) encoder() *wgpu.CommandEncoder {
	if p.enc == nil {
		enc, err := p.gpu.device.CreateCommandEncoder(nil)
		if err != nil {
			p.warnDevice.Do(func() { logError("command encoder", err) })
			return nil
		}
		p.enc = enc
	}
	return p.enc
}

// renderPass returns the pass draws go into, opening one on the frame if need
// be. Only the first pass of a frame clears it, every later one loads it.
func (p *Painter) renderPass() *wgpu.RenderPassEncoder {
	if p.pass != nil {
		return p.pass
	}
	if p.frame == nil || p.encoder() == nil {
		return nil
	}
	load := gputypes.LoadOpLoad
	if p.clear {
		load = gputypes.LoadOpClear
	}
	pass, err := p.enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{View: p.frame.view, LoadOp: load, StoreOp: gputypes.StoreOpStore, ClearValue: p.clearColor}},
	})
	if err != nil {
		p.warnDevice.Do(func() { logError("render pass", err) })
		return nil
	}
	p.pass, p.pipe, p.bound, p.uniformsBound, p.vertexPipe, p.clear = pass, nil, nil, false, nil, false
	p.scissor = image.Rect(0, 0, p.width, p.height) // where a pass starts
	// The frame is the drawable, which on X11 is the window's size while the
	// canvas has yet to catch up with a resize: the content keeps its scale
	// in the top left of it rather than stretching, as the GL painter's
	// viewport does. The shaders' pixel coordinates start at the top left
	// too, so nothing else moves.
	if w, h := p.canvasPixels(); (w != p.width || h != p.height) && w > 0 && h > 0 {
		pass.SetViewport(gputypes.Viewport{Width: float32(w), Height: float32(h), MaxDepth: 1})
	}
	return pass
}

// canvasPixels is the canvas's size in frame pixels.
func (p *Painter) canvasPixels() (w, h int) {
	size := p.canvas.Size()
	return int(float32(scale.ToScreenCoordinate(p.canvas, size.Width)) * p.texScale),
		int(float32(scale.ToScreenCoordinate(p.canvas, size.Height)) * p.texScale)
}

func (p *Painter) endPass() {
	if p.pass == nil {
		return
	}
	if err := p.pass.End(); err != nil {
		logError("render pass", err)
	}
	p.pass = nil
}

// submit sends everything recorded so far to the GPU, after writing the
// uniforms and vertices it reads. Batches still queued are not included,
// callers flush them first when they belong in.
func (p *Painter) submit() {
	if p.clear && p.frame != nil {
		p.renderPass() // a frame with nothing drawn still has to be cleared
	}
	p.endPass()
	if p.enc == nil {
		return
	}
	q := p.gpu.queue
	if len(p.uniforms) > 0 {
		if err := q.WriteBuffer(p.uniformBuf, 0, p.uniforms); err != nil {
			logError("uniform upload", err)
		}
	}
	if len(p.vertices) > 0 {
		if err := q.WriteBuffer(p.vertexBuf, 0, p.vertices); err != nil {
			logError("vertex upload", err)
		}
	}
	cmd, err := p.enc.Finish()
	p.enc, p.uniforms, p.vertices = nil, p.uniforms[:0], p.vertices[:0]
	if err == nil {
		_, err = q.Submit(cmd)
	}
	if err != nil {
		logError("submit", err)
	}
	for _, release := range p.garbage {
		release()
	}
	p.garbage = p.garbage[:0]
}

// retire calls release once nothing recorded can still use what it frees:
// gogpu defers a release past the last submit, but not past one not yet made.
func (p *Painter) retire(release func()) {
	if p.enc == nil {
		release()
		return
	}
	p.garbage = append(p.garbage, release)
}

func logError(what string, err error) {
	fyne.LogError("WebGPU "+what+" failed", err)
}
