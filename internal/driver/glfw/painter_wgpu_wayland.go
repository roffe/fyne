//go:build wgpu && wayland && !x11 && (linux || freebsd || openbsd || netbsd)

package glfw

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	wgpu "github.com/roffe/gowgpu"
)

func surfaceTarget(v *glfw.Window) wgpu.SurfaceTargetUnsafe {
	return wgpu.SurfaceTargetFromWaylandSurface(uintptr(unsafe.Pointer(glfw.GetWaylandDisplay())), uintptr(unsafe.Pointer(v.GetWaylandWindow())))
}
