//go:build windows

package clipboard

import (
	"fmt"

	"github.com/atotto/clipboard"
)

// Write copies the provided string to the system clipboard
func Write(text string) error {
	if err := clipboard.WriteAll(text); err != nil {
		return fmt.Errorf("clipboard failed: %w", err)
	}
	return nil
}

// Read returns the current string content of the system clipboard
func Read() (string, error) {
	text, err := clipboard.ReadAll()
	if err != nil {
		return "", fmt.Errorf("clipboard failed: %w", err)
	}
	return text, nil
}
