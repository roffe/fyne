//go:build !no_glfw && !mobile

package glfw

import (
	"testing"
	"time"

	"github.com/go-gl/glfw/v3.4/glfw"
	"github.com/stretchr/testify/assert"
)

func TestStallTimer_FiresThroughPollEvents(t *testing.T) {
	var ran bool
	runOnMain(func() {
		defer func() { polling = false }()

		polling, lastTick = true, time.Now().Add(-time.Second)
		funcQueue.In() <- funcData{f: func() { ran = true }}
		for len(funcQueue.Out()) == 0 { // it arrives through a goroutine
			time.Sleep(time.Millisecond)
		}
		time.Sleep(2 * tickPeriod) // the timer comes due
		glfw.PollEvents()
	})

	assert.True(t, ran)
}
