//go:build darwin

package glfw

/*
#import <stdbool.h>

void setDisableDisplaySleep(bool);
double doubleClickInterval();
void startStallTimer(void);
*/
import "C"
import "time"

var stallDriver *gLDriver

// startStallTimer has the main run loop call drawIfStalled sixty times a
// second in every mode, including the one a window drag runs in.
func startStallTimer(d *gLDriver) {
	stallDriver = d
	C.startStallTimer()
}

//export fyneStallTick
func fyneStallTick() {
	drawIfStalled(stallDriver)
}

func setDisableScreenBlank(disable bool) {
	C.setDisableDisplaySleep(C.bool(disable))
}

func (*gLDriver) DoubleTapDelay() time.Duration {
	millis := int64(float64(C.doubleClickInterval()) * 1000)
	return time.Duration(millis) * time.Millisecond
}
