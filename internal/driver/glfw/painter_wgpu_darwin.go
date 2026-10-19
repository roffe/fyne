//go:build wgpu || !gl

package glfw

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework QuartzCore
#import <AppKit/AppKit.h>
#import <QuartzCore/CAMetalLayer.h>

// metalLayer makes view host a CAMetalLayer for Metal to present to.
static void *metalLayer(void *view) {
	NSView *v = (NSView *)view;
	CAMetalLayer *layer = [CAMetalLayer layer];
	layer.contentsScale = v.window.backingScaleFactor;
	[v setLayer:layer];
	[v setWantsLayer:YES];
	return layer;
}

// followBackingScale keeps the view's CAMetalLayer at the window's backing
// scale. The layer is the view's own, not one AppKit manages, so AppKit
// leaves its contentsScale alone when the window moves to a screen of
// another scale, and the frame presented blurred.
static void followBackingScale(void *view) {
	NSView *v = (NSView *)view;
	if (v.window == nil) {
		return;
	}
	CGFloat scale = v.window.backingScaleFactor;
	if (v.layer.contentsScale != scale) {
		v.layer.contentsScale = scale;
	}
}
*/
import "C"

import (
	"github.com/go-gl/glfw/v3.4/glfw"
	wgpu "github.com/roffe/gowgpu"
)

func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	return wgpu.SurfaceTargetFromMetalLayer(uintptr(C.metalLayer(v.GetCocoaView())))
}

// followBackingScale runs before every present, see the C function.
func followBackingScale(v *glfw.Window) {
	C.followBackingScale(v.GetCocoaView())
}
