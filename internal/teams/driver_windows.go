//go:build windows

package teams

// Windows driver: UI Automation (UIA) via raw COM vtable calls, so the build
// stays pure Go (no cgo) and cross-compiles from any OS.

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"

	"teamsstatus/internal/config"
)

var (
	clsidCUIAutomation  = ole.NewGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iidIUIAutomation    = ole.NewGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
	iidInvokePattern    = ole.NewGUID("{FB377FBE-8EA6-46D5-9C73-6499642D3059}")
	iidSelectionItem    = ole.NewGUID("{A8EFA66A-0FDA-421A-9194-38021F3578EA}")
	iidExpandCollapse   = ole.NewGUID("{619BE086-1F4E-4EE4-BAFA-210128738730}")
	iidTogglePattern    = ole.NewGUID("{94CF8058-9B8D-4AB9-8BFD-4CD0A33C8C70}")
	user32              = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows     = user32.NewProc("EnumWindows")
	procEnumChild       = user32.NewProc("EnumChildWindows")
	procGetWindowText   = user32.NewProc("GetWindowTextW")
	procGetClassName    = user32.NewProc("GetClassNameW")
	procIsWindowVisible = user32.NewProc("IsWindowVisible")
	procIsIconic        = user32.NewProc("IsIconic")
	procShowWindow      = user32.NewProc("ShowWindow")
	procPostMessage     = user32.NewProc("PostMessageW")
	procGetWindowThread = user32.NewProc("GetWindowThreadProcessId")
)

const (
	uiaNameProperty        = 30005
	uiaControlTypeProperty = 30003
	uiaInvokePattern       = 10000
	uiaSelectionItem       = 10010
	uiaExpandCollapse      = 10005
	uiaTogglePattern       = 10015
	treeScopeDescendants   = 4

	swShowNoActivate    = 4
	swShowMinNoActivate = 7
	swHide              = 0
	wmKeyDown           = 0x0100
	wmKeyUp             = 0x0101
	vkEscape            = 0x1B

	teamsTitle = "Microsoft Teams" // desktop and web tab titles end with "| Microsoft Teams"
)

var controlRoles = map[int32]string{
	50000: "button", 50031: "button", 50011: "menuitem", 50013: "radio",
	50002: "checkbox", 50005: "link", 50007: "listitem",
}

var teamsExes = map[string]bool{"ms-teams.exe": true}
var browserExes = map[string]bool{
	"chrome.exe": true, "msedge.exe": true, "firefox.exe": true, "brave.exe": true,
	"vivaldi.exe": true, "opera.exe": true, "arc.exe": true,
}

// ---- COM helpers ------------------------------------------------------------

// comCall invokes method idx of the COM interface obj.
func comCall(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return r
}

func comRelease(obj unsafe.Pointer) {
	if obj != nil {
		comCall(obj, 2)
	}
}

// Vtable indices (UIAutomationClient.h).
const (
	autoElementFromHandle   = 6
	autoCreateCacheRequest  = 20
	autoCreateTrueCondition = 21
	cacheAddProperty        = 3
	elemFindAllBuildCache   = 8
	elemGetCachedProperty   = 12
	elemGetCurrentPatternAs = 14
	arrayLength             = 3
	arrayGetElement         = 4
	patternAction           = 3 // Invoke / Select / Expand / Toggle
)

// ---- windows ----------------------------------------------------------------

type hwndInfo struct {
	hwnd  windows.HWND
	title string
	exe   string
}

func windowText(h windows.HWND) string {
	buf := make([]uint16, 512)
	procGetWindowText.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), 512)
	return windows.UTF16ToString(buf)
}

func className(h windows.HWND) string {
	buf := make([]uint16, 256)
	procGetClassName.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), 256)
	return windows.UTF16ToString(buf)
}

func exeName(h windows.HWND) string {
	var pid uint32
	procGetWindowThread.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(proc)
	buf := make([]uint16, 1024)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); err != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(windows.UTF16ToString(buf[:size])))
}

func topWindows() []hwndInfo {
	var out []hwndInfo
	cb := windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		if t := windowText(h); t != "" {
			out = append(out, hwndInfo{h, t, ""})
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	for i := range out {
		out[i].exe = exeName(out[i].hwnd)
	}
	return out
}

func findTeamsWindow(target string) (hwndInfo, error) {
	exes := teamsExes
	if target == config.TargetBrowser {
		exes = browserExes
	}
	for _, w := range topWindows() {
		if exes[w.exe] && strings.Contains(strings.ToLower(w.title), strings.ToLower(teamsTitle)) {
			return w, nil
		}
	}
	if target == config.TargetBrowser {
		return hwndInfo{}, errors.New("no browser window is showing Teams (open teams.microsoft.com and keep it the active tab of its window)")
	}
	return hwndInfo{}, errors.New("the Teams desktop app (new Teams) isn't running")
}

// HasPermission: Windows needs no special permission for UI Automation.
func HasPermission(prompt bool) bool { return true }

// Status reports whether the target can be driven right now (cheap; no UI).
func Status(target string) (bool, string) {
	if _, err := findTeamsWindow(target); err != nil {
		return false, err.Error()
	}
	if target == config.TargetBrowser {
		return true, "Teams web in browser"
	}
	return true, "Teams desktop app"
}

// ---- driver -----------------------------------------------------------------

type winDriver struct {
	win        hwndInfo
	auto       unsafe.Pointer // IUIAutomation
	root       unsafe.Pointer // IUIAutomationElement for the window
	cache      unsafe.Pointer // IUIAutomationCacheRequest
	cond       unsafe.Pointer // IUIAutomationCondition (true)
	batches    [][]unsafe.Pointer
	restoreCmd int // ShowWindow command to restore the window state, or -1
}

// Open prepares the Teams window for driving. Close must be called on the same goroutine.
func Open(target string) (Driver, error) {
	runtime.LockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oe, ok := err.(*ole.OleError); !ok || oe.Code() != 1 { // S_FALSE = already initialised
			runtime.UnlockOSThread()
			return nil, fmt.Errorf("COM init: %w", err)
		}
	}
	d := &winDriver{restoreCmd: -1}
	fail := func(err error) (Driver, error) { d.Close(); return nil, err }

	win, err := findTeamsWindow(target)
	if err != nil {
		return fail(err)
	}
	d.win = win

	// Minimised or hidden (Teams "closed" to the tray): show it without activating.
	if v, _, _ := procIsWindowVisible.Call(uintptr(win.hwnd)); v == 0 {
		d.restoreCmd = swHide
	} else if m, _, _ := procIsIconic.Call(uintptr(win.hwnd)); m != 0 {
		d.restoreCmd = swShowMinNoActivate
	}
	if d.restoreCmd >= 0 {
		procShowWindow.Call(uintptr(win.hwnd), swShowNoActivate)
		time.Sleep(1500 * time.Millisecond)
	}

	unk, err := ole.CreateInstance(clsidCUIAutomation, iidIUIAutomation)
	if err != nil {
		return fail(fmt.Errorf("UI Automation unavailable: %w", err))
	}
	d.auto = unsafe.Pointer(unk)
	if hr := comCall(d.auto, autoElementFromHandle, uintptr(win.hwnd), uintptr(unsafe.Pointer(&d.root))); hr != 0 || d.root == nil {
		return fail(fmt.Errorf("UI Automation couldn't attach to the Teams window (0x%x)", hr))
	}
	comCall(d.auto, autoCreateCacheRequest, uintptr(unsafe.Pointer(&d.cache)))
	comCall(d.cache, cacheAddProperty, uiaNameProperty)
	comCall(d.cache, cacheAddProperty, uiaControlTypeProperty)
	comCall(d.auto, autoCreateTrueCondition, uintptr(unsafe.Pointer(&d.cond)))
	if d.cache == nil || d.cond == nil {
		return fail(errors.New("UI Automation setup failed"))
	}

	// Chromium builds its accessibility tree once a client starts asking.
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

func (d *winDriver) cachedProp(el unsafe.Pointer, prop uintptr) ole.VARIANT {
	var v ole.VARIANT
	ole.VariantInit(&v)
	comCall(el, elemGetCachedProperty, prop, uintptr(unsafe.Pointer(&v)))
	return v
}

func (d *winDriver) Elements() []Element {
	var arr unsafe.Pointer
	if hr := comCall(d.root, elemFindAllBuildCache, treeScopeDescendants, uintptr(d.cond), uintptr(d.cache), uintptr(unsafe.Pointer(&arr))); hr != 0 || arr == nil {
		return nil
	}
	defer comRelease(arr)
	var n int32
	comCall(arr, arrayLength, uintptr(unsafe.Pointer(&n)))
	var keep []unsafe.Pointer
	var out []Element
	for i := int32(0); i < n && i < 20000; i++ {
		var el unsafe.Pointer
		if comCall(arr, arrayGetElement, uintptr(i), uintptr(unsafe.Pointer(&el))) != 0 || el == nil {
			continue
		}
		ct := d.cachedProp(el, uiaControlTypeProperty)
		role := controlRoles[int32(ct.Val)]
		ole.VariantClear(&ct)
		if role == "" {
			comRelease(el)
			continue
		}
		nv := d.cachedProp(el, uiaNameProperty)
		name := ""
		if nv.VT == ole.VT_BSTR {
			name = strings.Join(strings.Fields(nv.ToString()), " ")
		}
		ole.VariantClear(&nv)
		keep = append(keep, el)
		out = append(out, Element{Role: role, Name: name, Native: el})
	}
	// Keep the previous batch alive: callers may still press an element from it.
	d.batches = append(d.batches, keep)
	for len(d.batches) > 2 {
		for _, el := range d.batches[0] {
			comRelease(el)
		}
		d.batches = d.batches[1:]
	}
	return out
}

func (d *winDriver) Press(e Element) error {
	el := e.Native.(unsafe.Pointer)
	for _, p := range []struct {
		id  uintptr
		iid *ole.GUID
	}{
		{uiaInvokePattern, iidInvokePattern},
		{uiaSelectionItem, iidSelectionItem},
		{uiaTogglePattern, iidTogglePattern},
		{uiaExpandCollapse, iidExpandCollapse},
	} {
		var pat unsafe.Pointer
		if comCall(el, elemGetCurrentPatternAs, p.id, uintptr(unsafe.Pointer(p.iid)), uintptr(unsafe.Pointer(&pat))) != 0 || pat == nil {
			continue
		}
		hr := comCall(pat, patternAction)
		comRelease(pat)
		if hr == 0 {
			time.Sleep(600 * time.Millisecond)
			return nil
		}
	}
	return fmt.Errorf("couldn't press %q (no supported UI Automation pattern)", e.Name)
}

// renderWidget finds Chromium's input window inside the Teams window.
func renderWidget(top windows.HWND) windows.HWND {
	var found windows.HWND
	cb := windows.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		if className(h) == "Chrome_RenderWidgetHostHWND" {
			found = h
			return 0
		}
		return 1
	})
	procEnumChild.Call(uintptr(top), cb, 0)
	if found == 0 {
		return top
	}
	return found
}

func (d *winDriver) Dismiss() {
	h := renderWidget(d.win.hwnd)
	procPostMessage.Call(uintptr(h), wmKeyDown, vkEscape, 0x00010001)
	procPostMessage.Call(uintptr(h), wmKeyUp, vkEscape, 0xC0010001)
}

func (d *winDriver) Close() {
	for _, b := range d.batches {
		for _, el := range b {
			comRelease(el)
		}
	}
	d.batches = nil
	comRelease(d.cond)
	comRelease(d.cache)
	comRelease(d.root)
	comRelease(d.auto)
	d.cond, d.cache, d.root, d.auto = nil, nil, nil, nil
	if d.restoreCmd >= 0 && d.win.hwnd != 0 {
		procShowWindow.Call(uintptr(d.win.hwnd), uintptr(d.restoreCmd))
		d.restoreCmd = -1
	}
	ole.CoUninitialize()
	runtime.UnlockOSThread()
}
