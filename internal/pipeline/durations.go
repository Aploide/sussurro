package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// DefaultMaxDuration is the safety cap used when max_duration is absent or invalid.
const DefaultMaxDuration = "2m"

// ResolveMaxDuration returns the limit the pipeline will enforce. A configured
// zero retains the existing unlimited behavior.
func ResolveMaxDuration(value string) (string, error) {
	if strings.EqualFold(value, "infinite") || value == "0" {
		return "infinite", nil
	}
	if value == "" {
		return DefaultMaxDuration, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return DefaultMaxDuration, err
	}
	if duration <= 0 {
		return DefaultMaxDuration, fmt.Errorf("must be positive")
	}
	return value, nil
}
