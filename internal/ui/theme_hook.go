package ui

import "github.com/aploide/sussurro/internal/config"

// SetThemeCallback registers the live appearance callback for this Manager.
// Passing nil removes it.
func (m *Manager) SetThemeCallback(fn func(config.Theme)) {
	m.themeMu.Lock()
	m.themeCallback = fn
	m.themeMu.Unlock()
}

func (m *Manager) applyTheme(theme config.Theme) {
	m.themeMu.RLock()
	callback := m.themeCallback
	m.themeMu.RUnlock()
	if callback != nil {
		callback(theme)
	}
}
