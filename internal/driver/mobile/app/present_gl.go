//go:build !(android && (wgpu || !gl) && (arm64 || amd64))

package app

// glPresents is whether frames reach the screen through the app's GL context:
// Publish swaps the EGL or EAGL buffers. Without it the painter presents itself.
const glPresents = true

func redrawDone() {}

// WindowReleased tells Android that the painter has let go of the native window
// it was destroying. Only a painter that presents itself holds one.
func WindowReleased() {}

func waitWindowReleased() {}
