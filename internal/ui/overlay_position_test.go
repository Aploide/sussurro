package ui

import (
	"os"
	"strings"
	"testing"
)

func TestLinuxOverlayRepositionsBeforeEveryShow(t *testing.T) {
	body, err := os.ReadFile("overlay_linux.c")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	initialHeight := "od->panel_height  = OVERLAY_REST_HEIGHT;"
	if !strings.Contains(source, initialHeight) {
		t.Error("Linux overlay does not initialize the height used by its first show")
	}

	start := strings.Index(source, "static gboolean idle_set_visible")
	if start < 0 {
		t.Fatal("cannot locate Linux overlay visibility callback")
	}
	end := strings.Index(source[start:], "static void overlay_set_visible_async")
	if end < 0 {
		t.Fatal("cannot locate end of Linux overlay visibility callback")
	}
	callback := source[start : start+end]
	showEnd := strings.Index(callback, "} else {")
	if showEnd < 0 {
		t.Fatal("cannot locate Linux overlay show branch")
	}
	showBranch := callback[:showEnd]
	position := "reposition_overlay(arg->win, PANEL_WIDTH, od->panel_height);"
	if !strings.Contains(showBranch, position) {
		t.Error("Linux overlay does not refresh its monitor position before showing")
	}
	if strings.Index(showBranch, position) > strings.Index(showBranch, "gtk_widget_show_all") {
		t.Error("Linux overlay is repositioned only after it becomes visible")
	}
}
