//go:build darwin

package ui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

// SussurroWindowDelegate hides the window instead of closing it so the
// webview backing store is preserved across open/close cycles.
@interface SussurroWindowDelegate : NSObject <NSWindowDelegate>
@end
@implementation SussurroWindowDelegate
- (BOOL)windowShouldClose:(NSWindow *)sender {
    [sender orderOut:nil];
    return NO;
}
@end

static SussurroWindowDelegate *g_settings_delegate = nil;

static void show_window(void *win) {
    NSWindow *w = (__bridge NSWindow *)win;
    [w makeKeyAndOrderFront:nil];
}
static void hide_window(void *win) {
    NSWindow *w = (__bridge NSWindow *)win;
    [w orderOut:nil];
}
// window_visible asks AppKit rather than tracking a flag in Go: the window is
// also ordered out by the delegate above when the user clicks close, which
// never passes through hide_window.
static int window_visible(void *win) {
    NSWindow *w = (__bridge NSWindow *)win;
    return [w isVisible] ? 1 : 0;
}
static void intercept_close(void *win) {
    NSWindow *w = (__bridge NSWindow *)win;
    if (!g_settings_delegate) {
        g_settings_delegate = [[SussurroWindowDelegate alloc] init];
    }
    [w setDelegate:g_settings_delegate];
}

// work_area_size reports the usable screen area, excluding the menu bar and
// the Dock, so a window can be capped to what actually fits rather than to a
// guess about the smallest display anyone might have.
//
// visibleFrame is in points, which is the same unit NSWindow geometry — and
// therefore webview's SetSize — takes. Retina scaling lives below that and
// must not be multiplied back in here.
static void work_area_size(int *width, int *height) {
    *width = 0;
    *height = 0;
    // The screen carrying the key window is where a new window is placed, so
    // it is the one whose work area bounds it.
    NSScreen *screen = [NSScreen mainScreen];
    if (!screen) {
        NSArray<NSScreen *> *screens = [NSScreen screens];
        if (screens.count == 0) return;
        screen = screens[0];
    }
    NSRect area = [screen visibleFrame];
    *width  = (int)area.size.width;
    *height = (int)area.size.height;
}
*/
import "C"
import "unsafe"

func showWebviewWindow(win unsafe.Pointer) {
	C.show_window(win)
}

func hideWebviewWindow(win unsafe.Pointer) {
	C.hide_window(win)
}

// webviewWindowVisible reports whether the settings window is currently on screen.
func webviewWindowVisible(win unsafe.Pointer) bool {
	return C.window_visible(win) != 0
}

// interceptSettingsClose attaches an NSWindowDelegate that hides the window
// instead of destroying it when the user clicks the close button.
func interceptSettingsClose(win unsafe.Pointer) {
	C.intercept_close(win)
}

// windowScale reports the display's content scaling factor.
//
// macOS sizes windows in points and handles Retina scaling below that, so a
// window sized in points already yields the matching CSS viewport.
func windowScale() float64 {
	return 1.0
}

// workAreaSize reports the usable screen dimensions in the units SetSize
// takes. Zero means the display could not be queried, and the caller falls
// back to its conservative built-in budget.
//
// Returning zero unconditionally was safe but wasteful: the built-in budget
// describes a 1366x768 laptop, so on any larger Mac display the settings
// window was capped well below what fits, and the tallest tabs scrolled for
// no reason.
func workAreaSize() (int, int) {
	var width, height C.int
	C.work_area_size(&width, &height)
	return int(width), int(height)
}
