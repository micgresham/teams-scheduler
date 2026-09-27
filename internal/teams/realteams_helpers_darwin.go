//go:build darwin && realteams

package teams

/*
#include "ax_darwin.h"
*/
import "C"

import "time"

// Test helpers for the live Teams test (cgo isn't allowed in _test.go files).

func teamsPid() C.int { return C.int(pidFor(teamsBundleID)) }

func minimizeTeams() {
	hidden := C.tss_is_hidden(teamsPid()) != 0
	C.tss_set_hidden(teamsPid(), 0)
	time.Sleep(500 * time.Millisecond)
	defer func() {
		if hidden {
			C.tss_set_hidden(teamsPid(), 1)
			time.Sleep(time.Second)
		}
	}()
	w := C.tss_first_window(teamsPid(), nil)
	C.tss_set_minimized(w, 1)
	C.tss_release(w)
	time.Sleep(1500 * time.Millisecond)
}

func hideTeams() {
	w := C.tss_first_window(teamsPid(), nil)
	C.tss_set_minimized(w, 0)
	C.tss_release(w)
	time.Sleep(time.Second)
	C.tss_set_hidden(teamsPid(), 1)
	time.Sleep(1500 * time.Millisecond)
}

func showTeams() {
	C.tss_set_hidden(teamsPid(), 0)
	w := C.tss_first_window(teamsPid(), nil)
	C.tss_set_minimized(w, 0)
	C.tss_release(w)
	time.Sleep(time.Second)
}

func teamsState() (hidden, minimized bool, front int) {
	return C.tss_is_hidden(teamsPid()) != 0, C.tss_all_minimized(teamsPid(), nil) != 0, int(C.tss_frontmost_pid())
}
