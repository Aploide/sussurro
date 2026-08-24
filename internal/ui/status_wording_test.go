package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProductionSourcesDoNotRenderTranscribing guards every rendering layer,
// including the macOS and Windows sources that the Linux test run cannot
// compile. This structural invariant is deliberate: path-by-path fixes let the
// same user-visible word return three times under sussurro-xvj.34.
func TestProductionSourcesDoNotRenderTranscribing(t *testing.T) {
	roots := []string{".", "../pipeline", "../../cmd/sussurro"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(content)
			for _, literal := range []string{`"Transcribing`, `"transcribing"`} {
				if strings.Contains(text, literal) {
					t.Errorf("%s contains forbidden user-visible literal %q", path, literal)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan production sources under %s: %v", root, err)
		}
	}
}
