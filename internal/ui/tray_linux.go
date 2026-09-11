//go:build linux

package ui

import (
	"log/slog"

	"github.com/godbus/dbus/v5"
)

// statusNotifierWatcher is the well-known bus name a StatusNotifier host
// (KDE, GNOME with the AppIndicator extension, waybar, ...) owns. systray
// registers its item with whoever owns it and silently stays unhosted when
// nobody does.
const statusNotifierWatcher = "org.kde.StatusNotifierWatcher"

// watchTrayHost reports, through onHosted, whether a StatusNotifier host owns
// the watcher name on the session bus. systray reports readiness
// unconditionally, so this is the only way to tell a desktop that shows the
// icon from one that never will.
//
// The first answer is immediate. When it is negative the bus is watched for
// the name gaining an owner — a panel started alongside Sussurro at login
// registers its watcher seconds after this runs, and systray itself registers
// the icon then — and onHosted(true) is called once, when it does.
//
// The shared session connection is used and left open: systray holds the same
// one, and closing it would tear the tray down.
func watchTrayHost(onHosted func(bool)) {
	conn, err := dbus.SessionBus()
	if err != nil {
		slog.Debug("Tray host probe: no session bus", "error", err)
		onHosted(false)
		return
	}
	if trayHostPresent(conn) {
		onHosted(true)
		return
	}
	onHosted(false)

	// Subscribe before re-probing, so a host that appears between the probe
	// above and the match being installed is not missed.
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, statusNotifierWatcher),
	); err != nil {
		slog.Debug("Tray host watch unavailable", "error", err)
		return
	}
	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	if trayHostPresent(conn) {
		conn.RemoveSignal(signals)
		onHosted(true)
		return
	}

	go func() {
		defer conn.RemoveSignal(signals)
		for sig := range signals {
			if sig == nil || sig.Name != "org.freedesktop.DBus.NameOwnerChanged" || len(sig.Body) < 3 {
				continue
			}
			name, _ := sig.Body[0].(string)
			newOwner, _ := sig.Body[2].(string)
			if name == statusNotifierWatcher && newOwner != "" {
				slog.Info("System tray host appeared; the overlay no longer needs to stay visible")
				onHosted(true)
				return
			}
		}
	}()
}

// trayHostPresent reports whether a StatusNotifier host owns the watcher name.
func trayHostPresent(conn *dbus.Conn) bool {
	var owned bool
	call := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, statusNotifierWatcher)
	if err := call.Store(&owned); err != nil {
		slog.Debug("Tray host probe failed", "error", err)
		return false
	}
	return owned
}
