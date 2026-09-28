// macOS Accessibility helpers used by driver_darwin.go.
#import <AppKit/AppKit.h>
#include "ax_darwin.h"

int tss_trusted(int prompt) {
	if (!prompt) return AXIsProcessTrusted();
	NSDictionary *opts = @{(__bridge NSString *)kAXTrustedCheckOptionPrompt: @YES};
	return AXIsProcessTrustedWithOptions((__bridge CFDictionaryRef)opts);
}

static NSRunningApplication *appFor(int pid) {
	return [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
}

int tss_pid_for_bundle(const char *bundleID) {
	@autoreleasepool {
		NSArray *apps = [NSRunningApplication runningApplicationsWithBundleIdentifier:[NSString stringWithUTF8String:bundleID]];
		return apps.count ? [apps[0] processIdentifier] : 0;
	}
}

// Hidden state via Accessibility (like System Events). NSRunningApplication's
// -hide is unreliable from a background process and -isHidden can be stale.
int tss_is_hidden(int pid) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	CFTypeRef v = NULL;
	int hidden = AXUIElementCopyAttributeValue(app, kAXHiddenAttribute, &v) == kAXErrorSuccess && v &&
	             CFGetTypeID(v) == CFBooleanGetTypeID() && CFBooleanGetValue(v);
	if (v) CFRelease(v);
	CFRelease(app);
	return hidden;
}

void tss_set_hidden(int pid, int hidden) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	AXUIElementSetAttributeValue(app, kAXHiddenAttribute, hidden ? kCFBooleanTrue : kCFBooleanFalse);
	CFRelease(app);
}

int tss_frontmost_pid(void) {
	@autoreleasepool { return [[[NSWorkspace sharedWorkspace] frontmostApplication] processIdentifier]; }
}

void tss_activate(int pid) {
	@autoreleasepool { [appFor(pid) activateWithOptions:0]; }
}

static CFTypeRef copyAttr(AXUIElementRef el, CFStringRef name) {
	CFTypeRef value = NULL;
	if (AXUIElementCopyAttributeValue(el, name, &value) != kAXErrorSuccess) return NULL;
	return value;
}

static int titleMatches(AXUIElementRef win, const char *filter) {
	if (!filter || !filter[0]) return 1;
	CFTypeRef t = copyAttr(win, kAXTitleAttribute);
	int ok = 0;
	if (t && CFGetTypeID(t) == CFStringGetTypeID()) {
		NSString *title = (__bridge NSString *)t;
		ok = [title rangeOfString:[NSString stringWithUTF8String:filter] options:NSCaseInsensitiveSearch].location != NSNotFound;
	}
	if (t) CFRelease(t);
	return ok;
}

// Returns a retained array of matching windows (or NULL).
static CFArrayRef copyWindows(int pid, const char *filter) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	CFTypeRef wins = copyAttr(app, kAXWindowsAttribute);
	CFRelease(app);
	if (!wins) return NULL;
	if (CFGetTypeID(wins) != CFArrayGetTypeID()) { CFRelease(wins); return NULL; }
	CFMutableArrayRef out = CFArrayCreateMutable(NULL, 0, &kCFTypeArrayCallBacks);
	for (CFIndex i = 0; i < CFArrayGetCount(wins); i++) {
		AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex(wins, i);
		if (titleMatches(w, filter)) CFArrayAppendValue(out, w);
	}
	CFRelease(wins);
	return out;
}

// Roots to scan: the app's windows plus any other top-level children (pop-up
// panels some Teams builds use for menus). With a title
// filter (browser mode) only matching windows are used.
static CFArrayRef copyRoots(int pid, const char *filter) {
	CFMutableArrayRef out = CFArrayCreateMutable(NULL, 0, &kCFTypeArrayCallBacks);
	CFArrayRef wins = copyWindows(pid, filter);
	if (wins) { CFArrayAppendArray(out, wins, CFRangeMake(0, CFArrayGetCount(wins))); CFRelease(wins); }
	if (filter && filter[0]) return out;
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	CFTypeRef kids = copyAttr(app, kAXChildrenAttribute);
	CFRelease(app);
	if (kids && CFGetTypeID(kids) == CFArrayGetTypeID()) {
		for (CFIndex i = 0; i < CFArrayGetCount(kids); i++) {
			CFTypeRef k = CFArrayGetValueAtIndex(kids, i);
			CFTypeRef role = copyAttr((AXUIElementRef)k, kAXRoleAttribute);
			// Only window-like roots (pop-up panels); not the menu bar, and not the
			// nested AXApplication element Teams' WebView exposes.
			int windowLike = role && CFGetTypeID(role) == CFStringGetTypeID() &&
				(CFEqual(role, kAXWindowRole) || CFEqual(role, kAXSheetRole) || CFEqual(role, kAXDrawerRole) ||
				 CFEqual(role, CFSTR("AXPopover")));
			if (role) CFRelease(role);
			if (windowLike && !CFArrayContainsValue(out, CFRangeMake(0, CFArrayGetCount(out)), k)) CFArrayAppendValue(out, k);
		}
	}
	if (kids) CFRelease(kids);
	return out;
}

// One line per scan root: role/subrole and title (diagnostics).
int tss_describe_roots(int pid, char *buf, int buflen) {
	buf[0] = 0;
	CFArrayRef roots = copyRoots(pid, NULL);
	NSMutableArray *lines = [NSMutableArray array];
	for (CFIndex i = 0; i < CFArrayGetCount(roots); i++) {
		AXUIElementRef r = (AXUIElementRef)CFArrayGetValueAtIndex(roots, i);
		NSString *role = (__bridge_transfer NSString *)copyAttr(r, kAXRoleAttribute);
		NSString *sub = (__bridge_transfer NSString *)copyAttr(r, kAXSubroleAttribute);
		CFTypeRef t = copyAttr(r, kAXTitleAttribute);
		NSString *title = (t && CFGetTypeID(t) == CFStringGetTypeID()) ? (__bridge NSString *)t : @"";
		[lines addObject:[NSString stringWithFormat:@"%@/%@ \"%@\"", role ?: @"?", sub ?: @"-", title]];
		if (t) CFRelease(t);
	}
	CFRelease(roots);
	strlcpy(buf, [[lines componentsJoinedByString:@"\n"] UTF8String], buflen);
	return (int)lines.count;
}

// Alternative ways to activate a control when AXPress has no visible effect.
int tss_focus_and_return(int pid, AXUIElementRef ref) {
	AXUIElementSetAttributeValue(ref, kAXFocusedAttribute, kCFBooleanTrue);
	usleep(150000);
	for (int down = 1; down >= 0; down--) {
		CGEventRef ev = CGEventCreateKeyboardEvent(NULL, 36 /* Return */, down);
		CGEventPostToPid(pid, ev);
		CFRelease(ev);
	}
	return 0;
}

int tss_click(int pid, AXUIElementRef ref) {
	CFTypeRef posv = copyAttr(ref, kAXPositionAttribute), sizev = copyAttr(ref, kAXSizeAttribute);
	CGPoint pos; CGSize size;
	int ok = posv && sizev && AXValueGetValue(posv, kAXValueCGPointType, &pos) && AXValueGetValue(sizev, kAXValueCGSizeType, &size);
	if (posv) CFRelease(posv);
	if (sizev) CFRelease(sizev);
	if (!ok) return -1;
	CGPoint c = CGPointMake(pos.x + size.width / 2, pos.y + size.height / 2);
	CGEventType types[2] = {kCGEventLeftMouseDown, kCGEventLeftMouseUp};
	for (int i = 0; i < 2; i++) {
		CGEventRef ev = CGEventCreateMouseEvent(NULL, types[i], c, kCGMouseButtonLeft);
		CGEventSetIntegerValueField(ev, kCGMouseEventClickState, 1);
		CGEventPostToPid(pid, ev);
		CFRelease(ev);
		usleep(60000);
	}
	return 0;
}

static int isMinimized(AXUIElementRef w) {
	CFTypeRef v = copyAttr(w, kAXMinimizedAttribute);
	int m = v && CFGetTypeID(v) == CFBooleanGetTypeID() && CFBooleanGetValue(v);
	if (v) CFRelease(v);
	return m;
}

int tss_window_count(int pid, const char *filter) {
	CFArrayRef w = copyWindows(pid, filter);
	int n = w ? (int)CFArrayGetCount(w) : 0;
	if (w) CFRelease(w);
	return n;
}

int tss_all_minimized(int pid, const char *filter) {
	CFArrayRef w = copyWindows(pid, filter);
	if (!w) return 0;
	int all = CFArrayGetCount(w) > 0;
	for (CFIndex i = 0; i < CFArrayGetCount(w); i++)
		if (!isMinimized((AXUIElementRef)CFArrayGetValueAtIndex(w, i))) all = 0;
	CFRelease(w);
	return all;
}

AXUIElementRef tss_first_window(int pid, const char *filter) {
	CFArrayRef w = copyWindows(pid, filter);
	AXUIElementRef first = NULL;
	if (w && CFArrayGetCount(w) > 0) first = (AXUIElementRef)CFRetain(CFArrayGetValueAtIndex(w, 0));
	if (w) CFRelease(w);
	return first;
}

void tss_set_minimized(AXUIElementRef window, int minimized) {
	AXUIElementSetAttributeValue(window, kAXMinimizedAttribute, minimized ? kCFBooleanTrue : kCFBooleanFalse);
}

// Make a (non-minimised) matching window the app's main/key window without
// activating the app: keyboard events posted to the app (Escape) are dropped
// when none of its windows is key.
void tss_make_main(int pid, const char *filter) {
	CFArrayRef w = copyWindows(pid, filter);
	if (!w) return;
	for (CFIndex i = 0; i < CFArrayGetCount(w); i++) {
		AXUIElementRef win = (AXUIElementRef)CFArrayGetValueAtIndex(w, i);
		if (!isMinimized(win)) {
			AXUIElementSetAttributeValue(win, kAXMainAttribute, kCFBooleanTrue);
			break;
		}
	}
	CFRelease(w);
}

int tss_window_titles(int pid, char *buf, int buflen) {
	buf[0] = 0;
	CFArrayRef w = copyWindows(pid, NULL);
	if (!w) return 0;
	NSMutableArray *titles = [NSMutableArray array];
	for (CFIndex i = 0; i < CFArrayGetCount(w); i++) {
		CFTypeRef t = copyAttr((AXUIElementRef)CFArrayGetValueAtIndex(w, i), kAXTitleAttribute);
		if (t && CFGetTypeID(t) == CFStringGetTypeID()) [titles addObject:(__bridge NSString *)t];
		if (t) CFRelease(t);
	}
	CFRelease(w);
	strlcpy(buf, [[titles componentsJoinedByString:@"\n"] UTF8String], buflen);
	return (int)titles.count;
}

// Ask Chromium-based apps to build their web accessibility tree. Teams'
// WebView ignores AXManualAccessibility (the Chromium/Electron switch) but
// honours AXEnhancedUserInterface (the VoiceOver one). Returns the previous
// AXEnhancedUserInterface value.
int tss_enable_ax(int pid) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	CFTypeRef prev = copyAttr(app, CFSTR("AXEnhancedUserInterface"));
	int was = prev && CFGetTypeID(prev) == CFBooleanGetTypeID() && CFBooleanGetValue(prev);
	if (prev) CFRelease(prev);
	AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), kCFBooleanTrue);
	AXUIElementSetAttributeValue(app, CFSTR("AXEnhancedUserInterface"), kCFBooleanTrue);
	CFRelease(app);
	return was;
}


static int roleCode(CFTypeRef role) {
	if (!role || CFGetTypeID(role) != CFStringGetTypeID()) return 0;
	NSString *r = (__bridge NSString *)role;
	if ([r isEqualToString:@"AXButton"] || [r isEqualToString:@"AXPopUpButton"] || [r isEqualToString:@"AXMenuButton"]) return TSS_BUTTON;
	if ([r isEqualToString:@"AXMenuItem"]) return TSS_MENUITEM;
	if ([r isEqualToString:@"AXRadioButton"]) return TSS_RADIO;
	if ([r isEqualToString:@"AXCheckBox"]) return TSS_CHECKBOX;
	if ([r isEqualToString:@"AXLink"]) return TSS_LINK;
	if ([r isEqualToString:@"AXCell"] || [r isEqualToString:@"AXRow"]) return TSS_LISTITEM;
	return 0;
}

static int isString(CFTypeRef v) { return v && CFGetTypeID(v) == CFStringGetTypeID() && CFStringGetLength(v) > 0; }

// Breadth-first walk of the matching windows, collecting actionable controls.
int tss_collect(int pid, const char *filter, TSSItem *out, int maxOut, int maxNodes, double maxSeconds) {
	@autoreleasepool {
		CFArrayRef roots = copyRoots(pid, filter);
		CFMutableArrayRef queue = CFArrayCreateMutableCopy(NULL, 0, roots);
		CFRelease(roots);
		CFArrayRef attrs = (__bridge CFArrayRef)@[@"AXRole", @"AXTitle", @"AXDescription", @"AXValue", @"AXChildren"];
		CFAbsoluteTime deadline = CFAbsoluteTimeGetCurrent() + maxSeconds;
		int found = 0, seen = 0;
		CFIndex head = 0;
		while (head < CFArrayGetCount(queue) && seen < maxNodes && found < maxOut && CFAbsoluteTimeGetCurrent() < deadline) {
			AXUIElementRef node = (AXUIElementRef)CFArrayGetValueAtIndex(queue, head++);
			seen++;
			CFArrayRef values = NULL;
			if (AXUIElementCopyMultipleAttributeValues(node, attrs, 0, &values) != kAXErrorSuccess || !values) continue;
			CFTypeRef role = CFArrayGetValueAtIndex(values, 0);
			int code = roleCode(role);
			if (code) {
				CFTypeRef name = NULL;
				for (int i = 1; i <= 3 && !name; i++) {
					CFTypeRef v = CFArrayGetValueAtIndex(values, i);
					if (isString(v)) name = v;
				}
				out[found].ref = (AXUIElementRef)CFRetain(node);
				out[found].role = code;
				// Separate call: asking for AXHasPopup in the batch above makes the
				// whole batch fail on some nodes.
				CFTypeRef popup = copyAttr(node, CFSTR("AXHasPopup"));
				out[found].hasPopup = popup && CFGetTypeID(popup) == CFBooleanGetTypeID() && CFBooleanGetValue(popup);
				if (popup) CFRelease(popup);
				out[found].name[0] = 0;
				if (name) CFStringGetCString(name, out[found].name, sizeof(out[found].name), kCFStringEncodingUTF8);
				found++;
			}
			CFTypeRef kids = CFArrayGetValueAtIndex(values, 4);
			if (kids && CFGetTypeID(kids) == CFArrayGetTypeID())
				CFArrayAppendArray(queue, kids, CFRangeMake(0, CFArrayGetCount(kids)));
			CFRelease(values);
		}
		CFRelease(queue);
		return found;
	}
}

int tss_press(AXUIElementRef ref) { return AXUIElementPerformAction(ref, kAXPressAction); }

void tss_release(AXUIElementRef ref) { if (ref) CFRelease(ref); }

void tss_post_escape(int pid) {
	for (int down = 1; down >= 0; down--) {
		CGEventRef ev = CGEventCreateKeyboardEvent(NULL, 53, down);
		CGEventPostToPid(pid, ev);
		CFRelease(ev);
	}
}
