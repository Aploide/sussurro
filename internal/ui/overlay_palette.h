#pragma once

/* The ABI contains roles only. Go owns every colour value and passes complete
 * palette copies to native overlays. */
typedef struct {
    double r;
    double g;
    double b;
    double a;
} OverlayColor;

typedef struct {
    OverlayColor background;
    OverlayColor border;
    OverlayColor primary;
    OverlayColor secondary;
    OverlayColor provisional;
    OverlayColor copied;
    OverlayColor finalizing;
    OverlayColor track;
    OverlayColor fill;
    OverlayColor warning;
    OverlayColor shimmer_base;
    OverlayColor shimmer_peak;
} OverlayPalette;

typedef enum {
    OVERLAY_THEME_SYSTEM = 0,
    OVERLAY_THEME_LIGHT = 1,
    OVERLAY_THEME_DARK = 2,
} OverlayThemeMode;
