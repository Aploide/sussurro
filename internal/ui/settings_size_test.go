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

func TestScaleDimensionCapsForSmallDisplays(t *testing.T) {
	// A 1366x768 laptop at high scaling must still get a window that fits.
	if got := scaleDimension(settingsContentWidth, 3, maxSettingsWidth); got != maxSettingsWidth {
		t.Errorf("width = %d, want the cap %d", got, maxSettingsWidth)
	}
	if got := scaleDimension(settingsContentHeight+settingsChrome, 3, maxSettingsHeight); got != maxSettingsHeight {
		t.Errorf("height = %d, want the cap %d", got, maxSettingsHeight)
	}
}

func TestSettingsWindowFitsSmallDisplay(t *testing.T) {
	const (
		smallWidth  = 1366
		smallHeight = 768
	)
	if maxSettingsWidth > smallWidth {
		t.Errorf("maxSettingsWidth = %d, wider than a %d display", maxSettingsWidth, smallWidth)
	}
	if maxSettingsHeight > smallHeight {
		t.Errorf("maxSettingsHeight = %d, taller than a %d display", maxSettingsHeight, smallHeight)
	}
}

// TestUnscaledDisplayGetsContentSize guards the common case: with no display
// scaling the window is sized in content terms directly.
func TestUnscaledDisplayGetsContentSize(t *testing.T) {
	if got := scaleDimension(settingsContentWidth, 1, maxSettingsWidth); got != settingsContentWidth {
		t.Errorf("width = %d, want %d", got, settingsContentWidth)
	}
}
