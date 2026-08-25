package ui

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aploide/sussurro/internal/config"
)

func paletteColors(p overlayPalette) map[string]overlayColor {
	return map[string]overlayColor{
		"background": p.Background, "border": p.Border, "primary": p.Primary,
		"secondary": p.Secondary, "provisional": p.Provisional, "track": p.Track,
		"fill": p.Fill, "warning": p.Warning, "shimmer base": p.ShimmerBase,
		"shimmer peak": p.ShimmerPeak,
	}
}

func TestOverlayPalettesAreComplete(t *testing.T) {
	palettes := map[string]overlayPalette{
		"light": lightOverlayPalette, "linux dark": linuxDarkOverlayPalette,
		"windows dark": windowsDarkOverlayPalette, "darwin dark": darwinDarkOverlayPalette,
	}
	for paletteName, palette := range palettes {
		for role, color := range paletteColors(palette) {
			if color.A <= 0 || color.A > 1 || color.R < 0 || color.R > 1 ||
				color.G < 0 || color.G > 1 || color.B < 0 || color.B > 1 {
				t.Errorf("%s %s = %+v, want complete normalized RGBA", paletteName, role, color)
			}
		}
	}
}

func composite(foreground, background overlayColor) overlayColor {
	a := foreground.A + background.A*(1-foreground.A)
	if a == 0 {
		return overlayColor{}
	}
	return overlayColor{
		R: (foreground.R*foreground.A + background.R*background.A*(1-foreground.A)) / a,
		G: (foreground.G*foreground.A + background.G*background.A*(1-foreground.A)) / a,
		B: (foreground.B*foreground.A + background.B*background.A*(1-foreground.A)) / a,
		A: a,
	}
}

func channelLuminance(value float64) float64 {
	if value <= 0.04045 {
		return value / 12.92
	}
	return math.Pow((value+0.055)/1.055, 2.4)
}

func contrastRatio(a, b overlayColor) float64 {
	la := 0.2126*channelLuminance(a.R) + 0.7152*channelLuminance(a.G) + 0.0722*channelLuminance(a.B)
	lb := 0.2126*channelLuminance(b.R) + 0.7152*channelLuminance(b.G) + 0.0722*channelLuminance(b.B)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestLightOverlayTextAndWaveformContrastAcrossDesktopBackdrops(t *testing.T) {
	backdrops := map[string]overlayColor{
		"light desktop": {R: 1, G: 1, B: 1, A: 1},
		"dark desktop":  {R: 0.03, G: 0.03, B: 0.03, A: 1},
	}
	for name, backdrop := range backdrops {
		background := composite(lightOverlayPalette.Background, backdrop)
		for role, foreground := range map[string]overlayColor{
			"text":             lightOverlayPalette.Primary,
			"waveform":         lightOverlayPalette.Primary,
			"status":           lightOverlayPalette.Secondary,
			"provisional text": lightOverlayPalette.Provisional,
			"shimmer base":     lightOverlayPalette.ShimmerBase,
		} {
			painted := composite(foreground, background)
			minimum := 4.5
			if role == "text" || role == "waveform" {
				minimum = 7
			}
			if ratio := contrastRatio(painted, background); ratio < minimum {
				t.Errorf("%s on %s contrast = %.2f, want at least %.1f:1", role, name, ratio, minimum)
			}
		}
	}
}

type themeRecordingOverlay struct {
	themes []config.Theme
}

func (*themeRecordingOverlay) Show()             {}
func (*themeRecordingOverlay) Hide()             {}
func (*themeRecordingOverlay) SetState(AppState) {}
func (*themeRecordingOverlay) PushRMS(float32)   {}
func (*themeRecordingOverlay) Close()            {}
func (o *themeRecordingOverlay) SetTheme(theme config.Theme) {
	o.themes = append(o.themes, theme)
}

type noThemeOverlay struct{}

func (*noThemeOverlay) Show()             {}
func (*noThemeOverlay) Hide()             {}
func (*noThemeOverlay) SetState(AppState) {}
func (*noThemeOverlay) PushRMS(float32)   {}
func (*noThemeOverlay) Close()            {}

func TestSetOverlayThemeUsesOptionalExtension(t *testing.T) {
	overlay := &themeRecordingOverlay{}
	setOverlayTheme(overlay, config.ThemeLight)
	if len(overlay.themes) != 1 || overlay.themes[0] != config.ThemeLight {
		t.Fatalf("forwarded themes = %v, want [light]", overlay.themes)
	}

	setOverlayTheme(&noThemeOverlay{}, config.ThemeDark)
}

func TestManagerConfiguresInitialAndLiveOverlayTheme(t *testing.T) {
	overlay := &themeRecordingOverlay{}
	manager := &Manager{
		cfg:     &config.Config{Appearance: config.AppearanceConfig{Theme: config.ThemeLight}},
		overlay: overlay,
	}
	manager.configureOverlayTheme()
	manager.applyTheme(config.ThemeDark)

	want := []config.Theme{config.ThemeLight, config.ThemeDark}
	if len(overlay.themes) != len(want) || overlay.themes[0] != want[0] || overlay.themes[1] != want[1] {
		t.Fatalf("configured themes = %v, want %v", overlay.themes, want)
	}
}

func TestOverlayThemeMode(t *testing.T) {
	for _, test := range []struct {
		theme config.Theme
		want  int
	}{{config.ThemeSystem, 0}, {config.ThemeLight, 1}, {config.ThemeDark, 2}} {
		if got := overlayThemeMode(test.theme); got != test.want {
			t.Errorf("overlayThemeMode(%q) = %d, want %d", test.theme, got, test.want)
		}
	}
}

func TestNativeOverlayPaletteContract(t *testing.T) {
	files := []string{"overlay_linux.c", "overlay_windows.c", "overlay_darwin.m"}
	for _, name := range files {
		body, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		source := string(body)
		if !strings.Contains(source, `#include "overlay_palette.h"`) ||
			!strings.Contains(source, "OverlayPalette") {
			t.Errorf("%s does not include and use the shared native palette", name)
		}
	}

	header := readOverlaySource(t, "overlay_palette.h")
	if regexp.MustCompile(`(?m)^\s*#define\s+`).MatchString(header) {
		t.Error("overlay_palette.h contains constants; Go must own every colour value")
	}

	linux := readOverlaySource(t, "overlay_linux.c")
	if got := strings.Count(linux, "cairo_set_source_rgba("); got != 2 ||
		!strings.Contains(linux, "cairo_set_source_rgba(cr, color.r, color.g, color.b") ||
		!strings.Contains(linux, "cairo_set_source_rgba(cr, 0, 0, 0, 0)") {
		t.Error("overlay_linux.c has a visible Cairo colour outside the palette helper and structural clear")
	}

	windows := readOverlaySource(t, "overlay_windows.c")
	if regexp.MustCompile(`(?i)0x[0-9a-f]{6,8}`).MatchString(windows) ||
		regexp.MustCompile(`(?m)^\s*#define\s+\w*(?:COLOR|ARGB|BG_|BORDER_)`).MatchString(windows) {
		t.Error("overlay_windows.c has a native colour literal or macro")
	}
	for _, line := range strings.Split(windows, "\n") {
		if (strings.Contains(line, "GdipCreateSolidFill(") || strings.Contains(line, "GdipCreatePen1(")) &&
			!strings.Contains(line, "overlay_argb(") {
			t.Errorf("overlay_windows.c bypasses palette conversion: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(windows, "GdipGraphicsClear(od->gfx, 0); /* structural transparent clear */") {
		t.Error("overlay_windows.c does not distinguish its structural transparent clear")
	}

	darwin := readOverlaySource(t, "overlay_darwin.m")
	if !strings.Contains(darwin, "effect.appearance = nil;") ||
		!strings.Contains(darwin, "viewDidChangeEffectiveAppearance") {
		t.Error("overlay_darwin.m forces an appearance in System mode, which blocks live system changes")
	}
	if strings.Count(darwin, "CGContextSetRGBFillColor(") != 1 ||
		strings.Count(darwin, "CGContextSetRGBStrokeColor(") != 1 ||
		strings.Contains(darwin, "colorWithWhite:") || strings.Contains(darwin, "whiteColor") {
		t.Error("overlay_darwin.m has a visible colour outside its palette helpers")
	}
	if strings.Count(darwin, "CGContextClearRect(") != 1 ||
		strings.Count(darwin, "[NSColor clearColor]") != 1 {
		t.Error("overlay_darwin.m structural transparent clears changed or became visible colours")
	}
}

func TestNativeThemeWatchersKeepSystemModeLive(t *testing.T) {
	linux := readOverlaySource(t, "overlay_linux.c")
	subscribe := strings.Index(linux, "g_dbus_connection_signal_subscribe(")
	read := strings.Index(linux, "g_dbus_connection_call(")
	if subscribe < 0 || read < 0 || subscribe > read || strings.Contains(linux, "g_dbus_connection_call_sync(") {
		t.Error("Linux must subscribe before reading the portal preference asynchronously")
	}

	windows := readOverlaySource(t, "overlay_windows.c")
	if !strings.Contains(windows, "read_system_dark_apps(BOOL *dark)") ||
		!strings.Contains(windows, "if (read_system_dark_apps(&dark))") ||
		!strings.Contains(windows, "od->system_dark   = TRUE; /* preserve the existing dark UI when unknown */") {
		t.Error("Windows must distinguish an unknown registry value from an explicit light preference")
	}

	darwin := readOverlaySource(t, "overlay_darwin.m")
	if !strings.Contains(darwin, "[NSApplication sharedApplication]") ||
		!strings.Contains(darwin, "effect.appearance = nil;") {
		t.Error("macOS must initialize AppKit and inherit appearance in System mode")
	}

	mainSource, err := os.ReadFile(filepath.Join("..", "..", "cmd", "sussurro", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainSource), "runtime.LockOSThread()") {
		t.Error("UI startup is not pinned to the OS thread that owns native windows")
	}
}

func readOverlaySource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
