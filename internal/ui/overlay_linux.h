#pragma once

#include <gtk/gtk.h>
#include <math.h>
#include <string.h>
#include <stdlib.h>
#include "overlay_state.h"

/* Conditionally include gtk-layer-shell */
#ifdef HAVE_GTK_LAYER_SHELL
#include <gtk-layer-shell/gtk-layer-shell.h>
#endif

/* Conditionally include X11 for global hotkeys */
#ifndef WAYLAND_ONLY
#include <gdk/gdkx.h>
#include <X11/Xlib.h>
#include <X11/keysym.h>
#include <X11/XKBlib.h>
#endif

/* ---- Geometry ---- */
#define OVERLAY_WIDTH    220
#define OVERLAY_HEIGHT    52

/* Transcript panel geometry. Live text needs room to wrap, so the window
   widens and grows downward while text is showing, then returns to the
   capsule when it is cleared. */
#define PANEL_WIDTH      620
#define PANEL_PAD_X       22
#define PANEL_PAD_Y       18
#define PANEL_TEXT_SIZE   17
#define PANEL_STATUS_SIZE 12
#define PANEL_MAX_HEIGHT 320
#define OVERLAY_RADIUS    26.0
#define ITEM_COUNT         7

/* ---- Colors (#1A1A1A @ 90%) ---- */
#define BG_R  0.102
#define BG_G  0.102
#define BG_B  0.102
#define BG_A  0.90

/* ---- Bar parameters ---- */
#define BAR_WIDTH       5.0
#define BAR_RADIUS      2.5
#define BAR_SPACING     8.0
#define BAR_MIN_HEIGHT  4.0
#define BAR_MAX_HEIGHT 40.0
#define RMS_SCALE       0.08

/* ---- Dot parameters ---- */
#define DOT_RADIUS   3.0
#define DOT_SPACING 10.0

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

/* ---- Public API ---- */

/* Create the overlay window (layer-shell if possible, else always-on-top fallback) */
GtkWidget *overlay_create(void);

/* Install X11 global hotkey bound to the overlay (no-op on Wayland) */
/* Installs the recording bindings. Either may be NULL or empty: push-to-talk
   fires down/up as the key is held and released, toggle fires once per press.
   Independent, so a user can hold one key and tap another. */
void overlay_install_hotkey(GtkWidget *win, const char *push_to_talk,
                            const char *toggle, HotkeyDownCB down_cb,
                            HotkeyUpCB up_cb, HotkeyDownCB toggle_cb);

/* Thread-safe async state/RMS updates via gdk_threads_add_idle */
void overlay_set_state_async(GtkWidget *win, int state);
void overlay_push_rms_async(GtkWidget *win, float rms);

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
                           const char *status, int provisional);

void overlay_show(GtkWidget *win);
void overlay_hide(GtkWidget *win);
