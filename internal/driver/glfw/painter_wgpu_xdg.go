//go:build wgpu && ((x11 && wayland) || (!x11 && !wayland)) && (linux || freebsd || openbsd || netbsd) && !wasm && !test_web_driver

package glfw

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	wgpu "github.com/roffe/gowgpu"

	"fyne.io/fyne/v2/internal/build"
)

// surfaceTarget picks the window system GLFW connected to. x11-only and
// wayland-only builds lack the other's GLFW natives, see painter_wgpu_x11.go
// and painter_wgpu_wayland.go.
func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	if build.IsWayland {
		return wgpu.SurfaceTargetFromWaylandSurface(uintptr(unsafe.Pointer(glfw.GetWaylandDisplay())), uintptr(unsafe.Pointer(v.GetWaylandWindow())))
	}
	return wgpu.SurfaceTargetFromXlibWindow(uintptr(unsafe.Pointer(glfw.GetX11Display())), uintptr(v.GetX11Window()))
}
