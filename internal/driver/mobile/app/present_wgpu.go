//go:build android && wgpu && (arm64 || amd64)

package app

import (
	"log"
	"time"
)

// glPresents is whether frames reach the screen through the app's GL context:
// Publish swaps the EGL or EAGL buffers. Without it the painter presents itself.
// With wgpu the window gets no EGL surface at all: Vulkan cannot create a
// swapchain on a window that already has one (VK_ERROR_NATIVE_WINDOW_IN_USE_KHR).
const glPresents = false

// NativeWindow is the ANativeWindow the painter presents to, delivered as the
// DrawContext of the lifecycle event that makes the app visible.
type NativeWindow uintptr

func init() {
	drawContext = func(*app) any { return NativeWindow(nativeWindow) }
}

// windowReleased carries the painter's acknowledgement that it has let go of a
// window Android is destroying. Buffered, so the ack never waits on the receiver.
var windowReleased = make(chan struct{}, 1)

// redrawDone unblocks onNativeWindowRedrawNeeded once the painter has presented.
func redrawDone() {
	select {
	case windowRedrawDone <- struct{}{}:
	default:
	}
}

// WindowReleased tells Android that the painter has let go of the native window
// it was destroying.
func WindowReleased() {
	select {
	case windowReleased <- struct{}{}:
	default:
	}
}

// waitWindowReleased holds onNativeWindowDestroyed until the painter's surface
// is gone: the window is invalid once that callback returns, and a swapchain
// still on it would present into freed memory.
func waitWindowReleased() {
	select {
	case <-windowReleased:
	case <-time.After(2 * time.Second):
		log.Print("app: painter did not release the window in time")
	}
}
