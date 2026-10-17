//go:build (wgpu || !gl) && x11 && !wayland && (linux || freebsd || openbsd || netbsd) && !wasm && !test_web_driver

package glfw

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	wgpu "github.com/roffe/gowgpu"
)

func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	return wgpu.SurfaceTargetFromXlibWindow(uintptr(unsafe.Pointer(glfw.GetX11Display())), uintptr(v.GetX11Window())) //nolint:gosec // G103: native handles for the surface
}
