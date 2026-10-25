//go:build !no_glfw && !mobile

package glfw

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRefreshWindow_DrawsWhileLoopStalled(t *testing.T) {
	w := createWindow("Test")

	var ran, stalled, healthy, notPolling bool
	queue := func() {
		ran = false
		funcQueue.In() <- funcData{f: func() { ran = true }}
		for len(funcQueue.Out()) == 0 { // it arrives through a goroutine
			time.Sleep(time.Millisecond)
		}
	}
	runOnMain(func() {
		defer polling.Store(false)

		polling.Store(true)
		lastTick = time.Now().Add(-time.Second)
		queue()
		refreshWindow(w.window)
		stalled = ran

		polling.Store(true)
		lastTick = time.Now()
		queue()
		refreshWindow(w.window)
		healthy = ran

		polling.Store(false)
		lastTick = time.Now().Add(-time.Second)
		queue()
		refreshWindow(w.window)
		notPolling = ran
	})

	assert.True(t, stalled)
	assert.False(t, healthy)
	assert.False(t, notPolling)
}

// BenchmarkRunOnMain measures the cost of calling a function
// on the main thread.
func BenchmarkRunOnMain(b *testing.B) {
	f := func() {}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runOnMain(f)
	}
}

// BenchmarkRunOnDraw measures the cost of calling a function
// on the draw thread.
func BenchmarkRunOnDraw(b *testing.B) {
	f := func() {}
	w := createWindow("Test")
	w.create()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.RunWithContext(f)
	}
}
