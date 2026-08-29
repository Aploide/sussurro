package ui

import (
	"os"
	"strings"
	"testing"
)

// These source checks keep platform-tagged implementations in the shared
// binding contract. Linux CI cannot compile the Cocoa or Win32 files.
func TestEveryNativePlatformInstallsEditBinding(t *testing.T) {
	for _, file := range []string{"app_darwin.go", "app_windows.go", "overlay_linux.go"} {
		t.Run(file, func(t *testing.T) {
			body, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("reading %s: %v", file, err)
			}
			if !strings.Contains(string(body), "bindings.Edit") {
				t.Errorf("%s does not install the edit binding", file)
			}
		})
	}
}

func TestLinuxHotkeyReplacementReleasesOldGrabsOnce(t *testing.T) {
	body, err := os.ReadFile("overlay_linux.c")
	if err != nil {
		t.Fatalf("reading overlay_linux.c: %v", err)
	}
	source := string(body)
	if !strings.Contains(source, "XUngrabKey") {
		t.Error("Linux re-registration does not release old X11 grabs")
	}
	if !strings.Contains(source, "hotkey_filter_installed") {
		t.Error("Linux re-registration has no duplicate-filter guard")
	}
	if !strings.Contains(source, "g_main_context_invoke") {
		t.Error("Linux re-registration is not marshalled to the GTK main context")
	}
}

func TestLinuxRoutingUsesExactModifiersAndPressedOwner(t *testing.T) {
	body, err := os.ReadFile("overlay_linux.c")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{
		"effective_modifiers", "od->hk_mods == mods", "od->tg_mods == mods",
		"od->ed_mods == mods", "pressed_owner", "release_pressed_binding",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("X11 routing is missing %q", required)
		}
	}
	if strings.Contains(source, "(xe->xkey.state & od->hk_mods) == od->hk_mods") {
		t.Error("X11 routing still accepts modifier supersets")
	}
}

func TestWindowsReplacementRetainsLiveOwnership(t *testing.T) {
	body, err := os.ReadFile("app_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	for _, required := range []string{
		"stopOnce", "loop.stop", "PostThreadMessageW failed; retaining live hotkey ownership",
		"stillActive = append(stillActive, loop)",
	} {
		if !strings.Contains(source, required) {
			t.Errorf("Windows lifecycle is missing %q", required)
		}
	}
}
