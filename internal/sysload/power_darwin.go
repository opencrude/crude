//go:build cgo

package sysload

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/ps/IOPowerSources.h>

static int crude_power_state(void) {
	CFTypeRef snapshot = IOPSCopyPowerSourcesInfo();
	if (snapshot == NULL) return -1;
	CFStringRef source = IOPSGetProvidingPowerSourceType(snapshot);
	int state = -1;
	if (source != NULL) {
		if (CFEqual(source, CFSTR(kIOPMACPowerKey))) state = 0;
		else if (CFEqual(source, CFSTR(kIOPMBatteryPowerKey))) state = 1;
	}
	CFRelease(snapshot);
	return state;
}
*/
import "C"

// Read the source powering the computer, not the presence of a battery in a
// peripheral. IOKit avoids spawning a subprocess on each sample.
func powerState() (known, onBattery bool) {
	state := C.crude_power_state()
	return state >= 0, state == 1
}
