#pragma once

#include <gtk/gtk.h>
#include <math.h>
#include <string.h>
#include <stdlib.h>
#include "overlay_state.h"
#include "overlay_palette.h"

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
/* One overlay in every state, not a capsule that swaps for a panel. The window
   keeps a constant width and a bottom edge that does not move; text grows the
   height upwards from the resting size and shrinks it back as text clears. */
#define PANEL_WIDTH      860
#define PANEL_PAD_X       22
#define PANEL_PAD_Y       18
#define PANEL_TEXT_SIZE   17
#define PANEL_STATUS_SIZE 12
/* Fraction of the monitor height the panel may occupy before it stops growing
   and scrolls instead. A fixed pixel cap was the cause of sussurro-xvj.48:
   past it the window stopped rising while the text kept going, so the overflow
   ran off the bottom edge and read as the panel growing downwards. */
#define PANEL_MAX_HEIGHT_FRACTION 0.6
/* Floor for the above, for a very short screen or an unreadable monitor size. */
#define PANEL_MIN_MAX_HEIGHT 320
#define OVERLAY_RADIUS    26.0
#define ITEM_COUNT         7

/* ---- Bar parameters ---- */
#define BAR_WIDTH       5.0
#define BAR_RADIUS      2.5
#define BAR_SPACING     8.0
#define BAR_MIN_HEIGHT  4.0
#define BAR_MAX_HEIGHT 40.0
#define RMS_SCALE       0.08

/* ---- Bottom control row ---- */
/* Waveform and buffer-fill share one row anchored to the overlay's bottom
 * edge, which is the only position that stays put as text grows upwards
 * (sussurro-xvj.48). Both are permanent: anything drawn only on the pill
 * vanished the moment text arrived, which is what made the buffer-fill
 * indicator useless during the long dictations it exists to warn about.
 *
 * The gauge takes the larger share because it is what carries information over
 * a long dictation; the waveform only has to show that capture is live. */
#define ROW_WAVEFORM_FRACTION 0.10
#define ROW_GAP               14.0
#define ROW_HEIGHT            44.0
/* Breathing room between the transcript and the control row below it. Text
   sitting directly on the row read as cramped. */
#define TEXT_ROW_GAP          14
/* The overlay's resting height: the control row plus its padding, with no text.
   Text grows the panel upwards from here. */
#define OVERLAY_REST_HEIGHT   (int)(ROW_HEIGHT + 2 * PANEL_PAD_Y)

/* ---- Recording buffer fill indicator ---- */
#define FILL_TRACK_HEIGHT    5.0
/* Past this fraction the fill turns warning-coloured. */
#define FILL_WARN_FRACTION   0.8

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
                           const char *status, int provisional, int copied);

void overlay_show(GtkWidget *win);
void overlay_hide(GtkWidget *win);
