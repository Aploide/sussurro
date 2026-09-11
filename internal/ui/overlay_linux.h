#pragma once

#include <gtk/gtk.h>
#include <math.h>
#include <string.h>
#include <stdlib.h>
#include "overlay_state.h"
#include "overlay_palette.h"
#include "overlay_panel.h"

/* gtk-layer-shell is dlopen()'d at runtime, never linked; see the header. */
#include "layer_shell_linux.h"

/* Conditionally include X11 for global hotkeys */
#ifndef WAYLAND_ONLY
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/XKBlib.h>
#endif

/* Panel geometry lives in overlay_panel.h, included above and shared with the
   Cocoa backend. Only the X11 callback and data types stay here. */

/* ---- Callback types ---- */
typedef void (*HotkeyDownCB)(void);
typedef void (*HotkeyUpCB)(void);
typedef void (*MenuOpenSettingsCB)(void);
typedef void (*MenuQuitCB)(void);

/* Opaque overlay data */
typedef struct OverlayData OverlayData;

/* Idle callback argument structs (heap-allocated by Go, freed in C) */
typedef struct {
    GtkWidget *win;
    int        state;
} IdleStateArg;

typedef struct {
    GtkWidget *win;
    float      rms;
} IdleRMSArg;

typedef struct {
    GtkWidget *win;
    double     fill;
} IdleFillArg;

typedef struct {
    GtkWidget      *win;
    int             mode;
    OverlayPalette  dark_palette;
    OverlayPalette  light_palette;
} IdleThemeArg;

/* ---- Public API ---- */

/* Create the overlay window (layer-shell if possible, else always-on-top fallback) */
GtkWidget *overlay_create(const OverlayPalette *dark_palette,
                          const OverlayPalette *light_palette);

/* Install X11 global hotkey bound to the overlay (no-op on Wayland) */
/* Installs the recording bindings. Any may be NULL or empty: push-to-talk and
   edit fire down/up as held and released, while toggle fires once per press. */
void overlay_install_hotkey(GtkWidget *win, const char *push_to_talk,
                            const char *toggle, const char *edit,
                            HotkeyDownCB down_cb, HotkeyUpCB up_cb,
                            HotkeyDownCB toggle_cb, HotkeyDownCB edit_down_cb,
                            HotkeyUpCB edit_up_cb);
/* Queue a binding replacement on the owning GTK main context. */
void overlay_replace_hotkeys_async(GtkWidget *win, const char *push_to_talk,
                                   const char *toggle, const char *edit);

/* Thread-safe async state/RMS updates via gdk_threads_add_idle */
void overlay_set_state_async(GtkWidget *win, int state);
void overlay_push_rms_async(GtkWidget *win, float rms);
void overlay_push_fill_async(GtkWidget *win, double fill);
void overlay_set_theme_async(GtkWidget *win, int mode,
                             const OverlayPalette *dark_palette,
                             const OverlayPalette *light_palette);

/* Idle callbacks (called by GLib event loop, not directly from Go) */
gboolean idle_set_state(gpointer data);
gboolean idle_push_rms(gpointer data);

/* Right-click context menu (fallback for when no system tray is visible) */
void overlay_install_context_menu(GtkWidget *win,
                                  MenuOpenSettingsCB open_settings_cb,
                                  MenuQuitCB quit_cb);

/* Show / hide */
/* Sets the transcript text and status line shown in the expanded panel.
   Either may be NULL or empty. Safe to call from any thread. */
/* Applies state, transcript and status in a single main-thread callback.
   Setting them through separate calls lets the GTK loop draw between the two,
   showing a state that no longer matches the text beside it. */
void overlay_present_async(GtkWidget *win, int state, const char *text,
                           const char *status, int provisional, int copied,
                           int finalizing);

void overlay_show(GtkWidget *win);
void overlay_hide(GtkWidget *win);
