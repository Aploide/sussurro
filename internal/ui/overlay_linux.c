#include "overlay_linux.h"
#include <pango/pangocairo.h>

/* ------------------------------------------------------------------ */
/* Internal data structure                                             */
/* ------------------------------------------------------------------ */

struct OverlayData {
    GtkWidget   *window;
    GtkWidget   *drawing_area;

    int          state;       /* OVERLAY_STATE_* */
    double       anim_time;   /* seconds, incremented each tick       */

    /* Idle-dot animation: per-dot alpha cache (not strictly needed,
       recomputed each frame, kept for potential future interpolation) */
    double       dot_alpha[ITEM_COUNT];

    /* Bar heights */
    float        rms_ring[ITEM_COUNT];   /* ring-buffer of last N rms values */
    int          rms_head;
    double       bar_heights[ITEM_COUNT]; /* smoothed current heights         */
    double       bar_targets[ITEM_COUNT]; /* targets from RMS                 */

    /* Recording buffer fill, 0 to 1. Smoothed toward its target on the
       animation tick like the bars, so the track glides rather than steps. */
    double       fill;
    double       fill_target;

    /* Shimmer phase for transcribing text */
    double       shimmer_phase;

    /* Live transcript shown in the expanded panel. Owned by this struct and
       only touched on the GTK main thread. */
    char        *transcript;
    char        *status;
    gboolean     provisional;   /* text is still being revised */
    int          panel_height;  /* 0 when showing the capsule */

    /* Animation timer source id, 0 when stopped. The timer only runs while
       the overlay is mapped: a hidden capsule must cost nothing. */
    guint        anim_source;

    /* X11 hotkeys. Push-to-talk and toggle are separate bindings, either of
       which may be unset (keycode 0), so a user can hold one key and tap
       another. */
    HotkeyDownCB down_cb;
    HotkeyUpCB   up_cb;
    HotkeyDownCB toggle_cb;
    int          hk_keycode;
    unsigned int hk_mods;
    gboolean     hk_pressed;
    int          tg_keycode;
    unsigned int tg_mods;
    gboolean     tg_pressed;
};

/* ------------------------------------------------------------------ */
/* Drawing helpers                                                     */
/* ------------------------------------------------------------------ */

/* Appends a rounded-rectangle sub-path. The corner radius is clamped so a
 * shape shorter or narrower than its own corners still renders, degenerating
 * to a stadium rather than to overlapping arcs. */
static void rounded_rect(cairo_t *cr, double x, double y,
                         double w, double h, double r)
{
    if (r > w / 2.0) r = w / 2.0;
    if (r > h / 2.0) r = h / 2.0;

    cairo_new_sub_path(cr);
    cairo_arc(cr, x + r,     y + r,     r, M_PI,         3.0*M_PI/2.0);
    cairo_arc(cr, x + w - r, y + r,     r, 3.0*M_PI/2.0, 0.0);
    cairo_arc(cr, x + w - r, y + h - r, r, 0.0,          M_PI/2.0);
    cairo_arc(cr, x + r,     y + h - r, r, M_PI/2.0,     M_PI);
    cairo_close_path(cr);
}

/* Draws the idle dots centred within the slot (x, w) at vertical centre_y. */
static void draw_idle_dots(cairo_t *cr, OverlayData *od,
                           double x, double w, double centre_y)
{
    double total_w = (ITEM_COUNT - 1) * DOT_SPACING;
    double start_x = x + (w - total_w) / 2.0;
    double center_y = centre_y;

    for (int i = 0; i < ITEM_COUNT; i++) {
        double t   = od->anim_time;
        double phi = 2.0 * M_PI * t / 4.0 + i * 2.0 * M_PI / (double)ITEM_COUNT;
        double s   = sin(phi);
        double a   = 0.35 + 0.65 * s * s;

        double cx = start_x + i * DOT_SPACING;
        cairo_arc(cr, cx, center_y, DOT_RADIUS, 0, 2.0 * M_PI);
        cairo_set_source_rgba(cr, 1.0, 1.0, 1.0, a);
        cairo_fill(cr);
    }
}

/* Draws the recording buffer fill into the given rect.
 *
 * The rect is passed in rather than derived from the pill, because this now
 * lives in the overlay's permanent bottom row: it must stay visible while text
 * is on screen, which is exactly when a long dictation risks reaching the cap.
 *
 * The track turns warning-coloured past FILL_WARN_FRACTION, so the approach to
 * a truncating cap reads at a glance without needing a number. */
static void draw_buffer_fill(cairo_t *cr, OverlayData *od,
                             double x, double y, double w)
{
    double r = FILL_TRACK_HEIGHT / 2.0;

    /* Unfilled track: dim enough to read as a groove rather than content. */
    cairo_set_source_rgba(cr, 1.0, 1.0, 1.0, 0.18);
    rounded_rect(cr, x, y, w, FILL_TRACK_HEIGHT, r);
    cairo_fill(cr);

    double fill_w = w * od->fill;
    if (fill_w <= 0.0) return;
    /* Never narrower than the cap it is drawn with, or the rounded ends
       degenerate into a dot at very low fill. */
    if (fill_w < FILL_TRACK_HEIGHT) fill_w = FILL_TRACK_HEIGHT;

    if (od->fill >= FILL_WARN_FRACTION) {
        cairo_set_source_rgba(cr, 1.0, 0.62, 0.23, 0.95);
    } else {
        cairo_set_source_rgba(cr, 1.0, 1.0, 1.0, 0.55);
    }
    rounded_rect(cr, x, y, fill_w, FILL_TRACK_HEIGHT, r);
    cairo_fill(cr);
}

/* Draws the waveform centred within the slot (x, w) at vertical centre_y.
 *
 * The bars occupy a fixed width, so at the wide slot of the unified layout they
 * are centred in the space rather than stretched across it: the waveform is a
 * liveness indicator here, and thinner bars spread wider would read as less,
 * not more. */
static void draw_recording_bars(cairo_t *cr, OverlayData *od,
                                double x, double w, double centre_y)
{
    double total_w = (ITEM_COUNT - 1) * BAR_SPACING;
    double start_x = x + (w - total_w) / 2.0;
    double center_y = centre_y;

    cairo_set_source_rgba(cr, 1.0, 1.0, 1.0, 1.0);

    for (int i = 0; i < ITEM_COUNT; i++) {
        double h  = od->bar_heights[i];
        double cx = start_x + i * BAR_SPACING;
        double x  = cx - BAR_WIDTH / 2.0;
        double y  = center_y - h / 2.0;

        rounded_rect(cr, x, y, BAR_WIDTH, h, BAR_RADIUS);
        cairo_fill(cr);
    }
}

/* Draws the overlay's permanent bottom row: waveform on the left, buffer-fill
 * gauge filling the rest.
 *
 * Anchored to the bottom edge, which is the only part of the overlay that
 * holds still as text grows upwards (sussurro-xvj.48). Drawn in every state, so
 * neither element disappears when text arrives — that disappearance was the
 * reported defect.
 *
 * panel_h is the overlay's current height, so the row finds its own position
 * whether the overlay is the bare control strip or a full transcript panel. */
static void draw_control_row(cairo_t *cr, OverlayData *od,
                             double width, double panel_h)
{
    double content_w = width - 2.0 * PANEL_PAD_X;
    double wave_w    = content_w * ROW_WAVEFORM_FRACTION;
    double gauge_w   = content_w - wave_w - ROW_GAP;
    double row_top   = panel_h - PANEL_PAD_Y - ROW_HEIGHT;
    double centre_y  = row_top + ROW_HEIGHT / 2.0;

    if (od->state == OVERLAY_STATE_IDLE) {
        draw_idle_dots(cr, od, PANEL_PAD_X, wave_w, centre_y);
    } else {
        draw_recording_bars(cr, od, PANEL_PAD_X, wave_w, centre_y);
    }

    /* The gauge is vertically centred in the row rather than sitting on the
       overlay's edge, so it stays aligned with the waveform beside it. */
    draw_buffer_fill(cr, od, PANEL_PAD_X + wave_w + ROW_GAP,
                     centre_y - FILL_TRACK_HEIGHT / 2.0, gauge_w);
}

/* Keeps the overlay bottom-centred on the primary monitor at the given size.
   The window changes size as the transcript grows, so the position has to be
   recomputed rather than set once at creation.

   Under gtk-layer-shell the compositor owns placement and this is a no-op. */
static void reposition_overlay(GtkWidget *win, int width, int height)
{
#ifdef HAVE_GTK_LAYER_SHELL
    (void)win; (void)width; (void)height;
#else
    GdkDisplay *display = gdk_display_get_default();
    GdkMonitor *monitor = gdk_display_get_primary_monitor(display);
    if (!monitor) monitor = gdk_display_get_monitor(display, 0);
    GdkRectangle geo = {0, 0, 1920, 1080}; /* safe fallback */
    if (monitor) gdk_monitor_get_geometry(monitor, &geo);

    int x = geo.x + (geo.width - width) / 2;
    int y = geo.y + geo.height - height - 24;
    gtk_window_move(GTK_WINDOW(win), x, y);
#endif
}

/* ------------------------------------------------------------------ */
/* Transcript panel                                                     */
/* ------------------------------------------------------------------ */

/* Builds the Pango layout for the transcript text.
   cairo_show_text cannot wrap, so live dictation needs a real text layout. */
/* Returns the display's font scale relative to the 96dpi baseline Pango
   assumes. Xft.dpi is what the rest of the desktop is sized by, so ignoring
   it makes the panel text visibly smaller than every other application. */
static double panel_font_scale(void)
{
    GdkScreen *screen = gdk_screen_get_default();
    if (!screen) return 1.0;

    double dpi = gdk_screen_get_resolution(screen);
    if (dpi <= 0) return 1.0;   /* unset: Pango's own default applies */
    return dpi / 96.0;
}

/* The tallest the panel may become before it scrolls instead of growing.
   Derived from the monitor so a long transcript on a large screen uses the
   space available, rather than stopping at a pixel count chosen for a smaller
   one. */
static int panel_max_height(void)
{
    GdkDisplay *display = gdk_display_get_default();
    GdkMonitor *monitor = display ? gdk_display_get_primary_monitor(display) : NULL;
    if (display && !monitor) monitor = gdk_display_get_monitor(display, 0);

    GdkRectangle geo = {0, 0, 1920, 1080}; /* safe fallback */
    if (monitor) gdk_monitor_get_geometry(monitor, &geo);

    int cap = (int)(geo.height * PANEL_MAX_HEIGHT_FRACTION);
    if (cap < PANEL_MIN_MAX_HEIGHT) cap = PANEL_MIN_MAX_HEIGHT;
    return cap;
}

static PangoLayout *panel_text_layout(cairo_t *cr, OverlayData *od)
{
    PangoLayout *layout = pango_cairo_create_layout(cr);
    PangoFontDescription *font = pango_font_description_from_string("Sans");
    pango_font_description_set_absolute_size(
        font, PANEL_TEXT_SIZE * panel_font_scale() * PANGO_SCALE);
    pango_layout_set_font_description(layout, font);
    pango_font_description_free(font);

    pango_layout_set_width(layout, (PANEL_WIDTH - 2 * PANEL_PAD_X) * PANGO_SCALE);
    pango_layout_set_wrap(layout, PANGO_WRAP_WORD_CHAR);
    pango_layout_set_text(layout, od->transcript ? od->transcript : "", -1);
    return layout;
}

/* Rounded rectangle path for the expanded panel. */
static void panel_path(cairo_t *cr, double w, double h)
{
    const double r = 12.0;
    cairo_new_sub_path(cr);
    cairo_arc(cr, w - r, r,     r, -G_PI / 2, 0);
    cairo_arc(cr, w - r, h - r, r, 0,          G_PI / 2);
    cairo_arc(cr, r,     h - r, r, G_PI / 2,   G_PI);
    cairo_arc(cr, r,     r,     r, G_PI,       3 * G_PI / 2);
    cairo_close_path(cr);
}

/* Draws the transcript panel. Returns the height it needs, so the caller can
   size the window to the text. */
static int draw_panel(cairo_t *cr, OverlayData *od, gboolean paint)
{
    PangoLayout *layout = panel_text_layout(cr, od);

    int text_w = 0, text_h = 0;
    pango_layout_get_pixel_size(layout, &text_w, &text_h);

    int status_h = 0;
    PangoLayout *status_layout = NULL;
    if (od->status && od->status[0]) {
        status_layout = pango_cairo_create_layout(cr);
        PangoFontDescription *sf = pango_font_description_from_string("Sans");
        pango_font_description_set_absolute_size(
            sf, PANEL_STATUS_SIZE * panel_font_scale() * PANGO_SCALE);
        pango_layout_set_font_description(status_layout, sf);
        pango_font_description_free(sf);
        pango_layout_set_text(status_layout, od->status, -1);

        int sw = 0;
        pango_layout_get_pixel_size(status_layout, &sw, &status_h);
        status_h += 6; /* gap above the status line */
    }

    /* The control row is permanent, so it is part of the panel's height at
       every size, including when there is no text at all. */
    int height = PANEL_PAD_Y * 2 + text_h + status_h + (int)ROW_HEIGHT;

    /* Past the cap the panel stops growing. The text is then anchored to its
       END rather than its start: during dictation the newest words matter, and
       drawing from the top would leave them off the bottom edge, which is the
       cropping reported in sussurro-xvj.48. */
    int max_height = panel_max_height();
    int text_offset = 0;
    if (height > max_height) {
        text_offset = height - max_height;
        height = max_height;
    }

    if (paint) {
        panel_path(cr, PANEL_WIDTH, height);
        cairo_set_source_rgba(cr, 0.12, 0.12, 0.12, 0.94);
        cairo_fill_preserve(cr);
        cairo_set_source_rgba(cr, 1, 1, 1, 0.10);
        cairo_set_line_width(cr, 1.0);
        cairo_stroke(cr);

        /* Provisional text is dimmed: it is still being revised, and the
           user should be able to tell settled text from text that may
           still change under them. */
        if (od->provisional) {
            cairo_set_source_rgba(cr, 1, 1, 1, 0.72);
        } else {
            cairo_set_source_rgba(cr, 1, 1, 1, 0.95);
        }
        /* Clip the text to its own region, which stops above the status
           line. A scrolled layout starts above the panel's top edge, so
           without this it would paint over the window's surroundings, and
           its last line would run underneath the status text. */
        cairo_save(cr);
        cairo_rectangle(cr, 0, 0, PANEL_WIDTH,
                        height - PANEL_PAD_Y - status_h - ROW_HEIGHT);
        cairo_clip(cr);
        cairo_move_to(cr, PANEL_PAD_X, PANEL_PAD_Y - text_offset);
        pango_cairo_show_layout(cr, layout);
        cairo_restore(cr);

        if (status_layout) {
            cairo_set_source_rgba(cr, 1, 1, 1, 0.45);
            /* Positioned from the panel's bottom edge so it stays visible when
               the text above it has scrolled. */
            cairo_move_to(cr, PANEL_PAD_X,
                          height - PANEL_PAD_Y - ROW_HEIGHT - (status_h - 6));
            pango_cairo_show_layout(cr, status_layout);
        }

        draw_control_row(cr, od, PANEL_WIDTH, height);
    }

    if (status_layout) g_object_unref(status_layout);
    g_object_unref(layout);
    return height;
}

/* ------------------------------------------------------------------ */
/* Draw callback                                                       */
/* ------------------------------------------------------------------ */

static gboolean on_draw(GtkWidget *widget, cairo_t *cr, gpointer data)
{
    OverlayData *od = (OverlayData *)data;

    /* Transparent background (composited window) */
    cairo_set_operator(cr, CAIRO_OPERATOR_SOURCE);
    cairo_set_source_rgba(cr, 0, 0, 0, 0);
    cairo_paint(cr);
    cairo_set_operator(cr, CAIRO_OPERATOR_OVER);

    /* One overlay in every state. There is no pill-to-panel switch: the panel
       always carries the control row along its bottom edge, and the text area
       above it grows from zero as text arrives. Swapping between two shapes is
       what produced the jolt, and what made anything drawn on the pill vanish
       the moment text appeared. */
    draw_panel(cr, od, TRUE);
    return FALSE;
}

/* ------------------------------------------------------------------ */
/* Animation timer (60 fps)                                            */
/* ------------------------------------------------------------------ */

static gboolean animation_tick(gpointer data)
{
    OverlayData *od = (OverlayData *)data;
    double dt = 1.0 / 60.0;
    od->anim_time     += dt;
    od->shimmer_phase += dt;

    /* Smooth bar heights toward targets */
    for (int i = 0; i < ITEM_COUNT; i++) {
        od->bar_heights[i] = od->bar_heights[i] * 0.7 + od->bar_targets[i] * 0.3;
    }

    od->fill = od->fill * 0.9 + od->fill_target * 0.1;

    gtk_widget_queue_draw(od->drawing_area);
    return G_SOURCE_CONTINUE;
}

/* Starts the animation timer if it is not already running. */
static void overlay_start_animation(OverlayData *od)
{
    if (od->anim_source == 0) {
        od->anim_source = g_timeout_add(16, animation_tick, od);
    }
}

/* Stops the animation timer. Idle redraws are pure waste while hidden. */
static void overlay_stop_animation(OverlayData *od)
{
    if (od->anim_source != 0) {
        g_source_remove(od->anim_source);
        od->anim_source = 0;
    }
}

/* ------------------------------------------------------------------ */
/* X11 global hotkey via GDK event filter                              */
/* ------------------------------------------------------------------ */

#ifndef WAYLAND_ONLY

static GdkFilterReturn x11_event_filter(GdkXEvent *xevent, GdkEvent *event, gpointer data)
{
    (void)event;
    OverlayData *od = (OverlayData *)data;
    XEvent *xe = (XEvent *)xevent;

    if (xe->type == KeyPress || xe->type == KeyRelease) {
        g_debug("sussurro hotkey filter: type=%s keycode=%d state=0x%x "
                "want_keycode=%d want_mods=0x%x pressed=%d",
                xe->type == KeyPress ? "press" : "release",
                (int)xe->xkey.keycode, (unsigned int)xe->xkey.state,
                od->hk_keycode, od->hk_mods, od->hk_pressed);
    }

    if (xe->type == KeyPress) {
        if (od->hk_keycode && (int)xe->xkey.keycode == od->hk_keycode &&
            (xe->xkey.state & od->hk_mods) == od->hk_mods) {
            if (!od->hk_pressed) {
                od->hk_pressed = TRUE;
                if (od->down_cb) od->down_cb();
            }
            return GDK_FILTER_REMOVE;
        }
        /* The toggle binding acts on press and ignores release entirely. */
        if (od->tg_keycode && (int)xe->xkey.keycode == od->tg_keycode &&
            (xe->xkey.state & od->tg_mods) == od->tg_mods) {
            if (!od->tg_pressed) {
                od->tg_pressed = TRUE;
                if (od->toggle_cb) od->toggle_cb();
            }
            return GDK_FILTER_REMOVE;
        }
    } else if (xe->type == KeyRelease) {
        /* Clear the toggle's held flag so the next press counts, without
           firing anything: toggling happens on press only. */
        if (od->tg_keycode && (int)xe->xkey.keycode == od->tg_keycode) {
            od->tg_pressed = FALSE;
            return GDK_FILTER_REMOVE;
        }
        if (od->hk_keycode && (int)xe->xkey.keycode == od->hk_keycode) {
            if (od->hk_pressed) {
                od->hk_pressed = FALSE;
                if (od->up_cb) od->up_cb();
            } else {
                /* Belt and braces: detectable auto-repeat should mean this
                   never happens, but a release with no matching press has
                   previously left a recording running to max_duration. Ending
                   it costs nothing when there is nothing to end. */
                g_debug("sussurro: release with no active press; ending anyway");
                if (od->up_cb) od->up_cb();
            }
            return GDK_FILTER_REMOVE;
        }
        /* A release for a key we did not grab, arriving while the hotkey is
           held, means the modifier went up first. X11 delivers the grabbed
           key's release to the grab owner, but some setups (and key repeat
           filters) drop it once the modifier state no longer matches. Ending
           the recording here is better than waiting for max_duration. */
        if (od->hk_pressed && od->hk_mods != 0) {
            unsigned int released_mod = 0;
            KeySym       sym = XkbKeycodeToKeysym(xe->xkey.display,
                                                  xe->xkey.keycode, 0, 0);
            switch (sym) {
                case XK_Super_L: case XK_Super_R: released_mod = Mod4Mask;    break;
                case XK_Control_L: case XK_Control_R: released_mod = ControlMask; break;
                case XK_Shift_L: case XK_Shift_R: released_mod = ShiftMask;   break;
                case XK_Alt_L: case XK_Alt_R: released_mod = Mod1Mask;        break;
                default: break;
            }
            if (released_mod && (od->hk_mods & released_mod)) {
                od->hk_pressed = FALSE;
                if (od->up_cb) od->up_cb();
            }
        }
    }

    return GDK_FILTER_CONTINUE;
}

static unsigned int parse_x11_mods(const char *trigger)
{
    unsigned int mods = 0;
    char *copy = strdup(trigger);
    char *tok  = strtok(copy, "+");
    while (tok) {
        if      (strcmp(tok, "ctrl")  == 0) mods |= ControlMask;
        else if (strcmp(tok, "shift") == 0) mods |= ShiftMask;
        else if (strcmp(tok, "alt")   == 0) mods |= Mod1Mask;
        else if (strcmp(tok, "super") == 0) mods |= Mod4Mask;
        tok = strtok(NULL, "+");
    }
    free(copy);
    return mods;
}

static KeySym parse_x11_keysym(const char *trigger)
{
    /* Last token after splitting on '+' */
    const char *p = strrchr(trigger, '+');
    const char *key_str = p ? p + 1 : trigger;

    if (strcmp(key_str, "space") == 0) return XK_space;
    if (strcmp(key_str, "enter") == 0) return XK_Return;
    if (strcmp(key_str, "tab")   == 0) return XK_Tab;

    /* Single character keys */
    if (strlen(key_str) == 1) {
        char buf[2] = {key_str[0], 0};
        return XStringToKeysym(buf);
    }

    /* F-keys */
    if (key_str[0] == 'f' || key_str[0] == 'F') {
        int n = atoi(key_str + 1);
        if (n >= 1 && n <= 12) return XK_F1 + (n - 1);
    }

    return XStringToKeysym(key_str);
}

#endif /* WAYLAND_ONLY */

/* ------------------------------------------------------------------ */
/* Public API                                                          */
/* ------------------------------------------------------------------ */

GtkWidget *overlay_create(void)
{
    GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);

    gtk_window_set_title(GTK_WINDOW(win), "Sussurro Overlay");
    gtk_window_set_default_size(GTK_WINDOW(win), PANEL_WIDTH, OVERLAY_REST_HEIGHT);
    gtk_window_set_resizable(GTK_WINDOW(win), FALSE);
    gtk_window_set_decorated(GTK_WINDOW(win), FALSE);
    /* EWMH window type — WMs don't decorate notification windows regardless
       of how the process was launched (terminal vs double-click). */
    gtk_window_set_type_hint(GTK_WINDOW(win), GDK_WINDOW_TYPE_HINT_NOTIFICATION);
    gtk_window_set_accept_focus(GTK_WINDOW(win), FALSE);
    gtk_window_set_skip_taskbar_hint(GTK_WINDOW(win), TRUE);
    gtk_window_set_skip_pager_hint(GTK_WINDOW(win), TRUE);
    gtk_window_set_keep_above(GTK_WINDOW(win), TRUE);
    gtk_widget_set_app_paintable(win, TRUE);

    /* RGBA visual for transparency */
    GdkScreen  *screen  = gtk_widget_get_screen(win);
    GdkVisual  *visual  = gdk_screen_get_rgba_visual(screen);
    if (visual) gtk_widget_set_visual(win, visual);

    /* Drawing area */
    GtkWidget *da = gtk_drawing_area_new();
    gtk_widget_set_size_request(da, PANEL_WIDTH, OVERLAY_REST_HEIGHT);
    gtk_container_add(GTK_CONTAINER(win), da);

    /* Allocate and attach overlay data */
    OverlayData *od = g_new0(OverlayData, 1);
    od->window       = win;
    od->drawing_area = da;
    od->state        = OVERLAY_STATE_IDLE;
    for (int i = 0; i < ITEM_COUNT; i++) {
        od->bar_heights[i] = BAR_MIN_HEIGHT;
        od->bar_targets[i] = BAR_MIN_HEIGHT;
    }

    g_object_set_data(G_OBJECT(win), "overlay-data", od);

    /* Connect draw callback */
    g_signal_connect(da, "draw", G_CALLBACK(on_draw), od);

    /* Suppress delete-window */
    g_signal_connect(win, "delete-event", G_CALLBACK(gtk_true), NULL);

#ifdef HAVE_GTK_LAYER_SHELL
    /* wlr-layer-shell overlay */
    gtk_layer_init_for_window(GTK_WINDOW(win));
    gtk_layer_set_layer(GTK_WINDOW(win), GTK_LAYER_SHELL_LAYER_OVERLAY);
    gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_BOTTOM, TRUE);
    gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_LEFT,   FALSE);
    gtk_layer_set_anchor(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_RIGHT,  FALSE);
    gtk_layer_set_margin(GTK_WINDOW(win), GTK_LAYER_SHELL_EDGE_BOTTOM, 24);
    gtk_layer_set_exclusive_zone(GTK_WINDOW(win), -1);
    gtk_layer_set_keyboard_mode(GTK_WINDOW(win), GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);
    gtk_layer_set_namespace(GTK_WINDOW(win), "sussurro");
#else
    /* X11 / non-layer-shell fallback: position bottom-center of the primary
       monitor and bypass the WM entirely with override-redirect.

       gtk_window_move() is only a WM hint and can be ignored (especially
       when the process is launched from a file manager instead of a
       terminal).  Setting override-redirect before the window is mapped
       tells X11 to skip the WM for this window: no decorations, no
       re-positioning, no moving — the window sits exactly where we put it,
       regardless of how the process was started. */
    {
        reposition_overlay(win, PANEL_WIDTH, OVERLAY_REST_HEIGHT);

        /* Realize creates the underlying GdkWindow without mapping (showing)
           it, so override-redirect can be set before the WM ever sees the
           window. */
        gtk_widget_realize(win);
        GdkWindow *gdk_win = gtk_widget_get_window(win);
        if (gdk_win) {
            gdk_window_set_override_redirect(gdk_win, TRUE);
        }
    }
#endif

    /* Deliberately not shown here. The capsule is mapped only while
       something is happening (see overlay_show), so an idle Sussurro leaves
       nothing on screen. gtk_widget_show_all on the child keeps the drawing
       area realized so the first map paints immediately, without mapping the
       toplevel itself.

       On X11 the realize/override-redirect ordering above still holds: the
       window is realized but unmapped, which is exactly what
       override-redirect needs. */
    gtk_widget_show_all(da);

    return win;
}

void overlay_install_hotkey(GtkWidget *win, const char *push_to_talk,
                            const char *toggle, HotkeyDownCB down_cb,
                            HotkeyUpCB up_cb, HotkeyDownCB toggle_cb)
{
    OverlayData *od = (OverlayData *)g_object_get_data(G_OBJECT(win), "overlay-data");
    if (!od) return;

    od->down_cb   = down_cb;
    od->up_cb     = up_cb;
    od->toggle_cb = toggle_cb;

#ifndef WAYLAND_ONLY
    GdkDisplay *display = gdk_display_get_default();

    /* Only install on X11 displays */
    if (!GDK_IS_X11_DISPLAY(display)) return;

    Display *xdpy  = gdk_x11_display_get_xdisplay(display);
    Window   xroot = DefaultRootWindow(xdpy);

    /* Without this, X11 auto-repeat synthesises a KeyRelease immediately
       followed by a KeyPress for as long as the key is held. Those synthetic
       releases are indistinguishable from a real one, so a genuine release
       arriving between a repeat's release and its press is discarded as
       spurious — the recording then runs to max_duration. Detectable
       auto-repeat suppresses the synthetic releases outright, which is
       exactly what a push-to-talk grab wants. */
    Bool detectable = False;
    XkbSetDetectableAutoRepeat(xdpy, True, &detectable);
    if (!detectable) {
        g_warning("sussurro: detectable auto-repeat unavailable; "
                  "push-to-talk release may be missed while a key repeats");
    }

    /* Lock-key combinations each need their own grab, or the hotkey stops
       working with Caps or Num Lock on. */
    unsigned int lock_combos[] = {0, LockMask, Mod2Mask, LockMask | Mod2Mask};

    od->hk_keycode = 0;
    od->tg_keycode = 0;

    if (push_to_talk && push_to_talk[0]) {
        od->hk_mods    = parse_x11_mods(push_to_talk);
        od->hk_keycode = XKeysymToKeycode(xdpy, parse_x11_keysym(push_to_talk));
        for (int i = 0; i < 4; i++) {
            XGrabKey(xdpy, od->hk_keycode, od->hk_mods | lock_combos[i],
                     xroot, True, GrabModeAsync, GrabModeAsync);
        }
    }

    if (toggle && toggle[0]) {
        od->tg_mods    = parse_x11_mods(toggle);
        od->tg_keycode = XKeysymToKeycode(xdpy, parse_x11_keysym(toggle));
        for (int i = 0; i < 4; i++) {
            XGrabKey(xdpy, od->tg_keycode, od->tg_mods | lock_combos[i],
                     xroot, True, GrabModeAsync, GrabModeAsync);
        }
    }

    /* Install GDK event filter on root window */
    GdkWindow *root_gdk = gdk_x11_window_foreign_new_for_display(display, xroot);
    if (root_gdk) {
        gdk_window_add_filter(root_gdk, x11_event_filter, od);
        g_object_unref(root_gdk);
    }
#endif
}

/* ---- Async state/RMS update ---- */

gboolean idle_set_state(gpointer data)
{
    IdleStateArg *arg = (IdleStateArg *)data;
    OverlayData  *od  = (OverlayData *)g_object_get_data(G_OBJECT(arg->win), "overlay-data");
    if (od) {
        /* Entering RECORDING starts a fresh buffer, so clear the fill rather
           than letting the smoothing drag the previous recording's value down
           across the first second of the new one. */
        if (arg->state == OVERLAY_STATE_RECORDING && od->state != OVERLAY_STATE_RECORDING) {
            od->fill        = 0.0;
            od->fill_target = 0.0;
        }
        od->state = arg->state;
        gtk_widget_queue_draw(od->drawing_area);
    }
    g_free(arg);
    return G_SOURCE_REMOVE;
}

gboolean idle_push_rms(gpointer data)
{
    IdleRMSArg  *arg = (IdleRMSArg *)data;
    OverlayData *od  = (OverlayData *)g_object_get_data(G_OBJECT(arg->win), "overlay-data");
    if (od) {
        /* Write into ring buffer */
        od->rms_ring[od->rms_head] = arg->rms;
        od->rms_head = (od->rms_head + 1) % ITEM_COUNT;

        /* Update bar targets from ring buffer */
        for (int i = 0; i < ITEM_COUNT; i++) {
            int idx = (od->rms_head + i) % ITEM_COUNT;
            float rms = od->rms_ring[idx];
            double norm = rms / RMS_SCALE;
            if (norm > 1.0) norm = 1.0;
            od->bar_targets[i] = BAR_MIN_HEIGHT + norm * (BAR_MAX_HEIGHT - BAR_MIN_HEIGHT);
        }
    }
    g_free(arg);
    return G_SOURCE_REMOVE;
}

void overlay_set_state_async(GtkWidget *win, int state)
{
    IdleStateArg *arg = g_new(IdleStateArg, 1);
    arg->win   = win;
    arg->state = state;
    gdk_threads_add_idle(idle_set_state, arg);
}

void overlay_push_rms_async(GtkWidget *win, float rms)
{
    IdleRMSArg *arg = g_new(IdleRMSArg, 1);
    arg->win = win;
    arg->rms = rms;
    gdk_threads_add_idle(idle_push_rms, arg);
}

gboolean idle_push_fill(gpointer data)
{
    IdleFillArg *arg = (IdleFillArg *)data;
    OverlayData *od  = (OverlayData *)g_object_get_data(G_OBJECT(arg->win), "overlay-data");
    if (od) {
        double fill = arg->fill;
        if (fill < 0.0) fill = 0.0;
        if (fill > 1.0) fill = 1.0;
        od->fill_target = fill;
    }
    g_free(arg);
    return G_SOURCE_REMOVE;
}

void overlay_push_fill_async(GtkWidget *win, double fill)
{
    IdleFillArg *arg = g_new(IdleFillArg, 1);
    arg->win  = win;
    arg->fill = fill;
    gdk_threads_add_idle(idle_push_fill, arg);
}

/* ------------------------------------------------------------------ */
/* Right-click context menu                                            */
/* ------------------------------------------------------------------ */

static MenuOpenSettingsCB g_open_settings_cb = NULL;
static MenuQuitCB         g_quit_cb          = NULL;

static void on_menu_open_settings(GtkMenuItem *item, gpointer data)
{
    (void)item; (void)data;
    if (g_open_settings_cb) g_open_settings_cb();
}

static void on_menu_quit(GtkMenuItem *item, gpointer data)
{
    (void)item; (void)data;
    if (g_quit_cb) g_quit_cb();
}

static gboolean on_button_press(GtkWidget *widget, GdkEventButton *event, gpointer data)
{
    (void)widget; (void)data;
    if (event->type == GDK_BUTTON_PRESS && event->button == 3) {
        GtkWidget *menu      = gtk_menu_new();
        GtkWidget *i_settings = gtk_menu_item_new_with_label("Open Settings");
        GtkWidget *i_sep     = gtk_separator_menu_item_new();
        GtkWidget *i_quit    = gtk_menu_item_new_with_label("Quit");

        g_signal_connect(i_settings, "activate",
                         G_CALLBACK(on_menu_open_settings), NULL);
        g_signal_connect(i_quit,     "activate",
                         G_CALLBACK(on_menu_quit), NULL);

        gtk_menu_shell_append(GTK_MENU_SHELL(menu), i_settings);
        gtk_menu_shell_append(GTK_MENU_SHELL(menu), i_sep);
        gtk_menu_shell_append(GTK_MENU_SHELL(menu), i_quit);
        gtk_widget_show_all(menu);

        gtk_menu_popup_at_pointer(GTK_MENU(menu), (GdkEvent *)event);
        return TRUE;
    }
    return FALSE;
}

void overlay_install_context_menu(GtkWidget *win,
                                  MenuOpenSettingsCB open_settings_cb,
                                  MenuQuitCB quit_cb)
{
    g_open_settings_cb = open_settings_cb;
    g_quit_cb          = quit_cb;

    gtk_widget_add_events(win, GDK_BUTTON_PRESS_MASK);
    g_signal_connect(win, "button-press-event",
                     G_CALLBACK(on_button_press), NULL);
}

/* Show and hide are called from the pipeline goroutine and from the systray
   goroutine, never the GTK main thread, so they marshal like the state and RMS
   updates above. Touching GTK directly from another thread is undefined
   behaviour that happens to work until it does not. */
static gboolean idle_set_visible(gpointer data)
{
    IdleStateArg *arg = (IdleStateArg *)data;
    OverlayData  *od  = (OverlayData *)g_object_get_data(G_OBJECT(arg->win), "overlay-data");

    if (arg->state) {
        if (od) overlay_start_animation(od);
        gtk_widget_show_all(arg->win);
    } else {
        if (od) overlay_stop_animation(od);
        gtk_widget_hide(arg->win);
    }

    g_free(arg);
    return G_SOURCE_REMOVE;
}

/* Queues a visibility change on the GTK main thread. */
static void overlay_set_visible_async(GtkWidget *win, gboolean visible)
{
    IdleStateArg *arg = g_new(IdleStateArg, 1);
    arg->win   = win;
    arg->state = visible ? 1 : 0;
    gdk_threads_add_idle(idle_set_visible, arg);
}

/* Argument for a queued transcript update. */
typedef struct {
    GtkWidget *win;
    int        state;
    char      *text;
    char      *status;
    int        provisional;
} IdleTranscriptArg;

/* Applies a transcript update on the GTK main thread, resizing the window to
   fit the text. */
static gboolean idle_set_transcript(gpointer data)
{
    IdleTranscriptArg *arg = (IdleTranscriptArg *)data;
    OverlayData *od = (OverlayData *)g_object_get_data(G_OBJECT(arg->win), "overlay-data");
    if (!od) goto done;

    /* State and text are applied together: updating them through separate
       idle callbacks let a draw land between the two, briefly showing the
       transcribing capsule in place of text that was already on screen. */
    od->state = arg->state;

    g_free(od->transcript);
    g_free(od->status);
    od->transcript  = arg->text   ? g_strdup(arg->text)   : NULL;
    od->status      = arg->status ? g_strdup(arg->status) : NULL;
    od->provisional = arg->provisional ? TRUE : FALSE;

    /* One size calculation for every state. Measured against a throwaway
       surface because the height depends on how the text wraps, which only
       Pango can tell us; with no text the panel collapses to the control row
       and its padding, which is the resting size. Reverting to a separate
       pill geometry here is what made the overlay change shape. */
    cairo_surface_t *probe = cairo_image_surface_create(CAIRO_FORMAT_ARGB32, 1, 1);
    cairo_t *cr = cairo_create(probe);
    int height = draw_panel(cr, od, FALSE);
    cairo_destroy(cr);
    cairo_surface_destroy(probe);

    if (height != od->panel_height) {
        od->panel_height = height;
        gtk_widget_set_size_request(od->drawing_area, PANEL_WIDTH, height);
        gtk_window_resize(GTK_WINDOW(arg->win), PANEL_WIDTH, height);
        reposition_overlay(arg->win, PANEL_WIDTH, height);
    }

    gtk_widget_queue_draw(od->drawing_area);

done:
    g_free(arg->text);
    g_free(arg->status);
    g_free(arg);
    return G_SOURCE_REMOVE;
}

void overlay_present_async(GtkWidget *win, int state, const char *text,
                           const char *status, int provisional)
{
    IdleTranscriptArg *arg = g_new0(IdleTranscriptArg, 1);
    arg->win         = win;
    arg->state       = state;
    arg->text        = text   ? g_strdup(text)   : NULL;
    arg->status      = status ? g_strdup(status) : NULL;
    arg->provisional = provisional;
    gdk_threads_add_idle(idle_set_transcript, arg);
}

void overlay_show(GtkWidget *win)
{
    overlay_set_visible_async(win, TRUE);
}

void overlay_hide(GtkWidget *win)
{
    overlay_set_visible_async(win, FALSE);
}
