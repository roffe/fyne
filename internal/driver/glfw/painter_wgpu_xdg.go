//go:build wgpu && ((x11 && wayland) || (!x11 && !wayland)) && (linux || freebsd || openbsd || netbsd) && !wasm && !test_web_driver

package glfw

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	"github.com/gogpu/wgpu"

	"fyne.io/fyne/v2/internal/build"
)

// ponytail: x11-only and wayland-only builds lack the other's GLFW natives,
// so wgpu needs the default build, which has both.
func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	if build.IsWayland {
		return wgpu.SurfaceTargetFromWaylandSurface(uintptr(unsafe.Pointer(glfw.GetWaylandDisplay())), uintptr(unsafe.Pointer(v.GetWaylandWindow())))
	}
	return wgpu.SurfaceTargetFromXlibWindow(uintptr(unsafe.Pointer(glfw.GetX11Display())), uintptr(v.GetX11Window()))
}
