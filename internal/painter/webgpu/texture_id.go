//go:build !android && !ios && !mobile

package webgpu

import "fyne.io/fyne/v2/internal/cache"

// textureID turns a number into the texture cache's key type, which is a plain
// integer on desktop and gomobile's gl.Texture on mobile.
func textureID(n uint32) cache.TextureType {
	return cache.TextureType(n)
}
