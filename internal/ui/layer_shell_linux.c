#include "layer_shell_linux.h"

#include <dlfcn.h>

/* ABI constants copied from gtk-layer-shell.h so the header is not a build
   dependency. They are part of the library's public ABI and stable. */
enum { LS_LAYER_OVERLAY = 3 };
enum { LS_EDGE_LEFT = 0, LS_EDGE_RIGHT = 1, LS_EDGE_TOP = 2, LS_EDGE_BOTTOM = 3 };
enum { LS_KEYBOARD_MODE_NONE = 0 };

typedef gboolean (*ls_is_supported_fn)(void);
typedef void (*ls_init_for_window_fn)(GtkWindow *);
typedef void (*ls_set_layer_fn)(GtkWindow *, int);
typedef void (*ls_set_anchor_fn)(GtkWindow *, int, gboolean);
typedef void (*ls_set_margin_fn)(GtkWindow *, int, int);
typedef void (*ls_set_exclusive_zone_fn)(GtkWindow *, int);
typedef void (*ls_set_keyboard_mode_fn)(GtkWindow *, int);
typedef void (*ls_set_namespace_fn)(GtkWindow *, const char *);

static struct {
    int                      probed;    /* 0 = not yet, 1 = done */
    gboolean                 available;
    ls_init_for_window_fn    init_for_window;
    ls_set_layer_fn          set_layer;
    ls_set_anchor_fn         set_anchor;
    ls_set_margin_fn         set_margin;
    ls_set_exclusive_zone_fn set_exclusive_zone;
    ls_set_keyboard_mode_fn  set_keyboard_mode;
    ls_set_namespace_fn      set_namespace;
} ls;

static void *ls_sym(void *handle, const char *name)
{
    void *fn = dlsym(handle, name);
    if (!fn) g_warning("gtk-layer-shell: missing symbol %s; overlay falls back to a floating window", name);
    return fn;
}

gboolean layer_shell_available(void)
{
    if (ls.probed) return ls.available;
    ls.probed = 1;

    /* The SONAME is used on purpose: the unversioned .so only exists with
       the -dev/-devel package installed. RTLD_GLOBAL matches what a normal
       link would do; the library sits next to GTK in the symbol namespace. */
    void *h = dlopen("libgtk-layer-shell.so.0", RTLD_NOW | RTLD_GLOBAL);
    if (!h) {
        g_message("gtk-layer-shell not installed; overlay uses a floating window "
                  "(install the gtk-layer-shell / libgtk-layer-shell0 package for a true Wayland overlay)");
        return FALSE;
    }

    ls_is_supported_fn is_supported = (ls_is_supported_fn)ls_sym(h, "gtk_layer_is_supported");
    ls.init_for_window    = (ls_init_for_window_fn)   ls_sym(h, "gtk_layer_init_for_window");
    ls.set_layer          = (ls_set_layer_fn)         ls_sym(h, "gtk_layer_set_layer");
    ls.set_anchor         = (ls_set_anchor_fn)        ls_sym(h, "gtk_layer_set_anchor");
    ls.set_margin         = (ls_set_margin_fn)        ls_sym(h, "gtk_layer_set_margin");
    ls.set_exclusive_zone = (ls_set_exclusive_zone_fn)ls_sym(h, "gtk_layer_set_exclusive_zone");
    ls.set_keyboard_mode  = (ls_set_keyboard_mode_fn) ls_sym(h, "gtk_layer_set_keyboard_mode");
    ls.set_namespace      = (ls_set_namespace_fn)     ls_sym(h, "gtk_layer_set_namespace");

    if (!is_supported || !ls.init_for_window || !ls.set_layer || !ls.set_anchor ||
        !ls.set_margin || !ls.set_exclusive_zone || !ls.set_keyboard_mode || !ls.set_namespace) {
        return FALSE;
    }

    /* X11, or Wayland with a compositor that lacks wlr-layer-shell (GNOME's
       Mutter, for instance): the floating-window fallback is the only option.
       Checking first also keeps gtk_layer_init_for_window() away from X11
       windows, which the old unconditional call did not. */
    if (!is_supported()) {
        g_message("display is not a Wayland compositor with wlr-layer-shell; overlay uses a floating window");
        return FALSE;
    }

    ls.available = TRUE;
    return TRUE;
}

void layer_shell_setup_overlay(GtkWindow *win, int bottom_margin)
{
    if (!layer_shell_available()) return;

    ls.init_for_window(win);
    ls.set_layer(win, LS_LAYER_OVERLAY);
    ls.set_anchor(win, LS_EDGE_BOTTOM, TRUE);
    ls.set_anchor(win, LS_EDGE_LEFT,   FALSE);
    ls.set_anchor(win, LS_EDGE_RIGHT,  FALSE);
    ls.set_margin(win, LS_EDGE_BOTTOM, bottom_margin);
    ls.set_exclusive_zone(win, -1);
    ls.set_keyboard_mode(win, LS_KEYBOARD_MODE_NONE);
    ls.set_namespace(win, "sussurro");
}
