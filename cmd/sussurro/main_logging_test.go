package main

import (
	"os"
	"strings"
	"testing"
)

func TestHotkeyLayerDoesNotLogPipelineStateChanges(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		`log.Info("Listening...")`,
		`log.Info("Recording stopped")`,
		`log.Info("Listening for edit...")`,
		`log.Info("Edit recording stopped")`,
	} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("main.go contains pipeline-owned state log %s", forbidden)
		}
	}
}
