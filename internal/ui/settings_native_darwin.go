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
