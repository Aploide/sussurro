package ui

import (
	"sync"

	"github.com/aploide/sussurro/internal/config"
)

// Theme callbacks live outside Manager until the native overlay half adds its
// platform-specific state. Keeping this hook narrow lets Settings apply a
// saved change live without coupling the bridge to an overlay implementation.
var managerThemeCallbacks sync.Map

// SetThemeCallback registers the live appearance callback for this Manager.
// Passing nil removes it.
func (m *Manager) SetThemeCallback(fn func(config.Theme)) {
	if fn == nil {
		managerThemeCallbacks.Delete(m)
		return
	}
	managerThemeCallbacks.Store(m, fn)
}

func (m *Manager) applyTheme(theme config.Theme) {
	callback, ok := managerThemeCallbacks.Load(m)
	if ok {
		callback.(func(config.Theme))(theme)
	}
}
