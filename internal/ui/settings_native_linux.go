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

// window_visible asks GTK rather than tracking a flag in Go, because the
// window is also hidden by routes that never call hide_window: the WM close
// button goes through on_settings_delete below. A cached bool would desync
// from those and invert the toggle.
static gboolean window_visible(void *win) {
    return gtk_widget_get_visible(GTK_WIDGET(win));
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
    // gtk_settings_get_default() returns NULL until GTK is initialised, and
    // silently yields the 1.0 fallback below - which sizes the window in CSS
    // pixels and undoes the whole correction. Initialising here is safe:
    // gtk_init_check is idempotent, and webview has usually initialised GTK
    // already by the time a window is being sized.
    if (!gtk_init_check(NULL, NULL)) {
        return 1.0;
    }

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

// work_area_size reports the usable screen area in device pixels, excluding
// panels and docks, so a window can be capped to what actually fits rather
// than to a guess about the smallest display anyone might have.
//
// gdk_monitor_get_workarea returns logical units that GTK has already divided
// by the integer HiDPI scale factor, which is the same unit gtk_window_resize
// takes. Multiplying back is therefore wrong here: the caller wants the units
// it will pass to SetSize, not physical pixels.
static void work_area_size(int *width, int *height) {
    *width = 0;
    *height = 0;
    if (!gtk_init_check(NULL, NULL)) {
        return;
    }
    GdkDisplay *display = gdk_display_get_default();
    if (!display) {
        return;
    }
    // The primary monitor is where a new window without a parent is normally
    // placed, so it is the one whose work area bounds it.
    GdkMonitor *monitor = gdk_display_get_primary_monitor(display);
    if (!monitor) {
        monitor = gdk_display_get_monitor(display, 0);
    }
    if (!monitor) {
        return;
    }
    GdkRectangle area;
    gdk_monitor_get_workarea(monitor, &area);
    *width = area.width;
    *height = area.height;
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

// webviewWindowVisible reports whether the settings window is currently mapped.
func webviewWindowVisible(win unsafe.Pointer) bool {
	return C.window_visible(win) != 0
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

// workAreaSize reports the usable screen dimensions in the units SetSize
// takes. Zero means the display could not be queried, and the caller falls
// back to its conservative built-in budget.
func workAreaSize() (int, int) {
	var width, height C.int
	C.work_area_size(&width, &height)
	return int(width), int(height)
}
