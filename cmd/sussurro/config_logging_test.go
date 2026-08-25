package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aploide/sussurro/internal/config"
	"github.com/spf13/viper"
)

func TestEffectiveConfigurationLogNamesLoadedFileAndAudioLimits(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	path := filepath.Join(t.TempDir(), "custom.yaml")
	if err := os.WriteFile(path, []byte(`audio:
  max_duration: 2m
  min_duration: 450ms
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	maxDuration, minDuration, entry := captureEffectiveConfiguration(t, cfg)
	if maxDuration != "2m" || minDuration != "450ms" {
		t.Fatalf("resolved durations = %q, %q", maxDuration, minDuration)
	}
	assertLogFields(t, entry, map[string]string{
		"config_path":  path,
		"max_duration": "2m",
		"min_duration": "450ms",
	})
}

func TestEffectiveConfigurationLogResolvesPipelineFallbacks(t *testing.T) {
	tests := []struct {
		name, configuredMax, configuredMin, wantMax, wantMin string
	}{
		{name: "missing", wantMax: "30s", wantMin: "300ms"},
		{name: "invalid", configuredMax: "later", configuredMin: "briefly", wantMax: "30s", wantMin: "300ms"},
		{name: "non-positive minimum", configuredMax: "1m", configuredMin: "0", wantMax: "1m", wantMin: "300ms"},
		{name: "unlimited maximum", configuredMax: "0", configuredMin: "1s", wantMax: "infinite", wantMin: "1s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Audio: config.AudioConfig{
				MaxDuration: tt.configuredMax,
				MinDuration: tt.configuredMin,
			}}
			maxDuration, minDuration, entry := captureEffectiveConfiguration(t, cfg)
			if maxDuration != tt.wantMax || minDuration != tt.wantMin {
				t.Fatalf("resolved durations = %q, %q; want %q, %q",
					maxDuration, minDuration, tt.wantMax, tt.wantMin)
			}
			assertLogFields(t, entry, map[string]string{
				"max_duration": tt.wantMax,
				"min_duration": tt.wantMin,
			})
		})
	}
}

func captureEffectiveConfiguration(t *testing.T, cfg *config.Config) (string, string, map[string]any) {
	t.Helper()
	var output bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&output, nil))
	maxDuration, minDuration := logEffectiveConfiguration(log, cfg)

	scanner := bufio.NewScanner(&output)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "Effective configuration" {
			return maxDuration, minDuration, entry
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("Effective configuration log entry not found")
	return "", "", nil
}

func assertLogFields(t *testing.T, entry map[string]any, fields map[string]string) {
	t.Helper()
	for key, want := range fields {
		if got := entry[key]; got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
}
