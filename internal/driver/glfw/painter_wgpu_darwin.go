//go:build wgpu

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
*/
import "C"

import (
	"github.com/go-gl/glfw/v3.4/glfw"
	"github.com/gogpu/wgpu"
)

// ponytail: contentsScale is set once, so a move to a screen of another
// scale presents blurred until the layer follows backingScaleFactor.
func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	return wgpu.SurfaceTargetFromMetalLayer(uintptr(C.metalLayer(v.GetCocoaView())))
}
