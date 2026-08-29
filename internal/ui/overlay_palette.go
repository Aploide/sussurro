package ui

import "github.com/aploide/sussurro/internal/config"

// overlayColor is an unpremultiplied semantic colour passed to native drawing
// code. Native backends may vary their dark palette to preserve their existing
// appearance, while all three use the same light palette.
type overlayColor struct {
	R, G, B, A float64
}

type overlayPalette struct {
	Background  overlayColor
	Border      overlayColor
	Primary     overlayColor
	Secondary   overlayColor
	Provisional overlayColor
	Copied      overlayColor
	Track       overlayColor
	Fill        overlayColor
	Warning     overlayColor
	ShimmerBase overlayColor
	ShimmerPeak overlayColor
}

var lightOverlayPalette = overlayPalette{
	Background:  overlayColor{R: 0.969, G: 0.969, B: 0.969, A: 0.97},
	Border:      overlayColor{R: 0.08, G: 0.08, B: 0.08, A: 0.30},
	Primary:     overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 1.0},
	Secondary:   overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.68},
	Provisional: overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.72},
	Copied:      overlayColor{R: 0.086, G: 0.514, B: 0.231, A: 1.0},
	Track:       overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.18},
	Fill:        overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.58},
	Warning:     overlayColor{R: 0.71, G: 0.22, B: 0.035, A: 1.0},
	ShimmerBase: overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.62},
	ShimmerPeak: overlayColor{R: 0.075, G: 0.075, B: 0.075, A: 0.92},
}

var linuxDarkOverlayPalette = overlayPalette{
	Background:  overlayColor{R: 0.12, G: 0.12, B: 0.12, A: 0.94},
	Border:      overlayColor{R: 1, G: 1, B: 1, A: 0.10},
	Primary:     overlayColor{R: 1, G: 1, B: 1, A: 1},
	Secondary:   overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	Provisional: overlayColor{R: 1, G: 1, B: 1, A: 0.72},
	Copied:      overlayColor{R: 0.188, G: 0.82, B: 0.345, A: 1.0},
	Track:       overlayColor{R: 1, G: 1, B: 1, A: 0.18},
	Fill:        overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	Warning:     overlayColor{R: 1, G: 0.62, B: 0.23, A: 0.95},
	ShimmerBase: overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	ShimmerPeak: overlayColor{R: 1, G: 1, B: 1, A: 0.95},
}

var windowsDarkOverlayPalette = overlayPalette{
	Background:  overlayColor{R: 26.0 / 255, G: 26.0 / 255, B: 26.0 / 255, A: 230.0 / 255},
	Border:      overlayColor{R: 1, G: 1, B: 1, A: 77.0 / 255},
	Primary:     overlayColor{R: 1, G: 1, B: 1, A: 1},
	Secondary:   overlayColor{R: 1, G: 1, B: 1, A: 179.0 / 255},
	Provisional: overlayColor{R: 1, G: 1, B: 1, A: 0.72},
	Copied:      overlayColor{R: 0.188, G: 0.82, B: 0.345, A: 1.0},
	Track:       overlayColor{R: 1, G: 1, B: 1, A: 0.18},
	Fill:        overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	Warning:     overlayColor{R: 1, G: 0.62, B: 0.23, A: 0.95},
	ShimmerBase: overlayColor{R: 1, G: 1, B: 1, A: 179.0 / 255},
	ShimmerPeak: overlayColor{R: 1, G: 1, B: 1, A: 128.0 / 255},
}

var darwinDarkOverlayPalette = overlayPalette{
	Background:  overlayColor{R: 0, G: 0, B: 0, A: 0.28},
	Border:      overlayColor{R: 1, G: 1, B: 1, A: 0.25},
	Primary:     overlayColor{R: 1, G: 1, B: 1, A: 1},
	Secondary:   overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	Provisional: overlayColor{R: 1, G: 1, B: 1, A: 0.72},
	Copied:      overlayColor{R: 0.188, G: 0.82, B: 0.345, A: 1.0},
	Track:       overlayColor{R: 1, G: 1, B: 1, A: 0.18},
	Fill:        overlayColor{R: 1, G: 1, B: 1, A: 0.55},
	Warning:     overlayColor{R: 1, G: 0.62, B: 0.23, A: 0.95},
	ShimmerBase: overlayColor{R: 1, G: 1, B: 1, A: 0.28},
	ShimmerPeak: overlayColor{R: 1, G: 1, B: 1, A: 0.90},
}

// ThemeSetter is the optional appearance extension to Overlay. Keeping it
// optional preserves lightweight test and fallback overlays.
type ThemeSetter interface {
	SetTheme(config.Theme)
}

func setOverlayTheme(overlay Overlay, theme config.Theme) {
	if setter, ok := overlay.(ThemeSetter); ok {
		setter.SetTheme(theme)
	}
}

func (m *Manager) configureOverlayTheme() {
	setOverlayTheme(m.overlay, m.cfg.Appearance.Theme)
	m.SetThemeCallback(func(theme config.Theme) {
		setOverlayTheme(m.overlay, theme)
	})
}

func overlayThemeMode(theme config.Theme) int {
	switch theme {
	case config.ThemeLight:
		return 1
	case config.ThemeDark:
		return 2
	default:
		return 0
	}
}
