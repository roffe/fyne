//go:build darwin

package glfw

/*
#import <stdbool.h>

void setDisableDisplaySleep(bool);
double doubleClickInterval();
void startStallTimer(void);
*/
import "C"
import (
	"time"

	"fyne.io/fyne/v2"
)

// startStallTimer has the main run loop call drawIfStalled sixty times a
// second in every mode, including the one a window drag runs in.
func startStallTimer() {
	C.startStallTimer()
}

//export fyneStallTick
func fyneStallTick() {
	if d, ok := fyne.CurrentApp().Driver().(*gLDriver); ok {
		drawIfStalled(d)
	}
}

func setDisableScreenBlank(disable bool) {
	C.setDisableDisplaySleep(C.bool(disable))
}

func (*gLDriver) DoubleTapDelay() time.Duration {
	millis := int64(float64(C.doubleClickInterval()) * 1000)
	return time.Duration(millis) * time.Millisecond
}
