//go:build darwin

package keepawake

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/pwr_mgt/IOPMLib.h>

static IOPMAssertionID tss_assert(CFStringRef type) {
	IOPMAssertionID id = 0;
	IOPMAssertionCreateWithName(type, kIOPMAssertionLevelOn, CFSTR("Teams Status Scheduler: keep awake"), &id);
	return id;
}
static IOPMAssertionID tss_idle_sleep(void)   { return tss_assert(kIOPMAssertionTypePreventUserIdleSystemSleep); }
static IOPMAssertionID tss_system_sleep(void) { return tss_assert(kIOPMAssertionTypePreventSystemSleep); }
static IOPMAssertionID tss_display(void)      { return tss_assert(kIOPMAssertionTypePreventUserIdleDisplaySleep); }
static void tss_release(IOPMAssertionID id)   { if (id) IOPMAssertionRelease(id); }
*/
import "C"

// macOS: IOKit power assertions (what `caffeinate -i -s [-d]` uses). They
// are released automatically if the process exits. The Mac still sleeps
// when a laptop lid is closed unless it's in clamshell mode.
type darwin struct{ ids []C.IOPMAssertionID }

func newPlatform() platform { return &darwin{} }

func (d *darwin) set(active, display bool) error {
	for _, id := range d.ids {
		C.tss_release(id)
	}
	d.ids = nil
	if active {
		d.ids = append(d.ids, C.tss_idle_sleep(), C.tss_system_sleep())
		if display {
			d.ids = append(d.ids, C.tss_display())
		}
	}
	return nil
}
