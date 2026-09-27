//go:build darwin

package teams

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices
#include <stdlib.h>
#include "ax_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"teamsstatus/internal/config"
)

const (
	teamsBundleID = "com.microsoft.teams2"
	maxItems      = 600
	maxNodes      = 20000
	maxSeconds    = 6.0
	browserFilter = "Microsoft Teams" // Teams web tab titles end with "| Microsoft Teams"
)

var browserBundleIDs = []string{
	"com.google.Chrome", "com.microsoft.edgemac", "com.apple.Safari", "org.mozilla.firefox",
	"company.thebrowser.Browser", "com.brave.Browser", "com.vivaldi.Vivaldi", "com.operasoftware.Opera",
}

var roleNames = map[C.int]string{
	C.TSS_BUTTON: "button", C.TSS_MENUITEM: "menuitem", C.TSS_RADIO: "radio",
	C.TSS_CHECKBOX: "checkbox", C.TSS_LINK: "link", C.TSS_LISTITEM: "listitem",
}

// HasPermission reports whether this app may use Accessibility; prompt=true
// shows the system dialog that points the user to System Settings.
func HasPermission(prompt bool) bool {
	p := 0
	if prompt {
		p = 1
	}
	return C.tss_trusted(C.int(p)) != 0
}

func pidFor(bundleID string) int {
	cs := C.CString(bundleID)
	defer C.free(unsafe.Pointer(cs))
	return int(C.tss_pid_for_bundle(cs))
}

func windowTitles(pid int) string {
	buf := (*C.char)(C.malloc(8192))
	defer C.free(unsafe.Pointer(buf))
	C.tss_window_titles(C.int(pid), buf, 8192)
	return C.GoString(buf)
}

// locate finds the process (and window-title filter) for the target.
func locate(target string) (pid int, filter string, err error) {
	if target == config.TargetBrowser {
		for _, id := range browserBundleIDs {
			if p := pidFor(id); p != 0 && strings.Contains(strings.ToLower(windowTitles(p)), strings.ToLower(browserFilter)) {
				return p, browserFilter, nil
			}
		}
		return 0, "", errors.New("no browser window is showing Teams (open teams.microsoft.com and keep it the active tab of its window)")
	}
	if p := pidFor(teamsBundleID); p != 0 {
		return p, "", nil
	}
	return 0, "", errors.New("the Teams desktop app (new Teams) isn't running")
}

// Status reports whether the target can be driven right now (cheap; no UI).
func Status(target string) (bool, string) {
	if !HasPermission(false) {
		return false, "needs Accessibility permission (if it already looks switched on, remove the entry with – and add the app again)"
	}
	if _, _, err := locate(target); err != nil {
		return false, err.Error()
	}
	if target == config.TargetBrowser {
		return true, "Teams web in browser"
	}
	return true, "Teams desktop app"
}

// batch is one C array of collected controls (refs retained until freed).
type batch struct {
	items unsafe.Pointer
	n     int
}

type macDriver struct {
	pid           C.int
	filter        *C.char
	batches       []batch
	restoreHidden bool
	reminimize    bool // re-minimise Teams' window on Close
	minimized     C.AXUIElementRef
	prevFront     int
}

// Open prepares the target Teams window for driving. Close must be called.
func Open(target string) (Driver, error) {
	if !HasPermission(false) {
		HasPermission(true)
		return nil, errors.New("Accessibility permission is needed: allow “Teams Status Scheduler” in System Settings → Privacy & Security → Accessibility")
	}
	pid, filter, err := locate(target)
	if err != nil {
		return nil, err
	}
	d := &macDriver{pid: C.int(pid), prevFront: int(C.tss_frontmost_pid())}
	if filter != "" {
		d.filter = C.CString(filter)
	}
	C.tss_enable_ax(d.pid)

	if C.tss_is_hidden(d.pid) != 0 {
		// Menus don't open in a hidden app: unhide (without activating), re-hide after.
		C.tss_set_hidden(d.pid, 0)
		d.restoreHidden = true
		time.Sleep(time.Second)
	}
	count := C.tss_window_count(d.pid, d.filter)
	allMinimized := count > 0 && C.tss_all_minimized(d.pid, d.filter) != 0
	switch {
	case target != config.TargetBrowser && (count == 0 || allMinimized):
		// Window closed or minimised: a background "reopen" brings it back without
		// activating Teams. (Un-minimising via Accessibility is unreliable: Teams'
		// minimised windows sometimes drop out of the accessibility window list.)
		_ = exec.Command("open", "-g", "-b", teamsBundleID).Run()
		if count == 0 {
			d.restoreHidden = true
		} else {
			d.reminimize = true
		}
		deadline := time.Now().Add(15 * time.Second)
		for C.tss_window_count(d.pid, d.filter) == 0 || C.tss_all_minimized(d.pid, d.filter) != 0 {
			if time.Now().After(deadline) {
				d.Close()
				return nil, errors.New("couldn't bring up a Teams window")
			}
			time.Sleep(500 * time.Millisecond)
		}
		time.Sleep(time.Second)
	case count == 0:
		d.Close()
		return nil, errors.New("the browser window showing Teams is closed")
	case allMinimized:
		// Browser: un-minimise the Teams window itself (a reopen could open a new window).
		d.minimized = C.tss_first_window(d.pid, d.filter)
		C.tss_set_minimized(d.minimized, 0)
		time.Sleep(time.Second)
	}
	C.tss_make_main(d.pid, d.filter)

	// Chromium builds its web accessibility tree only once a client starts
	// asking, so the first reads may show just the window frame.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		named := 0
		for _, e := range d.Elements() {
			if e.Name != "" {
				named++
			}
		}
		if named >= 3 {
			break
		}
		time.Sleep(400 * time.Millisecond)
	}
	return d, nil
}

func (d *macDriver) Elements() []Element {
	items := (*[maxItems]C.TSSItem)(C.malloc(C.size_t(maxItems) * C.size_t(unsafe.Sizeof(C.TSSItem{}))))
	n := int(C.tss_collect(d.pid, d.filter, &items[0], maxItems, maxNodes, maxSeconds))
	// Keep the previous batch alive: callers may still press an element from it.
	d.batches = append(d.batches, batch{unsafe.Pointer(items), n})
	for len(d.batches) > 2 {
		freeBatch(d.batches[0])
		d.batches = d.batches[1:]
	}
	out := make([]Element, 0, n)
	for i := 0; i < n; i++ {
		name := strings.Join(strings.Fields(C.GoString(&items[i].name[0])), " ")
		out = append(out, Element{Role: roleNames[items[i].role], Name: name, Native: items[i].ref})
	}
	return out
}

func freeBatch(b batch) {
	items := (*[maxItems]C.TSSItem)(b.items)
	for i := 0; i < b.n; i++ {
		C.tss_release(items[i].ref)
	}
	C.free(b.items)
}

func (d *macDriver) Press(e Element) error {
	if code := C.tss_press(e.Native.(C.AXUIElementRef)); code != 0 {
		return fmt.Errorf("couldn't press %q (AX error %d)", e.Name, int(code))
	}
	time.Sleep(600 * time.Millisecond)
	return nil
}

func (d *macDriver) Dismiss() {
	C.tss_make_main(d.pid, d.filter)
	C.tss_post_escape(d.pid)
}

func (d *macDriver) Close() {
	for _, b := range d.batches {
		freeBatch(b)
	}
	d.batches = nil
	if d.minimized != 0 {
		C.tss_set_minimized(d.minimized, 1)
		C.tss_release(d.minimized)
		d.minimized = 0
	}
	if d.reminimize && !d.restoreHidden {
		if w := C.tss_first_window(d.pid, d.filter); w != 0 {
			C.tss_set_minimized(w, 1)
			C.tss_release(w)
		}
	}
	if d.restoreHidden {
		C.tss_set_hidden(d.pid, 1)
	}
	if d.prevFront != 0 && d.prevFront != int(d.pid) {
		C.tss_activate(C.int(d.prevFront))
	}
	if d.filter != nil {
		C.free(unsafe.Pointer(d.filter))
		d.filter = nil
	}
}
