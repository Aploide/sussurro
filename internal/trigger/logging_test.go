package trigger

import (
	"os"
	"strings"
	"testing"
)

func TestTriggerLayerDoesNotLogPipelineStateChanges(t *testing.T) {
	source, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		`s.log.Info("Recording started")`,
		`s.log.Info("Recording stopped - processing...")`,
	} {
		if strings.Contains(string(source), forbidden) {
			t.Errorf("server.go contains pipeline-owned state log %s", forbidden)
		}
	}
}
