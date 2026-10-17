//go:build ios && (wgpu || !gl)

package app

// The define makes darwin_ios.m back the app's view with a CAMetalLayer
// instead of making a GLKView and its EAGL context.

// #cgo CPPFLAGS: -DFYNE_METAL_LAYER
import "C"

// glPresents is whether frames reach the screen through the app's GL context.
// With WebGPU the painter presents to the view's CAMetalLayer itself.
const glPresents = false

// NativeWindow is the CAMetalLayer the painter presents to, delivered as the
// DrawContext of the lifecycle event that makes the app visible. The layer
// lives as long as the app: the painter lets go of it in the background, as
// iOS allows no GPU work there, and attaches again on return.
type NativeWindow uintptr

var metalLayer uintptr

// setMetalLayer is called once the view controller has made its view.
//
//export setMetalLayer
func setMetalLayer(layer uintptr) {
	metalLayer = layer
}

func init() {
	drawContext = func(*app) any { return NativeWindow(metalLayer) }
}

func redrawDone() {}

// WindowReleased has no one to tell on iOS, the layer outlives the painter.
func WindowReleased() {}

func waitWindowReleased() {}
