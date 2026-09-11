//go:build linux

package ui

import (
	"log/slog"

	"github.com/godbus/dbus/v5"
)

// statusNotifierWatcher is the well-known bus name of the StatusNotifier
// watcher, the broker between items (us) and hosts (a panel's tray widget).
// systray registers its item with the watcher and silently stays unhosted
// when no host is registered with it.
const (
	statusNotifierWatcher     = "org.kde.StatusNotifierWatcher"
	statusNotifierWatcherPath = "/StatusNotifierWatcher"
)

// watchTrayHost reports, through onHosted, whether a tray host is showing
// items, and keeps reporting as that changes. systray reports readiness
// unconditionally, so this is the only way to tell a desktop that shows the
// icon from one that never will.
//
// The watcher service is not enough: on KDE, kded runs it whether or not any
// panel has a tray widget, so its mere presence hid the overlay for a user
// with no tray at all. What matters is the watcher's
// IsStatusNotifierHostRegistered property, which a panel sets when its tray
// widget registers as a host and clears when the widget is removed. Both the
// watcher appearing (a panel started after Sussurro) and hosts registering or
// unregistering are followed for the life of the process.
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

	// Subscribe before the first probe, so a change between the probe and
	// the match being installed is not missed.
	subscribed := true
	for _, match := range [][]dbus.MatchOption{
		{
			dbus.WithMatchObjectPath("/org/freedesktop/DBus"),
			dbus.WithMatchInterface("org.freedesktop.DBus"),
			dbus.WithMatchSender("org.freedesktop.DBus"),
			dbus.WithMatchMember("NameOwnerChanged"),
			dbus.WithMatchArg(0, statusNotifierWatcher),
		},
		{
			dbus.WithMatchObjectPath(statusNotifierWatcherPath),
			dbus.WithMatchInterface(statusNotifierWatcher),
			dbus.WithMatchMember("StatusNotifierHostRegistered"),
		},
		{
			dbus.WithMatchObjectPath(statusNotifierWatcherPath),
			dbus.WithMatchInterface(statusNotifierWatcher),
			dbus.WithMatchMember("StatusNotifierHostUnregistered"),
		},
	} {
		if err := conn.AddMatchSignal(match...); err != nil {
			slog.Debug("Tray host watch unavailable", "error", err)
			subscribed = false
			break
		}
	}

	hosted := trayHostPresent(conn)
	onHosted(hosted)
	if !subscribed {
		return
	}

	signals := make(chan *dbus.Signal, 16)
	conn.Signal(signals)
	go func() {
		defer conn.RemoveSignal(signals)
		for sig := range signals {
			if sig == nil {
				continue
			}
			switch sig.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				if len(sig.Body) < 3 {
					continue
				}
				if name, _ := sig.Body[0].(string); name != statusNotifierWatcher {
					continue
				}
			case statusNotifierWatcher + ".StatusNotifierHostRegistered",
				statusNotifierWatcher + ".StatusNotifierHostUnregistered":
			default:
				continue
			}
			// Re-probe rather than trust the signal: a watcher that just
			// appeared may or may not have a host yet, and Unregistered can
			// leave other hosts behind.
			if now := trayHostPresent(conn); now != hosted {
				hosted = now
				onHosted(hosted)
			}
		}
	}()
}

// trayHostPresent reports whether a tray host is registered with the watcher.
// A missing watcher, or one without the property, counts as no host.
func trayHostPresent(conn *dbus.Conn) bool {
	variant, err := conn.Object(statusNotifierWatcher, statusNotifierWatcherPath).
		GetProperty(statusNotifierWatcher + ".IsStatusNotifierHostRegistered")
	if err != nil {
		slog.Debug("Tray host probe failed", "error", err)
		return false
	}
	hosted, _ := variant.Value().(bool)
	return hosted
}
