package ui

import "testing"

// TestScaleDimensionMeetsRequirement checks the device size divides back to at
// least the CSS requirement. Rounding to nearest can land a pixel short, which
// is enough to crop a control that fits exactly.
//
// Above roughly 1.64 the requirement stops being satisfiable: 793 CSS px at
// 1.75 scaling needs 1388 device px, wider than the 1366 display the cap
// protects. There the cap deliberately wins, because a window larger than the
// screen cannot be used at all, whereas a narrow one still scrolls. Those
// scales are covered by TestScaleDimensionPrefersFittingTheScreen.
func TestScaleDimensionMeetsRequirement(t *testing.T) {
	scales := []float64{1, 1.25, 1.3333, 1.5}
	for _, scale := range scales {
		device := scaleDimension(settingsContentWidth, scale, maxSettingsWidth)
		if float64(device) > float64(maxSettingsWidth) {
			t.Fatalf("scale %.4f: test scale should not be capped", scale)
		}
		if got := float64(device) / scale; got < settingsContentWidth {
			t.Errorf("scale %.4f: device %d yields %.2f CSS px, want >= %d",
				scale, device, got, settingsContentWidth)
		}
	}
}

// TestScaleDimensionPrefersFittingTheScreen covers the scales where the
// content requirement and the display cap conflict.
func TestScaleDimensionPrefersFittingTheScreen(t *testing.T) {
	for _, scale := range []float64{1.75, 2, 3} {
		if got := scaleDimension(settingsContentWidth, scale, maxSettingsWidth); got != maxSettingsWidth {
			t.Errorf("scale %.2f: width = %d, want it capped at %d so the window fits the screen",
				scale, got, maxSettingsWidth)
		}
	}
}

func TestSettingsSizeForContentAdaptsHeightAndKeepsWidthUsable(t *testing.T) {
	width, short := settingsSizeForContent(640, 120, 1)
	if width != settingsContentWidth {
		t.Errorf("narrow width = %d, want minimum %d", width, settingsContentWidth)
	}
	if want := settingsMinContentHeight + settingsChrome; short != want {
		t.Errorf("short height = %d, want minimum %d", short, want)
	}

	wide, tall := settingsSizeForContent(980, 700, 1)
	if wide != 980 {
		t.Errorf("wide viewport = %d, want 980", wide)
	}
	if tall != 700+settingsChrome {
		t.Errorf("tall height = %d, want %d", tall, 700+settingsChrome)
	}
	if tall <= short {
		t.Errorf("tall content height %d did not grow beyond short height %d", tall, short)
	}

	_, capped := settingsSizeForContent(980, 5000, 1)
	if capped != maxSettingsHeight {
		t.Errorf("oversized height = %d, want cap %d", capped, maxSettingsHeight)
	}

	_, scaled := settingsSizeForContent(980, 400, 1.5)
	if want := 600 + settingsChrome; scaled != want {
		t.Errorf("scaled height = %d, want CSS content scaled but native chrome unchanged: %d", scaled, want)
	}
}

func TestScaleDimensionCapsForSmallDisplays(t *testing.T) {
	// A 1366x768 laptop at high scaling must still get a window that fits.
	if got := scaleDimension(settingsContentWidth, 3, maxSettingsWidth); got != maxSettingsWidth {
		t.Errorf("width = %d, want the cap %d", got, maxSettingsWidth)
	}
	if got := scaleDimension(settingsContentHeight+settingsChrome, 3, maxSettingsHeight); got != maxSettingsHeight {
		t.Errorf("height = %d, want the cap %d", got, maxSettingsHeight)
	}
}

func TestSettingsWindowFitsCommonDisplays(t *testing.T) {
	// The cap must not exceed the working area of a display people actually
	// use. 1366x768 is the smallest common laptop, but its 768 height cannot
	// hold the content the Models tab needs (600 CSS px, 800 device px at
	// 1.33 scaling) once a title bar and panel are allowed for. Scaling is
	// also rare on such a display, so the unscaled 600 fits it comfortably.
	//
	// The cap therefore guards against a scaled window growing past a
	// 1920x1080 screen, and the small-laptop case is covered by the content
	// height fitting unscaled.
	const (
		commonWidth  = 1920
		commonHeight = 1080
	)
	if maxSettingsWidth > commonWidth {
		t.Errorf("maxSettingsWidth = %d, wider than a %d display", maxSettingsWidth, commonWidth)
	}
	if maxSettingsHeight > commonHeight {
		t.Errorf("maxSettingsHeight = %d, taller than a %d display", maxSettingsHeight, commonHeight)
	}

	// Unscaled, the content must fit a small laptop with room for chrome.
	const smallLaptopHeight = 768
	if settingsContentHeight+settingsChrome > smallLaptopHeight {
		t.Errorf("content %d+%d does not fit a %d display unscaled",
			settingsContentHeight, settingsChrome, smallLaptopHeight)
	}
}

// TestUnscaledDisplayGetsContentSize guards the common case: with no display
// scaling the window is sized in content terms directly.
func TestUnscaledDisplayGetsContentSize(t *testing.T) {
	if got := scaleDimension(settingsContentWidth, 1, maxSettingsWidth); got != settingsContentWidth {
		t.Errorf("width = %d, want %d", got, settingsContentWidth)
	}
}
