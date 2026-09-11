#pragma once

#include <gtk/gtk.h>

/* gtk-layer-shell is loaded with dlopen() at runtime instead of being linked.
   The prebuilt release binary runs on machines that never installed the
   library (it is an optional package on every distro); a DT_NEEDED entry made
   the loader abort with "libgtk-layer-shell.so.0: cannot open shared object
   file" before main() ran. With the runtime probe the overlay becomes a real
   wlr-layer-shell surface when the library *and* a supporting compositor are
   present, and a plain floating window otherwise. */

/* TRUE when libgtk-layer-shell.so.0 loaded and gtk_layer_is_supported()
   reports a Wayland compositor that speaks the protocol. Cached after the
   first call; must run after gtk_init(). */
gboolean layer_shell_available(void);

/* Turns `win` into a bottom-centred overlay layer surface with the given
   bottom margin. Call before the window is realized. No-op unless
   layer_shell_available(). */
void layer_shell_setup_overlay(GtkWindow *win, int bottom_margin);
