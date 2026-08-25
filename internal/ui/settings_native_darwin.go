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

// workAreaSize returns zero so the caller keeps its built-in budget.
//
// Not yet implemented for macOS: NSScreen.visibleFrame would supply it, but
// the clamp only bites on displays smaller than the content needs, and the
// built-in budget is already safe there.
func workAreaSize() (int, int) {
	return 0, 0
}
