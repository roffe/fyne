package webgpu

import (
	"image"

	wgpu "github.com/roffe/gowgpu"
)

// AttachSurface makes Present show frames in the window target names, in place
// of any surface the painter had. Android hands an app a new native window each
// time it comes back to the foreground, so a mobile painter is made without a
// target and gets its window here.
func (p *Painter) AttachSurface(target wgpu.SurfaceTargetUnsafe) error {
	p.DetachSurface()
	if p.gpu == nil { // before Init, which makes the surface on a backend that can draw it
		p.target = &target
		return nil
	}
	inst, err := instance()
	if err != nil {
		return err
	}
	surface, err := inst.CreateSurfaceUnsafe(target)
	if err != nil {
		return err
	}
	p.surface, p.target, p.configured = surface, &target, image.Point{}
	p.chooseSurfaceFormat()
	return nil
}

// DetachSurface lets go of the window surface once the GPU is done with what
// was submitted to it, so the window can be destroyed. Everything else stays:
// the painter draws offscreen until a surface is attached again.
func (p *Painter) DetachSurface() {
	if p.surface == nil {
		return
	}
	p.flushBatch()
	p.submit()
	p.surface.Unconfigure()
	p.surface.Release()
	p.surface, p.target, p.configured = nil, nil, image.Point{}
}
