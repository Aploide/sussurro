//go:build linux

package ui

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

static void show_window(void *win) {
    gtk_widget_show_all(GTK_WIDGET(win));
    gtk_window_present(GTK_WINDOW(win));
}
static void hide_window(void *win) {
    gtk_widget_hide(GTK_WIDGET(win));
}

// Intercept the WM "X" close button: hide instead of destroy.
// Returning TRUE suppresses the default action (gtk_widget_destroy),
// keeping the window alive so it can be shown again later.
static gboolean on_settings_delete(GtkWidget *win, GdkEvent *ev, gpointer data) {
    (void)ev; (void)data;
    gtk_widget_hide(win);
    return TRUE;
}
static void setup_settings_hide_on_close(void *win) {
    g_signal_connect(GTK_WIDGET(win), "delete-event",
                     G_CALLBACK(on_settings_delete), NULL);
}

// window_scale returns the factor by which the display magnifies CSS pixels,
// so a caller can size a window in content terms rather than device pixels.
//
// Two independent mechanisms scale a GTK window, and they multiply:
//
//   - gdk_monitor_get_scale_factor: integer HiDPI scaling (1, 2, ...), which
//     GTK already applies to the size passed to gtk_window_resize.
//   - gtk-xft-dpi: fractional font DPI (Xft.dpi, GNOME text-scaling-factor),
//     in 1024ths of a point. GTK does NOT apply this to window geometry, but
//     WebKit does apply it to page content, which is why a window sized in
//     device pixels yields a smaller CSS viewport than expected.
//
// Only the second needs correcting here; the first is already handled. It is
// returned separately so the caller can divide it back out.
static double window_scale(void) {
    GtkSettings *settings = gtk_settings_get_default();
    if (!settings) {
        return 1.0;
    }
    gint xft_dpi = 0;
    g_object_get(settings, "gtk-xft-dpi", &xft_dpi, NULL);
    if (xft_dpi <= 0) {
        return 1.0;
    }
    // gtk-xft-dpi is in 1024ths; 96 dpi is the unscaled baseline.
    return ((double)xft_dpi / 1024.0) / 96.0;
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

// interceptSettingsClose ensures the WM close button hides the window
// rather than destroying it, so it can be reopened.
func interceptSettingsClose(win unsafe.Pointer) {
	C.setup_settings_hide_on_close(win)
}

// windowScale reports the display's fractional content scaling. See the
// window_scale C comment for why integer HiDPI scaling is excluded.
func windowScale() float64 {
	return float64(C.window_scale())
}
