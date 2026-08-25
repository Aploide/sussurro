#pragma once

#include "overlay_state.h"
#include "overlay_palette.h"

/* ---- Geometry (identical to overlay_linux.h) ---- */
#define OVERLAY_WIDTH    220
#define OVERLAY_HEIGHT    52
#define OVERLAY_RADIUS    26.0f
#define ITEM_COUNT         7
#define OVERLAY_MARGIN_BOTTOM 24

/* ---- Bar parameters ---- */
#define BAR_WIDTH       5.0f
#define BAR_RADIUS      2.5f
#define BAR_SPACING     8.0f
#define BAR_MIN_HEIGHT  4.0f
#define BAR_MAX_HEIGHT 40.0f
#define RMS_SCALE       0.08f

/* ---- Dot parameters ---- */
#define DOT_RADIUS   3.0f
#define DOT_SPACING 10.0f

/* ---- Callback types (same shapes as overlay_linux.h) ---- */
typedef void (*MenuOpenSettingsCB)(void);
typedef void (*MenuQuitCB)(void);

/* ---- Public API ----
 * hwnd is the overlay HWND returned by overlay_create, passed as void*. */
void *overlay_create(const OverlayPalette *dark_palette,
                     const OverlayPalette *light_palette);
void  overlay_show(void *hwnd);
void  overlay_hide(void *hwnd);

/* Thread-safe async updates: PostMessage to the owner thread
 * (the Windows analogue of gdk_threads_add_idle). */
void  overlay_set_state_async(void *hwnd, int state);
void  overlay_push_rms_async(void *hwnd, float rms);
void  overlay_set_theme_async(void *hwnd, int mode,
                              const OverlayPalette *dark_palette,
                              const OverlayPalette *light_palette);

/* Right-click context menu (fallback for when no system tray is visible) */
void  overlay_install_context_menu(void *hwnd,
                                   MenuOpenSettingsCB open_settings_cb,
                                   MenuQuitCB quit_cb);
