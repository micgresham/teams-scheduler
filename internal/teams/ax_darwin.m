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

// Ask Chromium-based apps to build their web accessibility tree.
void tss_enable_ax(int pid) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	AXUIElementSetAttributeValue(app, CFSTR("AXManualAccessibility"), kCFBooleanTrue);
	CFRelease(app);
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
		CFArrayRef wins = copyWindows(pid, filter);
		if (!wins) return 0;
		CFMutableArrayRef queue = CFArrayCreateMutableCopy(NULL, 0, wins);
		CFRelease(wins);
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
