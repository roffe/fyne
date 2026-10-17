//go:build freebsd || linux || darwin || openbsd

package app

import (
	"fyne.io/fyne/v2/internal/driver/mobile/event/lifecycle"
	"fyne.io/fyne/v2/internal/driver/mobile/event/size"
)

func screenOrientation(width, height int) size.Orientation {
	if width > height {
		return size.OrientationLandscape
	}

	return size.OrientationPortrait
}

// drawContext is what a lifecycle event hands the driver to draw with: the GL
// context, or the native window when the painter presents itself.
var drawContext = func(a *app) any { return a.glctx }

func (a *app) sendLifecycle(to lifecycle.Stage) {
	if a.lifecycleStage == to {
		return
	}
	a.events.In() <- lifecycle.Event{
		From:        a.lifecycleStage,
		To:          to,
		DrawContext: drawContext(a),
	}
	a.lifecycleStage = to
}
